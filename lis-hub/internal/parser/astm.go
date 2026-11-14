package parser

import (
	"bytes"
	"strings"

	"lis-hub/internal/model"
)

// ASTM E1381 帧控制字符
const (
	astmSTX = 0x02
	astmETX = 0x03
	astmETB = 0x17
	astmENQ = 0x05
	astmACK = 0x06
	astmLF  = 0x0A
	astmCR  = 0x0D
)

// ParseASTM 容错解析 ASTM E1394 记录; 帧层(E1381)截断/校验和错误时尽量抢救记录内容
func ParseASTM(raw []byte) (*model.ParsedMessage, error) {
	pm := &model.ParsedMessage{Protocol: "astm"}

	// 1. 帧层: 提取 STX..ETX/ETB 之间的数据; 截断帧(无ETX)取到末尾并告警
	var payload bytes.Buffer
	i := 0
	truncated := false
	for i < len(raw) {
		b := raw[i]
		switch b {
		case astmENQ, astmACK, 0x04: // ENQ/ACK/EOT 控制字符跳过
			i++
		case astmSTX:
			// 找帧尾
			end := -1
			endByte := byte(0)
			for j := i + 1; j < len(raw); j++ {
				if raw[j] == astmETX || raw[j] == astmETB {
					end = j
					endByte = raw[j]
					break
				}
			}
			if end < 0 {
				// 截断帧: 取剩余全部
				payload.Write(raw[i+1:])
				truncated = true
				i = len(raw)
			} else {
				payload.Write(raw[i+1 : end])
				// 校验和: ETX/ETB 后 2 字节 hex
				if end+2 < len(raw) {
					sum := astmChecksum(raw[i+1 : end+1])
					got := strings.ToUpper(string(raw[end+1 : end+3]))
					want := strings.ToUpper(string([]byte{hexDigit(sum >> 4), hexDigit(sum & 0xF)}))
					if got != want {
						pm.Warnings = append(pm.Warnings, "帧校验和不匹配(收到 "+got+", 应为 "+want+"), 已按原样解析")
					}
					i = end + 3
				} else {
					pm.Warnings = append(pm.Warnings, "帧尾缺少校验和, 可能为截断报文")
					i = end + 1
				}
				if endByte == astmETB {
					// 中间帧, 继续等下一帧
				}
				// 跳过 CRLF
				for i < len(raw) && (raw[i] == astmCR || raw[i] == astmLF) {
					i++
				}
			}
		default:
			// 帧外裸数据(有些仪器直接发记录文本)
			payload.WriteByte(b)
			i++
		}
	}
	if truncated {
		pm.Warnings = append(pm.Warnings, "报文截断: 最后一个帧缺少ETX, 已解析已接收部分")
	}

	text, enc := DetectEncoding(payload.Bytes())
	if enc != "utf-8" {
		pm.Warnings = append(pm.Warnings, "报文非UTF-8编码, 已按 "+enc+" 转码")
	}

	// 2. 记录层
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	records := strings.Split(text, "\r")

	var patient model.Patient
	var sampleID string
	var haveHeader bool

	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "|")
		get := func(i int) string {
			if i < len(fields) {
				return strings.TrimSpace(fields[i])
			}
			return ""
		}
		switch fields[0] {
		case "H": // 头: H|\^&|||sender...
			haveHeader = true
			if v := firstComponent(get(4)); v != "" {
				pm.Source = v
			}
			if pm.Source == "" {
				pm.Source = firstComponent(get(3))
			}
		case "P": // 病人(E1394): P|seq||practiceID|||name||birth|sex
			patient.MRN = firstComponent(get(3))
			patient.Name = firstComponent(get(6))
			patient.Birth = get(8)
			patient.Sex = normalizeSex(get(9))
		case "Q": // 质控品记录(部分仪器用Q): Q|1|^lot^level|...
			// 标记后续 R 为 QC
			patient = model.Patient{Name: "QC"}
		case "O": // 医嘱: O|1|sampleID|...
			if v := firstComponent(get(2)); v != "" {
				sampleID = v
			}
		case "R": // 结果: R|1|^^^code^name|value|unit|refRange|flag|status|...|datetime
			pr := model.ParsedResult{
				SampleID: sampleID,
				Patient:  patient,
				Status:   "F",
			}
			testField := get(2) // ^^^GLU 或 ^^^GLU^血糖
			parts := strings.Split(testField, "^")
			var nonEmpty []string
			for _, p := range parts {
				if strings.TrimSpace(p) != "" {
					nonEmpty = append(nonEmpty, strings.TrimSpace(p))
				}
			}
			if len(nonEmpty) > 0 {
				pr.SourceCode = nonEmpty[0]
			}
			if len(nonEmpty) > 1 {
				pr.SourceName = nonEmpty[1]
			}
			pr.RawValue = get(3)
			pr.RawUnit = strings.TrimSpace(get(4))
			pr.InstrFlag = get(6)
			if st := get(8); st != "" {
				pr.Status = strings.ToUpper(strings.TrimSpace(st))
			}
			// 时间字段位置各厂商不一, 从后往前找
			for i := len(fields) - 1; i >= 9; i-- {
				if t := parseHL7Time(fields[i]); !t.IsZero() {
					pr.TestTime = t
					break
				}
			}
			// QC 判定: 样本号以 QC 开头或 P 记录缺失
			if strings.HasPrefix(strings.ToUpper(sampleID), "QC") || patient.Name == "QC" {
				pr.IsQC = true
				// 从样本号提取批号和水平: QC-LOT123-L1
				qs := strings.Split(sampleID, "-")
				if len(qs) >= 2 {
					pr.QCLot = qs[1]
				}
				if len(qs) >= 3 {
					pr.QCLevel = qs[2]
				}
			}
			if pr.SourceCode == "" && pr.RawValue == "" {
				pm.Warnings = append(pm.Warnings, "R记录缺项目代码和结果值, 跳过")
				continue
			}
			pm.Results = append(pm.Results, pr)
		case "C", "L", "M":
			// 注释/终止/厂商自定义, 忽略
		default:
			pm.Warnings = append(pm.Warnings, "未知记录类型: "+fields[0])
		}
	}
	if !haveHeader {
		pm.Warnings = append(pm.Warnings, "缺少H头记录, 按残缺报文容错解析")
	}
	if len(pm.Results) == 0 {
		return pm, errParse("未解析出任何结果(R记录)")
	}
	return pm, nil
}

func astmChecksum(data []byte) byte {
	var sum int
	for _, b := range data {
		sum += int(b)
	}
	return byte(sum % 256)
}

func hexDigit(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + (n - 10)
}

// ASTMFrame 打包一条 ASTM 消息为 E1381 帧(测试数据生成用)
func ASTMFrame(records string) []byte {
	var out []byte
	out = append(out, astmSTX)
	out = append(out, []byte(records)...)
	out = append(out, astmETX)
	sum := astmChecksum(append([]byte(records), astmETX))
	out = append(out, hexDigit(sum>>4), hexDigit(sum&0xF))
	out = append(out, astmCR, astmLF)
	return out
}
