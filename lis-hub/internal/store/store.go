package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"lis-hub/internal/model"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS raw_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  received_at TEXT NOT NULL,
  source TEXT NOT NULL,
  protocol TEXT NOT NULL,
  content TEXT NOT NULL,
  encoding TEXT DEFAULT '',
  parse_status TEXT DEFAULT 'ok',
  parse_error TEXT DEFAULT '',
  msg_hash TEXT UNIQUE
);
CREATE TABLE IF NOT EXISTS devices (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT UNIQUE NOT NULL,
  name TEXT DEFAULT '',
  protocol TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS analytes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT UNIQUE NOT NULL,
  name TEXT NOT NULL,
  canonical_unit TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS ref_ranges (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  analyte_id INTEGER NOT NULL REFERENCES analytes(id),
  sex TEXT DEFAULT '',
  age_min REAL DEFAULT 0,
  age_max REAL DEFAULT 200,
  low REAL NOT NULL,
  high REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS mappings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source TEXT NOT NULL,
  source_code TEXT NOT NULL,
  source_name TEXT DEFAULT '',
  source_unit TEXT DEFAULT '',
  analyte_id INTEGER REFERENCES analytes(id),
  factor REAL DEFAULT 1,
  offset REAL DEFAULT 0,
  UNIQUE(source, source_code, source_unit)
);
CREATE TABLE IF NOT EXISTS results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  raw_message_id INTEGER NOT NULL REFERENCES raw_messages(id),
  source TEXT NOT NULL,
  sample_id TEXT DEFAULT '',
  patient_mrn TEXT DEFAULT '',
  patient_name TEXT DEFAULT '',
  patient_sex TEXT DEFAULT '',
  patient_age REAL DEFAULT 0,
  source_code TEXT DEFAULT '',
  source_name TEXT DEFAULT '',
  analyte_id INTEGER REFERENCES analytes(id),
  raw_value TEXT DEFAULT '',
  raw_unit TEXT DEFAULT '',
  value REAL,
  unit TEXT DEFAULT '',
  flag TEXT DEFAULT '',
  ref_low REAL,
  ref_high REAL,
  status TEXT DEFAULT 'F',
  version INTEGER DEFAULT 1,
  supersedes_id INTEGER,
  is_current INTEGER DEFAULT 1,
  dedup_key TEXT UNIQUE,
  test_time TEXT,
  received_at TEXT NOT NULL,
  is_qc INTEGER DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_results_sample ON results(sample_id);
CREATE INDEX IF NOT EXISTS idx_results_analyte ON results(analyte_id);
CREATE INDEX IF NOT EXISTS idx_results_raw ON results(raw_message_id);
CREATE TABLE IF NOT EXISTS critical_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  analyte_id INTEGER NOT NULL REFERENCES analytes(id),
  sex TEXT DEFAULT '',
  age_min REAL DEFAULT 0,
  age_max REAL DEFAULT 200,
  low REAL,
  high REAL
);
CREATE TABLE IF NOT EXISTS critical_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  result_id INTEGER NOT NULL REFERENCES results(id),
  level TEXT NOT NULL,
  status TEXT DEFAULT 'open',
  ack_by TEXT DEFAULT '',
  ack_at TEXT,
  closed_by TEXT DEFAULT '',
  closed_at TEXT,
  note TEXT DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS qc_lots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source TEXT NOT NULL,
  analyte_id INTEGER NOT NULL REFERENCES analytes(id),
  lot TEXT NOT NULL,
  level TEXT DEFAULT '',
  mean REAL NOT NULL,
  sd REAL NOT NULL,
  UNIQUE(source, analyte_id, lot, level)
);
CREATE TABLE IF NOT EXISTS qc_results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  qc_lot_id INTEGER NOT NULL REFERENCES qc_lots(id),
  result_id INTEGER NOT NULL REFERENCES results(id),
  value REAL NOT NULL,
  z REAL NOT NULL,
  run_at TEXT NOT NULL,
  violations TEXT DEFAULT '[]'
);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

