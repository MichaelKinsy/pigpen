package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// startObsRig is startRigIn with the host answering getSessionID, which the transcript path needs.
func startObsRig(t *testing.T, cwd string, starter ...childStarter) *rig {
	t.Helper()
	return startObsRigWith(t, cwd, "", starter...)
}

// startObsRigWith is startObsRig whose host fails getSessionID with sessionFailure when it is not "".
func startObsRigWith(t *testing.T, cwd, sessionFailure string, starter ...childStarter) *rig {
	t.Helper()
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	start := childStarter(r.fleet.start)
	if len(starter) > 0 {
		start = starter[0]
	}
	r.app = newApp(e, start)
	r.app.register(e)
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		if method == "getSessionID" {
			if sessionFailure != "" {
				return nil, sessionFailure
			}
			return obj{"sessionId": "session-1"}, ""
		}
		return r.answer(method, args)
	}})
	r.FireAll("session_start", obj{"reason": "startup"})
	t.Cleanup(func() { r.app.mgr.abortAll() })
	return r
}

// obsWorkspace writes a workspace with an optional project subagents.json and one agent file, "custom".
func obsWorkspace(t *testing.T, settingsJSON, frontmatter string) string {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir()) // transcripts land under the temp dir
	cwd := t.TempDir()
	if settingsJSON != "" {
		must(t, os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755))
		must(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(settingsJSON), 0o644))
	}
	if frontmatter != "" {
		dir := filepath.Join(cwd, ".pi", "agents")
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "custom.md"), []byte("---\ndescription: a custom agent\n"+frontmatter+"\n---\nDo the work.\n"), 0o644))
	}
	return cwd
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// readEntries reads a transcript's JSON lines.
func readEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var m map[string]any
		must(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

// fakeSession is the test double of the original's makeFakeSession: it feeds a transcriptStream the events a child
// emits. push appends messages (each a message_end), fire sends any other event.
type fakeSession struct{ stream *transcriptStream }

func newFakeSession(stream *transcriptStream, initial ...map[string]any) *fakeSession {
	s := &fakeSession{stream: stream}
	s.push(initial...)
	return s
}

func (s *fakeSession) push(msgs ...map[string]any) {
	for _, m := range msgs {
		s.stream.onEvent(obj{"type": "message_end", "message": m})
	}
}
func (s *fakeSession) fire(ev obj) { s.stream.onEvent(ev) }

// bus collects the payloads emitted on a channel.
type bus struct {
	mu   sync.Mutex
	data []obj
}

func (b *bus) all() []obj {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]obj{}, b.data...)
}

func (r *rig) collect(channel string) *bus {
	b := &bus{}
	r.onBus(channel, func(d any) {
		m, _ := d.(map[string]any)
		b.mu.Lock()
		b.data = append(b.data, m)
		b.mu.Unlock()
	})
	return b
}

func (b *bus) wait(t *testing.T, n int) []obj {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if got := b.all(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d events", n)
	return nil
}

// toolRaw runs a tool and returns the whole result object (content and usage), or the failure.
func (r *rig) toolRaw(name string, params obj, callID string) (obj, string) {
	r.t.Helper()
	argv, _ := json.Marshal(params)
	raw, failure := r.Host.roundTrip(obj{"method": "tool_call", "tool": name, "tool_call_id": callID, "args": json.RawMessage(argv)})
	if failure != "" {
		return nil, failure
	}
	var out obj
	must(r.t, json.Unmarshal(raw, &out))
	return out, ""
}

// spend feeds one assistant message's usage into the child's stream, as a real child's message_end would.
func (c *fakeChild) spend(u lifetimeUsage) { c.spec.Hooks.Usage(u) }

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
