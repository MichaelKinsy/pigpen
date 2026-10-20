package pi_subagents

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Subagent profiles (src/profiles/profiles.ts): model profiles per provider (quota and quality), the provider model catalog they
// are generated from, and their application to the user's settings.

const defaultProviderModelsMaxAgeDays = 7

// profileHost is what the profile functions need from the session: running the Pi executable and the model registry.
type profileHost struct {
	// exec runs a command; nil means pi.exec is unavailable (probes are skipped).
	exec   func(command string, args []string, cwd string, timeoutMs int) (stdout, stderr string, code int, killed bool, err error)
	models func() []map[string]any
}

type probeResult struct{ Status, Message string }

func isoNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func readJSONObjectFile(path string) (*jsObject, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, msg := jsJSONParse(string(b))
	if msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	o, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("File '%s' must contain a JSON object.", path)
	}
	return o, nil
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(marshalJSON(v, "  ")+"\n"), 0o644)
}

var safePathTokenRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func normalizePathToken(value, label string) (string, error) {
	t := jsTrim(value)
	if t == "" {
		return "", fmt.Errorf("%s is required.", label)
	}
	if !safePathTokenRe.MatchString(t) || t == "." || t == ".." || strings.ContainsAny(t, `/\`) {
		return "", fmt.Errorf("%s must be a safe file name using only letters, numbers, dots, underscores, and hyphens.", label)
	}
	return t, nil
}

func normalizeProfileName(name string) (string, error) {
	t := jsTrim(name)
	return normalizePathToken(strings.TrimSuffix(t, ".json"), "Profile name")
}

func normalizeProviderName(provider string) (string, error) {
	return normalizePathToken(provider, "Provider")
}

func validateSubagentProfile(filePath string, parsed *jsObject) (*jsObject, error) {
	subagents := parsed.obj("subagents")
	if subagents == nil {
		return nil, fmt.Errorf("Profile '%s' must contain a 'subagents' object.", filePath)
	}
	overrides := subagents.obj("agentOverrides")
	if overrides == nil {
		return nil, fmt.Errorf("Profile '%s' must contain 'subagents.agentOverrides' as an object.", filePath)
	}
	for _, name := range overrides.order() {
		o, ok := overrides.vals[name].(*jsObject)
		if !ok {
			return nil, fmt.Errorf("Profile '%s' has invalid override '%s'; expected an object.", filePath, name)
		}
		if m, has := o.vals["model"]; has && o.has("model") {
			if _, isStr := m.(string); !isStr {
				return nil, fmt.Errorf("Profile '%s' has invalid model for '%s'; expected a string.", filePath, name)
			}
		}
		if th, has := o.vals["thinking"]; has && o.has("thinking") {
			if s, isStr := th.(string); !isStr && th != false {
				_ = s
				return nil, fmt.Errorf("Profile '%s' has invalid thinking for '%s'; expected a string or false.", filePath, name)
			}
		}
		if o.has("fallbackModels") {
			return nil, fmt.Errorf("Profile '%s' uses removed field fallbackModels for '%s'; configure one model instead.", filePath, name)
		}
	}
	if db, has := subagents.vals["disableBuiltins"]; has && subagents.has("disableBuiltins") {
		if _, ok := db.(bool); !ok {
			return nil, fmt.Errorf("Profile '%s' has invalid subagents.disableBuiltins; expected a boolean.", filePath)
		}
	}
	return parsed, nil
}

func profilesRootDir() string { return filepath.Join(agentDir(), "profiles", "pi-subagents") }

func ensureProfilesDir() (string, error) {
	dir := profilesRootDir()
	return dir, os.MkdirAll(dir, 0o755)
}

func providerModelsPath(provider string) (string, error) {
	name, err := normalizeProviderName(provider)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(profilesRootDir(), "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".models.json"), nil
}

func resolveProfilePath(name string) (string, error) {
	dir, err := ensureProfilesDir()
	if err != nil {
		return "", err
	}
	n, err := normalizeProfileName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, n+".json"), nil
}

func listSubagentProfiles() ([]string, error) {
	dir, err := ensureProfilesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return localeLess(names[i], names[j]) })
	return names, nil
}

func readSubagentProfile(name string) (string, *jsObject, error) {
	path, err := resolveProfilePath(name)
	if err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return "", nil, fmt.Errorf("Profile not found: %s", name)
	}
	parsed, err := readJSONObjectFile(path)
	if err != nil {
		return "", nil, err
	}
	profile, err := validateSubagentProfile(path, parsed)
	return path, profile, err
}

// applySubagentProfile writes a profile's agent mapping into the user's settings; unrelated subagent settings survive.
func applySubagentProfile(name string) (filePath, settingsPath string, err error) {
	filePath, profile, err := readSubagentProfile(name)
	if err != nil {
		return "", "", err
	}
	settingsPath = filepath.Join(agentDir(), "settings.json")
	settings := newObject()
	if _, serr := os.Stat(settingsPath); serr == nil {
		if settings, err = readJSONObjectFile(settingsPath); err != nil {
			return "", "", err
		}
	}
	existing := settings.obj("subagents")
	if existing == nil {
		existing = newObject()
	}
	// A profile owns the complete agent mapping, but unrelated subagent settings survive profile switches. Machine placement is
	// not a model choice, so an existing pin survives a profile switch too.
	overrides := profile.obj("subagents").obj("agentOverrides").clone()
	if old := existing.obj("agentOverrides"); old != nil {
		for _, n := range old.order() {
			ov, _ := old.vals[n].(*jsObject)
			if ov == nil {
				continue
			}
			machine, isStr := ov.vals["machine"].(string)
			if !isStr {
				continue
			}
			cur, _ := overrides.vals[n].(*jsObject)
			if cur != nil && cur.has("machine") {
				continue
			}
			next := newObject()
			if cur != nil {
				next = cur.clone()
			}
			next.set("machine", machine)
			overrides.set(n, next)
		}
	}
	merged := existing.clone()
	sub := profile.obj("subagents")
	for _, k := range sub.order() {
		merged.set(k, sub.vals[k])
	}
	merged.set("agentOverrides", overrides)
	settings.set("subagents", merged)
	return filePath, settingsPath, writeJSONFile(settingsPath, settings)
}

// --- classification ---

var versionNumberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

func extractVersionScore(id string) float64 {
	best := 0.0
	for _, m := range versionNumberRe.FindAllString(id, -1) {
		if f, err := strconv.ParseFloat(m, 64); err == nil && f > best {
			best = f
		}
	}
	return best
}

var (
	tokenLetterDigitRe = regexp.MustCompile(`([a-z])([0-9])`)
	tokenDigitLetterRe = regexp.MustCompile(`([0-9])([a-z])`)
	tokenSplitRe       = regexp.MustCompile(`[^a-z0-9.]+`)
)

func modelNameTokens(name string) map[string]bool {
	s := strings.ToLower(name)
	s = tokenLetterDigitRe.ReplaceAllString(s, "${1} ${2}")
	s = tokenDigitLetterRe.ReplaceAllString(s, "${1} ${2}")
	out := map[string]bool{}
	for _, t := range tokenSplitRe.Split(s, -1) {
		if t != "" {
			out[t] = true
		}
	}
	return out
}

func anyToken(tokens map[string]bool, names ...string) bool {
	for _, n := range names {
		if tokens[n] {
			return true
		}
	}
	return false
}

func inferProfileBand(name string) int {
	t := modelNameTokens(name)
	switch {
	case anyToken(t, "spark", "flash", "nano", "tiny", "instant"):
		return 0
	case anyToken(t, "mini", "haiku", "small"):
		return 1
	case anyToken(t, "opus", "max", "ultra", "pro"):
		return 4
	case anyToken(t, "sonnet", "turbo", "plus"):
		return 3
	}
	return 2
}

// classInput is ModelClassificationInput; nil pointers are unset.
type classInput struct {
	ID, Name                 string
	Reasoning                *bool
	ContextWindow, MaxTokens *float64
	Cost                     map[string]float64
}

type numericStats struct{ min, max float64 }

type classContext struct{ cost, contextWindow, maxTokens *numericStats }

func combinedCost(cost map[string]float64, keys ...string) *float64 {
	if cost == nil {
		return nil
	}
	sum, n := 0.0, 0
	for _, k := range keys {
		if v, ok := cost[k]; ok && finite(v) {
			sum += v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return &sum
}

func inputCost(c classInput) *float64 {
	return combinedCost(c.Cost, "input", "output", "cacheRead", "cacheWrite")
}

func collectStats(values []*float64) *numericStats {
	var s *numericStats
	for _, v := range values {
		if v == nil || !finite(*v) {
			continue
		}
		if s == nil {
			s = &numericStats{*v, *v}
			continue
		}
		s.min, s.max = math.Min(s.min, *v), math.Max(s.max, *v)
	}
	return s
}

func normalizeStat(v *float64, s *numericStats) *float64 {
	if v == nil || s == nil {
		return nil
	}
	r := 0.5
	if s.max > s.min {
		r = (*v - s.min) / (s.max - s.min)
	}
	return &r
}

func buildClassContext(models []classInput) classContext {
	var costs, ctxs, maxes []*float64
	for _, m := range models {
		costs, ctxs, maxes = append(costs, inputCost(m)), append(ctxs, m.ContextWindow), append(maxes, m.MaxTokens)
	}
	return classContext{collectStats(costs), collectStats(ctxs), collectStats(maxes)}
}

type classification struct {
	ProfileRank           int
	CostTier, QualityTier string
	LatencyTier           string
	RecommendedRoleTier   string
	RecommendedAgents     []string
	Sources               []string
}

func tierOf(v float64, a, b float64, low, mid, high string) string {
	if v <= a {
		return low
	}
	if v <= b {
		return mid
	}
	return high
}

func classifyModel(in classInput, ctx classContext) classification {
	name := jsTrim(in.Name)
	if name == "" {
		name = in.ID
	}
	tokens := modelNameTokens(name)
	band := inferProfileBand(name)
	version := extractVersionScore(in.ID)
	costNorm := normalizeStat(inputCost(in), ctx.cost)
	ctxNorm := normalizeStat(in.ContextWindow, ctx.contextWindow)
	maxNorm := normalizeStat(in.MaxTokens, ctx.maxTokens)
	sources := []string{"heuristic-name"}
	if costNorm != nil || ctxNorm != nil || maxNorm != nil {
		sources = []string{"official-metadata", "heuristic-name"}
	}
	signals := []float64{float64(band) / 4}
	for _, n := range []*float64{ctxNorm, maxNorm} {
		if n != nil {
			signals = append(signals, *n)
		}
	}
	if in.Reasoning != nil {
		if *in.Reasoning {
			signals = append(signals, 1)
		} else {
			signals = append(signals, 0)
		}
	}
	fast := tokens["highspeed"] || tokens["flash"] || tokens["instant"] || tokens["turbo"]
	slow := tokens["pro"] || tokens["ultra"] || tokens["opus"] || tokens["max"]
	sum := 0.0
	for _, s := range signals {
		sum += s
	}
	quality := sum / float64(len(signals))
	if fast {
		quality -= 0.2
	}
	quality = math.Max(0, math.Min(1, quality))
	var cost string
	switch {
	case costNorm != nil:
		cost = tierOf(*costNorm, 0.33, 0.66, "cheap", "medium", "expensive")
	case band == 0:
		cost = "cheap"
	case band >= 3:
		cost = "expensive"
	default:
		cost = "medium"
	}
	qualityTier := tierOf(quality, 0.33, 0.66, "weak", "medium", "strong")
	var latency string
	switch {
	case fast:
		latency = "fast"
	case slow:
		latency = "slow"
	case costNorm != nil:
		latency = tierOf(*costNorm, 0.33, 0.66, "fast", "medium", "slow")
	case band <= 1:
		latency = "fast"
	case band >= 3:
		latency = "slow"
	default:
		latency = "medium"
	}
	role := "cheap"
	if qualityTier == "strong" {
		role = "strong"
	} else if qualityTier == "medium" && cost != "cheap" {
		role = "medium"
	}
	agents := map[string][]string{"cheap": {"scout", "delegate"}, "medium": {"researcher", "reviewer"}, "strong": {"worker", "reviewer", "oracle"}}[role]
	penalty := 0
	if fast {
		penalty = 125
	}
	rank := int(jsRound(quality*100*10)) + int(jsRound(version*25)) - penalty
	return classification{rank, cost, qualityTier, latency, role, agents, sources}
}

var (
	probeAuthRe        = regexp.MustCompile(`(?i)(unauthori[sz]ed|forbidden|api key|auth|billing|credit|quota)`)
	probeUnavailableRe = regexp.MustCompile(`(?i)(not found|unknown model|model unavailable|model disabled|unsupported model|unavailable)`)
)

func resolveProbeStatus(text string, timedOut bool) string {
	switch {
	case timedOut:
		return "timeout"
	case text == "":
		return "error"
	case probeAuthRe.MatchString(text):
		return "auth"
	case probeUnavailableRe.MatchString(text):
		return "unavailable"
	}
	return "error"
}

func (h profileHost) probeModel(fullID string) probeResult {
	if h.exec == nil {
		return probeResult{"skipped", "pi.exec is unavailable in this runtime."}
	}
	stdout, stderr, code, killed, err := h.exec(pigBinary(), []string{"-p", "--model", fullID, "--no-tools", `Reply with exactly "OK".`}, os.TempDir(), 45000)
	if err != nil {
		return probeResult{"error", err.Error()}
	}
	stdout, stderr = jsTrim(stdout), jsTrim(stderr)
	var parts []string
	for _, p := range []string{stderr, stdout} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	combined := jsTrim(strings.Join(parts, "\n"))
	if code == 0 {
		if stdout == "" {
			stdout = "Probe succeeded."
		}
		return probeResult{"ok", stdout}
	}
	if combined == "" {
		combined = fmt.Sprintf("Probe exited with code %d.", code)
	}
	return probeResult{resolveProbeStatus(combined, killed), combined}
}

// --- catalog ---

// catalogModel is ProviderModelCatalogModel.
type catalogModel struct {
	ID, FullID string
	Available  bool
	Name       string
	Reasoning  *bool
	Thinking   []string
	Context    *float64
	MaxTokens  *float64
	Cost       map[string]float64
	Probe      probeResult
	CheckedAt  string
	Derived    classification
	Warnings   []string
}

type providerCatalog struct {
	Provider, RefreshedAt string
	MaxAgeDays            float64
	Sources               []string
	Models                []catalogModel
}

func jsStrings(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

func (m catalogModel) object() *jsObject {
	observed := obj("availableInRegistry", m.Available)
	if m.Name != "" {
		observed.set("name", m.Name)
	}
	if m.Reasoning != nil {
		observed.set("reasoning", *m.Reasoning)
	}
	observed.set("thinkingLevels", jsStrings(m.Thinking))
	if m.Context != nil {
		observed.set("contextWindow", *m.Context)
	}
	if m.MaxTokens != nil {
		observed.set("maxTokens", *m.MaxTokens)
	}
	if m.Cost != nil {
		cost := newObject()
		keys := make([]string, 0, len(m.Cost))
		for k := range m.Cost {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return costKeyOrder(keys[i]) < costKeyOrder(keys[j]) || costKeyOrder(keys[i]) == costKeyOrder(keys[j]) && keys[i] < keys[j]
		})
		for _, k := range keys {
			cost.set(k, m.Cost[k])
		}
		observed.set("cost", cost)
	}
	probe := obj("status", m.Probe.Status, "checkedAt", m.CheckedAt)
	if m.Probe.Message != "" {
		probe.set("message", m.Probe.Message)
	}
	observed.set("probe", probe)
	d := m.Derived
	derived := obj("profileRank", float64(d.ProfileRank), "costTier", d.CostTier, "qualityTier", d.QualityTier, "latencyTier", d.LatencyTier,
		"recommendedRoleTier", d.RecommendedRoleTier, "recommendedAgents", jsStrings(d.RecommendedAgents), "classificationSources", jsStrings(d.Sources))
	return obj("id", m.ID, "fullId", m.FullID, "observed", observed, "derived", derived, "warnings", jsStrings(m.Warnings), "notes", []any{})
}

func costKeyOrder(k string) int {
	for i, n := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		if k == n {
			return i
		}
	}
	return 4
}

func (c *providerCatalog) object() *jsObject {
	models := make([]any, len(c.Models))
	for i, m := range c.Models {
		models[i] = m.object()
	}
	return obj("provider", c.Provider, "refreshedAt", c.RefreshedAt, "maxAgeDays", c.MaxAgeDays, "sources", jsStrings(c.Sources), "models", models)
}

func parseProviderCatalog(o *jsObject) *providerCatalog {
	c := &providerCatalog{MaxAgeDays: defaultProviderModelsMaxAgeDays}
	c.Provider, _ = o.str("provider")
	c.RefreshedAt, _ = o.str("refreshedAt")
	if f, ok := o.num("maxAgeDays"); ok {
		c.MaxAgeDays = f
	}
	c.Sources = stringsOf(o.vals["sources"])
	models, _ := o.vals["models"].([]any)
	for _, mv := range models {
		mo, ok := mv.(*jsObject)
		if !ok {
			continue
		}
		m := catalogModel{}
		m.ID, _ = mo.str("id")
		m.FullID, _ = mo.str("fullId")
		if ob := mo.obj("observed"); ob != nil {
			m.Available, _ = ob.flag("availableInRegistry")
			m.Name, _ = ob.str("name")
			if b, ok := ob.flag("reasoning"); ok {
				m.Reasoning = &b
			}
			m.Thinking = stringsOf(ob.vals["thinkingLevels"])
			if f, ok := ob.num("contextWindow"); ok {
				m.Context = &f
			}
			if f, ok := ob.num("maxTokens"); ok {
				m.MaxTokens = &f
			}
			if co := ob.obj("cost"); co != nil {
				m.Cost = map[string]float64{}
				for _, k := range co.order() {
					if f, ok := co.num(k); ok {
						m.Cost[k] = f
					}
				}
			}
			if p := ob.obj("probe"); p != nil {
				m.Probe.Status, _ = p.str("status")
				m.Probe.Message, _ = p.str("message")
				m.CheckedAt, _ = p.str("checkedAt")
			}
		}
		if d := mo.obj("derived"); d != nil {
			f, _ := d.num("profileRank")
			m.Derived.ProfileRank = int(f)
			m.Derived.CostTier, _ = d.str("costTier")
			m.Derived.QualityTier, _ = d.str("qualityTier")
			m.Derived.LatencyTier, _ = d.str("latencyTier")
			m.Derived.RecommendedRoleTier, _ = d.str("recommendedRoleTier")
			m.Derived.RecommendedAgents = stringsOf(d.vals["recommendedAgents"])
			m.Derived.Sources = stringsOf(d.vals["classificationSources"])
		}
		m.Warnings = stringsOf(mo.vals["warnings"])
		c.Models = append(c.Models, m)
	}
	return c
}

func readProviderModelCatalog(provider string) (*providerCatalog, error) {
	path, err := providerModelsPath(provider)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	o, err := readJSONObjectFile(path)
	if err != nil {
		return nil, err
	}
	return parseProviderCatalog(o), nil
}

func isProviderModelCatalogStale(c *providerCatalog, maxAgeDays float64) bool {
	t, err := time.Parse(time.RFC3339Nano, c.RefreshedAt)
	if err != nil {
		return true
	}
	return float64(time.Since(t).Milliseconds()) > maxAgeDays*24*60*60*1000
}

func (m catalogModel) usesHeuristic() bool {
	has := func(s string) bool {
		for _, x := range m.Derived.Sources {
			if x == s {
				return true
			}
		}
		return false
	}
	return has("heuristic-name") && !has("official-metadata")
}

func (c *providerCatalog) heuristicFallbackCount() int {
	n := 0
	for _, m := range c.Models {
		if m.usesHeuristic() {
			n++
		}
	}
	return n
}

type refreshOptions struct {
	Force      bool
	MaxAgeDays *float64
	NoProbe    bool
}

type refreshResult struct {
	FilePath               string
	Catalog                *providerCatalog
	Reused                 bool
	HeuristicFallbackCount int
}

func rawFloat(m map[string]any, k string) *float64 {
	if f, ok := m[k].(float64); ok {
		return &f
	}
	return nil
}

func classInputOf(m map[string]any) classInput {
	in := classInput{}
	in.ID, _ = m["id"].(string)
	in.Name, _ = m["name"].(string)
	if b, ok := m["reasoning"].(bool); ok {
		in.Reasoning = &b
	}
	in.ContextWindow, in.MaxTokens = rawFloat(m, "contextWindow"), rawFloat(m, "maxTokens")
	if co, ok := m["cost"].(map[string]any); ok {
		in.Cost = map[string]float64{}
		for k, v := range co {
			if f, ok := v.(float64); ok {
				in.Cost[k] = f
			}
		}
	}
	return in
}

func (h profileHost) refreshProviderModelCatalog(provider string, opts refreshOptions) (*refreshResult, error) {
	name, err := normalizeProviderName(provider)
	if err != nil {
		return nil, err
	}
	maxAge := float64(defaultProviderModelsMaxAgeDays)
	if opts.MaxAgeDays != nil {
		maxAge = *opts.MaxAgeDays
	}
	filePath, err := providerModelsPath(name)
	if err != nil {
		return nil, err
	}
	if !opts.Force {
		if existing, err := readProviderModelCatalog(name); err != nil {
			return nil, err
		} else if existing != nil && !isProviderModelCatalogStale(existing, maxAge) {
			return &refreshResult{filePath, existing, true, existing.heuristicFallbackCount()}, nil
		}
	}
	var available []map[string]any
	for _, m := range h.models() {
		if p, _ := m["provider"].(string); p == name {
			available = append(available, m)
		}
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("No models found in the current registry for provider '%s'.", name)
	}
	probes := make([]probeResult, len(available))
	for i, m := range available {
		id, _ := m["id"].(string)
		if opts.NoProbe {
			probes[i] = probeResult{"skipped", "Live probing disabled."}
		} else {
			probes[i] = h.probeModel(name + "/" + id)
		}
	}
	inputs := make([]classInput, len(available))
	for i, m := range available {
		inputs[i] = classInputOf(m)
	}
	cctx := buildClassContext(inputs)
	catalog := &providerCatalog{Provider: name, MaxAgeDays: maxAge, Sources: []string{"runtime-registry"}}
	if !opts.NoProbe {
		catalog.Sources = append(catalog.Sources, "live-probe")
	}
	catalog.Sources = append(catalog.Sources, "heuristic-classifier")
	for i, m := range available {
		in := inputs[i]
		d := classifyModel(in, cctx)
		cm := catalogModel{ID: in.ID, FullID: name + "/" + in.ID, Available: true, Name: in.Name, Reasoning: in.Reasoning,
			Thinking: getSupportedThinkingLevels(ptrModelInfo(modelInfoFromRegistry(m))), Context: in.ContextWindow, MaxTokens: in.MaxTokens, Cost: in.Cost,
			Probe: probes[i], CheckedAt: isoNow(), Derived: d, Warnings: []string{}}
		if cm.usesHeuristic() {
			cm.Warnings = []string{"Classification fell back to name heuristics."}
		}
		catalog.Models = append(catalog.Models, cm)
	}
	sort.SliceStable(catalog.Models, func(i, j int) bool {
		a, b := catalog.Models[i], catalog.Models[j]
		if a.Derived.ProfileRank != b.Derived.ProfileRank {
			return a.Derived.ProfileRank < b.Derived.ProfileRank
		}
		return localeLess(a.FullID, b.FullID)
	})
	catalog.RefreshedAt = isoNow()
	if err := writeJSONFile(filePath, catalog.object()); err != nil {
		return nil, err
	}
	return &refreshResult{filePath, catalog, false, catalog.heuristicFallbackCount()}, nil
}

func ptrModelInfo(m ModelInfo) *ModelInfo { return &m }

// --- generation ---

func (m catalogModel) usable() bool {
	switch m.Probe.Status {
	case "unavailable", "auth", "timeout", "error":
		return false
	}
	return m.Available
}

func (m catalogModel) observedCost() *float64 {
	return combinedCost(m.Cost, "input", "output", "cacheRead", "cacheWrite")
}

func boolInt(b *bool) int {
	if b != nil && *b {
		return 1
	}
	return 0
}

func derefOr0(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func dominates(a, b catalogModel) bool {
	ca, cb := a.observedCost(), b.observedCost()
	if ca == nil || cb == nil || *ca > *cb || a.Derived.ProfileRank < b.Derived.ProfileRank || boolInt(a.Reasoning) < boolInt(b.Reasoning) ||
		derefOr0(a.Context) < derefOr0(b.Context) || derefOr0(a.MaxTokens) < derefOr0(b.MaxTokens) {
		return false
	}
	return *ca < *cb || a.Derived.ProfileRank > b.Derived.ProfileRank || (a.Reasoning != nil && *a.Reasoning && (b.Reasoning == nil || !*b.Reasoning)) ||
		derefOr0(a.Context) > derefOr0(b.Context) || derefOr0(a.MaxTokens) > derefOr0(b.MaxTokens)
}

func filterDominated(models []catalogModel) []catalogModel {
	var out []catalogModel
	for i, c := range models {
		dominated := false
		for j, o := range models {
			if i != j && dominates(o, c) {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, c)
		}
	}
	return out
}

func roundIndex(count int, position float64) int {
	if count <= 1 {
		return 0
	}
	return int(math.Max(0, math.Min(float64(count-1), jsRound(float64(count-1)*position))))
}

type tierModels struct{ Cheap, Medium, Strong string }

func pickTierModels(models []catalogModel, quota bool) (tierModels, error) {
	if len(models) == 0 {
		return tierModels{}, fmt.Errorf("No provider models are available for profile generation.")
	}
	pool := models
	if quota && len(models) > 1 {
		pool = models[:len(models)-1]
	}
	cheap, medium, strong := 1.0/3, 2.0/3, 1.0
	if quota {
		cheap, medium, strong = 0, 1.0/3, 2.0/3
	}
	return tierModels{pool[roundIndex(len(pool), cheap)].FullID, pool[roundIndex(len(pool), medium)].FullID, pool[roundIndex(len(pool), strong)].FullID}, nil
}

func buildProfileFile(m tierModels) *jsObject {
	model := func(id string) *jsObject { return obj("model", id) }
	return obj("subagents", obj("agentOverrides", obj("scout", model(m.Cheap), "delegate", model(m.Cheap), "researcher", model(m.Medium),
		"worker", model(m.Strong), "reviewer", model(m.Strong), "oracle", model(m.Strong))))
}

type generateResult struct {
	QuotaPath, QualityPath, CatalogPath                    string
	QuotaModels, QualityModels                             tierModels
	HeuristicFallbackCount, SelectedHeuristicFallbackCount int
}

func (h profileHost) generateProfilesForProvider(provider string, opts refreshOptions) (*generateResult, error) {
	name, err := normalizeProviderName(provider)
	if err != nil {
		return nil, err
	}
	r, err := h.refreshProviderModelCatalog(name, opts)
	if err != nil {
		return nil, err
	}
	var usable []catalogModel
	for _, m := range r.Catalog.Models {
		if m.usable() {
			usable = append(usable, m)
		}
	}
	models := filterDominated(usable)
	if len(models) == 0 {
		return nil, fmt.Errorf("Provider '%s' has no usable models after filtering.", name)
	}
	quota, err := pickTierModels(models, true)
	if err != nil {
		return nil, err
	}
	quality, _ := pickTierModels(models, false)
	dir, err := ensureProfilesDir()
	if err != nil {
		return nil, err
	}
	res := &generateResult{QuotaPath: filepath.Join(dir, name+".quota.json"), QualityPath: filepath.Join(dir, name+".quality.json"), CatalogPath: r.FilePath,
		QuotaModels: quota, QualityModels: quality, HeuristicFallbackCount: r.HeuristicFallbackCount}
	if err := writeJSONFile(res.QuotaPath, buildProfileFile(quota)); err != nil {
		return nil, err
	}
	if err := writeJSONFile(res.QualityPath, buildProfileFile(quality)); err != nil {
		return nil, err
	}
	selected := map[string]bool{quota.Cheap: true, quota.Medium: true, quota.Strong: true, quality.Cheap: true, quality.Medium: true, quality.Strong: true}
	for _, m := range models {
		if selected[m.FullID] && m.usesHeuristic() {
			res.SelectedHeuristicFallbackCount++
		}
	}
	return res, nil
}

// --- check ---

type profileCheckEntry struct {
	Agent, Model string
	InRegistry   bool
	Probe        probeResult
}

type profileCheckResult struct {
	ProfileName, FilePath string
	Results               []profileCheckEntry
}

func (h profileHost) checkSubagentProfile(name string) (*profileCheckResult, error) {
	filePath, profile, err := readSubagentProfile(name)
	if err != nil {
		return nil, err
	}
	var available []ModelInfo
	for _, m := range h.models() {
		available = append(available, modelInfoFromRegistry(m))
	}
	overrides := profile.obj("subagents").obj("agentOverrides")
	cache := map[string]probeResult{}
	res := &profileCheckResult{ProfileName: name, FilePath: filePath, Results: []profileCheckEntry{}}
	for _, agent := range overrides.order() {
		o, _ := overrides.vals[agent].(*jsObject)
		model, _ := o.str("model")
		if o == nil || jsTrim(model) == "" {
			continue
		}
		model = jsTrim(model)
		info := findModelInfo(model, available, "")
		_, suffix := splitKnownThinkingSuffix(model)
		probeID := model
		if info != nil {
			probeID = info.FullID + suffix
		}
		probe, ok := cache[probeID]
		if !ok {
			probe = h.probeModel(probeID)
			cache[probeID] = probe
		}
		res.Results = append(res.Results, profileCheckEntry{agent, model, info != nil, probe})
	}
	return res, nil
}
