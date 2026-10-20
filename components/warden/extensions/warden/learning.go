package warden

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Smart hold learning (src/learning.ts): every hold and every judged allowed call is written to a SQLite database
// with its context, and what the user did next labels it. The database is the file the original writes (holds.db
// beside the user config, WAL), so history carries over; PIGPEN_WARDEN_DB (the original's PI_WARDEN_DB, D6) overrides
// the path. The engine is behind sqlStore (sqlite_store.go).

const learningDBEnv = "PIGPEN_WARDEN_DB"

// HoldsSchema creates the holds table and its indexes (HOLDS_SCHEMA).
const HoldsSchema = `
  CREATE TABLE IF NOT EXISTS holds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    project_root TEXT NOT NULL,
    tool TEXT NOT NULL,
    signature_hash TEXT NOT NULL,
    command_preview TEXT,
    task TEXT,
    plan TEXT,
    context_summary TEXT,
    preceding_actions TEXT,
    scores TEXT NOT NULL,
    level TEXT NOT NULL,
    held INTEGER NOT NULL,
    reasons TEXT,
    agent_reason TEXT,
    outcome TEXT,
    outcome_at INTEGER,
    confidence REAL
  );
  CREATE INDEX IF NOT EXISTS idx_holds_project_signature ON holds(project_root, signature_hash);
  CREATE INDEX IF NOT EXISTS idx_holds_outcome ON holds(outcome);
  CREATE INDEX IF NOT EXISTS idx_holds_timestamp ON holds(timestamp);
`

// dbEntry is one connection per resolved path; concurrent first calls share one open. A failed open is remembered
// as the NOOP database so later calls neither warn again nor retry forever.
type dbEntry struct {
	once sync.Once
	db   sqlStore
}

var learningDBs sync.Map // path -> *dbEntry

// Learning is the hold-learning store at one database path.
type Learning struct {
	path string
	// Warn receives the module's console warnings; nil drops them.
	Warn func(string)
}

// LearningPath is the database file: PIGPEN_WARDEN_DB, else holds.db in warden's data folder.
func LearningPath(dataDir string, getenv func(string) string) string {
	if p := getenv(learningDBEnv); p != "" {
		return p
	}
	return filepath.Join(dataDir, "holds.db")
}

// NewLearning returns the store for a database path.
func NewLearning(path string, warn func(string)) *Learning { return &Learning{path: path, Warn: warn} }

func (l *Learning) warn(msg string) {
	if l.Warn != nil {
		l.Warn(msg)
	}
}

// open returns the shared connection, or nil (the NOOP database) when the file cannot be opened.
func (l *Learning) open() sqlStore {
	v, _ := learningDBs.LoadOrStore(l.path, &dbEntry{})
	e := v.(*dbEntry)
	e.once.Do(func() {
		db, err := openSQLite(l.path)
		if err != nil {
			l.warn("pi-warden: could not open " + l.path + ": " + err.Error())
			return
		}
		e.db = db
	})
	return e.db
}

// CloseLearningDatabases closes every open connection and forgets them (tests, shutdown).
func CloseLearningDatabases() {
	learningDBs.Range(func(k, v any) bool {
		if e := v.(*dbEntry); e.db != nil {
			_ = e.db.close()
		}
		learningDBs.Delete(k)
		return true
	})
}

// InitSchema creates the table, migrates older files, and prunes records older than retentionDays (0 keeps all).
// Failures are warned about, never raised.
func (l *Learning) InitSchema(retentionDays int) {
	d := l.open()
	if d == nil {
		return
	}
	err := func() error {
		if _, _, err := d.exec(HoldsSchema); err != nil {
			return err
		}
		// Migrate: add columns that may be missing from older databases (the column exists on current ones).
		_, _, _ = d.exec("ALTER TABLE holds ADD COLUMN preceding_actions TEXT")
		if retentionDays > 0 {
			cutoff := time.Now().UnixMilli() - int64(retentionDays)*86_400_000
			changes, _, err := d.exec("DELETE FROM holds WHERE timestamp < ?", cutoff)
			if err != nil {
				return err
			}
			if changes > 0 {
				if _, _, err := d.exec("VACUUM"); err != nil {
					return err
				}
			}
		}
		return nil
	}()
	if err != nil {
		code := ""
		var coder interface{ Code() int }
		if errors.As(err, &coder) {
			code = " (errcode " + strconv.Itoa(coder.Code()) + ")"
		}
		l.warn("pi-warden: hold retention prune failed:" + code + " " + err.Error())
	}
}

