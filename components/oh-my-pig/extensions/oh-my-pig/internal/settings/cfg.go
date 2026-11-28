package settings

import (
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

//go:embed cfg.md
var cfgPromptDoc string

//go:embed cfg-approval-timeout.md
var cfgApprovalTimeoutTemplate string

//go:embed cfg-env-shadowed.md
var cfgEnvShadowedTemplate string

//go:embed cfg-write-result.md
var cfgWriteResultTemplate string

const (
	cfgURLPrefix       = "cfg://"
	cfgSaveSegment     = "save"
	redactedValue      = "<redacted>"
	treeCommentMaxRune = 120

	// Timeout detection window mirroring omp's ask/confirm fallback tolerance.
	// When PiG's ConfirmWithOptions returns false right at the configured deadline,
	// we distinguish an unanswered timeout from a user dismissal.
	timeoutDetectionTolerance = 250 * time.Millisecond
)

// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:66
var provenanceLabels = map[Provenance]string{
	ProvenanceEnv:     "environment variable",
	ProvenanceRuntime: "session override",
	ProvenanceOverlay: "--config overlay",
	ProvenanceProject: "project config",
	ProvenanceGlobal:  "global config",
	ProvenanceDefault: "default",
}

// Approval is the user's answer to a cfg:// change request.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts CfgApproval
type Approval string

const (
	ApprovalOnce    Approval = "once"
	ApprovalSession Approval = "session"
	ApprovalDeny    Approval = "deny"
	ApprovalTimeout Approval = "timeout"
)

// ChangeRequest is a settings change awaiting user approval.
// Values are display-formatted with credentials redacted.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts CfgChangeRequest
type ChangeRequest struct {
	Path             string
	Previous         string
	Value            string
	Save             bool
	ShadowedBy       string
	WillDropComments bool // true when /save must re-serialize over a commented file (T7)
}

// AppliedChange reports a settings write that took effect.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts CfgAppliedChange
type AppliedChange struct {
	Path  string
	Value any
	Store *Store
	Save  bool
}

// ApprovalHost optionally overrides the default sdk.Context.Confirm approval
// prompt (useful for hosts with a 3-choice session-grant selector or tests).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts CfgApprovalHost
type ApprovalHost interface {
	Approve(c urls.Context, req ChangeRequest) (Approval, error)
	Applied(change AppliedChange)
	PersistentStore(c urls.Context) *Store
}

// WriteOutcome describes how a cfg:// write ended.
// upstream: packages/tui/src/tools/cfg-render.ts CfgWriteOutcome
type WriteOutcome string

const (
	OutcomeApplied   WriteOutcome = "applied"
	OutcomeDeclined  WriteOutcome = "declined"
	OutcomeUnchanged WriteOutcome = "unchanged"
)

// ReadDetails matches omp's CfgReadDetails (packages/tui/src/tools/cfg-render.ts).
type ReadDetails struct {
	Path     string `json:"path"`
	Count    int    `json:"count"`
	Modified int    `json:"modified"`
}

// WriteDetails matches omp's CfgWriteDetails (packages/tui/src/tools/cfg-render.ts).
type WriteDetails struct {
	Path      string       `json:"path"`
	Save      bool         `json:"save"`
	Previous  string       `json:"previous"`
	Value     string       `json:"value"`
	Outcome   WriteOutcome `json:"outcome"`
	Effective string       `json:"effective,omitempty"`
}

// Target is a parsed cfg:// URL.
// upstream: packages/tui/src/tools/cfg-url.ts CfgTarget
type Target struct {
	Segments []string
	Save     bool
}

// ParseCfgURL parses `cfg://advisor/enabled` or `cfg://advisor.enabled/save`.
// upstream: packages/tui/src/tools/cfg-url.ts parseCfgUrl
func ParseCfgURL(input string) (Target, bool) {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) < len(cfgURLPrefix) || !strings.EqualFold(trimmed[:len(cfgURLPrefix)], cfgURLPrefix) {
		return Target{}, false
	}
	rest := trimmed[len(cfgURLPrefix):]
	if idx := strings.IndexAny(rest, "?#"); idx >= 0 {
		rest = rest[:idx]
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '.'
	})
	save := len(parts) > 1 && parts[len(parts)-1] == cfgSaveSegment
	if save {
		parts = parts[:len(parts)-1]
	}
	return Target{Segments: parts, Save: save}, true
}

