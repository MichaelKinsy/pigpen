package langsmith

// Port of src/config.ts: the extension config (zod schema semantics), its three sources and their merge order
// (defaults < ~/.pi/langsmith.json < <cwd>/.pi/langsmith.json < environment).

import (
	"math"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// defaultProject is config.ts:29.
const defaultProject = "pi-coding-agent"

// replicaCfg is one parsed ReplicaSchema value (config.ts:7-27); a nil pointer is `undefined`.
type replicaCfg struct {
	APIURL  *string
	APIKey  *string
	Project *string
	Updates *omap
}

// config is config.ts Config (z.infer<typeof ConfigSchema>) after the defaults were applied: Project is never
// undefined, because the defaults set it and no source can unset it.
type config struct {
	Enabled  bool
	APIKey   *string
	APIURL   *string
	Project  string
	Metadata *omap
	// Replicas is nil for `undefined`; a non-nil empty slice is the explicit empty list.
	Replicas []replicaCfg
}

// partialConfig is PartialConfigSchema's output: a nil pointer or false flag is an absent key.
type partialConfig struct {
	Enabled     *bool
	APIKey      *string
	APIURL      *string
	Project     *string
	Metadata    *omap
	Replicas    []replicaCfg
	HasReplicas bool
}

// configOptions is getConfig's `options` (config.ts:111-115). Lookup is process.env.
type configOptions struct {
	Home   string
	Cwd    string
	Lookup func(string) (string, bool)
}

// zodIssue is one ZodError issue (zod 4 invalid_type).
type zodIssue struct {
	Expected string
	Path     []any // string and int
	Message  string
}

// zodError is a ZodError. Its message is what zod 4 gives: the issues as JSON with a two-space indent.
type zodError struct{ Issues []zodIssue }

func (e *zodError) Error() string {
	var b []byte
	if len(e.Issues) == 0 {
		return "[]"
	}
	b = append(b, "[\n"...)
	for i, is := range e.Issues {
		if i > 0 {
			b = append(b, ",\n"...)
		}
		b = append(b, "  {\n    \"expected\": "...)
		b = json.AppendStringifyString(b, is.Expected)
		b = append(b, ",\n    \"code\": \"invalid_type\",\n    \"path\": ["...)
		for j, p := range is.Path {
			if j > 0 {
				b = append(b, ',')
			}
			b = append(b, "\n      "...)
			switch x := p.(type) {
			case int:
				b = strconv.AppendInt(b, int64(x), 10)
			case string:
				b = json.AppendStringifyString(b, x)
			}
		}
		if len(is.Path) > 0 {
			b = append(b, "\n    "...)
		}
		b = append(b, "],\n    \"message\": "...)
		b = json.AppendStringifyString(b, is.Message)
		b = append(b, "\n  }"...)
	}
	b = append(b, "\n]"...)
	return string(b)
}

// parsedType is zod 4's received-type name for a decoded JSON value.
func parsedType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undefinedT:
		return "undefined"
	case []any:
		return "array"
	case *omap, map[string]any:
		return "object"
	case string:
		return "string"
	case float64:
		switch {
		case math.IsNaN(x):
			return "nan"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
		return "number"
	case bool:
		return "boolean"
	}
	return "unknown"
}

func invalidType(expected string, v any, path []any) zodIssue {
	return zodIssue{Expected: expected, Path: append([]any(nil), path...), Message: "Invalid input: expected " + expected + ", received " + parsedType(v)}
}

// parseBoolean is config.ts:55-63. The environment only supplies strings.
func parseBoolean(value any) *bool {
	switch x := value.(type) {
	case bool:
		return &x
	case string:
		switch jsToLowerCase(jsTrim(x)) {
		case "1", "true", "yes", "on":
			t := true
			return &t
		case "0", "false", "no", "off":
			f := false
			return &f
		}
	}
	return nil
}

// parseJSON is config.ts:65-73: JSON.parse of a non-blank string, undefined on a syntax error. Objects decode to
// *omap in the text's key order, as JSON.parse keeps it.
func parseJSON(value string, present bool) any {
	if !present || jsTrim(value) == "" {
		return undef
	}
	v, ok := parseJSONOrdered(value)
	if !ok {
		return undef
	}
	return v
}

// readConfigFile is config.ts:81-88: any read, parse or schema failure yields undefined (nil).
func readConfigFile(file string) *partialConfig {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	v, ok := parseJSONOrdered(string(data))
	if !ok {
		return nil
	}
	in, isObj := v.(*omap)
	if !isObj {
		return nil
	}
	pc, issues := validatePartial(in)
	if len(issues) > 0 {
		return nil
	}
	return pc
}

// getVar is config.ts:90-92: `env[LANGSMITH_PI_X] ?? env[LANGSMITH_X]` (an empty value counts).
func getVar(suffix string, lookup func(string) (string, bool)) (string, bool) {
	if v, ok := lookup("LANGSMITH_PI_" + suffix); ok {
		return v, true
	}
	return lookup("LANGSMITH_" + suffix)
}

// readConfigEnv is config.ts:94-107. Its ZodError is not caught upstream: getConfig fails with it.
func readConfigEnv(lookup func(string) (string, bool)) (*partialConfig, error) {
	in := newOmap()
	enabled := undef
	if v, ok := lookup("TRACE_TO_LANGSMITH"); ok {
		if b := parseBoolean(v); b != nil {
			enabled = *b
		}
	}
	in.set("enabled", enabled)
	for _, f := range [...]struct{ key, suffix string }{{"api_key", "API_KEY"}, {"api_url", "ENDPOINT"}, {"project", "PROJECT"}} {
		if v, ok := getVar(f.suffix, lookup); ok {
			in.set(f.key, v)
		} else {
			in.set(f.key, undef)
		}
	}
	for _, f := range [...]struct{ key, suffix string }{{"metadata", "METADATA"}, {"replicas", "RUNS_ENDPOINTS"}} {
		v, ok := getVar(f.suffix, lookup)
		in.set(f.key, parseJSON(v, ok))
	}
	pc, issues := validatePartial(in)
	if len(issues) > 0 {
		return nil, &zodError{Issues: issues}
	}
	return pc, nil
}

