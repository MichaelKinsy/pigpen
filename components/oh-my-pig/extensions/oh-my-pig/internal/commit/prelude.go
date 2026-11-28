package commit

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/commit/selective/prelude-definition.ts,
//           packages/coding-agent/src/sdk.ts:2648-2656, 2830-2843

//go:embed assets/commit.md
var commitDocumentation string

//go:embed assets/commit-guidance.md
var commitGuidance string

//go:embed assets/prelude.js
var commitJavascript string

//go:embed assets/prelude.py
var commitPython string

const (
	// SettingCommitEnabled matches omp's cfgCommitEnabled ("commit.enabled", default true).
	SettingCommitEnabled = "commit.enabled"
	// SettingOutputMaxColumns matches omp's tools.outputMaxColumns (default 768).
	SettingOutputMaxColumns = "tools.outputMaxColumns"
	defaultOutputMaxColumns = 768
	maxDraftsPerSession     = 8
	mainAgentID             = "Main"
)

// Setting reads a setting key for ctx (wired by wiring.go -> settings.Any).
var Setting func(ctx sdk.Context, key string) any

// AgentIdentity returns the journal agent identity (agent, parent) for ctx.
// Wired by wiring.go -> internal/agents. Defaults to ("Main", "").
var AgentIdentity func(ctx sdk.Context) JournalAgent

// IsAgentRunning reports whether agentID is currently running in ctx's session tree.
// Wired by wiring.go -> internal/agents.
var IsAgentRunning func(ctx sdk.Context, agentID string) bool

// ArtifactsDirResolver resolves the session tree's artifacts directory for ctx.
// Returns "" when the session keeps no artifacts (e.g. --no-session / in-memory).
// Wired by wiring.go; defaults to deriving from ctx.GetSessionFile() when ending in ".jsonl".
var ArtifactsDirResolver func(ctx sdk.Context) string

// DefaultArtifactsDir returns the artifacts directory for ctx: if ArtifactsDirResolver
// is installed it wins; otherwise a `.jsonl` session file maps to its sibling directory
// (matching session-manager.ts:184-187 artifactsDirectoryFor).
func DefaultArtifactsDir(ctx sdk.Context) string {
	if fn := ArtifactsDirResolver; fn != nil {
		return fn(ctx)
	}
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	sf, err := ctx.GetSessionFile()
	if err != nil || sf == nil || !strings.HasSuffix(*sf, ".jsonl") {
		return ""
	}
	return strings.TrimSuffix(*sf, ".jsonl")
}

// DefaultAgentIdentity returns the calling agent's journal identity for ctx.
func DefaultAgentIdentity(ctx sdk.Context) JournalAgent {
	if fn := AgentIdentity; fn != nil {
		id := fn(ctx)
		if id.Agent != "" {
			return id
		}
	}
	return JournalAgent{Agent: mainAgentID}
}

// JournalActive reports cheaply whether change journaling is active for ctx
// (`commit.enabled` is true and the session keeps an artifacts directory).
// Callers use this before doing any extra snapshot I/O.
func JournalActive(ctx sdk.Context) bool {
	if !commitEnabled(ctx) {
		return false
	}
	return DefaultArtifactsDir(ctx) != ""
}

// RecordFileChange appends a file change to ctx's session tree journal.
// Artifacts under the session's artifacts dir are never recorded (matching sdk.ts:2833).
// upstream: packages/coding-agent/src/sdk.ts:2830-2843
func RecordFileChange(ctx sdk.Context, tool, absPath string, before, after *string) {
	if !commitEnabled(ctx) {
		return
	}
	dir := DefaultArtifactsDir(ctx)
	if dir == "" {
		return
	}
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(absPath)
	if strings.HasPrefix(cleanPath, cleanDir+string(filepath.Separator)) || cleanPath == cleanDir {
		return
	}
	owner := DefaultAgentIdentity(ctx)
	JournalAt(dir).RecordChange(ChangeInput{
		Agent:  owner.Agent,
		Parent: owner.Parent,
		Tool:   tool,
		Path:   cleanPath,
		Before: before,
		After:  after,
	})
}

