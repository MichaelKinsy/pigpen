package mapper_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// recorded is one captured pi event stream (upstream test/fixtures/*.json, recorded from a real
// model and scrubbed).
type recorded struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Events      []obj  `json:"events"`
}

// expectedFixtures is the manifest of upstream's recorded corpus: a missing, extra or renamed
// fixture fails the load, as in upstream's loadRecordedFixtures.
var expectedFixtures = []string{
	"abort", "bash-long-output", "compaction", "parallel-tools", "plain-text", "single-tool", "steering",
	"tool-bash", "tool-edit", "tool-error", "tool-find", "tool-grep", "tool-loop", "tool-ls", "tool-write",
}

func loadRecorded(t testing.TB) []recorded {
	t.Helper()
	files, err := filepath.Glob("testdata/fixtures/*.json")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	sort.Strings(names)
	want := append([]string(nil), expectedFixtures...)
	sort.Strings(want)
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("recorded fixture set differs from the manifest; expected=%v actual=%v", want, names)
	}
	var out []recorded
	for _, n := range expectedFixtures {
		raw, err := os.ReadFile(filepath.Join("testdata/fixtures", n+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var r recorded
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if r.Name != n || r.Prompt == "" || len(r.Events) == 0 {
			t.Fatalf("%s: malformed fixture", n)
		}
		out = append(out, r)
	}
	return out
}

func replayFixture(t testing.TB, r recorded) replayed {
	t.Helper()
	m := mapper.NewTurnMapper("replay-turn", 0)
	actions := []ahptypes.StateAction{mapper.UserTurnStarted("replay-turn", r.Prompt, "1970-01-01T00:00:00.000Z")}
	for _, e := range r.Events {
		actions = append(actions, m.Handle(e)...)
	}
	return replayed{actions: actions, state: reduce(t, actions)}
}

func markdownText(state obj) string {
	return strings.Join(contents(partsOfKind(state, "markdown")), "")
}

func toolResultText(call obj) string {
	var sb strings.Builder
	for _, b := range list(call, "content") {
		if str(b, "type") == "text" {
			sb.WriteString(str(b, "text"))
		}
	}
	return sb.String()
}

func assertReplayInvariants(t *testing.T, r recorded, rep replayed) {
	t.Helper()
	opened := len(ofType(rep.actions, "chat/turnStarted"))
	terminators := len(ofType(rep.actions, "chat/turnComplete")) + len(ofType(rep.actions, "chat/turnCancelled")) + len(ofType(rep.actions, "chat/error"))
	// More than one turn means pi injected a message mid-run; each still has to terminate exactly once.
	if terminators != opened {
		t.Fatalf("%s: every opened turn must terminate once (%d opened, %d terminated)", r.Name, opened, terminators)
	}
	if rep.state["activeTurn"] != nil {
		t.Fatalf("%s: active turn remained after replay", r.Name)
	}
	if n := len(list(rep.state, "turns")); n != opened {
		t.Fatalf("%s: reduced turn count %d differs from opened turns %d", r.Name, n, opened)
	}
	assertActionsValid(t, rep.actions, r.Name)
	// Every tool call ends completed or cancelled: none is left running.
	for _, turn := range list(rep.state, "turns") {
		for _, p := range list(turn, "responseParts") {
			if str(p, "kind") == "toolCall" {
				if st := str(p, "toolCall", "status"); st != "completed" && st != "cancelled" {
					t.Fatalf("%s: %s ended in %s", r.Name, str(p, "toolCall", "toolName"), st)
				}
			}
		}
	}
	// A delta naming an unknown partId is a silent reducer no-op, so validate creation order as
	// well as uniqueness.
	created := map[string]bool{}
	var ids []string
	for _, a := range rep.actions {
		o := asObj(a)
		switch o["type"] {
		case "chat/responsePart":
			if id := str(o, "part", "id"); id != "" {
				ids = append(ids, id)
				created[id] = true
			}
		case "chat/delta", "chat/reasoning":
			if !created[str(o, "partId")] {
				t.Fatalf("%s: delta targets unknown part %s", r.Name, str(o, "partId"))
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("%s: response part ids collided within a turn: %s", r.Name, id)
		}
		seen[id] = true
	}
}

// Twins of upstream test/mapper-fixtures.test.ts.
func TestRecordedStreamReplay(t *testing.T) {
	fixtures := loadRecorded(t)
	byName := map[string]replayed{}
	src := map[string]recorded{}
	for _, r := range fixtures {
		byName[r.Name] = replayFixture(t, r)
		src[r.Name] = r
	}

	// One literal twin per recorded stream (the twin gate reads titles from source).
	twin.Run(t, "mapper-fixtures", "plain-text — A reply with no tool calls — the simplest possible turn.", func(t *testing.T) {
		assertReplayInvariants(t, src["plain-text"], byName["plain-text"])
	})

	twin.Run(t, "mapper-fixtures", "single-tool — One tool call, then an answer derived from its result.", func(t *testing.T) {
		assertReplayInvariants(t, src["single-tool"], byName["single-tool"])
	})

	twin.Run(t, "mapper-fixtures", "parallel-tools — Several tool calls in one assistant message — exercises contentIndex fan-out.", func(t *testing.T) {
		assertReplayInvariants(t, src["parallel-tools"], byName["parallel-tools"])
	})

	twin.Run(t, "mapper-fixtures", "tool-loop — Two or more assistant messages in one turn — the case where contentIndex restarts and partIds must not collide.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-loop"], byName["tool-loop"])
	})

	twin.Run(t, "mapper-fixtures", "tool-error — A failing tool call the agent has to recover from.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-error"], byName["tool-error"])
	})

	twin.Run(t, "mapper-fixtures", "abort — A turn cancelled while it is running.", func(t *testing.T) {
		assertReplayInvariants(t, src["abort"], byName["abort"])
	})

	twin.Run(t, "mapper-fixtures", "steering — A steering message injected into a running turn.", func(t *testing.T) {
		assertReplayInvariants(t, src["steering"], byName["steering"])
	})

	twin.Run(t, "mapper-fixtures", "tool-edit — An `edit` call — the one tool whose result carries a diff and a patch.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-edit"], byName["tool-edit"])
	})

	twin.Run(t, "mapper-fixtures", "tool-write — A `write` call — a whole new file rather than an edit to one.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-write"], byName["tool-write"])
	})

	twin.Run(t, "mapper-fixtures", "tool-bash — A `bash` call — stdout, exit status, and pi's truncation metadata.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-bash"], byName["tool-bash"])
	})

	twin.Run(t, "mapper-fixtures", "tool-ls — An `ls` call — a directory listing, which reports its own entry limit.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-ls"], byName["tool-ls"])
	})

	twin.Run(t, "mapper-fixtures", "tool-grep — A `grep` call — match counts and line truncation live in its details.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-grep"], byName["tool-grep"])
	})

	twin.Run(t, "mapper-fixtures", "tool-find — A `find` call — path globbing, with its own result limit.", func(t *testing.T) {
		assertReplayInvariants(t, src["tool-find"], byName["tool-find"])
	})

	twin.Run(t, "mapper-fixtures", "compaction — A manual compaction — `buildContextEntries` starts from the newest one, so this is the boundary history rebuilding and turn paging are built around.", func(t *testing.T) {
		assertReplayInvariants(t, src["compaction"], byName["compaction"])
	})

	twin.Run(t, "mapper-fixtures", "bash-long-output — A bash call whose output is large enough for pi to stream updates and report truncation.", func(t *testing.T) {
		assertReplayInvariants(t, src["bash-long-output"], byName["bash-long-output"])
	})

	twin.Run(t, "mapper-fixtures", "plain-text: answers with no tool calls", func(t *testing.T) {
		st := byName["plain-text"].state
		if got := str(st, "turns", 0, "state"); got != "complete" {
			t.Fatal(got)
		}
		if n := len(toolCalls(st)); n != 0 {
			t.Fatalf("tool calls = %d", n)
		}
		if !regexp.MustCompile(`(?i)PONG`).MatchString(markdownText(st)) {
			t.Fatal(markdownText(st))
		}
	})

	twin.Run(t, "mapper-fixtures", "describes a tool call in terms a user can act on", func(t *testing.T) {
		// A heading, a line while it runs and a line once it has: a message that only repeats the
		// tool's name spends all three on saying `read` three times.
		for _, r := range fixtures {
			for _, turn := range list(byName[r.Name].state, "turns") {
				for _, p := range list(turn, "responseParts") {
					call, _ := get(p, "toolCall").(obj)
					if call == nil {
						continue
					}
					if in := str(call, "toolInput"); in == "" || in == "{}" {
						continue
					}
					if str(call, "invocationMessage") == str(call, "toolName") {
						t.Fatalf("%s: %s announces itself with nothing but its own name", r.Name, str(call, "toolName"))
					}
					// Identical text leaves a completed call still claiming to be running.
					if str(call, "pastTenseMessage") == str(call, "invocationMessage") {
						t.Fatalf("%s: %s still says it is %s after it finished", r.Name, str(call, "toolName"), str(call, "invocationMessage"))
					}
				}
			}
		}
	})

	twin.Run(t, "mapper-fixtures", "shows a tool's subject rather than its arguments as JSON", func(t *testing.T) {
		for _, r := range fixtures {
			for _, turn := range list(byName[r.Name].state, "turns") {
				for _, p := range list(turn, "responseParts") {
					call, _ := get(p, "toolCall").(obj)
					if call == nil {
						continue
					}
					if in, ok := call["toolInput"].(string); ok && strings.HasPrefix(in, "{") {
						t.Fatalf("%s: %s shows its arguments as JSON: %.60s", r.Name, str(call, "toolName"), in)
					}
				}
			}
		}
	})

	twin.Run(t, "mapper-fixtures", "tool-edit: hands the client the patch, not a count of edited blocks", func(t *testing.T) {
		var edits []obj
		for _, c := range toolCalls(byName["tool-edit"].state) {
			if str(c, "toolName") == "edit" {
				edits = append(edits, c)
			}
		}
		if len(edits) < 1 {
			t.Fatal("expected an edit call")
		}
		for _, c := range edits {
			shown := toolResultText(c)
			if !regexp.MustCompile(`^--- |\n--- `).MatchString(shown) {
				t.Fatalf("edit result carries no patch: %.80s", shown)
			}
			if !regexp.MustCompile(`(?m)^\+.*$`).MatchString(shown) {
				t.Fatal("a patch with no added lines is not a patch")
			}
		}
	})

	twin.Run(t, "mapper-fixtures", "single-tool: runs one tool and answers from its result", func(t *testing.T) {
		st := byName["single-tool"].state
		if got := str(st, "turns", 0, "state"); got != "complete" {
			t.Fatal(got)
		}
		if len(toolCalls(st)) < 1 {
			t.Fatal("no tool call")
		}
		if !strings.Contains(markdownText(st), "ALPHA BETA GAMMA") {
			t.Fatal(markdownText(st))
		}
	})

	twin.Run(t, "mapper-fixtures", "parallel-tools: keeps several tool calls in one message distinct", func(t *testing.T) {
		st := byName["parallel-tools"].state
		if n := len(toolCalls(st)); n < 2 {
			t.Fatalf("expected multiple tool calls, got %d", n)
		}
		if !strings.Contains(markdownText(st), "FIRST") || !strings.Contains(markdownText(st), "SECOND") {
			t.Fatal(markdownText(st))
		}
	})

	twin.Run(t, "mapper-fixtures", "tool-loop: spans several assistant messages without part collisions", func(t *testing.T) {
		assistant := 0
		for _, e := range src["tool-loop"].Events {
			if e["type"] == "message_start" && str(e, "message", "role") == "assistant" {
				assistant++
			}
		}
		// The scenario exists to produce more than one assistant message, where contentIndex restarts at 0.
		if assistant < 2 {
			t.Fatalf("expected multiple assistant messages, got %d", assistant)
		}
		st := byName["tool-loop"].state
		if got := str(st, "turns", 0, "state"); got != "complete" {
			t.Fatal(got)
		}
		if !strings.Contains(markdownText(st), "42") {
			t.Fatal(markdownText(st))
		}
	})

	twin.Run(t, "mapper-fixtures", "tool-error: surfaces a failed tool without failing the turn", func(t *testing.T) {
		st := byName["tool-error"].state
		var failed []obj
		for _, c := range toolCalls(st) {
			if c["success"] == false {
				failed = append(failed, c)
			}
		}
		if len(failed) < 1 {
			t.Fatal("expected a failed tool call")
		}
		// What the client shows for a failure is error.message: pi says why (an ENOENT naming the
		// path); repeating the tool's name tells the user nothing.
		for _, c := range failed {
			reported := toolResultText(c)
			if reported == "" {
				t.Fatalf("%s: no failure text to show", str(c, "toolName"))
			}
			if got := str(c, "error", "message"); got != reported {
				t.Fatalf("%s: error.message %q should carry what pi reported %q", str(c, "toolName"), got, reported)
			}
		}
		if got := str(st, "turns", 0, "state"); got != "complete" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "mapper-fixtures", "abort: ends the turn as cancelled", func(t *testing.T) {
		if got := str(byName["abort"].state, "turns", 0, "state"); got != "cancelled" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "mapper-fixtures", "steering: the injected message becomes its own turn", func(t *testing.T) {
		injected := 0
		for _, e := range src["steering"].Events {
			if e["type"] == "message_start" && str(e, "message", "role") == "user" {
				injected++
			}
		}
		if injected < 2 {
			t.Fatal("expected the steering message to appear mid-run")
		}
		st := byName["steering"].state
		// pi stores an injected message as an ordinary user message, so a rebuild from disk makes it
		// a turn; the live path matches.
		if n := len(list(st, "turns")); n != injected {
			t.Fatalf("turns = %d, injected = %d", n, injected)
		}
		if !strings.Contains(str(st, "turns", -1, "message", "text"), "Stop counting") {
			t.Fatal(str(st, "turns", -1, "message", "text"))
		}
	})
}
