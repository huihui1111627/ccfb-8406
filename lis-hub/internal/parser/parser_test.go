package parser

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestParseHL7Normal(t *testing.T) {
	msg := "MSH|^~\\&|AU5800|LAB|LIS|HOSP|20240101120000||ORU^R01|MSG1|P|2.3.1\r" +
		"PID|1||P001^^^HOSP||张三||19850312|M\r" +
		"OBR|1||S001|||20240101110000\r" +
		"OBX|1|NM|GLU^血糖|1|5.6|mmol/L|3.9-6.1|N|||F|||20240101110000\r"
	pm, err := ParseHL7([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Source != "AU5800" {
		t.Errorf("source = %q, want AU5800", pm.Source)
	}
	if len(pm.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(pm.Results))
	}
	r := pm.Results[0]
	if r.SourceCode != "GLU" || r.RawValue != "5.6" || r.RawUnit != "mmol/L" || r.Status != "F" {
		t.Errorf("unexpected result: %+v", r)
	}
	if r.Patient.Name != "张三" || r.Patient.Sex != "M" {
		t.Errorf("unexpected patient: %+v", r.Patient)
	}
	if r.TestTime.IsZero() {
		t.Error("test time not parsed")
	}
}

func TestParseHL7GBK(t *testing.T) {
	msg := "MSH|^~\\&|AU5800|LAB|LIS|HOSP|20240101120000||ORU^R01|MSG2|P|2.3.1\r" +
		"PID|1||P002^^^HOSP||李四||19900820|F\r" +
		"OBR|1||S002|||20240101110000\r" +
		"OBX|1|NM|GLU^葡萄糖|1|6.8|mmol/L|3.9-6.1|H|||F|||20240101110000\r"
	gbk, _, _ := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(msg))
	pm, err := ParseHL7(gbk)
	if err != nil {
		t.Fatal(err)
	}
	if pm.Results[0].Patient.Name != "李四" {
		t.Errorf("GBK name = %q, want 李四", pm.Results[0].Patient.Name)
	}
	if len(pm.Warnings) == 0 {
		t.Error("expected encoding warning")
	}
}

func TestParseHL7StatusInOBX10(t *testing.T) {
	// 容错: 状态放在 OBX-10 而非 OBX-11
	msg := "MSH|^~\\&|AU5800|LAB|LIS|HOSP|20240101120000||ORU^R01|MSG3|P|2.3.1\r" +
		"PID|1||P003^^^HOSP||王五||19560301|M\r" +
		"OBR|1||S003|||20240101110000\r" +
		"OBX|1|NM|GLU^血糖|1|5.2|mmol/L|3.9-6.1|N||C|||20240101110000\r"
	pm, err := ParseHL7([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Results[0].Status != "C" {
		t.Errorf("status = %q, want C", pm.Results[0].Status)
	}
}

func TestParseASTMNormal(t *testing.T) {
	recs := "H|\\^&|||XN-1000|||||||||E1394-97\r" +
		"P|1||P201||孙七||19920415|F\r" +
		"O|1|S101||^^^CBC\r" +
		"R|1|^^^HGB^血红蛋白|135|g/L|115-150|N||F||||20240101110000\r" +
		"L|1|N\r"
	pm, err := ParseASTM(ASTMFrame(recs))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Source != "XN-1000" {
		t.Errorf("source = %q", pm.Source)
	}
	if len(pm.Results) != 1 || pm.Results[0].SourceCode != "HGB" {
		t.Fatalf("unexpected results: %+v", pm.Results)
	}
	if len(pm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", pm.Warnings)
	}
}

func TestParseASTMTruncated(t *testing.T) {
	recs := "H|\\^&|||XN-1000|||||||||E1394-97\r" +
		"P|1||P202||周八||19881030|M\r" +
		"O|1|S102||^^^CBC\r" +
		"R|1|^^^HGB^血红蛋白|145|g/L|130-175|N||F||||20240101110000\r" +
		"R|2|^^^WBC^白细"
	full := ASTMFrame(recs)
	truncated := full[:len(full)-20]
	pm, err := ParseASTM(truncated)
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.Results) != 1 {
		t.Fatalf("results = %d, want 1 (截断前的完整记录应保留)", len(pm.Results))
	}
	joined := strings.Join(pm.Warnings, ";")
	if !strings.Contains(joined, "截断") {
		t.Errorf("expected truncation warning, got %v", pm.Warnings)
	}
}

func TestParseASTMBadChecksum(t *testing.T) {
	recs := "H|\\^&|||XN-1000|||||||||E1394-97\r" +
		"O|1|S103||^^^CBC\r" +
		"R|1|^^^WBC^白细胞|6.8|10^9/L|3.5-9.5|N||F||||20240101110000\r" +
		"L|1|N\r"
	frame := ASTMFrame(recs)
	// 篡改校验和
	frame[len(frame)-4] = 'X'
	pm, err := ParseASTM(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.Results) != 1 {
		t.Fatal("校验和错误不应导致结果丢失")
	}
	if !strings.Contains(strings.Join(pm.Warnings, ";"), "校验和") {
		t.Errorf("expected checksum warning, got %v", pm.Warnings)
	}
}

func TestParseFloat(t *testing.T) {
	cases := map[string]float64{"5.6": 5.6, ">100": 100, "<0.5": 0.5, "1,234.5": 1234.5}
	for in, want := range cases {
		if got, ok := ParseFloat(in); !ok || got != want {
			t.Errorf("ParseFloat(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseFloat("阴性"); ok {
		t.Error("定性结果不应解析为数值")
	}
}