// validatePartial is PartialConfigSchema.parse(in) for a decoded object: every issue is collected, in the schema's key
// order; unknown keys are stripped; an undefined value is an absent key.
func validatePartial(in *omap) (*partialConfig, []zodIssue) {
	var issues []zodIssue
	pc := &partialConfig{}
	present := func(k string) (any, bool) {
		v := in.val(k)
		return v, !isUndef(v)
	}
	if v, ok := present("enabled"); ok {
		if b, isBool := v.(bool); isBool {
			pc.Enabled = &b
		} else {
			issues = append(issues, invalidType("boolean", v, []any{"enabled"}))
		}
	}
	for _, f := range [...]struct {
		key string
		dst **string
	}{{"api_key", &pc.APIKey}, {"api_url", &pc.APIURL}, {"project", &pc.Project}} {
		if v, ok := present(f.key); ok {
			if s, isStr := v.(string); isStr {
				*f.dst = &s
			} else {
				issues = append(issues, invalidType("string", v, []any{f.key}))
			}
		}
	}
	if v, ok := present("metadata"); ok {
		if m, isRec := v.(*omap); isRec {
			pc.Metadata = zodRecord(m)
		} else {
			issues = append(issues, invalidType("record", v, []any{"metadata"}))
		}
	}
	if v, ok := present("replicas"); ok {
		arr, isArr := v.([]any)
		if !isArr {
			issues = append(issues, invalidType("array", v, []any{"replicas"}))
		} else {
			reps := make([]replicaCfg, 0, len(arr))
			for i, e := range arr {
				r, is := validateReplica(e, []any{"replicas", i})
				issues = append(issues, is...)
				reps = append(reps, r)
			}
			pc.Replicas, pc.HasReplicas = reps, true
		}
	}
	return pc, issues
}

// validateReplica is ReplicaSchema (config.ts:7-27): the preprocess maps the camelCase aliases (a null alias falls
// through `??`) and drops every other key, then z.object checks the four fields.
func validateReplica(v any, path []any) (replicaCfg, []zodIssue) {
	rec, isRec := v.(*omap)
	if !isRec {
		return replicaCfg{}, []zodIssue{invalidType("object", v, path)}
	}
	pick := func(a, b string) any {
		if x := rec.val(a); x != nil && !isUndef(x) {
			return x
		}
		return rec.val(b)
	}
	var out replicaCfg
	var issues []zodIssue
	sub := func(k string) []any { return append(append([]any(nil), path...), k) }
	for _, f := range [...]struct {
		key string
		val any
		dst **string
	}{
		{"api_url", pick("api_url", "apiUrl"), &out.APIURL},
		{"api_key", pick("api_key", "apiKey"), &out.APIKey},
		{"project", pick("project", "projectName"), &out.Project},
	} {
		if isUndef(f.val) {
			continue
		}
		if s, ok := f.val.(string); ok {
			*f.dst = &s
		} else {
			issues = append(issues, invalidType("string", f.val, sub(f.key)))
		}
	}
	if u := rec.val("updates"); !isUndef(u) {
		if m, ok := u.(*omap); ok {
			out.Updates = zodRecord(m)
		} else {
			issues = append(issues, invalidType("record", u, sub("updates")))
		}
	}
	return out, issues
}

// zodRecord is the output of z.record(z.string(), z.unknown()): zod assigns each key onto a fresh object, so a `__proto__`
// key sets that object's prototype instead of an own property and is lost.
func zodRecord(m *omap) *omap {
	if !m.has("__proto__") {
		return m
	}
	return m.omit("__proto__")
}

// getHomeDir is config.ts:109: `process.env.HOME ?? os.homedir()`.
func getHomeDir() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// getConfig is config.ts:111-133. With nil options it reads the process: HOME, the working directory and the
// environment. The error is a *zodError (environment) or an I/O error resolving the working directory.
func getConfig(options *configOptions) (*config, error) {
	var home, cwd string
	lookup := os.LookupEnv
	if options != nil {
		home, cwd = options.Home, options.Cwd
		if options.Lookup != nil {
			lookup = options.Lookup
		}
	} else {
		home = getHomeDir()
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}

	envConfig, err := readConfigEnv(lookup)
	if err != nil {
		return nil, err
	}
	globalConfig := readConfigFile(filepath.Join(home, ".pi", "langsmith.json"))
	localConfig := readConfigFile(filepath.Join(cwd, ".pi", "langsmith.json"))

	cfg := &config{Project: defaultProject}
	for _, pc := range [...]*partialConfig{globalConfig, localConfig, envConfig} {
		if pc == nil {
			continue
		}
		if pc.Enabled != nil {
			cfg.Enabled = *pc.Enabled
		}
		if pc.APIKey != nil {
			cfg.APIKey = pc.APIKey
		}
		if pc.APIURL != nil {
			cfg.APIURL = pc.APIURL
		}
		if pc.Project != nil {
			cfg.Project = *pc.Project
		}
		if pc.Metadata != nil {
			cfg.Metadata = pc.Metadata
		}
		if pc.HasReplicas {
			cfg.Replicas = pc.Replicas
		}
	}
	return cfg, nil
}
