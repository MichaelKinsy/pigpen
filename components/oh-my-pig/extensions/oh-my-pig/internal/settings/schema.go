package settings

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed schema.json
var schemaJSON []byte

// ValueType is a setting's declared type in omp's registry.
// upstream: packages/coding-agent/src/config/registry.ts SettingDefinition["type"]
type ValueType string

const (
	TypeBoolean ValueType = "boolean"
	TypeString  ValueType = "string"
	TypeNumber  ValueType = "number"
	TypeEnum    ValueType = "enum"
	TypeArray   ValueType = "array"
	TypeRecord  ValueType = "record"
)

// ArrayItemsSpec is the closed vocabulary for an array setting's entries.
// upstream: packages/coding-agent/src/config/registry.ts ArrayDefinition.items
type ArrayItemsSpec struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
}

// PathScopedSpec marks an array setting whose entries may scope values by path prefix.
// upstream: packages/coding-agent/src/config/registry.ts DefinitionBase.pathScoped
type PathScopedSpec struct {
	ValuesKey string `json:"valuesKey"`
	ItemType  string `json:"itemType,omitempty"`
}

// Entry is one setting in omp's schema (generated from v19.1.1).
// upstream: packages/coding-agent/src/config/registry.ts SettingDefinition
type Entry struct {
	ID          string          `json:"id"`
	Type        ValueType       `json:"type"`
	Default     any             `json:"default,omitempty"`
	HasDefault  bool            `json:"hasDefault"`
	Enum        []string        `json:"enum,omitempty"`
	Description string          `json:"description,omitempty"`
	EnvName     string          `json:"envName,omitempty"`
	EnvFallback string          `json:"envFallback,omitempty"` // "", "true", "blank"
	Credential  bool            `json:"credential,omitempty"`
	ArrayMerge  string          `json:"arrayMerge,omitempty"` // "", "replace", "prepend", "append"
	ArrayItems  *ArrayItemsSpec `json:"arrayItems,omitempty"`
	PathScoped  *PathScopedSpec `json:"pathScoped,omitempty"`
	Honoured    bool            `json:"honoured,omitempty"`

	// Segments is ID split on ".". Populated on schema load.
	Segments []string `json:"-"`
}

var (
	schemaOnce      sync.Once
	schemaAll       []Entry
	schemaSorted    []*Entry
	schemaByID      map[string]*Entry
	schemaByLowerID map[string]*Entry
	groupOnlyPfx    map[string]bool
)

func loadSchema() {
	schemaOnce.Do(func() {
		// Decode schema.json using ordered decoding so default object keys
		// (e.g. bashInterceptor.patterns `{pattern, tool, message}`) keep exact
		// upstream key order (fixes A2).
		rawAny, err := decodeOrderedJSON(string(schemaJSON))
		if err != nil {
			panic(fmt.Sprintf("settings: invalid embedded schema.json: %v", err))
		}
		rawList, ok := rawAny.([]any)
		if !ok {
			panic("settings: embedded schema.json root must be an array")
		}
		dec := json.NewDecoder(bytes.NewReader(schemaJSON))
		dec.UseNumber()
		var raw []Entry
		if err := dec.Decode(&raw); err != nil {
			panic(fmt.Sprintf("settings: invalid embedded schema.json: %v", err))
		}
		schemaAll = make([]Entry, len(raw))
		schemaByID = make(map[string]*Entry, len(raw))
		schemaByLowerID = make(map[string]*Entry, len(raw))
		for i := range raw {
			e := raw[i]
			if i < len(rawList) {
				if om, ok := rawList[i].(*OrderedMap); ok {
					if defVal, has := om.Get("default"); has {
						e.Default = defVal
					}
				}
			}
			if e.Default != nil {
				e.Default = normalizeOrderedNumbers(e.Default)
			}
			e.Segments = strings.Split(e.ID, ".")
			schemaAll[i] = e
		}
		schemaSorted = make([]*Entry, len(schemaAll))
		for i := range schemaAll {
			p := &schemaAll[i]
			schemaByID[p.ID] = p
			schemaByLowerID[strings.ToLower(p.ID)] = p
			schemaSorted[i] = p
		}
		// upstream: cfg-protocol.ts allSettings() — segment-by-segment case-insensitive sort
		sort.Slice(schemaSorted, func(i, j int) bool {
			left := schemaSorted[i].Segments
			right := schemaSorted[j].Segments
			n := min(len(left), len(right))
			for k := range n {
				al := strings.ToLower(left[k])
				bl := strings.ToLower(right[k])
				if al != bl {
					return al < bl
				}
			}
			return len(left) < len(right)
		})
		// upstream: settings.ts settingsGroupOnlyPrefixes()
		groupOnlyPfx = make(map[string]bool)
		for i := range schemaAll {
			id := schemaAll[i].ID
			for dot := strings.IndexByte(id, '.'); dot != -1; {
				groupOnlyPfx[id[:dot]] = true
				next := strings.IndexByte(id[dot+1:], '.')
				if next == -1 {
					break
				}
				dot = dot + 1 + next
			}
		}
		for i := range schemaAll {
			delete(groupOnlyPfx, schemaAll[i].ID)
		}
	})
}

func normalizeOrderedNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case uint:
		return float64(x)
	case uint64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeOrderedNumbers(item)
		}
		return out
	case *OrderedMap:
		out := &OrderedMap{
			Keys:   append([]string(nil), x.Keys...),
			Values: make(map[string]any, len(x.Keys)),
		}
		for _, k := range x.Keys {
			out.Values[k] = normalizeOrderedNumbers(x.Values[k])
		}
		return out
	case map[string]any:
		return toOrderedValue(x)
	default:
		return v
	}
}

// Schema returns a copy of all setting entries in upstream declaration order.
func Schema() []Entry {
	loadSchema()
	out := make([]Entry, len(schemaAll))
	copy(out, schemaAll)
	return out
}

// SortedSchema returns pointers to all schema entries in segment-sorted order
// (as cfg:// displays them).
func SortedSchema() []*Entry {
	loadSchema()
	out := make([]*Entry, len(schemaSorted))
	copy(out, schemaSorted)
	return out
}

// Lookup returns the schema entry for an exact setting id.
func Lookup(id string) (*Entry, bool) {
	loadSchema()
	e, ok := schemaByID[id]
	return e, ok
}

// LookupFold returns the schema entry matching id case-insensitively.
func LookupFold(id string) (*Entry, bool) {
	loadSchema()
	e, ok := schemaByLowerID[strings.ToLower(id)]
	return e, ok
}

// NamespaceCounts returns the count of settings per top-level namespace
// ("(top-level)" for un-dotted keys).
func NamespaceCounts() map[string]int {
	loadSchema()
	counts := make(map[string]int)
	for i := range schemaAll {
		id := schemaAll[i].ID
		ns := "(top-level)"
		if dot := strings.IndexByte(id, '.'); dot >= 0 {
			ns = id[:dot]
		}
		counts[ns]++
	}
	return counts
}

// DefaultClone returns a deep copy of e's default value (or nil when unset).
func (e *Entry) DefaultClone() any {
	if !e.HasDefault {
		return nil
	}
	return deepCloneOrdered(e.Default)
}

// Accepts reports whether value conforms to e's declared type and enum values.
// upstream: packages/coding-agent/src/config/registry.ts Setting.accepts
func (e *Entry) Accepts(value any) bool {
	if value == nil {
		return false
	}
	switch e.Type {
	case TypeBoolean:
		_, ok := value.(bool)
		return ok
	case TypeNumber:
		f, ok := asFloat64(value)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0)
	case TypeString:
		_, ok := value.(string)
		return ok
	case TypeEnum:
		s, ok := value.(string)
		if !ok {
			return false
		}
		for _, v := range e.Enum {
			if v == s {
				return true
			}
		}
		return false
	case TypeArray:
		_, ok := value.([]any)
		if ok {
			return true
		}
		_, ok = value.([]string)
		return ok
	case TypeRecord:
		if _, ok := value.(*OrderedMap); ok {
			return true
		}
		if _, ok := value.(map[string]any); ok {
			return true
		}
		_, ok := value.(map[string]string)
		return ok
	default:
		return false
	}
}

