package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// CronSchedule represents a parsed cron, interval, or one-shot schedule compatible
// with Hermes's ~/.hermes/cron/jobs.json format.
type CronSchedule struct {
	Kind    string `json:"kind"` // "cron", "interval", "once"
	Expr    string `json:"expr,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
	RunAt   string `json:"run_at,omitempty"`
	Display string `json:"display,omitempty"`

	extra map[string]json.RawMessage
}

type cronScheduleAlias CronSchedule

var cronScheduleKnownKeys = map[string]bool{
	"kind":    true,
	"expr":    true,
	"minutes": true,
	"run_at":  true,
	"display": true,
}

// UnmarshalJSON preserves any unknown fields on CronSchedule.
func (s *CronSchedule) UnmarshalJSON(data []byte) error {
	var alias cronScheduleAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = CronSchedule(alias)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		for k, v := range raw {
			if !cronScheduleKnownKeys[k] {
				if s.extra == nil {
					s.extra = make(map[string]json.RawMessage)
				}
				s.extra[k] = v
			}
		}
	}
	return nil
}

// MarshalJSON writes known fields along with any preserved unknown fields.
func (s CronSchedule) MarshalJSON() ([]byte, error) {
	if len(s.extra) == 0 {
		return json.Marshal(cronScheduleAlias(s))
	}
	base, err := json.Marshal(cronScheduleAlias(s))
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range s.extra {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// CronJob is one persisted cron job record in jobs.json.
type CronJob struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Prompt          string       `json:"prompt"`
	Schedule        CronSchedule `json:"schedule"`
	ScheduleDisplay string       `json:"schedule_display"`
	Skills          []string     `json:"skills,omitempty"`
	Deliver         string       `json:"deliver,omitempty"`
	Enabled         bool         `json:"enabled"`
	State           string       `json:"state"` // "scheduled", "paused", "completed", "error"
	CreatedAt       string       `json:"created_at"`
	NextRunAt       string       `json:"next_run_at,omitempty"`
	LastRunAt       string       `json:"last_run_at,omitempty"`
	LastStatus      string       `json:"last_status,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
	PausedAt        string       `json:"paused_at,omitempty"`

	extra map[string]json.RawMessage
}

type cronJobAlias CronJob

var cronJobKnownKeys = map[string]bool{
	"id":               true,
	"name":             true,
	"prompt":           true,
	"schedule":         true,
	"schedule_display": true,
	"skills":           true,
	"deliver":          true,
	"enabled":          true,
	"state":            true,
	"created_at":       true,
	"next_run_at":      true,
	"last_run_at":      true,
	"last_status":      true,
	"last_error":       true,
	"paused_at":        true,
}

// UnmarshalJSON preserves any unknown fields on CronJob so shared Hermes jobs.json
// entries (e.g. origin, repeat, model, agent_id) round-trip without data loss.
func (j *CronJob) UnmarshalJSON(data []byte) error {
	var alias cronJobAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*j = CronJob(alias)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		for k, v := range raw {
			if !cronJobKnownKeys[k] {
				if j.extra == nil {
					j.extra = make(map[string]json.RawMessage)
				}
				j.extra[k] = v
			}
		}
	}
	return nil
}

