package powerline_footer

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf16"
)

// Working vibes: AI-generated contextual working messages that match a theme, from pre-generated files or on demand, and the
// /vibe command. upstream: working-vibes.ts (the manager) and index.ts 2743-2855 (/vibe). The manager keeps the original's
// module-level state in one struct, so each twin starts from a fresh one; the SDK wiring is in working_vibes_ext.go.

type vibeMode string

const (
	vibeModeGenerate vibeMode = "generate"
	vibeModeFile     vibeMode = "file"
)

// vibeThinkingLevels are the suffixes `provider/model:level` accepts. upstream: working-vibes.ts:17.
var vibeThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// Constants. upstream: working-vibes.ts:62-78.
const (
	defaultVibeModel = "openai-codex/gpt-5.6-luna:low"

	defaultVibePrompt = `Generate a 2-4 word "{theme}" themed loading message ending in "...".

Task: {task}

Be creative and unexpected. Avoid obvious/clichéd phrases for this theme.
The message should hint at the task using theme vocabulary.
{exclude}
Output only the message, nothing else.`

	vibeBatchPrompt = `Generate {count} unique 2-4 word loading messages for a "{theme}" theme.
Each message should end with "..."
Be creative, varied, and thematic. No duplicates.
Output one message per line, nothing else. No numbering, no bullets.`

	vibeSystemPrompt = "You generate short themed loading messages and reply with the requested text only."

	maxRecentVibes = 5
)

// vibeConfig is the original's VibeConfig. A nil theme is "disabled". Durations are milliseconds as JavaScript numbers.
type vibeConfig struct {
	Theme           *string
	Mode            vibeMode
	ModelSpec       string
	Fallback        string
	TimeoutMs       float64
	RefreshInterval float64
	PromptTemplate  string
	MaxLength       int
}

// vibeRegistry is what the manager needs from the host's model registry (the original's extensionCtx.modelRegistry). Find reports
// an error where the original's lookup throws (a stale context); the SDK's Find cannot.
type vibeRegistry interface {
	Find(provider, modelID string) (map[string]any, error)
	// GetApiKeyAndHeaders answers {ok, apiKey, headers, env} or {ok: false, error}.
	GetApiKeyAndHeaders(model map[string]any) (map[string]any, error)
	// GetProviderBaseURL is getProviderAuth(provider)?.auth.baseUrl ("" when the credential carries none).
	GetProviderBaseURL(provider string) (string, error)
	// Complete streams one completion and returns its final assistant message.
	Complete(model, request, options map[string]any) map[string]any
}

// vibeAbort is an AbortController: abort closes the channel once.
type vibeAbort struct {
	once sync.Once
	ch   chan struct{}
}

func newVibeAbort() *vibeAbort { return &vibeAbort{ch: make(chan struct{})} }

func (a *vibeAbort) abort() { a.once.Do(func() { close(a.ch) }) }

func (a *vibeAbort) aborted() bool {
	select {
	case <-a.ch:
		return true
	default:
		return false
	}
}

type vibeManager struct {
	mu sync.Mutex

	config            vibeConfig
	registry          vibeRegistry // nil until initVibeManager (extensionCtx)
	currentGeneration *vibeAbort
	isStreaming       bool
	lastVibeTime      int64
	messageTheme      theme  // workingMessageTheme: nil until the footer has built its context
	messageColor      string // workingMessageColor: "", a theme token, a #rrggbb value or "rainbow"

	vibeCache      []string
	vibeCacheTheme *string
	vibeSeed       int64
	vibeIndex      int
	recentVibes    []string

	now   func() int64
	debug func(args ...any) // console.debug
	wg    sync.WaitGroup    // generations in flight (tests wait for them)
}

func newVibeManager() *vibeManager {
	m := &vibeManager{
		now:   func() int64 { return time.Now().UnixMilli() },
		debug: func(...any) {},
	}
	m.config = m.loadConfig()
	m.vibeSeed = m.now()
	return m
}

// wait blocks until every in-flight generation has finished.
func (m *vibeManager) wait() { m.wg.Wait() }

// Configuration management. upstream: working-vibes.ts:150-275.

func vibeSettingsPath() string { return getAgentPath("settings.json") }

// readVibeSettingsForLoad: a missing, unreadable or non-object file is {}.
func (m *vibeManager) readSettingsForLoad() *jsObject {
	path := vibeSettingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			m.debug("[working-vibes] Failed to load settings from "+path+":", err)
		}
		return newObject()
	}
	v, err := parseJSON(data)
	if err != nil {
		m.debug("[working-vibes] Failed to load settings from "+path+":", err)
		return newObject()
	}
	o, ok := v.(*jsObject)
	if !ok {
		m.debug("[working-vibes] Ignoring non-object settings at " + path)
		return newObject()
	}
	return o
}

