package pi_subagents

// The session's background state and the completions bg_wait reads from it. Ports the SubagentState fields of the background
// runs (types.ts), wait-completions.ts and api/background-work.ts (the process-local provider registry).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ForegroundChildRecord is a foreground child remembered after it detached from its parent.
type ForegroundChildRecord struct {
	Index           int
	Agent           string
	Status          string // detached, completed, failed, ...
	ActivityState   string
	CurrentTool     string
	CurrentToolArgs string
	TranscriptPath  string
}

// ForegroundRunRecord is a foreground run remembered for bg_wait and resume.
type ForegroundRunRecord struct {
	RunID     string
	SessionID string
	Children  []ForegroundChildRecord
}

// completedResult is a terminal payload the result watcher observed, kept until its TTL ends.
type completedResult struct {
	SessionID  string
	SeenAt     int64
	Completion WaitCompletion
}

// completedStore is the session's record of consumed completions (a Map in the original; an interface so a test can race it).
type completedStore interface {
	Get(runID string) (completedResult, bool)
	Set(runID string, v completedResult)
	Prune(now, ttlMs int64)
}

// mapCompletedStore is safe for concurrent use: the result watcher records completions while bg_wait reads them.
type mapCompletedStore struct {
	mu sync.Mutex
	m  map[string]completedResult
}

func newCompletedStore() completedStore { return &mapCompletedStore{m: map[string]completedResult{}} }

func (s *mapCompletedStore) Get(id string) (completedResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	return v, ok
}
func (s *mapCompletedStore) Set(id string, v completedResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = v
}
func (s *mapCompletedStore) Prune(now, ttl int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.m {
		if now-e.SeenAt > ttl {
			delete(s.m, k)
		}
	}
}

// SubagentState is the extension's per-session state for background runs.
type SubagentState struct {
	Mu               sync.Mutex
	BaseCwd          string
	CurrentSessionID string
	AsyncJobs        map[string]*AsyncJobState
	// FleetJobs are the jobs kept after the widget drops them: every job seen, with at most maxRecentFleetJobs terminal ones.
	FleetJobs        map[string]*AsyncJobState
	CompletedResults completedStore
	CompletionSeen   map[string]int64
	ForegroundRuns   map[string]*ForegroundRunRecord
	WidgetsSuspended bool
}

func newSubagentState() *SubagentState {
	return &SubagentState{AsyncJobs: map[string]*AsyncJobState{}, FleetJobs: map[string]*AsyncJobState{}, CompletionSeen: map[string]int64{}}
}

const maxRecentFleetJobs = 20

// rememberFleetJob is async-job-tracker.ts rememberFleetJob: the job joins the retained set, whose terminal members are
// limited to the most recent maxRecentFleetJobs.
func (s *SubagentState) rememberFleetJob(j *AsyncJobState) {
	if s.FleetJobs == nil {
		s.FleetJobs = map[string]*AsyncJobState{}
	}
	s.FleetJobs[j.AsyncID] = j
	var terminal []*AsyncJobState
	for _, id := range sortedKeys(s.FleetJobs) {
		if c := s.FleetJobs[id]; isTerminalAsyncState(c.Status) {
			terminal = append(terminal, c)
		}
	}
	sort.SliceStable(terminal, func(a, b int) bool { return terminal[a].UpdatedAt > terminal[b].UpdatedAt })
	for i := maxRecentFleetJobs; i < len(terminal); i++ {
		delete(s.FleetJobs, terminal[i].AsyncID)
	}
}

// AsyncJobState is a run the session tracks (the original's AsyncJobState, for single-child runs).
type AsyncJobState struct {
	AsyncID           string
	AsyncDir          string
	ToolCallID        string
	Cwd               string
	SessionRoot       string
	Status            string
	Description       string
	PID               int
	SessionID         string
	CompletionOwnerID string
	ActivityState     string
	LastActivityAt    *ms
	CurrentTool       string
	CurrentToolAt     *ms
	CurrentPath       string
	TurnCount         *int
	ToolCount         *int
	Mode              string
	Agents            []string
	CurrentStep       *int
	Steps             []AsyncRunStepSummary
	StartedAt         int64
	UpdatedAt         int64
	TimeoutMs         *ms
	DeadlineAt        *ms
	SessionFile       string
	OutputFile        string
	SessionDir        string
	TotalTokens       *TokenUsage
}

// ── wait-completions.ts ──