// MarshalJSON writes known fields along with any preserved unknown fields.
func (j CronJob) MarshalJSON() ([]byte, error) {
	if len(j.extra) == 0 {
		return json.Marshal(cronJobAlias(j))
	}
	base, err := json.Marshal(cronJobAlias(j))
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range j.extra {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// CreateJobRequest specifies parameters for creating a new cron job.
type CreateJobRequest struct {
	Name     string
	Schedule string
	Prompt   string
	Skills   []string
	Deliver  string
}

type cronJobsFileState struct {
	jobs      []CronJob
	topExtras map[string]json.RawMessage
}

// CronStore manages atomic JSON persistence of cron jobs in jobs.json.
type CronStore struct {
	mu   sync.Mutex
	file string
	now  func() time.Time
}

// NewCronStore creates a CronStore backed by jobsFile (e.g. ~/.hermes/cron/jobs.json).
func NewCronStore(jobsFile string) *CronStore {
	return &CronStore{
		file: jobsFile,
		now:  time.Now,
	}
}

func (s *CronStore) loadLocked() (cronJobsFileState, error) {
	if s.file == "" {
		return cronJobsFileState{}, errors.New("cron jobs file path is empty")
	}
	raw, err := os.ReadFile(s.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cronJobsFileState{jobs: []CronJob{}}, nil
		}
		return cronJobsFileState{}, fmt.Errorf("read cron jobs: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return cronJobsFileState{jobs: []CronJob{}}, nil
	}
	// Strip UTF-8 BOM if present.
	trimmed = strings.TrimPrefix(trimmed, "\uFEFF")

	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &top); err == nil && top != nil {
		if rawJobs, ok := top["jobs"]; ok {
			var jobs []CronJob
			if err := json.Unmarshal(rawJobs, &jobs); err == nil {
				if jobs == nil {
					jobs = []CronJob{}
				}
				var extras map[string]json.RawMessage
				for k, v := range top {
					if k == "jobs" {
						continue
					}
					if extras == nil {
						extras = make(map[string]json.RawMessage)
					}
					extras[k] = v
				}
				return cronJobsFileState{jobs: jobs, topExtras: extras}, nil
			}
		}
	}
	// Accept bare JSON array for compatibility with upstream auto-repair.
	var bare []CronJob
	if err := json.Unmarshal([]byte(trimmed), &bare); err == nil {
		return cronJobsFileState{jobs: bare}, nil
	}
	return cronJobsFileState{}, fmt.Errorf("cron jobs file %s is corrupted", s.file)
}

func (s *CronStore) saveLocked(state cronJobsFileState) error {
	if s.file == "" {
		return errors.New("cron jobs file path is empty")
	}
	dir := filepath.Dir(s.file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create cron directory: %w", err)
	}
	jobs := state.jobs
	if jobs == nil {
		jobs = []CronJob{}
	}
	jobsRaw, err := json.Marshal(jobs)
	if err != nil {
		return fmt.Errorf("marshal cron jobs: %w", err)
	}
	out := make(map[string]json.RawMessage, len(state.topExtras)+1)
	for k, v := range state.topExtras {
		out[k] = v
	}
	out["jobs"] = jobsRaw
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cron jobs: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".jobs-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp cron file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp cron file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp cron file: %w", err)
	}
	_ = os.Chmod(tmpPath, 0o600)

	if err := os.Rename(tmpPath, s.file); err != nil {
		return fmt.Errorf("atomic rename cron jobs file: %w", err)
	}
	cleanup = false
	return nil
}

// List returns all stored cron jobs.
func (s *CronStore) List(includeDisabled bool) ([]CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	jobs := state.jobs
	if includeDisabled {
		out := make([]CronJob, len(jobs))
		copy(out, jobs)
		return out, nil
	}
	out := make([]CronJob, 0, len(jobs))
	for _, j := range jobs {
		if j.Enabled {
			out = append(out, j)
		}
	}
	return out, nil
}

// Get finds a job by exact ID or case-insensitive name.
func (s *CronStore) Get(idOrName string) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	idx := findJobIndex(state.jobs, idOrName)
	if idx < 0 {
		return nil, fmt.Errorf("cron job %q not found", idOrName)
	}
	cp := state.jobs[idx]
	return &cp, nil
}

// Create validates the schedule, computes next_run_at, and persists a new job atomically.
func (s *CronStore) Create(req CreateJobRequest) (*CronJob, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required to create a cron job")
	}
	schedStr := strings.TrimSpace(req.Schedule)
	if schedStr == "" {
		return nil, errors.New("schedule is required to create a cron job")
	}

	now := s.now()
	sched, err := ParseSchedule(schedStr, now)
	if err != nil {
		return nil, err
	}
	nextRun, err := ComputeNextRun(sched, now)
	if err != nil {
		return nil, err
	}
	if nextRun == nil {
		return nil, fmt.Errorf("schedule %q has no future run time", schedStr)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultJobName(prompt)
	}
	deliver := strings.TrimSpace(req.Deliver)
	if deliver == "" {
		deliver = "local"
	}

	job := CronJob{
		ID:              generateCronID(),
		Name:            name,
		Prompt:          prompt,
		Schedule:        sched,
		ScheduleDisplay: sched.Display,
		Skills:          req.Skills,
		Deliver:         deliver,
		Enabled:         true,
		State:           "scheduled",
		CreatedAt:       now.Format(time.RFC3339),
		NextRunAt:       nextRun.Format(time.RFC3339),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	state.jobs = append(state.jobs, job)
	if err := s.saveLocked(state); err != nil {
		return nil, err
	}
	return &job, nil
}

