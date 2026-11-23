package pi_subagents_test

// The three run options the port honors, through the tool (runopts_test.go has the rules and texts): `timeoutMs` /
// `maxRuntimeMs` ends the run at its deadline with the original's terminal message, `checkpointBeforeDeadlineMs` is
// accepted on an async single run, and `output` binds the answer to a file and tells the child where it goes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// toolContent is the tool result's text, its JSON escapes decoded (a Windows path is escaped in the raw bytes).
func toolContent(t *testing.T, raw []byte) string {
	t.Helper()
	var result struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	return result.Content
}

// readBody is the file the run wrote.
func readBody(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// savedOutputPath pulls the path out of the original's `Output saved to: <path> (<size>, <n> lines)` line.
func savedOutputPath(t *testing.T, content string) string {
	t.Helper()
	text := content
	start := strings.Index(text, "Output saved to: ")
	if start < 0 {
		t.Fatalf("no output reference in %q", text)
	}
	rest := text[start+len("Output saved to: "):]
	end := strings.Index(rest, " (")
	if end < 0 {
		t.Fatalf("malformed output reference in %q", text)
	}
	return rest[:end]
}

func TestTimeoutMsIsValidatedLikeTheOriginal(t *testing.T) {
	h, calls := refusedHost(t)
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"timeoutMs": 0}, "timeoutMs must be a positive integer."},
		{map[string]any{"timeoutMs": -1}, "timeoutMs must be a positive integer."},
		{map[string]any{"timeoutMs": "soon"}, "timeoutMs must be a positive integer."},
		{map[string]any{"maxRuntimeMs": 2_147_483_648}, "maxRuntimeMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"timeoutMs": 1_000, "maxRuntimeMs": 2_000}, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both."},
		{map[string]any{"checkpointBeforeDeadlineMs": 0}, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"output": 7}, "output must be a string or a boolean when provided."},
	} {
		params := map[string]any{"agent": "helper", "task": "t"}
		for k, v := range c.params {
			params[k] = v
		}
		_, failure := h.Tool("subagent", params)
		eq(t, failure, c.want, "the original's message")
	}
	eq(t, len(*calls), 0, "no child started")
}

// A run ends at its deadline: the child is stopped and the call fails with the original's terminal timeout text.
func TestTimeoutMsEndsTheRunAtItsDeadline(t *testing.T) {
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		<-done
		return sub.ChildResult{}, sub.ErrCancelled
	})
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	start := time.Now()
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "timeoutMs": 250})
	elapsed := time.Since(start)
	eq(t, failure, "Subagent timed out after 250ms.", "the deadline's terminal failure")
	if elapsed < 200*time.Millisecond {
		t.Errorf("the run ended after %v, before its deadline", elapsed)
	}

	// the alias behaves the same, and the two agree
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "maxRuntimeMs": 150})
	eq(t, failure, "Subagent timed out after 150ms.", "maxRuntimeMs")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "timeoutMs": 150, "maxRuntimeMs": 150})
	eq(t, failure, "Subagent timed out after 150ms.", "both aliases together")
}

// `output` binds the answer to a file: the child is told where to write it (the original's instruction, in the task),
// the answer lands there, and the result carries the original's reference line.
func TestOutputBindsTheAnswerToAFile(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"report": "the findings"})
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "write the report", "output": "notes/report.md"})
	eq(t, failure, "", "the run")
	content := toolContent(t, raw)
	path := savedOutputPath(t, content)
	if !filepath.IsAbs(path) {
		t.Errorf("the saved path is absolute: %q", path)
	}
	if !strings.Contains(filepath.ToSlash(path), "/outputs/") {
		t.Errorf("a relative output lands under the run's outputs directory: %q", path)
	}
	body := readBody(t, path)
	eq(t, body, "the findings", "the answer is the file")
	if !strings.Contains(content, "Output saved to: "+path+" (") || !strings.Contains(content, ". Read this file if needed.") {
		t.Errorf("the reference line: %s", content)
	}
	// the child was told the authoritative path, as the original's instruction does
	stdin := (*calls)[0].Stdin
	if !strings.Contains(stdin, "**Output:**\nWrite your findings to exactly this path: "+path) ||
		!strings.Contains(stdin, "This path is authoritative for this run.") ||
		!strings.Contains(stdin, "Ignore any other output filename or output path mentioned elsewhere") {
		t.Errorf("the output instruction: %q", stdin)
	}
}

// An agent's own `output` frontmatter is the default file (and `output: false` turns it off); a read-only child gets
// the original's "the runtime persists it" wording instead of being told to write.
func TestAgentOutputFrontmatterIsTheDefault(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"draft": "draft body"})
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "writer.md"),
		"---\nname: writer\ndescription: Writes\ntools: read, grep\noutput: draft.md\n---\n\nYou write.\n")
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	raw, failure := h.Tool("subagent", map[string]any{"agent": "writer", "task": "draft it"})
	eq(t, failure, "", "the agent's own output is used")
	content := toolContent(t, raw)
	path := savedOutputPath(t, content)
	eq(t, readBody(t, path), "draft body", "the answer is the file")
	if !strings.Contains((*calls)[0].Stdin, "The runtime will persist it to exactly this path: "+path) {
		t.Errorf("the read-only instruction: %q", (*calls)[0].Stdin)
	}

	// `get` shows it, as the original's detail does
	detail, failure := h.Tool("subagent", map[string]any{"action": "get", "agent": "writer"})
	eq(t, failure, "", "get")
	if !strings.Contains(string(detail), "Output: draft.md") {
		t.Errorf("get: %s", detail)
	}

	// output: false turns the file off: no instruction, no reference
	raw, failure = h.Tool("subagent", map[string]any{"agent": "writer", "task": "draft it", "output": false})
	eq(t, failure, "", "output: false")
	if strings.Contains(toolContent(t, raw), "Output saved to:") {
		t.Errorf("no file was asked for: %s", raw)
	}
	if strings.Contains((*calls)[1].Stdin, "**Output:**") {
		t.Errorf("no instruction without an output file: %q", (*calls)[1].Stdin)
	}
}

// `checkpointBeforeDeadlineMs` is accepted on an async single run (where the original steers it) and on a foreground
// run (where the original ignores it); neither is refused.
func TestCheckpointBeforeDeadlineMsIsAccepted(t *testing.T) {
	started := make(chan struct{}, 4)
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		return sub.ChildResult{Stdout: "ok"}, nil
	})
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": true,
		"timeoutMs": 60_000, "checkpointBeforeDeadlineMs": 30_000})
	eq(t, failure, "", "an async run with a checkpoint lead")
	if !strings.Contains(toolContent(t, raw), "Async: helper [run-") {
		t.Errorf("the async receipt: %s", raw)
	}
	select { // the detached child ran
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the detached child never ran")
	}

	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "checkpointBeforeDeadlineMs": 30_000})
	eq(t, failure, "", "a foreground run accepts it too (the original steers the checkpoint on async runs only)")
}