// HoldScores are the scores a hold is signed and stored with.
type HoldScores struct {
	Irreversible float64  `json:"irreversible"`
	Reasons      []string `json:"reasons"`
	// LargeOutput is the large_output score of a judged bash call; recorded for calibration, never part of the signature.
	LargeOutput *float64 `json:"largeOutput,omitempty"`
}

// HoldContext are the fields gathered at hold time, passed to ToHoldRecord.
type HoldContext struct {
	Task, Plan, ContextSummary, PrecedingActions, AgentReason string
	// Preview is the redacted command or path, capped at 200 chars; nil stores the bare tool name.
	Preview *string
}

// HoldRecord is one row to insert.
type HoldRecord struct {
	Timestamp        int64
	ProjectRoot      string
	Tool             string
	CommandPreview   string
	Task             string
	Plan             string
	ContextSummary   string
	PrecedingActions string
	Scores           HoldScores
	Level            string
	Held             bool
	Reasons          []string
	AgentReason      string
	Confidence       *float64
}

// SmartHistory is what past holds say about a call.
type SmartHistory struct {
	Exact, Similar, SameReason []Row
	SignatureHash              string
}

// ConfidenceResult is the weighted approval ratio of a history.
type ConfidenceResult struct {
	Confidence                                float64
	Reason                                    string
	ExactCount, SimilarCount, SameReasonCount int
}

// SkipResult says whether a hold could be skipped.
type SkipResult struct {
	Skip       bool
	Confidence float64
	Reason     string
}

func jsonStrings(s []string) string {
	b, err := marshalNoEscape(nonNilStrings(s))
	if err != nil {
		return "[]"
	}
	return string(b)
}