// Pause disables a job and sets state="paused".
func (s *CronStore) Pause(idOrName string) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	idx := findJobIndex(state.jobs, idOrName)
	if idx < 0 {
		return nil, fmt.Errorf("cron job %q not found", idOrName)
	}
	state.jobs[idx].Enabled = false
	state.jobs[idx].State = "paused"
	state.jobs[idx].PausedAt = s.now().Format(time.RFC3339)
	if err := s.saveLocked(state); err != nil {
		return nil, err
	}
	cp := state.jobs[idx]
	return &cp, nil
}

// Resume re-enables a paused job and recomputes next_run_at.
func (s *CronStore) Resume(idOrName string) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	idx := findJobIndex(state.jobs, idOrName)
	if idx < 0 {
		return nil, fmt.Errorf("cron job %q not found", idOrName)
	}
	now := s.now()
	nextRun, err := ComputeNextRun(state.jobs[idx].Schedule, now)
	if err != nil {
		return nil, err
	}
	state.jobs[idx].Enabled = true
	state.jobs[idx].State = "scheduled"
	state.jobs[idx].PausedAt = ""
	if nextRun != nil {
		state.jobs[idx].NextRunAt = nextRun.Format(time.RFC3339)
	} else {
		state.jobs[idx].NextRunAt = ""
	}
	if err := s.saveLocked(state); err != nil {
		return nil, err
	}
	cp := state.jobs[idx]
	return &cp, nil
}

// Remove deletes a job by ID or name and returns the removed job.
func (s *CronStore) Remove(idOrName string) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	idx := findJobIndex(state.jobs, idOrName)
	if idx < 0 {
		return nil, fmt.Errorf("cron job %q not found", idOrName)
	}
	removed := state.jobs[idx]
	state.jobs = append(state.jobs[:idx], state.jobs[idx+1:]...)
	if err := s.saveLocked(state); err != nil {
		return nil, err
	}
	return &removed, nil
}

// MarkRun records the outcome of a manual or scheduled execution and advances next_run_at.
func (s *CronStore) MarkRun(idOrName, status, errMsg string, ranAt time.Time) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	idx := findJobIndex(state.jobs, idOrName)
	if idx < 0 {
		return nil, fmt.Errorf("cron job %q not found", idOrName)
	}
	state.jobs[idx].LastRunAt = ranAt.Format(time.RFC3339)
	state.jobs[idx].LastStatus = status
	state.jobs[idx].LastError = errMsg
	if status == "error" {
		state.jobs[idx].State = "error"
	} else if state.jobs[idx].Schedule.Kind == "once" {
		state.jobs[idx].State = "completed"
		state.jobs[idx].Enabled = false
		state.jobs[idx].NextRunAt = ""
	} else if state.jobs[idx].Enabled {
		state.jobs[idx].State = "scheduled"
		if nextRun, err := ComputeNextRun(state.jobs[idx].Schedule, ranAt); err == nil && nextRun != nil {
			state.jobs[idx].NextRunAt = nextRun.Format(time.RFC3339)
		}
	}
	if err := s.saveLocked(state); err != nil {
		return nil, err
	}
	cp := state.jobs[idx]
	return &cp, nil
}

func findJobIndex(jobs []CronJob, idOrName string) int {
	needle := strings.TrimSpace(idOrName)
	if needle == "" {
		return -1
	}
	for i, j := range jobs {
		if j.ID == needle {
			return i
		}
	}
	lower := strings.ToLower(needle)
	for i, j := range jobs {
		if strings.ToLower(j.Name) == lower {
			return i
		}
	}
	return -1
}

func generateCronID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(b[:])
}

func defaultJobName(prompt string) string {
	s := strings.Join(strings.Fields(prompt), " ")
	if len(s) > 48 {
		return s[:48]
	}
	return s
}

// --- Schedule Parser & 5-Field Cron Evaluator ---

