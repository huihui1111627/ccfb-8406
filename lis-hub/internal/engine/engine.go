package engine

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lis-hub/internal/model"
	"lis-hub/internal/parser"
	"lis-hub/internal/store"
)

type Engine struct {
	st *store.Store
}

func New(st *store.Store) *Engine { return &Engine{st: st} }

// IngestResult 单条结果的处理结果
type IngestOutcome struct {
	Action   string `json:"action"` // inserted | duplicate | corrected | failed
	ResultID int64  `json:"result_id"`
	Warning  string `json:"warning,omitempty"`
}

// Ingest 处理一条已解析报文: 标准化 -> 去重/更正 -> 危急值 -> 质控
func (e *Engine) Ingest(raw []byte, source, protocol string) (rawID int64, outcomes []IngestOutcome, err error) {
	var pm *model.ParsedMessage
	var parseErr error
	switch protocol {
	case "hl7":
		pm, parseErr = parser.ParseHL7(raw)
	case "astm":
		pm, parseErr = parser.ParseASTM(raw)
	default:
		return 0, nil, fmt.Errorf("不支持的协议: %s", protocol)
	}
	if pm == nil {
		pm = &model.ParsedMessage{Protocol: protocol}
	}
	if pm.Source != "" && source == "" {
		source = pm.Source
	}
	if source == "" {
		source = "unknown"
	}

	// 原始报文无论如何先落库(任何结果可追回原始报文; 解析失败也要留底)
	text, enc := parser.DetectEncoding(raw)
	h := sha1.Sum([]byte(protocol + "|" + source + "|" + text))
	rm := &model.RawMessage{
		ReceivedAt:  time.Now(),
		Source:      source,
		Protocol:    protocol,
		Content:     text,
		Encoding:    enc,
		ParseStatus: "ok",
		MsgHash:     hex.EncodeToString(h[:]),
	}
	if parseErr != nil {
		rm.ParseStatus = "failed"
		rm.ParseError = parseErr.Error()
	} else if len(pm.Warnings) > 0 {
		rm.ParseStatus = "partial"
		rm.ParseError = strings.Join(pm.Warnings, "; ")
	}
	rawID, inserted, err := e.st.InsertRawMessage(rm)
	if err != nil {
		return 0, nil, err
	}
	if !inserted {
		return rawID, []IngestOutcome{{Action: "duplicate", Warning: "相同报文已接收过, 整体去重"}}, nil
	}
	if parseErr != nil {
		return rawID, nil, nil // 解析失败, 仅留底
	}

	for _, pr := range pm.Results {
		outcomes = append(outcomes, e.ingestOne(rawID, source, pr))
	}
	return rawID, outcomes, nil
}

// IngestParsed Excel 等已解析结构直接入库
func (e *Engine) IngestParsed(pm *model.ParsedMessage, rawText, source string) (int64, []IngestOutcome, error) {
	if source == "" {
		source = pm.Source
	}
	h := sha1.Sum([]byte(pm.Protocol + "|" + source + "|" + rawText))
	rm := &model.RawMessage{
		ReceivedAt:  time.Now(),
		Source:      source,
		Protocol:    pm.Protocol,
		Content:     rawText,
		Encoding:    "utf-8",
		ParseStatus: "ok",
		MsgHash:     hex.EncodeToString(h[:]),
	}
	if len(pm.Warnings) > 0 {
		rm.ParseStatus = "partial"
		rm.ParseError = strings.Join(pm.Warnings, "; ")
	}
	rawID, inserted, err := e.st.InsertRawMessage(rm)
	if err != nil {
		return 0, nil, err
	}
	if !inserted {
		return rawID, []IngestOutcome{{Action: "duplicate", Warning: "相同报文已接收过, 整体去重"}}, nil
	}
	var outcomes []IngestOutcome
	for _, pr := range pm.Results {
		outcomes = append(outcomes, e.ingestOne(rawID, source, pr))
	}
	return rawID, outcomes, nil
}

