package ohmyhermes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	hermespaths "github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const (
	MemoryRecordsSchema = "omh_memory_records/v1"
	DocumentPlanSchema  = "omh_document_plan/v1"
	SourceTrustSchema   = "omh_source_trust/v1"

	defaultEvidenceTimeoutSec = 60
	maxEvidenceTimeoutSec     = 180
	defaultEvidenceTruncate   = 4000
	maxEvidenceTruncate       = 20000
	maxEvidenceCommands       = 8
	maxSourceTrustClaims      = 64
)

// --- Structured OMH Memory Records Store (backed by OmhMemoryProvider + <omh_home>/memory/records.json) ---

// OMHMemoryRecord represents a managed memory record for omh_memory CRUD.
type OMHMemoryRecord struct {
	RecordID       string   `json:"record_id"`
	RecordType     string   `json:"record_type"` // fact, decision, lesson, procedure, episode
	Summary        string   `json:"summary"`
	Subject        string   `json:"subject,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Scope          string   `json:"scope"`           // project, user
	RetentionClass string   `json:"retention_class"` // durable, standard, volatile
	Status         string   `json:"status"`          // pending, confirmed, retired
	RetireReason   string   `json:"retire_reason,omitempty"`
	ConfirmCount   int      `json:"confirm_count"`
	NoteID         string   `json:"note_id,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

type omhMemoryRecordsFile struct {
	Schema  string            `json:"schema"`
	Records []OMHMemoryRecord `json:"records"`
}

type omhMemoryRecordStore struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

func newOMHMemoryRecordStore(pathsFn func() OMHPaths) *omhMemoryRecordStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &omhMemoryRecordStore{paths: pathsFn}
}

func (s *omhMemoryRecordStore) filePath() string {
	return filepath.Join(s.paths().MemoryDir, "records.json")
}

func (s *omhMemoryRecordStore) loadLocked() (omhMemoryRecordsFile, error) {
	doc := omhMemoryRecordsFile{Schema: MemoryRecordsSchema}
	exists, err := readJSON(s.filePath(), &doc)
	if err != nil {
		return doc, err
	}
	if !exists {
		doc.Schema = MemoryRecordsSchema
	}
	return doc, nil
}

func (s *omhMemoryRecordStore) list() ([]OMHMemoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]OMHMemoryRecord, len(doc.Records))
	copy(out, doc.Records)
	return out, nil
}

func (s *omhMemoryRecordStore) capture(rec OMHMemoryRecord) (OMHMemoryRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.RecordID == "" {
		rec.RecordID = newShortID("mem")
	}
	if rec.RecordType == "" {
		rec.RecordType = "fact"
	}
	if rec.Scope == "" {
		rec.Scope = "project"
	}
	if rec.RetentionClass == "" {
		rec.RetentionClass = "standard"
	}
	if rec.Status == "" {
		rec.Status = "pending"
	}
	rec.CreatedAt = now
	rec.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return OMHMemoryRecord{}, err
	}
	doc.Records = append(doc.Records, rec)
	if err := atomicWriteJSON(s.filePath(), doc); err != nil {
		return OMHMemoryRecord{}, err
	}
	return rec, nil
}

func (s *omhMemoryRecordStore) update(recordID string, fn func(*OMHMemoryRecord) error) (OMHMemoryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return OMHMemoryRecord{}, err
	}
	for i := range doc.Records {
		if doc.Records[i].RecordID == recordID {
			if err := fn(&doc.Records[i]); err != nil {
				return OMHMemoryRecord{}, err
			}
			doc.Records[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := atomicWriteJSON(s.filePath(), doc); err != nil {
				return OMHMemoryRecord{}, err
			}
			return doc.Records[i], nil
		}
	}
	return OMHMemoryRecord{}, fmt.Errorf("memory record %q not found", recordID)
}

func (s *omhMemoryRecordStore) recall(query string, limit int) ([]OMHMemoryRecord, error) {
	if limit < 1 {
		limit = 5
	}
	if limit > 6 {
		limit = 6
	}
	recs, err := s.list()
	if err != nil {
		return nil, err
	}
	var active []OMHMemoryRecord
	for _, r := range recs {
		if r.Status != "retired" {
			active = append(active, r)
		}
	}
	if len(active) == 0 {
		return nil, nil
	}
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		if len(active) > limit {
			active = active[len(active)-limit:]
		}
		for i, j := 0, len(active)-1; i < j; i, j = i+1, j-1 {
			active[i], active[j] = active[j], active[i]
		}
		return active, nil
	}
	type scored struct {
		rec   OMHMemoryRecord
		score int
		idx   int
	}
	var hits []scored
	for i, r := range active {
		hay := strings.ToLower(r.Summary + " " + r.Subject + " " + r.RecordType + " " + strings.Join(r.Tags, " "))
		sc := 0
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				sc++
			}
		}
		if sc > 0 {
			if r.Status == "confirmed" {
				sc++
			}
			hits = append(hits, scored{rec: r, score: sc, idx: i})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].idx > hits[j].idx
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]OMHMemoryRecord, len(hits))
	for i, h := range hits {
		out[i] = h.rec
	}
	return out, nil
}

// --- Document Plan Persistence (<omh_home>/documents/<plan_id>/plan.json) ---