// readSettingsForWrite: a missing file is {}; a file that does not parse to an object is never overwritten (nil).
func (m *vibeManager) readSettingsForWrite(scope string) *jsObject {
	path := vibeSettingsPath()
	if _, err := os.Stat(path); err != nil {
		return newObject()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.debug("[working-vibes] Failed to parse settings while writing "+scope+" at "+path+":", err)
		return nil
	}
	v, err := parseJSON(data)
	if err != nil {
		m.debug("[working-vibes] Failed to parse settings while writing "+scope+" at "+path+":", err)
		return nil
	}
	o, ok := v.(*jsObject)
	if !ok {
		m.debug("[working-vibes] Refusing to write " + scope + ": settings at " + path + " is not an object")
		return nil
	}
	return o
}

func (m *vibeManager) persistSettings(settings *jsObject, scope string) bool {
	path := vibeSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.debug("[working-vibes] Failed to persist "+scope+" to "+path+":", err)
		return false
	}
	if err := os.WriteFile(path, []byte(marshalJSON(settings, "  ")+"\n"), 0o644); err != nil {
		m.debug("[working-vibes] Failed to persist "+scope+" to "+path+":", err)
		return false
	}
	return true
}

func deleteObjectKey(o *jsObject, key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

func finiteNumber(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func (m *vibeManager) loadConfig() vibeConfig {
	settings := m.readSettingsForLoad().vals

	// "off" in settings.json is the same as null/disabled.
	var theme *string
	if raw, ok := settings["workingVibe"].(string); ok && strings.ToLower(raw) != "off" {
		theme = &raw
	}

	mode := vibeModeGenerate
	if raw := settings["workingVibeMode"]; raw == "file" || raw == "generate" {
		mode = vibeMode(raw.(string))
	}

	refreshSeconds := 30.0
	if f, ok := finiteNumber(settings["workingVibeRefreshInterval"]); ok {
		refreshSeconds = math.Max(0, f)
	}
	maxLength := 65
	if f, ok := finiteNumber(settings["workingVibeMaxLength"]); ok {
		maxLength = int(math.Max(4, math.Floor(f)))
	}

	cfg := vibeConfig{
		Theme:           theme,
		Mode:            mode,
		ModelSpec:       defaultVibeModel,
		Fallback:        "Working",
		TimeoutMs:       3000,
		RefreshInterval: refreshSeconds * 1000,
		PromptTemplate:  defaultVibePrompt,
		MaxLength:       maxLength,
	}
	if s, ok := settings["workingVibeModel"].(string); ok {
		cfg.ModelSpec = s
	}
	if s, ok := settings["workingVibeFallback"].(string); ok {
		cfg.Fallback = s
	}
	if s, ok := settings["workingVibePrompt"].(string); ok {
		cfg.PromptTemplate = s
	}
	return cfg
}

func (m *vibeManager) saveConfig() bool {
	settings := m.readSettingsForWrite("workingVibe")
	if settings == nil {
		return false
	}
	if m.config.Theme == nil {
		deleteObjectKey(settings, "workingVibe")
	} else {
		settings.set("workingVibe", *m.config.Theme)
	}
	return m.persistSettings(settings, "workingVibe")
}

func (m *vibeManager) saveModelConfig() bool {
	settings := m.readSettingsForWrite("workingVibeModel")
	if settings == nil {
		return false
	}
	if m.config.ModelSpec == defaultVibeModel {
		deleteObjectKey(settings, "workingVibeModel")
	} else {
		settings.set("workingVibeModel", m.config.ModelSpec)
	}
	return m.persistSettings(settings, "workingVibeModel")
}

func (m *vibeManager) saveModeConfig() bool {
	settings := m.readSettingsForWrite("workingVibeMode")
	if settings == nil {
		return false
	}
	if m.config.Mode == vibeModeGenerate {
		deleteObjectKey(settings, "workingVibeMode")
	} else {
		settings.set("workingVibeMode", string(m.config.Mode))
	}
	return m.persistSettings(settings, "workingVibeMode")
}

// Model specs. upstream: working-vibes.ts:24-35, 112-134.

type parsedModelSpec struct{ provider, modelID string }

func parseModelSpec(spec string) *parsedModelSpec {
	slash := strings.Index(spec, "/")
	if slash == -1 {
		return nil
	}
	provider, modelID := spec[:slash], spec[slash+1:]
	if provider == "" || modelID == "" {
		return nil
	}
	return &parsedModelSpec{provider, modelID}
}

type resolvedVibeModel struct {
	provider      string
	model         map[string]any
	thinkingLevel string // "" when the spec has no suffix
}

// resolveModelSpec finds the model, then retries without a trailing `:level` thinking suffix. nil: not found.
func (m *vibeManager) resolveModelSpec(registry vibeRegistry, spec string) (*resolvedVibeModel, error) {
	parsed := parseModelSpec(spec)
	if parsed == nil || registry == nil {
		return nil, nil
	}
	exact, err := registry.Find(parsed.provider, parsed.modelID)
	if err != nil {
		return nil, err
	}
	if exact != nil {
		return &resolvedVibeModel{provider: parsed.provider, model: exact}, nil
	}
	colon := strings.LastIndex(parsed.modelID, ":")
	if colon == -1 {
		return nil, nil
	}
	suffix := parsed.modelID[colon+1:]
	if !slices.Contains(vibeThinkingLevels, suffix) {
		return nil, nil
	}
	model, err := registry.Find(parsed.provider, parsed.modelID[:colon])
	if err != nil || model == nil {
		return nil, err
	}
	return &resolvedVibeModel{provider: parsed.provider, model: model, thinkingLevel: suffix}, nil
}

// completeVibe streams through the provider with the credential's base URL, as ModelRuntime.prepareRequest resolves it, and
// returns the final assistant message. A fired signal ends the wait with an aborted message, as an aborted provider request
// resolves; the SDK has no way to cancel the host's request itself. upstream: working-vibes.ts:41-56.
func completeVibe(registry vibeRegistry, provider string, model map[string]any, request, options map[string]any, signal <-chan struct{}) (map[string]any, error) {
	baseURL, err := registry.GetProviderBaseURL(provider)
	if err != nil {
		return nil, err
	}
	requestModel := model
	if baseURL != "" {
		requestModel = make(map[string]any, len(model)+1)
		for k, v := range model {
			requestModel[k] = v
		}
		requestModel["baseUrl"] = baseURL
	}
	done := make(chan map[string]any, 1)
	go func() { done <- registry.Complete(requestModel, request, options) }()
	select {
	case result := <-done:
		return result, nil
	case <-signal:
		return map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted", "errorMessage": "Request was aborted"}, nil
	}
}

func vibeRequestOptions(auth map[string]any, thinkingLevel string) map[string]any {
	options := map[string]any{}
	for _, key := range []string{"apiKey", "headers", "env"} {
		if v := auth[key]; v != nil {
			options[key] = v
		}
	}
	if thinkingLevel != "" {
		options["reasoning"] = thinkingLevel
	}
	return options
}

// buildAiContext is the model request: the system prompt and one user message. upstream: working-vibes.ts:435-444.
func (m *vibeManager) buildAiContext(prompt string) map[string]any {
	return map[string]any{
		"systemPrompt": vibeSystemPrompt,
		"messages": []any{map[string]any{
			"role":      "user",
			"content":   []any{map[string]any{"type": "text", "text": prompt}},
			"timestamp": m.now(),
		}},
	}
}

// firstText is `response.content.find(c => c.type === "text")?.text || ""`, and whether such a block exists.
func firstText(response map[string]any) string {
	content, _ := response["content"].([]any)
	for _, block := range content {
		b, _ := block.(map[string]any)
		if b["type"] == "text" {
			s, _ := b["text"].(string)
			return s
		}
	}
	return ""
}

// File-based vibes. upstream: working-vibes.ts:281-388.

func vibesDir() string { return getAgentPath("vibes") }

var (
	slugRuns    = regexp.MustCompile(`[^a-z0-9_-]+`)
	slugDashes  = regexp.MustCompile(`-+`)
	slugEnds    = regexp.MustCompile(`^[-_]+|[-_]+$`)
	trailingEll = regexp.MustCompile(`[.\x{2026}]+$`)
)

// jsLower is String.prototype.toLowerCase for the characters that matter here: U+0130 lowercases to "i" plus U+0307 in
// JavaScript (full case mapping) but to "i" in Go.
func jsLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}

func toVibeFileSlug(theme string) string {
	slug := jsLower(jsTrim(theme))
	slug = slugRuns.ReplaceAllString(slug, "-")
	slug = slugDashes.ReplaceAllString(slug, "-")
	slug = slugEnds.ReplaceAllString(slug, "")
	if slug == "" {
		return "theme"
	}
	return slug
}

func vibeFilePath(theme string) string {
	return filepath.Join(vibesDir(), toVibeFileSlug(theme)+".txt")
}

// normalizeEllipsis gives the trailing ellipsis its script-appropriate form: CJK text keeps "……" (the six-dot Chinese and
// Japanese ellipsis), other text gets "...". A trailing run of dots and "…" collapses first, so mixed endings never double up.
// upstream: working-vibes.ts:301-308.
func normalizeEllipsis(text string) string {
	cjk := strings.IndexFunc(text, func(r rune) bool { return unicode.Is(cjkScriptExtensions, r) }) >= 0
	tail := "..."
	if cjk {
		tail = "\u2026\u2026"
	}
	return trailingEll.ReplaceAllString(text, "") + tail
}

func endsWithEllipsis(line string) bool {
	return strings.HasSuffix(line, "...") || strings.HasSuffix(line, "\u2026")
}

func (m *vibeManager) loadVibesFromFile(theme string) []string {
	path := vibeFilePath(theme)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.debug("[working-vibes] Failed to load vibe file "+path+":", err)
		return nil
	}
	var vibes []string
	for _, line := range strings.Split(string([]rune(string(data))), "\n") {
		line = jsTrim(line)
		if !endsWithEllipsis(line) {
			continue
		}
		line = normalizeEllipsis(line)
		if line == "..." || line == "\u2026\u2026" {
			continue
		}
		vibes = append(vibes, line)
	}
	return vibes
}

