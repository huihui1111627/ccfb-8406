// 测试报文生成器: 生成正常/乱码/截断/错单位/更正/重发/危急值/质控等场景
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"lis-hub/internal/parser"
)

var outDir = "out"

func write(name string, data []byte) {
	p := filepath.Join(outDir, name)
	if err := os.WriteFile(p, data, 0644); err != nil {
		panic(err)
	}
	fmt.Println("生成:", p)
}

func hl7Time(t time.Time) string { return t.Format("20060102150405") }

func main() {
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}
	now := time.Now()
	t1 := hl7Time(now.Add(-47 * time.Minute))
	t2 := hl7Time(now.Add(-32 * time.Minute))
	t3 := hl7Time(now.Add(-15 * time.Minute))

	// 1. 正常 HL7 ORU (AU5800 生化)
	write("01_hl7_normal.hl7", []byte(
		"MSH|^~\\&|AU5800|LAB|LIS|HOSP|"+hl7Time(now)+"||ORU^R01|MSG00001|P|2.3.1\r"+
			"PID|1||P10023^^^HOSP||张三||19850312|M\r"+
			"OBR|1||S2024001|||"+t1+"\r"+
			"OBX|1|NM|GLU^空腹血糖|1|5.6|mmol/L|3.9-6.1|N|||F|||"+t1+"\r"+
			"OBX|2|NM|K^血清钾|1|4.2|mmol/L|3.5-5.3|N|||F|||"+t1+"\r"+
			"OBX|3|NM|Na^血清钠|1|141|mmol/L|137-147|N|||F|||"+t1+"\r"+
			"OBX|4|NM|CRE^肌酐|1|78|umol/L|57-111|N|||F|||"+t1+"\r"+
			"OBX|5|NM|ALT^丙氨酸氨基转移酶|1|35|U/L|9-50|N|||F|||"+t1+"\r"))

	// 2. HL7 GBK 编码(乱码场景): 仪器发 GBK 字节流
	gbkMsg := "MSH|^~\\&|AU5800|LAB|LIS|HOSP|" + hl7Time(now) + "||ORU^R01|MSG00002|P|2.3.1\r" +
		"PID|1||P10045^^^HOSP||李四||19900820|F\r" +
		"OBR|1||S2024002|||" + t2 + "\r" +
		"OBX|1|NM|GLU^葡萄糖|1|6.8|mmol/L|3.9-6.1|H|||F|||" + t2 + "\r" +
		"OBX|2|NM|K^钾|1|3.9|mmol/L|3.5-5.3|N|||F|||" + t2 + "\r"
	gbk, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(gbkMsg))
	if err != nil {
		panic(err)
	}
	write("02_hl7_gbk_mojibake.hl7", gbk)

	// 3. HL7 危急值 (钾 6.9)
	write("03_hl7_critical.hl7", []byte(
		"MSH|^~\\&|AU5800|LAB|LIS|HOSP|"+hl7Time(now)+"||ORU^R01|MSG00003|P|2.3.1\r"+
			"PID|1||P10067^^^HOSP||王五||19560301|M\r"+
			"OBR|1||S2024003|||"+t3+"\r"+
			"OBX|1|NM|K^血清钾|1|6.9|mmol/L|3.5-5.3|HH|||F|||"+t3+"\r"+
			"OBX|2|NM|GLU^空腹血糖|1|1.8|mmol/L|3.9-6.1|LL|||F|||"+t3+"\r"))

	// 4. HL7 更正报文 (先 F 后 C)
	write("04a_hl7_result.hl7", []byte(
		"MSH|^~\\&|AU5800|LAB|LIS|HOSP|"+hl7Time(now)+"||ORU^R01|MSG00004|P|2.3.1\r"+
			"PID|1||P10089^^^HOSP||赵六||19771105|F\r"+
			"OBR|1||S2024004|||"+t1+"\r"+
			"OBX|1|NM|GLU^空腹血糖|1|7.8|mmol/L|3.9-6.1|H|||F|||"+t1+"\r"))
	write("04b_hl7_correction.hl7", []byte(
		"MSH|^~\\&|AU5800|LAB|LIS|HOSP|"+hl7Time(now)+"||ORU^R01|MSG00005|P|2.3.1\r"+
			"PID|1||P10089^^^HOSP||赵六||19771105|F\r"+
			"OBR|1||S2024004|||"+t1+"\r"+
			"OBX|1|NM|GLU^空腹血糖|1|5.2|mmol/L|3.9-6.1|N|||C|||"+t1+"\r"))

	// 5. HL7 重发(与04a完全相同 -> 报文级去重); 以及同内容新MSGID(结果级去重)
	write("05_hl7_resend_same.hl7", []byte(
		"MSH|^~\\&|AU5800|LAB|LIS|HOSP|"+hl7Time(now)+"||ORU^R01|MSG00004|P|2.3.1\r"+
			"PID|1||P10089^^^HOSP||赵六||19771105|F\r"+
			"OBR|1||S2024004|||"+t1+"\r"+
			"OBX|1|NM|GLU^空腹血糖|1|7.8|mmol/L|3.9-6.1|H|||F|||"+t1+"\r"))

	// 6. ASTM 正常 (XN-1000 血常规, 含一个 g/dL 单位的 HGB -> 换算)
	astmRecs := "H|\\^&|||XN-1000|||||||||E1394-97\r" +
		"P|1||P20011||孙七||19920415|F\r" +
		"O|1|S2024010||^^^CBC\r" +
		"R|1|^^^HGB^血红蛋白|13.5|g/dL|11.5-15.0|N|||F||||" + t1 + "\r" +
		"R|2|^^^WBC^白细胞|6.8|10^9/L|3.5-9.5|N|||F||||" + t1 + "\r" +
		"R|3|^^^PLT^血小板|210|10^9/L|125-350|N|||F||||" + t1 + "\r" +
		"L|1|N\r"
	write("06_astm_normal.astm", parser.ASTMFrame(astmRecs))

	// 7. ASTM 截断(发到一半断了)
	truncated := "H|\\^&|||XN-1000|||||||||E1394-97\r" +
		"P|1||P20022||周八||19881030|M\r" +
		"O|1|S2024011||^^^CBC\r" +
		"R|1|^^^HGB^血红蛋白|145|g/L|130-175|N|||F||||" + t2 + "\r" +
		"R|2|^^^WBC^白细"
	full := parser.ASTMFrame(truncated)
	write("07_astm_truncated.astm", full[:len(full)-20]) // 砍掉帧尾, 模拟传输中断

	// 8. ASTM 质控 (QC-LOT2401-L1, 连续多天, 最后一天 1_3s 失控)
	for i := 0; i < 6; i++ {
		qt := now.Add(time.Duration(i-6) * 24 * time.Hour)
		val := 5.5 + []float64{0.05, -0.08, 0.12, -0.05, 0.09, 0.62}[i] // 最后一天 z>3
		recs := "H|\\^&|||AU5800|||||||||E1394-97\r" +
			"O|1|QC-LOT2401-L1||^^^QC\r" +
			fmt.Sprintf("R|1|^^^GLU^葡萄糖质控|%.2f|mmol/L|||F||||%s\r", val, hl7Time(qt)) +
			"L|1|N\r"
		write(fmt.Sprintf("08_astm_qc_day%d.astm", i+1), parser.ASTMFrame(recs))
	}

	// 9. Excel 导入(合作医院, 含错单位: 血糖用 mg/dL, 一个未知单位)
	f := excelize.NewFile()
	sheet := "Sheet1"
	_ = f.SetSheetRow(sheet, "A1", &[]any{"样本号", "病人ID", "姓名", "性别", "年龄", "项目代码", "项目名称", "结果", "单位", "检测时间"})
	rows := [][]any{
		{"H2024001", "P30001", "吴九", "男", 45, "血糖", "血糖", 118.0, "mg/dL", t1},
		{"H2024001", "P30001", "吴九", "男", 45, "钾", "血钾", 4.5, "mmol/L", t1},
		{"H2024002", "P30002", "郑十", "女", 32, "血红蛋白", "血红蛋白", 128.0, "g/L", t2},
		{"H2024003", "P30003", "钱一", "男", 60, "血糖", "血糖", 7.2, "mmol/L", t3},
		{"H2024004", "P30004", "冯二", "女", 28, "血糖", "血糖", 5.1, "mmol/L", ""},   // 缺检测时间
		{"H2024005", "P30005", "褚三", "男", 51, "未知项", "未知项目", 99.0, "奇怪单位", t3}, // 未映射+未知单位
	}
	for i, r := range rows {
		cell := fmt.Sprintf("A%d", i+2)
		_ = f.SetSheetRow(sheet, cell, &r)
	}
	xlsx, err := f.WriteToBuffer()
	if err != nil {
		panic(err)
	}
	write("09_excel_hospital.xlsx", xlsx.Bytes())

	fmt.Println("\n全部测试报文已生成到", outDir)
}