// DocumentChunk represents one numbered read range in a document plan.
type DocumentChunk struct {
	Chunk     int    `json:"chunk"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Unit      string `json:"unit"` // "lines", "pages", "chars"
	Title     string `json:"title,omitempty"`
	State     string `json:"state"` // "next", "covered", "missing"
	Note      string `json:"note,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// DocumentPlan is persisted at <omh_home>/documents/<plan_id>/plan.json.
type DocumentPlan struct {
	Schema       string          `json:"schema"`
	PlanID       string          `json:"plan_id"`
	Source       string          `json:"source"`
	Unit         string          `json:"unit"`
	TotalUnits   int             `json:"total_units"`
	BudgetChars  int             `json:"budget_chars"`
	CharsPerPage int             `json:"chars_per_page,omitempty"`
	Chunks       []DocumentChunk `json:"chunks"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}
var docPlanMu sync.Mutex

func documentPlanPath(p OMHPaths, planID string) (string, error) {
	id, err := sanitizeID(planID)
	if err != nil {
		return "", err
	}
	return filepath.Join(p.Home, "documents", id, "plan.json"), nil
}
// --- Evidence Gathering Allowlist & Security Checks ---

var shellMetacharacters = []string{";", "|", "&", "$", "(", ")", "`", ">", "<", "\n", "\r"}

var allowlistedEvidencePrefixes = [][]string{
	{"go", "test"},
	{"go", "vet"},
	{"git", "status"},
	{"git", "diff"},
	{"pytest"},
	{"python3", "-m", "unittest"},
	{"npm", "test"},
	{"npm", "run", "check"},
}

func isPathEscapeArg(val string) bool {
	if val == "" {
		return false
	}
	if filepath.IsAbs(val) || strings.HasPrefix(val, "/") || strings.HasPrefix(val, "\\") {
		return true
	}
	// Windows drive-letter prefix (e.g. C:\ or C:/)
	if len(val) >= 2 && ((val[0] >= 'a' && val[0] <= 'z') || (val[0] >= 'A' && val[0] <= 'Z')) && val[1] == ':' {
		return true
	}
	cleaned := filepath.Clean(filepath.FromSlash(val))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || filepath.IsAbs(cleaned) {
		return true
	}
	for _, part := range strings.FieldsFunc(val, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func normalizeFlagName(arg string) (string, string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", "", false
	}
	name := arg
	val := ""
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		name = arg[:idx]
		val = arg[idx+1:]
	}
	return name, val, true
}

func validateEvidenceArgs(prefix, args []string) error {
	isGitDiff := len(prefix) == 2 && prefix[0] == "git" && prefix[1] == "diff"
	isGo := len(prefix) >= 1 && prefix[0] == "go"
	isNpm := len(prefix) >= 1 && prefix[0] == "npm"
	isPytest := len(prefix) >= 1 && prefix[0] == "pytest"

	for _, arg := range args {
		if flagName, flagVal, isFlag := normalizeFlagName(arg); isFlag {
			trimmed := strings.TrimLeft(flagName, "-")
			switch {
			case isGitDiff:
				switch flagName {
				case "--output", "--ext-diff", "--textconv", "--no-index":
					return fmt.Errorf("disallowed git diff flag %q", arg)
				}
			case isGo:
				switch trimmed {
				case "exec", "toolexec", "vettool", "C", "o", "overlay", "modfile", "outputdir":
					return fmt.Errorf("disallowed go flag %q", arg)
				}
				if strings.HasSuffix(trimmed, "profile") {
					return fmt.Errorf("disallowed go profile flag %q", arg)
				}
			case isNpm:
				if flagName == "--prefix" || flagName == "-C" {
					return fmt.Errorf("disallowed npm flag %q", arg)
				}
			case isPytest:
				if flagName == "--rootdir" || flagName == "-c" || flagName == "-p" || flagName == "--basetemp" {
					return fmt.Errorf("disallowed pytest flag %q", arg)
				}
			}
			if flagVal != "" && isPathEscapeArg(flagVal) {
				return fmt.Errorf("disallowed path argument %q (must stay within workdir)", arg)
			}
			continue
		}

		if isPathEscapeArg(arg) {
			return fmt.Errorf("disallowed path argument %q (must stay within workdir)", arg)
		}
	}
	return nil
}

func validateEvidenceCommand(raw string) ([]string, error) {
	cmdStr := strings.TrimSpace(raw)
	if cmdStr == "" {
		return nil, fmt.Errorf("empty command")
	}
	for _, meta := range shellMetacharacters {
		if strings.Contains(cmdStr, meta) {
			return nil, fmt.Errorf("rejected shell metacharacter %q in command %q", meta, cmdStr)
		}
	}
	fields := strings.Fields(cmdStr)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	for _, prefix := range allowlistedEvidencePrefixes {
		if len(fields) >= len(prefix) {
			matched := true
			for i, tok := range prefix {
				if fields[i] != tok {
					matched = false
					break
				}
			}
			if matched {
				if err := validateEvidenceArgs(prefix, fields[len(prefix):]); err != nil {
					return nil, err
				}
				return fields, nil
			}
		}
	}
	return nil, fmt.Errorf("command %q is not in the omh_gather_evidence allowlist (allowed: go test, go vet, git status, git diff, pytest, python3 -m unittest, npm test, npm run check)", cmdStr)
}

func resolveConfinedWorkdir(sessionCwd, projectRoot, workdir string) (string, string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", "", fmt.Errorf("project_root is required")
	}
	absRoot, err := filepath.Abs(filepath.Clean(projectRoot))
	if err != nil {
		return "", "", fmt.Errorf("invalid project_root: %w", err)
	}
	if evalRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = evalRoot
	}
	info, err := os.Stat(absRoot)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("project_root %q is not an accessible directory", absRoot)
	}

	if strings.TrimSpace(sessionCwd) != "" {
		absCwd, err := filepath.Abs(filepath.Clean(sessionCwd))
		if err != nil {
			return "", "", fmt.Errorf("invalid session cwd: %w", err)
		}
		if evalCwd, err := filepath.EvalSymlinks(absCwd); err == nil {
			absCwd = evalCwd
		}
		relRoot, err := filepath.Rel(absCwd, absRoot)
		if err != nil || relRoot == ".." || strings.HasPrefix(relRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relRoot) {
			return "", "", fmt.Errorf("project_root %q escapes session cwd %q", projectRoot, absCwd)
		}
	}

	target := absRoot
	if strings.TrimSpace(workdir) != "" {
		wd := strings.TrimSpace(workdir)
		if !filepath.IsAbs(wd) {
			wd = filepath.Join(absRoot, wd)
		}
		absWD, err := filepath.Abs(filepath.Clean(wd))
		if err != nil {
			return "", "", fmt.Errorf("invalid workdir: %w", err)
		}
		if evalWD, err := filepath.EvalSymlinks(absWD); err == nil {
			absWD = evalWD
		}
		rel, err := filepath.Rel(absRoot, absWD)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", "", fmt.Errorf("workdir %q escapes project_root %q", workdir, absRoot)
		}
		wInfo, err := os.Stat(absWD)
		if err != nil || !wInfo.IsDir() {
			return "", "", fmt.Errorf("workdir %q is not an accessible directory", absWD)
		}
		target = absWD
	}
	return absRoot, target, nil
}

func truncateTail(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(s) <= maxChars {
		return s, false
	}
	return s[len(s)-maxChars:], true
}

// --- Source Trust Fail-Closed Ceiling Evaluator ---