// Canonicalize converts Go-typed slices/maps/ints into the store's canonical
// ordered representation (float64, []any, *OrderedMap) and applies setting-specific
// normalizers.
func (e *Entry) Canonicalize(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	v := toOrderedValue(value)
	switch e.ID {
	case "tui.spinnerInterval":
		n, err := normalizeSpinnerInterval(v)
		if err != nil {
			return nil, err
		}
		return float64(n), nil
	case "tui.maxFps":
		n, err := normalizeMaxFps(v)
		if err != nil {
			return nil, err
		}
		return float64(n), nil
	case "providers.maxInFlightRequests":
		return normalizeMaxInFlightRequests(v)
	case "systemPromptTemplates":
		return validateSystemPromptTemplates(v)
	}
	return v, nil
}

func canonicalValue(value any) any {
	switch x := value.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case float32:
		return float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = canonicalValue(item)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, s := range x {
			out[k] = s
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = canonicalValue(item)
		}
		return out
	default:
		return value
	}
}

// ValidateWrite checks that value is valid for writing to this setting.
// upstream: packages/coding-agent/src/config/registry.ts Setting.assertWritable
func (e *Entry) ValidateWrite(value any) error {
	if value == nil {
		return nil
	}
	v := toOrderedValue(value)
	if e.Type == TypeArray && e.ArrayItems != nil {
		if arr, ok := v.([]any); ok {
			allowed := make(map[string]bool, len(e.ArrayItems.Values))
			for _, a := range e.ArrayItems.Values {
				allowed[a] = true
			}
			var unknown []string
			seen := make(map[string]bool)
			for _, item := range arr {
				s, isStr := item.(string)
				if !isStr || !allowed[s] {
					formatted := fmt.Sprint(item)
					if isStr {
						b, _ := json.Marshal(s)
						formatted = string(b)
					}
					if !seen[formatted] {
						seen[formatted] = true
						unknown = append(unknown, formatted)
					}
				}
			}
			if len(unknown) > 0 {
				noun := e.ArrayItems.Label
				if len(unknown) != 1 {
					noun += "s"
				}
				return fmt.Errorf("Unknown %s: %s. Valid %ss: %s",
					noun, strings.Join(unknown, ", "), e.ArrayItems.Label, strings.Join(e.ArrayItems.Values, ", "))
			}
		}
	}
	if err := e.validateCustom(v); err != nil {
		return err
	}
	if !e.Accepts(v) {
		expected := "a " + string(e.Type)
		if len(e.Enum) > 0 {
			expected = "one of " + strings.Join(e.Enum, ", ")
		}
		return fmt.Errorf("Invalid value for %s: %v (expected %s)", e.ID, v, expected)
	}
	return nil
}

// ValidateConfigured runs setting-specific validation on loaded config values
// (like omp's Setting.checkConfigured at config/registry.ts:731).
func (e *Entry) ValidateConfigured(raw any) error {
	if raw == nil {
		return nil
	}
	return e.validateCustom(toOrderedValue(raw))
}

