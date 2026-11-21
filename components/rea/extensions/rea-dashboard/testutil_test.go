package readashboard

import (
	"context"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fakeRunner answers `doctor --json` and `inspect-artifact --json` from testdata and counts the runs.
type fakeRunner struct {
	t       *testing.T
	mu      sync.Mutex
	doctor  int
	inspect []string
	failure error
}

func (f *fakeRunner) run(_ context.Context, _ reaServer, args []string, _ int) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return nil, false, f.failure
	}
	switch args[0] {
	case "doctor":
		f.doctor++
		return readTestdata(f.t, "doctor.json"), false, nil
	case "inspect-artifact":
		f.inspect = append(f.inspect, args[len(args)-1])
		return readTestdata(f.t, "inspect-artifact.json"), false, nil
	}
	return nil, false, nil
}

func (f *fakeRunner) counts() (doctor, inspect int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.doctor, len(f.inspect)
}

var testServer = reaServer{
	Present: true, Node: "/p/node/bin/node", Entry: "/p/rea/scripts/rea.mjs", Dir: "/p",
	Payload: payloadInfo{
		Target: "darwin/arm64", Entry: "rea/scripts/rea.mjs", TreeSha256: "de42ae95b0cab20ebfc810d80b21a4315c51127a513b672a3ed35e99656e55f9",
		Files: 8965, Bytes: 270558811,
	},
}

func init() {
	testServer.Payload.Rea.Package = "rea-agents"
	testServer.Payload.Rea.Version = "6.8.0"
	testServer.Payload.Rea.Tag = "rea-agents-6.8.0"
	testServer.Payload.Node.Version = "24.22.2"
}

const resultJSON = `{"result":{"evidence":[{"evidence_id":"ev_aaa"},{"evidence_id":"ev_bbb"}],"limitations":["Hopper is not configured","Symbols are stripped"]}}`

func textResult(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

// newTestDashboard has a registered server, a finished doctor and a few calls.
func newTestDashboard(t *testing.T) (*dashboard, *fakeRunner) {
	t.Helper()
	fr := &fakeRunner{t: t}
	d := newDashboard(fr.run, func() time.Time { return fixedNow })
	t.Cleanup(d.shutdown)
	d.loadedAt = fixedNow.Add(-time.Hour)
	d.server = testServer
	d.harness = "0.1.0"
	report, err := parseDoctor(readTestdata(t, "doctor.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.doctor = doctorState{Status: doctorDone, Report: report}
	d.doctorKey = testServer.identity()
	return d, fr
}

func addCalls(d *dashboard) {
	start := fixedNow.Add(-time.Minute)
	for i, c := range []struct {
		id, parent, tool, path string
		err                    bool
		result                 any
	}{
		{"c1", "", "open_binary", "/bin/ls", false, textResult(`{"result":{"path":"/bin/ls"}}`)},
		{"c2/1", "c2", "inspect_artifact", "/bin/ls", false, textResult(resultJSON)},
		{"c3/1", "c2", "list_strings", "/Applications/Foo.app", true, textResult(`{"error":{"code":"x","message":"no such session"}}`)},
	} {
		ev := toolEvent{ID: c.id, Parent: c.parent, Tool: c.tool, Args: map[string]any{"path": c.path}, Result: c.result, IsError: c.err}
		d.store.start(ev, start.Add(time.Duration(i)*time.Second))
		ms := int64(120 + i)
		ev.DurationMs = &ms
		d.store.end(ev, start.Add(time.Duration(i)*time.Second+time.Second))
	}
}

func plainLines(lines []string) string { return stripANSI(strings.Join(lines, "\n")) }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func registered(command string, args ...string) []sdk.RegisteredMcpServer {
	return []sdk.RegisteredMcpServer{
		{Name: "other", Config: sdk.McpServerConfig{Command: "/bin/other"}},
		{Name: "rea", Config: sdk.McpServerConfig{Type: "stdio", Command: command, Args: append(args, "mcp")}},
	}
}
