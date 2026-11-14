package model

import "time"

// ---------- 原始报文 ----------

type RawMessage struct {
	ID          int64     `json:"id"`
	ReceivedAt  time.Time `json:"received_at"`
	Source      string    `json:"source"`       // 来源(仪器/医院标识)
	Protocol    string    `json:"protocol"`     // hl7 | astm | excel
	Content     string    `json:"content"`      // 原始报文(按收到时的字节原样保存,可能含乱码)
	Encoding    string    `json:"encoding"`     // 探测到的编码: utf-8 / gbk / ...
	ParseStatus string    `json:"parse_status"` // ok | partial | failed
	ParseError  string    `json:"parse_error"`
	MsgHash     string    `json:"msg_hash"` // 报文级去重
	ResultCount int       `json:"result_count"`
}

// ---------- 主数据 ----------

type Device struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type Analyte struct {
	ID            int64  `json:"id"`
	Code          string `json:"code"` // 标准编码(如 LOINC 或院内码)
	Name          string `json:"name"`
	CanonicalUnit string `json:"canonical_unit"` // 标准单位
}

type RefRange struct {
	ID        int64   `json:"id"`
	AnalyteID int64   `json:"analyte_id"`
	Sex       string  `json:"sex"`     // M/F/空=不限
	AgeMin    float64 `json:"age_min"` // 岁
	AgeMax    float64 `json:"age_max"`
	Low       float64 `json:"low"`
	High      float64 `json:"high"`
}

// 项目映射: 某仪器/来源的项目代码 -> 标准项目
type Mapping struct {
	ID         int64   `json:"id"`
	Source     string  `json:"source"`      // 来源标识(仪器或医院)
	SourceCode string  `json:"source_code"` // 对方项目代码
	SourceName string  `json:"source_name"` // 对方项目名(便于人工辨认)
	SourceUnit string  `json:"source_unit"` // 对方单位
	AnalyteID  *int64  `json:"analyte_id"`  // 未映射为 null
	Factor     float64 `json:"factor"`      // 换算: 标准值 = 原值*factor + offset
	Offset     float64 `json:"offset"`
}

type Patient struct {
	ID    int64   `json:"id"`
	MRN   string  `json:"mrn"`
	Name  string  `json:"name"`
	Sex   string  `json:"sex"`   // M/F/U
	Birth string  `json:"birth"` // YYYY-MM-DD, 可能为空
	Age   float64 `json:"age"`   // 报文里直接给年龄时用
}

// ---------- 结果 ----------

type Result struct {
	ID           int64     `json:"id"`
	RawMessageID int64     `json:"raw_message_id"` // 溯源: 任何结果可追回原始报文
	Source       string    `json:"source"`
	SampleID     string    `json:"sample_id"`
	PatientMRN   string    `json:"patient_mrn"`
	PatientName  string    `json:"patient_name"`
	PatientSex   string    `json:"patient_sex"`
	PatientAge   float64   `json:"patient_age"`
	SourceCode   string    `json:"source_code"`
	SourceName   string    `json:"source_name"`
	AnalyteID    *int64    `json:"analyte_id"`
	AnalyteCode  string    `json:"analyte_code"`
	AnalyteName  string    `json:"analyte_name"`
	RawValue     string    `json:"raw_value"` // 原始值(可能是文本)
	RawUnit      string    `json:"raw_unit"`  // 原始单位
	Value        *float64  `json:"value"`     // 标准化数值(换算后)
	Unit         string    `json:"unit"`      // 标准单位
	Flag         string    `json:"flag"`      // N/H/L/A(异常定性)/空
	RefLow       *float64  `json:"ref_low"`
	RefHigh      *float64  `json:"ref_high"`
	Status       string    `json:"status"` // F终报 P初报 C更正
	Version      int       `json:"version"`
	SupersedesID *int64    `json:"supersedes_id"` // 更正链: 指向被替代的旧版本
	IsCurrent    bool      `json:"is_current"`
	DedupKey     string    `json:"dedup_key"`
	TestTime     time.Time `json:"test_time"`   // 仪器检测时间
	ReceivedAt   time.Time `json:"received_at"` // 平台接收时间
	IsQC         bool      `json:"is_qc"`
	CreatedAt    time.Time `json:"created_at"`
}

// ---------- 危急值 ----------

type CriticalRule struct {
	ID        int64    `json:"id"`
	AnalyteID int64    `json:"analyte_id"`
	Sex       string   `json:"sex"`
	AgeMin    float64  `json:"age_min"`
	AgeMax    float64  `json:"age_max"`
	Low       *float64 `json:"low"`  // 低于此值危急
	High      *float64 `json:"high"` // 高于此值危急
}

type CriticalEvent struct {
	ID        int64      `json:"id"`
	ResultID  int64      `json:"result_id"`
	Level     string     `json:"level"`  // low | high
	Status    string     `json:"status"` // open | ack | closed
	AckBy     string     `json:"ack_by"`
	AckAt     *time.Time `json:"ack_at"`
	ClosedBy  string     `json:"closed_by"`
	ClosedAt  *time.Time `json:"closed_at"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	// 关联展示字段
	SampleID    string   `json:"sample_id"`
	PatientName string   `json:"patient_name"`
	AnalyteName string   `json:"analyte_name"`
	Value       *float64 `json:"value"`
	Unit        string   `json:"unit"`
}

// ---------- 质控 ----------

type QCLot struct {
	ID          int64   `json:"id"`
	Source      string  `json:"source"`
	AnalyteID   int64   `json:"analyte_id"`
	AnalyteName string  `json:"analyte_name"`
	Lot         string  `json:"lot"`
	Level       string  `json:"level"` // L1/L2/L3
	Mean        float64 `json:"mean"`
	SD          float64 `json:"sd"`
}

type QCResult struct {
	ID          int64     `json:"id"`
	QCLotID     int64     `json:"qc_lot_id"`
	ResultID    int64     `json:"result_id"` // 关联 results 表
	Value       float64   `json:"value"`
	Z           float64   `json:"z"`
	RunAt       time.Time `json:"run_at"`
	Violations  []string  `json:"violations"` // Westgard 规则违反: 1_2s/1_3s/2_2s/R_4s/4_1s/10x
	AnalyteName string    `json:"analyte_name"`
	Lot         string    `json:"lot"`
	Level       string    `json:"level"`
	Mean        float64   `json:"mean"`
	SD          float64   `json:"sd"`
}

// ---------- 解析中间结构 ----------

type ParsedResult struct {
	SampleID   string
	Patient    Patient
	SourceCode string
	SourceName string
	RawValue   string
	RawUnit    string
	InstrFlag  string // 仪器给的异常标记
	Status     string // F/P/C
	TestTime   time.Time
	IsQC       bool
	QCLot      string
	QCLevel    string
}

type ParsedMessage struct {
	Source   string
	Protocol string
	Results  []ParsedResult
	Warnings []string // 容错解析产生的警告(截断/乱码/缺字段等)
}
