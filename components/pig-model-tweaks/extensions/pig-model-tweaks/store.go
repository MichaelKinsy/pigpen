package pigmodeltweaks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store file. The Pi original wrote one file for the whole package,
// ~/.pi/agent/pi-tweaks-settings.json; this keeps that shape under PiG's agent
// directory with the pi-tweaks prefix replaced, matching the pmt- command
// prefix: ~/.pig/agent/pmt-settings.json.
const settingsFileName = "pmt-settings.json"

// settingsVersion is the on-disk shape version. It stays 1 because the sections
// below are the Pi original's, minus the subagent profiles this extension does
// not carry.
const settingsVersion = 1

// modelRef is one provider-qualified model.
type modelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// String renders the `provider/model` form used in commands and messages.
func (m modelRef) String() string { return m.Provider + "/" + m.Model }

// rememberModelState is the saved model and whether it is saved at all.
type rememberModelState struct {
	Enabled bool `json:"enabled"`
	Last    *struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	} `json:"last,omitempty"`
}

// modelGuardState is the allow-list. An empty list allows every model.
//
// Confirm asks the question in a dialog instead of refusing the prompt outright.
// It is off by default because a refusal needs no turn of the conversation's own
// goroutine, and a dialog asked from an input handler cannot be asked safely on
// a host that dispatches those handlers on its terminal goroutine.
type modelGuardState struct {
	Enabled       bool       `json:"enabled"`
	Confirm       bool       `json:"confirm"`
	AllowedModels []modelRef `json:"allowedModels"`
}

// lockState maps a base OpenRouter model id to a preferred upstream provider
// slug, for example "deepseek/deepseek-v4.1-flash" -> "deepseek".
type lockState struct {
	Enabled bool              `json:"enabled"`
	Locks   map[string]string `json:"locks"`
}

// settings is the whole file. Unknown fields are dropped on load and a missing
// or damaged file yields defaults, so a bad edit cannot keep the extension from
// loading.
type settings struct {
	Version                     int                `json:"version"`
	RememberModel               rememberModelState `json:"rememberModel"`
	ModelGuard                  modelGuardState    `json:"modelGuard"`
	OpenRouterModelProviderPref lockState          `json:"openrouterModelProviderPref"`
}

// store owns the settings file: one cached copy, serialized read-modify-write,
// and an atomic replace so a crash cannot leave a half-written file.
type store struct {
	// mu is a one-token channel rather than a sync.Mutex: load and update hand
	// the cached pointer out to callers, and a mutex cannot protect a value that
	// is read after it is unlocked.
	mu      chan struct{}
	home    string
	path    string
	current *settings
	// startup is the model PiG was configured with when the session began, which
	// the guard trusts: a session that opens on the model the user set up is not a
	// stray switch. A model chosen later in the session is not the startup model,
	// so the guard still sees it.
	startup modelRef
	// approvals owns the guard's confirmations that outlive their input handler.
	approvals approvals
	// approved lists the models the user said yes to at the guard's question.
	// Session-scoped and never persisted: approving a selection means "use it
	// now", not "remember it as my default". The empty ref is never in it.
	approved map[modelRef]bool
	// asking is the model a selection question is currently open for. A second
	// selection that arrives while the dialog is open supersedes it, and the
	// older dialog's answer must then do nothing.
	asking modelRef
	// reverting is the model a declined selection is switching back to. The
	// switch itself emits model_select, and that event must be consumed without
	// being asked about again, or a decline would loop.
	reverting modelRef
}

// newStore returns a store with no file bound yet. Nothing touches the
// filesystem until the first load, so constructing the extension is free.
func newStore() *store {
	s := &store{mu: make(chan struct{}, 1), approved: map[modelRef]bool{}}
	s.mu <- struct{}{}
	return s
}

// bind records PiG's agent directory from a live session. The first binding
// wins, so a later session cannot move the file another session is using.
func (s *store) bind(dir string) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.home == "" && dir != "" {
		s.home, s.path = dir, filepath.Join(dir, settingsFileName)
	}
}

// file is the settings path, resolved from the bound agent directory or, before
// any session, from PIG_HOME the way PiG itself resolves it.
func (s *store) file() string {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.path == "" {
		s.home, s.path = agentDir(""), filepath.Join(agentDir(""), settingsFileName)
	}
	return s.path
}

