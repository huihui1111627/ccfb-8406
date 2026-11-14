package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"lis-hub/internal/engine"
	"lis-hub/internal/model"
	"lis-hub/internal/parser"
	"lis-hub/internal/store"
)

type Server struct {
	st  *store.Store
	en  *engine.Engine
	mux *http.ServeMux
}

func New(st *store.Store, en *engine.Engine) *Server {
	s := &Server{st: st, en: en, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// 接入
	s.mux.HandleFunc("POST /api/ingest", s.handleIngest)
	s.mux.HandleFunc("POST /api/import/excel", s.handleExcelImport)
	s.mux.HandleFunc("GET /api/messages", s.handleListMessages)
	s.mux.HandleFunc("GET /api/messages/{id}", s.handleGetMessage)
	// 主数据
	s.mux.HandleFunc("GET /api/analytes", s.handleListAnalytes)
	s.mux.HandleFunc("POST /api/analytes", s.handleSaveAnalyte)
	s.mux.HandleFunc("GET /api/refranges", s.handleListRefRanges)
	s.mux.HandleFunc("POST /api/refranges", s.handleAddRefRange)
	s.mux.HandleFunc("GET /api/mappings", s.handleListMappings)
	s.mux.HandleFunc("POST /api/mappings", s.handleSaveMapping)
	s.mux.HandleFunc("DELETE /api/mappings/{id}", s.handleDeleteMapping)
	// 结果
	s.mux.HandleFunc("GET /api/results", s.handleQueryResults)
	s.mux.HandleFunc("GET /api/results/{id}/history", s.handleResultHistory)
	// 危急值
	s.mux.HandleFunc("GET /api/critical", s.handleListCritical)
	s.mux.HandleFunc("GET /api/critical/rules", s.handleListCriticalRules)
	s.mux.HandleFunc("POST /api/critical/rules", s.handleAddCriticalRule)
	s.mux.HandleFunc("POST /api/critical/{id}/ack", s.handleAckCritical)
	s.mux.HandleFunc("POST /api/critical/{id}/close", s.handleCloseCritical)
	// 质控
	s.mux.HandleFunc("GET /api/qc/lots", s.handleListQCLots)
	s.mux.HandleFunc("POST /api/qc/lots", s.handleSaveQCLot)
	s.mux.HandleFunc("GET /api/qc/results", s.handleListQCResults)
	// 统计
	s.mux.HandleFunc("GET /api/stats/tat", s.handleTAT)
	// 控制台静态页
	s.mux.HandleFunc("GET /", s.handleIndex)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---------- 接入 ----------

// POST /api/ingest?source=X&protocol=hl7|astm  body=原始报文
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	protocol := r.URL.Query().Get("protocol")
	source := r.URL.Query().Get("source")
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeErr(w, 400, "读取报文失败")
		return
	}
	if protocol == "" {
		// 嗅探
		trim := strings.TrimLeft(string(body), "\x0b\x05 \t\r\n")
		if strings.HasPrefix(trim, "MSH") {
			protocol = "hl7"
		} else if strings.Contains(string(body), "\x02") || strings.HasPrefix(trim, "H|") {
			protocol = "astm"
		} else {
			writeErr(w, 400, "无法识别协议, 请显式指定 protocol=hl7|astm")
			return
		}
	}
	rawID, outcomes, err := s.en.Ingest(body, source, protocol)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"raw_message_id": rawID, "outcomes": outcomes})
}

// POST /api/import/excel?source=X  multipart file=
func (s *Server) handleExcelImport(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "excel-import"
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "解析表单失败")
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少文件字段 file")
		return
	}
	defer f.Close()
	pm, err := parser.ParseExcel(f, source)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// 原始内容留底: 记录文件名+解析摘要(二进制xlsx不适合直接存文本)
	rawText := "Excel导入: " + fh.Filename + "\n" + strings.Join(pm.Warnings, "\n")
	rawID, outcomes, err := s.en.IngestParsed(pm, rawText, source)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"raw_message_id": rawID, "warnings": pm.Warnings, "outcomes": outcomes})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	ms, err := s.st.ListRawMessages(limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ms)
}

