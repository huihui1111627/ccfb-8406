package parser

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"lis-hub/internal/model"
)

// DetectEncoding 探测编码: 非法 UTF-8 时按 GBK 转码(国内仪器常见)
func DetectEncoding(raw []byte) (text string, encoding string) {
	if utf8.Valid(raw) {
		return string(raw), "utf-8"
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	if err == nil && utf8.Valid(out) {
		return string(out), "gbk"
	}
	// 兜底: 去掉非法字节
	return strings.ToValidUTF8(string(raw), "�"), "utf-8-lossy"
}

// ParseHL7 容错解析 HL7 v2 ORU^R01
func ParseHL7(raw []byte) (*model.ParsedMessage, error) {
	text, enc := DetectEncoding(raw)
	pm := &model.ParsedMessage{Protocol: "hl7"}
	if enc != "utf-8" {
		pm.Warnings = append(pm.Warnings, "报文非UTF-8编码, 已按 "+enc+" 转码")
	}

	// 段分隔: \r 或 \n
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	segs := strings.Split(text, "\r")

	var patient model.Patient
	var sampleID string
	var obrTime time.Time
	var haveMSH bool

	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if len(seg) < 4 {
			continue
		}
		// MSH 的分隔符在段内定义
		var fields []string
		name := seg[:3]
		if name == "MSH" {
			if len(seg) < 8 {
				pm.Warnings = append(pm.Warnings, "MSH段过短, 跳过")
				continue
			}
			fs := string(seg[3])
			rest := seg[4:]
			fields = strings.Split(rest, fs)
			haveMSH = true
			// fields[0] = 分隔符定义段(如 ^~\&), MSH-2; MSH-n = fields[n-2]
			if len(fields) >= 2 {
				pm.Source = firstComponent(fields[1]) // MSH-3 发送应用(仪器)
			}
			if pm.Source == "" && len(fields) >= 3 {
				pm.Source = firstComponent(fields[2]) // MSH-4 发送设施
			}
			continue
		}
		fields = strings.Split(seg[4:], "|")

		get := func(i int) string { // i 为 1-based 段内字段号
			if i-1 < len(fields) {
				return strings.TrimSpace(fields[i-1])
			}
			return ""
		}

		switch name {
		case "PID":
			patient.MRN = firstComponent(get(3))
			patient.Name = firstComponent(get(5))
			patient.Birth = get(7)
			patient.Sex = normalizeSex(get(8))
		case "OBR":
			if v := firstComponent(get(2)); v != "" {
				sampleID = v
			}
			if v := firstComponent(get(3)); v != "" {
				sampleID = v // filler order number 优先
			}
			if t := parseHL7Time(get(7)); !t.IsZero() {
				obrTime = t
			}
			if t := parseHL7Time(get(14)); !t.IsZero() {
				obrTime = t
			}
		case "OBX":
			pr := model.ParsedResult{
				SampleID: sampleID,
				Patient:  patient,
				Status:   "F",
			}
			code := get(3) // OBX-3 观察标识, 形如 718-7^Hemoglobin^LN 或 GLU^血糖
			parts := strings.Split(code, "^")
			if len(parts) > 0 {
				pr.SourceCode = strings.TrimSpace(parts[0])
			}
			if len(parts) > 1 {
				pr.SourceName = strings.TrimSpace(parts[1])
			}
			if pr.SourceCode == "" && pr.SourceName != "" {
				pr.SourceCode = pr.SourceName
			}
			pr.RawValue = get(5)
			pr.RawUnit = strings.TrimSpace(get(6))
			pr.InstrFlag = get(8)
			st := get(11)
			if st == "" {
				// 容错: 部分仪器把状态放在 OBX-10
				if f10 := strings.ToUpper(get(10)); f10 == "F" || f10 == "P" || f10 == "C" || f10 == "R" || f10 == "X" {
					st = f10
				}
			}
			if st != "" {
				pr.Status = strings.ToUpper(st)
			}
			t := parseHL7Time(get(14))
			if t.IsZero() {
				t = obrTime
			}
			pr.TestTime = t
			if pr.SourceCode == "" && pr.RawValue == "" {
				pm.Warnings = append(pm.Warnings, "OBX段缺项目代码和结果值, 跳过")
				continue
			}
			pm.Results = append(pm.Results, pr)
		case "NTE", "ORC", "SPM", "TQ1", "PV1", "IN1", "SAC", "OBX2":
			// 已知但不处理的段
		default:
			if name != "" && !haveMSH {
				// 非 MSH 开头也尽量解析
			}
		}
	}
	if !haveMSH {
		pm.Warnings = append(pm.Warnings, "缺少MSH段, 按残缺报文容错解析")
	}
	if len(pm.Results) == 0 {
		return pm, errParse("未解析出任何结果(OBX)")
	}
	return pm, nil
}

func firstComponent(s string) string {
	if i := strings.IndexAny(s, "^&"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func normalizeSex(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "M", "F":
		return s
	case "男":
		return "M"
	case "女":
		return "F"
	default:
		return ""
	}
}

// parseHL7Time 支持 YYYYMMDDHHMMSS 及截断形式
func parseHL7Time(s string) time.Time {
	s = strings.TrimSpace(s)
	// 去掉时区后缀
	if i := strings.IndexAny(s, "+-"); i > 8 {
		s = s[:i]
	}
	if len(s) < 8 {
		return time.Time{}
	}
	layouts := map[int]string{
		8: "20060102", 10: "2006010215", 12: "200601021504", 14: "20060102150405",
	}
	if l, ok := layouts[len(s)]; ok {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	if len(s) > 14 {
		if t, err := time.ParseInLocation("20060102150405", s[:14], time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ParseFloat 容错数值解析: 允许 ">100"、"<0.5"、千分位、逗号小数点
func ParseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, ">")
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimPrefix(s, "≥")
	s = strings.TrimPrefix(s, "≤")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }
func errParse(msg string) error   { return &parseErr{msg} }