var (
	cfgMu           sync.Mutex
	approvalHost    ApprovalHost
	approvalPromptQ sync.Mutex // serializes concurrent approval prompts (approvalQueue in omp)
	// nowFunc is overridable in tests for deterministic timeout timing.
	nowFunc = time.Now
	// sessionIDHook allows tests without a live fake host to supply a session id.
	sessionIDHook func(c urls.Context) string
)

// SetApprovalHost registers a custom approval host for cfg:// writes.
// Passing nil restores the default sdk.Context.Confirm host.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts setCfgApprovalHost
func SetApprovalHost(host ApprovalHost) {
	cfgMu.Lock()
	approvalHost = host
	cfgMu.Unlock()
	clearAllSessionGrants()
}

func resetCfgStateForTests() {
	cfgMu.Lock()
	approvalHost = nil
	nowFunc = time.Now
	sessionIDHook = nil
	cfgMu.Unlock()
}

// Register installs the cfg:// handler on the process-wide internal URL router,
// registers per-session cleanup via lifecycle.OnEnd, refreshes project trust on
// `session_start` and `before_agent_start` (never on `project_trust`, which fires
// before a trust decision is made: T2), and hooks `session_shutdown` to release
// only the ending session's state. Safe to call multiple times in one process
// without disturbing live sessions; does no I/O and never panics.
func Register(e *sdk.Extension) {
	_ = urls.Register(NewCfgHandler())
	lifecycle.OnEnd("settings", releaseSession)
	if e != nil {
		e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
			startSession(ctx)
			return nil, nil
		})
		e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			refreshSessionTrust(ctx)
			return nil, nil
		})
		e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
			if id := safeSessionID(ctx); id != "" {
				lifecycle.End(id)
			}
			return nil, nil
		})
	}
}

// CfgHandler implements urls.Handler, urls.Writer, urls.Completer, and urls.PromptDocer
// for the `cfg://` scheme.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts CfgProtocolHandler
type CfgHandler struct{}

// NewCfgHandler returns a handler for `cfg://`.
func NewCfgHandler() *CfgHandler { return &CfgHandler{} }

func (h *CfgHandler) Scheme() string { return "cfg" }

func (h *CfgHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingVirtual,
		Selectors: urls.SelectorsLines,
		Immutable: true,
		Write: &urls.WritePolicy{
			Via:     "handler",
			Payload: "verbatim",
			Scope:   "workspace",
		},
	}
}

// PromptDoc advertises cfg:// only when writes can be approved.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:352
func (h *CfgHandler) PromptDoc(host urls.SchemeHost) string {
	if !host.SettingsApproval {
		return ""
	}
	return strings.TrimSpace(cfgPromptDoc)
}

// Resolve serves `cfg://` (full tree), `cfg://<ns>` (namespace subtree), and
// `cfg://<ns>/<key>` (leaf value, type, default, source, choices, description).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:356
func (h *CfgHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	store := StoreFor(c.SDK)
	raw := u.RawHref
	if raw == "" {
		raw = u.Href
	}
	target, _ := ParseCfgURL(raw)
	resolvedPath, leaf, members, err := resolveSegments(target.Segments)
	if err != nil {
		return nil, err
	}
	var sections []string
	modified := 0
	if leaf != nil {
		sections = append(sections, renderLeaf(store, leaf))
		val, _ := store.getOrdered(leaf.ID)
		if !orderedValuesEqual(val, leaf.DefaultClone()) {
			modified++
		}
	}
	if len(members) > 0 {
		treeText, treeMod := renderTree(store, members, resolvedPath)
		sections = append(sections, treeText)
		modified += treeMod
	}
	content := strings.Join(sections, "\n\n")
	count := len(members)
	if leaf != nil {
		count++
	}
	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/plain",
		Details: map[string]any{
			"cfg": ReadDetails{
				Path:     resolvedPath,
				Count:    count,
				Modified: modified,
			},
		},
	}, nil
}

