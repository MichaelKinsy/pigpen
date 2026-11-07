package langsmith

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// configVector is one case of testdata/ext_config.json (produced by the real config.js, zod 4).
type configVector struct {
	Env    map[string]string `json:"env"`
	Home   *string           `json:"home"`
	Cwd    *string           `json:"cwd"`
	Result *string           `json:"result"`
	Error  *struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// configJSON is JSON.stringify of the Config zod returns: keys in the schema's order, undefined dropped.
func configJSON(c *config) string {
	o := obj("enabled", c.Enabled, "api_key", ptrOrUndef(c.APIKey), "api_url", ptrOrUndef(c.APIURL), "project", c.Project)
	if c.Metadata != nil {
		o.set("metadata", c.Metadata)
	}
	if c.Replicas != nil {
		reps := make([]any, len(c.Replicas))
		for i, r := range c.Replicas {
			ro := obj("api_url", ptrOrUndef(r.APIURL), "api_key", ptrOrUndef(r.APIKey), "project", ptrOrUndef(r.Project))
			if r.Updates != nil {
				ro.set("updates", r.Updates)
			}
			reps[i] = ro
		}
		o.set("replicas", reps)
	}
	b, _, _ := stringify(o)
	return string(b)
}

func readVectors[T any](t *testing.T, name string) []T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConfigVectors(t *testing.T) {
	vectors := readVectors[configVector](t, "ext_config.json")
	if len(vectors) < 60 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for i, v := range vectors {
		base := t.TempDir()
		home, cwd := filepath.Join(base, "home"), filepath.Join(base, "cwd")
		for dir, content := range map[string]*string{home: v.Home, cwd: v.Cwd} {
			if content == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".pi", "langsmith.json"), []byte(*content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		env := v.Env
		cfg, err := getConfig(&configOptions{Home: home, Cwd: cwd, Lookup: func(k string) (string, bool) { s, ok := env[k]; return s, ok }})
		switch {
		case v.Error != nil:
			ze, ok := err.(*zodError)
			if !ok {
				t.Errorf("vector %d (%v): want ZodError, got %v", i, v.Env, err)
				continue
			}
			if v.Error.Name != "ZodError" || ze.Error() != v.Error.Message {
				t.Errorf("vector %d (%v):\n got %s\nwant %s", i, v.Env, ze.Error(), v.Error.Message)
			}
		case err != nil:
			t.Errorf("vector %d (%v): %v", i, v.Env, err)
		default:
			if got := configJSON(cfg); got != *v.Result {
				t.Errorf("vector %d (env %v home %v cwd %v):\n got %s\nwant %s", i, v.Env, deref(v.Home), deref(v.Cwd), got, *v.Result)
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func TestParseBoolean(t *testing.T) {
	yes, no := true, false
	for in, want := range map[any]*bool{
		true: &yes, false: &no, "1": &yes, "true": &yes, " YES ": &yes, "on": &yes, "0": &no, "False": &no, "no": &no, "OFF": &no,
		"": nil, "maybe": nil, "\u00a0true\u00a0": &yes, "\ufefftrue": &yes, 1.0: nil, nil: nil, "2": nil,
	} {
		got := parseBoolean(in)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("parseBoolean(%#v) = %v, want %v", in, got, want)
		}
	}
}

func TestParseJSON(t *testing.T) {
	if !isUndef(parseJSON("", false)) || !isUndef(parseJSON("   ", true)) || !isUndef(parseJSON("{bad", true)) {
		t.Fatal("blank, absent and malformed text are undefined")
	}
	if parseJSON("null", true) != nil {
		t.Fatal("JSON null stays null")
	}
	m, ok := parseJSON(`{"b":1,"a":2}`, true).(*omap)
	if !ok || m.keys[0] != "b" || m.keys[1] != "a" {
		t.Fatalf("object order lost: %#v", m)
	}
}

func TestGetVarFallback(t *testing.T) {
	env := map[string]string{"LANGSMITH_X": "plain", "LANGSMITH_PI_Y": "", "LANGSMITH_Y": "plain"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if v, ok := getVar("X", lookup); !ok || v != "plain" {
		t.Fatal("falls back to LANGSMITH_X")
	}
	if v, ok := getVar("Y", lookup); !ok || v != "" {
		t.Fatal("an empty LANGSMITH_PI_Y is kept (?? only skips undefined)")
	}
	if _, ok := getVar("Z", lookup); ok {
		t.Fatal("unset is undefined")
	}
}

func TestGetHomeDir(t *testing.T) {
	t.Setenv("HOME", "/some/home")
	if got := getHomeDir(); got != "/some/home" {
		t.Fatalf("HOME wins: %q", got)
	}
	t.Setenv("HOME", "")
	if got := getHomeDir(); got != "" {
		t.Fatalf("an empty HOME is kept (process.env.HOME ?? ...): %q", got)
	}
}

func TestGetConfigOptionsNil(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(cwd)
	t.Setenv("TRACE_TO_LANGSMITH", "")
	t.Setenv("LANGSMITH_PI_PROJECT", "from-env")
	cfg, err := getConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.Project != "from-env" {
		t.Fatalf("process environment, cwd and HOME are read: %+v", cfg)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "langsmith.json"), []byte(`{"enabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = getConfig(nil); !cfg.Enabled {
		t.Fatal("<cwd>/.pi/langsmith.json is read from the working directory")
	}
}

func TestZodErrorShape(t *testing.T) {
	e := &zodError{Issues: []zodIssue{{Expected: "record", Path: []any{"metadata"}, Message: "Invalid input: expected record, received null"}}}
	want := "[\n  {\n    \"expected\": \"record\",\n    \"code\": \"invalid_type\",\n    \"path\": [\n      \"metadata\"\n    ],\n    \"message\": \"Invalid input: expected record, received null\"\n  }\n]"
	if e.Error() != want {
		t.Fatalf("got %q", e.Error())
	}
	if (&zodError{}).Error() != "[]" {
		t.Fatal("no issues")
	}
	if parsedType(math.Inf(1)) != "Infinity" || parsedType(math.Inf(-1)) != "-Infinity" || parsedType(math.NaN()) != "nan" || parsedType(1.5) != "number" {
		t.Fatal("number type names")
	}
	if parsedType(struct{}{}) != "unknown" || parsedType(undef) != "undefined" || parsedType(map[string]any{}) != "object" {
		t.Fatal("parsedType")
	}
}

func TestPiEmitsAgentSettled(t *testing.T) {
	for v, want := range map[string]bool{
		"0.80.3": false, "0.80.4": true, "0.79.99": false, "0.81.0": true, "0.86.1": true, "0.87.0": true, "1.0.0": true, "1.5.0": true,
		"0.80.4-beta": true, "0.80": false, "": false, "x.y.z": false, "0.80.x": false, "10.0.0": true, "0.0.0": false, "0.80.3+1.5.0": false,
		"1.2": false, "1.2.": false, "1..2": false, "v1.5.0": false,
	} {
		if got := piEmitsAgentSettled(v); got != want {
			t.Errorf("piEmitsAgentSettled(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestReplicaValidationBranches(t *testing.T) {
	root, ok := parseJSONOrdered(`{"replicas":[{"apiUrl":"u","apiKey":"k","projectName":"p","updates":{"a":1}},{"api_url":null,"apiUrl":"b"},{"api_url":"x","apiUrl":"y"}]}`)
	if !ok {
		t.Fatal("parse")
	}
	pc, issues := validatePartial(root.(*omap))
	if len(issues) != 0 || len(pc.Replicas) != 3 {
		t.Fatalf("%v %+v", issues, pc)
	}
	if *pc.Replicas[0].APIURL != "u" || *pc.Replicas[0].APIKey != "k" || *pc.Replicas[0].Project != "p" || pc.Replicas[0].Updates == nil {
		t.Fatalf("aliases: %+v", pc.Replicas[0])
	}
	if *pc.Replicas[1].APIURL != "b" {
		t.Fatal("a null api_url falls through to apiUrl (??)")
	}
	if *pc.Replicas[2].APIURL != "x" {
		t.Fatal("api_url wins over apiUrl")
	}
}
