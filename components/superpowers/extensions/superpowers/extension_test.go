package superpowers

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The twins of upstream's tests/pi/test-pi-extension.mjs (obra/superpowers v6.4.2). Each carries the
// upstream title unchanged; the first file argument is the upstream test file.

// contextReply is what the host receives from a context handler: the message list and whether it is the
// list the host sent (the SDK's _pigContextUnchanged, the Go form of a handler returning undefined).
type contextReply struct {
	Messages  []map[string]any `json:"messages"`
	Unchanged bool             `json:"_pigContextUnchanged"`
}

func fireContext(t *testing.T, h *Host, messages []any) contextReply {
	t.Helper()
	raw := h.Fire("context", map[string]any{"messages": messages})
	var reply contextReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("context reply %s: %v", raw, err)
	}
	return reply
}

func textOf(message map[string]any) string {
	if s, ok := message["content"].(string); ok {
		return s
	}
	var out []string
	parts, _ := message["content"].([]any)
	for _, part := range parts {
		p, _ := part.(map[string]any)
		if p["type"] == "text" {
			out = append(out, p["text"].(string))
		}
	}
	return strings.Join(out, "\n")
}

func startExt(t *testing.T) *Host {
	t.Helper()
	return StartHost(t, Extension(), HostOptions{})
}

func userMessage(text string, timestamp int) map[string]any {
	return map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": timestamp}
}

func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("got %s, want %s", g, w)
	}
}