// Write applies a session-only override (`cfg://<key>`) or persisted global write
// (`cfg://<key>/save`) after user approval through Context.Confirm (or the
// registered ApprovalHost).
//
// Only interactive TUI sessions may write (matching omp's settingsApproval gate;
// print, JSON, and RPC sessions are refused, fixes A3).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:381
func (h *CfgHandler) Write(c urls.Context, u urls.URL, content string) (*urls.WriteResult, error) {
	cfgMu.Lock()
	host := approvalHost
	cfgMu.Unlock()

	// Refuse print, JSON, and RPC sessions (upstream cfg-protocol.ts:327-340, fixes A3).
	if host == nil && (!safeHasUI(c.SDK) || safeMode(c.SDK) != "tui") {
		return nil, errors.New("Changing settings requires user approval, but this session has no interactive UI. Ask the user to change the setting themselves.")
	}

	store := StoreFor(c.SDK)
	raw := u.RawHref
	if raw == "" {
		raw = u.Href
	}
	target, _ := ParseCfgURL(raw)
	resolvedPath, leaf, _, err := resolveSegments(target.Segments)
	if err != nil {
		return nil, err
	}
	if leaf == nil {
		prefix := ""
		if resolvedPath != "" {
			prefix = strings.ReplaceAll(resolvedPath, ".", "/") + "/"
		}
		example := fmt.Sprintf("%s%s<key>", cfgURLPrefix, prefix)
		name := resolvedPath
		if name == "" {
			name = cfgURLPrefix
		}
		return nil, fmt.Errorf("%s is a namespace; write a single setting, e.g. %s.", name, example)
	}

	save := target.Save
	value, err := leaf.ParseText(content)
	if err != nil {
		return nil, err
	}
	previous, _ := store.getOrdered(leaf.ID)
	req := ChangeRequest{
		Path:     leaf.ID,
		Previous: formatValue(leaf, previous),
		Value:    formatValue(leaf, value),
		Save:     save,
	}
	settingURL := cfgURLPrefix + strings.ReplaceAll(leaf.ID, ".", "/")

	finish := func(outcome WriteOutcome, shadowLayer Provenance, shadowStore *Store) *urls.WriteResult {
		var effective string
		if shadowLayer != "" && shadowStore != nil {
			effVal, _ := shadowStore.getOrdered(leaf.ID)
			effective = formatValue(leaf, effVal)
		}
		details := WriteDetails{
			Path:      req.Path,
			Save:      req.Save,
			Previous:  req.Previous,
			Value:     req.Value,
			Outcome:   outcome,
			Effective: effective,
		}
		text := renderWriteResult(writeResultVars{
			path:       req.Path,
			previous:   req.Previous,
			value:      req.Value,
			url:        settingURL,
			saveURL:    settingURL + "/" + cfgSaveSegment,
			declined:   outcome == OutcomeDeclined,
			unchanged:  outcome == OutcomeUnchanged,
			saved:      outcome == OutcomeApplied && save,
			applied:    outcome == OutcomeApplied && !save,
			effective:  effective,
			provenance: provenanceLabels[shadowLayer],
		})
		return &urls.WriteResult{
			Text:    text,
			Details: map[string]any{"cfg": details},
		}
	}

	if !save && orderedValuesEqual(previous, value) {
		return finish(OutcomeUnchanged, "", nil), nil
	}

	// Refuse before prompting when a non-fallback env var overrides the setting.
	// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:424
	if !save && leaf.EnvFallback == "" && shadowingLayer(leaf, store, value, aboveSession) != "" {
		return nil, errors.New(renderEnvShadowed(leaf.ID, formatValue(leaf, previous), leaf.EnvName))
	}

	persistent := store
	if host != nil {
		if ps := host.PersistentStore(c); ps != nil {
			persistent = ps
		}
	}

	if save {
		// Fail before prompting if the on-disk global config is known to be unparseable (B3).
		if loadErr := persistent.LoadError(); loadErr != nil {
			return nil, fmt.Errorf("cannot save %s: %w", leaf.ID, loadErr)
		}
		above := make([]Provenance, 0, len(aboveGlobal))
		for _, layer := range aboveGlobal {
			if layer == ProvenanceEnv && leaf.EnvFallback != "" {
				continue
			}
			if layer == ProvenanceRuntime && store == persistent {
				continue
			}
			above = append(above, layer)
		}
		if layer := saveShadowingLayer(leaf, persistent, value, above); layer != "" {
			if layer == ProvenanceEnv && leaf.EnvName != "" {
				req.ShadowedBy = "environment variable " + leaf.EnvName
			} else {
				req.ShadowedBy = provenanceLabels[layer]
			}
		}
		req.WillDropComments = persistent.SaveWillDropComments(leaf.ID, value)
	}

	// Serialize concurrent prompts, mirroring omp's approvalQueue.
	approvalPromptQ.Lock()
	answer, approveErr := decideApproval(host, c, req)
	approvalPromptQ.Unlock()
	if approveErr != nil {
		return nil, approveErr
	}
	if answer == ApprovalTimeout {
		return nil, errors.New(renderApprovalTimeout(leaf.ID, req.Previous))
	}
	if answer == ApprovalDeny {
		return finish(OutcomeDeclined, "", nil), nil
	}

	if !save {
		if err := store.Set(leaf.ID, value, false); err != nil {
			return nil, err
		}
		effVal, _ := store.Get(leaf.ID)
		if host != nil {
			host.Applied(AppliedChange{Path: leaf.ID, Value: effVal, Store: store, Save: false})
		}
		layer := shadowingLayer(leaf, store, value, aboveSession)
		if layer != "" {
			return finish(OutcomeApplied, layer, store), nil
		}
		return finish(OutcomeApplied, "", nil), nil
	}

	saveRes, err := persistent.setWithResult(leaf.ID, value, true)
	if err != nil {
		return nil, err
	}
	if store == persistent {
		store.ClearOverride(leaf.ID)
	} else {
		if err := store.Set(leaf.ID, value, false); err != nil {
			return nil, err
		}
		if host != nil {
			pVal, _ := persistent.Get(leaf.ID)
			host.Applied(AppliedChange{Path: leaf.ID, Value: pVal, Store: persistent, Save: true})
		}
	}
	if host != nil {
		sVal, _ := store.Get(leaf.ID)
		host.Applied(AppliedChange{Path: leaf.ID, Value: sVal, Store: store, Save: true})
	}
	layer := shadowingLayer(leaf, persistent, value, aboveGlobal)
	var out *urls.WriteResult
	if layer != "" {
		out = finish(OutcomeApplied, layer, persistent)
	} else {
		out = finish(OutcomeApplied, "", nil)
	}
	if saveRes.changed && !saveRes.commentsPreserved {
		out.Text += "\nNote: comments in the config file were not preserved when rewriting this setting."
	}
	return out, nil
}

