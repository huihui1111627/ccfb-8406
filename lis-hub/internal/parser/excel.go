package parser

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"lis-hub/internal/model"
)

// Excel 模板列名(表头按名称匹配, 允许别名)
var excelColumns = map[string][]string{
	"sample_id": {"样本号", "样本编号", "条码号", "sample_id", "sampleid"},
	"mrn":       {"病人ID", "病历号", "患者编号", "mrn"},
	"name":      {"姓名", "患者姓名", "name"},
	"sex":       {"性别", "sex"},
	"age":       {"年龄", "age"},
	"birth":     {"出生日期", "生日", "birth"},
	"code":      {"项目代码", "项目编码", "检验项目", "code"},
	"item":      {"项目名称", "项目名", "item"},
	"result":    {"结果", "结果值", "result"},
	"unit":      {"单位", "unit"},
	"flag":      {"提示", "异常标记", "flag"},
	"test_time": {"检测时间", "检验时间", "test_time"},
	"lot":       {"质控批号", "批号", "lot"},
	"level":     {"质控水平", "水平", "level"},
}

// ParseExcel 按模板导入 Excel (.xlsx)
func ParseExcel(r io.Reader, source string) (*model.ParsedMessage, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("打开Excel失败: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errParse("Excel无工作表")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}
	if len(rows) < 2 {
		return nil, errParse("Excel至少需要表头+1行数据")
	}

	// 表头映射
	colIdx := map[string]int{}
	for i, h := range rows[0] {
		h = strings.TrimSpace(h)
		for key, aliases := range excelColumns {
			for _, a := range aliases {
				if strings.EqualFold(h, a) {
					colIdx[key] = i
				}
			}
		}
	}
	if _, ok := colIdx["result"]; !ok {
		return nil, errParse("缺少必需列: 结果")
	}
	if _, ok := colIdx["code"]; !ok {
		if _, ok2 := colIdx["item"]; !ok2 {
			return nil, errParse("缺少必需列: 项目代码 或 项目名称")
		}
	}

	pm := &model.ParsedMessage{Protocol: "excel", Source: source}
	cell := func(row []string, key string) string {
		i, ok := colIdx[key]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	for ri, row := range rows[1:] {
		if len(strings.Join(row, "")) == 0 {
			continue // 空行
		}
		pr := model.ParsedResult{
			SampleID:   cell(row, "sample_id"),
			SourceCode: cell(row, "code"),
			SourceName: cell(row, "item"),
			RawValue:   cell(row, "result"),
			RawUnit:    cell(row, "unit"),
			InstrFlag:  cell(row, "flag"),
			Status:     "F",
			IsQC:       cell(row, "lot") != "",
			QCLot:      cell(row, "lot"),
			QCLevel:    cell(row, "level"),
		}
		if pr.SourceCode == "" {
			pr.SourceCode = pr.SourceName
		}
		pr.Patient = model.Patient{
			MRN:   cell(row, "mrn"),
			Name:  cell(row, "name"),
			Sex:   normalizeSex(cell(row, "sex")),
			Birth: cell(row, "birth"),
		}
		if age := cell(row, "age"); age != "" {
			if a, ok := ParseFloat(age); ok {
				pr.Patient.Age = a
			}
		}
		if ts := cell(row, "test_time"); ts != "" {
			pr.TestTime = parseExcelTime(ts)
		}
		if pr.RawValue == "" {
			pm.Warnings = append(pm.Warnings, fmt.Sprintf("第%d行结果为空, 跳过", ri+2))
			continue
		}
		pm.Results = append(pm.Results, pr)
	}
	if len(pm.Results) == 0 {
		return pm, errParse("Excel中无有效数据行")
	}
	return pm, nil
}

func parseExcelTime(s string) time.Time {
	for _, l := range []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
		"2006/01/02 15:04:05", "2006/01/02 15:04", "2006/01/02",
	} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}
