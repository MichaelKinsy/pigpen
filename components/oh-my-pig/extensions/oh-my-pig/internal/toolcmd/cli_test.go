package toolcmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var fixtureSchemaJSON = json.RawMessage(`{
	"type": "object",
	"properties": {
		"mode": {"type": "string", "enum": ["fast", "safe"], "description": "Execution mode."},
		"count": {"type": "integer", "description": "Number of passes."},
		"settings": {
			"type": "object",
			"properties": {
				"label": {"type": "string", "description": "Nested label."},
				"enabled": {"type": "boolean"}
			},
			"required": ["label"]
		},
		"tags": {"type": "array", "items": {"type": "string", "enum": ["one", "two"]}}
	},
	"required": ["mode", "settings"]
}`)

var intentSchemaJSON = json.RawMessage(`{
	"type": "object",
	"properties": {
		"i": {"type": "string", "description": "Short present-participle intent for this call."},
		"text": {"type": "string"}
	},
	"required": ["i", "text"]
}`)

func parseFixture(argv []string, stdin string) (map[string]any, error) {
	return ParseToolArgs(context.Background(), fixtureSchemaJSON, argv, ToolCliParseOptions{Cwd: "/", Stdin: stdin})
}

type funcCliHost struct {
	defFn  func(ctx context.Context, name string) (ToolCliDefinition, error)
	callFn func(ctx context.Context, name string, args map[string]any) (ToolCliResult, error)
}

func (h funcCliHost) Definition(ctx context.Context, name string) (ToolCliDefinition, error) {
	return h.defFn(ctx, name)
}

func (h funcCliHost) Call(ctx context.Context, name string, args map[string]any) (ToolCliResult, error) {
	return h.callFn(ctx, name, args)
}

func newRunnerHost() (ToolCliHost, *[]map[string]any, ToolCliDefinition) {
	var calls []map[string]any
	def := ToolCliDefinition{Name: "fixture", Description: "Run fixture.", HasDesc: true, Schema: intentSchemaJSON}
	host := funcCliHost{
		defFn: func(_ context.Context, name string) (ToolCliDefinition, error) {
			if name != def.Name {
				return ToolCliDefinition{}, errors.New("unknown command")
			}
			return def, nil
		},
		callFn: func(_ context.Context, _ string, args map[string]any) (ToolCliResult, error) {
			calls = append(calls, args)
			b, _ := json.Marshal(args)
			return ToolCliResult{Stdout: string(b) + "\n", Stderr: "", ExitCode: 0}, nil
		},
	}
	return host, &calls, def
}

func assertUsageError(t *testing.T, err error) {
	t.Helper()
	var ue *ToolCliUsageError
	if !errors.As(err, &ue) {
		t.Fatalf("expected ToolCliUsageError, got %T (%v)", err, err)
	}
}