// nodeFsMessage words a failed file operation as Node does: `EACCES: permission denied, open '/path'`.
func nodeFsMessage(err error, syscallName, path string) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err.Error()
	}
	codes := map[syscall.Errno][2]string{
		syscall.ENOENT:  {"ENOENT", "no such file or directory"},
		syscall.EACCES:  {"EACCES", "permission denied"},
		syscall.EPERM:   {"EPERM", "operation not permitted"},
		syscall.EEXIST:  {"EEXIST", "file already exists"},
		syscall.ENOTDIR: {"ENOTDIR", "not a directory"},
		syscall.EISDIR:  {"EISDIR", "illegal operation on a directory"},
		syscall.ENOSPC:  {"ENOSPC", "no space left on device"},
		syscall.EROFS:   {"EROFS", "read-only file system"},
	}
	c, ok := codes[errno]
	if !ok {
		return err.Error()
	}
	return c[0] + ": " + c[1] + ", " + syscallName + " '" + path + "'"
}

func saveVibesToFile(theme string, vibes []string) error {
	dir, path := vibesDir(), vibeFilePath(theme)
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errors.New(nodeFsMessage(err, "mkdir", dir))
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(vibes, "\n")), 0o644); err != nil {
		return errors.New(nodeFsMessage(err, "open", path))
	}
	return nil
}