func waitResultPayloadCandidate(resultsDir string, run AsyncRunSummary) (string, error) {
	publicPath := resultFilePath(resultsDir, run.ID)
	if run.SessionID == "" {
		return publicPath, nil
	}
	p, err := resultPayloadPathForSessionRun(resultsDir, run.SessionID, run.ID)
	if err != nil {
		if !isAccessDenied(err) {
			return "", err
		}
		pending, perr := fallbackResultPayloadPathForSessionRun(resultsDir, run.SessionID, run.ID)
		if perr != nil {
			return "", perr
		}
		if pending != "" {
			return pending, nil
		}
		return publicPath, nil
	}
	if p != "" {
		return p, nil
	}
	return publicPath, nil
}

// readWaitResultPayload reads the payload at candidatePath; nil when it is not this session's run (a run without a session owns nothing).
func readWaitResultPayload(candidatePath string, run AsyncRunSummary) (map[string]any, error) {
	if run.SessionID == "" {
		return nil, nil
	}
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, errors.New(jsParseErrorText(data, err))
	}
	if !resultPayloadMatchesSessionRun(payload, run.SessionID, run.ID) {
		return nil, nil
	}
	return payload.(map[string]any), nil
}

// recordWaitCompletion records a consumed terminal payload for bg_wait, pruning stale entries with the notification TTL.
// Payload ownership must be explicit and agree with persistence ownership. It reports whether the replay is durable.
func recordWaitCompletion(state *SubagentState, runID string, data map[string]any, now, ttlMs int64, persistence *struct{ ResultsDir, SessionID string }) bool {
	sessionID := nonEmptyString(data["sessionId"])
	if sessionID == "" || !resultPayloadMatchesSessionRun(data, sessionID, runID) {
		return false
	}
	if persistence != nil && persistence.SessionID != sessionID {
		return false
	}
	if state.CompletedResults == nil {
		state.CompletedResults = newCompletedStore()
	}
	state.CompletedResults.Prune(now, ttlMs)
	completion, err := toWaitCompletion(data, runID)
	if err != nil {
		return false
	}
	if persistence != nil {
		if rec, werr := writeCompletionReplay(persistence.ResultsDir, runID, sessionID, completion, data, now, ttlMs); werr == nil {
			completion = rec.Completion
		}
	}
	state.CompletedResults.Set(runID, completedResult{SessionID: sessionID, SeenAt: now, Completion: completion})
	return completion.ArchivePath != ""
}

// collectWaitCompletions: the terminal payloads of the runs a wait covered: the watcher's in-memory record first, then the
// not-yet-consumed result file. The read is read-only; the watcher owns notification and cleanup.
func collectWaitCompletions(terminal []AsyncRunSummary, state *SubagentState, resultsDir string, onReference func(string)) ([]WaitCompletion, error) {
	if len(terminal) == 0 {
		return nil, nil
	}
	var completions []WaitCompletion
	add := func(c WaitCompletion, resultPath string) {
		completions = append(completions, c)
		// tool details are not model context: publish the evidence address in content, never the output text
		ref := c.ArchivePath
		if ref == "" {
			ref = resultPath
		}
		if ref != "" && onReference != nil {
			onReference(fmt.Sprintf("Result [%s]: %s", c.RunID, ref))
		}
	}
	replay := func(run AsyncRunSummary) (*CompletionReplayRecord, error) {
		return readCompletionReplay(resultsDir, run.ID, run.SessionID, 0)
	}
	recorded := func(id string) (completedResult, bool) {
		if state == nil || state.CompletedResults == nil {
			return completedResult{}, false
		}
		return state.CompletedResults.Get(id)
	}
	for _, run := range terminal {
		if rec, ok := recorded(run.ID); ok && rec.SessionID == run.SessionID {
			if rec.Completion.ArchivePath != "" {
				add(rec.Completion, "")
				continue
			}
			candidate, cerr := waitResultPayloadCandidate(resultsDir, run)
			if cerr == nil {
				payload, perr := readWaitResultPayload(candidate, run)
				switch {
				case perr == nil && payload == nil:
					r, rerr := replay(run)
					if rerr != nil {
						return nil, rerr
					}
					if r != nil {
						add(r.Completion, "")
					} else {
						add(rec.Completion, "")
					}
					continue
				case perr == nil:
					add(rec.Completion, candidate)
					continue
				case !isNotExist(perr):
					return nil, perr
				}
			} else if !isNotExist(cerr) {
				return nil, cerr
			}
			r, rerr := replay(run)
			if rerr != nil {
				return nil, rerr
			}
			if r != nil {
				add(r.Completion, "")
			} else {
				add(rec.Completion, "")
			}
			continue
		}
		candidate, err := waitResultPayloadCandidate(resultsDir, run)
		if err != nil {
			return nil, fmt.Errorf("Failed to read subagent result '%s': %s", resultFilePath(resultsDir, run.ID), err.Error())
		}
		payload, err := readWaitResultPayload(candidate, run)
		if err == nil {
			if payload == nil {
				if r, rerr := replay(run); rerr == nil && r != nil {
					add(r.Completion, "")
				}
				continue
			}
			c, cerr := toWaitCompletion(payload, run.ID)
			if cerr != nil {
				return nil, cerr
			}
			add(c, candidate)
			continue
		}
		if !isNotExist(err) {
			return nil, fmt.Errorf("Failed to read subagent result '%s': %s", candidate, err.Error())
		}
		// the watcher may have consumed the file between the store check and the read: its in-memory record, then the replay
		if late, ok := recorded(run.ID); ok && late.SessionID == run.SessionID {
			add(late.Completion, "")
			continue
		}
		r, rerr := replay(run)
		if rerr != nil {
			return nil, fmt.Errorf("Failed to read completion replay for '%s': %s", run.ID, rerr.Error())
		}
		if r != nil {
			add(r.Completion, "")
		}
	}
	return completions, nil
}