type sourceTrustClaim struct {
	Tier       string `json:"tier"`       // upstream_official, practitioner_heuristic, unattributed
	ClaimKind  string `json:"claim_kind"` // approach, finding, completion
	Claim      string `json:"claim"`
	SourceID   string `json:"source_id,omitempty"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

type sourceTrustEval struct {
	Index           int    `json:"index"`
	Tier            string `json:"tier"`
	ClaimKind       string `json:"claim_kind"`
	Claim           string `json:"claim"`
	SourceID        string `json:"source_id,omitempty"`
	Allowed         bool   `json:"allowed"`
	MaxClaimCeiling string `json:"max_claim_ceiling"`
	Reason          string `json:"reason"`
}

func evaluateSourceTrustClaim(idx int, c sourceTrustClaim) sourceTrustEval {
	tier := strings.ToLower(strings.TrimSpace(c.Tier))
	kind := strings.ToLower(strings.TrimSpace(c.ClaimKind))

	ev := sourceTrustEval{
		Index:     idx,
		Tier:      tier,
		ClaimKind: kind,
		Claim:     strings.TrimSpace(c.Claim),
		SourceID:  strings.TrimSpace(c.SourceID),
	}

	// Validate claim_kind first (fail closed).
	switch kind {
	case "approach", "finding", "completion":
	default:
		ev.Allowed = false
		ev.MaxClaimCeiling = "none"
		ev.Reason = fmt.Sprintf("fail-closed: unrecognized claim_kind %q", c.ClaimKind)
		return ev
	}

	// Completion claims can NEVER be satisfied by external prose/source trust alone;
	// they always require observed local verification evidence.
	if kind == "completion" {
		ev.Allowed = false
		ev.MaxClaimCeiling = "observed_verification_required"
		ev.Reason = "fail-closed: completion claims require observed local command/test proof, never source citations alone"
		return ev
	}

	switch tier {
	case "upstream_official":
		ev.Allowed = true
		ev.MaxClaimCeiling = "finding"
		ev.Reason = "upstream_official supports approach and finding claims (not completion)"
	case "practitioner_heuristic":
		ev.MaxClaimCeiling = "approach"
		if kind == "approach" {
			ev.Allowed = true
			ev.Reason = "practitioner_heuristic supports approach guidance only"
		} else {
			ev.Allowed = false
			ev.Reason = "fail-closed: practitioner_heuristic cannot support authoritative finding claims; ceiling is approach"
		}
	case "unattributed":
		ev.Allowed = false
		ev.MaxClaimCeiling = "none"
		ev.Reason = "fail-closed: unattributed sources cannot support approach, finding, or completion claims"
	default:
		ev.Allowed = false
		ev.MaxClaimCeiling = "none"
		ev.Reason = fmt.Sprintf("fail-closed: unrecognized trust tier %q", c.Tier)
	}
	return ev
}

var validSubjectSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)

// --- Registration of all 11 Extended omh_* Tools ---

func registerExtendedTools(
	ext *sdk.Extension,
	pathsFn func() OMHPaths,
	router *RouteRouter,
	dags *DAGStore,
	teams *TeamStore,
	loops *LoopStore,
	completions *CompletionStore,
	memProv *OmhMemoryProvider,
) {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	memRecords := newOMHMemoryRecordStore(pathsFn)
	registerOnboardSessionHook(ext, pathsFn, router)

	// 1. omh_status
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_status",
		Label:         "OMH Status",
		Description:   "Return OMH runtime status including profile, active DAGs, teams, loops, memory records, and routing overrides.",
		PromptSnippet: "Check OMH runtime status (profile, active DAGs/teams/loops, memory records, routing overrides) with omh_status.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max recent items to summarize (default 5)"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			limit := paramInt(params, "limit", 5)
			if limit < 1 {
				limit = 5
			}
			p := pathsFn()
			hp := hermespaths.ResolveProfilePaths("")

			dagList, _ := dags.List()
			teamList, _ := teams.List()
			loopList, _ := loops.List()
			recs, _ := memRecords.list()
			notes, _ := memProv.Recall("", limit)
			_, overridden, _ := router.AllChains()

			activeDAGs := 0
			for _, d := range dagList {
				if d.Status == "active" {
					activeDAGs++
				}
			}
			activeTeams := 0
			for _, tm := range teamList {
				if tm.Status == "active" {
					activeTeams++
				}
			}
			activeLoops := 0
			for _, lp := range loopList {
				if lp.Status == "active" {
					activeLoops++
				}
			}
			var overrideKeys []string
			for k := range overridden {
				overrideKeys = append(overrideKeys, k)
			}
			sort.Strings(overrideKeys)

			var sp SetupProfile
			hasProfile, _ := readJSON(filepath.Join(p.Home, "setup-profile.json"), &sp)

			return jsonToolResult(map[string]any{
				"ok":                true,
				"schema":            "omh_status/v1",
				"omh_home":          p.Home,
				"hermes_profile":    hp.ProfileName,
				"setup_initialized": hasProfile || fileExists(filepath.Join(p.Home, "manifest.json")),
				"memory_mode":       sp.MemoryMode,
				"default_executor":  sp.DefaultExecutor,
				"counts": map[string]any{
					"dags_total":        len(dagList),
					"dags_active":       activeDAGs,
					"teams_total":       len(teamList),
					"teams_active":      activeTeams,
					"loops_total":       len(loopList),
					"loops_active":      activeLoops,
					"memory_records":    len(recs),
					"episodic_notes":    len(notes),
					"routing_overrides": len(overrideKeys),
				},
				"routing_overrides": overrideKeys,
			}, false), nil
		},
	})

	// 2. omh_hud
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_hud",
		Label:         "OMH HUD",
		Description:   "Return structured OMH HUD payload (preset: minimal|focused|full) with token/context metrics and active DAG/team/loop summary.",
		PromptSnippet: "Inspect compact or full OMH HUD telemetry with omh_hud (preset: minimal|focused|full).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"preset":                    map[string]any{"type": "string", "enum": []string{"minimal", "focused", "full"}},
				"limit":                     map[string]any{"type": "integer"},
				"tokens_remaining":          map[string]any{"type": "number"},
				"token_budget":              map[string]any{"type": "number"},
				"input_tokens":              map[string]any{"type": "number"},
				"output_tokens":             map[string]any{"type": "number"},
				"context_remaining_percent": map[string]any{"type": "number"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			preset := strings.ToLower(paramString(params, "preset"))
			if preset == "" {
				preset = "focused"
			}
			switch preset {
			case "minimal", "focused", "full":
			default:
				return errorToolResult(fmt.Sprintf("invalid preset %q (valid: minimal, focused, full)", preset)), nil
			}
			limit := paramInt(params, "limit", 3)
			if limit < 1 {
				limit = 3
			}

			dagList, _ := dags.List()
			teamList, _ := teams.List()
			loopList, _ := loops.List()
			inTok, outTok, cost, turns, toolCalls := usage.Default().Totals()

			metrics := map[string]any{
				"input_tokens":  inTok,
				"output_tokens": outTok,
				"cost_usd":      cost,
				"turns":         turns,
				"tool_calls":    toolCalls,
			}
			for _, k := range []string{"tokens_remaining", "token_budget", "input_tokens", "output_tokens", "context_remaining_percent"} {
				if v, ok := params[k]; ok && v != nil {
					metrics[k] = v
				}
			}

			payload := map[string]any{
				"ok":       true,
				"schema":   "omh_hud/v1",
				"preset":   preset,
				"omh_home": pathsFn().Home,
				"metrics":  metrics,
				"summary": map[string]any{
					"dags":        len(dagList),
					"teams":       len(teamList),
					"loops":       len(loopList),
					"delegations": len(teams.Delegations()),
				},
			}

			if preset == "focused" || preset == "full" {
				var activeDAGSummaries []map[string]any
				for _, d := range dagList {
					if d.Status == "active" || preset == "full" {
						activeDAGSummaries = append(activeDAGSummaries, map[string]any{
							"run_id": d.RunID,
							"title":  d.Title,
							"status": d.Status,
							"nodes":  len(d.Nodes),
							"ready":  ReadyNodes(d),
						})
						if len(activeDAGSummaries) >= limit {
							break
						}
					}
				}
				payload["dags"] = activeDAGSummaries
			}
			if preset == "full" {
				var teamSummaries []map[string]any
				for _, tm := range teamList {
					teamSummaries = append(teamSummaries, map[string]any{
						"team_id": tm.TeamID,
						"title":   tm.Title,
						"status":  tm.Status,
						"lanes":   len(tm.Lanes),
					})
					if len(teamSummaries) >= limit {
						break
					}
				}
				var loopSummaries []map[string]any
				for _, lp := range loopList {
					loopSummaries = append(loopSummaries, map[string]any{
						"loop_id":   lp.LoopID,
						"goal":      lp.Goal,
						"status":    lp.Status,
						"iteration": lp.Iteration,
						"max_iterations": lp.MaxIterations,
					})
					if len(loopSummaries) >= limit {
						break
					}
				}
				payload["teams"] = teamSummaries
				payload["loops"] = loopSummaries
			}
			return jsonToolResult(payload, false), nil
		},
	})

	// 3. omh_context
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_context",
		Label:         "OMH Context",
		Description:   "Return OMH routing and workflow context hints for a given message and source (hashes message rather than storing raw prompt).",
		PromptSnippet: "Get compact OMH routing and workflow hints for a task message with omh_context.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"message":                map[string]any{"type": "string"},
				"source":                 map[string]any{"type": "string"},
				"limit":                  map[string]any{"type": "integer"},
				"include_prompt_context": map[string]any{"type": "boolean"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			msg := paramString(params, "message")
			source := paramString(params, "source")
			if source == "" {
				source = "hermes-agent"
			}
			limit := paramInt(params, "limit", 3)
			if limit < 0 {
				limit = 0
			}
			if limit > 10 {
				limit = 10
			}
			includePrompt, _ := paramBool(params, "include_prompt_context")

			sum := sha256.Sum256([]byte(msg))
			msgHash := hex.EncodeToString(sum[:8])

			rec := scoreRecommendations(msg, max(limit, 1), router)
			hints := rec.Recommendations
			if limit == 0 {
				hints = nil
			} else if len(hints) > limit {
				hints = hints[:limit]
			}

			resp := map[string]any{
				"ok":              true,
				"schema":          "omh_context/v1",
				"source":          source,
				"message_sha256":  msgHash,
				"suggested_route": rec.SuggestedRoute,
				"primary_chain":   rec.PrimaryChain,
				"workflow_hints":  hints,
			}
			if includePrompt {
				resp["prompt_context"] = omhAwarenessPrompt
			}
			return jsonToolResult(resp, false), nil
		},
	})

	// 4. omh_recommend
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_recommend",
		Label:         "OMH Recommend",
		Description:   "Recommend OMH workflow skills and routing categories for a natural-language task message.",
		PromptSnippet: "Score and recommend OMH skills and routing categories for a message with omh_recommend.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
			},
			"required": []string{"message"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			msg := paramString(params, "message")
			if msg == "" {
				return errorToolResult("message is required"), nil
			}
			limit := paramInt(params, "limit", 5)
			if limit < 1 {
				limit = 1
			}
			if limit > 10 {
				limit = 10
			}
			res := scoreRecommendations(msg, limit, router)
			sum := sha256.Sum256([]byte(msg))
			return jsonToolResult(map[string]any{
				"ok":              true,
				"schema":          "omh_recommend/v1",
				"message_sha256":  hex.EncodeToString(sum[:8]),
				"suggested_route": res.SuggestedRoute,
				"primary_chain":   res.PrimaryChain,
				"recommendations": res.Recommendations,
			}, false), nil
		},
	})

	// 5. omh_probe
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_probe",
		Label:         "OMH Probe",
		Description:   "Probe ~/.omh and ~/.hermes state, installed skills, active tools, and optional parity/roadmap metadata.",
		PromptSnippet: "Inspect OMH capability and environment readiness with omh_probe.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"include_parity":  map[string]any{"type": "boolean"},
				"include_roadmap": map[string]any{"type": "boolean"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			incParity, _ := paramBool(params, "include_parity")
			incRoadmap, _ := paramBool(params, "include_roadmap")
			payload := buildProbePayload(pathsFn(), incParity, incRoadmap, dags, teams, loops)
			payload["ok"] = true
			return jsonToolResult(payload, false), nil
		},
	})

	// 6. omh_capabilities
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_capabilities",
		Label:         "OMH Capabilities",
		Description:   "Inspect OMH roles, skills, tools, orchestration patterns, and evidence boundaries (action: summary|list|inspect|export).",
		PromptSnippet: "Inspect OMH capability manifests (roles, skills, tools, orchestration_patterns, evidence_boundaries) with omh_capabilities.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string", "enum": []string{"summary", "list", "inspect", "export"}},
				"section": map[string]any{"type": "string", "enum": []string{"roles", "skills", "tools", "orchestration_patterns", "evidence_boundaries"}},
				"id":      map[string]any{"type": "string"},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			section := strings.ToLower(paramString(params, "section"))
			id := strings.TrimSpace(paramString(params, "id"))
			if action == "" {
				if section != "" {
					action = "export"
				} else {
					action = "summary"
				}
			}

			roles := BuiltinRoles()
			tools := allOMHToolNames(LSPToolsEnabled())
			patterns := []map[string]any{
				{"id": "dag", "tool": "omh_dag", "summary": "Topological dependency graph with ready-frontier scheduling"},
				{"id": "team", "tool": "omh_team", "summary": "Parallel worker lanes with isolated write scopes"},
				{"id": "loop", "tool": "omh_loop", "summary": "Bounded iterative convergence loop with stop conditions"},
				{"id": "maestro", "tool": "omh_maestro", "summary": "Explicit owner-assigned external handoff packets"},
			}
			boundaries := []map[string]any{
				{"id": "prepared_vs_observed", "rule": "Prepared plans/diffs never certify completion; observed exit_code=0 proof is required."},
				{"id": "source_trust_ceiling", "rule": "External prose cannot certify completion; unattributed sources are fail-closed."},
				{"id": "evidence_allowlist", "rule": "omh_gather_evidence runs only allowlisted verification commands confined to project_root."},
			}

			switch action {
			case "summary":
				return jsonToolResult(map[string]any{
					"ok":                     true,
					"schema":                 "omh_capabilities/v1",
					"action":                 "summary",
					"roles_count":            len(roles),
					"skills_count":           len(bundledOMHSkills),
					"tools_count":            len(tools),
					"orchestration_patterns": len(patterns),
					"evidence_boundaries":    len(boundaries),
				}, false), nil

			case "list":
				switch section {
				case "", "all":
					return jsonToolResult(map[string]any{
						"ok":       true,
						"sections": []string{"roles", "skills", "tools", "orchestration_patterns", "evidence_boundaries"},
					}, false), nil
				case "roles":
					ids := make([]string, len(roles))
					for i, r := range roles {
						ids[i] = r.ID
					}
					return jsonToolResult(map[string]any{"ok": true, "section": "roles", "items": ids}, false), nil
				case "skills":
					ids := make([]string, len(bundledOMHSkills))
					for i, s := range bundledOMHSkills {
						ids[i] = s.ID
					}
					return jsonToolResult(map[string]any{"ok": true, "section": "skills", "items": ids}, false), nil
				case "tools":
					return jsonToolResult(map[string]any{"ok": true, "section": "tools", "items": tools}, false), nil
				case "orchestration_patterns":
					ids := make([]string, len(patterns))
					for i, pt := range patterns {
						ids[i] = pt["id"].(string)
					}
					return jsonToolResult(map[string]any{"ok": true, "section": "orchestration_patterns", "items": ids}, false), nil
				case "evidence_boundaries":
					ids := make([]string, len(boundaries))
					for i, eb := range boundaries {
						ids[i] = eb["id"].(string)
					}
					return jsonToolResult(map[string]any{"ok": true, "section": "evidence_boundaries", "items": ids}, false), nil
				default:
					return errorToolResult(fmt.Sprintf("unknown capabilities section %q", section)), nil
				}

			case "inspect":
				if id == "" {
					return errorToolResult("id is required for inspect"), nil
				}
				if r, ok := LookupBuiltinRole(id); ok {
					return jsonToolResult(map[string]any{"ok": true, "kind": "role", "item": r}, false), nil
				}
				for _, s := range bundledOMHSkills {
					if s.ID == id || s.Name == id {
						return jsonToolResult(map[string]any{"ok": true, "kind": "skill", "item": s}, false), nil
					}
				}
				for _, t := range tools {
					if t == id {
						return jsonToolResult(map[string]any{"ok": true, "kind": "tool", "item": map[string]any{"name": t}}, false), nil
					}
				}
				for _, pt := range patterns {
					if pt["id"] == id {
						return jsonToolResult(map[string]any{"ok": true, "kind": "orchestration_pattern", "item": pt}, false), nil
					}
				}
				for _, eb := range boundaries {
					if eb["id"] == id {
						return jsonToolResult(map[string]any{"ok": true, "kind": "evidence_boundary", "item": eb}, false), nil
					}
				}
				return errorToolResult(fmt.Sprintf("capability %q not found", id)), nil

			case "export":
				full := map[string]any{
					"roles":                  roles,
					"skills":                 bundledOMHSkills,
					"tools":                  tools,
					"orchestration_patterns": patterns,
					"evidence_boundaries":    boundaries,
				}
				if section != "" && section != "all" {
					v, ok := full[section]
					if !ok {
						return errorToolResult(fmt.Sprintf("unknown capabilities section %q", section)), nil
					}
					return jsonToolResult(map[string]any{"ok": true, "section": section, "data": v}, false), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "schema": "omh_capabilities/v1", "capabilities": full}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unknown action %q (valid: summary, list, inspect, export)", action)), nil
			}
		},
	})

	// 7. omh_memory (full upstream action set backed by OmhMemoryProvider)
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_memory",
		Label:         "OMH Memory",
		Description:   "Manage OMH memory records and inspect Hermes memory blocks (status, blocks, read, capture, recall, confirm, correct, retire).",
		PromptSnippet: "Manage OMH durable memory records and inspect memory blocks with omh_memory (status, blocks, read, capture, recall, confirm, correct, retire).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":          map[string]any{"type": "string", "enum": []string{"status", "blocks", "read", "capture", "recall", "confirm", "correct", "retire"}},
				"label":           map[string]any{"type": "string", "description": "Memory block label for read (e.g. memory, user, omh_notes, omh_records)"},
				"summary":         map[string]any{"type": "string", "description": "Summary text for capture/correct (max 500 chars)"},
				"record_type":     map[string]any{"type": "string", "enum": []string{"fact", "decision", "lesson", "procedure", "episode"}},
				"tags":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"subject":         map[string]any{"type": "string"},
				"scope":           map[string]any{"type": "string", "enum": []string{"project", "user"}},
				"retention_class": map[string]any{"type": "string", "enum": []string{"durable", "standard", "volatile"}},
				"query":           map[string]any{"type": "string"},
				"limit":           map[string]any{"type": "integer"},
				"record_id":       map[string]any{"type": "string"},
				"reason":          map[string]any{"type": "string", "enum": []string{"no_longer_true", "superseded", "user_asked"}},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			if action == "" {
				action = "status"
			}
			sessionID := resolveSessionID(ctx)
			p := pathsFn()
			hp := hermespaths.ResolveProfilePaths("")

			switch action {
			case "status":
				recs, err := memRecords.list()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				notes, _ := memProv.Recall("", 100)
				activeCount, confirmedCount, retiredCount := 0, 0, 0
				for _, r := range recs {
					switch r.Status {
					case "confirmed":
						confirmedCount++
						activeCount++
					case "retired":
						retiredCount++
					default:
						activeCount++
					}
				}
				return jsonToolResult(map[string]any{
					"ok":              true,
					"provider":        memProv.Name(),
					"omh_memory_dir":  p.MemoryDir,
					"records_total":   len(recs),
					"records_active":  activeCount,
					"records_confirm": confirmedCount,
					"records_retired": retiredCount,
					"episodic_notes":  len(notes),
					"hermes_memories": hp.MemoriesDir,
				}, false), nil

			case "blocks":
				memFile := filepath.Join(hp.MemoriesDir, "MEMORY.md")
				userFile := filepath.Join(hp.MemoriesDir, "USER.md")
				blocks := []map[string]any{
					{"label": "memory", "path": memFile, "exists": fileExists(memFile), "owner": "hermes"},
					{"label": "user", "path": userFile, "exists": fileExists(userFile), "owner": "hermes"},
					{"label": "omh_notes", "path": p.MemoryNotesFile, "exists": fileExists(p.MemoryNotesFile), "owner": "omh"},
					{"label": "omh_records", "path": memRecords.filePath(), "exists": fileExists(memRecords.filePath()), "owner": "omh"},
				}
				return jsonToolResult(map[string]any{"ok": true, "blocks": blocks}, false), nil

			case "read":
				label := strings.ToLower(paramString(params, "label"))
				if label == "" {
					return errorToolResult("label is required for read"), nil
				}
				switch label {
				case "memory", "user":
					st := memory.NewStore(hp.MemoriesDir, 0, 0)
					target := memory.TargetMemory
					if label == "user" {
						target = memory.TargetUser
					}
					entries, used, limit, err := st.ReadLive(target)
					if err != nil {
						return errorToolResult(err.Error()), nil
					}
					return jsonToolResult(map[string]any{"ok": true, "label": label, "entries": entries, "count": len(entries), "chars_used": used, "chars_limit": limit}, false), nil
				case "omh_notes":
					notes, err := memProv.Recall("", paramInt(params, "limit", 20))
					if err != nil {
						return errorToolResult(err.Error()), nil
					}
					return jsonToolResult(map[string]any{"ok": true, "label": label, "notes": notes, "count": len(notes)}, false), nil
				case "omh_records":
					recs, err := memRecords.list()
					if err != nil {
						return errorToolResult(err.Error()), nil
					}
					return jsonToolResult(map[string]any{"ok": true, "label": label, "records": recs, "count": len(recs)}, false), nil
				default:
					return errorToolResult(fmt.Sprintf("unknown memory block label %q", label)), nil
				}

			case "capture":
				summary := paramString(params, "summary")
				if summary == "" {
					return errorToolResult("summary is required for capture"), nil
				}
				if len(summary) > 500 {
					return errorToolResult("summary exceeds 500 characters"), nil
				}
				recType := strings.ToLower(paramString(params, "record_type"))
				if recType == "" {
					recType = "fact"
				}
				switch recType {
				case "fact", "decision", "lesson", "procedure", "episode":
				default:
					return errorToolResult(fmt.Sprintf("invalid record_type %q", recType)), nil
				}
				tags := paramStringSlice(params, "tags")
				if len(tags) > 8 {
					tags = tags[:8]
				}
				subject := strings.ToLower(paramString(params, "subject"))
				if subject != "" && !validSubjectSlugRe.MatchString(subject) {
					return errorToolResult(fmt.Sprintf("invalid subject slug %q", subject)), nil
				}
				scope := strings.ToLower(paramString(params, "scope"))
				if scope == "" {
					scope = "project"
				}
				if scope != "project" && scope != "user" {
					return errorToolResult(fmt.Sprintf("invalid scope %q", scope)), nil
				}
				ret := strings.ToLower(paramString(params, "retention_class"))
				if ret == "" {
					ret = "standard"
				}
				if ret != "durable" && ret != "standard" && ret != "volatile" {
					return errorToolResult(fmt.Sprintf("invalid retention_class %q", ret)), nil
				}

				note, err := memProv.Remember(sessionID, "memory_capture:"+recType, summary, tags)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				rec, err := memRecords.capture(OMHMemoryRecord{
					RecordType:     recType,
					Summary:        summary,
					Subject:        subject,
					Tags:           tags,
					Scope:          scope,
					RetentionClass: ret,
					Status:         "pending",
					NoteID:         note.NoteID,
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "record": rec}, false), nil

			case "recall":
				q := paramString(params, "query")
				limit := paramInt(params, "limit", 5)
				if limit < 1 {
					limit = 1
				}
				if limit > 6 {
					limit = 6
				}
				recs, err := memRecords.recall(q, limit)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				notes, err := memProv.Recall(q, limit)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":      true,
					"query":   q,
					"records": recs,
					"notes":   notes,
					"count":   len(recs),
				}, false), nil

			case "confirm":
				recID := paramString(params, "record_id")
				if recID == "" {
					return errorToolResult("record_id is required for confirm"), nil
				}
				updated, err := memRecords.update(recID, func(r *OMHMemoryRecord) error {
					if r.Status == "retired" {
						return fmt.Errorf("cannot confirm retired record %q", recID)
					}
					r.Status = "confirmed"
					r.ConfirmCount++
					return nil
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "record": updated}, false), nil

			case "correct":
				recID := paramString(params, "record_id")
				summary := paramString(params, "summary")
				if recID == "" || summary == "" {
					return errorToolResult("record_id and summary are required for correct"), nil
				}
				if len(summary) > 500 {
					return errorToolResult("summary exceeds 500 characters"), nil
				}
				updated, err := memRecords.update(recID, func(r *OMHMemoryRecord) error {
					if r.Status == "retired" {
						return fmt.Errorf("cannot correct retired record %q", recID)
					}
					r.Summary = summary
					if tags := paramStringSlice(params, "tags"); len(tags) > 0 {
						if len(tags) > 8 {
							tags = tags[:8]
						}
						r.Tags = tags
					}
					return nil
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				_, _ = memProv.Remember(sessionID, "memory_correct", fmt.Sprintf("[%s] %s", recID, summary), updated.Tags)
				return jsonToolResult(map[string]any{"ok": true, "record": updated}, false), nil

			case "retire":
				recID := paramString(params, "record_id")
				reason := strings.ToLower(paramString(params, "reason"))
				if recID == "" {
					return errorToolResult("record_id is required for retire"), nil
				}
				if reason == "" {
					reason = "no_longer_true"
				}
				switch reason {
				case "no_longer_true", "superseded", "user_asked":
				default:
					return errorToolResult(fmt.Sprintf("invalid retire reason %q", reason)), nil
				}
				updated, err := memRecords.update(recID, func(r *OMHMemoryRecord) error {
					r.Status = "retired"
					r.RetireReason = reason
					return nil
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{"ok": true, "record": updated}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unknown omh_memory action %q", action)), nil
			}
		},
	})

	// 8. omh_resume
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_resume",
		Label:         "OMH Resume",
		Description:   "Read recent OMH plans, DAG runs, and completion checkpoints across sessions in this profile.",
		PromptSnippet: "Resume recent OMH plans, DAG runs, and completion checkpoints with omh_resume.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			limit := paramInt(params, "limit", 5)
			if limit < 1 {
				limit = 1
			}
			if limit > 20 {
				limit = 20
			}
			p := pathsFn()
			sessionID := resolveSessionID(ctx)

			dagList, _ := dags.List()
			if len(dagList) > limit {
				dagList = dagList[:limit]
			}
			compList, _ := completions.List()
			if len(compList) > limit {
				compList = compList[len(compList)-limit:]
			}

			// Discover persisted document plans under <omh_home>/documents/*/plan.json.
			var docPlans []map[string]any
			docsRoot := filepath.Join(p.Home, "documents")
			if entries, err := os.ReadDir(docsRoot); err == nil {
				for _, e := range entries {
					if !e.IsDir() {
						continue
					}
					var dp DocumentPlan
					if ok, err := readJSON(filepath.Join(docsRoot, e.Name(), "plan.json"), &dp); err == nil && ok {
						covered := 0
						for _, ch := range dp.Chunks {
							if ch.State == "covered" {
								covered++
							}
						}
						docPlans = append(docPlans, map[string]any{
							"plan_id":        dp.PlanID,
							"source":         dp.Source,
							"chunks_total":   len(dp.Chunks),
							"chunks_covered": covered,
							"updated_at":     dp.UpdatedAt,
						})
					}
					if len(docPlans) >= limit {
						break
					}
				}
			}

			todos := todo.Default().Get(sessionID)

			return jsonToolResult(map[string]any{
				"ok":          true,
				"schema":      "omh_resume/v1",
				"omh_home":    p.Home,
				"session_id":  sessionID,
				"dags":        dagList,
				"completions": compList,
				"doc_plans":   docPlans,
				"todos":       todos,
			}, false), nil
		},
	})

	// 9. omh_gather_evidence
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_gather_evidence",
		Label:         "OMH Gather Evidence",
		Description:   "Execute strictly allowlisted verification commands confined to project_root/workdir with timeout and tail truncation.",
		PromptSnippet: "Run allowlisted verification commands (go test, go vet, git status, git diff, pytest, unittest, npm test/check) with omh_gather_evidence.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"commands":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maxEvidenceCommands},
				"project_root": map[string]any{"type": "string"},
				"workdir":      map[string]any{"type": "string"},
				"timeout":      map[string]any{"type": "integer", "minimum": 1, "maximum": maxEvidenceTimeoutSec},
				"truncate":     map[string]any{"type": "integer", "minimum": 100, "maximum": maxEvidenceTruncate},
			},
			"required": []string{"commands"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			cmds := paramStringSlice(params, "commands")
			if len(cmds) == 0 {
				return errorToolResult("commands must contain at least one verification command"), nil
			}
			if len(cmds) > maxEvidenceCommands {
				return errorToolResult(fmt.Sprintf("commands exceeds maximum of %d", maxEvidenceCommands)), nil
			}

			sessionCwd := ctx.Cwd()
			projectRoot := paramString(params, "project_root")
			if projectRoot == "" {
				projectRoot = sessionCwd
			}
			workdir := paramString(params, "workdir")
			absRoot, runDir, err := resolveConfinedWorkdir(sessionCwd, projectRoot, workdir)
			if err != nil {
				return errorToolResult(err.Error()), nil
			}

			timeoutSec := paramInt(params, "timeout", defaultEvidenceTimeoutSec)
			if timeoutSec < 1 {
				timeoutSec = defaultEvidenceTimeoutSec
			}
			if timeoutSec > maxEvidenceTimeoutSec {
				timeoutSec = maxEvidenceTimeoutSec
			}
			truncMax := paramInt(params, "truncate", defaultEvidenceTruncate)
			if truncMax < 100 {
				truncMax = 100
			}
			if truncMax > maxEvidenceTruncate {
				truncMax = maxEvidenceTruncate
			}

			// Validate ALL commands before executing any of them.
			parsed := make([][]string, len(cmds))
			for i, rawCmd := range cmds {
				argv, err := validateEvidenceCommand(rawCmd)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				parsed[i] = argv
			}

			results := make([]map[string]any, 0, len(parsed))
			allPassed := true
			for i, argv := range parsed {
				parentCtx, parentCancel := context.WithCancel(context.Background())
				if done := ctx.Done(); done != nil {
					go func() {
						select {
						case <-done:
							parentCancel()
						case <-parentCtx.Done():
						}
					}()
				}
				runCtx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutSec)*time.Second)
				cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
				cmd.Dir = runDir
				cmd.WaitDelay = 2 * time.Second
				setProcGroup(cmd)
				cmd.Cancel = func() error {
					return killProcGroup(cmd)
				}
				var outBuf bytes.Buffer
				cmd.Stdout = &outBuf
				cmd.Stderr = &outBuf

				start := time.Now()
				runErr := cmd.Run()
				durMs := time.Since(start).Milliseconds()
				timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
				cancel()
				parentCancel()

				exitCode := 0
				if runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay) {
					allPassed = false
					var ee *exec.ExitError
					if errors.As(runErr, &ee) {
						exitCode = ee.ExitCode()
					} else {
						exitCode = -1
					}
				} else if errors.Is(runErr, exec.ErrWaitDelay) && timedOut {
					allPassed = false
					exitCode = -1
				}
				tail, truncated := truncateTail(outBuf.String(), truncMax)
				results = append(results, map[string]any{
					"command":     cmds[i],
					"exit_code":   exitCode,
					"timed_out":   timedOut,
					"duration_ms": durMs,
					"truncated":   truncated,
					"output_tail": tail,
				})
			}

			return jsonToolResult(map[string]any{
				"ok":           true,
				"schema":       "omh_gather_evidence/v1",
				"project_root": absRoot,
				"workdir":      runDir,
				"all_passed":   allPassed,
				"results":      results,
			}, false), nil
		},
	})

	// 10. omh_document_plan
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_document_plan",
		Label:         "OMH Document Plan",
		Description:   "Split long documents into numbered read-range chunks persisted under <omh_home>/documents/<plan_id>/plan.json (plan|show|mark).",
		PromptSnippet: "Plan, inspect, and mark chunk coverage for long documents with omh_document_plan (plan|show|mark).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action":         map[string]any{"type": "string", "enum": []string{"plan", "show", "mark"}},
				"source":         map[string]any{"type": "string"},
				"pages":          map[string]any{"type": "integer"},
				"chars":          map[string]any{"type": "integer"},
				"lines":          map[string]any{"type": "integer"},
				"budget_chars":   map[string]any{"type": "integer"},
				"chars_per_page": map[string]any{"type": "integer"},
				"plan_id":        map[string]any{"type": "string"},
				"from":           map[string]any{"type": "integer"},
				"limit":          map[string]any{"type": "integer"},
				"chunk":          map[string]any{"type": "integer"},
				"state":          map[string]any{"type": "string", "enum": []string{"covered", "next", "missing"}},
				"note":           map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			p := pathsFn()

			switch action {
			case "plan":
				source := paramString(params, "source")
				if source == "" {
					return errorToolResult("source is required for plan"), nil
				}
				lines := paramInt(params, "lines", 0)
				pages := paramInt(params, "pages", 0)
				chars := paramInt(params, "chars", 0)
				budgetChars := paramInt(params, "budget_chars", 8000)
				if budgetChars < 500 {
					budgetChars = 500
				}
				charsPerPage := paramInt(params, "chars_per_page", 2500)
				if charsPerPage < 200 {
					charsPerPage = 200
				}

				unit := "lines"
				total := lines
				chunkSpan := max(budgetChars/80, 10)
				if total <= 0 && pages > 0 {
					unit = "pages"
					total = pages
					chunkSpan = max(budgetChars/charsPerPage, 1)
				}
				if total <= 0 && chars > 0 {
					unit = "chars"
					total = chars
					chunkSpan = budgetChars
				}
				if total <= 0 {
					return errorToolResult("one of lines, pages, or chars must be a positive integer"), nil
				}

				planID := paramString(params, "plan_id")
				if planID == "" {
					planID = newShortID("doc")
				}
				planPath, err := documentPlanPath(p, planID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}

				var chunks []DocumentChunk
				idx := 1
				for start := 1; start <= total; start += chunkSpan {
					end := start + chunkSpan - 1
					if end > total {
						end = total
					}
					chunks = append(chunks, DocumentChunk{
						Chunk: idx,
						Start: start,
						End:   end,
						Unit:  unit,
						State: "next",
					})
					idx++
				}

				now := time.Now().UTC().Format(time.RFC3339)
				dp := DocumentPlan{
					Schema:       DocumentPlanSchema,
					PlanID:       filepath.Base(filepath.Dir(planPath)),
					Source:       source,
					Unit:         unit,
					TotalUnits:   total,
					BudgetChars:  budgetChars,
					CharsPerPage: charsPerPage,
					Chunks:       chunks,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				docPlanMu.Lock()
				writeErr := atomicWriteJSON(planPath, dp)
				docPlanMu.Unlock()
				if writeErr != nil {
					return errorToolResult(writeErr.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":           true,
					"plan":         dp,
					"plan_path":    planPath,
					"chunks_count": len(dp.Chunks),
				}, false), nil

			case "show":
				planID := paramString(params, "plan_id")
				if planID == "" {
					return errorToolResult("plan_id is required for show"), nil
				}
				planPath, err := documentPlanPath(p, planID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				var dp DocumentPlan
				ok, err := readJSON(planPath, &dp)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				if !ok {
					return errorToolResult(fmt.Sprintf("document plan %q not found", planID)), nil
				}
				fromIdx := paramInt(params, "from", 1)
				if fromIdx < 1 {
					fromIdx = 1
				}
				limit := paramInt(params, "limit", len(dp.Chunks))
				if limit < 1 {
					limit = len(dp.Chunks)
				}
				start := fromIdx - 1
				if start > len(dp.Chunks) {
					start = len(dp.Chunks)
				}
				end := start + limit
				if end > len(dp.Chunks) {
					end = len(dp.Chunks)
				}
				return jsonToolResult(map[string]any{
					"ok":           true,
					"plan_id":      dp.PlanID,
					"source":       dp.Source,
					"unit":         dp.Unit,
					"total_units":  dp.TotalUnits,
					"chunks_total": len(dp.Chunks),
					"chunks":       dp.Chunks[start:end],
				}, false), nil

			case "mark":
				planID := paramString(params, "plan_id")
				chunkNum := paramInt(params, "chunk", 0)
				state := strings.ToLower(paramString(params, "state"))
				note := paramString(params, "note")
				if planID == "" || chunkNum < 1 {
					return errorToolResult("plan_id and chunk (>=1) are required for mark"), nil
				}
				switch state {
				case "covered", "next", "missing":
				default:
					return errorToolResult(fmt.Sprintf("invalid chunk state %q (valid: covered, next, missing)", state)), nil
				}
				planPath, err := documentPlanPath(p, planID)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				docPlanMu.Lock()
				var dp DocumentPlan
				ok, err := readJSON(planPath, &dp)
				if err != nil {
					docPlanMu.Unlock()
					return errorToolResult(err.Error()), nil
				}
				if !ok {
					docPlanMu.Unlock()
					return errorToolResult(fmt.Sprintf("document plan %q not found", planID)), nil
				}
				if chunkNum > len(dp.Chunks) {
					docPlanMu.Unlock()
					return errorToolResult(fmt.Sprintf("chunk %d out of range (1..%d)", chunkNum, len(dp.Chunks))), nil
				}
				now := time.Now().UTC().Format(time.RFC3339)
				dp.Chunks[chunkNum-1].State = state
				if note != "" {
					dp.Chunks[chunkNum-1].Note = note
				}
				dp.Chunks[chunkNum-1].UpdatedAt = now
				dp.UpdatedAt = now
				writeErr := atomicWriteJSON(planPath, dp)
				updatedChunk := dp.Chunks[chunkNum-1]
				docPlanMu.Unlock()
				if writeErr != nil {
					return errorToolResult(writeErr.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":    true,
					"plan":  dp.PlanID,
					"chunk": updatedChunk,
				}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unknown omh_document_plan action %q (valid: plan, show, mark)", action)), nil
			}
		},
	})

	// 11. omh_source_trust
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_source_trust",
		Label:         "OMH Source Trust",
		Description:   "Fail-closed mechanical source-trust ceiling evaluator over claims[] (tier x claim_kind).",
		PromptSnippet: "Evaluate claim trust ceilings (fail-closed) over upstream_official, practitioner_heuristic, and unattributed sources with omh_source_trust.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{"type": "string"},
				"claims": map[string]any{
					"type":     "array",
					"maxItems": maxSourceTrustClaims,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tier":        map[string]any{"type": "string", "enum": []string{"upstream_official", "practitioner_heuristic", "unattributed"}},
							"claim_kind":  map[string]any{"type": "string", "enum": []string{"approach", "finding", "completion"}},
							"claim":       map[string]any{"type": "string"},
							"source_id":   map[string]any{"type": "string"},
							"recorded_at": map[string]any{"type": "string"},
						},
						"required": []string{"tier", "claim_kind", "claim"},
					},
				},
			},
			"required": []string{"claims"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			topic := paramString(params, "topic")
			rawClaims, ok := params["claims"]
			if !ok || rawClaims == nil {
				return errorToolResult("claims array is required (fail-closed)"), nil
			}
			rawBytes, err := json.Marshal(rawClaims)
			if err != nil {
				return errorToolResult(fmt.Sprintf("invalid claims payload: %v", err)), nil
			}
			var claims []sourceTrustClaim
			if err := json.Unmarshal(rawBytes, &claims); err != nil {
				return errorToolResult(fmt.Sprintf("invalid claims structure: %v", err)), nil
			}
			if len(claims) == 0 {
				return jsonToolResult(map[string]any{
					"ok":            true,
					"schema":        SourceTrustSchema,
					"topic":         topic,
					"all_allowed":   false,
					"overall_state": "blocked_empty_claims",
					"evaluations":   []sourceTrustEval{},
				}, false), nil
			}
			if len(claims) > maxSourceTrustClaims {
				return errorToolResult(fmt.Sprintf("claims exceeds maximum of %d", maxSourceTrustClaims)), nil
			}

			evals := make([]sourceTrustEval, 0, len(claims))
			allAllowed := true
			blockedCount := 0
			for i, c := range claims {
				ev := evaluateSourceTrustClaim(i, c)
				if !ev.Allowed {
					allAllowed = false
					blockedCount++
				}
				evals = append(evals, ev)
			}
			state := "allowed"
			if !allAllowed {
				state = "blocked_by_trust_ceiling"
			}
			return jsonToolResult(map[string]any{
				"ok":            true,
				"schema":        SourceTrustSchema,
				"topic":         topic,
				"all_allowed":   allAllowed,
				"blocked_count": blockedCount,
				"overall_state": state,
				"evaluations":   evals,
			}, false), nil
		},
	})
}