// mulberry32 is the original's seeded PRNG, in the uint32 arithmetic JavaScript's bit operators reduce to.
func mulberry32(seed int64) func() float64 {
	s := uint32(seed)
	return func() float64 {
		s += 0x6D2B79F5
		t := s
		t = (t ^ t>>15) * (t | 1)
		t ^= t + (t^t>>7)*(t|61)
		return float64(t^t>>14) / 4294967296
	}
}

// getVibeAtIndex picks a vibe by a seeded Fisher-Yates shuffle (no repeat until all are used).
func (m *vibeManager) vibeAtIndex(vibes []string, index int, seed int64) string {
	if len(vibes) == 0 {
		return normalizeEllipsis(m.config.Fallback)
	}
	effective := index % len(vibes)
	rng := mulberry32(seed)
	indices := make([]int, len(vibes))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := int(math.Floor(rng() * float64(i+1)))
		indices[i], indices[j] = indices[j], indices[i]
	}
	return vibes[indices[effective]]
}

func (m *vibeManager) nextVibeFromFile() string {
	if m.config.Theme == nil || *m.config.Theme == "" {
		return normalizeEllipsis(m.config.Fallback)
	}
	if m.vibeCacheTheme == nil || *m.vibeCacheTheme != *m.config.Theme {
		m.vibeCache = m.loadVibesFromFile(*m.config.Theme)
		m.vibeCacheTheme = new(*m.config.Theme)
		m.vibeSeed = m.now()
		m.vibeIndex = 0
	}
	if len(m.vibeCache) == 0 {
		return normalizeEllipsis(m.config.Fallback)
	}
	vibe := m.vibeAtIndex(m.vibeCache, m.vibeIndex, m.vibeSeed)
	m.vibeIndex++
	return vibe
}

// Prompt building and response parsing. upstream: working-vibes.ts:394-433.

// utf16Head is `s.slice(0, n)` in UTF-16 code units; a cut through a surrogate pair leaves U+FFFD, the only way a Go string
// holds a lone surrogate.
func utf16Head(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if n >= len(u) {
		return s
	}
	return string(utf16.Decode(u[:n]))
}