func (e *Engine) ingestOne(rawID int64, source string, pr model.ParsedResult) IngestOutcome {
	now := time.Now()
	r := &model.Result{
		RawMessageID: rawID,
		Source:       source,
		SampleID:     pr.SampleID,
		PatientMRN:   pr.Patient.MRN,
		PatientName:  pr.Patient.Name,
		PatientSex:   pr.Patient.Sex,
		PatientAge:   pr.Patient.Age,
		SourceCode:   pr.SourceCode,
		SourceName:   pr.SourceName,
		RawValue:     pr.RawValue,
		RawUnit:      pr.RawUnit,
		Flag:         normalizeFlag(pr.InstrFlag),
		Status:       pr.Status,
		Version:      1,
		IsCurrent:    true,
		TestTime:     pr.TestTime,
		ReceivedAt:   now,
		IsQC:         pr.IsQC,
		CreatedAt:    now,
	}
	if r.Status == "" {
		r.Status = "F"
	}
	// 报文给出生日期时换算年龄(用于按年龄取参考范围/危急值规则)
	if r.PatientAge == 0 && pr.Patient.Birth != "" {
		if t := parseBirth(pr.Patient.Birth); !t.IsZero() {
			r.PatientAge = float64(int(now.Sub(t).Hours()/24/365.25*10)) / 10
		}
	}

	// 1. 项目映射 + 单位换算
	m, err := e.st.FindMapping(source, pr.SourceCode, pr.RawUnit)
	if err == nil && m == nil {
		// 自动登记未映射项目, 控制台可维护
		_ = e.st.EnsureMapping(source, pr.SourceCode, pr.SourceName, pr.RawUnit)
	}
	if m != nil && m.AnalyteID != nil {
		r.AnalyteID = m.AnalyteID
		if v, ok := parser.ParseFloat(pr.RawValue); ok {
			cv := v*m.Factor + m.Offset
			r.Value = &cv
		}
		// 标准单位
		if a, err := e.st.GetAnalyte(*m.AnalyteID); err == nil {
			r.Unit = a.CanonicalUnit
		}
	} else if v, ok := parser.ParseFloat(pr.RawValue); ok {
		// 未映射项目: 数值原样保留, 单位按原始单位
		r.Value = &v
		r.Unit = pr.RawUnit
	}

	// 2. 参考范围 + 高低标记(按性别年龄)
	if r.AnalyteID != nil && r.Value != nil {
		if lo, hi, ok, err := e.st.FindRefRange(*r.AnalyteID, r.PatientSex, r.PatientAge); err == nil && ok {
			r.RefLow = &lo
			r.RefHigh = &hi
			if *r.Value < lo {
				r.Flag = "L"
			} else if *r.Value > hi {
				r.Flag = "H"
			} else if r.Flag == "" {
				r.Flag = "N"
			}
		}
	}

	// 3. 更正处理: 状态为 C 时挂到历史链上
	if r.Status == "C" {
		if prev, err := e.st.FindCurrentResult(source, r.SampleID, r.SourceCode); err == nil && prev != nil {
			r.Version = prev.Version + 1
			r.SupersedesID = &prev.ID
		}
	}

	// 4. 去重键: 同一来源同样本同项目同时间同值同状态 = 重复发送
	dedup := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		source, r.SampleID, r.SourceCode, r.TestTime.Format(time.RFC3339), r.RawValue, r.Status)))
	r.DedupKey = hex.EncodeToString(dedup[:])

	id, inserted, err := e.st.InsertResult(r)
	if err != nil {
		return IngestOutcome{Action: "failed", Warning: err.Error()}
	}
	if !inserted {
		return IngestOutcome{Action: "duplicate", Warning: fmt.Sprintf("%s 重复发送已去重", r.SourceCode)}
	}
	action := "inserted"
	if r.SupersedesID != nil {
		_ = e.st.MarkSuperseded(*r.SupersedesID)
		action = "corrected"
	}

	// 5. 危急值
	if r.AnalyteID != nil && r.Value != nil && !r.IsQC {
		if level, err := e.st.MatchCritical(*r.AnalyteID, r.PatientSex, r.PatientAge, *r.Value); err == nil && level != "" {
			_, _ = e.st.CreateCriticalEvent(id, level)
		}
	}

	// 6. 质控: Westgard
	if r.IsQC && r.AnalyteID != nil && r.Value != nil {
		e.runWestgard(source, *r.AnalyteID, pr.QCLot, pr.QCLevel, id, *r.Value, now)
	}

	return IngestOutcome{Action: action, ResultID: id}
}