var (
	durationRe  = regexp.MustCompile(`^(\d*)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days)$`)
	clockTimeRe = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm)?$`)
	isoDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

var cronShortcuts = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var weekdayToCronDOW = map[string]string{
	"sunday": "0", "sun": "0",
	"monday": "1", "mon": "1",
	"tuesday": "2", "tue": "2", "tues": "2",
	"wednesday": "3", "wed": "3", "weds": "3",
	"thursday": "4", "thu": "4", "thur": "4", "thurs": "4",
	"friday": "5", "fri": "5",
	"saturday": "6", "sat": "6",
}

var dayspecToCronDOW = map[string]string{
	"day": "0-6", "daily": "0-6", "everyday": "0-6",
	"weekday": "1-5", "weekdays": "1-5",
	"weekend": "0,6", "weekends": "0,6",
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// ParseSchedule parses a 5-field cron expression, interval ("30m", "2h", "every 15m"),
// relative one-shot ("in 30m"), natural day/time ("every monday 9am", "weekdays at 9am"),
// or ISO-8601/RFC3339 one-shot timestamp.
func ParseSchedule(schedule string, now time.Time) (CronSchedule, error) {
	orig := strings.TrimSpace(schedule)
	if orig == "" {
		return CronSchedule{}, errors.New("schedule cannot be empty")
	}
	lower := strings.ToLower(orig)

	// Cron shortcuts (@hourly, @daily, etc.)
	if expr, ok := cronShortcuts[lower]; ok {
		if _, err := parseCronExpr(expr); err != nil {
			return CronSchedule{}, err
		}
		return CronSchedule{Kind: "cron", Expr: expr, Display: orig}, nil
	}

	// Natural day/time phrases ("every monday 9am", "weekdays at 9am") or "every <duration>".
	isEvery := strings.HasPrefix(lower, "every ")
	rest := lower
	if isEvery {
		rest = strings.TrimSpace(orig[6:])
	}
	if expr, ok := naturalEveryToCron(rest); ok {
		if _, err := parseCronExpr(expr); err != nil {
			return CronSchedule{}, err
		}
		return CronSchedule{Kind: "cron", Expr: expr, Display: orig}, nil
	}
	if isEvery {
		mins, err := parseDurationMinutes(rest)
		if err != nil {
			return CronSchedule{}, err
		}
		return CronSchedule{Kind: "interval", Minutes: mins, Display: fmt.Sprintf("every %dm", mins)}, nil
	}

	// 5-field cron expression.
	fields := strings.Fields(orig)
	if len(fields) == 5 {
		if _, err := parseCronExpr(orig); err != nil {
			return CronSchedule{}, err
		}
		return CronSchedule{Kind: "cron", Expr: strings.Join(fields, " "), Display: orig}, nil
	}

	// ISO-8601 / RFC3339 one-shot timestamp.
	if strings.Contains(orig, "T") || isoDateRe.MatchString(orig) {
		ts, err := parseISO8601(orig, now.Location())
		if err != nil {
			return CronSchedule{}, fmt.Errorf("invalid timestamp %q: %w", orig, err)
		}
		return CronSchedule{
			Kind:    "once",
			RunAt:   ts.Format(time.RFC3339),
			Display: fmt.Sprintf("once at %s", ts.Format("2006-01-02 15:04")),
		}, nil
	}

	// Explicit one-shot relative duration ("in 30m", "in 2h").
	if strings.HasPrefix(lower, "in ") {
		durStr := strings.TrimSpace(orig[3:])
		mins, err := parseDurationMinutes(durStr)
		if err != nil {
			return CronSchedule{}, err
		}
		runAt := now.Add(time.Duration(mins) * time.Minute)
		return CronSchedule{
			Kind:    "once",
			RunAt:   runAt.Format(time.RFC3339),
			Display: fmt.Sprintf("once in %s", durStr),
		}, nil
	}

	// Bare recurring interval ("30m", "2h", "1d", "hour").
	if mins, err := parseDurationMinutes(orig); err == nil {
		return CronSchedule{
			Kind:    "interval",
			Minutes: mins,
			Display: fmt.Sprintf("every %dm", mins),
		}, nil
	}

	return CronSchedule{}, fmt.Errorf("invalid schedule %q: expected 5-field cron expression, interval (e.g. '30m', '2h'), or RFC3339 timestamp", orig)
}

func parseDurationMinutes(s string) (int, error) {
	m := durationRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q: use format like '30m', '2h', '1d'", s)
	}
	val := 1
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q: value must be a positive integer", s)
		}
		val = n
	}
	switch m[2][0] {
	case 'm':
		return val, nil
	case 'h':
		return val * 60, nil
	case 'd':
		return val * 1440, nil
	default:
		return 0, fmt.Errorf("invalid duration unit in %q", s)
	}
}

func parseClockTime(text string) (int, int, bool) {
	t := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(text)), " ", "")
	if t == "" {
		return 0, 0, false
	}
	if t == "noon" || t == "midday" {
		return 12, 0, true
	}
	if t == "midnight" {
		return 0, 0, true
	}
	m := clockTimeRe.FindStringSubmatch(t)
	if m == nil {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	minute := 0
	if m[2] != "" {
		minute, err = strconv.Atoi(m[2])
		if err != nil {
			return 0, 0, false
		}
	}
	meridiem := m[3]
	if meridiem != "" {
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		hour = hour % 12
		if meridiem == "pm" {
			hour += 12
		}
	}
	if hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

func naturalEveryToCron(rest string) (string, bool) {
	tokens := strings.Fields(strings.ReplaceAll(strings.ToLower(rest), ",", " "))
	if len(tokens) == 0 {
		return "", false
	}
	dow, ok := dayspecToCronDOW[tokens[0]]
	idx := 1
	if !ok {
		var days []string
		seen := map[string]bool{}
		idx = len(tokens)
		for i, tok := range tokens {
			if tok == "and" {
				continue
			}
			mapped, ok := weekdayToCronDOW[tok]
			if !ok {
				idx = i
				break
			}
			if !seen[mapped] {
				seen[mapped] = true
				days = append(days, mapped)
			}
		}
		if len(days) == 0 {
			return "", false
		}
		dow = strings.Join(days, ",")
	}
	if dow == "0-6" {
		dow = "*"
	}
	timeTokens := tokens[idx:]
	if len(timeTokens) > 0 && timeTokens[0] == "at" {
		timeTokens = timeTokens[1:]
	}
	if len(timeTokens) == 0 {
		return "", false
	}
	hour, minute, ok := parseClockTime(strings.Join(timeTokens, " "))
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d %d * * %s", minute, hour, dow), true
}

func parseISO8601(s string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognized ISO-8601 / RFC3339 timestamp format")
}

type cronExpr struct {
	minute      [60]bool
	hour        [24]bool
	dom         [32]bool // 1..31
	month       [13]bool // 1..12
	dow         [7]bool  // 0..6 (0=Sunday)
	domWildcard bool
	dowWildcard bool
}

func parseCronExpr(expr string) (*cronExpr, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("invalid cron expression %q: expected 5 fields (minute hour dom month dow), got %d", expr, len(fields))
	}
	c := &cronExpr{
		domWildcard: fields[2] == "*",
		dowWildcard: fields[4] == "*",
	}
	if err := parseCronField(fields[0], 0, 59, nil, c.minute[:]); err != nil {
		return nil, fmt.Errorf("invalid cron minute field %q: %w", fields[0], err)
	}
	if err := parseCronField(fields[1], 0, 23, nil, c.hour[:]); err != nil {
		return nil, fmt.Errorf("invalid cron hour field %q: %w", fields[1], err)
	}
	if err := parseCronField(fields[2], 1, 31, nil, c.dom[:]); err != nil {
		return nil, fmt.Errorf("invalid cron day-of-month field %q: %w", fields[2], err)
	}
	if err := parseCronField(fields[3], 1, 12, monthNames, c.month[:]); err != nil {
		return nil, fmt.Errorf("invalid cron month field %q: %w", fields[3], err)
	}
	var dowBits [8]bool
	if err := parseCronField(fields[4], 0, 7, dowNames, dowBits[:]); err != nil {
		return nil, fmt.Errorf("invalid cron day-of-week field %q: %w", fields[4], err)
	}
	for i := range 7 {
		c.dow[i] = dowBits[i]
	}
	if dowBits[7] {
		c.dow[0] = true
	}
	return c, nil
}

func parseCronField(field string, minVal, maxVal int, names map[string]int, bits []bool) error {
	if field == "" {
		return errors.New("empty field")
	}
	parts := strings.Split(field, ",")
	for _, part := range parts {
		if part == "" {
			return errors.New("empty list entry")
		}
		step := 1
		rangePart := part
		if strings.Contains(part, "/") {
			halves := strings.Split(part, "/")
			if len(halves) != 2 || halves[1] == "" {
				return fmt.Errorf("invalid step syntax %q", part)
			}
			s, err := strconv.Atoi(halves[1])
			if err != nil || s <= 0 {
				return fmt.Errorf("step must be a positive integer in %q", part)
			}
			step = s
			rangePart = halves[0]
		}

		var start, end int
		switch {
		case rangePart == "*":
			start, end = minVal, maxVal
		case strings.Contains(rangePart, "-"):
			bounds := strings.Split(rangePart, "-")
			if len(bounds) != 2 {
				return fmt.Errorf("invalid range %q", rangePart)
			}
			var err error
			start, err = parseCronAtom(bounds[0], minVal, maxVal, names)
			if err != nil {
				return err
			}
			end, err = parseCronAtom(bounds[1], minVal, maxVal, names)
			if err != nil {
				return err
			}
			if start > end {
				return fmt.Errorf("range start %d exceeds end %d in %q", start, end, rangePart)
			}
		default:
			v, err := parseCronAtom(rangePart, minVal, maxVal, names)
			if err != nil {
				return err
			}
			start = v
			if strings.Contains(part, "/") {
				end = maxVal
			} else {
				end = v
			}
		}

		for v := start; v <= end; v += step {
			bits[v] = true
		}
	}
	return nil
}

func parseCronAtom(atom string, minVal, maxVal int, names map[string]int) (int, error) {
	lower := strings.ToLower(strings.TrimSpace(atom))
	if names != nil {
		if v, ok := names[lower]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(lower)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", atom)
	}
	if v < minVal || v > maxVal {
		return 0, fmt.Errorf("value %d out of range [%d..%d]", v, minVal, maxVal)
	}
	return v, nil
}

func (c *cronExpr) matchesDay(t time.Time) bool {
	domMatch := c.dom[t.Day()]
	dowMatch := c.dow[int(t.Weekday())]
	if c.domWildcard && c.dowWildcard {
		return true
	}
	if c.domWildcard {
		return dowMatch
	}
	if c.dowWildcard {
		return domMatch
	}
	return domMatch || dowMatch
}

func (c *cronExpr) next(from time.Time) (time.Time, error) {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for !t.After(limit) {
		if !c.month[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.matchesDay(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t, nil
	}
	return time.Time{}, errors.New("no matching run time found within 5 years")
}

// ComputeNextRun calculates the next scheduled run time strictly after from.
func ComputeNextRun(sched CronSchedule, from time.Time) (*time.Time, error) {
	switch sched.Kind {
	case "once":
		if sched.RunAt == "" {
			return nil, errors.New("once schedule missing run_at")
		}
		ts, err := parseISO8601(sched.RunAt, from.Location())
		if err != nil {
			return nil, err
		}
		if !ts.After(from) {
			return nil, nil
		}
		return &ts, nil
	case "interval":
		if sched.Minutes <= 0 {
			return nil, errors.New("interval schedule minutes must be positive")
		}
		next := from.Add(time.Duration(sched.Minutes) * time.Minute)
		return &next, nil
	case "cron":
		parsed, err := parseCronExpr(sched.Expr)
		if err != nil {
			return nil, err
		}
		next, err := parsed.next(from)
		if err != nil {
			return nil, err
		}
		return &next, nil
	default:
		return nil, fmt.Errorf("unknown schedule kind %q", sched.Kind)
	}
}

// --- cronjob_manage Tool Registration ---

func registerCronTool(ext *sdk.Extension, store *CronStore) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "cronjob_manage",
		Label:         "Cron Job Manager",
		Description:   "Create, list, inspect, pause, resume, remove, or run scheduled cron jobs stored in ~/.hermes/cron/jobs.json.",
		PromptSnippet: "Manage scheduled cron jobs (create, list, get, pause, resume, remove, run_now) with cronjob_manage.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "list", "get", "pause", "resume", "remove", "run_now"},
					"description": "Operation to perform.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Human-readable name for the cron job (used with action='create').",
				},
				"schedule": map[string]any{
					"type":        "string",
					"description": "5-field cron expression (e.g. '0 9 * * *'), interval ('30m', '2h', 'every 1h'), natural phrase ('every monday 9am'), or ISO-8601/RFC3339 one-shot timestamp.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Prompt to execute when the job runs (required for action='create').",
				},
				"job_id": map[string]any{
					"type":        "string",
					"description": "Job ID or name (required for get, pause, resume, remove, run_now).",
				},
				"skills": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional skill names to associate with the job.",
				},
				"deliver": map[string]any{
					"type":        "string",
					"description": "Delivery target (default 'local').",
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeCronManage(ctx, store, params), nil
		},
	})
}

func executeCronManage(ctx sdk.Context, store *CronStore, params map[string]any) sdk.ToolResult {
	action := strings.ToLower(strings.TrimSpace(paramString(params, "action")))
	switch action {
	case "create":
		job, err := store.Create(CreateJobRequest{
			Name:     paramString(params, "name"),
			Schedule: paramString(params, "schedule"),
			Prompt:   paramString(params, "prompt"),
			Skills:   paramStringSlice(params, "skills"),
			Deliver:  paramString(params, "deliver"),
		})
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"job":     job,
		}, false)

	case "list":
		jobs, err := store.List(true)
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"jobs":    jobs,
			"count":   len(jobs),
		}, false)

	case "get":
		jobID := paramString(params, "job_id")
		job, err := store.Get(jobID)
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"job":     job,
		}, false)

	case "pause":
		jobID := paramString(params, "job_id")
		job, err := store.Pause(jobID)
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"job":     job,
		}, false)

	case "resume":
		jobID := paramString(params, "job_id")
		job, err := store.Resume(jobID)
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"job":     job,
		}, false)

	case "remove":
		jobID := paramString(params, "job_id")
		removed, err := store.Remove(jobID)
		if err != nil {
			return errorResult(err.Error())
		}
		return jsonResult(map[string]any{
			"success": true,
			"removed": removed,
		}, false)

	case "run_now", "run":
		jobID := paramString(params, "job_id")
		job, err := store.Get(jobID)
		if err != nil {
			return errorResult(err.Error())
		}
		return runCronJobNow(ctx, store, job)

	default:
		return errorResult(fmt.Sprintf("unknown cronjob_manage action %q (expected create, list, get, pause, resume, remove, or run_now)", action))
	}
}

func runCronJobNow(ctx sdk.Context, store *CronStore, job *CronJob) sdk.ToolResult {
	now := store.now()
	session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
		Cwd:          ctx.Cwd(),
		InMemory:     true,
		ExcludeTools: BlockedToolsList(),
	})
	if err != nil {
		updated, _ := store.MarkRun(job.ID, "error", err.Error(), now)
		return jsonResult(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to start cron job session: %v", err),
			"job":     updated,
		}, true)
	}
	defer session.Dispose()

	runCtx, cancel := contextFromDone(ctx.Done())
	defer cancel()

	var (
		evMu         sync.Mutex
		stopReason   string
		errorMessage string
		lastText     string
	)
	unsub := session.Subscribe(func(ev map[string]any) {
		evType, _ := ev["type"].(string)
		if evType != "message_end" {
			return
		}
		msg, ok := ev["message"].(map[string]any)
		if !ok {
			return
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			return
		}
		evMu.Lock()
		defer evMu.Unlock()
		if txt := extractAssistantText(msg); txt != "" {
			lastText = txt
		}
		if sr, _ := msg["stopReason"].(string); sr != "" {
			stopReason = sr
		}
		if em, _ := msg["errorMessage"].(string); em != "" {
			errorMessage = em
		}
	})
	defer unsub()

	promptText := job.Prompt
	if len(job.Skills) > 0 {
		promptText = fmt.Sprintf("Skills to apply: %s\n\n%s", strings.Join(job.Skills, ", "), promptText)
	}

	promptErr := session.Prompt(runCtx, promptText)

	evMu.Lock()
	finalStop := stopReason
	finalErrMsg := errorMessage
	fallbackText := lastText
	evMu.Unlock()

	if runErr, _ := classifyChildOutcome(promptErr, runCtx.Err(), finalStop, finalErrMsg); runErr != nil {
		updated, _ := store.MarkRun(job.ID, "error", runErr.Error(), now)
		return jsonResult(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("cron job execution failed: %v", runErr),
			"job":     updated,
		}, true)
	}

	summary := ""
	if st, err := session.State(); err == nil && st.LastAssistantText != nil {
		summary = *st.LastAssistantText
	}
	if summary == "" {
		summary = fallbackText
	}
	updated, _ := store.MarkRun(job.ID, "ok", "", now)
	return jsonResult(map[string]any{
		"success": true,
		"job":     updated,
		"output":  summary,
	}, false)
}

func contextFromDone(done <-chan struct{}) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(context.Background())
	if done == nil {
		return runCtx, cancel
	}
	go func() {
		select {
		case <-done:
			cancel()
		case <-runCtx.Done():
		}
	}()
	return runCtx, cancel
}