// jsSubstitute expands the `$` patterns of a String.prototype.replace replacement string for a match without capture groups
// (ES GetSubstitution): `$$`, `$&`, "$`" and `$'`; anything else, `$1` and `$<` included, stays literal.
func jsSubstitute(repl, matched, str string, position int) string {
	if !strings.Contains(repl, "$") {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		if repl[i] != '$' || i+1 >= len(repl) {
			b.WriteByte(repl[i])
			continue
		}
		switch repl[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '&':
			b.WriteString(matched)
			i++
		case '`':
			b.WriteString(str[:position])
			i++
		case '\'':
			b.WriteString(str[position+len(matched):])
			i++
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

// jsReplaceAll is str.replace(/<token>/g, repl) for a literal token.
func jsReplaceAll(str, token, repl string) string {
	var b strings.Builder
	pos := 0
	for {
		i := strings.Index(str[pos:], token)
		if i < 0 {
			break
		}
		i += pos
		b.WriteString(str[pos:i])
		b.WriteString(jsSubstitute(repl, token, str, i))
		pos = i + len(token)
	}
	b.WriteString(str[pos:])
	return b.String()
}

func (m *vibeManager) buildVibePrompt(themeName, userPrompt string) string {
	// Truncate the user prompt to save tokens (most context is in the first 100 chars).
	task := utf16Head(userPrompt, 100)
	exclude := ""
	if len(m.recentVibes) > 0 {
		exclude = "Don't use: " + strings.Join(m.recentVibes, ", ")
	}
	out := jsReplaceAll(m.config.PromptTemplate, "{theme}", themeName)
	out = jsReplaceAll(out, "{task}", task)
	return jsReplaceAll(out, "{exclude}", exclude)
}

func (m *vibeManager) parseVibeResponse(response, fallback string) string {
	if response == "" {
		return normalizeEllipsis(fallback)
	}
	// Only the first line (the model sometimes adds explanations).
	vibe := jsTrim(strings.SplitN(jsTrim(response), "\n", 2)[0])
	// Remove quotes if the model wrapped the response: /^["']|["']$/g.
	if strings.HasPrefix(vibe, `"`) || strings.HasPrefix(vibe, "'") {
		vibe = vibe[1:]
	}
	if strings.HasSuffix(vibe, `"`) || strings.HasSuffix(vibe, "'") {
		vibe = vibe[:len(vibe)-1]
	}
	vibe = normalizeEllipsis(vibe)
	// Enforce the length limit (configurable, default 65).
	if utf16Len(vibe) > m.config.MaxLength {
		vibe = normalizeEllipsis(utf16Head(vibe, m.config.MaxLength-3))
	}
	if vibe == "" || vibe == "..." || vibe == "\u2026\u2026" {
		return normalizeEllipsis(fallback)
	}
	return vibe
}

// AI generation. upstream: working-vibes.ts:450-551.

func (m *vibeManager) trackRecentVibe(vibe string) {
	if vibe == normalizeEllipsis(m.config.Fallback) {
		return // fallback messages are not tracked
	}
	next := []string{vibe}
	for _, v := range m.recentVibes {
		if v != vibe {
			next = append(next, v)
		}
	}
	if len(next) > maxRecentVibes {
		next = next[:maxRecentVibes]
	}
	m.recentVibes = next
}

// setStyledWorkingMessage colors a message with workingVibes.color. An empty message is passed through (undefined resets
// Pi's default). The state is read under the lock; setWorkingMessage is called without it.
func (m *vibeManager) setStyledWorkingMessage(setWorkingMessage func(string), message string) {
	m.mu.Lock()
	color, th := m.messageColor, m.messageTheme
	m.mu.Unlock()
	if message == "" || color == "" || th == nil {
		setWorkingMessage(message)
		return
	}
	if color == "rainbow" {
		setWorkingMessage(rainbow(message))
		return
	}
	setWorkingMessage(applyColor(th, color, message))
}

// generateVibe asks the model for one message. A model, auth or credential failure the original reports through console.debug
// shows the fallback; an error the original lets propagate is returned.
func (m *vibeManager) generateVibe(registry vibeRegistry, themeName, userPrompt, modelSpec string, signal <-chan struct{}) (string, error) {
	m.mu.Lock()
	fallback := m.config.Fallback
	m.mu.Unlock()
	if registry == nil {
		return normalizeEllipsis(fallback), nil
	}
	resolved, err := m.resolveModelSpec(registry, modelSpec)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		m.debug("[working-vibes] Model not found: " + modelSpec)
		return normalizeEllipsis(fallback), nil
	}
	auth, err := registry.GetApiKeyAndHeaders(resolved.model)
	if err != nil {
		return "", err
	}
	if ok, _ := auth["ok"].(bool); !ok {
		m.debug("[working-vibes] Auth failed for "+resolved.provider+":", auth["error"])
		return normalizeEllipsis(fallback), nil
	}
	m.mu.Lock()
	request := m.buildAiContext(m.buildVibePrompt(themeName, userPrompt))
	m.mu.Unlock()
	response, err := completeVibe(registry, resolved.provider, resolved.model, request, vibeRequestOptions(auth, resolved.thinkingLevel), signal)
	if err != nil {
		return "", err
	}
	text := firstText(response)
	if text == "" && response["stopReason"] == "error" {
		if msg, _ := response["errorMessage"].(string); msg != "" {
			m.debug("[working-vibes] Vibe generation failed for " + modelSpec + ": " + msg)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.parseVibeResponse(text, m.config.Fallback), nil
}

// generateAndUpdate: file mode answers at once; generate mode starts a model call (the 3 s timeout and the previous
// generation's abort included) and updates the working message when it returns. The call runs in the background, as the
// original's fire-and-forget promise does.
func (m *vibeManager) generateAndUpdate(prompt string, setWorkingMessage func(string)) {
	m.mu.Lock()
	if m.config.Mode == vibeModeFile {
		message := m.nextVibeFromFile()
		m.mu.Unlock()
		m.setStyledWorkingMessage(setWorkingMessage, message)
		return
	}
	// Cancel any in-flight generation and take a new controller (kept in a local so a later call cannot race it).
	controller := newVibeAbort()
	if m.currentGeneration != nil {
		m.currentGeneration.abort()
	}
	m.currentGeneration = controller
	themeName := ""
	if m.config.Theme != nil {
		themeName = *m.config.Theme
	}
	modelSpec, registry := m.config.ModelSpec, m.registry
	timeout := time.Duration(m.config.TimeoutMs * float64(time.Millisecond))
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		// The combined signal: the controller or the timeout.
		combined := make(chan struct{})
		stop := make(chan struct{})
		defer close(stop)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		go func() {
			select {
			case <-controller.ch:
			case <-timer.C:
			case <-stop:
				return
			}
			close(combined)
		}()

		vibe, err := m.generateVibe(registry, themeName, prompt, modelSpec, combined)
		if err != nil {
			if isStaleExtensionContextError(err) {
				return // an in-flight decorative update outlived its extension context
			}
			m.debug("[working-vibes] Generation failed:", err)
			return // the fallback is already showing
		}
		// Only update if still streaming and THIS generation was not aborted.
		m.mu.Lock()
		if !m.isStreaming || controller.aborted() {
			m.mu.Unlock()
			return
		}
		m.trackRecentVibe(vibe)
		m.mu.Unlock()
		m.setStyledWorkingMessage(setWorkingMessage, vibe)
	}()
}

// Exported functions (called from index.ts). upstream: working-vibes.ts:557-688.

func (m *vibeManager) setWorkingMessageTheme(th theme) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messageTheme = th
}

func (m *vibeManager) setWorkingMessageColor(color string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messageColor = color
}

// initVibeManager takes the registry and refreshes the config in case the settings changed.
func (m *vibeManager) initVibeManager(registry vibeRegistry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry = registry
	m.config = m.loadConfig()
}

func (m *vibeManager) getTheme() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Theme
}

func (m *vibeManager) setTheme(theme *string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Theme = theme
	m.recentVibes = nil // cleared on theme change
	return m.saveConfig()
}

func (m *vibeManager) getModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.ModelSpec
}