// load returns the current settings, reading the file once per process.
func (s *store) load() *settings {
	path := s.file()
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.current != nil {
		return s.current
	}
	s.current = readSettings(path)
	return s.current
}

// update applies mutate to a copy of the settings, persists it, and keeps the
// copy only when the write succeeded. It returns the settings as they now stand,
// so a caller can read its own change back.
func (s *store) update(mutate func(*settings)) *settings {
	path := s.file()
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.current == nil {
		s.current = readSettings(path)
	}
	next := s.current.clone()
	mutate(next)
	if err := writeSettings(path, next); err != nil {
		logf("write %s: %v", path, err)
		return s.current
	}
	s.current = next
	return next
}

// clone deep-copies the parts a mutation could touch, so a failed write cannot
// leave the in-memory copy half changed.
func (s *settings) clone() *settings {
	next := *s
	next.RememberModel.Last = nil
	if s.RememberModel.Last != nil {
		last := *s.RememberModel.Last
		next.RememberModel.Last = &last
	}
	// Non-nil so an emptied allow-list serializes as [], never as null.
	allowed := make([]modelRef, len(s.ModelGuard.AllowedModels))
	copy(allowed, s.ModelGuard.AllowedModels)
	next.ModelGuard.AllowedModels = allowed
	next.OpenRouterModelProviderPref.Locks = map[string]string{}
	for key, value := range s.OpenRouterModelProviderPref.Locks {
		next.OpenRouterModelProviderPref.Locks[key] = value
	}
	return &next
}

// defaultSettings is what an absent file means. Every feature defaults to on,
// as in the Pi original.
func defaultSettings() *settings {
	return &settings{
		Version:                     settingsVersion,
		RememberModel:               rememberModelState{Enabled: true},
		ModelGuard:                  modelGuardState{Enabled: true, Confirm: false, AllowedModels: []modelRef{}},
		OpenRouterModelProviderPref: lockState{Enabled: true, Locks: map[string]string{}},
	}
}

// readSettings loads the file, coercing anything unexpected into a valid
// settings value. A missing file is not an error.
func readSettings(path string) *settings {
	out := defaultSettings()
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		logf("%s is not valid JSON; using defaults", path)
		return out
	}
	applyRememberModel(out, object(parsed["rememberModel"]))
	applyModelGuard(out, object(parsed["modelGuard"]))
	applyLocks(out, object(parsed["openrouterModelProviderPref"]))
	return out
}

// applyRememberModel reads the rememberModel section: enabled plus the last
// provider/model pair.
func applyRememberModel(out *settings, section map[string]any) {
	if section == nil {
		return
	}
	if enabled, ok := boolValue(section["enabled"]); ok {
		out.RememberModel.Enabled = enabled
	}
	last := object(section["last"])
	provider, modelID := trimmed(last["provider"]), trimmed(last["modelId"])
	if provider == "" || modelID == "" {
		return
	}
	out.RememberModel.Last = &struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}{Provider: provider, ModelID: modelID}
}

// applyModelGuard reads the modelGuard section: enabled plus the allow-list,
// dropping entries without both halves.
func applyModelGuard(out *settings, section map[string]any) {
	if section == nil {
		return
	}
	if enabled, ok := boolValue(section["enabled"]); ok {
		out.ModelGuard.Enabled = enabled
	}
	if confirm, ok := boolValue(section["confirm"]); ok {
		out.ModelGuard.Confirm = confirm
	}
	raw, isList := section["allowedModels"].([]any)
	if !isList {
		return
	}
	out.ModelGuard.AllowedModels = nil
	for _, entry := range raw {
		item := object(entry)
		provider, model := trimmed(item["provider"]), trimmed(item["model"])
		if provider == "" || model == "" {
			continue
		}
		out.ModelGuard.AllowedModels = append(out.ModelGuard.AllowedModels, modelRef{Provider: provider, Model: model})
	}
}

// applyLocks reads the openrouterModelProviderPref section: enabled plus the
// base-model-id to provider-slug map.
func applyLocks(out *settings, section map[string]any) {
	if section == nil {
		return
	}
	if enabled, ok := boolValue(section["enabled"]); ok {
		out.OpenRouterModelProviderPref.Enabled = enabled
	}
	locks := object(section["locks"])
	if locks == nil {
		return
	}
	for key, raw := range locks {
		provider := trimmed(raw)
		if key == "" || provider == "" {
			continue
		}
		out.OpenRouterModelProviderPref.Locks[key] = provider
	}
}