// Complete lists all settings as `ns/key` autocomplete candidates.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:489
func (h *CfgHandler) Complete(c urls.Context, query string) ([]urls.Completion, error) {
	_ = c
	q := strings.ToLower(strings.TrimSpace(query))
	all := SortedSchema()
	out := make([]urls.Completion, 0, len(all))
	for _, s := range all {
		val := strings.ReplaceAll(s.ID, ".", "/")
		if q != "" && !strings.Contains(strings.ToLower(val), q) && !strings.Contains(strings.ToLower(s.ID), q) {
			continue
		}
		out = append(out, urls.Completion{
			Value:       val,
			Description: s.Description,
		})
	}
	return out, nil
}

// decideApproval answers from the session grant when it covers req; otherwise
// prompts and records a new grant keyed ONLY by session ID (never cwd, fixes B8).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:135-148
func decideApproval(host ApprovalHost, c urls.Context, req ChangeRequest) (Approval, error) {
	sessionID := resolveSessionID(c)
	if sessionID != "" {
		if grant, ok := getSessionGrant(sessionID); ok && (grant.save || !req.Save) {
			return ApprovalOnce, nil
		}
	}

	var (
		ans Approval
		err error
	)
	if host != nil {
		ans, err = host.Approve(c, req)
	} else {
		ans, err = confirmViaSDK(c, req)
	}
	if err != nil {
		return ApprovalDeny, err
	}
	if ans == ApprovalSession && sessionID != "" {
		setSessionGrant(sessionID, req.Save)
	}
	return ans, nil
}

func resolveSessionID(c urls.Context) string {
	cfgMu.Lock()
	hook := sessionIDHook
	cfgMu.Unlock()
	if hook != nil {
		return hook(c)
	}
	return safeSessionID(c.SDK)
}