func (m *vibeManager) setModel(spec string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.ModelSpec = spec
	return m.saveModelConfig()
}

func (m *vibeManager) getMode() vibeMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Mode
}

func (m *vibeManager) setMode(mode vibeMode) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Mode = mode
	return m.saveModeConfig()
}

// onBeforeAgentStart queues the themed placeholder (applied when the loader is created) and starts generating the real one.
func (m *vibeManager) onBeforeAgentStart(prompt string, setWorkingMessage func(string)) {
	m.mu.Lock()
	if m.config.Theme == nil || *m.config.Theme == "" || m.registry == nil {
		m.mu.Unlock()
		return
	}
	placeholder := normalizeEllipsis("Channeling " + *m.config.Theme)
	m.mu.Unlock()
	m.setStyledWorkingMessage(setWorkingMessage, placeholder)

	m.mu.Lock()
	m.lastVibeTime = m.now() // for rate limiting
	m.mu.Unlock()
	m.generateAndUpdate(prompt, setWorkingMessage)
}

func (m *vibeManager) onAgentStart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isStreaming = true
}

// onToolCall refreshes the vibe when the rate limit allows. agentContext is the recent assistant text ("" when there is none).
func (m *vibeManager) onToolCall(toolName string, toolInput map[string]any, setWorkingMessage func(string), agentContext string) {
	m.mu.Lock()
	if m.config.Theme == nil || *m.config.Theme == "" || m.registry == nil || !m.isStreaming {
		m.mu.Unlock()
		return
	}
	now := m.now()
	if float64(now-m.lastVibeTime) < m.config.RefreshInterval {
		m.mu.Unlock()
		return
	}
	m.lastVibeTime = now
	m.mu.Unlock()

	// Prefer the agent's own text (richer); fall back to a hint from the tool.
	var hint string
	if utf16Len(agentContext) > 10 {
		hint = utf16Head(agentContext, 150)
	} else {
		hint = "using " + toolName + " tool"
		switch {
		case toolName == "read" && jsTruthy(toolInput["path"]):
			hint = "reading file: " + jsString(toolInput["path"])
		case toolName == "write" && jsTruthy(toolInput["path"]):
			hint = "writing file: " + jsString(toolInput["path"])
		case toolName == "edit" && jsTruthy(toolInput["path"]):
			hint = "editing file: " + jsString(toolInput["path"])
		case toolName == "bash" && jsTruthy(toolInput["command"]):
			hint = "running command: " + utf16Head(jsString(toolInput["command"]), 40)
		}
	}
	m.generateAndUpdate(hint, setWorkingMessage)
}

// onAgentEnd cancels the in-flight generation and resets Pi's default working message.
func (m *vibeManager) onAgentEnd(setWorkingMessage func(string)) {
	m.mu.Lock()
	m.isStreaming = false
	if m.currentGeneration != nil {
		m.currentGeneration.abort()
	}
	m.mu.Unlock()
	setWorkingMessage("")
}