func parseBirth(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range []string{"20060102", "2006-01-02", "2006/01/02"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func normalizeFlag(f string) string {
	f = strings.ToUpper(strings.TrimSpace(f))
	switch f {
	case "N", "H", "L", "A":
		return f
	case "HH", "LL":
		return f // 仪器级危急标记保留
	case "", "NORMAL":
		return ""
	default:
		return f
	}
}

// runWestgard 对一条 QC 结果执行 Westgard 多规则
func (e *Engine) runWestgard(source string, analyteID int64, lot, level string, resultID int64, value float64, runAt time.Time) {
	qlot, err := e.st.FindQCLot(source, analyteID, lot, level)
	if err != nil || qlot == nil || qlot.SD == 0 {
		return
	}
	z := (value - qlot.Mean) / qlot.SD
	qr := &model.QCResult{
		QCLotID:  qlot.ID,
		ResultID: resultID,
		Value:    value,
		Z:        z,
		RunAt:    runAt,
	}
	violations := Westgard(z, func(n int) []float64 {
		hist, err := e.st.RecentQC(qlot.ID, n)
		if err != nil {
			return nil
		}
		zs := make([]float64, 0, len(hist))
		for _, h := range hist {
			zs = append(zs, h.Z)
		}
		return zs
	})
	qr.Violations = violations
	vb, _ := json.Marshal(violations)
	_, _ = e.st.InsertQCResult(qr, string(vb))
}

// Westgard 规则评估: 输入本次 z 值和历史 z(最新在前), 返回违反的规则
// 1_2s 警告; 1_3s / 2_2s / R_4s / 4_1s / 10x 失控
func Westgard(z float64, history func(n int) []float64) []string {
	var v []string
	az := abs(z)
	if az > 2 {
		v = append(v, "1_2s") // 警告规则
	}
	if az > 3 {
		v = append(v, "1_3s")
	}
	hist := history(12) // 最近12个历史点
	// 2_2s: 连续2点同侧超2SD
	if az > 2 && len(hist) >= 1 && abs(hist[0]) > 2 && sameSign(z, hist[0]) {
		v = append(v, "2_2s")
	}
	// R_4s: 相邻两点差超4SD(一个+2一个-2)
	if len(hist) >= 1 && abs(z-hist[0]) > 4 {
		v = append(v, "R_4s")
	}
	// 4_1s: 连续4点同侧超1SD
	if az > 1 && len(hist) >= 3 {
		ok := true
		for i := 0; i < 3; i++ {
			if !(abs(hist[i]) > 1 && sameSign(z, hist[i])) {
				ok = false
				break
			}
		}
		if ok {
			v = append(v, "4_1s")
		}
	}
	// 10x: 连续10点同侧(均值同侧)
	if len(hist) >= 9 {
		ok := true
		for i := 0; i < 9; i++ {
			if !sameSign(z, hist[i]) {
				ok = false
				break
			}
		}
		if ok {
			v = append(v, "10x")
		}
	}
	return v
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func sameSign(a, b float64) bool {
	return (a > 0 && b > 0) || (a < 0 && b < 0)
}