// writeSettings persists the file atomically: a sibling temporary file is
// written and renamed over the target, so a reader never sees a partial file and
// an interrupted write leaves the previous one intact.
func writeSettings(path string, value *settings) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".pmt-settings-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// object returns value as a JSON object, or nil for anything else.
func object(value any) map[string]any {
	if asObject, ok := value.(map[string]any); ok {
		return asObject
	}
	return nil
}

// boolValue returns value as a bool, reporting whether it was one.
func boolValue(value any) (bool, bool) {
	asBool, ok := value.(bool)
	return asBool, ok
}

// trimmed returns value as a non-empty trimmed string, or "".
func trimmed(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// parseModelRef parses the `provider/model` form commands accept. A model id
// may itself contain slashes, so only the first one separates.
func parseModelRef(input string) (modelRef, bool) {
	value := strings.TrimSpace(input)
	provider, model, found := strings.Cut(value, "/")
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if !found || provider == "" || model == "" {
		return modelRef{}, false
	}
	return modelRef{Provider: provider, Model: model}, true
}

// formatAllowList renders the allow-list for a confirmation or status message.
func formatAllowList(models []modelRef) string {
	if len(models) == 0 {
		return "(any model)"
	}
	parts := make([]string, 0, len(models))
	for _, model := range models {
		parts = append(parts, model.String())
	}
	return strings.Join(parts, ", ")
}

// rememberStartupModel snapshots the model PiG starts this session with, so the
// guard can tell a deliberate starting point from a switch made mid-session. The
// first snapshot wins: a session switch or fork does not get to redefine what
// this process trusted when it started.
func (s *store) rememberStartupModel(dir string) {
	provider, model := readDefaultModel(dir)
	if provider == "" || model == "" {
		return
	}
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.startup.Provider == "" {
		s.startup = modelRef{Provider: provider, Model: model}
	}
}

// startupModel is the model this session began with, or the empty reference when
// PiG's settings.json names none.
func (s *store) startupModel() modelRef {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	return s.startup
}

// approveForSession records a selection the user confirmed at the guard's
// question. The approval dies with the process: it is deliberately not written
// anywhere, so the next session asks again and the remembered default stays
// whatever it was.
func (s *store) approveForSession(model modelRef) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	s.approved[model] = true
}

// approvedForSession reports whether the user approved this model earlier in
// the session. It compares like sameModel, so a locked id and its base id are
// one approval rather than two.
func (s *store) approvedForSession(state *settings, model modelRef) bool {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	for approved := range s.approved {
		if sameModel(state, approved, model) {
			return true
		}
	}
	return false
}

// markReverting remembers the model a declined selection is going back to.
func (s *store) markReverting(model modelRef) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	s.reverting = model
}

// markAsking records which model the open selection dialog is about, so an
// answer that arrives after a newer selection knows the world has moved on.
func (s *store) markAsking(model modelRef) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	s.asking = model
}

// takeAsking consumes the open-dialog marker when the answer is still about
// its model, and reports so. A newer selection has overwritten the marker with
// its own model; that dialog's answer owns it, so this one leaves it alone and
// reports false. An error path that never reaches this call leaves the marker
// set, which the next markAsking overwrites, so nothing goes stale.
func (s *store) takeAsking(model modelRef) bool {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.asking != model {
		return false
	}
	s.asking = modelRef{}
	return true
}

// peekReverting reports a pending revert without consuming it. remember-model
// runs before the guard on the same event, so it must only look: the guard's
// takeReverting clears the flag and must be the one to see it.
func (s *store) peekReverting(state *settings, model modelRef) bool {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	return s.reverting != (modelRef{}) && sameModel(state, s.reverting, model)
}

// takeReverting consumes a pending revert when the event names the model the
// revert was waiting for. A different model's event is not the revert and
// leaves the flag set, so the switch-back is still recognised when it arrives;
// the explicit clear in declineSelect's error path covers a revert that never
// emits, so no flag outlives the call that set it.
func (s *store) takeReverting(state *settings, model modelRef) bool {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if s.reverting == (modelRef{}) || !sameModel(state, s.reverting, model) {
		return false
	}
	s.reverting = modelRef{}
	return true
}

// logf reports a store or settings problem that the user cannot otherwise see.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, extensionName+": "+format+"\n", args...)
}