func (m *vibeManager) hasVibeFile(theme string) bool {
	_, err := os.Stat(vibeFilePath(theme))
	return err == nil
}

func (m *vibeManager) vibeFileCount(theme string) int { return len(m.loadVibesFromFile(theme)) }

// jsTruthy is JavaScript truthiness of a decoded JSON value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// getRecentAgentContext is the first ~200 chars of the latest assistant text block on the branch (thinking blocks skipped).
// upstream: index.ts getRecentAgentContext.
func getRecentAgentContext(branch []map[string]any) string {
	for i := len(branch) - 1; i >= 0; i-- {
		e := branch[i]
		msg, _ := e["message"].(map[string]any)
		if e["type"] != "message" || msg == nil || msg["role"] != "assistant" {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if text == "" {
				continue
			}
			if text = jsTrim(text); text != "" {
				return utf16Head(text, 200)
			}
		}
	}
	return ""
}

// Batch generation. upstream: working-vibes.ts:691-788.

type generateVibesResult struct {
	Success  bool
	Count    int
	FilePath string
	Error    string
}

// parseVibeGenerateArgs splits `<theme words> [count]`: a trailing integer (with at least one word before it) is the count,
// clamped to 1..500; otherwise every word is the theme and the count is 100.
func parseVibeGenerateArgs(args []string) (theme string, count int, ok bool) {
	if len(args) == 0 {
		return "", 0, false
	}
	last := args[len(args)-1]
	hasCount := false
	parsed := 0.0
	if isDigits(last) {
		// parseInt of a digit string overflows to Infinity, which is not finite.
		if f, err := strconv.ParseFloat(last, 64); err == nil && !math.IsInf(f, 0) {
			parsed, hasCount = f, len(args) > 1
		}
	}
	if hasCount {
		theme = strings.Join(args[:len(args)-1], " ")
	} else {
		theme = strings.Join(args, " ")
	}
	if theme == "" {
		return "", 0, false
	}
	count = 100
	if hasCount {
		count = int(math.Min(math.Max(math.Floor(parsed), 1), 500))
	}
	return theme, count, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// leading is /^["'\d.\-)\s]+/ : quotes, digits, dots, dashes, closing parentheses and whitespace before a vibe.
func stripVibeBullet(line string) string {
	return jsTrim(strings.TrimLeftFunc(line, func(r rune) bool {
		return r == '"' || r == '\'' || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == ')' || isJSSpace(r)
	}))
}

// generateVibesBatch asks the model for count vibes and saves them to the theme's file. The error is a failure the original
// lets propagate (the registry lookup or the credential request rejecting); every other failure is in the result.
func (m *vibeManager) generateVibesBatch(theme string, count int) (generateVibesResult, error) {
	filePath := vibeFilePath(theme)
	safeCount := min(max(count, 1), 500)
	fail := func(msg string) (generateVibesResult, error) {
		return generateVibesResult{FilePath: filePath, Error: msg}, nil
	}

	m.mu.Lock()
	registry, modelSpec := m.registry, m.config.ModelSpec
	m.mu.Unlock()
	if registry == nil {
		return fail("Extension not initialized")
	}
	if parseModelSpec(modelSpec) == nil {
		return fail("Invalid model spec")
	}
	resolved, err := m.resolveModelSpec(registry, modelSpec)
	if err != nil {
		return generateVibesResult{}, err
	}
	if resolved == nil {
		return fail("Model not found: " + modelSpec)
	}
	auth, err := registry.GetApiKeyAndHeaders(resolved.model)
	if err != nil {
		return generateVibesResult{}, err
	}
	if ok, _ := auth["ok"].(bool); !ok {
		if msg, isString := auth["error"].(string); isString {
			return fail(msg)
		}
		return fail("undefined")
	}

	prompt := jsReplaceAll(jsReplaceAll(vibeBatchPrompt, "{theme}", theme), "{count}", strconv.Itoa(safeCount))
	m.mu.Lock()
	request := m.buildAiContext(prompt)
	m.mu.Unlock()

	// A longer timeout for the batch: 30 seconds.
	timeout := make(chan struct{})
	timer := time.AfterFunc(30*time.Second, func() { close(timeout) })
	defer timer.Stop()
	response, err := completeVibe(registry, resolved.provider, resolved.model, request, vibeRequestOptions(auth, resolved.thinkingLevel), timeout)
	if err != nil {
		return fail(err.Error())
	}
	text := firstText(response)
	if text == "" {
		if msg, _ := response["errorMessage"].(string); response["stopReason"] == "error" && msg != "" {
			return fail(msg)
		}
		return fail("Empty response from model")
	}

	var vibes []string
	for _, line := range strings.Split(text, "\n") {
		line = jsTrim(line)
		if line == "" {
			continue
		}
		vibe := stripVibeBullet(line)
		if strings.HasSuffix(vibe, `"`) || strings.HasSuffix(vibe, "'") {
			vibe = vibe[:len(vibe)-1]
		}
		vibe = normalizeEllipsis(vibe)
		if utf16Len(vibe) > 3 && vibe != "..." {
			vibes = append(vibes, vibe)
		}
	}
	if len(vibes) == 0 {
		return fail("No valid vibes generated")
	}
	if err := saveVibesToFile(theme, vibes); err != nil {
		return fail(err.Error())
	}

	// Clear the cache so the next use loads fresh.
	m.mu.Lock()
	if m.vibeCacheTheme != nil && *m.vibeCacheTheme == theme {
		m.vibeCache, m.vibeCacheTheme = nil, nil
	}
	m.mu.Unlock()
	return generateVibesResult{Success: true, Count: len(vibes), FilePath: filePath}, nil
}

// The /vibe command. upstream: index.ts:2743-2855.

const notPersisted = " (not persisted; check settings.json)"

// command runs `/vibe <args>`. The error is a failure the original lets propagate out of the handler.
func (m *vibeManager) command(args string, notify func(message, level string)) error {
	trimmed := jsTrim(args)
	var parts []string
	if trimmed != "" {
		parts = strings.FieldsFunc(trimmed, isJSSpace)
	}
	subcommand := ""
	if len(parts) > 0 {
		subcommand = strings.ToLower(parts[0])
	}

	// No args: show the current status.
	if trimmed == "" {
		theme, mode, model := m.getTheme(), m.getMode(), m.getModel()
		shown := "off"
		if theme != nil && *theme != "" {
			shown = *theme
		}
		status := "Vibe: " + shown + " | Mode: " + string(mode) + " | Model: " + model
		if theme != nil && *theme != "" && mode == vibeModeFile {
			if count := m.vibeFileCount(*theme); count > 0 {
				status += " | File: " + strconv.Itoa(count) + " vibes"
			} else {
				status += " | File: not found"
			}
		}
		notify(status, "info")
		return nil
	}

	switch subcommand {
	case "model": // /vibe model [spec] - show or set the model
		spec := strings.Join(parts[1:], " ")
		if spec == "" {
			notify("Current vibe model: "+m.getModel(), "info")
			return nil
		}
		if !strings.Contains(spec, "/") {
			notify("Invalid model format. Use: provider/modelId (e.g., openai-codex/gpt-5.4-mini)", "error")
			return nil
		}
		if m.setModel(spec) {
			notify("Vibe model set to: "+spec, "info")
		} else {
			notify("Vibe model set to: "+spec+notPersisted, "warning")
		}
		return nil

	case "mode": // /vibe mode [generate|file] - show or set the mode
		newMode := ""
		if len(parts) > 1 {
			newMode = strings.ToLower(parts[1])
		}
		if newMode == "" {
			notify("Current vibe mode: "+string(m.getMode()), "info")
			return nil
		}
		if newMode != "generate" && newMode != "file" {
			notify("Invalid mode. Use: generate or file", "error")
			return nil
		}
		// A vibe file must exist before switching to file mode.
		theme := m.getTheme()
		if newMode == "file" && theme != nil && *theme != "" && !m.hasVibeFile(*theme) {
			notify(`No vibe file for "`+*theme+`". Run /vibe generate `+*theme+" first", "error")
			return nil
		}
		if m.setMode(vibeMode(newMode)) {
			notify("Vibe mode set to: "+newMode, "info")
		} else {
			notify("Vibe mode set to: "+newMode+notPersisted, "warning")
		}
		return nil

	case "generate": // /vibe generate <theme> [count] - generate vibes and save them to a file
		theme, count, ok := parseVibeGenerateArgs(parts[1:])
		if !ok {
			notify("Usage: /vibe generate <theme> [count]", "error")
			return nil
		}
		notify("Generating "+strconv.Itoa(count)+` vibes for "`+theme+`"...`, "info")
		result, err := m.generateVibesBatch(theme, count)
		if err != nil {
			return err
		}
		if result.Success {
			notify("Generated "+strconv.Itoa(result.Count)+` vibes for "`+theme+`" → `+result.FilePath, "info")
		} else {
			notify("Failed to generate vibes: "+result.Error, "error")
		}
		return nil

	case "off": // /vibe off - disable
		if m.setTheme(nil) {
			notify("Vibe disabled", "info")
		} else {
			notify("Vibe disabled"+notPersisted, "warning")
		}
		return nil
	}

	// /vibe <theme> - set the theme (original casing kept).
	theme := trimmed
	persisted := m.setTheme(new(theme))
	if m.getMode() == vibeModeFile && !m.hasVibeFile(theme) {
		suffix := ""
		if !persisted {
			suffix = notPersisted
		}
		notify("Vibe set to: "+theme+" (file mode, but no file found - run /vibe generate "+theme+")"+suffix, "warning")
	} else if persisted {
		notify("Vibe set to: "+theme, "info")
	} else {
		notify("Vibe set to: "+theme+notPersisted, "warning")
	}
	return nil
}
