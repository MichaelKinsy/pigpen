package schedule_prompt

import (
	"context"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of the original's test/subagent.test.ts (27 cases). The original mocks createAgentSession and DefaultResourceLoader; the twins
// stand in the session factory of the host (PiG's child agent session): `noExtensions`, `noSkills` and `tools` are the options of the
// session, `extensionsOverride` and `skillsOverride` are the name lists the host filters with (the filter is the host's, tested in PiG).

const sub_ = "subagent"

var testModels = []map[string]any{
	{"id": "claude-opus-4-6", "name": "Claude Opus 4.6", "provider": "anthropic"},
	{"id": "claude-sonnet-4-6", "name": "Claude Sonnet 4.6", "provider": "anthropic"},
	{"id": "claude-haiku-4-5-20251001", "name": "Claude Haiku 4.5", "provider": "anthropic"},
	{"id": "gpt-4o", "name": "GPT-4o", "provider": "openai"},
	{"id": "gemini-2.5-pro", "name": "Gemini 2.5 Pro", "provider": "google"},
}

type fakeRegistry struct{ available []map[string]any }

func (r fakeRegistry) Find(provider, id string) map[string]any {
	for _, m := range testModels {
		if m["provider"] == provider && m["id"] == id {
			return m
		}
	}
	return nil
}
func (r fakeRegistry) GetAvailable() ([]map[string]any, error) { return r.available, nil }

func registryOf(available ...map[string]any) fakeRegistry {
	if available == nil {
		available = testModels
	}
	return fakeRegistry{available}
}

type fakeChild struct {
	bound, aborted, disposed int
	messages                 []map[string]any
	listener                 sdk.AgentSessionEventListener
	prompted                 []string
	promptErr                error
	script                   func(emit func(map[string]any))
}

func (f *fakeChild) BindExtensions() error { f.bound++; return nil }
func (f *fakeChild) Abort() error          { f.aborted++; return nil }
func (f *fakeChild) Subscribe(l sdk.AgentSessionEventListener) func() {
	f.listener = l
	return func() { f.listener = nil }
}
func (f *fakeChild) Prompt(_ context.Context, text string, _ ...sdk.PromptOptions) error {
	f.prompted = append(f.prompted, text)
	if f.script != nil && f.listener != nil {
		f.script(f.listener)
	}
	return f.promptErr
}
func (f *fakeChild) Messages() ([]map[string]any, error) { return f.messages, nil }
func (f *fakeChild) Dispose()                            { f.disposed++ }

type fakeSubHost struct {
	registry  modelRegistry
	session   *fakeChild
	options   []sdk.CreateAgentSessionOptions
	createErr error
}

func (h *fakeSubHost) Cwd() string                  { return "/tmp" }
func (h *fakeSubHost) ModelRegistry() modelRegistry { return h.registry }
func (h *fakeSubHost) CreateAgentSession(o sdk.CreateAgentSessionOptions) (childSession, error) {
	h.options = append(h.options, o)
	if h.createErr != nil {
		return nil, h.createErr
	}
	return h.session, nil
}

func newSubHost() *fakeSubHost { return &fakeSubHost{registry: registryOf(), session: &fakeChild{}} }

func TestSubagentResolveModel(t *testing.T) {
	resolve := func(reg fakeRegistry, s string) map[string]any {
		m, err := resolveModel(reg, s)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	tw(t, sub_, "matches exact provider/id", func(t *testing.T) {
		if m := resolve(registryOf(), "anthropic/claude-opus-4-6"); !reflect.DeepEqual(m, testModels[0]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "falls through to fuzzy when provider/id is not exact", func(t *testing.T) {
		if m := resolve(registryOf(), "anthropic/haiku"); !reflect.DeepEqual(m, testModels[2]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "matches exact id without provider prefix", func(t *testing.T) {
		if m := resolve(registryOf(), "gpt-4o"); !reflect.DeepEqual(m, testModels[3]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "is case-insensitive on exact id", func(t *testing.T) {
		if m := resolve(registryOf(), "GPT-4o"); !reflect.DeepEqual(m, testModels[3]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "matches via id substring", func(t *testing.T) {
		if m := resolve(registryOf(), "haiku"); !reflect.DeepEqual(m, testModels[2]) {
			t.Fatalf("%v", m)
		}
		if m := resolve(registryOf(), "sonnet"); !reflect.DeepEqual(m, testModels[1]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "matches via name substring when id substring misses", func(t *testing.T) {
		if m := resolve(registryOf(), "Gemini"); !reflect.DeepEqual(m, testModels[4]) {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "returns undefined for unknown model", func(t *testing.T) {
		if m := resolve(registryOf(), "definitely-not-a-real-model"); m != nil {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "returns undefined when registry is empty", func(t *testing.T) {
		if m := resolve(fakeRegistry{available: []map[string]any{}}, "haiku"); m != nil {
			t.Fatalf("%v", m)
		}
	})
	tw(t, sub_, "only resolves against models with configured auth (getAvailable)", func(t *testing.T) {
		if m := resolve(registryOf(testModels[0]), "sonnet"); m != nil {
			t.Fatalf("%v", m)
		}
	})
}

func TestSubagentDescribeAvailableModels(t *testing.T) {
	describe := func(reg fakeRegistry) string {
		s, err := describeAvailableModels(reg)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	tw(t, sub_, "returns explanatory text when registry is empty", func(t *testing.T) {
		if got := describe(fakeRegistry{available: []map[string]any{}}); got != "No models with configured auth." {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "lists up to 5 models as provider/id pairs", func(t *testing.T) {
		text := describe(registryOf())
		if !strings.HasPrefix(text, "Available: ") || !strings.Contains(text, "anthropic/claude-opus-4-6") || !strings.Contains(text, "openai/gpt-4o") {
			t.Fatalf("%q", text)
		}
	})
	tw(t, sub_, "appends a '… (N more)' suffix when more than 5 models exist", func(t *testing.T) {
		var many []map[string]any
		for i := 0; i < 8; i++ {
			many = append(many, map[string]any{"id": "m" + string(rune('0'+i)), "name": "Model", "provider": "p"})
		}
		text := describe(fakeRegistry{many})
		if !strings.Contains(text, "p/m0") || !strings.Contains(text, "p/m4") || strings.Contains(text, "p/m5") || !strings.Contains(text, "(3 more)") {
			t.Fatalf("%q", text)
		}
	})
	tw(t, sub_, "does not append the 'more' suffix when count is exactly 5", func(t *testing.T) {
		if text := describe(registryOf(testModels[:5]...)); strings.Contains(text, "more") {
			t.Fatalf("%q", text)
		}
	})
}

func TestSubagentLastAssistantText(t *testing.T) {
	msg := func(role string, content any) map[string]any { return map[string]any{"role": role, "content": content} }
	text := func(s string) any { return map[string]any{"type": "text", "text": s} }
	tw(t, sub_, "returns empty string when there are no messages", func(t *testing.T) {
		if got := getLastAssistantText(nil); got != "" {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "returns empty string when no assistant message exists", func(t *testing.T) {
		got := getLastAssistantText([]map[string]any{msg("user", "hi"), msg("toolResult", []any{text("ok")})})
		if got != "" {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "extracts text from a string-content assistant message", func(t *testing.T) {
		if got := getLastAssistantText([]map[string]any{msg("assistant", "hello world")}); got != "hello world" {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "concatenates text parts from an array-content assistant message", func(t *testing.T) {
		got := getLastAssistantText([]map[string]any{msg("assistant", []any{text("part one "), map[string]any{"type": "toolCall", "name": "bash"}, text("part two")})})
		if got != "part one part two" {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "walks backward and returns the most recent assistant message", func(t *testing.T) {
		got := getLastAssistantText([]map[string]any{msg("assistant", "first"), msg("user", "follow up"), msg("assistant", "second")})
		if got != "second" {
			t.Fatalf("%q", got)
		}
	})
	tw(t, sub_, "skips assistant messages that contain only tool calls (no text)", func(t *testing.T) {
		got := getLastAssistantText([]map[string]any{msg("assistant", "real text"), msg("assistant", []any{map[string]any{"type": "toolCall", "name": "bash"}})})
		if got != "real text" {
			t.Fatalf("%q", got)
		}
	})
}

func TestSubagentRunOnce(t *testing.T) {
	run := func(t *testing.T, opts subagentOptions) (*fakeSubHost, sdk.CreateAgentSessionOptions) {
		t.Helper()
		h := newSubHost()
		res := runSubagentOnce(h, "test prompt", testModels[0]["id"].(string), nil, opts)
		if !res.OK {
			t.Fatalf("result = %+v", res)
		}
		if len(h.options) != 1 {
			t.Fatalf("sessions created: %d", len(h.options))
		}
		return h, h.options[0]
	}
	tools := func(t *testing.T, o sdk.CreateAgentSessionOptions, want []string) {
		t.Helper()
		if !reflect.DeepEqual(o.Tools, want) {
			t.Fatalf("tools = %v, want %v", o.Tools, want)
		}
	}
	tw(t, sub_, "default: creates subagent with noExtensions=true, noSkills=true, tools=DEFAULT_TOOL_NAMES, no bindExtensions", func(t *testing.T) {
		h, o := run(t, subagentOptions{})
		if !o.Resources.NoExtensions || !o.Resources.NoSkills {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, []string{"bash", "read", "edit", "write", "grep", "find", "ls"})
		if h.session.bound != 0 {
			t.Fatal("bindExtensions was called")
		}
	})
	tw(t, sub_, "extensions:true: sets noExtensions=false, noSkills=true, tools=undefined, calls bindExtensions", func(t *testing.T) {
		h, o := run(t, subagentOptions{Extensions: true})
		if o.Resources.NoExtensions || !o.Resources.NoSkills {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, nil)
		if h.session.bound != 1 {
			t.Fatalf("bindExtensions called %d times", h.session.bound)
		}
	})
	tw(t, sub_, "skills:true: sets noExtensions=true, noSkills=false, tools=DEFAULT_TOOL_NAMES, no bindExtensions", func(t *testing.T) {
		h, o := run(t, subagentOptions{Skills: true})
		if !o.Resources.NoExtensions || o.Resources.NoSkills {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, defaultToolNames)
		if h.session.bound != 0 {
			t.Fatal("bindExtensions was called")
		}
	})
	tw(t, sub_, "extensions:true + skills:true: sets noExtensions=false, noSkills=false, tools=undefined, calls bindExtensions", func(t *testing.T) {
		h, o := run(t, subagentOptions{Extensions: true, Skills: true})
		if o.Resources.NoExtensions || o.Resources.NoSkills {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, nil)
		if h.session.bound != 1 {
			t.Fatalf("bindExtensions called %d times", h.session.bound)
		}
	})
	tw(t, sub_, "extensions:[\"pkg\"]: filters extensions, sets noExtensions=false, tools=undefined, calls bindExtensions", func(t *testing.T) {
		h, o := run(t, subagentOptions{Extensions: []any{"telegram"}})
		if o.Resources.NoExtensions || !reflect.DeepEqual(o.Resources.ExtensionNames, []string{"telegram"}) {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, nil)
		if h.session.bound != 1 {
			t.Fatalf("bindExtensions called %d times", h.session.bound)
		}
	})
	tw(t, sub_, "skills:[\"git\"]: filters skills, sets noSkills=false", func(t *testing.T) {
		h, o := run(t, subagentOptions{Skills: []any{"git"}})
		if o.Resources.NoSkills || !reflect.DeepEqual(o.Resources.SkillNames, []string{"git"}) {
			t.Fatalf("resources = %+v", o.Resources)
		}
		if h.session.bound != 0 {
			t.Fatal("bindExtensions was called")
		}
	})
	tw(t, sub_, "skills:[]: empty array means none — same as unset", func(t *testing.T) {
		h, o := run(t, subagentOptions{Skills: []any{}})
		if !o.Resources.NoSkills || o.Resources.SkillNames != nil {
			t.Fatalf("resources = %+v", o.Resources)
		}
		if h.session.bound != 0 {
			t.Fatal("bindExtensions was called")
		}
	})
	tw(t, sub_, "extensions:[]: empty array means none — same as unset", func(t *testing.T) {
		h, o := run(t, subagentOptions{Extensions: []any{}})
		if !o.Resources.NoExtensions || o.Resources.ExtensionNames != nil {
			t.Fatalf("resources = %+v", o.Resources)
		}
		tools(t, o, defaultToolNames)
		if h.session.bound != 0 {
			t.Fatal("bindExtensions was called")
		}
	})
}