// confirmViaSDK prompts the user through sdk.Context.Confirm / ConfirmWithOptions,
// honouring ask.timeout (seconds; 0 disables deadline) and omp's headline/warning format.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts #promptCfgChange
func confirmViaSDK(c urls.Context, req ChangeRequest) (Approval, error) {
	headline := fmt.Sprintf("⚙️ Your agent wants to change `%s` for this session.", req.Path)
	if req.Save {
		headline = fmt.Sprintf("💾 Your agent wants to save `%s` to your config.", req.Path)
	}
	body := fmt.Sprintf("%s → %s", req.Previous, req.Value)
	if req.ShadowedBy != "" {
		body += fmt.Sprintf("\n⚠️ Overridden by your %s: the saved value won't take effect here.", req.ShadowedBy)
	}
	if req.WillDropComments {
		body += "\n⚠️ Saving this list/record value will re-serialize config.yml and drop existing comments."
	}
	timeoutMs := AskTimeoutMs(c.SDK)
	start := nowFunc()
	var (
		raw any
		err error
	)
	if timeoutMs > 0 {
		raw, err = c.SDK.ConfirmWithOptions(headline, body, sdk.DialogOptions{Timeout: timeoutMs})
	} else {
		raw, err = c.SDK.Confirm(headline, body)
	}
	if err != nil {
		if isTimeoutError(err) {
			return ApprovalTimeout, nil
		}
		return ApprovalDeny, err
	}
	if confirmed, ok := raw.(bool); ok && confirmed {
		return ApprovalOnce, nil
	}
	if timeoutMs > 0 {
		deadline := time.Duration(timeoutMs * float64(time.Millisecond))
		elapsed := nowFunc().Sub(start)
		if elapsed+timeoutDetectionTolerance >= deadline {
			return ApprovalTimeout, nil
		}
	}
	return ApprovalDeny, nil
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded")
}

// ── Rendering & Shadowing helpers ────────────────────────────────────────────

var (
	aboveSession = []Provenance{ProvenanceEnv}
	aboveGlobal  = []Provenance{ProvenanceEnv, ProvenanceRuntime, ProvenanceOverlay, ProvenanceProject}
	sentenceRe   = regexp.MustCompile(`(?s)^.*?[.!?](?:\s|$)`)
)