// RecordFileChangeForAgent records a file change attributed to an explicit agent identity
// (used by isolated subagent merges if enabled).
func RecordFileChangeForAgent(ctx sdk.Context, agent JournalAgent, tool, absPath string, before, after *string) {
	dir := DefaultArtifactsDir(ctx)
	if dir == "" {
		return
	}
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(absPath)
	if strings.HasPrefix(cleanPath, cleanDir+string(filepath.Separator)) || cleanPath == cleanDir {
		return
	}
	if agent.Agent == "" {
		agent = DefaultAgentIdentity(ctx)
	}
	JournalAt(dir).RecordChange(ChangeInput{
		Agent:  agent.Agent,
		Parent: agent.Parent,
		Tool:   tool,
		Path:   cleanPath,
		Before: before,
		After:  after,
	})
}

// DraftContext holds the shared inputs for DraftFor.
// upstream: prelude-definition.ts:48
type DraftContext struct {
	Cwd          string
	ArtifactsDir string
	Agent        string
	IsRunning    func(agent string) bool
	Ctx          context.Context
}

// DraftFor drafts the repository's changes for context.Agent.
// upstream: prelude-definition.ts:58
func DraftFor(dctx DraftContext, scope ScopeSpec) (*Draft, error) {
	var journal *ChangeJournal
	if dctx.ArtifactsDir != "" {
		journal = JournalAt(dctx.ArtifactsDir)
	}
	agent := dctx.Agent
	if agent == "" {
		agent = mainAgentID
	}
	return CreateDraft(DraftSource{
		Cwd:       dctx.Cwd,
		Journal:   journal,
		Caller:    agent,
		Agents:    scope,
		IsRunning: dctx.IsRunning,
		Ctx:       dctx.Ctx,
	})
}

type rememberedDraft struct {
	id    string
	draft *Draft
	scope ScopeSpec
}

type sessionDraftStore struct {
	mu       sync.Mutex
	sessions map[string][]rememberedDraft // oldest first, capped at maxDraftsPerSession
}

func newSessionDraftStore() *sessionDraftStore {
	return &sessionDraftStore{
		sessions: make(map[string][]rememberedDraft),
	}
}

func (s *sessionDraftStore) remember(sessionKey string, d *Draft, scope ScopeSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.sessions[sessionKey]
	// Replace if same id exists, else append.
	for i := range list {
		if list[i].id == d.ID {
			list[i] = rememberedDraft{id: d.ID, draft: d, scope: scope}
			s.sessions[sessionKey] = list
			return
		}
	}
	list = append(list, rememberedDraft{id: d.ID, draft: d, scope: scope})
	if len(list) > maxDraftsPerSession {
		list = list[len(list)-maxDraftsPerSession:]
	}
	s.sessions[sessionKey] = list
}

func (s *sessionDraftStore) lookup(sessionKey, id string) (*Draft, ScopeSpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.sessions[sessionKey] {
		if entry.id == id {
			return entry.draft, entry.scope, true
		}
	}
	return nil, ScopeSpec{}, false
}

func (s *sessionDraftStore) remove(sessionKey, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.sessions[sessionKey]
	for i := range list {
		if list[i].id == id {
			list = append(list[:i], list[i+1:]...)
			s.sessions[sessionKey] = list
			return
		}
	}
}

func (s *sessionDraftStore) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

var defaultDraftStore = newSessionDraftStore()

// Register installs the `commit` eval prelude and its lifecycle cleanup.
// Safe to call multiple times across sessions in one process; does no I/O.
func Register(e *sdk.Extension) {
	lifecycle.OnEnd("commit", func(sessionID string) {
		defaultDraftStore.endSession(sessionID)
	})
	eval.RegisterPrelude(PreludeDefinition(defaultDraftStore))
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
			lifecycle.End(strings.TrimSpace(id))
		}
		return nil, nil
	})
}

func commitEnabled(ctx sdk.Context) bool {
	if Setting != nil {
		if v, ok := Setting(ctx, SettingCommitEnabled).(bool); ok {
			return v
		}
	}
	var zero sdk.Context
	if ctx != zero {
		if cfg, err := ctx.GetSettings(); err == nil && cfg != nil {
			if v, ok := lookupBool(cfg, SettingCommitEnabled); ok {
				return v
			}
		}
	}
	return true
}