// ── api/background-work.ts ──

const (
	backgroundWorkProtocolVersion = 1
	maxProviderNameLength         = 128
	maxProviders                  = 100
	maxItemIDLength               = 256
	maxBackgroundSessionIDLength  = 4096
	maxWakeChannelLength          = 256
	maxItemsPerProvider           = 10_000
)

// BackgroundWorkItem is one active unit of provider work.
type BackgroundWorkItem struct {
	ID        string
	SessionID string
	// extra names the unknown fields a provider's item carried (a JavaScript provider can add any; a Go one cannot, a test can).
	Extra []string
}

// RegisteredBackgroundWorkItem is a work item with its provider.
type RegisteredBackgroundWorkItem struct {
	Provider  string
	ID        string
	SessionID string
}

// BackgroundWorkSnapshot is the active provider work of one session.
type BackgroundWorkSnapshot struct {
	Providers []string
	Items     []RegisteredBackgroundWorkItem
}

// BackgroundWorkContext is what a provider is asked with.
type BackgroundWorkContext struct {
	SessionID string
	NowMs     int64
}

// BackgroundWorkProvider is a source of background work that has no native completion notification.
type BackgroundWorkProvider struct {
	Name           string
	ListActiveWork func(ctx BackgroundWorkContext) ([]BackgroundWorkItem, error)
	WakeChannels   []string
	Reconcile      func(ctx BackgroundWorkContext) error
	// unknownFields lets a test model a provider object with fields the protocol does not know.
	UnknownFields []string
}

var (
	bgWorkMu        sync.Mutex
	bgWorkProviders = map[string]*BackgroundWorkProvider{}
	bgWorkOrder     []string
)

func validateBGString(value any, field string, maxLength int) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" || jsTrim(s) != s {
		return "", fmt.Errorf("%s must be a non-empty string without leading or trailing whitespace.", field)
	}
	if len(jsUTF16(s)) > maxLength {
		return "", fmt.Errorf("%s must be at most %d characters.", field, maxLength)
	}
	if strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("%s must not contain NUL characters.", field)
	}
	return s, nil
}

func validateBGProvider(p *BackgroundWorkProvider) error {
	if p == nil {
		return errors.New("Background-work provider must be an object.")
	}
	if len(p.UnknownFields) > 0 {
		return fmt.Errorf("Background-work provider has unknown fields: %s.", strings.Join(p.UnknownFields, ", "))
	}
	name, err := validateBGString(p.Name, "Background-work provider name", maxProviderNameLength)
	if err != nil {
		return err
	}
	if p.ListActiveWork == nil {
		return fmt.Errorf("Background-work provider '%s' must expose listActiveWork().", name)
	}
	seen := map[string]bool{}
	for i, c := range p.WakeChannels {
		if _, err := validateBGString(c, fmt.Sprintf("Background-work provider '%s' wakeChannels[%d]", name, i), maxWakeChannelLength); err != nil {
			return err
		}
		if seen[c] {
			return fmt.Errorf("Background-work provider '%s' wakeChannels must not contain duplicates.", name)
		}
		seen[c] = true
	}
	return nil
}

