package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port of src/runs/background/scheduled-runs.ts: project schedules of workflow scripts (`schedule.create`, `.list`, `.show`,
// `.history`, `.pause`, `.resume`, `.run`, `.run-due`, `.delete`), a durable store per project, timers that fire a schedule
// when it is due, and the bookkeeping of the async workflow each fire starts.

var scheduledRunActions = []string{"schedule.create", "schedule.list", "schedule.show", "schedule.history", "schedule.pause", "schedule.resume", "schedule.run", "schedule.run-due", "schedule.delete"}

const (
	scheduleMaxTimerDelayMs    = 2147483647
	scheduleDefaultMaxPending  = 20
	scheduleMaxHistory         = 100
	scheduleStaleLaunchClaimMs = 5 * 60_000
)

var scheduleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func isScheduledRunAction(a any) bool {
	s, ok := a.(string)
	if !ok {
		return false
	}
	for _, n := range scheduledRunActions {
		if n == s {
			return true
		}
	}
	return false
}

// scheduleConfig is the part of the extension config the schedules read (config.scheduledRuns).
type scheduleConfig struct {
	Enabled    *bool
	MaxPending *int
	StoreRoot  string
}

func scheduledRunsEnabled(c scheduleConfig) bool { return c.Enabled == nil || *c.Enabled }

// scheduledRunStorePath is scheduledRunStorePath: the project's own `.pi/subagents/schedules`, or a directory of the configured
// store root named by a hash of the project path.
func scheduledRunStorePath(cwd, _sessionID, root string) string {
	abs, _ := filepath.Abs(cwd)
	if root == "" {
		return filepath.Join(getProjectSubagentsDir(abs), "schedules")
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(root, hex.EncodeToString(sum[:])[:20])
}

var (
	scheduleRelativeAt = regexp.MustCompile(`^\+(\d+)(s|m|h|d)$`)
	scheduleISOAt      = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?(Z|[+-]\d{2}:\d{2})$`)
	scheduleEveryRe    = regexp.MustCompile(`^(\d+)(m|h|d|w)$`)
)

// parseScheduledRunTime is parseScheduledRunTime: a one-shot delay ("+10m") or an ISO timestamp with a zone, in milliseconds.
func parseScheduledRunTime(at string, now int64) (int64, error) {
	trimmed := jsTrim(at)
	if m := scheduleRelativeAt.FindStringSubmatch(trimmed); m != nil {
		amount, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || amount < 1 || amount > 9007199254740991 {
			return 0, fmt.Errorf(`Invalid at value "%s". Relative delays must be positive, such as "+10m".`, at)
		}
		unit := map[string]int64{"s": 1_000, "m": 60_000, "h": 3_600_000, "d": 86_400_000}[m[2]]
		if amount > (9007199254740991-now)/unit {
			return 0, fmt.Errorf(`Invalid at value "%s". Relative delay is too large.`, at)
		}
		return now + amount*unit, nil
	}
	m := scheduleISOAt.FindStringSubmatch(trimmed)
	if m == nil {
		return 0, fmt.Errorf(`Invalid at value "%s". Use a one-shot delay such as "+10m" or an ISO timestamp with timezone.`, at)
	}
	num := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	year, month, day, hour, minute := num(m[1]), num(m[2]), num(m[3]), num(m[4]), num(m[5])
	second := 0
	if m[6] != "" {
		second = num(m[6])
	}
	frac := 0
	if m[7] != "" {
		frac = num((m[7] + "00")[:3])
	}
	zone := m[8]
	offH, offM := 0, 0
	if zone != "Z" {
		offH, offM = num(zone[1:3]), num(zone[4:6])
	}
	dim := 0
	if month >= 1 && month <= 12 {
		dim = time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	if month < 1 || month > 12 || day < 1 || day > dim || hour > 23 || minute > 59 || second > 59 || offH > 23 || offM > 59 {
		return 0, fmt.Errorf(`Invalid at value "%s". Use a valid ISO timestamp.`, at)
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, frac*1_000_000, time.UTC)
	off := time.Duration(offH)*time.Hour + time.Duration(offM)*time.Minute
	if zone != "Z" && zone[0] == '+' {
		t = t.Add(-off)
	} else if zone != "Z" {
		t = t.Add(off)
	}
	parsed := t.UnixMilli()
	if parsed <= now {
		return 0, fmt.Errorf("Scheduled time %s is in the past.", isoMillis(t))
	}
	return parsed, nil
}

// parseScheduleInterval is parseScheduleInterval: a fixed interval such as "30m", "6h", "2d" or "2w", in milliseconds.
func parseScheduleInterval(every string) (int64, error) {
	m := scheduleEveryRe.FindStringSubmatch(jsTrim(every))
	if m == nil {
		return 0, fmt.Errorf(`Invalid every value "%s". This first recurring slice supports fixed intervals such as "30m", "6h", "2d", or "2w".`, every)
	}
	amount, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || amount < 1 || amount > 9007199254740991 {
		return 0, fmt.Errorf(`Invalid every value "%s". Interval must be positive.`, every)
	}
	unit := map[string]int64{"m": 60_000, "h": 3_600_000, "d": 86_400_000, "w": 604_800_000}[m[2]]
	if amount > 9007199254740991/unit {
		return 0, fmt.Errorf(`Invalid every value "%s". Interval is too large.`, every)
	}
	return amount * unit, nil
}

func scheduleTimestamp(ms int64) string { return isoMillis(time.UnixMilli(ms)) }

func validateScheduleID(id string) (string, error) {
	if !scheduleIDPattern.MatchString(id) {
		return "", errors.New("Schedule id must be 1-64 characters and contain only letters, numbers, '.', '_', or '-'.")
	}
	return id, nil
}

func normalizedComparisonPath(v string) string {
	abs, _ := filepath.Abs(v)
	if runtime.GOOS != "windows" {
		return abs
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	abs = strings.ReplaceAll(abs, "/", `\`)
	switch {
	case strings.HasPrefix(abs, `\\?\UNC\`):
		abs = `\\` + abs[8:]
	case strings.HasPrefix(abs, `\\?\`):
		abs = abs[4:]
	}
	abs = strings.ToLower(filepath.Clean(abs))
	if len(abs) > 3 {
		abs = strings.TrimRight(abs, `\`)
	}
	return abs
}

func schedulePathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(normalizedComparisonPath(root), normalizedComparisonPath(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func scheduleSamePath(a, b string) bool {
	return normalizedComparisonPath(a) == normalizedComparisonPath(b)
}

var gitdirLine = regexp.MustCompile(`(?i)^gitdir:[ \t]*([^\r\n]+)$`)

func resolveGitCommonDirForCheckout(checkoutRoot string) string {
	gitPath := filepath.Join(checkoutRoot, ".git")
	st, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if st.IsDir() {
		real, err := filepath.EvalSymlinks(gitPath)
		if err != nil {
			return ""
		}
		return real
	}
	if !st.Mode().IsRegular() {
		return ""
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	m := gitdirLine.FindStringSubmatch(jsTrim(string(data)))
	if m == nil || m[1] == "" {
		return ""
	}
	target := m[1]
	if !filepath.IsAbs(target) {
		target = filepath.Join(checkoutRoot, target)
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return ""
	}
	return real
}

func resolveTrustedGitConfigRoot(checkoutRoot, commonDir string) string {
	resolved := resolveGitCommonDirForCheckout(checkoutRoot)
	if resolved == "" || !scheduleSamePath(resolved, commonDir) {
		return ""
	}
	configRoot := filepath.Join(checkoutRoot, getConfigDirName())
	real, err := filepath.EvalSymlinks(configRoot)
	if err != nil {
		return ""
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() || !schedulePathWithin(checkoutRoot, real) {
		return ""
	}
	return real
}

func registeredGitWorktreeRoots(projectCwd string) []string {
	out, err := exec.Command("git", "-C", projectCwd, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		if real, err := filepath.EvalSymlinks(jsTrim(strings.TrimPrefix(line, "worktree "))); err == nil {
			roots = append(roots, real)
		}
	}
	return roots
}

func resolveSharedGitConfigRoot(projectCwd string) string {
	repo := resolveGitRepositoryIdentity(projectCwd)
	if repo == nil {
		return ""
	}
	projectConfigRoot, err := filepath.EvalSymlinks(filepath.Join(projectCwd, getConfigDirName()))
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	for _, checkout := range registeredGitWorktreeRoots(projectCwd) {
		if seen[checkout] {
			continue
		}
		seen[checkout] = true
		if resolved := resolveTrustedGitConfigRoot(checkout, repo.CommonDir); resolved != "" && scheduleSamePath(resolved, projectConfigRoot) {
			return resolved
		}
	}
	return ""
}

// scheduleStore is the directory of one project's schedules.
type scheduleStore struct {
	Root       string
	ProjectCwd string // "" when the root is a configured store root (no project containment check)
}

func (s *scheduleStore) assertRoot(create bool) error {
	if s.ProjectCwd == "" {
		if create {
			return os.MkdirAll(s.Root, 0o700)
		}
		return nil
	}
	projectPath, err := filepath.EvalSymlinks(s.ProjectCwd)
	if err != nil {
		if !create && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	existing := s.Root
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	existingPath, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	shared := ""
	if !schedulePathWithin(projectPath, existingPath) {
		shared = resolveSharedGitConfigRoot(s.ProjectCwd)
	}
	trusted := func(c string) bool {
		return schedulePathWithin(projectPath, c) || (shared != "" && schedulePathWithin(shared, c))
	}
	outside := fmt.Errorf("Project schedule root '%s' resolves outside the real project.", s.Root)
	if !trusted(existingPath) {
		return outside
	}
	if !create {
		return nil
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(s.Root)
	if err != nil || !trusted(real) {
		return outside
	}
	return nil
}

// directory is scheduleDir: the real directory of a schedule, created on demand.
func (s *scheduleStore) directory(id string, create bool) (string, error) {
	if err := s.assertRoot(create); err != nil {
		return "", err
	}
	if _, err := validateScheduleID(id); err != nil {
		return "", err
	}
	dir := filepath.Join(s.Root, id)
	st, err := os.Lstat(dir)
	switch {
	case err == nil:
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return "", fmt.Errorf("Schedule path '%s' must be a real directory.", dir)
		}
	case errors.Is(err, fs.ErrNotExist):
		if !create {
			return dir, nil
		}
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	rootPath, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", err
	}
	dirPath, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if !scheduleSamePath(dirPath, filepath.Join(rootPath, id)) {
		return "", fmt.Errorf("Schedule path '%s' escapes the project schedule root.", dir)
	}
	return dir, nil
}

func (s *scheduleStore) ids() ([]string, error) {
	if err := s.assertRoot(false); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && scheduleIDPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func scheduleReadJSON(file, label string) (any, error) {
	data, err := os.ReadFile(file)
	if err == nil {
		var v any
		if v, err = parseJSON(data); err == nil {
			return v, nil
		}
	}
	return nil, fmt.Errorf("Failed to read %s '%s': %s", label, file, err.Error())
}

// scheduleTrigger is the trigger of a schedule: one-shot (`at`) or a fixed interval (`every`).
type scheduleTrigger struct {
	Kind      string
	At        string
	Every     string
	EveryMs   int64
	AnchorAt  string
	NextRunAt string // "" when none
}

// scheduleTarget is the workflow script a schedule runs.
type scheduleTarget struct {
	WorkflowScript string
	Args           *jsObject
	BaseRef        string
	HasBaseRef     bool
}

// scheduleRecord is schedule.json.
type scheduleRecord struct {
	ID               string
	Name             string
	Cwd              string
	Trigger          scheduleTrigger
	Target           scheduleTarget
	CatchUp          string
	TimeoutMs        *int64
	Paused           bool
	SessionOnly      *bool
	Quiet            *bool
	OwnerSessionFile string
	CreatedAt        string
	UpdatedAt        string
	ActiveRunID      string
	LastRunID        string
}

func (r *scheduleRecord) object(public bool) *jsObject {
	o := jo("schemaVersion", 1.0, "id", r.ID, "name", r.Name, "cwd", r.Cwd)
	t := newObject()
	t.set("kind", r.Trigger.Kind)
	if r.Trigger.Kind == "once" {
		t.set("at", r.Trigger.At)
	} else {
		t.set("every", r.Trigger.Every)
		t.set("everyMs", float64(r.Trigger.EveryMs))
		t.set("anchorAt", r.Trigger.AnchorAt)
	}
	if r.Trigger.NextRunAt != "" {
		t.set("nextRunAt", r.Trigger.NextRunAt)
	}
	o.set("trigger", t)
	target := jo("workflowScript", r.Target.WorkflowScript, "args", r.Target.Args)
	if r.Target.HasBaseRef {
		target.set("baseRef", r.Target.BaseRef)
	}
	o.set("target", target)
	o.set("overlap", "skip")
	o.set("catchUp", r.CatchUp)
	if r.TimeoutMs != nil {
		o.set("timeoutMs", float64(*r.TimeoutMs))
	}
	o.set("paused", r.Paused)
	if r.SessionOnly != nil {
		o.set("sessionOnly", *r.SessionOnly)
	}
	if r.OwnerSessionFile != "" && !public {
		o.set("ownerSessionFile", r.OwnerSessionFile)
	}
	if r.Quiet != nil {
		o.set("quiet", *r.Quiet)
	}
	o.set("createdAt", r.CreatedAt)
	o.set("updatedAt", r.UpdatedAt)
	if r.ActiveRunID != "" {
		o.set("activeRunId", r.ActiveRunID)
	}
	if r.LastRunID != "" {
		o.set("lastRunId", r.LastRunID)
	}
	return o
}

func scheduleStr(o *jsObject, k string) (string, bool) {
	s, ok := o.vals[k].(string)
	return s, ok
}

func parseScheduleTarget(v any, file string) (scheduleTarget, error) {
	var zero scheduleTarget
	o, ok := v.(*jsObject)
	if !ok {
		return zero, fmt.Errorf("Schedule record '%s' has invalid trigger or target.", file)
	}
	if script, ok := scheduleStr(o, "workflowScript"); ok && jsTrim(script) != "" {
		t := scheduleTarget{WorkflowScript: jsTrim(script)}
		if ref, has := o.vals["baseRef"]; has && !isUndefinedValue(ref) {
			s, isStr := ref.(string)
			if !isStr || !validGitRef(s) {
				return zero, fmt.Errorf("Schedule record '%s' has an invalid baseRef: %s", file, baseRefError)
			}
			t.BaseRef, t.HasBaseRef = s, true
		}
		args := any(undef{})
		if a, has := o.vals["args"]; has {
			args = a
		}
		norm, err := NormalizeWorkflowArgs(args)
		if err != nil {
			return zero, fmt.Errorf("Schedule record '%s' has invalid args: %s", file, err.Error())
		}
		t.Args = norm
		return t, nil
	}
	if o.has("agent") || o.has("task") {
		return zero, fmt.Errorf("Schedule record '%s' uses a removed legacy agent target; recreate it with target.workflowScript.", file)
	}
	return zero, fmt.Errorf("Schedule record '%s' requires a workflowScript target.", file)
}

const baseRefError = "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."

func parseSchedule(v any, file string) (*scheduleRecord, error) {
	o, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Schedule record '%s' must be a JSON object.", file)
	}
	id, idOK := scheduleStr(o, "id")
	name, nameOK := scheduleStr(o, "name")
	cwd, cwdOK := scheduleStr(o, "cwd")
	created, createdOK := scheduleStr(o, "createdAt")
	updated, updatedOK := scheduleStr(o, "updatedAt")
	paused, pausedOK := o.vals["paused"].(bool)
	if o.vals["schemaVersion"] != 1.0 || !idOK || !nameOK || !cwdOK || !createdOK || !updatedOK || !pausedOK {
		return nil, fmt.Errorf("Schedule record '%s' has invalid required fields.", file)
	}
	if _, err := validateScheduleID(id); err != nil {
		return nil, err
	}
	trig, tok := o.vals["trigger"].(*jsObject)
	_, tgok := o.vals["target"].(*jsObject)
	if !tok || !tgok {
		return nil, fmt.Errorf("Schedule record '%s' has invalid trigger or target.", file)
	}
	catchUp, _ := scheduleStr(o, "catchUp")
	if o.vals["overlap"] != "skip" || (catchUp != "none" && catchUp != "latest") {
		return nil, fmt.Errorf("Schedule record '%s' has unsupported policy fields.", file)
	}
	rec := &scheduleRecord{ID: id, Name: name, Cwd: cwd, CatchUp: catchUp, Paused: paused, CreatedAt: created, UpdatedAt: updated}
	kind, _ := scheduleStr(trig, "kind")
	next, hasNext := scheduleStr(trig, "nextRunAt")
	switch kind {
	case "once":
		at, ok := scheduleStr(trig, "at")
		if !ok || (trig.has("nextRunAt") && !hasNext) {
			return nil, fmt.Errorf("Schedule record '%s' has an invalid one-shot trigger.", file)
		}
		rec.Trigger = scheduleTrigger{Kind: "once", At: at, NextRunAt: next}
	case "interval":
		every, ok1 := scheduleStr(trig, "every")
		everyMs, ok2 := trig.vals["everyMs"].(float64)
		anchor, ok3 := scheduleStr(trig, "anchorAt")
		if !ok1 || !ok2 || !ok3 || !hasNext {
			return nil, fmt.Errorf("Schedule record '%s' has an invalid interval trigger.", file)
		}
		rec.Trigger = scheduleTrigger{Kind: "interval", Every: every, EveryMs: int64(everyMs), AnchorAt: anchor, NextRunAt: next}
	default:
		return nil, fmt.Errorf("Schedule record '%s' has an unsupported trigger.", file)
	}
	if v, has := o.vals["sessionOnly"]; has && !isUndefinedValue(v) {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("Schedule record '%s' has invalid sessionOnly.", file)
		}
		rec.SessionOnly = &b
	}
	if v, has := o.vals["quiet"]; has && !isUndefinedValue(v) {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("Schedule record '%s' has invalid quiet.", file)
		}
		rec.Quiet = &b
	}
	owner, _ := scheduleStr(o, "ownerSessionFile")
	rec.OwnerSessionFile = owner
	if rec.SessionOnly != nil && *rec.SessionOnly && jsTrim(owner) == "" {
		return nil, fmt.Errorf("Schedule record '%s' is session-only but has no owner session file.", file)
	}
	if f, ok := o.vals["timeoutMs"].(float64); ok {
		t := int64(f)
		rec.TimeoutMs = &t
	}
	rec.ActiveRunID, _ = scheduleStr(o, "activeRunId")
	rec.LastRunID, _ = scheduleStr(o, "lastRunId")
	t, err := parseScheduleTarget(o.vals["target"], file)
	if err != nil {
		return nil, err
	}
	rec.Target = t
	return rec, nil
}

func (s *scheduleStore) find(id string) (*scheduleRecord, error) {
	dir, err := s.directory(id, false)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "schedule.json")
	if _, err := os.Stat(file); err != nil {
		return nil, nil
	}
	v, err := scheduleReadJSON(file, "schedule record")
	if err != nil {
		return nil, err
	}
	return parseSchedule(v, file)
}

func (s *scheduleStore) get(id string) (*scheduleRecord, error) {
	r, err := s.find(id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("Schedule '%s' not found.", id)
	}
	return r, nil
}

func (s *scheduleStore) list() ([]*scheduleRecord, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	var out []*scheduleRecord
	for _, id := range ids {
		r, err := s.find(id)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *scheduleStore) write(r *scheduleRecord) error {
	dir, err := s.directory(r.ID, true)
	if err != nil {
		return err
	}
	return writePrivateAtomicJSON(filepath.Join(dir, "schedule.json"), r.object(false))
}

func (s *scheduleStore) delete(id string) error {
	dir, err := s.directory(id, false)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// scheduleRun is one run of a schedule (runs/<id>.json and history.json).
type scheduleRun struct {
	ID          string
	ScheduleID  string
	PlannedAt   string
	DueReason   string // timer, run-due, manual
	State       string // running, skipped, missed, completed, failed_launch, failed_run
	StartedAt   string
	CompletedAt string
	AsyncID     string
	AsyncDir    string
	Error       string
}

func (r *scheduleRun) object() *jsObject {
	o := jo("schemaVersion", 1.0, "id", r.ID, "scheduleId", r.ScheduleID, "plannedAt", r.PlannedAt, "dueReason", r.DueReason, "state", r.State)
	for _, kv := range [][2]string{{"startedAt", r.StartedAt}, {"completedAt", r.CompletedAt}, {"asyncId", r.AsyncID}, {"asyncDir", r.AsyncDir}, {"error", r.Error}} {
		if kv[1] != "" {
			o.set(kv[0], kv[1])
		}
	}
	return o
}

func parseScheduleRun(v any) *scheduleRun {
	o, _ := v.(*jsObject)
	if o == nil {
		return nil
	}
	r := &scheduleRun{}
	r.ID, _ = scheduleStr(o, "id")
	r.ScheduleID, _ = scheduleStr(o, "scheduleId")
	r.PlannedAt, _ = scheduleStr(o, "plannedAt")
	r.DueReason, _ = scheduleStr(o, "dueReason")
	r.State, _ = scheduleStr(o, "state")
	r.StartedAt, _ = scheduleStr(o, "startedAt")
	r.CompletedAt, _ = scheduleStr(o, "completedAt")
	r.AsyncID, _ = scheduleStr(o, "asyncId")
	r.AsyncDir, _ = scheduleStr(o, "asyncDir")
	r.Error, _ = scheduleStr(o, "error")
	return r
}

func (s *scheduleStore) history(id string) ([]*scheduleRun, error) {
	dir, err := s.directory(id, false)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "history.json")
	if _, err := os.Stat(file); err != nil {
		return nil, nil
	}
	v, err := scheduleReadJSON(file, "schedule history")
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsObject)
	runs, isArr := []any(nil), false
	if o != nil {
		runs, isArr = o.vals["runs"].([]any)
	}
	if o == nil || o.vals["schemaVersion"] != 1.0 || !isArr {
		return nil, fmt.Errorf("Schedule history '%s' has invalid fields.", file)
	}
	out := make([]*scheduleRun, 0, len(runs))
	for _, r := range runs {
		if p := parseScheduleRun(r); p != nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *scheduleStore) appendEventLine(dir string, rec *jsObject) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(marshalJSON(rec, "") + "\n")
	return err
}

func (s *scheduleStore) writeRun(sch *scheduleRecord, run *scheduleRun, event string) error {
	dir, err := s.directory(sch.ID, true)
	if err != nil {
		return err
	}
	if err := writePrivateAtomicJSON(filepath.Join(dir, "runs", run.ID+".json"), run.object()); err != nil {
		return err
	}
	prev, err := s.history(sch.ID)
	if err != nil {
		return err
	}
	runs := []any{run.object()}
	for _, p := range prev {
		if p.ID != run.ID {
			runs = append(runs, p.object())
		}
	}
	if len(runs) > scheduleMaxHistory {
		runs = runs[:scheduleMaxHistory]
	}
	if err := writePrivateAtomicJSON(filepath.Join(dir, "history.json"), jo("schemaVersion", 1.0, "runs", runs)); err != nil {
		return err
	}
	return s.appendEventLine(dir, jo("schemaVersion", 1.0, "timestamp", isoMillis(time.Now()), "event", event, "scheduleId", sch.ID, "runId", run.ID, "state", run.State))
}

func (s *scheduleStore) appendEvent(sch *scheduleRecord, event string) error {
	dir, err := s.directory(sch.ID, true)
	if err != nil {
		return err
	}
	return s.appendEventLine(dir, jo("schemaVersion", 1.0, "timestamp", isoMillis(time.Now()), "event", event, "scheduleId", sch.ID))
}

// ---- the manager ----

// scheduleCtx is the session a schedule operation runs for: its project and its session identity, as the original snapshots them.
type scheduleCtx struct {
	Cwd         string
	SessionID   string
	SessionFile string // "" when the session has no file
	Live        any    // the host context a launch runs with (the live registration); tests leave it nil
}

// scheduleLaunchResult is what launching a scheduled workflow reported.
type scheduleLaunchResult struct {
	Text     string
	IsError  bool
	AsyncID  string
	AsyncDir string
}

// scheduleResult is the answer to a schedule action.
type scheduleResult struct {
	Text    string
	IsError bool
	Details *jsObject
}

// scheduleTimers is setTimeout and clearTimeout, replaceable for tests.
type scheduleTimers interface {
	SetTimeout(fn func(), delayMs int64) any
	ClearTimeout(h any)
}

type realScheduleTimers struct{}

func (realScheduleTimers) SetTimeout(fn func(), delayMs int64) any {
	return time.AfterFunc(time.Duration(delayMs)*time.Millisecond, fn)
}
func (realScheduleTimers) ClearTimeout(h any) {
	if t, ok := h.(*time.Timer); ok {
		t.Stop()
	}
}

// scheduleWarn and scheduleLogError are console.warn and console.error of the original, replaceable for tests.
var (
	scheduleWarn     = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	scheduleLogError = func(msg string, err error) { fmt.Fprintln(os.Stderr, msg, err) }
)

type scheduledRunDeps struct {
	Config    scheduleConfig
	Launch    func(params map[string]any, ctx scheduleCtx) scheduleLaunchResult
	StoreRoot string
	Now       func() int64
	RandomID  func() string
	// CapabilityCeilingActive reports whether the session runs under a capability ceiling (a schedule is never persisted then).
	CapabilityCeilingActive func(sessionID string) bool
	Timers                  scheduleTimers
}

type scheduledRunManager struct {
	mu       sync.Mutex
	deps     scheduledRunDeps
	store    *scheduleStore
	stores   map[string]*scheduleStore
	contexts map[string]scheduleCtx
	timers   map[string]any
	observed map[string]bool
	now      func() int64
}

func newScheduledRunManager(deps scheduledRunDeps) *scheduledRunManager {
	m := &scheduledRunManager{deps: deps, stores: map[string]*scheduleStore{}, contexts: map[string]scheduleCtx{}, timers: map[string]any{}, observed: map[string]bool{}}
	m.now = deps.Now
	if m.now == nil {
		m.now = func() int64 { return time.Now().UnixMilli() }
	}
	if m.deps.RandomID == nil {
		m.deps.RandomID = func() string { return randomUUID()[:8] }
	}
	if m.deps.Timers == nil {
		m.deps.Timers = realScheduleTimers{}
	}
	return m
}

func normalizedSessionFile(v string) string {
	if jsTrim(v) == "" {
		return ""
	}
	abs, _ := filepath.Abs(v)
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

func scheduleBelongsToSession(r *scheduleRecord, ctx scheduleCtx) bool {
	if r.SessionOnly == nil || !*r.SessionOnly {
		return true
	}
	owner := normalizedSessionFile(r.OwnerSessionFile)
	return owner != "" && owner == normalizedSessionFile(ctx.SessionFile)
}

func listScheduledRunSummaries(cwd, root string) ([]*scheduleRecord, error) {
	abs, _ := filepath.Abs(cwd)
	s := &scheduleStore{Root: scheduledRunStorePath(cwd, "", root)}
	if root == "" {
		s.ProjectCwd = abs
	}
	return s.list()
}

func (m *scheduledRunManager) newID() (id string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return m.deps.RandomID(), nil
}

// bindSession is bindSession: a session starts in a project, whose schedules are restored and armed.
func (m *scheduledRunManager) bindSession(ctx scheduleCtx) error {
	if !scheduledRunsEnabled(m.deps.Config) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selectProject(ctx.Cwd, ctx)
}

func (m *scheduledRunManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.timers {
		m.deps.Timers.ClearTimeout(t)
	}
	m.timers = map[string]any{}
	m.store = nil
	m.stores = map[string]*scheduleStore{}
	m.contexts = map[string]scheduleCtx{}
	m.observed = map[string]bool{}
}

func (m *scheduledRunManager) observedCompletionRunIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sortedKeys(m.observed)
}

// referencedAsyncRunIDs is the async runs the schedules name: those observed and those recorded in any run history.
func (m *scheduledRunManager) referencedAsyncRunIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := map[string]bool{}
	for k := range m.observed {
		ids[k] = true
	}
	for _, s := range m.stores {
		sids, _ := s.ids()
		for _, sid := range sids {
			runs, _ := s.history(sid)
			for _, r := range runs {
				if r.AsyncID != "" {
					ids[r.AsyncID] = true
				}
			}
		}
	}
	return sortedKeys(ids)
}

// handleAsyncCompletion is handleAsyncCompletion: the end of an async run finishes the schedule run that started it.
func (m *scheduledRunManager) handleAsyncCompletion(payload map[string]any) {
	asyncID, _ := payload["runId"].(string)
	if asyncID == "" {
		asyncID, _ = payload["id"].(string)
	}
	if asyncID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	roots := make([]string, 0, len(m.stores))
	for r := range m.stores {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, root := range roots {
		store := m.stores[root]
		ids, err := store.ids()
		if err != nil {
			scheduleLogError(fmt.Sprintf("Failed to inspect schedule store '%s' during async completion:", store.Root), err)
			continue
		}
		for _, id := range ids {
			if m.finishMatching(store, id, asyncID, payload) {
				return
			}
		}
	}
}

func (m *scheduledRunManager) finishMatching(store *scheduleStore, id, asyncID string, payload map[string]any) (done bool) {
	defer func() {
		if r := recover(); r != nil {
			scheduleLogError(fmt.Sprintf("Failed to inspect schedule '%s' in '%s' during async completion:", id, store.Root), fmt.Errorf("%v", r))
			done = false
		}
	}()
	sch, err := store.get(id)
	if err == nil {
		var runs []*scheduleRun
		if runs, err = store.history(id); err == nil {
			for _, run := range runs {
				if run.AsyncID == asyncID && run.State == "running" {
					summary, _ := payload["summary"].(string)
					ok, _ := payload["success"].(bool)
					if ferr := m.finishRun(store, sch, run, ok, summary); ferr != nil {
						err = ferr
						break
					}
					return true
				}
			}
		}
	}
	if err != nil {
		scheduleLogError(fmt.Sprintf("Failed to inspect schedule '%s' in '%s' during async completion:", id, store.Root), err)
	}
	return false
}

func scheduleText(text string, schedules []*scheduleRecord, runs []*scheduleRun, isError bool) scheduleResult {
	sched := newObject()
	if schedules != nil {
		recs := make([]any, len(schedules))
		for i, s := range schedules {
			recs[i] = s.object(true)
		}
		sched.set("records", recs)
	}
	if runs != nil {
		rs := make([]any, len(runs))
		for i, r := range runs {
			rs[i] = r.object()
		}
		sched.set("runs", rs)
	}
	return scheduleResult{Text: text, IsError: isError, Details: jo("mode", "management", "results", []any{}, "schedules", sched)}
}

func scheduleFail(text string) scheduleResult { return scheduleText(text, nil, nil, true) }

func targetLabel(t scheduleTarget) string {
	if p, ok := PreviewSimpleWorkflowRun(t.WorkflowScript); ok && p.Agent != nil && *p.Agent != "" {
		return "workflowScript -> agent " + *p.Agent
	}
	return "workflowScript (dynamic)"
}

func schedParamString(p map[string]any, k string) (string, bool) {
	s, ok := p[k].(string)
	return s, ok
}

func paramTrim(p map[string]any, k string) string {
	s, _ := schedParamString(p, k)
	return jsTrim(s)
}

// sanitizeTarget is sanitizeTarget: the target of schedule.create, or why it is refused.
func sanitizeScheduleTarget(p map[string]any) (*scheduleTarget, string) {
	if defined(p, "tasks") || defined(p, "chain") {
		return nil, "Recurring schedules require workflowScript; legacy tasks and chain inputs are unsupported."
	}
	if defined(p, "agent") || defined(p, "task") {
		return nil, "schedule.create requires workflowScript. Use workflowScript: \"return runs.run('main', { agent, task })\"."
	}
	script, ok := p["workflowScript"].(string)
	if !ok || jsTrim(script) == "" {
		return nil, "schedule.create requires a non-empty workflowScript."
	}
	if p["context"] == "fork" {
		return nil, "Scheduled runs require fresh context."
	}
	if p["async"] == false {
		return nil, "Scheduled runs are always async."
	}
	t := &scheduleTarget{WorkflowScript: jsTrim(script)}
	if ref, has := p["baseRef"]; has && !isUndefined(ref) {
		s, isStr := ref.(string)
		if !isStr || !validGitRef(s) {
			return nil, baseRefError
		}
		t.BaseRef, t.HasBaseRef = s, true
	}
	acceptance, msg := normalizeGateAcceptance(p["gate"], p["acceptance"])
	if msg != "" {
		return nil, msg
	}
	if errs := validateAcceptanceInput(acceptance, "acceptance"); len(errs) > 0 {
		return nil, strings.Join(errs, " ")
	}
	argsV := any(undef{})
	if defined(p, "args") {
		argsV = parseParamJSON(p["args"])
	}
	args, err := NormalizeWorkflowArgs(argsV)
	if err != nil {
		return nil, err.Error()
	}
	t.Args = args
	return t, ""
}

func (m *scheduledRunManager) quietFlag(s *scheduleRecord) bool { return s.Quiet != nil && *s.Quiet }

func executionParamsOf(s *scheduleRecord, quiet bool) map[string]any {
	origin := map[string]any{"id": s.ID}
	if s.Name != "" {
		origin["name"] = s.Name
	}
	if quiet {
		origin["quiet"] = true
	}
	p := map[string]any{"workflowScript": s.Target.WorkflowScript, "args": s.Target.Args, "async": true, "context": "fresh", "cwd": s.Cwd, "mission": false, "scheduleOrigin": origin}
	if s.Target.HasBaseRef {
		p["baseRef"] = s.Target.BaseRef
	}
	if s.TimeoutMs != nil {
		p["timeoutMs"] = float64(*s.TimeoutMs)
	}
	return p
}

func scheduleNext(t scheduleTrigger, planned, now int64) string {
	if t.Kind == "once" {
		return ""
	}
	next := planned + t.EveryMs
	for next <= now {
		next += t.EveryMs
	}
	return scheduleTimestamp(next)
}

func nextRunAtOf(s *scheduleRecord) (int64, bool, error) {
	if s.Trigger.NextRunAt == "" {
		return 0, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.Trigger.NextRunAt)
	if err != nil {
		return 0, false, fmt.Errorf("Schedule '%s' has invalid nextRunAt.", s.ID)
	}
	return t.UnixMilli(), true, nil
}

func duePlannedAt(s *scheduleRecord, now int64) (int64, bool, error) {
	next, ok, err := nextRunAtOf(s)
	if err != nil || !ok || next > now || s.CatchUp != "latest" || s.Trigger.Kind != "interval" {
		return next, ok, err
	}
	return next + ((now-next)/s.Trigger.EveryMs)*s.Trigger.EveryMs, true, nil
}

func hasPendingScheduleWork(s *scheduleRecord) bool {
	return s.ActiveRunID != "" || s.Trigger.NextRunAt != ""
}

func (m *scheduledRunManager) resolveMaxPending() int {
	if m.deps.Config.MaxPending != nil && *m.deps.Config.MaxPending >= 1 {
		return *m.deps.Config.MaxPending
	}
	return scheduleDefaultMaxPending
}

// handleToolCall is handleToolCall: one schedule action.
func (m *scheduledRunManager) handleToolCall(p map[string]any, ctx scheduleCtx) (res scheduleResult) {
	defer func() {
		if r := recover(); r != nil {
			res = scheduleFail(fmt.Sprint(r))
		}
	}()
	if !scheduledRunsEnabled(m.deps.Config) {
		return scheduleFail("Scheduled runs are disabled by scheduledRuns.enabled=false.")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cwd := ctx.Cwd
	if c, ok := p["cwd"].(string); ok && c != "" {
		cwd = c
	}
	if err := m.selectProject(cwd, ctx); err != nil {
		return scheduleFail(err.Error())
	}
	action, _ := p["action"].(string)
	var out scheduleResult
	var err error
	switch action {
	case "schedule.create":
		out, err = m.create(p, ctx)
	case "schedule.list":
		out, err = m.list()
	case "schedule.show":
		out, err = m.show(p)
	case "schedule.history":
		out, err = m.historyAction(p)
	case "schedule.pause":
		out, err = m.pause(p, true)
	case "schedule.resume":
		out, err = m.pause(p, false)
	case "schedule.run":
		out, err = m.runManual(p)
	case "schedule.run-due":
		out, err = m.runDue()
	case "schedule.delete":
		out, err = m.remove(p)
	default:
		return scheduleFail("Unknown schedule action: " + action)
	}
	if err != nil {
		return scheduleFail(err.Error())
	}
	return out
}

func (m *scheduledRunManager) requireStore() (*scheduleStore, error) {
	if m.store == nil {
		return nil, errors.New("Schedule store is unavailable.")
	}
	return m.store, nil
}

func (m *scheduledRunManager) requireContext(s *scheduleStore) (scheduleCtx, error) {
	c, ok := m.contexts[s.Root]
	if !ok {
		return scheduleCtx{}, errors.New("Schedule runtime context is unavailable.")
	}
	return c, nil
}

func (m *scheduledRunManager) resolve(p map[string]any) (*scheduleRecord, error) {
	id := paramTrim(p, "id")
	if id == "" {
		action, _ := p["action"].(string)
		return nil, fmt.Errorf("%s requires id.", action)
	}
	s, err := m.requireStore()
	if err != nil {
		return nil, err
	}
	return s.get(id)
}

func (m *scheduledRunManager) create(p map[string]any, ctx scheduleCtx) (scheduleResult, error) {
	store, err := m.requireStore()
	if err != nil {
		return scheduleResult{}, err
	}
	target, msg := sanitizeScheduleTarget(p)
	if msg != "" {
		return scheduleFail(msg), nil
	}
	at, every := paramTrim(p, "at"), paramTrim(p, "every")
	if (at != "") == (every != "") {
		return scheduleFail("schedule.create requires exactly one trigger: at or every."), nil
	}
	if v := p["overlap"]; v != nil && v != "skip" {
		return scheduleFail("This first recurring slice supports overlap='skip' only."), nil
	}
	if v := p["catchUp"]; v != nil && v != "none" && v != "latest" {
		return scheduleFail("catchUp must be 'none' or 'latest'."), nil
	}
	for _, k := range []string{"missionId", "mission", "missionUpdate", "missionStatus", "missionScope"} {
		if defined(p, k) {
			return scheduleFail("Mission attachment is deferred from this first schedule slice."), nil
		}
	}
	if defined(p, "on") || defined(p, "timezone") || every == "day" || every == "week" || every == "month" || every == "year" {
		return scheduleFail("Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."), nil
	}
	quiet, hasQuiet := p["quiet"]
	if hasQuiet && !isUndefined(quiet) {
		if _, isBool := quiet.(bool); !isBool {
			return scheduleFail("quiet must be a boolean."), nil
		}
	}
	if at != "" && quiet == true {
		return scheduleFail("quiet is only supported for recurring schedules."), nil
	}
	sessionOnly := p["sessionOnly"] == true
	if c, ok := p["cwd"].(string); sessionOnly && ok && !scheduleSamePath(c, ctx.Cwd) {
		return scheduleFail("sessionOnly schedules cannot use an explicit cross-project cwd."), nil
	}
	owner := ""
	if sessionOnly {
		owner = ctx.SessionFile
		if owner == "" {
			return scheduleFail("sessionOnly schedules require a persisted current session."), nil
		}
	}
	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = "unknown"
	}
	if m.deps.CapabilityCeilingActive != nil && m.deps.CapabilityCeilingActive(sessionID) {
		return scheduleFail("Cannot persist a schedule while a capability ceiling is active."), nil
	}
	existing, err := store.list()
	if err != nil {
		return scheduleResult{}, err
	}
	pending := 0
	for _, s := range existing {
		if hasPendingScheduleWork(s) {
			pending++
		}
	}
	if max := m.resolveMaxPending(); pending >= max {
		return scheduleFail(fmt.Sprintf("Schedule limit reached (%d).", max)), nil
	}
	id := paramTrim(p, "id")
	if id == "" {
		if id, err = m.newID(); err != nil {
			return scheduleResult{}, err
		}
	}
	if _, err := validateScheduleID(id); err != nil {
		return scheduleResult{}, err
	}
	ids, err := store.ids()
	if err != nil {
		return scheduleResult{}, err
	}
	for _, e := range ids {
		if e == id {
			return scheduleFail(fmt.Sprintf("Schedule '%s' already exists.", id)), nil
		}
	}
	now := m.now()
	var trigger scheduleTrigger
	if at != "" {
		planned, err := parseScheduledRunTime(at, now)
		if err != nil {
			return scheduleResult{}, err
		}
		trigger = scheduleTrigger{Kind: "once", At: at, NextRunAt: scheduleTimestamp(planned)}
	} else {
		everyMs, err := parseScheduleInterval(every)
		if err != nil {
			return scheduleResult{}, err
		}
		trigger = scheduleTrigger{Kind: "interval", Every: every, EveryMs: everyMs, AnchorAt: scheduleTimestamp(now), NextRunAt: scheduleTimestamp(now + everyMs)}
	}
	name := paramTrim(p, "name")
	if name == "" {
		name = targetLabel(*target)
	}
	absCwd := ctx.Cwd
	if c, ok := p["cwd"].(string); ok && c != "" {
		absCwd = c
	}
	absCwd, _ = filepath.Abs(absCwd)
	catchUp := "latest"
	if c, ok := p["catchUp"].(string); ok {
		catchUp = c
	}
	rec := &scheduleRecord{ID: id, Name: name, Cwd: absCwd, Trigger: trigger, Target: *target, CatchUp: catchUp, CreatedAt: scheduleTimestamp(now), UpdatedAt: scheduleTimestamp(now)}
	if f, ok := p["timeoutMs"].(float64); ok {
		t := int64(f)
		rec.TimeoutMs = &t
	}
	if sessionOnly {
		t := true
		abs, _ := filepath.Abs(owner)
		rec.SessionOnly, rec.OwnerSessionFile = &t, abs
	}
	if trigger.Kind == "interval" && quiet == true {
		t := true
		rec.Quiet = &t
	}
	if err := store.write(rec); err != nil {
		return scheduleResult{}, err
	}
	if err := store.appendEvent(rec, "schedule.created"); err != nil {
		return scheduleResult{}, err
	}
	if err := m.arm(rec, store, 0, false); err != nil {
		return scheduleResult{}, err
	}
	trig := "every " + every
	if at != "" {
		trig = "at " + at
	}
	yes := func(b *bool) string {
		if b != nil && *b {
			return "yes"
		}
		return "no"
	}
	return scheduleText(fmt.Sprintf("Created schedule %s.\nName: %s\nTrigger: %s\nSession only: %s\nQuiet: %s\nNext: %s\nTarget: %s", id, rec.Name, trig, yes(rec.SessionOnly), yes(rec.Quiet), rec.Trigger.NextRunAt, targetLabel(rec.Target)), []*scheduleRecord{rec}, nil, false), nil
}

func (m *scheduledRunManager) list() (scheduleResult, error) {
	store, err := m.requireStore()
	if err != nil {
		return scheduleResult{}, err
	}
	schedules, err := store.list()
	if err != nil {
		return scheduleResult{}, err
	}
	sort.SliceStable(schedules, func(i, j int) bool {
		return localeCompare(schedules[i].Trigger.NextRunAt, schedules[j].Trigger.NextRunAt) < 0
	})
	if len(schedules) == 0 {
		return scheduleText("No project schedules.", []*scheduleRecord{}, nil, false), nil
	}
	lines := []string{fmt.Sprintf("Project schedules: %d", len(schedules))}
	for _, s := range schedules {
		next := s.Trigger.NextRunAt
		if next == "" {
			next = "no next run"
		}
		kind := "project"
		if s.SessionOnly != nil && *s.SessionOnly {
			kind = "session-only"
		}
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | %s | %s", s.ID, scheduleStateOf(s), next, kind, s.Name))
	}
	return scheduleText(strings.Join(lines, "\n"), schedules, nil, false), nil
}

func scheduleStateOf(s *scheduleRecord) string {
	switch {
	case s.Paused:
		return "paused"
	case s.ActiveRunID != "":
		return "running"
	}
	return "scheduled"
}

func (m *scheduledRunManager) show(p map[string]any) (scheduleResult, error) {
	s, err := m.resolve(p)
	if err != nil {
		return scheduleResult{}, err
	}
	yes := func(b *bool) string {
		if b != nil && *b {
			return "yes"
		}
		return "no"
	}
	next := s.Trigger.NextRunAt
	if next == "" {
		next = "none"
	}
	lines := []string{"Schedule: " + s.ID, "Name: " + s.Name, "State: " + scheduleStateOf(s), "Session only: " + yes(s.SessionOnly), "Quiet: " + yes(s.Quiet), "Target: " + targetLabel(s.Target), "CWD: " + bgShortenPath(s.Cwd), "Next: " + next, "Catch up: " + s.CatchUp}
	if s.ActiveRunID != "" {
		lines = append(lines, "Active run: "+s.ActiveRunID)
	}
	return scheduleText(strings.Join(lines, "\n"), []*scheduleRecord{s}, nil, false), nil
}

func (m *scheduledRunManager) historyAction(p map[string]any) (scheduleResult, error) {
	s, err := m.resolve(p)
	if err != nil {
		return scheduleResult{}, err
	}
	runs, err := m.store.history(s.ID)
	if err != nil {
		return scheduleResult{}, err
	}
	if runs == nil {
		runs = []*scheduleRun{}
	}
	text := fmt.Sprintf("No runs recorded for schedule %s.", s.ID)
	if len(runs) > 0 {
		lines := []string{"Schedule history: " + s.ID}
		for _, r := range runs {
			line := fmt.Sprintf("- %s | %s | %s", r.ID, r.State, r.PlannedAt)
			if r.AsyncID != "" {
				line += " | async " + r.AsyncID
			}
			lines = append(lines, line)
		}
		text = strings.Join(lines, "\n")
	}
	return scheduleText(text, []*scheduleRecord{s}, runs, false), nil
}

func (m *scheduledRunManager) pause(p map[string]any, paused bool) (scheduleResult, error) {
	s, err := m.resolve(p)
	if err != nil {
		return scheduleResult{}, err
	}
	word := map[bool]string{true: "paused", false: "active"}[paused]
	if s.Paused == paused {
		return scheduleText(fmt.Sprintf("Schedule %s is already %s.", s.ID, word), []*scheduleRecord{s}, nil, false), nil
	}
	s.Paused = paused
	s.UpdatedAt = scheduleTimestamp(m.now())
	store, _ := m.requireStore()
	if err := store.write(s); err != nil {
		return scheduleResult{}, err
	}
	ev := map[bool]string{true: "schedule.paused", false: "schedule.resumed"}[paused]
	if err := store.appendEvent(s, ev); err != nil {
		return scheduleResult{}, err
	}
	if paused {
		m.clearTimer(store, s.ID)
	} else if err := m.restoreOne(store, s, 0, true); err != nil {
		return scheduleResult{}, err
	}
	return scheduleText(fmt.Sprintf("%s schedule %s.", map[bool]string{true: "Paused", false: "Resumed"}[paused], s.ID), []*scheduleRecord{s}, nil, false), nil
}

func (m *scheduledRunManager) runManual(p map[string]any) (scheduleResult, error) {
	store, err := m.requireStore()
	if err != nil {
		return scheduleResult{}, err
	}
	sch, err := m.resolve(p)
	if err != nil {
		return scheduleResult{}, err
	}
	ctx, err := m.requireContext(store)
	if err != nil {
		return scheduleResult{}, err
	}
	if !scheduleBelongsToSession(sch, ctx) {
		return scheduleText(fmt.Sprintf("Skipped schedule %s: current session is not its owner.", sch.ID), []*scheduleRecord{sch}, nil, false), nil
	}
	if v, has := p["quiet"]; has && !isUndefined(v) {
		if _, isBool := v.(bool); !isBool {
			return scheduleFail("quiet must be a boolean."), nil
		}
	}
	run, err := m.launch(store, sch, m.now(), "manual", false, p["quiet"] == true)
	if err != nil {
		return scheduleResult{}, err
	}
	updated, err := store.get(sch.ID)
	if err != nil {
		return scheduleResult{}, err
	}
	if run.State == "running" {
		now := m.now()
		if updated.Trigger.Kind == "interval" {
			updated.Trigger.NextRunAt = scheduleTimestamp(now + updated.Trigger.EveryMs)
		} else {
			updated.Trigger.NextRunAt = ""
		}
		updated.UpdatedAt = scheduleTimestamp(now)
		if err := store.write(updated); err != nil {
			return scheduleResult{}, err
		}
		if err := store.appendEvent(updated, "schedule.manual_satisfied"); err != nil {
			return scheduleResult{}, err
		}
		if err := m.arm(updated, store, 0, false); err != nil {
			return scheduleResult{}, err
		}
	}
	latest, err := store.get(sch.ID)
	if err != nil {
		return scheduleResult{}, err
	}
	attached := ""
	if run.AsyncID != "" {
		attached = " (async " + run.AsyncID + ")"
	}
	return scheduleText(fmt.Sprintf("Manual schedule run %s: %s%s.", run.ID, run.State, attached), []*scheduleRecord{latest}, []*scheduleRun{run}, run.State == "failed_launch"), nil
}

func (m *scheduledRunManager) runDue() (scheduleResult, error) {
	store, err := m.requireStore()
	if err != nil {
		return scheduleResult{}, err
	}
	ctx, err := m.requireContext(store)
	if err != nil {
		return scheduleResult{}, err
	}
	all, err := store.list()
	if err != nil {
		return scheduleResult{}, err
	}
	var due []*scheduleRecord
	for _, s := range all {
		next, ok, nerr := nextRunAtOf(s)
		if nerr != nil {
			return scheduleResult{}, nerr
		}
		if scheduleBelongsToSession(s, ctx) && !s.Paused && ok && next <= m.now() {
			due = append(due, s)
		}
	}
	runs := []*scheduleRun{}
	for _, s := range due {
		planned, _, err := duePlannedAt(s, m.now())
		if err != nil {
			return scheduleResult{}, err
		}
		var run *scheduleRun
		if s.ActiveRunID == "" && s.CatchUp == "none" && planned < m.now() {
			run, err = m.recordMissed(store, s, planned, "run-due")
		} else {
			run, err = m.launch(store, s, planned, "run-due", true, false)
		}
		if err != nil {
			return scheduleResult{}, err
		}
		runs = append(runs, run)
	}
	text := "No schedules are due."
	if len(runs) > 0 {
		text = fmt.Sprintf("Processed %d due schedule(s).", len(runs))
	}
	listed, err := store.list()
	if err != nil {
		return scheduleResult{}, err
	}
	if listed == nil {
		listed = []*scheduleRecord{}
	}
	return scheduleText(text, listed, runs, false), nil
}

var scheduleTerminalAsync = map[string]bool{"complete": true, "failed": true, "stopped": true, "rejected": true}

func (m *scheduledRunManager) remove(p map[string]any) (scheduleResult, error) {
	sch, err := m.resolve(p)
	if err != nil {
		return scheduleResult{}, err
	}
	store, _ := m.requireStore()
	if sch.ActiveRunID != "" {
		runs, err := store.history(sch.ID)
		if err != nil {
			return scheduleResult{}, err
		}
		terminal := false
		for _, run := range runs {
			if run.ID != sch.ActiveRunID {
				continue
			}
			if run.ScheduleID == sch.ID && run.State == "running" && run.AsyncID != "" && run.AsyncDir != "" {
				v, rerr := scheduleReadJSON(filepath.Join(run.AsyncDir, "status.json"), "async status")
				if rerr != nil {
					return scheduleResult{}, rerr
				}
				if st, ok := v.(*jsObject); ok {
					state, isStr := st.vals["state"].(string)
					terminal = st.vals["runId"] == run.AsyncID && isStr && scheduleTerminalAsync[state]
				}
			}
			break
		}
		if !terminal {
			return scheduleText(fmt.Sprintf("Schedule %s has active run %s; stop that run before deleting the schedule.", sch.ID, sch.ActiveRunID), []*scheduleRecord{sch}, nil, true), nil
		}
	}
	m.clearTimer(store, sch.ID)
	if err := store.appendEvent(sch, "schedule.deleted"); err != nil {
		return scheduleResult{}, err
	}
	if err := store.delete(sch.ID); err != nil {
		return scheduleResult{}, err
	}
	return scheduleText("Deleted schedule "+sch.ID+".", nil, nil, false), nil
}

func (m *scheduledRunManager) restore(store *scheduleStore) error {
	all, err := store.list()
	if err != nil {
		return err
	}
	for _, s := range all {
		if err := m.restoreOne(store, s, 0, true); err != nil {
			return err
		}
	}
	return nil
}

func isENOENT(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "no such file"))
}

// restoreOne is restoreOne: what a schedule needs when a session (re)binds its project: its running run reconciled with the
// async status, and its timer armed (and a miss recorded for a catch-up-none schedule that is overdue).
func (m *scheduledRunManager) restoreOne(store *scheduleStore, sch *scheduleRecord, notBefore int64, rearm bool) error {
	ctx, err := m.requireContext(store)
	if err != nil {
		return err
	}
	if !scheduleBelongsToSession(sch, ctx) {
		return nil
	}
	if sch.ActiveRunID != "" {
		runs, err := store.history(sch.ID)
		if err != nil {
			return err
		}
		var run *scheduleRun
		for _, r := range runs {
			if r.ID == sch.ActiveRunID {
				run = r
				break
			}
		}
		if run != nil && run.State == "running" && run.AsyncID != "" {
			m.observed[run.AsyncID] = true
		}
		if run != nil && run.State == "running" && run.AsyncDir != "" {
			v, rerr := scheduleReadJSON(filepath.Join(run.AsyncDir, "status.json"), "async status")
			if rerr != nil {
				if !isENOENT(rerr) {
					return rerr
				}
			} else if st, ok := v.(*jsObject); ok {
				if state, _ := st.vals["state"].(string); scheduleTerminalAsync[state] {
					msg, _ := st.vals["error"].(string)
					if err := m.finishRun(store, sch, run, state == "complete", msg); err != nil {
						return err
					}
				}
			}
		}
		var started int64
		hasStart := false
		if run != nil && run.StartedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, run.StartedAt); err == nil {
				started, hasStart = t.UnixMilli(), true
			}
		}
		if sch.ActiveRunID != "" && (run == nil || run.State != "running" || (run.AsyncID == "" && hasStart && started+scheduleStaleLaunchClaimMs <= m.now())) {
			if run != nil && run.State == "running" {
				run.State = "failed_launch"
				run.CompletedAt = scheduleTimestamp(m.now())
				run.Error = "Recovered a stale launch claim before an async run was attached."
				if err := store.writeRun(sch, run, "schedule.run.failed"); err != nil {
					return err
				}
			}
			sch.ActiveRunID = ""
			sch.UpdatedAt = scheduleTimestamp(m.now())
			if err := store.write(sch); err != nil {
				return err
			}
			if dir, derr := store.directory(sch.ID, false); derr == nil {
				_ = os.Remove(filepath.Join(dir, "active.lock"))
			}
		}
	}
	if !rearm || sch.Paused {
		return nil
	}
	next, ok, err := nextRunAtOf(sch)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if sch.ActiveRunID == "" && next < m.now() && sch.CatchUp == "none" {
		if _, err := m.recordMissed(store, sch, next, "timer"); err != nil {
			if notBefore != 0 {
				_ = m.arm(sch, store, notBefore, true)
			}
			return err
		}
	}
	return m.arm(sch, store, notBefore, true)
}

func (m *scheduledRunManager) timerKey(store *scheduleStore, id string) string {
	return store.Root + "\x00" + id
}

func (m *scheduledRunManager) clearTimer(store *scheduleStore, id string) {
	key := m.timerKey(store, id)
	if t, ok := m.timers[key]; ok {
		m.deps.Timers.ClearTimeout(t)
		delete(m.timers, key)
	}
}

// arm is arm: the timer of a schedule, at its next run (not before notBefore).
func (m *scheduledRunManager) arm(sch *scheduleRecord, store *scheduleStore, notBefore int64, _ bool) error {
	m.clearTimer(store, sch.ID)
	if sch.Paused {
		return nil
	}
	next, ok, err := nextRunAtOf(sch)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := m.now()
	delay := next - now
	if d := notBefore - now; d > delay {
		delay = d
	}
	if delay < 0 {
		delay = 0
	}
	if delay > scheduleMaxTimerDelayMs {
		delay = scheduleMaxTimerDelayMs
	}
	id := sch.ID
	m.timers[m.timerKey(store, id)] = m.deps.Timers.SetTimeout(func() { m.fireTimer(store, id) }, delay)
	return nil
}

// fireTimer runs a timer callback: it is outside every caller's error handling, so whatever fails is contained to this schedule.
func (m *scheduledRunManager) fireTimer(store *scheduleStore, id string) {
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		return m.fire(store, id)
	}()
	if err != nil {
		scheduleWarn(fmt.Sprintf("[pi-subagents] Scheduled run '%s' failed to fire: %s", id, err.Error()))
		m.restoreAfterFireError(store, id)
	}
}

func (m *scheduledRunManager) restoreAfterFireError(store *scheduleStore, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := func() error {
		sch, err := store.find(id)
		if err != nil || sch == nil {
			return err
		}
		now := m.now()
		var notBefore int64
		if sch.Trigger.Kind == "interval" {
			planned, ok, perr := duePlannedAt(sch, now)
			if perr != nil {
				return perr
			}
			if ok && planned <= now {
				t, _ := time.Parse(time.RFC3339Nano, scheduleNext(sch.Trigger, planned, now))
				notBefore = t.UnixMilli()
			}
		}
		return m.restoreOne(store, sch, notBefore, sch.Trigger.Kind == "interval")
	}()
	if err != nil {
		scheduleWarn(fmt.Sprintf("[pi-subagents] Scheduled run '%s' could not be restored after fire failure: %s", id, err.Error()))
	}
}

func (m *scheduledRunManager) fire(store *scheduleStore, id string) error {
	m.mu.Lock()
	m.clearTimer(store, id)
	sch, err := store.find(id)
	if err != nil || sch == nil {
		m.mu.Unlock()
		return err
	}
	ctx, err := m.requireContext(store)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if !scheduleBelongsToSession(sch, ctx) {
		m.mu.Unlock()
		return nil
	}
	planned, ok, err := duePlannedAt(sch, m.now())
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if !ok || sch.Paused {
		m.mu.Unlock()
		return nil
	}
	if planned > m.now() {
		err := m.arm(sch, store, 0, false)
		m.mu.Unlock()
		return err
	}
	_, err = m.launch(store, sch, planned, "timer", true, false)
	m.mu.Unlock()
	return err
}

// launch is launch: one run of a schedule. m.mu is held; it is released while the launch itself runs.
func (m *scheduledRunManager) launch(store *scheduleStore, sch *scheduleRecord, planned int64, dueReason string, advance, quiet bool) (*scheduleRun, error) {
	now := m.now()
	nextBeforeClaim := sch.Trigger.NextRunAt
	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	run := &scheduleRun{ID: id, ScheduleID: sch.ID, PlannedAt: scheduleTimestamp(planned), DueReason: dueReason, State: "running", StartedAt: scheduleTimestamp(now)}
	skip := func() (*scheduleRun, error) {
		run.State = "skipped"
		run.CompletedAt = scheduleTimestamp(now)
		if advance {
			sch.Trigger.NextRunAt = scheduleNext(sch.Trigger, planned, now)
			sch.UpdatedAt = scheduleTimestamp(now)
			if err := store.write(sch); err != nil {
				return nil, err
			}
		}
		if err := store.writeRun(sch, run, "schedule.skipped_overlap"); err != nil {
			return nil, err
		}
		return run, m.arm(sch, store, 0, false)
	}
	if sch.ActiveRunID != "" {
		return skip()
	}
	dir, err := store.directory(sch.ID, true)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "active.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		return skip()
	}
	_, _ = f.WriteString(run.ID)
	_ = f.Close()
	sch.ActiveRunID = run.ID
	sch.LastRunID = run.ID
	if advance {
		sch.Trigger.NextRunAt = scheduleNext(sch.Trigger, planned, now)
	}
	sch.UpdatedAt = scheduleTimestamp(now)
	if err := store.write(sch); err != nil {
		return nil, err
	}
	if err := store.writeRun(sch, run, "schedule.run.started"); err != nil {
		return nil, err
	}
	ctx, err := m.requireContext(store)
	if err != nil {
		return nil, err
	}
	useQuiet := quiet
	if dueReason != "manual" {
		useQuiet = m.quietFlag(sch)
	}
	params := executionParamsOf(sch, useQuiet)
	m.mu.Unlock()
	res := scheduleLaunchResult{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				res = scheduleLaunchResult{IsError: true, Text: fmt.Sprint(r)}
			}
		}()
		res = m.deps.Launch(params, ctx)
	}()
	m.mu.Lock()
	if res.IsError || res.AsyncID == "" {
		text := res.Text
		if text == "" {
			text = "Scheduled launch failed."
		}
		run.State = "failed_launch"
		run.CompletedAt = scheduleTimestamp(m.now())
		run.Error = text
		latest, err := store.get(sch.ID)
		if err != nil {
			return nil, err
		}
		latest.ActiveRunID = ""
		if !advance && nextBeforeClaim != "" {
			latest.Trigger.NextRunAt = nextBeforeClaim
		}
		latest.UpdatedAt = scheduleTimestamp(m.now())
		if err := store.write(latest); err != nil {
			return nil, err
		}
		if err := store.writeRun(latest, run, "schedule.run.failed"); err != nil {
			return nil, err
		}
		_ = os.Remove(lockPath)
		return run, m.arm(latest, store, 0, false)
	}
	run.AsyncID, run.AsyncDir = res.AsyncID, res.AsyncDir
	m.observed[res.AsyncID] = true
	if err := store.writeRun(sch, run, "schedule.run.attached_async"); err != nil {
		return nil, err
	}
	return run, m.arm(sch, store, 0, false)
}

func (m *scheduledRunManager) finishRun(store *scheduleStore, sch *scheduleRecord, run *scheduleRun, success bool, errText string) error {
	now := m.now()
	next, ok, err := nextRunAtOf(sch)
	if err != nil {
		return err
	}
	if ok && next <= now {
		planned, _, _ := duePlannedAt(sch, now)
		id, err := m.newID()
		if err != nil {
			return err
		}
		skipped := &scheduleRun{ID: id, ScheduleID: sch.ID, PlannedAt: scheduleTimestamp(planned), DueReason: "timer", State: "skipped", CompletedAt: scheduleTimestamp(now)}
		sch.Trigger.NextRunAt = scheduleNext(sch.Trigger, planned, now)
		if err := store.writeRun(sch, skipped, "schedule.skipped_overlap"); err != nil {
			return err
		}
	}
	if run.AsyncID != "" {
		delete(m.observed, run.AsyncID)
	}
	if success {
		run.State = "completed"
	} else {
		run.State = "failed_run"
		if errText != "" {
			run.Error = errText
		}
	}
	run.CompletedAt = scheduleTimestamp(now)
	sch.ActiveRunID = ""
	sch.UpdatedAt = scheduleTimestamp(now)
	if err := store.write(sch); err != nil {
		return err
	}
	if dir, err := store.directory(sch.ID, false); err == nil {
		_ = os.Remove(filepath.Join(dir, "active.lock"))
	}
	ev := "schedule.run.failed"
	if success {
		ev = "schedule.run.completed"
	}
	if err := store.writeRun(sch, run, ev); err != nil {
		return err
	}
	return m.arm(sch, store, 0, false)
}

func (m *scheduledRunManager) recordMissed(store *scheduleStore, sch *scheduleRecord, planned int64, dueReason string) (*scheduleRun, error) {
	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	run := &scheduleRun{ID: id, ScheduleID: sch.ID, PlannedAt: scheduleTimestamp(planned), DueReason: dueReason, State: "missed", CompletedAt: scheduleTimestamp(m.now())}
	sch.Trigger.NextRunAt = scheduleNext(sch.Trigger, planned, m.now())
	sch.UpdatedAt = scheduleTimestamp(m.now())
	if err := store.write(sch); err != nil {
		return nil, err
	}
	return run, store.writeRun(sch, run, "schedule.missed")
}

// selectProject is selectProject: the schedule store of a project, with the context of the session that opened it.
func (m *scheduledRunManager) selectProject(cwd string, ctx scheduleCtx) error {
	projectCwd, _ := filepath.Abs(cwd)
	root := scheduledRunStorePath(projectCwd, "", m.deps.StoreRoot)
	ctxCwd, _ := filepath.Abs(ctx.Cwd)
	isBound := ctxCwd == projectCwd
	prev, hadPrev := m.contexts[root]
	changed := isBound && hadPrev && normalizedSessionFile(prev.SessionFile) != normalizedSessionFile(ctx.SessionFile)
	if isBound {
		snap := ctx
		snap.Cwd = projectCwd
		m.contexts[root] = snap
	} else if _, ok := m.contexts[root]; !ok {
		return fmt.Errorf("Cannot use project '%s' until that project has been opened in this runtime.", projectCwd)
	}
	store := m.stores[root]
	if store == nil {
		store = &scheduleStore{Root: root}
		if m.deps.StoreRoot == "" {
			store.ProjectCwd = projectCwd
		}
		m.stores[root] = store
		m.store = store
		if err := m.restore(store); err != nil {
			return err
		}
	} else if changed {
		m.store = store
		if err := m.restore(store); err != nil {
			return err
		}
	}
	m.store = store
	return nil
}