func outputMaxColumns(ctx sdk.Context) int {
	if Setting != nil {
		if n, ok := asIntNumber(Setting(ctx, SettingOutputMaxColumns)); ok && n >= 0 {
			return n
		}
	}
	var zero sdk.Context
	if ctx != zero {
		if cfg, err := ctx.GetSettings(); err == nil && cfg != nil {
			if n, ok := lookupNumber(cfg, SettingOutputMaxColumns); ok && n >= 0 {
				return n
			}
		}
	}
	return defaultOutputMaxColumns
}

func lookupBool(cfg map[string]any, dotted string) (bool, bool) {
	if v, ok := cfg[dotted]; ok {
		b, ok := v.(bool)
		return b, ok
	}
	parts := strings.Split(dotted, ".")
	var cur any = cfg
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		cur, ok = m[p]
		if !ok {
			return false, false
		}
	}
	b, ok := cur.(bool)
	return b, ok
}

func lookupNumber(cfg map[string]any, dotted string) (int, bool) {
	if v, ok := cfg[dotted]; ok {
		return asIntNumber(v)
	}
	parts := strings.Split(dotted, ".")
	var cur any = cfg
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = m[p]
		if !ok {
			return 0, false
		}
	}
	return asIntNumber(cur)
}

func asIntNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return int(n), true
	}
	return 0, false
}

func sessionKeyFromSDK(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return "_default"
	}
	if id, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return "_default"
}

// parsedParams mirrors omp's paramsSchema union in prelude-definition.ts:22-43.
type parsedParams struct {
	action   string
	agents   ScopeSpec
	repo     *string
	draft    string
	path     *string
	units    []string
	grep     *string
	globs    []string
	subject  string
	body     string
	fixes    []string
	amend    bool
	noVerify bool
}