func (e *Entry) validateCustom(v any) error {
	switch e.ID {
	case "find.hitThreshold":
		f, ok := asFloat64(v)
		if !ok || !(f > 0 && f <= 1) {
			return fmt.Errorf("Invalid find.hitThreshold: expected a number above 0, up to 1, got %v.", v)
		}
	case "session.warnCost", "session.maxCost":
		f, ok := asFloat64(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			return fmt.Errorf("Invalid %s: expected a positive USD amount, got %v.", e.ID, v)
		}
	case "judgment.attemptTimeoutMs":
		f, ok := asFloat64(v)
		if !ok || math.Trunc(f) != f || f <= 0 {
			return fmt.Errorf("judgment.attemptTimeoutMs must be a positive safe integer")
		}
	case "task.workerThreads":
		f, ok := asFloat64(v)
		if !ok || math.Trunc(f) != f || f < 0 {
			return fmt.Errorf("Invalid task.workerThreads: expected a non-negative integer (0 runs subagents on the main thread), got %v.", v)
		}
	case "tools.restrictedFolders":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("tools.restrictedFolders must be a list of folder paths")
		}
		for _, item := range arr {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("tools.restrictedFolders must be a list of folder paths")
			}
		}
	case "tui.spinnerInterval":
		_, err := normalizeSpinnerInterval(v)
		return err
	case "tui.maxFps":
		_, err := normalizeMaxFps(v)
		return err
	case "providers.maxInFlightRequests":
		_, err := normalizeMaxInFlightRequests(v)
		return err
	case "systemPromptTemplates":
		_, err := validateSystemPromptTemplates(v)
		return err
	case "live.endpoint":
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			u, err := url.Parse(strings.TrimSpace(s))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				b, _ := json.Marshal(s)
				return fmt.Errorf("live.endpoint must be an http(s) URL, got %s", string(b))
			}
		}
	case "spotlight.experiment.holdout", "spotlight.experiment.scoreFree":
		// upstream: packages/coding-agent/src/spotlight/settings.ts:68 ("from 0 to 1", fixes A6)
		f, ok := asFloat64(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			return fmt.Errorf("Invalid %s: expected a number from 0 to 1, got %v.", e.ID, v)
		}
	case "task.perCallModelAllow":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("Invalid task.perCallModelAllow: expected a list of provider/model-id strings, got %v.", v)
		}
		for i, item := range arr {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("Invalid task.perCallModelAllow[%d]: expected a provider/model-id string, got %v.", i, item)
			}
		}
	}
	return nil
}

func normalizeSpinnerInterval(v any) (int, error) {
	f, ok := parseNumberOrString(v)
	if ok && math.Trunc(f) == f && (f == 0 || f >= 50) {
		return int(f), nil
	}
	b, _ := json.Marshal(v)
	return 0, fmt.Errorf("Spinner interval must be 0 (static) or a whole number of milliseconds >= 50, got %s.", string(b))
}

func normalizeMaxFps(v any) (int, error) {
	f, ok := parseNumberOrString(v)
	if ok && math.Trunc(f) == f && f >= 1 && f <= 120 {
		return int(f), nil
	}
	b, _ := json.Marshal(v)
	return 0, fmt.Errorf("Max frames per second must be a whole number from 1 to 120, got %s.", string(b))
}

func normalizeMaxInFlightRequests(v any) (any, error) {
	om, ok := v.(*OrderedMap)
	if !ok {
		return v, nil
	}
	var bad []string
	out := NewOrderedMap()
	for _, k := range om.Keys {
		val := om.Values[k]
		f, ok := asFloat64(val)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			bad = append(bad, k)
			continue
		}
		out.Set(k, math.Floor(f))
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("Provider request limits must be positive numbers: %s", strings.Join(bad, ", "))
	}
	return out, nil
}

func validateSystemPromptTemplates(v any) (any, error) {
	om, ok := v.(*OrderedMap)
	if !ok {
		return v, nil
	}
	var blankPatterns, invalidPaths []string
	for _, pattern := range om.Keys {
		raw := om.Values[pattern]
		if strings.TrimSpace(pattern) == "" {
			b, _ := json.Marshal(pattern)
			blankPatterns = append(blankPatterns, string(b))
			continue
		}
		s, ok := raw.(string)
		if !ok || (!strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~/") && !(len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/'))) {
			invalidPaths = append(invalidPaths, pattern)
		}
	}
	var problems []string
	if len(blankPatterns) > 0 {
		problems = append(problems, "model patterns must not be blank (use SYSTEM.md or APPEND_SYSTEM.md for every model): "+strings.Join(blankPatterns, ", "))
	}
	if len(invalidPaths) > 0 {
		problems = append(problems, "template paths must be absolute or start with ~/: "+strings.Join(invalidPaths, ", "))
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("systemPromptTemplates %s", strings.Join(problems, "; "))
	}
	return om, nil
}