func parseStringArray(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SignatureHash identifies similar holds: sha256 of the tool and scores, 16 hex characters.
func SignatureHash(tool string, scores HoldScores) string {
	b, _ := marshalNoEscape(struct {
		Irreversible float64  `json:"irreversible"`
		Reasons      []string `json:"reasons"`
	}{scores.Irreversible, nonNilStrings(scores.Reasons)})
	sum := sha256.Sum256([]byte(tool + ":" + string(b)))
	return hex.EncodeToString(sum[:])[:16]
}

// rowNumber reads a numeric column as float64 (JavaScript's typeof number).
func rowNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// scoreRows scores a batch of rows with time-decayed weights: recent holds (under a week) weigh fully, medium-age
// (under 4 weeks) 70%, older 40%. Approved outcomes add the weight; replanned subtract half (the user changed
// their mind).
func scoreRows(rows []Row, weight float64) (score, totalWeight float64) {
	now := float64(time.Now().UnixMilli())
	week := float64(7 * 24 * 60 * 60 * 1000)
	for _, row := range rows {
		ts, ok := rowNumber(row["timestamp"])
		outcome, isString := row["outcome"].(string)
		if !ok || !isString {
			continue
		}
		age := now - ts
		ageWeight := 0.4
		if age < week {
			ageWeight = 1.0
		} else if age < 4*week {
			ageWeight = 0.7
		}
		w := weight * ageWeight
		totalWeight += w
		if outcome == "approved" {
			score += w
		}
		if outcome == "replanned" {
			score -= w * 0.5
		}
	}
	return score, totalWeight
}

// HoldItem is the held-call data ToHoldRecord maps.
type HoldItem struct {
	At      int64
	Tool    string
	Level   string
	Reasons []string
	Scores  *CallScores
	// Held defaults to true when nil.
	Held *bool
}

// ToHoldRecord builds the HoldRecord from held call data: the field mapping lives here.
func ToHoldRecord(item HoldItem, projectRoot string, ctx HoldContext) HoldRecord {
	preview := item.Tool
	if ctx.Preview != nil {
		preview = *ctx.Preview
	}
	scores := HoldScores{Reasons: item.Reasons}
	if item.Scores != nil {
		scores.Irreversible = item.Scores.Irreversible
		scores.LargeOutput = item.Scores.LargeOutput
	}
	held := true
	if item.Held != nil {
		held = *item.Held
	}
	return HoldRecord{
		Timestamp: item.At, ProjectRoot: projectRoot, Tool: item.Tool,
		CommandPreview: utf16Slice(Redact(preview), 0, 200),
		Scores:         scores, Level: item.Level, Held: held, Reasons: item.Reasons,
		Task: ctx.Task, Plan: ctx.Plan, ContextSummary: ctx.ContextSummary, PrecedingActions: ctx.PrecedingActions, AgentReason: ctx.AgentReason,
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RecordHold inserts a hold and returns its id (0 while learning is off).
func (l *Learning) RecordHold(h HoldRecord) (int64, error) {
	d := l.open()
	if d == nil {
		return 0, nil
	}
	hash := SignatureHash(h.Tool, h.Scores)
	scores, err := marshalNoEscape(HoldScores{Irreversible: h.Scores.Irreversible, Reasons: nonNilStrings(h.Scores.Reasons), LargeOutput: h.Scores.LargeOutput})
	if err != nil {
		return 0, err
	}
	held := 0
	if h.Held {
		held = 1
	}
	var confidence any
	if h.Confidence != nil {
		confidence = *h.Confidence
	}
	_, id, err := d.exec(`
    INSERT INTO holds
    (timestamp, project_root, tool, signature_hash, command_preview,
     task, plan, context_summary, preceding_actions,
     scores, level, held, reasons, agent_reason, confidence)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  `, h.Timestamp, h.ProjectRoot, h.Tool, hash, h.CommandPreview,
		nullable(h.Task), nullable(h.Plan), nullable(h.ContextSummary), nullable(h.PrecedingActions),
		string(scores), h.Level, held, jsonStrings(h.Reasons), nullable(h.AgentReason), confidence)
	return id, err
}

// RecordOutcome labels a stored hold. A failure is warned about, never raised.
func (l *Learning) RecordOutcome(id int64, outcome string) {
	d := l.open()
	if d == nil {
		return
	}
	if _, _, err := d.exec("UPDATE holds SET outcome = ?, outcome_at = ? WHERE id = ?", outcome, time.Now().UnixMilli(), id); err != nil {
		l.warn("pi-warden: could not record hold outcome: " + err.Error())
	}
}

// QueryHoldsForProject lists a project's holds by held value (nil: all), oldest first.
func (l *Learning) QueryHoldsForProject(projectRoot string, held *bool) ([]Row, error) {
	d := l.open()
	if d == nil {
		return []Row{}, nil
	}
	if held != nil {
		h := 0
		if *held {
			h = 1
		}
		return d.all("SELECT id, tool, held, outcome, command_preview FROM holds WHERE project_root = ? AND held = ? ORDER BY timestamp", projectRoot, h)
	}
	return d.all("SELECT id, tool, held, outcome, command_preview FROM holds WHERE project_root = ? ORDER BY timestamp", projectRoot)
}

// HoldStats are the lifetime hold statistics of one project root. The precision formula matches holds.ts:
// (declined + replanned) / (approved + declined + replanned).
type HoldStats struct {
	// Held: held = 1 rows for this root.
	Held int
	// Labeled: rows with a label (approved + declined + replanned).
	Labeled int
	// Approved: held rows approved on retry (false positives).
	Approved int
	// Declined and Replanned: held rows declined or replanned (true positives).
	Declined, Replanned int
	// Allowed: held = 0 rows for this root.
	Allowed int
	// Regretted and Accepted: allowed rows the user regretted or accepted.
	Regretted, Accepted int
	// Oldest and Newest: the date range, ms since epoch.
	Oldest, Newest int64
}

func rowInt(r Row, k string) int {
	if f, ok := rowNumber(r[k]); ok {
		return int(f)
	}
	return 0
}

// HoldStats counts one project's rows.
func (l *Learning) HoldStats(projectRoot string) (HoldStats, error) {
	d := l.open()
	if d == nil {
		return HoldStats{}, nil
	}
	held, _, err := d.get(`SELECT
      COUNT(*) AS total,
      COUNT(CASE WHEN outcome IN ('approved','declined','replanned') THEN 1 END) AS labeled,
      COUNT(CASE WHEN outcome = 'approved' THEN 1 END) AS approved,
      COUNT(CASE WHEN outcome = 'declined' THEN 1 END) AS declined,
      COUNT(CASE WHEN outcome = 'replanned' THEN 1 END) AS replanned,
      MIN(timestamp) AS oldest,
      MAX(timestamp) AS newest
    FROM holds WHERE project_root = ? AND held = 1`, projectRoot)
	if err != nil {
		return HoldStats{}, err
	}
	allowed, _, err := d.get(`SELECT
      COUNT(*) AS total,
      COUNT(CASE WHEN outcome = 'regretted' THEN 1 END) AS regretted,
      COUNT(CASE WHEN outcome = 'accepted' THEN 1 END) AS accepted
    FROM holds WHERE project_root = ? AND held = 0`, projectRoot)
	if err != nil {
		return HoldStats{}, err
	}
	return HoldStats{
		Held: rowInt(held, "total"), Labeled: rowInt(held, "labeled"), Approved: rowInt(held, "approved"),
		Declined: rowInt(held, "declined"), Replanned: rowInt(held, "replanned"),
		Allowed: rowInt(allowed, "total"), Regretted: rowInt(allowed, "regretted"), Accepted: rowInt(allowed, "accepted"),
		Oldest: int64(rowInt(held, "oldest")), Newest: int64(rowInt(held, "newest")),
	}, nil
}

// QuerySmartHistory finds past holds like this one: the same signature, a close irreversible score of the same
// tool, and the same reason category.
func (l *Learning) QuerySmartHistory(tool string, scores HoldScores, projectRoot string) (SmartHistory, error) {
	hash := SignatureHash(tool, scores)
	h := SmartHistory{Exact: []Row{}, Similar: []Row{}, SameReason: []Row{}, SignatureHash: hash}
	d := l.open()
	if d == nil {
		return h, nil
	}
	var err error
	if h.Exact, err = d.all(`
    SELECT task, plan, outcome, scores, agent_reason, timestamp
    FROM holds WHERE signature_hash = ? AND project_root = ? AND held = 1
    ORDER BY timestamp DESC LIMIT 10
  `, hash, projectRoot); err != nil {
		return h, err
	}
	if h.Similar, err = d.all(`
    SELECT task, plan, outcome, scores, agent_reason, timestamp
    FROM holds WHERE tool = ? AND held = 1
    AND ABS(CAST(json_extract(scores, '$.irreversible') AS REAL) - ?) < 0.2
    ORDER BY timestamp DESC LIMIT 10
  `, tool, scores.Irreversible); err != nil {
		return h, err
	}
	reasonCat := ""
	if len(scores.Reasons) > 0 {
		reasonCat, _, _ = strings.Cut(scores.Reasons[0], ":")
	}
	if reasonCat != "" {
		if h.SameReason, err = d.all(`
    SELECT task, plan, outcome, scores, agent_reason, timestamp
    FROM holds WHERE held = 1 AND reasons LIKE ?
    ORDER BY timestamp DESC LIMIT 10
  `, "%"+reasonCat+"%"); err != nil {
			return h, err
		}
	}
	return h, nil
}

// CalculateSmartConfidence is the time-decayed approval ratio over the three row sets, as a confidence in [0, 1].
func CalculateSmartConfidence(h SmartHistory) ConfidenceResult {
	exactScore, exactWeight := scoreRows(h.Exact, 5)
	similarScore, similarWeight := scoreRows(h.Similar, 2)
	sameScore, sameWeight := scoreRows(h.SameReason, 1)
	total := exactWeight + similarWeight + sameWeight
	if total == 0 {
		return ConfidenceResult{Reason: "no history"}
	}
	// Normalize the weighted approval ratio to [0, 1]. The raw ratio ranges over [-0.5, 1] (all-replanned to
	// all-approved); the (x+1)/2 transform maps that to [0.25, 1], clamped to [0, 1].
	confidence := math.Max(0, math.Min(1, ((exactScore+similarScore+sameScore)/total+1)/2))
	reason := "low confidence"
	if confidence > 0.7 {
		reason = "high confidence approval"
	} else if confidence > 0.4 {
		reason = "borderline"
	}
	return ConfidenceResult{Confidence: confidence, Reason: reason, ExactCount: len(h.Exact), SimilarCount: len(h.Similar), SameReasonCount: len(h.SameReason)}
}

// ShouldSkipHold says whether history is strong enough to skip a hold; a destructive pattern is never skipped.
func (l *Learning) ShouldSkipHold(tool string, scores HoldScores, projectRoot string) (SkipResult, error) {
	h, err := l.QuerySmartHistory(tool, scores, projectRoot)
	if err != nil {
		return SkipResult{}, err
	}
	c := CalculateSmartConfidence(h)
	for _, r := range scores.Reasons {
		if strings.HasPrefix(r, "destructive:") {
			return SkipResult{Skip: false, Confidence: c.Confidence, Reason: "destructive pattern, never skip"}, nil
		}
	}
	if c.Confidence > 0.8 && len(h.Exact) >= 3 {
		return SkipResult{Skip: true, Confidence: c.Confidence, Reason: "high confidence, " + strconv.Itoa(len(h.Exact)) + " exact approvals"}, nil
	}
	return SkipResult{Skip: false, Confidence: c.Confidence, Reason: c.Reason}, nil
}

// ThresholdAdjustment is a suggested threshold change learned from past outcomes.
type ThresholdAdjustment struct {
	Guard              string
	CurrentThreshold   float64
	SuggestedThreshold float64
	Reason             string
	Confidence         float64
}

func fixed0(x float64) string { return toFixed(x, 0) }

// AnalyzeThresholds suggests threshold adjustments from hold outcomes.
func (l *Learning) AnalyzeThresholds(projectRoot string) ([]ThresholdAdjustment, error) {
	adjustments := []ThresholdAdjustment{}
	d := l.open()
	if d == nil {
		return adjustments, nil
	}
	// Action guard: holds against approvals.
	rows, err := d.all(`
    SELECT outcome, COUNT(*) as cnt
    FROM holds WHERE project_root = ? AND tool != 'rules' AND held = 1
    GROUP BY outcome
  `, projectRoot)
	if err != nil {
		return nil, err
	}
	total, declined, replanned := 0, 0, 0
	for _, r := range rows {
		total += rowInt(r, "cnt")
	}
	// find() takes the first matching group; a NULL outcome group matches none.
	declinedSeen, replannedSeen := false, false
	for _, r := range rows {
		switch r["outcome"] {
		case "declined":
			if !declinedSeen {
				declined, declinedSeen = rowInt(r, "cnt"), true
			}
		case "replanned":
			if !replannedSeen {
				replanned, replannedSeen = rowInt(r, "cnt"), true
			}
		}
	}
	if total >= 10 {
		precision := float64(declined+replanned) / float64(total)
		// Precision above 0.7 means real issues are caught: keep or raise the threshold. Below 0.3 the guard is too
		// aggressive: lower it.
		if precision < 0.3 && total >= 20 {
			adjustments = append(adjustments, ThresholdAdjustment{
				Guard: "action", CurrentThreshold: 0.7, SuggestedThreshold: 0.6,
				Reason:     "Low precision (" + fixed0(precision*100) + "%); consider lowering the confirmation threshold",
				Confidence: math.Min(1, float64(total)/50),
			})
		}
	}
	// Regret rates.
	regret, ok, err := d.get(`
    SELECT 
      COUNT(CASE WHEN outcome = 'regretted' THEN 1 END) as regrets,
      COUNT(CASE WHEN outcome IN ('accepted', 'regretted') THEN 1 END) as total
    FROM holds WHERE project_root = ? AND held = 0
  `, projectRoot)
	if err != nil {
		return nil, err
	}
	if ok && rowInt(regret, "total") >= 10 {
		rate := float64(rowInt(regret, "regrets")) / float64(rowInt(regret, "total"))
		if rate > 0.15 {
			adjustments = append(adjustments, ThresholdAdjustment{
				Guard: "action", CurrentThreshold: 0.7, SuggestedThreshold: 0.75,
				Reason:     "High regret rate (" + fixed0(rate*100) + "%); consider raising the confirmation threshold",
				Confidence: math.Min(1, float64(rowInt(regret, "total"))/30),
			})
		}
	}
	return adjustments, nil
}

// PatternInsight is a pattern category likely to be a false positive.
type PatternInsight struct {
	Pattern           string
	FalsePositiveRate float64
	SampleSize        int
	Suggestion        string
}

// AnalyzePatterns learns which pattern categories are most likely to be false positives.
func (l *Learning) AnalyzePatterns(projectRoot string) ([]PatternInsight, error) {
	insights := []PatternInsight{}
	d := l.open()
	if d == nil {
		return insights, nil
	}
	patterns, err := d.all(`
    SELECT 
      reasons,
      outcome,
      COUNT(*) as cnt
    FROM holds WHERE project_root = ? AND held = 1
    GROUP BY reasons, outcome
  `, projectRoot)
	if err != nil {
		return nil, err
	}
	// Aggregate by pattern category, in first-seen order.
	type tally struct{ approved, declined, total int }
	var order []string
	stats := map[string]*tally{}
	for _, row := range patterns {
		raw, _ := row["reasons"].(string)
		reasons, err := parseStringArray(raw)
		if err != nil {
			return nil, err
		}
		outcome, _ := row["outcome"].(string)
		cnt := rowInt(row, "cnt")
		for _, reason := range reasons {
			category, _, _ := strings.Cut(reason, ":")
			t := stats[category]
			if t == nil {
				t = &tally{}
				stats[category] = t
				order = append(order, category)
			}
			t.total += cnt
			if outcome == "approved" {
				t.approved += cnt
			}
			if outcome == "declined" {
				t.declined += cnt
			}
		}
	}
	for _, pattern := range order {
		t := stats[pattern]
		if t.total >= 5 {
			rate := float64(t.approved) / float64(t.total)
			if rate > 0.5 {
				insights = append(insights, PatternInsight{
					Pattern: pattern, FalsePositiveRate: rate, SampleSize: t.total,
					Suggestion: "Pattern '" + pattern + "' has a " + fixed0(rate*100) + "% false positive rate; consider adding it to exemptRules or raising its threshold",
				})
			}
		}
	}
	sort.SliceStable(insights, func(i, j int) bool { return insights[i].FalsePositiveRate > insights[j].FalsePositiveRate })
	return insights, nil
}

// ContextRecommendation is one line of /warden recommend.
type ContextRecommendation struct {
	Type     string // threshold | exempt | pattern
	Message  string
	Priority string // high | medium | low
}

// GenerateRecommendations turns the learning data into recommendations, high priority first.
func (l *Learning) GenerateRecommendations(projectRoot string) ([]ContextRecommendation, error) {
	recs := []ContextRecommendation{}
	adjustments, err := l.AnalyzeThresholds(projectRoot)
	if err != nil {
		return nil, err
	}
	for _, adj := range adjustments {
		if adj.Confidence > 0.5 {
			priority := "medium"
			if adj.Confidence > 0.7 {
				priority = "high"
			}
			recs = append(recs, ContextRecommendation{Type: "threshold", Message: adj.Reason + " (confidence: " + fixed0(adj.Confidence*100) + "%)", Priority: priority})
		}
	}
	insights, err := l.AnalyzePatterns(projectRoot)
	if err != nil {
		return nil, err
	}
	if len(insights) > 3 {
		insights = insights[:3]
	}
	for _, insight := range insights {
		if insight.FalsePositiveRate > 0.6 {
			priority := "medium"
			if insight.FalsePositiveRate > 0.8 {
				priority = "high"
			}
			recs = append(recs, ContextRecommendation{Type: "exempt", Message: insight.Suggestion, Priority: priority})
		}
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(recs, func(i, j int) bool { return rank[recs[i].Priority] < rank[recs[j].Priority] })
	return recs, nil
}

// SteerTypeStats is the effectiveness of one steer type.
type SteerTypeStats struct {
	Effective, Total int
	Rate             float64
}

// SteerPattern is one steer type's rate with its sample size.
type SteerPattern struct {
	Pattern       string
	Effectiveness float64
	SampleSize    int
}

// SteerEffectivenessReport says which types of steers change the agent's behavior.
type SteerEffectivenessReport struct {
	// Overall is the effectiveness rate (0-1).
	Overall float64
	// ByType is the effectiveness by steer type; TypeOrder lists the types in first-seen order.
	ByType      map[string]SteerTypeStats
	TypeOrder   []string
	Suggestions []string
	TopPatterns []SteerPattern
}

// AnalyzeSteerEffectivenessReport infers steer effectiveness from hold outcomes: a steer that led to approval (the
// agent fixed the issue) was effective.
func (l *Learning) AnalyzeSteerEffectivenessReport(projectRoot string) (SteerEffectivenessReport, error) {
	report := SteerEffectivenessReport{ByType: map[string]SteerTypeStats{}, Suggestions: []string{}, TopPatterns: []SteerPattern{}}
	d := l.open()
	if d == nil {
		return report, nil
	}
	rows, err := d.all(`
    SELECT 
      agent_reason,
      outcome,
      COUNT(*) as cnt
    FROM holds WHERE project_root = ? AND held = 1 AND agent_reason IS NOT NULL
    GROUP BY agent_reason, outcome
  `, projectRoot)
	if err != nil {
		return report, err
	}
	type tally struct{ effective, total int }
	var order []string
	stats := map[string]*tally{}
	for _, row := range rows {
		reason, _ := row["agent_reason"].(string)
		outcome, _ := row["outcome"].(string)
		cnt := rowInt(row, "cnt")
		steerType := "other"
		switch {
		case strings.Contains(reason, "irreversible"):
			steerType = "irreversible"
		case strings.Contains(reason, "off-task"):
			steerType = "off-task"
		case strings.Contains(reason, "intent mismatch"):
			steerType = "intent-mismatch"
		case strings.Contains(reason, "pattern"):
			steerType = "pattern"
		}
		t := stats[steerType]
		if t == nil {
			t = &tally{}
			stats[steerType] = t
			order = append(order, steerType)
		}
		t.total += cnt
		if outcome == "approved" {
			t.effective += cnt
		}
	}
	totalEffective, totalSteers := 0, 0
	for _, typ := range order {
		t := stats[typ]
		rate := 0.0
		if t.total > 0 {
			rate = float64(t.effective) / float64(t.total)
		}
		report.ByType[typ] = SteerTypeStats{Effective: t.effective, Total: t.total, Rate: rate}
		report.TypeOrder = append(report.TypeOrder, typ)
		totalEffective += t.effective
		totalSteers += t.total
		if t.total >= 5 && rate < 0.3 {
			report.Suggestions = append(report.Suggestions, "Steer type '"+typ+"' has a "+fixed0(rate*100)+"% effectiveness rate; consider rewording or adding more specific guidance")
		}
		if t.total >= 3 {
			report.TopPatterns = append(report.TopPatterns, SteerPattern{Pattern: typ, Effectiveness: rate, SampleSize: t.total})
		}
	}
	if totalSteers > 0 {
		report.Overall = float64(totalEffective) / float64(totalSteers)
	}
	sort.SliceStable(report.TopPatterns, func(i, j int) bool { return report.TopPatterns[i].Effectiveness > report.TopPatterns[j].Effectiveness })
	if len(report.TopPatterns) > 5 {
		report.TopPatterns = report.TopPatterns[:5]
	}
	return report, nil
}