func parseCommitParams(raw json.RawMessage) (*parsedParams, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("commit received invalid arguments: expected an object")
	}
	action, ok := m["action"].(string)
	if !ok {
		return nil, fmt.Errorf(`commit received invalid arguments: action must be "draft", "show", "take", "drop", "only", "diff" or "commit" (was %s)`, formatJSVal(m["action"]))
	}
	rejectExtra := func(allowed ...string) error {
		allowSet := make(map[string]bool, len(allowed)+1)
		allowSet["action"] = true
		for _, k := range allowed {
			allowSet[k] = true
		}
		for k := range m {
			if !allowSet[k] {
				return fmt.Errorf(`commit received invalid arguments: %s must be removed`, k)
			}
		}
		return nil
	}
	requireNonEmptyString := func(key string) (string, error) {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf(`commit received invalid arguments: %s must be a string (was missing)`, key)
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf(`commit received invalid arguments: %s must be a string (was %s)`, key, formatJSVal(v))
		}
		if s == "" {
			return "", fmt.Errorf(`commit received invalid arguments: %s must be at least length 1 (was 0)`, key)
		}
		return s, nil
	}
	optionalNonEmptyString := func(key string) (*string, error) {
		v, ok := m[key]
		if !ok {
			return nil, nil
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf(`commit received invalid arguments: %s must be a string (was %s)`, key, formatJSVal(v))
		}
		if s == "" {
			return nil, fmt.Errorf(`commit received invalid arguments: %s must be at least length 1 (was 0)`, key)
		}
		return &s, nil
	}
	requireStringSlice := func(key string) ([]string, error) {
		v, ok := m[key]
		if !ok {
			return nil, fmt.Errorf(`commit received invalid arguments: %s must be a string array`, key)
		}
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf(`commit received invalid arguments: %s must be a string array`, key)
		}
		out := make([]string, len(arr))
		for i, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf(`commit received invalid arguments: %s[%d] must be a string`, key, i)
			}
			out[i] = s
		}
		return out, nil
	}

	p := &parsedParams{action: action}
	switch action {
	case "draft":
		if err := rejectExtra("agents", "repo"); err != nil {
			return nil, err
		}
		if v, ok := m["agents"]; ok {
			switch a := v.(type) {
			case string:
				p.agents = ScopeString(a)
			case []any:
				list := make([]string, len(a))
				for i, item := range a {
					s, ok := item.(string)
					if !ok {
						return nil, fmt.Errorf("commit received invalid arguments: agents[%d] must be a string", i)
					}
					list[i] = s
				}
				p.agents = ScopeList(list)
			default:
				return nil, errors.New("commit received invalid arguments: agents must be a string or string[]")
			}
		}
		repo, err := optionalNonEmptyString("repo")
		if err != nil {
			return nil, err
		}
		p.repo = repo
		return p, nil

	case "show":
		if err := rejectExtra("draft", "path"); err != nil {
			return nil, err
		}
		draftID, err := requireNonEmptyString("draft")
		if err != nil {
			return nil, err
		}
		p.draft = draftID
		pathStr, err := optionalNonEmptyString("path")
		if err != nil {
			return nil, err
		}
		p.path = pathStr
		return p, nil

	case "take", "drop":
		if err := rejectExtra("draft", "path", "units", "grep"); err != nil {
			return nil, err
		}
		draftID, err := requireNonEmptyString("draft")
		if err != nil {
			return nil, err
		}
		p.draft = draftID
		pathStr, err := optionalNonEmptyString("path")
		if err != nil {
			return nil, err
		}
		p.path = pathStr
		if _, hasUnits := m["units"]; hasUnits {
			units, err := requireStringSlice("units")
			if err != nil {
				return nil, err
			}
			p.units = units
		}
		grepStr, err := optionalNonEmptyString("grep")
		if err != nil {
			return nil, err
		}
		p.grep = grepStr
		return p, nil

	case "only":
		if err := rejectExtra("draft", "globs"); err != nil {
			return nil, err
		}
		draftID, err := requireNonEmptyString("draft")
		if err != nil {
			return nil, err
		}
		p.draft = draftID
		globs, err := requireStringSlice("globs")
		if err != nil {
			return nil, err
		}
		p.globs = globs
		return p, nil

	case "diff":
		if err := rejectExtra("draft"); err != nil {
			return nil, err
		}
		draftID, err := requireNonEmptyString("draft")
		if err != nil {
			return nil, err
		}
		p.draft = draftID
		return p, nil

	case "commit":
		if err := rejectExtra("draft", "subject", "body", "fixes", "amend", "noVerify"); err != nil {
			return nil, err
		}
		draftID, err := requireNonEmptyString("draft")
		if err != nil {
			return nil, err
		}
		p.draft = draftID
		subject, err := requireNonEmptyString("subject")
		if err != nil {
			return nil, err
		}
		p.subject = subject
		if v, ok := m["body"]; ok {
			switch b := v.(type) {
			case string:
				p.body = b
			case []any:
				lines := make([]string, len(b))
				for i, item := range b {
					s, ok := item.(string)
					if !ok {
						return nil, fmt.Errorf("commit received invalid arguments: body[%d] must be a string", i)
					}
					lines[i] = s
				}
				p.body = strings.Join(lines, "\n")
			default:
				return nil, errors.New("commit received invalid arguments: body must be a string or string[]")
			}
		}
		if v, ok := m["fixes"]; ok {
			arr, ok := v.([]any)
			if !ok {
				return nil, errors.New("commit received invalid arguments: fixes must be an array of numbers or strings")
			}
			fixes := make([]string, len(arr))
			for i, item := range arr {
				switch f := item.(type) {
				case string:
					fixes[i] = f
				case float64:
					fixes[i] = strconv.FormatFloat(f, 'f', -1, 64)
				default:
					return nil, fmt.Errorf("commit received invalid arguments: fixes[%d] must be a number or string", i)
				}
			}
			p.fixes = fixes
		}
		if v, ok := m["amend"]; ok {
			b, ok := v.(bool)
			if !ok {
				return nil, errors.New("commit received invalid arguments: amend must be a boolean")
			}
			p.amend = b
		}
		if v, ok := m["noVerify"]; ok {
			b, ok := v.(bool)
			if !ok {
				return nil, errors.New("commit received invalid arguments: noVerify must be a boolean")
			}
			p.noVerify = b
		}
		return p, nil

	default:
		return nil, fmt.Errorf(`commit received invalid arguments: action must be "draft", "show", "take", "drop", "only", "diff" or "commit" (was %q)`, action)
	}
}