const tsLayout = "2006-01-02 15:04:05"

func fmtTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(tsLayout)
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{tsLayout, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func nullTS(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: fmtTS(t), Valid: true}
}

// ---------- raw_messages ----------

func (s *Store) InsertRawMessage(m *model.RawMessage) (int64, bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO raw_messages
		(received_at, source, protocol, content, encoding, parse_status, parse_error, msg_hash)
		VALUES (?,?,?,?,?,?,?,?)`,
		fmtTS(m.ReceivedAt), m.Source, m.Protocol, m.Content, m.Encoding, m.ParseStatus, m.ParseError, m.MsgHash)
	if err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, false, nil // 重复报文
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

func (s *Store) ListRawMessages(limit int) ([]model.RawMessage, error) {
	rows, err := s.db.Query(`SELECT r.id, r.received_at, r.source, r.protocol, r.content, r.encoding,
		r.parse_status, r.parse_error, r.msg_hash,
		(SELECT COUNT(*) FROM results x WHERE x.raw_message_id = r.id)
		FROM raw_messages r ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RawMessage
	for rows.Next() {
		var m model.RawMessage
		var ts string
		if err := rows.Scan(&m.ID, &ts, &m.Source, &m.Protocol, &m.Content, &m.Encoding,
			&m.ParseStatus, &m.ParseError, &m.MsgHash, &m.ResultCount); err != nil {
			return nil, err
		}
		m.ReceivedAt = parseTS(ts)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetRawMessage(id int64) (*model.RawMessage, error) {
	var m model.RawMessage
	var ts string
	err := s.db.QueryRow(`SELECT r.id, r.received_at, r.source, r.protocol, r.content, r.encoding,
		r.parse_status, r.parse_error, r.msg_hash,
		(SELECT COUNT(*) FROM results x WHERE x.raw_message_id = r.id)
		FROM raw_messages r WHERE r.id = ?`, id).
		Scan(&m.ID, &ts, &m.Source, &m.Protocol, &m.Content, &m.Encoding,
			&m.ParseStatus, &m.ParseError, &m.MsgHash, &m.ResultCount)
	m.ReceivedAt = parseTS(ts)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------- analytes / ref ranges ----------

func (s *Store) UpsertAnalyte(a *model.Analyte) (int64, error) {
	_, err := s.db.Exec(`INSERT INTO analytes (code, name, canonical_unit) VALUES (?,?,?)
		ON CONFLICT(code) DO UPDATE SET name=excluded.name, canonical_unit=excluded.canonical_unit`,
		a.Code, a.Name, a.CanonicalUnit)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM analytes WHERE code=?`, a.Code).Scan(&id)
	return id, err
}

func (s *Store) GetAnalyte(id int64) (*model.Analyte, error) {
	var a model.Analyte
	err := s.db.QueryRow(`SELECT id, code, name, canonical_unit FROM analytes WHERE id=?`, id).
		Scan(&a.ID, &a.Code, &a.Name, &a.CanonicalUnit)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListAnalytes() ([]model.Analyte, error) {
	rows, err := s.db.Query(`SELECT id, code, name, canonical_unit FROM analytes ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Analyte
	for rows.Next() {
		var a model.Analyte
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.CanonicalUnit); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AddRefRange(r *model.RefRange) (int64, error) {
	if r.AgeMax == 0 {
		r.AgeMax = 200
	}
	res, err := s.db.Exec(`INSERT INTO ref_ranges (analyte_id, sex, age_min, age_max, low, high) VALUES (?,?,?,?,?,?)`,
		r.AnalyteID, r.Sex, r.AgeMin, r.AgeMax, r.Low, r.High)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListRefRanges() ([]model.RefRange, error) {
	rows, err := s.db.Query(`SELECT id, analyte_id, sex, age_min, age_max, low, high FROM ref_ranges ORDER BY analyte_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RefRange
	for rows.Next() {
		var r model.RefRange
		if err := rows.Scan(&r.ID, &r.AnalyteID, &r.Sex, &r.AgeMin, &r.AgeMax, &r.Low, &r.High); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FindRefRange 按性别年龄取参考范围
func (s *Store) FindRefRange(analyteID int64, sex string, age float64) (low, high float64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT low, high FROM ref_ranges
		WHERE analyte_id=? AND (sex='' OR sex=?) AND age_min<=? AND age_max>=?
		ORDER BY (CASE WHEN sex=? THEN 0 ELSE 1 END), (age_max-age_min) LIMIT 1`,
		analyteID, sex, age, age, sex).Scan(&low, &high)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return low, high, true, nil
}

// ---------- mappings ----------

func (s *Store) ListMappings() ([]model.Mapping, error) {
	rows, err := s.db.Query(`SELECT id, source, source_code, source_name, source_unit, analyte_id, factor, offset
		FROM mappings ORDER BY source, source_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Mapping
	for rows.Next() {
		var m model.Mapping
		var aid sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Source, &m.SourceCode, &m.SourceName, &m.SourceUnit, &aid, &m.Factor, &m.Offset); err != nil {
			return nil, err
		}
		if aid.Valid {
			m.AnalyteID = &aid.Int64
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) FindMapping(source, code, unit string) (*model.Mapping, error) {
	// 优先精确匹配(含单位), 退化为不含单位的匹配
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT id, source, source_code, source_name, source_unit, analyte_id, factor, offset FROM mappings
		  WHERE source=? AND source_code=? AND source_unit=?`, []any{source, code, unit}},
		{`SELECT id, source, source_code, source_name, source_unit, analyte_id, factor, offset FROM mappings
		  WHERE source=? AND source_code=? AND (source_unit='' OR source_unit IS NULL) ORDER BY source_unit DESC LIMIT 1`, []any{source, code}},
	} {
		var m model.Mapping
		var aid sql.NullInt64
		err := s.db.QueryRow(q.sql, q.args...).Scan(&m.ID, &m.Source, &m.SourceCode, &m.SourceName, &m.SourceUnit, &aid, &m.Factor, &m.Offset)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if aid.Valid {
			m.AnalyteID = &aid.Int64
		}
		return &m, nil
	}
	return nil, nil
}

// EnsureMapping 未知项目自动登记一条未映射记录, 便于在控制台维护
func (s *Store) EnsureMapping(source, code, name, unit string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO mappings (source, source_code, source_name, source_unit, factor, offset)
		VALUES (?,?,?,?,1,0)`, source, code, name, unit)
	return err
}

func (s *Store) SaveMapping(m *model.Mapping) error {
	if m.ID > 0 {
		var aid any
		if m.AnalyteID != nil {
			aid = *m.AnalyteID
		}
		_, err := s.db.Exec(`UPDATE mappings SET source=?, source_code=?, source_name=?, source_unit=?, analyte_id=?, factor=?, offset=? WHERE id=?`,
			m.Source, m.SourceCode, m.SourceName, m.SourceUnit, aid, m.Factor, m.Offset, m.ID)
		return err
	}
	var aid any
	if m.AnalyteID != nil {
		aid = *m.AnalyteID
	}
	_, err := s.db.Exec(`INSERT INTO mappings (source, source_code, source_name, source_unit, analyte_id, factor, offset)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(source, source_code, source_unit) DO UPDATE SET
		source_name=excluded.source_name, analyte_id=excluded.analyte_id, factor=excluded.factor, offset=excluded.offset`,
		m.Source, m.SourceCode, m.SourceName, m.SourceUnit, aid, m.Factor, m.Offset)
	return err
}

func (s *Store) DeleteMapping(id int64) error {
	_, err := s.db.Exec(`DELETE FROM mappings WHERE id=?`, id)
	return err
}

// ---------- results ----------

func (s *Store) InsertResult(r *model.Result) (int64, bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO results
		(raw_message_id, source, sample_id, patient_mrn, patient_name, patient_sex, patient_age,
		 source_code, source_name, analyte_id, raw_value, raw_unit, value, unit, flag,
		 ref_low, ref_high, status, version, supersedes_id, is_current, dedup_key,
		 test_time, received_at, is_qc, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.RawMessageID, r.Source, r.SampleID, r.PatientMRN, r.PatientName, r.PatientSex, r.PatientAge,
		r.SourceCode, r.SourceName, r.AnalyteID, r.RawValue, r.RawUnit, r.Value, r.Unit, r.Flag,
		r.RefLow, r.RefHigh, r.Status, r.Version, r.SupersedesID, boolInt(r.IsCurrent), r.DedupKey,
		nullTS(r.TestTime), fmtTS(r.ReceivedAt), boolInt(r.IsQC), fmtTS(r.CreatedAt))
	if err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, false, nil // 重复发送, 去重
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FindCurrentResult 找同一样本同一项目的当前版本(更正时挂链)
func (s *Store) FindCurrentResult(source, sampleID, sourceCode string) (*model.Result, error) {
	rows, err := s.db.Query(`SELECT `+resultCols+resultJoin+`
		WHERE r.source=? AND r.sample_id=? AND r.source_code=? AND r.is_current=1
		ORDER BY r.version DESC LIMIT 1`, source, sampleID, sourceCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	r, err := scanResult(rows)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) MarkSuperseded(id int64) error {
	_, err := s.db.Exec(`UPDATE results SET is_current=0 WHERE id=?`, id)
	return err
}

const resultCols = `r.id, r.raw_message_id, r.source, r.sample_id, r.patient_mrn, r.patient_name,
	r.patient_sex, r.patient_age, r.source_code, r.source_name, r.analyte_id,
	COALESCE(a.code,''), COALESCE(a.name,''),
	r.raw_value, r.raw_unit, r.value, r.unit, r.flag, r.ref_low, r.ref_high,
	r.status, r.version, r.supersedes_id, r.is_current, r.dedup_key,
	r.test_time, r.received_at, r.is_qc, r.created_at`

const resultJoin = ` FROM results r LEFT JOIN analytes a ON a.id = r.analyte_id `

type scanner interface {
	Scan(dest ...any) error
}

func scanResult(row scanner) (model.Result, error) {
	var r model.Result
	var aid, sup sql.NullInt64
	var val, refLo, refHi sql.NullFloat64
	var testTime, recvAt, createdAt sql.NullString
	var isCur, isQC int
	err := row.Scan(&r.ID, &r.RawMessageID, &r.Source, &r.SampleID, &r.PatientMRN, &r.PatientName,
		&r.PatientSex, &r.PatientAge, &r.SourceCode, &r.SourceName, &aid,
		&r.AnalyteCode, &r.AnalyteName,
		&r.RawValue, &r.RawUnit, &val, &r.Unit, &r.Flag, &refLo, &refHi,
		&r.Status, &r.Version, &sup, &isCur, &r.DedupKey,
		&testTime, &recvAt, &isQC, &createdAt)
	if err != nil {
		return r, err
	}
	if aid.Valid {
		r.AnalyteID = &aid.Int64
	}
	if sup.Valid {
		r.SupersedesID = &sup.Int64
	}
	if val.Valid {
		r.Value = &val.Float64
	}
	if refLo.Valid {
		r.RefLow = &refLo.Float64
	}
	if refHi.Valid {
		r.RefHigh = &refHi.Float64
	}
	r.TestTime = parseTS(testTime.String)
	r.ReceivedAt = parseTS(recvAt.String)
	r.CreatedAt = parseTS(createdAt.String)
	r.IsCurrent = isCur == 1
	r.IsQC = isQC == 1
	return r, nil
}

type ResultFilter struct {
	SampleID   string
	PatientMRN string
	Analyte    string // 标准编码或名
	Source     string
	Current    bool
	Limit      int
}

func (s *Store) QueryResults(f ResultFilter) ([]model.Result, error) {
	q := `SELECT ` + resultCols + resultJoin + `WHERE 1=1`
	var args []any
	if f.Current {
		q += ` AND r.is_current=1`
	}
	if f.SampleID != "" {
		q += ` AND r.sample_id LIKE ?`
		args = append(args, "%"+f.SampleID+"%")
	}
	if f.PatientMRN != "" {
		q += ` AND (r.patient_mrn LIKE ? OR r.patient_name LIKE ?)`
		args = append(args, "%"+f.PatientMRN+"%", "%"+f.PatientMRN+"%")
	}
	if f.Analyte != "" {
		q += ` AND (a.code LIKE ? OR a.name LIKE ? OR r.source_code LIKE ?)`
		args = append(args, "%"+f.Analyte+"%", "%"+f.Analyte+"%", "%"+f.Analyte+"%")
	}
	if f.Source != "" {
		q += ` AND r.source=?`
		args = append(args, f.Source)
	}
	q += ` ORDER BY r.id DESC LIMIT ?`
	if f.Limit <= 0 {
		f.Limit = 200
	}
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ResultsByRawMessage(rawID int64) ([]model.Result, error) {
	rows, err := s.db.Query(`SELECT `+resultCols+resultJoin+`WHERE r.raw_message_id=? ORDER BY r.id`, rawID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResultHistory 更正链历史(含被替代版本)
func (s *Store) ResultHistory(resultID int64) ([]model.Result, error) {
	// 找到链头: 沿 supersedes_id 向下, 或直接用 sample+code 全部版本
	var sampleID, sourceCode, source string
	err := s.db.QueryRow(`SELECT sample_id, source_code, source FROM results WHERE id=?`, resultID).
		Scan(&sampleID, &sourceCode, &source)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+resultCols+resultJoin+`
		WHERE r.source=? AND r.sample_id=? AND r.source_code=? ORDER BY r.version`, source, sampleID, sourceCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- critical ----------

func (s *Store) AddCriticalRule(r *model.CriticalRule) (int64, error) {
	if r.AgeMax == 0 {
		r.AgeMax = 200
	}
	res, err := s.db.Exec(`INSERT INTO critical_rules (analyte_id, sex, age_min, age_max, low, high) VALUES (?,?,?,?,?,?)`,
		r.AnalyteID, r.Sex, r.AgeMin, r.AgeMax, r.Low, r.High)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListCriticalRules() ([]model.CriticalRule, error) {
	rows, err := s.db.Query(`SELECT id, analyte_id, sex, age_min, age_max, low, high FROM critical_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CriticalRule
	for rows.Next() {
		var r model.CriticalRule
		if err := rows.Scan(&r.ID, &r.AnalyteID, &r.Sex, &r.AgeMin, &r.AgeMax, &r.Low, &r.High); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MatchCritical 返回命中级别: "low"/"high"/""
func (s *Store) MatchCritical(analyteID int64, sex string, age, value float64) (string, error) {
	rows, err := s.db.Query(`SELECT low, high FROM critical_rules
		WHERE analyte_id=? AND (sex='' OR sex=?) AND age_min<=? AND age_max>=?`,
		analyteID, sex, age, age)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var lo, hi sql.NullFloat64
		if err := rows.Scan(&lo, &hi); err != nil {
			return "", err
		}
		if lo.Valid && value < lo.Float64 {
			return "low", nil
		}
		if hi.Valid && value > hi.Float64 {
			return "high", nil
		}
	}
	return "", nil
}

func (s *Store) CreateCriticalEvent(resultID int64, level string) (int64, error) {
	// 同一结果只建一次
	var existing int64
	err := s.db.QueryRow(`SELECT id FROM critical_events WHERE result_id=?`, resultID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	res, err := s.db.Exec(`INSERT INTO critical_events (result_id, level, status, created_at) VALUES (?,?,'open',?)`,
		resultID, level, fmtTS(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListCriticalEvents(status string) ([]model.CriticalEvent, error) {
	q := `SELECT e.id, e.result_id, e.level, e.status, e.ack_by, e.ack_at, e.closed_by, e.closed_at, e.note, e.created_at,
		r.sample_id, r.patient_name, COALESCE(a.name, r.source_name), r.value, r.unit
		FROM critical_events e
		JOIN results r ON r.id = e.result_id
		LEFT JOIN analytes a ON a.id = r.analyte_id`
	var args []any
	if status != "" {
		q += ` WHERE e.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY e.id DESC LIMIT 200`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CriticalEvent
	for rows.Next() {
		var e model.CriticalEvent
		var ackAt, closedAt, createdAt sql.NullString
		if err := rows.Scan(&e.ID, &e.ResultID, &e.Level, &e.Status, &e.AckBy, &ackAt, &e.ClosedBy, &closedAt, &e.Note, &createdAt,
			&e.SampleID, &e.PatientName, &e.AnalyteName, &e.Value, &e.Unit); err != nil {
			return nil, err
		}
		if ackAt.Valid {
			t := parseTS(ackAt.String)
			e.AckAt = &t
		}
		if closedAt.Valid {
			t := parseTS(closedAt.String)
			e.ClosedAt = &t
		}
		e.CreatedAt = parseTS(createdAt.String)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AckCritical(id int64, by, note string) error {
	_, err := s.db.Exec(`UPDATE critical_events SET status='ack', ack_by=?, ack_at=?, note=? WHERE id=? AND status='open'`,
		by, fmtTS(time.Now()), note, id)
	return err
}

func (s *Store) CloseCritical(id int64, by, note string) error {
	_, err := s.db.Exec(`UPDATE critical_events SET status='closed', closed_by=?, closed_at=?,
		note = CASE WHEN ?='' THEN note ELSE note || char(10) || ? END WHERE id=? AND status IN ('open','ack')`,
		by, fmtTS(time.Now()), note, note, id)
	return err
}

// ---------- QC ----------

func (s *Store) UpsertQCLot(l *model.QCLot) (int64, error) {
	_, err := s.db.Exec(`INSERT INTO qc_lots (source, analyte_id, lot, level, mean, sd) VALUES (?,?,?,?,?,?)
		ON CONFLICT(source, analyte_id, lot, level) DO UPDATE SET mean=excluded.mean, sd=excluded.sd`,
		l.Source, l.AnalyteID, l.Lot, l.Level, l.Mean, l.SD)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM qc_lots WHERE source=? AND analyte_id=? AND lot=? AND level=?`,
		l.Source, l.AnalyteID, l.Lot, l.Level).Scan(&id)
	return id, err
}

func (s *Store) ListQCLots() ([]model.QCLot, error) {
	rows, err := s.db.Query(`SELECT q.id, q.source, q.analyte_id, COALESCE(a.name,''), q.lot, q.level, q.mean, q.sd
		FROM qc_lots q LEFT JOIN analytes a ON a.id=q.analyte_id ORDER BY q.source, q.lot`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QCLot
	for rows.Next() {
		var l model.QCLot
		if err := rows.Scan(&l.ID, &l.Source, &l.AnalyteID, &l.AnalyteName, &l.Lot, &l.Level, &l.Mean, &l.SD); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) FindQCLot(source string, analyteID int64, lot, level string) (*model.QCLot, error) {
	var l model.QCLot
	err := s.db.QueryRow(`SELECT q.id, q.source, q.analyte_id, COALESCE(a.name,''), q.lot, q.level, q.mean, q.sd
		FROM qc_lots q LEFT JOIN analytes a ON a.id=q.analyte_id
		WHERE q.source=? AND q.analyte_id=? AND q.lot=? AND q.level=?`,
		source, analyteID, lot, level).
		Scan(&l.ID, &l.Source, &l.AnalyteID, &l.AnalyteName, &l.Lot, &l.Level, &l.Mean, &l.SD)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *Store) InsertQCResult(q *model.QCResult, violations string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO qc_results (qc_lot_id, result_id, value, z, run_at, violations) VALUES (?,?,?,?,?,?)`,
		q.QCLotID, q.ResultID, q.Value, q.Z, fmtTS(q.RunAt), violations)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecentQC 取某批次最近 N 条(含本次之前的历史, 用于 Westgard 多规则)
func (s *Store) RecentQC(lotID int64, limit int) ([]model.QCResult, error) {
	rows, err := s.db.Query(`SELECT id, qc_lot_id, result_id, value, z, run_at, violations FROM qc_results
		WHERE qc_lot_id=? ORDER BY run_at DESC, id DESC LIMIT ?`, lotID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QCResult
	for rows.Next() {
		var q model.QCResult
		var v, runAt string
		if err := rows.Scan(&q.ID, &q.QCLotID, &q.ResultID, &q.Value, &q.Z, &runAt, &v); err != nil {
			return nil, err
		}
		q.RunAt = parseTS(runAt)
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) ListQCResults(limit int) ([]model.QCResult, error) {
	rows, err := s.db.Query(`SELECT q.id, q.qc_lot_id, q.result_id, q.value, q.z, q.run_at, q.violations,
		COALESCE(a.name,''), l.lot, l.level, l.mean, l.sd
		FROM qc_results q
		JOIN qc_lots l ON l.id=q.qc_lot_id
		LEFT JOIN analytes a ON a.id=l.analyte_id
		ORDER BY q.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QCResult
	for rows.Next() {
		var q model.QCResult
		var v, runAt string
		if err := rows.Scan(&q.ID, &q.QCLotID, &q.ResultID, &q.Value, &q.Z, &runAt, &v,
			&q.AnalyteName, &q.Lot, &q.Level, &q.Mean, &q.SD); err != nil {
			return nil, err
		}
		q.RunAt = parseTS(runAt)
		out = append(out, q)
	}
	return out, rows.Err()
}

// ---------- 统计 ----------

type TATStat struct {
	Source      string  `json:"source"`
	AnalyteName string  `json:"analyte_name"`
	Count       int     `json:"count"`
	AvgMinutes  float64 `json:"avg_minutes"`
	P50Minutes  float64 `json:"p50_minutes"`
	P90Minutes  float64 `json:"p90_minutes"`
	MaxMinutes  float64 `json:"max_minutes"`
}

func (s *Store) TATStats() ([]TATStat, error) {
	// TAT = received_at - test_time (分钟), 只统计有检测时间的常规结果
	rows, err := s.db.Query(`SELECT r.source, COALESCE(a.name, r.source_name),
		(julianday(r.received_at) - julianday(r.test_time)) * 1440.0 AS tat
		FROM results r LEFT JOIN analytes a ON a.id=r.analyte_id
		WHERE r.is_current=1 AND r.is_qc=0 AND r.test_time IS NOT NULL AND r.test_time != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ src, name string }
	groups := map[key][]float64{}
	for rows.Next() {
		var src, name string
		var tat float64
		if err := rows.Scan(&src, &name, &tat); err != nil {
			return nil, err
		}
		if tat < 0 || tat > 60*24*30 { // 过滤明显异常
			continue
		}
		groups[key{src, name}] = append(groups[key{src, name}], tat)
	}
	var out []TATStat
	for k, v := range groups {
		out = append(out, TATStat{
			Source: k.src, AnalyteName: k.name, Count: len(v),
			AvgMinutes: avg(v), P50Minutes: pct(v, 0.5), P90Minutes: pct(v, 0.9), MaxMinutes: max(v),
		})
	}
	return out, nil
}

func avg(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func pct(v []float64, p float64) float64 {
	s := append([]float64(nil), v...)
	// 简单插入排序, 数据量小
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	idx := int(p * float64(len(s)-1))
	return s[idx]
}

func max(v []float64) float64 {
	m := v[0]
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}
