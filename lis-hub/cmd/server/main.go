package main

import (
	"flag"
	"log"
	"net/http"

	"lis-hub/internal/api"
	"lis-hub/internal/engine"
	"lis-hub/internal/model"
	"lis-hub/internal/store"
)

func main() {
	dbPath := flag.String("db", "lis.db", "SQLite数据库路径")
	httpAddr := flag.String("http", ":8080", "HTTP控制台地址")
	tcpAddr := flag.String("tcp", ":6600", "MLLP/ASTM接入监听地址(留空关闭)")
	seed := flag.Bool("seed", true, "首次启动写入演示主数据")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	if *seed {
		if err := seedMasterData(st); err != nil {
			log.Printf("主数据初始化: %v", err)
		}
	}

	en := engine.New(st)
	srv := api.New(st, en)

	if *tcpAddr != "" {
		if err := srv.ListenTCP(*tcpAddr); err != nil {
			log.Printf("TCP监听失败: %v", err)
		}
	}

	log.Printf("网页控制台: http://localhost%s", *httpAddr)
	log.Fatal(http.ListenAndServe(*httpAddr, srv.Handler()))
}

// seedMasterData 演示用主数据: 标准项目/参考范围/危急值规则/映射/质控批
func seedMasterData(st *store.Store) error {
	analytes := []model.Analyte{
		{Code: "GLU", Name: "空腹血糖", CanonicalUnit: "mmol/L"},
		{Code: "K", Name: "血清钾", CanonicalUnit: "mmol/L"},
		{Code: "NA", Name: "血清钠", CanonicalUnit: "mmol/L"},
		{Code: "HGB", Name: "血红蛋白", CanonicalUnit: "g/L"},
		{Code: "WBC", Name: "白细胞计数", CanonicalUnit: "10^9/L"},
		{Code: "PLT", Name: "血小板计数", CanonicalUnit: "10^9/L"},
		{Code: "CREA", Name: "肌酐", CanonicalUnit: "umol/L"},
		{Code: "ALT", Name: "丙氨酸氨基转移酶", CanonicalUnit: "U/L"},
	}
	ids := map[string]int64{}
	for _, a := range analytes {
		a := a
		id, err := st.UpsertAnalyte(&a)
		if err != nil {
			return err
		}
		ids[a.Code] = id
	}

	// 参考范围(按性别/年龄)
	ranges := []model.RefRange{
		{AnalyteID: ids["GLU"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 3.9, High: 6.1},
		{AnalyteID: ids["K"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 3.5, High: 5.3},
		{AnalyteID: ids["NA"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 137, High: 147},
		{AnalyteID: ids["HGB"], Sex: "M", AgeMin: 18, AgeMax: 200, Low: 130, High: 175},
		{AnalyteID: ids["HGB"], Sex: "F", AgeMin: 18, AgeMax: 200, Low: 115, High: 150},
		{AnalyteID: ids["HGB"], Sex: "", AgeMin: 0, AgeMax: 18, Low: 120, High: 160},
		{AnalyteID: ids["WBC"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 3.5, High: 9.5},
		{AnalyteID: ids["PLT"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 125, High: 350},
		{AnalyteID: ids["CREA"], Sex: "M", AgeMin: 0, AgeMax: 200, Low: 57, High: 111},
		{AnalyteID: ids["CREA"], Sex: "F", AgeMin: 0, AgeMax: 200, Low: 41, High: 81},
		{AnalyteID: ids["ALT"], Sex: "", AgeMin: 0, AgeMax: 200, Low: 9, High: 50},
	}
	existing, _ := st.ListRefRanges()
	if len(existing) == 0 {
		for _, r := range ranges {
			r := r
			if _, err := st.AddRefRange(&r); err != nil {
				return err
			}
		}
	}

	// 危急值规则
	rules := []model.CriticalRule{
		{AnalyteID: ids["K"], AgeMax: 200, Low: f64(2.5), High: f64(6.5)},
		{AnalyteID: ids["GLU"], AgeMax: 200, Low: f64(2.2), High: f64(22.2)},
		{AnalyteID: ids["NA"], AgeMax: 200, Low: f64(120), High: f64(160)},
		{AnalyteID: ids["HGB"], AgeMax: 200, Low: f64(50), High: nil},
		{AnalyteID: ids["PLT"], AgeMax: 200, Low: f64(30), High: f64(1000)},
	}
	existRules, _ := st.ListCriticalRules()
	if len(existRules) == 0 {
		for _, r := range rules {
			r := r
			if _, err := st.AddCriticalRule(&r); err != nil {
				return err
			}
		}
	}

	// 演示映射: 仪器A(贝克曼AU) / 仪器B(希森美康XN) / 合作医院HIS
	mappings := []model.Mapping{
		{Source: "AU5800", SourceCode: "GLU", SourceName: "Glucose", SourceUnit: "mmol/L", AnalyteID: &[]int64{ids["GLU"]}[0], Factor: 1},
		{Source: "AU5800", SourceCode: "GLU", SourceName: "Glucose", SourceUnit: "mg/dL", AnalyteID: &[]int64{ids["GLU"]}[0], Factor: 0.0555}, // 错单位换算
		{Source: "AU5800", SourceCode: "K", SourceName: "Potassium", SourceUnit: "mmol/L", AnalyteID: &[]int64{ids["K"]}[0], Factor: 1},
		{Source: "AU5800", SourceCode: "Na", SourceName: "Sodium", SourceUnit: "mmol/L", AnalyteID: &[]int64{ids["NA"]}[0], Factor: 1},
		{Source: "AU5800", SourceCode: "CRE", SourceName: "Creatinine", SourceUnit: "umol/L", AnalyteID: &[]int64{ids["CREA"]}[0], Factor: 1},
		{Source: "AU5800", SourceCode: "ALT", SourceName: "ALT", SourceUnit: "U/L", AnalyteID: &[]int64{ids["ALT"]}[0], Factor: 1},
		{Source: "XN-1000", SourceCode: "HGB", SourceName: "Hemoglobin", SourceUnit: "g/L", AnalyteID: &[]int64{ids["HGB"]}[0], Factor: 1},
		{Source: "XN-1000", SourceCode: "HGB", SourceName: "Hemoglobin", SourceUnit: "g/dL", AnalyteID: &[]int64{ids["HGB"]}[0], Factor: 10}, // g/dL -> g/L
		{Source: "XN-1000", SourceCode: "WBC", SourceName: "WBC", SourceUnit: "10^9/L", AnalyteID: &[]int64{ids["WBC"]}[0], Factor: 1},
		{Source: "XN-1000", SourceCode: "PLT", SourceName: "Platelet", SourceUnit: "10^9/L", AnalyteID: &[]int64{ids["PLT"]}[0], Factor: 1},
		{Source: "HOSP-A", SourceCode: "血糖", SourceName: "血糖", SourceUnit: "mmol/L", AnalyteID: &[]int64{ids["GLU"]}[0], Factor: 1},
		{Source: "HOSP-A", SourceCode: "血糖", SourceName: "血糖", SourceUnit: "mg/dL", AnalyteID: &[]int64{ids["GLU"]}[0], Factor: 0.0555}, // 错单位换算
		{Source: "HOSP-A", SourceCode: "钾", SourceName: "血钾", SourceUnit: "mmol/L", AnalyteID: &[]int64{ids["K"]}[0], Factor: 1},
		{Source: "HOSP-A", SourceCode: "血红蛋白", SourceName: "血红蛋白", SourceUnit: "g/L", AnalyteID: &[]int64{ids["HGB"]}[0], Factor: 1},
	}
	for _, m := range mappings {
		m := m
		if err := st.SaveMapping(&m); err != nil {
			return err
		}
	}

	// 质控批
	lots := []model.QCLot{
		{Source: "AU5800", AnalyteID: ids["GLU"], Lot: "LOT2401", Level: "L1", Mean: 5.5, SD: 0.15},
		{Source: "AU5800", AnalyteID: ids["GLU"], Lot: "LOT2401", Level: "L2", Mean: 12.0, SD: 0.3},
		{Source: "AU5800", AnalyteID: ids["K"], Lot: "LOT2401", Level: "L1", Mean: 4.0, SD: 0.08},
	}
	existLots, _ := st.ListQCLots()
	if len(existLots) == 0 {
		for _, l := range lots {
			l := l
			if _, err := st.UpsertQCLot(&l); err != nil {
				return err
			}
		}
	}
	return nil
}

func f64(v float64) *float64 { return &v }