func formatJSVal(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// describeCall produces omp's status-tree line for a completed call.
// upstream: prelude-definition.ts:74
func describeCall(raw json.RawMessage) string {
	p, err := parseCommitParams(raw)
	if err != nil {
		return ""
	}
	switch p.action {
	case "draft":
		var parts []string
		if p.agents.Set {
			if len(p.agents.List) > 0 {
				parts = append(parts, p.agents.List...)
			} else if p.agents.Single != "" {
				parts = append(parts, p.agents.Single)
			}
		}
		if p.repo != nil && *p.repo != "" {
			parts = append(parts, "repo="+*p.repo)
		}
		return fmt.Sprintf("commit.draft(%s)", strings.Join(parts, ", "))
	case "take", "drop":
		var parts []string
		if p.path != nil && *p.path != "" {
			parts = append(parts, *p.path)
		}
		parts = append(parts, p.units...)
		if p.grep != nil && *p.grep != "" {
			parts = append(parts, "/"+*p.grep+"/")
		}
		return fmt.Sprintf("commit.%s(%s)", p.action, strings.Join(parts, " "))
	case "only":
		return fmt.Sprintf("commit.only(%s)", strings.Join(p.globs, ", "))
	case "commit":
		b, _ := json.Marshal(p.subject)
		return fmt.Sprintf("commit.commit(%s)", string(b))
	default:
		return fmt.Sprintf("commit.%s()", p.action)
	}
}

func describeDraft(d *Draft) map[string]any {
	scope := make([]string, len(d.Scope))
	copy(scope, d.Scope)
	var head any
	if d.Head != nil {
		head = *d.Head
	}
	return map[string]any{
		"id":    d.ID,
		"head":  head,
		"scope": scope,
	}
}

func resolveToCwd(target, cwd string) string {
	var p string
	if target == "" {
		p = cwd
	} else if filepath.IsAbs(target) {
		p = filepath.Clean(target)
	} else {
		p = filepath.Clean(filepath.Join(cwd, target))
	}
	return dirs.StandardizeMacOSPath(p)
}

// PreludeDefinition constructs the eval.Prelude for `commit`.
// upstream: prelude-definition.ts:96
func PreludeDefinition(store *sessionDraftStore) eval.Prelude {
	if store == nil {
		store = defaultDraftStore
	}
	return eval.Prelude{
		Name: "commit",
		Documentation: func() string {
			return strings.TrimSpace(commitDocumentation)
		},
		Guidance: func() string {
			return strings.TrimSpace(commitGuidance)
		},
		JavaScript: commitJavascript,
		Python:     commitPython,
		Exports:    []string{"commit"},
		Enabled:    commitEnabled,
		// upstream: prelude-definition.ts:189-192 — "exec" for commit and invalid
		// arguments; "read" for draft/show/take/drop/only/diff.
		ApprovalFor: func(parameters json.RawMessage) string {
			p, err := parseCommitParams(parameters)
			if err != nil || p.action == "commit" {
				return "exec"
			}
			return "read"
		},
		Invoke: func(call eval.PreludeCall, rawParams json.RawMessage) (*eval.PreludeResult, error) {
			return invokeCommitPrelude(store, call, rawParams)
		},
		Status: func(parameters json.RawMessage, _ *eval.PreludeResult) string {
			return describeCall(parameters)
		},
	}
}

func invokeCommitPrelude(store *sessionDraftStore, call eval.PreludeCall, rawParams json.RawMessage) (*eval.PreludeResult, error) {
	p, err := parseCommitParams(rawParams)
	if err != nil {
		return nil, err
	}
	sessKey := sessionKeyFromSDK(call.SDK)
	var cwd string
	var done <-chan struct{}
	var zero sdk.Context
	if call.SDK != zero {
		cwd = call.SDK.Cwd()
		done = call.SDK.Done()
	}
	artDir := DefaultArtifactsDir(call.SDK)
	agentID := DefaultAgentIdentity(call.SDK)

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-reqCtx.Done():
			}
		}()
	}

	makeDraftCtx := func(targetCwd string) DraftContext {
		return DraftContext{
			Cwd:          targetCwd,
			ArtifactsDir: artDir,
			Agent:        agentID.Agent,
			IsRunning: func(id string) bool {
				if IsAgentRunning != nil {
					return IsAgentRunning(call.SDK, id)
				}
				return false
			},
			Ctx: reqCtx,
		}
	}

	if p.action == "draft" {
		targetCwd := dirs.StandardizeMacOSPath(cwd)
		if p.repo != nil {
			targetCwd = resolveToCwd(*p.repo, cwd)
		}
		draft, err := DraftFor(makeDraftCtx(targetCwd), p.agents)
		if err != nil {
			return nil, err
		}
		store.remember(sessKey, draft, p.agents)
		return &eval.PreludeResult{
			Text:    draft.Summary(),
			Details: describeDraft(draft),
		}, nil
	}

	draft, scope, ok := store.lookup(sessKey, p.draft)
	if !ok {
		return nil, fmt.Errorf("Draft %s expired or was committed; call commit.draft() again.", p.draft)
	}

	switch p.action {
	case "show":
		text, err := draft.Show(p.path)
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: text}, nil

	case "diff":
		patch := draft.Diff()
		view := FormatPatchPreview(patch, outputMaxColumns(call.SDK))
		if view == patch {
			return &eval.PreludeResult{Text: view}, nil
		}
		return &eval.PreludeResult{
			Text:    view,
			Details: map[string]any{"patch": patch},
		}, nil

	case "only":
		msg, err := draft.Only(p.globs)
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: msg + "\n" + draft.Summary()}, nil

	case "take", "drop":
		target := SelectionTarget{Path: p.path, Units: p.units, Grep: p.grep}
		var msg string
		if p.action == "take" {
			msg, err = draft.Take(target)
		} else {
			msg, err = draft.Drop(target)
		}
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: msg + "\n" + draft.Summary()}, nil

	case "commit":
		var journal *ChangeJournal
		if artDir != "" {
			journal = JournalAt(artDir)
		}
		outcome, err := CommitSelection(
			draft,
			CommitRequest{
				Subject:  p.subject,
				Body:     p.body,
				Fixes:    p.fixes,
				Amend:    p.amend,
				NoVerify: p.noVerify,
			},
			CommitContext{
				Journal: journal,
				Agent:   agentID,
				Ctx:     reqCtx,
			},
		)
		if err != nil {
			return nil, err
		}
		store.remove(sessKey, draft.ID)
		remaining, err := DraftFor(makeDraftCtx(draft.Root), scope)
		if err != nil {
			return nil, err
		}
		store.remember(sessKey, remaining, scope)

		branchLabel := draft.Branch
		if branchLabel == "" {
			branchLabel = "HEAD"
		}
		shaShort := outcome.SHA
		if len(shaShort) > 10 {
			shaShort = shaShort[:10]
		}
		lines := []string{fmt.Sprintf("[%s %s] %s", branchLabel, shaShort, outcome.Subject)}
		if outcome.Amended != "" {
			amendedShort := outcome.Amended
			if len(amendedShort) > 10 {
				amendedShort = amendedShort[:10]
			}
			parentDesc := "none, root commit"
			if outcome.Parent != nil {
				parentDesc = *outcome.Parent
				if len(parentDesc) > 10 {
					parentDesc = parentDesc[:10]
				}
			}
			lines = append(lines, fmt.Sprintf("amended %s (parent %s)", amendedShort, parentDesc))
		}
		lines = append(lines, fmt.Sprintf("%d path(s): %s", len(outcome.Paths), strings.Join(outcome.Paths, ", ")))
		var placedOn *string
		if outcome.Amended != "" {
			a := outcome.Amended
			placedOn = &a
		} else if outcome.Parent != nil {
			placedOn = outcome.Parent
		}
		if placedOn != nil && !equalStringPtr(placedOn, draft.Head) {
			pShort := *placedOn
			if len(pShort) > 10 {
				pShort = pShort[:10]
			}
			lines = append(lines, fmt.Sprintf("HEAD had moved to %s; the selection applied cleanly on top.", pShort))
		}
		lines = append(lines, outcome.IndexNotes...)
		lines = append(lines, "", "Remaining:", remaining.Summary())

		det := map[string]any{
			"sha":        outcome.SHA,
			"subject":    outcome.Subject,
			"paths":      outcome.Paths,
			"parent":     nil,
			"indexNotes": outcome.IndexNotes,
			"remaining":  describeDraft(remaining),
		}
		if outcome.Parent != nil {
			det["parent"] = *outcome.Parent
		}
		if outcome.Amended != "" {
			det["amended"] = outcome.Amended
		}
		return &eval.PreludeResult{
			Text:    strings.Join(lines, "\n"),
			Details: det,
		}, nil
	}
	return nil, fmt.Errorf("commit received unknown action %q", p.action)
}