func parseNumberOrString(v any) (float64, bool) {
	if f, ok := asFloat64(v); ok {
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	if s, ok := v.(string); ok {
		return parseJSNumber(s)
	}
	return 0, false
}

// parseJSNumber parses text following JavaScript Number(trimmed) for finite
// numbers: decimal integers/floats, hex (0x...), binary (0b...), octal (0o...).
// Empty string is rejected (matches config/registry.ts:623, fixes A5).
func parseJSNumber(text string) (float64, bool) {
	t := strings.TrimSpace(text)
	if t == "" || strings.ContainsRune(t, '_') {
		return 0, false
	}
	if len(t) > 2 && t[0] == '0' {
		switch t[1] {
		case 'x', 'X':
			u, err := strconv.ParseUint(t[2:], 16, 64)
			if err != nil {
				return 0, false
			}
			return float64(u), true
		case 'b', 'B':
			u, err := strconv.ParseUint(t[2:], 2, 64)
			if err != nil {
				return 0, false
			}
			return float64(u), true
		case 'o', 'O':
			u, err := strconv.ParseUint(t[2:], 8, 64)
			if err != nil {
				return 0, false
			}
			return float64(u), true
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseJSIntPrefix mirrors JavaScript Number.parseInt(raw, 10): parses a leading
// optional sign and decimal digits, ignoring trailing text ("5000ms" -> 5000, "2.5" -> 2).
// upstream: packages/coding-agent/src/hindsight/settings.ts:10 (fixes A6)
func parseJSIntPrefix(raw string) (float64, bool) {
	s := strings.TrimLeft(raw, " \t\r\n")
	if s == "" {
		return 0, false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	startDigits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == startDigits {
		return 0, false
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		f, ferr := strconv.ParseFloat(s[:i], 64)
		if ferr != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return float64(n), true
}

// parseJSFloatPrefix mirrors JavaScript Number.parseFloat(raw): parses a leading
// decimal float prefix, ignoring trailing text ("0.85x" -> 0.85).
// upstream: packages/coding-agent/src/edit/settings.ts:106 (fixes A6)
func parseJSFloatPrefix(raw string) (float64, bool) {
	s := strings.TrimLeft(raw, " \t\r\n")
	if s == "" {
		return 0, false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	hasDigit := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		hasDigit = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			hasDigit = true
			i++
		}
	}
	if !hasDigit {
		return 0, false
	}
	end := i
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		expStart := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > expStart {
			end = j
		}
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// ParseText parses user- or model-supplied text (e.g. `write cfg://...`) into a
// typed, normalized value for this setting.
// upstream: packages/coding-agent/src/config/registry.ts Setting.parse
func (e *Entry) ParseText(text string) (any, error) {
	trimmed := strings.TrimSpace(text)
	var val any
	switch e.Type {
	case TypeBoolean:
		switch strings.ToLower(trimmed) {
		case "true", "1", "yes", "on":
			val = true
		case "false", "0", "no", "off":
			val = false
		default:
			return nil, fmt.Errorf("Invalid boolean value: %s. Use true/false, yes/no, on/off, or 1/0", text)
		}
	case TypeNumber:
		f, ok := parseJSNumber(trimmed)
		if !ok {
			return nil, fmt.Errorf("Invalid number: %s", text)
		}
		val = f
	case TypeArray, TypeRecord:
		parsed, err := decodeOrderedJSON(trimmed)
		if err != nil {
			return nil, fmt.Errorf("Invalid %s JSON: %s", e.Type, text)
		}
		if !e.Accepts(parsed) {
			return nil, fmt.Errorf("Invalid %s JSON: %s", e.Type, text)
		}
		val = parsed
	case TypeString, TypeEnum:
		s := trimmed
		if strings.HasPrefix(s, `"`) {
			var unquoted string
			if err := json.Unmarshal([]byte(s), &unquoted); err == nil {
				s = unquoted
			}
		}
		if len(e.Enum) > 0 {
			found := false
			for _, choice := range e.Enum {
				if choice == s {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("Invalid value: %s. Valid values: %s", text, strings.Join(e.Enum, ", "))
			}
		}
		val = s
	default:
		return nil, fmt.Errorf("unsupported setting type %q", e.Type)
	}
	norm, err := e.Canonicalize(val)
	if err != nil {
		return nil, err
	}
	if err := e.ValidateWrite(norm); err != nil {
		return nil, err
	}
	return norm, nil
}

// EnvValue reads and parses this setting's declared environment variable, if set.
// upstream: packages/coding-agent/src/config/registry.ts Setting.envValue
func (e *Entry) EnvValue() (any, bool) {
	if e.EnvName == "" {
		return nil, false
	}
	raw, ok := os.LookupEnv(e.EnvName)
	if !ok {
		return nil, false
	}
	return e.parseEnvValue(raw)
}

func (e *Entry) parseEnvValue(raw string) (any, bool) {
	// Custom env parsers from omp-v19.1.1 domain settings files.
	switch e.ID {
	case "edit.fuzzyMatch":
		switch raw {
		case "1", "true":
			return true, true
		case "0", "false":
			return false, true
		default:
			return nil, false
		}
	case "edit.fuzzyThreshold":
		// upstream: edit/settings.ts:106 Number.parseFloat prefix semantics (fixes A6)
		f, ok := parseJSFloatPrefix(raw)
		if !ok || f < 0 || f > 1 {
			return nil, false
		}
		return f, true
	case "model.loopGuard.enabled":
		if raw == "1" {
			return false, true
		}
		return nil, false
	case "providers.tinyModelDevice", "providers.tinyModelDtype":
		norm := strings.ToLower(strings.TrimSpace(raw))
		for _, v := range e.Enum {
			if v == norm {
				return v, true
			}
		}
		return nil, false
	case "searxng.basicUsername", "searxng.basicPassword":
		// Empty string is a valid RFC 7617 credential.
		return raw, true
	case "spotlight.experiment.holdout", "spotlight.experiment.scoreFree":
		f, ok := parseJSNumber(raw)
		if !ok || f < 0 || f > 1 {
			return nil, false
		}
		return f, true
	case "spotlight.experiment.tools":
		parts := strings.Split(raw, ",")
		var out []any
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case "hindsight.retainEveryNTurns",
		"hindsight.recallMaxTokens",
		"hindsight.recallContextTurns",
		"hindsight.recallMaxQueryChars",
		"hindsight.requestTimeoutMs",
		"hindsight.reflectTimeoutMs",
		"hindsight.recallTimeoutMs",
		"hindsight.retainTimeoutMs":
		// upstream: hindsight/settings.ts:10 Number.parseInt prefix semantics (fixes A6)
		return parseJSIntPrefix(raw)
	}

	// Default env parsing. upstream: registry.ts Setting.#parseEnvText
	if e.Type == TypeBoolean {
		if raw == "" {
			return nil, false
		}
		return parseEnvFlag(raw), true
	}
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	val, err := e.ParseText(raw)
	if err != nil {
		return nil, false
	}
	return val, true
}

// parseEnvFlag matches omp's pi-utils parseFlag: exact lowercase/uppercase
// 1/y/true/yes/on are true; any other non-empty text is false.
// upstream: packages/utils/src/env.ts parseFlag
func parseEnvFlag(raw string) bool {
	switch raw {
	case "1", "Y", "y", "TRUE", "true", "YES", "yes", "ON", "on":
		return true
	default:
		return false
	}
}

func asFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