// formatValue formats a setting value like omp's formatValue (JSON.stringify
// with no HTML escaping, preserving key order, and JS number formatting; fixes A2, A5).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:150
func formatValue(entry *Entry, value any) string {
	if value == nil {
		return "null"
	}
	if entry.Credential {
		if s, ok := value.(string); !ok || s != "" {
			return redactedValue
		}
	}
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return formatJSNumber(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		s, err := marshalJSJSON(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return s
	}
}

// resolveSegments canonicalizes URL segments case-insensitively against the schema.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts resolveSegments
func resolveSegments(segments []string) (resolvedPath string, leaf *Entry, members []*Entry, err error) {
	all := SortedSchema()
	if len(segments) == 0 {
		return "", nil, all, nil
	}
	joined := strings.Join(segments, ".")
	lower := strings.ToLower(joined)
	prefix := lower + "."
	for _, cand := range all {
		candLower := strings.ToLower(cand.ID)
		if candLower == lower {
			leaf = cand
		} else if strings.HasPrefix(candLower, prefix) {
			members = append(members, cand)
		}
	}
	if leaf != nil {
		resolvedPath = leaf.ID
	} else if len(members) > 0 {
		resolvedPath = members[0].ID[:len(lower)]
	} else {
		similar := fuzzyFilterSettings(joined, all, 8)
		hint := ""
		if len(similar) > 0 {
			hint = "\nSimilar: " + strings.Join(similar, ", ")
		}
		return "", nil, nil, fmt.Errorf("Unknown setting: %s%s\nRead %s for the full tree.", joined, hint, cfgURLPrefix)
	}
	return resolvedPath, leaf, members, nil
}

func treeComment(entry *Entry, value any) string {
	var parts []string
	if len(entry.Enum) > 0 {
		parts = append(parts, strings.Join(entry.Enum, "|"))
	}
	fallback := entry.DefaultClone()
	if !orderedValuesEqual(value, fallback) {
		parts = append(parts, "default "+formatValue(entry, fallback))
	}
	if entry.Description != "" {
		sentence := entry.Description
		if loc := sentenceRe.FindStringIndex(sentence); loc != nil {
			sentence = strings.TrimRight(sentence[:loc[1]], " \t\r\n")
		}
		runes := []rune(sentence)
		if len(runes) > treeCommentMaxRune {
			sentence = string(runes[:treeCommentMaxRune-1]) + "…"
		}
		parts = append(parts, sentence)
	}
	if len(parts) == 0 {
		return ""
	}
	return "  # " + strings.Join(parts, " · ")
}

// renderTree formats a YAML-ish tree of members rooted below prefix.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts renderTree
func renderTree(store *Store, members []*Entry, prefix string) (string, int) {
	lines := make([]string, 0, len(members))
	modified := 0
	var opened []string
	strip := 0
	if prefix != "" {
		strip = len(prefix) + 1
	}
	for _, entry := range members {
		rel := entry.ID[strip:]
		segs := strings.Split(rel, ".")
		shared := 0
		for shared < len(opened) && shared < len(segs)-1 && opened[shared] == segs[shared] {
			shared++
		}
		opened = opened[:shared]
		for depth := shared; depth < len(segs)-1; depth++ {
			lines = append(lines, strings.Repeat("  ", depth)+segs[depth]+":")
			opened = append(opened, segs[depth])
		}
		val, _ := store.getOrdered(entry.ID)
		if !orderedValuesEqual(val, entry.DefaultClone()) {
			modified++
		}
		indent := strings.Repeat("  ", len(segs)-1)
		lines = append(lines, fmt.Sprintf("%s%s: %s%s", indent, segs[len(segs)-1], formatValue(entry, val), treeComment(entry, val)))
	}
	return strings.Join(lines, "\n"), modified
}

// renderLeaf formats a single setting's value, type, default, source, enum choices, and description.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts renderLeaf
func renderLeaf(store *Store, entry *Entry) string {
	val, _ := store.getOrdered(entry.ID)
	lines := []string{
		fmt.Sprintf("%s: %s", entry.ID, formatValue(entry, val)),
		fmt.Sprintf("type: %s", entry.Type),
		fmt.Sprintf("default: %s", formatValue(entry, entry.DefaultClone())),
		fmt.Sprintf("source: %s", provenanceLabels[store.Provenance(entry.ID)]),
	}
	if len(entry.Enum) > 0 {
		lines = append(lines, fmt.Sprintf("values: [%s]", strings.Join(entry.Enum, ", ")))
	}
	if entry.Description != "" {
		lines = append(lines, fmt.Sprintf("description: %s", entry.Description))
	}
	return strings.Join(lines, "\n")
}

// shadowingLayer returns the higher-precedence layer keeping a write from taking
// effect, or "" when it applies.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts shadowingLayer
func shadowingLayer(entry *Entry, store *Store, value any, above []Provenance) Provenance {
	owner := store.Provenance(entry.ID)
	if !slices.Contains(above, owner) {
		return ""
	}
	effective, _ := store.getOrdered(entry.ID)
	if keepsGlobalListEntries(entry, owner, effective, value) {
		return ""
	}
	valMap, isValMap := value.(*OrderedMap)
	effMap, isEffMap := effective.(*OrderedMap)
	if !isValMap || !isEffMap {
		if orderedValuesEqual(effective, value) {
			return ""
		}
		return owner
	}
	for _, k := range valMap.Keys {
		effVal, _ := effMap.Get(k)
		if !orderedValuesEqual(effVal, valMap.Values[k]) {
			return owner
		}
	}
	return ""
}

// saveShadowingLayer evaluates shadowing before a /save lands on persistent.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts saveShadowingLayer
func saveShadowingLayer(entry *Entry, persistent *Store, value any, above []Provenance) Provenance {
	owner := persistent.Provenance(entry.ID)
	effective, _ := persistent.getOrdered(entry.ID)
	globalVal, _ := getByPathOrdered(persistent.globalOrderedClone(), entry.Segments)
	globalOrDefault := globalVal
	if globalOrDefault == nil {
		globalOrDefault = entry.DefaultClone()
	}
	if slices.Contains(above, owner) && keepsGlobalListEntries(entry, owner, effective, globalOrDefault) {
		return ""
	}
	valMap, isValMap := value.(*OrderedMap)
	effMap, isEffMap := effective.(*OrderedMap)
	if owner == ProvenanceEnv || !slices.Contains(above, owner) || !isValMap || !isEffMap {
		return shadowingLayer(entry, persistent, value, above)
	}
	globalMap, _ := globalVal.(*OrderedMap)
	for _, k := range valMap.Keys {
		current, hasCurrent := effMap.Get(k)
		if !hasCurrent || orderedValuesEqual(current, valMap.Values[k]) {
			continue
		}
		if globalMap != nil {
			if gv, hasG := globalMap.Get(k); hasG && orderedValuesEqual(gv, current) {
				continue
			}
		}
		return owner
	}
	return ""
}

// keepsGlobalListEntries reports whether a project/overlay list with merge prepend/append
// preserves globalList's entries.
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts keepsGlobalListEntries
func keepsGlobalListEntries(entry *Entry, owner Provenance, effective, globalList any) bool {
	merge := ""
	if entry.Type == TypeArray {
		merge = entry.ArrayMerge
	}
	if (owner != ProvenanceProject && owner != ProvenanceOverlay) || (merge != "prepend" && merge != "append") {
		return false
	}
	effArr, ok1 := effective.([]any)
	globArr, ok2 := globalList.([]any)
	if !ok1 || !ok2 || len(effArr) <= len(globArr) {
		return false
	}
	offset := 0
	if merge == "prepend" {
		offset = len(effArr) - len(globArr)
	}
	for i, item := range globArr {
		if !orderedValuesEqual(effArr[offset+i], item) {
			return false
		}
	}
	return true
}

// ── Template rendering for omp's cfg-*.md prompts ────────────────────────────

func renderApprovalTimeout(path, previous string) string {
	r := strings.NewReplacer("{{path}}", path, "{{previous}}", previous)
	return strings.TrimSpace(r.Replace(cfgApprovalTimeoutTemplate))
}

func renderEnvShadowed(path, effective, envName string) string {
	r := strings.NewReplacer("{{path}}", path, "{{effective}}", effective, "{{env}}", envName)
	return strings.TrimSpace(r.Replace(cfgEnvShadowedTemplate))
}

type writeResultVars struct {
	path       string
	previous   string
	value      string
	url        string
	saveURL    string
	declined   bool
	unchanged  bool
	saved      bool
	applied    bool
	effective  string
	provenance string
}

// renderWriteResult renders cfg-write-result.md in a single pass using
// strings.NewReplacer so values containing `{{...}}` are never re-substituted (fixes B9).
func renderWriteResult(v writeResultVars) string {
	repl := strings.NewReplacer(
		"{{path}}", v.path,
		"{{previous}}", v.previous,
		"{{value}}", v.value,
		"{{url}}", v.url,
		"{{saveUrl}}", v.saveURL,
		"{{effective}}", v.effective,
		"{{provenance}}", v.provenance,
	)
	conds := map[string]bool{
		"declined":  v.declined,
		"unchanged": v.unchanged,
		"saved":     v.saved,
		"applied":   v.applied,
		"effective": v.effective != "",
	}
	return strings.TrimSpace(renderSimpleHandlebars(cfgWriteResultTemplate, conds, repl))
}

func renderSimpleHandlebars(tmpl string, conds map[string]bool, repl *strings.Replacer) string {
	lines := strings.Split(strings.ReplaceAll(tmpl, "\r\n", "\n"), "\n")
	type frame struct {
		cond   bool
		inElse bool
	}
	var stack []frame
	active := func() bool {
		for _, f := range stack {
			if f.inElse {
				if f.cond {
					return false
				}
			} else {
				if !f.cond {
					return false
				}
			}
		}
		return true
	}
	var out []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "{{#if ") && strings.HasSuffix(trim, "}}") {
			key := strings.TrimSpace(trim[len("{{#if ") : len(trim)-2])
			stack = append(stack, frame{cond: conds[key]})
			continue
		}
		if trim == "{{else}}" {
			if len(stack) > 0 {
				stack[len(stack)-1].inElse = true
			}
			continue
		}
		if trim == "{{/if}}" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !active() {
			continue
		}
		out = append(out, repl.Replace(line))
	}
	return strings.Join(out, "\n")
}