func TestUpstreamTwins(t *testing.T) {
	tskip(t, "test-pi-extension", "package.json declares a pi package with skills and extension resources",
		"asserts upstream's own package.json (name, keywords, pi.skills, pi.extensions); this Package's manifest lists its 15 renamed Skills and extensions/superpowers, and npm run check and pig package validate verify them")

	tw(t, "test-pi-extension", "extension registers lifecycle hooks without pre-compaction injection", func(t *testing.T) {
		h := startExt(t)
		for _, event := range []string{"resources_discover", "session_start", "session_compact", "context", "agent_end"} {
			if !h.Registered(event) {
				t.Errorf("missing %s handler", event)
			}
		}
		if h.Registered("session_before_compact") {
			t.Error("session_before_compact must not be registered")
		}
		// One handler per event: upstream's firstHandler asserts exactly one.
		for _, event := range []string{"resources_discover", "session_start", "session_compact", "context", "agent_end"} {
			if n := len(h.allHandlers[event]); n != 1 {
				t.Errorf("%s: %d handlers, want 1", event, n)
			}
		}
	})

	tw(t, "test-pi-extension", "resources_discover contributes the bundled skills directory", func(t *testing.T) {
		root := t.TempDir()
		skills := filepath.Join(root, "skills")
		h := startWithCommands(t, skillCommands(skills))
		raw := h.Fire("resources_discover", map[string]any{"cwd": root, "reason": "startup"})
		var result struct {
			SkillPaths []string `json:"skillPaths"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		sameJSON(t, result.SkillPaths, []string{skills})
	})

	tw(t, "test-pi-extension", "startup context injects the bootstrap as one user message until agent_end", func(t *testing.T) {
		h := startExt(t)
		h.Fire("session_start", map[string]any{"reason": "startup"})

		original := userMessage("Let us make a react todo list", 1)
		result := fireContext(t, h, []any{original})
		if len(result.Messages) != 2 {
			t.Fatalf("got %d messages, want 2", len(result.Messages))
		}
		if result.Messages[0]["role"] != "user" {
			t.Errorf("role = %v", result.Messages[0]["role"])
		}
		for _, want := range []string{"You have superpowers", "Pi tool mapping"} {
			if !strings.Contains(textOf(result.Messages[0]), want) {
				t.Errorf("bootstrap lacks %q", want)
			}
		}
		sameJSON(t, result.Messages[1], original)

		repeated := fireContext(t, h, []any{original})
		if len(repeated.Messages) != 2 {
			t.Fatalf("repeated request: %d messages, want 2", len(repeated.Messages))
		}
		if !strings.Contains(textOf(repeated.Messages[0]), "You have superpowers") {
			t.Error("repeated request lacks the bootstrap")
		}

		already := make([]any, len(result.Messages))
		for i, m := range result.Messages {
			already[i] = m
		}
		if got := fireContext(t, h, already); !got.Unchanged {
			t.Error("bootstrap should not duplicate when already present")
		}

		h.Fire("agent_end", map[string]any{"messages": []any{}})
		if got := fireContext(t, h, []any{original}); !got.Unchanged {
			t.Error("startup bootstrap should clear after agent_end")
		}
	})

	tw(t, "test-pi-extension", "session_compact injects bootstrap after compaction summaries, not before compaction", func(t *testing.T) {
		h := startExt(t)
		h.Fire("session_compact", map[string]any{"compactionEntry": map[string]any{}, "fromExtension": false})

		summary := map[string]any{"role": "compactionSummary", "summary": "Prior work summary", "tokensBefore": 123, "timestamp": 1}
		user := userMessage("Continue", 2)
		result := fireContext(t, h, []any{summary, user})
		if len(result.Messages) != 3 {
			t.Fatalf("got %d messages, want 3", len(result.Messages))
		}
		sameJSON(t, result.Messages[0], summary)
		if result.Messages[1]["role"] != "user" {
			t.Errorf("role = %v", result.Messages[1]["role"])
		}
		if !strings.Contains(textOf(result.Messages[1]), "You have superpowers") {
			t.Error("bootstrap missing after the summary")
		}
		sameJSON(t, result.Messages[2], user)
	})

	tskip(t, "test-pi-extension", "pi tools reference documents pi-specific mappings",
		"reads upstream's skills/using-superpowers/references/pi-tools.md, a Skill file outside this module; the shipped copy is checked byte for byte against the original (plus the recorded renames) by port/adapt.py and the Skill gate")
}

// skillCommands is what the host lists when the package's Skills are loaded from skillsDir: the shipped
// using-superpowers Skill and another one, as getCommands returns them.
func skillCommands(skillsDir string) []any {
	skill := func(name string) map[string]any {
		return map[string]any{
			"name": "skill:" + name, "description": "d", "source": "skill",
			"sourceInfo": map[string]any{"path": filepath.Join(skillsDir, name, "SKILL.md"), "source": "local", "scope": "project", "origin": "package"},
		}
	}
	return []any{
		map[string]any{"name": "review", "source": "prompt", "sourceInfo": map[string]any{"path": "/p/review.md", "source": "local", "scope": "user", "origin": "top-level"}},
		skill("pigpen-superpowers-brainstorming"),
		skill("pigpen-superpowers-using-superpowers"),
	}
}

// startWithCommands starts the extension on a host whose getCommands answers commands.
func startWithCommands(t *testing.T, commands []any) *Host {
	t.Helper()
	return StartHost(t, Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "getCommands" {
			return map[string]any{"commands": commands}, ""
		}
		return map[string]any{}, ""
	}})
}

var markerRe = regexp.MustCompile(`superpowers:using-superpowers bootstrap for pi`)

func TestBootstrapText(t *testing.T) {
	got := bootstrapContent()
	if !strings.HasPrefix(got, "<EXTREMELY_IMPORTANT>\nsuperpowers:using-superpowers bootstrap for pi\n\nYou have superpowers.\n\nThe using-superpowers skill content is included below and is already loaded for this Pi session. Follow it now. Do not try to load using-superpowers again.\n\n") {
		t.Errorf("bootstrap head: %q", got[:min(len(got), 300)])
	}
	if !strings.HasSuffix(got, "action.\n</EXTREMELY_IMPORTANT>") {
		t.Errorf("bootstrap tail: %q", got[max(0, len(got)-120):])
	}
	if n := len(markerRe.FindAllString(got, -1)); n != 1 {
		t.Errorf("marker appears %d times, want 1", n)
	}
	body := stripFrontmatter(bootstrapSkill)
	if strings.HasPrefix(body, "---") || !strings.Contains(got, "\n\n"+body+"\n\n## Pi tool mapping\n\n") {
		t.Error("the Skill body (without its frontmatter) is not followed by the Pi tool mapping")
	}
	if !strings.Contains(got, "`Skill` tool") || strings.Contains(got, "\\`") {
		t.Error("the mapping keeps its backticks and no escape")
	}
}

func TestStripFrontmatter(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"frontmatter and body", "---\nname: x\n---\nbody\n", "body"},
		{"body is trimmed", "---\nname: x\n---\n\n  body\n\n", "body"},
		{"no frontmatter", "\n  plain text \n", "plain text"},
		{"unterminated frontmatter is the whole text", "---\nname: x\nbody", "---\nname: x\nbody"},
		{"only the first block is frontmatter", "---\na: 1\n---\nx\n---\ny\n---\nz", "x\n---\ny\n---\nz"},
		{"CRLF does not match the frontmatter pattern", "---\r\nname: x\r\n---\r\nbody", "---\r\nname: x\r\n---\r\nbody"},
		{"empty block never matches", "---\n---\nbody", "---\n---\nbody"},
		// JavaScript's trim strips U+FEFF and U+00A0 from both ends but not U+0085 (NEL), so a trailing NEL keeps
		// the characters before it.
		{"U+FEFF and U+00A0 trim, U+0085 does not", "\ufeff\u00a0body\u00a0\ufeff\u0085", "body\u00a0\ufeff\u0085"},
		{"U+0085 at the start stays too", "\u0085 body", "\u0085 body"},
		{"U+2000 through U+200A trim", "\u2000\u2009\u200abody\u200a\u2000", "body"},
		{"a longer dash line does not close the frontmatter", "---\na: 1\n----\nb\n---\nbody", "body"},
	}
	for _, c := range cases {
		if got := stripFrontmatter(c.in); got != c.want {
			t.Errorf("%s: stripFrontmatter(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestMessageContainsBootstrap(t *testing.T) {
	m := bootstrapMarker
	cases := []struct {
		name    string
		message any
		want    bool
	}{
		{"string content with the marker", map[string]any{"content": "x " + m + " y"}, true},
		{"string content without it", map[string]any{"content": "x"}, false},
		{"text part with the marker", map[string]any{"content": []any{map[string]any{"type": "text", "text": m}}}, true},
		{"marker in a later part", map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b " + m}}}, true},
		{"marker in a part that is not text", map[string]any{"content": []any{map[string]any{"type": "image", "text": m}}}, false},
		{"text part whose text is not a string", map[string]any{"content": []any{map[string]any{"type": "text", "text": 3}}}, false},
		{"part that is not an object", map[string]any{"content": []any{"x", nil, 3}}, false},
		{"no content", map[string]any{"role": "compactionSummary", "summary": m}, false},
		{"content of another type", map[string]any{"content": 3}, false},
		{"not a message", "x", false},
		{"null", nil, false},
	}
	for _, c := range cases {
		if got := messageContainsBootstrap(c.message); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFirstNonCompactionSummaryIndex(t *testing.T) {
	s := map[string]any{"role": "compactionSummary"}
	u := map[string]any{"role": "user"}
	cases := []struct {
		name     string
		messages []any
		want     int
	}{
		{"empty", nil, 0},
		{"no summary", []any{u, s}, 0},
		{"one summary", []any{s, u}, 1},
		{"two summaries", []any{s, s, u, s}, 2},
		{"only summaries", []any{s, s}, 2},
		{"a message without a role", []any{map[string]any{}, s}, 0},
	}
	for _, c := range cases {
		if got := firstNonCompactionSummaryIndex(c.messages); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestInjectionCycle(t *testing.T) {
	h := startExt(t)
	user := userMessage("hello", 1)

	// A new extension injects before any session_start (injectBootstrap starts true).
	if got := fireContext(t, h, []any{user}); got.Unchanged || len(got.Messages) != 2 {
		t.Fatalf("first request: unchanged=%v messages=%d", got.Unchanged, len(got.Messages))
	}
	h.Fire("agent_end", nil)
	if got := fireContext(t, h, []any{user}); !got.Unchanged {
		t.Fatal("after agent_end the bootstrap must stop")
	}
	// session_start turns it on again, session_compact too.
	h.Fire("session_start", map[string]any{"reason": "new"})
	if got := fireContext(t, h, []any{user}); got.Unchanged {
		t.Fatal("session_start must re-enable the injection")
	}
	h.Fire("agent_end", nil)
	h.Fire("session_compact", nil)
	if got := fireContext(t, h, []any{user}); got.Unchanged {
		t.Fatal("session_compact must re-enable the injection")
	}
	// An agent_end that arrives twice does not break it.
	h.Fire("agent_end", nil)
	h.Fire("agent_end", nil)
	if got := fireContext(t, h, []any{user}); !got.Unchanged {
		t.Fatal("agent_end twice must stay off")
	}
}

func TestInjectionPlacement(t *testing.T) {
	h := startExt(t)
	s1 := map[string]any{"role": "compactionSummary", "summary": "a", "tokensBefore": 1, "timestamp": 1}
	s2 := map[string]any{"role": "compactionSummary", "summary": "b", "tokensBefore": 2, "timestamp": 2}
	u := userMessage("go", 3)
	a := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}}

	t.Run("before the first non-summary message", func(t *testing.T) {
		got := fireContext(t, h, []any{s1, s2, u, a})
		if len(got.Messages) != 5 || got.Messages[2]["role"] != "user" || !strings.Contains(textOf(got.Messages[2]), bootstrapMarker) {
			t.Fatalf("messages: %v", got.Messages)
		}
		sameJSON(t, []any{got.Messages[0], got.Messages[1], got.Messages[3], got.Messages[4]}, []any{s1, s2, u, a})
	})
	t.Run("an empty conversation gets the bootstrap alone", func(t *testing.T) {
		got := fireContext(t, h, []any{})
		if len(got.Messages) != 1 || !strings.Contains(textOf(got.Messages[0]), bootstrapMarker) {
			t.Fatalf("messages: %v", got.Messages)
		}
	})
	t.Run("a conversation of summaries gets it last", func(t *testing.T) {
		got := fireContext(t, h, []any{s1, s2})
		if len(got.Messages) != 3 || !strings.Contains(textOf(got.Messages[2]), bootstrapMarker) {
			t.Fatalf("messages: %v", got.Messages)
		}
	})
	t.Run("the message has a role, one text part and a millisecond timestamp", func(t *testing.T) {
		got := fireContext(t, h, []any{u})
		m := got.Messages[0]
		parts, _ := m["content"].([]any)
		if m["role"] != "user" || len(parts) != 1 || parts[0].(map[string]any)["type"] != "text" {
			t.Fatalf("message: %v", m)
		}
		if ts, _ := m["timestamp"].(float64); ts < 1e12 {
			t.Fatalf("timestamp %v is not milliseconds since the epoch", m["timestamp"])
		}
	})
	t.Run("a bootstrap in string content blocks the injection", func(t *testing.T) {
		carried := map[string]any{"role": "user", "content": "x " + bootstrapMarker, "timestamp": 1}
		if got := fireContext(t, h, []any{carried, u}); !got.Unchanged {
			t.Fatal("the bootstrap is already present")
		}
	})
	t.Run("the bootstrap in any message blocks it, wherever it is", func(t *testing.T) {
		carried := userMessage(bootstrapMarker, 1)
		if got := fireContext(t, h, []any{u, a, carried}); !got.Unchanged {
			t.Fatal("the bootstrap is already present")
		}
	})
}

func TestSkillsDir(t *testing.T) {
	discover := func(t *testing.T, h *Host) []string {
		t.Helper()
		raw := h.Fire("resources_discover", map[string]any{"reason": "startup"})
		var result struct {
			SkillPaths []string `json:"skillPaths"`
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatalf("result %s: %v", raw, err)
			}
		}
		return result.SkillPaths
	}
	t.Run("the directory above the shipped using-superpowers Skill", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "pkg", "skills")
		h := startWithCommands(t, skillCommands(dir))
		sameJSON(t, discover(t, h), []string{dir})
		var raw map[string]any
		if err := json.Unmarshal(h.Fire("resources_discover", map[string]any{"reason": "reload"}), &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["skillPaths"]; !ok {
			t.Errorf("the result's key is skillPaths, got %v", raw)
		}
	})
	t.Run("nothing when the host does not list the Skill", func(t *testing.T) {
		commands := skillCommands(t.TempDir())[:2]
		if got := discover(t, startWithCommands(t, commands)); got != nil {
			t.Errorf("skillPaths %v, want none", got)
		}
	})
	t.Run("nothing when the host lists no commands", func(t *testing.T) {
		if got := discover(t, startWithCommands(t, []any{})); got != nil {
			t.Errorf("skillPaths %v, want none", got)
		}
	})
	t.Run("a command of another source with the Skill's name is not the Skill", func(t *testing.T) {
		commands := []any{map[string]any{
			"name": bootstrapSkillName, "source": "prompt",
			"sourceInfo": map[string]any{"path": "/x/y/z.md", "source": "local", "scope": "user", "origin": "top-level"},
		}}
		if got := discover(t, startWithCommands(t, commands)); got != nil {
			t.Errorf("skillPaths %v, want none", got)
		}
	})
	t.Run("a Skill without a path is not usable", func(t *testing.T) {
		commands := []any{map[string]any{"name": bootstrapSkillName, "source": "skill", "sourceInfo": map[string]any{"path": "", "source": "local", "scope": "user", "origin": "top-level"}}}
		if got := discover(t, startWithCommands(t, commands)); got != nil {
			t.Errorf("skillPaths %v, want none", got)
		}
	})
	t.Run("the host asks for the commands once per discovery", func(t *testing.T) {
		h := startWithCommands(t, skillCommands(t.TempDir()))
		discover(t, h)
		if n := len(h.CallsTo("getCommands")); n != 1 {
			t.Errorf("getCommands called %d times, want 1", n)
		}
	})
	t.Run("a host error is the handler's error", func(t *testing.T) {
		h := StartHost(t, Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getCommands" {
				return nil, "no commands yet"
			}
			return map[string]any{}, ""
		}})
		if _, failure := h.roundTrip(map[string]any{"method": "event", "event": "resources_discover", "handler_id": h.handlers["resources_discover"], "args": json.RawMessage(`{"type":"resources_discover","reason":"startup"}`)}); failure == "" {
			t.Error("a getCommands error must surface from the handler")
		}
	})
}