// GET /api/messages/{id} — 原始报文 + 标准化结果对照
func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	m, err := s.st.GetRawMessage(id)
	if err != nil {
		writeErr(w, 404, "报文不存在")
		return
	}
	results, err := s.st.ResultsByRawMessage(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"message": m, "results": results})
}

// ---------- 主数据 ----------

func (s *Server) handleListAnalytes(w http.ResponseWriter, r *http.Request) {
	as, err := s.st.ListAnalytes()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, as)
}

func (s *Server) handleSaveAnalyte(w http.ResponseWriter, r *http.Request) {
	var a model.Analyte
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeErr(w, 400, "JSON格式错误")
		return
	}
	if a.Code == "" || a.Name == "" {
		writeErr(w, 400, "编码和名称必填")
		return
	}
	id, err := s.st.UpsertAnalyte(&a)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) handleListRefRanges(w http.ResponseWriter, r *http.Request) {
	rs, err := s.st.ListRefRanges()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rs)
}

func (s *Server) handleAddRefRange(w http.ResponseWriter, r *http.Request) {
	var rr model.RefRange
	if err := json.NewDecoder(r.Body).Decode(&rr); err != nil {
		writeErr(w, 400, "JSON格式错误")
		return
	}
	id, err := s.st.AddRefRange(&rr)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) handleListMappings(w http.ResponseWriter, r *http.Request) {
	ms, err := s.st.ListMappings()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ms)
}

func (s *Server) handleSaveMapping(w http.ResponseWriter, r *http.Request) {
	var m model.Mapping
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, 400, "JSON格式错误")
		return
	}
	if m.Source == "" || m.SourceCode == "" {
		writeErr(w, 400, "来源和项目代码必填")
		return
	}
	if err := s.st.SaveMapping(&m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteMapping(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.st.DeleteMapping(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ---------- 结果 ----------

func (s *Server) handleQueryResults(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.ResultFilter{
		SampleID:   q.Get("sample_id"),
		PatientMRN: q.Get("patient"),
		Analyte:    q.Get("analyte"),
		Source:     q.Get("source"),
		Current:    q.Get("all") != "1",
		Limit:      limit,
	}
	rs, err := s.st.QueryResults(f)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rs)
}

func (s *Server) handleResultHistory(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rs, err := s.st.ResultHistory(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rs)
}

// ---------- 危急值 ----------

func (s *Server) handleListCritical(w http.ResponseWriter, r *http.Request) {
	es, err := s.st.ListCriticalEvents(r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, es)
}

func (s *Server) handleListCriticalRules(w http.ResponseWriter, r *http.Request) {
	rs, err := s.st.ListCriticalRules()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rs)
}

func (s *Server) handleAddCriticalRule(w http.ResponseWriter, r *http.Request) {
	var cr model.CriticalRule
	if err := json.NewDecoder(r.Body).Decode(&cr); err != nil {
		writeErr(w, 400, "JSON格式错误")
		return
	}
	id, err := s.st.AddCriticalRule(&cr)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) handleAckCritical(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		By   string `json:"by"`
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.By == "" {
		body.By = "anonymous"
	}
	if err := s.st.AckCritical(id, body.By, body.Note); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ack"})
}

func (s *Server) handleCloseCritical(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		By   string `json:"by"`
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.By == "" {
		body.By = "anonymous"
	}
	if err := s.st.CloseCritical(id, body.By, body.Note); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "closed"})
}

// ---------- 质控 ----------

func (s *Server) handleListQCLots(w http.ResponseWriter, r *http.Request) {
	ls, err := s.st.ListQCLots()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ls)
}

func (s *Server) handleSaveQCLot(w http.ResponseWriter, r *http.Request) {
	var l model.QCLot
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		writeErr(w, 400, "JSON格式错误")
		return
	}
	id, err := s.st.UpsertQCLot(&l)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) handleListQCResults(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	qs, err := s.st.ListQCResults(limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, qs)
}

// ---------- 统计 ----------

func (s *Server) handleTAT(w http.ResponseWriter, r *http.Request) {
	stats, err := s.st.TATStats()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, stats)
}