func validateBGItem(provider string, item BackgroundWorkItem, index int) error {
	if len(item.Extra) > 0 {
		return fmt.Errorf("Background-work provider '%s' item %d has unknown fields: %s.", provider, index, strings.Join(item.Extra, ", "))
	}
	if _, err := validateBGString(item.ID, fmt.Sprintf("Background-work provider '%s' item %d id", provider, index), maxItemIDLength); err != nil {
		return err
	}
	_, err := validateBGString(item.SessionID, fmt.Sprintf("Background-work provider '%s' item %d sessionId", provider, index), maxBackgroundSessionIDLength)
	return err
}

// registerBackgroundWorkProvider registers or replaces a process-local provider. The disposer removes only this exact
// registration, so an old extension reload cannot unregister its replacement.
func registerBackgroundWorkProvider(p *BackgroundWorkProvider) (func(), error) {
	if err := validateBGProvider(p); err != nil {
		return nil, err
	}
	bgWorkMu.Lock()
	defer bgWorkMu.Unlock()
	if _, has := bgWorkProviders[p.Name]; !has {
		if len(bgWorkProviders) >= maxProviders {
			return nil, fmt.Errorf("Background-work registry supports at most %d providers.", maxProviders)
		}
		bgWorkOrder = append(bgWorkOrder, p.Name)
	}
	bgWorkProviders[p.Name] = p
	return func() {
		bgWorkMu.Lock()
		defer bgWorkMu.Unlock()
		if bgWorkProviders[p.Name] == p {
			delete(bgWorkProviders, p.Name)
			for i, n := range bgWorkOrder {
				if n == p.Name {
					bgWorkOrder = append(bgWorkOrder[:i], bgWorkOrder[i+1:]...)
					break
				}
			}
		}
	}, nil
}

func clearBackgroundWorkRegistry() {
	bgWorkMu.Lock()
	defer bgWorkMu.Unlock()
	bgWorkProviders = map[string]*BackgroundWorkProvider{}
	bgWorkOrder = nil
}

func listBackgroundWorkProviders() ([]*BackgroundWorkProvider, error) {
	bgWorkMu.Lock()
	defer bgWorkMu.Unlock()
	var out []*BackgroundWorkProvider
	for _, n := range bgWorkOrder {
		p := bgWorkProviders[n]
		if err := validateBGProvider(p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// listBackgroundWorkWakeChannels reads validated provider wake channels without reconciling or listing work.
func listBackgroundWorkWakeChannels() ([]string, error) {
	providers, err := listBackgroundWorkProviders()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range providers {
		for _, c := range p.WakeChannels {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// snapshotBackgroundWork reconciles and snapshots the active provider work owned by one exact session.
func snapshotBackgroundWork(sessionID string, nowMs int64) (BackgroundWorkSnapshot, error) {
	if _, err := validateBGString(sessionID, "Background-work snapshot sessionId", maxBackgroundSessionIDLength); err != nil {
		return BackgroundWorkSnapshot{}, err
	}
	providers, err := listBackgroundWorkProviders()
	if err != nil {
		return BackgroundWorkSnapshot{}, err
	}
	ctx := BackgroundWorkContext{SessionID: sessionID, NowMs: nowMs}
	snap := BackgroundWorkSnapshot{Providers: []string{}, Items: []RegisteredBackgroundWorkItem{}}
	identities := map[string]bool{}
	for _, p := range providers {
		if p.Reconcile != nil {
			if err := p.Reconcile(ctx); err != nil {
				return snap, fmt.Errorf("Background-work provider '%s' reconcile failed: %s", p.Name, err.Error())
			}
		}
		active, err := p.ListActiveWork(ctx)
		if err != nil {
			return snap, fmt.Errorf("Background-work provider '%s' listActiveWork failed: %s", p.Name, err.Error())
		}
		if len(active) > maxItemsPerProvider {
			return snap, fmt.Errorf("Background-work provider '%s' returned %d items; maximum is %d.", p.Name, len(active), maxItemsPerProvider)
		}
		for i, item := range active {
			if err := validateBGItem(p.Name, item, i); err != nil {
				return snap, err
			}
			id := p.Name + "\x00" + item.SessionID + "\x00" + item.ID
			if identities[id] {
				return snap, fmt.Errorf("Background-work provider '%s' returned duplicate item '%s' for session '%s'.", p.Name, item.ID, item.SessionID)
			}
			identities[id] = true
			if item.SessionID == sessionID {
				snap.Items = append(snap.Items, RegisteredBackgroundWorkItem{Provider: p.Name, ID: item.ID, SessionID: item.SessionID})
			}
		}
		snap.Providers = append(snap.Providers, p.Name)
	}
	return snap, nil
}

var _ = filepath.Join