// upstream: test/tool-cli.test.ts: "binds required scalar positionals, flags, booleans, arrays, and dotted keys"
func TestToolCliBindsPositionalsFlagsBooleansArraysAndDottedKeys(t *testing.T) {
	got, err := parseFixture([]string{
		"safe",
		"--count", "4",
		"--settings.label=fixture",
		"--settings.enabled",
		"--tags", "one",
		"--tags=two",
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"mode":     "safe",
		"count":    int64(4),
		"settings": map[string]any{"label": "fixture", "enabled": true},
		"tags":     []any{"one", "two"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	got2, err := parseFixture([]string{"safe", "--settings.label", "fixture", "--no-settings.enabled"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := map[string]any{
		"mode":     "safe",
		"settings": map[string]any{"label": "fixture", "enabled": false},
	}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got %#v, want %#v", got2, want2)
	}
}

// upstream: test/tool-cli.test.ts: "rejects malformed scalars and enum members"
func TestToolCliRejectsMalformedScalarsAndEnums(t *testing.T) {
	for _, argv := range [][]string{
		{"bogus"},
		{"--count", "1.5"},
		{"--count=abc"},
		{"safe", "--settings.enabled=maybe", "--settings.label", "ok"},
	} {
		_, err := parseFixture(argv, "")
		assertUsageError(t, err)
	}
	numSchema := json.RawMessage(`{"type":"object","properties":{"ratio":{"type":"number"}}}`)
	_, err := ParseToolArgs(context.Background(), numSchema, []string{"--ratio", "x"}, ToolCliParseOptions{Cwd: "/"})
	assertUsageError(t, err)

	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"0.5", 0.5},
		{" 12 ", 12},
		{"0x10", 16},
	} {
		got, err := ParseToolArgs(context.Background(), numSchema, []string{"--ratio", tc.raw}, ToolCliParseOptions{Cwd: "/"})
		if err != nil {
			t.Fatalf("raw=%q unexpected error: %v", tc.raw, err)
		}
		if !reflect.DeepEqual(got, map[string]any{"ratio": tc.want}) {
			t.Fatalf("raw=%q got %#v, want %v", tc.raw, got, tc.want)
		}
	}
}

// upstream: test/tool-cli.test.ts: "resolves no-prefixed booleans and keeps negation collisions JSON-only"
func TestToolCliNoPrefixedBooleansAndNegationCollisions(t *testing.T) {
	collisionSchema := json.RawMessage(`{"type":"object","properties":{"cache":{"type":"boolean"},"no_cache":{"type":"boolean"}}}`)
	_, err := ParseToolArgs(context.Background(), collisionSchema, []string{"--no-cache"}, ToolCliParseOptions{Cwd: "/"})
	assertUsageError(t, err)

	got, err := ParseToolArgs(context.Background(), collisionSchema, []string{"--json", `{"cache":false,"no_cache":true}`}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"cache": false, "no_cache": true}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}

	noPrefixed := json.RawMessage(`{"type":"object","properties":{"no_cache":{"type":"boolean"}}}`)
	got, err = ParseToolArgs(context.Background(), noPrefixed, []string{"--no-cache"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"no_cache": true}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}
	got, err = ParseToolArgs(context.Background(), noPrefixed, []string{"--no-no-cache"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"no_cache": false}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}

	ordinary := json.RawMessage(`{"type":"object","properties":{"cache":{"type":"boolean"}}}`)
	got, err = ParseToolArgs(context.Background(), ordinary, []string{"--no-cache"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"cache": false}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}
	got, err = ParseToolArgs(context.Background(), ordinary, []string{"--cache=false"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"cache": false}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}
}

// upstream: test/tool-cli.test.ts: "reserves root help/json names for CLI metadata while nested names remain flags"
func TestToolCliReservesRootHelpJsonWhileNestedRemainFlags(t *testing.T) {
	reservedSchema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"help":{"type":"boolean"},
			"json":{"type":"boolean"},
			"nested":{"type":"object","properties":{"help":{"type":"boolean"},"json":{"type":"boolean"}}}
		},
		"required":["help","json"]
	}`)
	got, err := ParseToolArgs(context.Background(), reservedSchema, []string{
		"--json", `{"help":true,"json":false}`, "--nested.help", "--nested.json=false",
	}, ToolCliParseOptions{Cwd: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"help":   true,
		"json":   false,
		"nested": map[string]any{"help": true, "json": false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// upstream: test/tool-cli.test.ts: "makes colliding schema-derived flag names JSON-only"
func TestToolCliCollidingSchemaDerivedFlagNamesJsonOnly(t *testing.T) {
	collisionSchema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"foo_bar":{"type":"string"},
			"foo-bar":{"type":"string"},
			"a.b":{"type":"string"},
			"a":{"type":"object","properties":{"b":{"type":"string"}},"required":["b"]}
		},
		"required":["foo_bar","foo-bar","a.b","a"]
	}`)
	_, err := ParseToolArgs(context.Background(), collisionSchema, []string{"--foo-bar", "wrong"}, ToolCliParseOptions{Cwd: "/"})
	assertUsageError(t, err)

	got, err := ParseToolArgs(context.Background(), collisionSchema, []string{
		"--json", `{"foo_bar":"one","foo-bar":"two","a.b":"root","a":{"b":"nested"}}`,
	}, ToolCliParseOptions{Cwd: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"foo_bar": "one",
		"foo-bar": "two",
		"a.b":     "root",
		"a":       map[string]any{"b": "nested"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// upstream: test/tool-cli.test.ts: "reserves positionals already populated by a flag and rejects surplus literals"
func TestToolCliReservesPopulatedPositionalsAndDoubleDash(t *testing.T) {
	got, err := parseFixture([]string{"--mode", "fast", "--settings.label", "ok"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"mode": "fast", "settings": map[string]any{"label": "ok"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	_, err = parseFixture([]string{"fast", "extra", "--settings.label", "ok"}, "")
	assertUsageError(t, err)

	textSchema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
	got, err = ParseToolArgs(context.Background(), textSchema, []string{"--", "--not-a-flag"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"text": "--not-a-flag"}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}
}

// upstream: test/tool-cli.test.ts: "deep-merges raw JSON while flags win regardless of ordering"
func TestToolCliDeepMergesJsonAndFlagsWin(t *testing.T) {
	got, err := parseFixture([]string{
		"--settings.enabled",
		"--json", `{"mode":"safe","count":2,"settings":{"label":"json","enabled":false}}`,
		"--count=7",
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"mode":     "safe",
		"count":    int64(7),
		"settings": map[string]any{"label": "json", "enabled": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	_, err = parseFixture([]string{"--json", "[]"}, "")
	assertUsageError(t, err)
}

// upstream: test/tool-cli.test.ts: "preserves prototype-like JSON and dotted keys without mutating object prototypes"
func TestToolCliPreservesProtoKeysSafely(t *testing.T) {
	freeForm := json.RawMessage(`{"type":"object","additionalProperties":true}`)
	got, err := ParseToolArgs(context.Background(), freeForm, []string{"--json", `{"__proto__":{"polluted":true}}`}, ToolCliParseOptions{Cwd: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"__proto__": map[string]any{"polluted": true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	dotted := json.RawMessage(`{
		"type":"object",
		"properties":{
			"__proto__":{"type":"object","properties":{"dotted":{"type":"boolean"}}},
			"constructor":{"type":"object","properties":{"prototype":{"type":"object","properties":{"ctor":{"type":"boolean"}}}}}
		}
	}`)
	got, err = ParseToolArgs(context.Background(), dotted, []string{"----proto--.dotted", "--constructor.prototype.ctor"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantDotted := map[string]any{
		"__proto__":   map[string]any{"dotted": true},
		"constructor": map[string]any{"prototype": map[string]any{"ctor": true}},
	}
	if !reflect.DeepEqual(got, wantDotted) {
		t.Fatalf("got %#v, want %#v", got, wantDotted)
	}
}

// upstream: test/tool-cli.test.ts: "uses stdin and @file as JSON or literal sources"
func TestToolCliStdinAndAtFileSources(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "args.json"), []byte(`{"mode":"fast","settings":{"label":"file"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mode.txt"), []byte("safe\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, ToolCliInputLimitBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "huge.txt"), huge, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ParseToolArgs(context.Background(), fixtureSchemaJSON, []string{"@args.json", "--count", "3"}, ToolCliParseOptions{Cwd: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"mode": "fast", "count": int64(3), "settings": map[string]any{"label": "file"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	got, err = ParseToolArgs(context.Background(), fixtureSchemaJSON, []string{"@mode.txt", "--settings.label", "text"}, ToolCliParseOptions{Cwd: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := map[string]any{"mode": "safe", "settings": map[string]any{"label": "text"}}
	if !reflect.DeepEqual(got, want2) {
		t.Fatalf("got %#v, want %#v", got, want2)
	}

	got, err = parseFixture([]string{"-", "--settings.label", "stdin"}, "fast\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want3 := map[string]any{"mode": "fast", "settings": map[string]any{"label": "stdin"}}
	if !reflect.DeepEqual(got, want3) {
		t.Fatalf("got %#v, want %#v", got, want3)
	}

	_, err = ParseToolArgs(context.Background(), fixtureSchemaJSON, []string{"@huge.txt"}, ToolCliParseOptions{Cwd: root})
	assertUsageError(t, err)
}

// upstream: test/tool-cli.test.ts: "cancels an in-flight argument reader without invoking a tool"
func TestToolCliCancelsInFlightArgumentReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ParseToolArgs(ctx, json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`), []string{"@endless"}, ToolCliParseOptions{
			Cwd: "/",
			ReadSource: func(rctx context.Context, _ string) (string, error) {
				close(started)
				<-rctx.Done()
				return "", rctx.Err()
			},
		})
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
}

// upstream: test/tool-cli.test.ts: "binds a JSON source to the sole required free-form object"
func TestToolCliBindsJsonSourceToSoleRequiredObject(t *testing.T) {
	objSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"object"},"limit":{"type":"integer"}},"required":["query"]}`)
	got, err := ParseToolArgs(context.Background(), objSchema, []string{"-", "--limit", "5"}, ToolCliParseOptions{
		Cwd:   "/",
		Stdin: `{"where":{"id":1}}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"query": map[string]any{"where": map[string]any{"id": int64(1)}}, "limit": int64(5)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	got, err = ParseToolArgs(context.Background(), objSchema, []string{"-"}, ToolCliParseOptions{
		Cwd:   "/",
		Stdin: `{"query":{"id":1}}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := map[string]any{"query": map[string]any{"id": int64(1)}}
	if !reflect.DeepEqual(got, want2) {
		t.Fatalf("got %#v, want %#v", got, want2)
	}
}

// upstream: test/tool-cli.test.ts: "normalizes local refs, nullable combinators, and allOf objects"
func TestToolCliNormalizesRefsNullableAndAllOf(t *testing.T) {
	composed := json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"$ref":"#/$defs/Action"},
			"app":{"anyOf":[{"$ref":"#/$defs/App"},{"type":"null"}]},
			"enabled":{"oneOf":[{"type":"boolean"},{"type":"null"}]}
		},
		"required":["action"],
		"$defs":{
			"Action":{"oneOf":[{"const":"open","type":"string"},{"const":"run","type":"string"}]},
			"App":{"type":"object","allOf":[{"properties":{"cdp_url":{"type":"string"}}},{"properties":{"relay":{"type":"boolean"}}}]}
		}
	}`)
	got, err := ParseToolArgs(context.Background(), composed, []string{"run", "--app.cdp-url", "http://localhost", "--app.relay", "--enabled"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"action":  "run",
		"app":     map[string]any{"cdp_url": "http://localhost", "relay": true},
		"enabled": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestToolCliStripsTopLevelAndPureUnionIntent verifies that any top-level `i`
// property (including inside each branch of a pure `anyOf`/`oneOf` root) is
// treated as harness-internal: omitted from help/flags/required checks, while
// `--i` is rejected as an unknown flag, and schemas without `i` are untouched.
func TestToolCliStripsTopLevelAndPureUnionIntent(t *testing.T) {
	withIntent := json.RawMessage(`{
		"type":"object",
		"properties":{
			"i":{"type":"string","description":"concise intent"},
			"text":{"type":"string","description":"Payload text."}
		},
		"required":["i","text"]
	}`)
	withoutIntent := json.RawMessage(`{
		"type":"object",
		"properties":{
			"text":{"type":"string","description":"Payload text."}
		},
		"required":["text"]
	}`)
	// Help text is identical with or without `i`.
	helpWith := RenderToolHelp(ToolCliDefinition{Name: "demo", Description: "Demo tool.", HasDesc: true, Schema: withIntent})
	helpWithout := RenderToolHelp(ToolCliDefinition{Name: "demo", Description: "Demo tool.", HasDesc: true, Schema: withoutIntent})
	if helpWith != helpWithout {
		t.Fatalf("help mismatch with injected `i`:\n--- WITH ---\n%s\n--- WITHOUT ---\n%s", helpWith, helpWithout)
	}
	if strings.Contains(helpWith, "--i") || strings.Contains(helpWith, "<i>") {
		t.Fatalf("help must not expose `i`: %s", helpWith)
	}
	// Positional parsing ignores `i` and rejects `--i`.
	got, err := ParseToolArgs(context.Background(), withIntent, []string{"hello"}, ToolCliParseOptions{Cwd: "/"})
	if err != nil || !reflect.DeepEqual(got, map[string]any{"text": "hello"}) {
		t.Fatalf("got %#v, err=%v", got, err)
	}
	_, err = ParseToolArgs(context.Background(), withIntent, []string{"--i", "reading", "hello"}, ToolCliParseOptions{Cwd: "/"})
	assertUsageError(t, err)

	// Pure `anyOf` and `oneOf` roots with `i` injected into each branch strip `i` cleanly.
	for _, unionKey := range []string{"anyOf", "oneOf"} {
		pureUnion := json.RawMessage(`{
			"` + unionKey + `":[
				{"type":"object","properties":{"i":{"type":"string","description":"concise intent"},"op":{"const":"open","type":"string"},"path":{"type":"string"}},"required":["i","op","path"]},
				{"type":"object","properties":{"i":{"type":"string","description":"concise intent"},"op":{"const":"close","type":"string"}},"required":["i","op"]}
			]
		}`)
		if !schemaHasHarnessIntent(pureUnion) {
			t.Fatalf("%s pure union should detect harness intent", unionKey)
		}
		unionHelp := RenderToolHelp(ToolCliDefinition{Name: "u", Description: "Union.", HasDesc: true, Schema: pureUnion})
		if strings.Contains(unionHelp, "--i") || strings.Contains(unionHelp, "<i>") {
			t.Fatalf("%s union help leaked `i`:\n%s", unionKey, unionHelp)
		}
		ugot, uerr := ParseToolArgs(context.Background(), pureUnion, []string{"close"}, ToolCliParseOptions{Cwd: "/"})
		if uerr != nil || !reflect.DeepEqual(ugot, map[string]any{"op": "close"}) {
			t.Fatalf("%s pure union parse got %#v, err=%v", unionKey, ugot, uerr)
		}
	}
}

// upstream: test/tool-cli.test.ts: "renders the usage line, positional, boolean, and repeatable array from the schema"
func TestToolCliRenderToolHelp(t *testing.T) {
	help := RenderToolHelp(ToolCliDefinition{
		Name: "fixture",
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"text":{"type":"string","description":"Text to handle."},
				"force":{"type":"boolean"},
				"tags":{"type":"array","items":{"type":"string","enum":["one","two"]}}
			},
			"required":["text"]
		}`),
	})
	for _, want := range []string{
		"Usage:\n  fixture <text> [OPTIONS] [@FILE] [-]",
		"  <text> <STRING>  Text to handle.",
		"--force / --no-force",
		"--tags {one|two}...",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q:\n%s", want, help)
		}
	}
}

// upstream: test/tool-cli.test.ts: "renders help from the live definition without invoking the command"
func TestToolCliRunHelpWithoutInvoking(t *testing.T) {
	host, calls, def := newRunnerHost()
	wantHelp := RenderToolHelp(def)
	for _, flag := range []string{"--help", "-h"} {
		res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Argv: []string{flag}, Cwd: "/"}, host)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ExitCode != 0 || res.Stderr != "" || res.Stdout != wantHelp {
			t.Fatalf("flag %s: got %+v", flag, res)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(*calls))
	}
}

// upstream: test/tool-cli.test.ts: "executes with parsed arguments and the injected protocol intent"
// In oh-my-pig, mounted calls inject `"i": ""` when the schema defines top-level `i`
// (for host validation against the shared wire schema) and omit `i` when it does not.
func TestToolCliRunExecutesWithInjectedProtocolIntent(t *testing.T) {
	host, calls, _ := newRunnerHost()
	res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Argv: []string{"hello"}, Cwd: "/"}, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	wantCalls := []map[string]any{{"text": "hello", "i": ""}}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", *calls, wantCalls)
	}

	// Tool without `i` in its schema must NOT receive `i`.
	var noIntentCalls []map[string]any
	noIntentDef := ToolCliDefinition{
		Name:        "plain",
		Description: "No intent.",
		HasDesc:     true,
		Schema:      json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}
	noIntentHost := funcCliHost{
		defFn: func(context.Context, string) (ToolCliDefinition, error) { return noIntentDef, nil },
		callFn: func(_ context.Context, _ string, args map[string]any) (ToolCliResult, error) {
			noIntentCalls = append(noIntentCalls, args)
			return ToolCliResult{Stdout: "ok\n", ExitCode: 0}, nil
		},
	}
	res2, err := RunToolCli(context.Background(), ToolCliRequest{Name: "plain", Argv: []string{"hello"}, Cwd: "/"}, noIntentHost)
	if err != nil || res2.ExitCode != 0 {
		t.Fatalf("plain tool failed: res=%+v err=%v", res2, err)
	}
	if !reflect.DeepEqual(noIntentCalls, []map[string]any{{"text": "hello"}}) {
		t.Fatalf("plain tool unexpectedly got `i`: %#v", noIntentCalls)
	}
}

// upstream: test/tool-cli.test.ts: "reports missing required arguments with the command help hint"
func TestToolCliRunMissingRequiredArgument(t *testing.T) {
	host, calls, _ := newRunnerHost()
	res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Argv: nil, Cwd: "/"}, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ToolCliResult{
		Stdout:   "",
		Stderr:   "fixture: missing required argument `text`\nfixture: run `fixture --help`\n",
		ExitCode: 2,
	}
	if res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(*calls))
	}
}

// upstream: test/tool-cli.test.ts: "maps unknown flags to usage errors naming the command"
func TestToolCliRunUnknownFlag(t *testing.T) {
	host, calls, _ := newRunnerHost()
	res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Argv: []string{"--bogus"}, Cwd: "/"}, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ToolCliResult{
		Stdout:   "",
		Stderr:   "fixture: unknown flag `--bogus`\nfixture: run `fixture --help`\n",
		ExitCode: 2,
	}
	if res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(*calls))
	}
}

// upstream: test/tool-cli.test.ts: "rejects help combined with invocation arguments"
func TestToolCliRunHelpCombinedWithArgs(t *testing.T) {
	host, calls, _ := newRunnerHost()
	res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Argv: []string{"hello", "--help"}, Cwd: "/"}, host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ToolCliResult{
		Stdout:   "",
		Stderr:   "fixture: --help cannot be combined with invocation arguments\n",
		ExitCode: 2,
	}
	if res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(*calls))
	}
}

// upstream: test/tool-cli.test.ts: "maps lookup and execution failures to exit code 1"
func TestToolCliRunMapsLookupAndExecutionFailuresToExit1(t *testing.T) {
	unknownHost := funcCliHost{
		defFn: func(context.Context, string) (ToolCliDefinition, error) {
			return ToolCliDefinition{}, errors.New("unknown command")
		},
		callFn: func(context.Context, string, map[string]any) (ToolCliResult, error) {
			return ToolCliResult{}, nil
		},
	}
	res, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Cwd: "/"}, unknownHost)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != (ToolCliResult{Stdout: "", Stderr: "fixture: unknown command\n", ExitCode: 1}) {
		t.Fatalf("got %+v", res)
	}

	failingHost := funcCliHost{
		defFn: func(_ context.Context, name string) (ToolCliDefinition, error) {
			return ToolCliDefinition{Name: name, Schema: json.RawMessage(`{"type":"object","properties":{}}`)}, nil
		},
		callFn: func(context.Context, string, map[string]any) (ToolCliResult, error) {
			return ToolCliResult{}, errors.New("tool exploded")
		},
	}
	res, err = RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Cwd: "/"}, failingHost)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != (ToolCliResult{Stdout: "", Stderr: "fixture: tool exploded\n", ExitCode: 1}) {
		t.Fatalf("got %+v", res)
	}
}

// upstream: test/tool-cli.test.ts: "rethrows abort errors instead of reporting them"
func TestToolCliRunRethrowsAbortErrors(t *testing.T) {
	abortedHost := funcCliHost{
		defFn: func(context.Context, string) (ToolCliDefinition, error) {
			return ToolCliDefinition{}, ErrAborted
		},
		callFn: func(context.Context, string, map[string]any) (ToolCliResult, error) {
			return ToolCliResult{}, nil
		},
	}
	if _, err := RunToolCli(context.Background(), ToolCliRequest{Name: "fixture", Cwd: "/"}, abortedHost); !errors.Is(err, ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runningHost := funcCliHost{
		defFn: func(_ context.Context, name string) (ToolCliDefinition, error) {
			return ToolCliDefinition{Name: name, Schema: json.RawMessage(`{"type":"object","properties":{}}`)}, nil
		},
		callFn: func(c context.Context, _ string, _ map[string]any) (ToolCliResult, error) {
			return ToolCliResult{}, c.Err()
		},
	}
	if _, err := RunToolCli(ctx, ToolCliRequest{Name: "fixture", Cwd: "/"}, runningHost); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
