package eq

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func shaHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestFakeFeatureHost is the fake host's process, not a test (see TestFakeOrderHost): it implements just enough of
// the RPC protocol to drive the scenario features end to end, and records what reached it in the workspace
// (received.log: the commands in arrival order; args.txt: its command line).
func TestFakeFeatureHost(t *testing.T) {
	if os.Getenv("EQ_FAKE_FEATURE_HOST") == "" {
		t.Skip("runs as the fake host of TestScenarioFeaturesEndToEnd")
	}
	var outMu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		outMu.Lock()
		defer outMu.Unlock()
		os.Stdout.Write(append(b, '\n'))
	}
	respond := func(id any, command string, data any) {
		m := map[string]any{"type": "response", "id": id, "command": command, "success": true}
		if data != nil {
			m["data"] = data
		}
		write(m)
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	_ = os.WriteFile("args.txt", []byte(strings.Join(args, "\n")+"\n"), 0o644)
	baseURL := func() string {
		b, err := os.ReadFile(filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "models.json"))
		if err != nil {
			panic(err)
		}
		var models struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
			} `json:"providers"`
		}
		_ = json.Unmarshal(b, &models)
		return models.Providers["eq-llm"].BaseURL
	}
	log := func(line string) {
		f, err := os.OpenFile("received.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		fmt.Fprintln(f, line)
	}
	var stopMu sync.Mutex
	var stop context.CancelFunc = func() {}
	run := func(ctx context.Context) {
		write(map[string]any{"type": "agent_start"})
		req, _ := http.NewRequestWithContext(ctx, "POST", baseURL()+"/chat/completions", strings.NewReader(`{"model":"eq-1","messages":[{"role":"user","content":"next"}]}`))
		resp, err := http.DefaultClient.Do(req)
		reason := "stop"
		if err != nil {
			reason = "aborted"
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		write(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": reason, "content": []any{map[string]any{"type": "text", "text": "done"}}}})
		write(map[string]any{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false, "result": map[string]any{"summary": "S", "firstKeptEntryId": "a1b2c3d4"}})
		write(map[string]any{"type": "agent_end"})
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var cmd map[string]any
		if json.Unmarshal(in.Bytes(), &cmd) != nil {
			continue
		}
		typ, _ := cmd["type"].(string)
		switch typ {
		case "get_state":
			respond(cmd["id"], typ, map[string]any{"sessionId": "x"})
		case "get_entries":
			log(typ)
			respond(cmd["id"], typ, map[string]any{"entries": []any{
				map[string]any{"id": "a1b2c3d4", "type": "message", "message": map[string]any{"role": "user"}},
				map[string]any{"id": "e5f6a7b8", "type": "message", "message": map[string]any{"role": "assistant"}},
			}, "leafId": "e5f6a7b8"})
		case "prompt":
			log(fmt.Sprintf("prompt %v", cmd["message"]))
			respond(cmd["id"], typ, nil)
			ctx, cancel := context.WithCancel(context.Background())
			stopMu.Lock()
			stop = cancel
			stopMu.Unlock()
			go run(ctx)
		case "abort":
			log("abort")
			stopMu.Lock()
			stop()
			stopMu.Unlock()
			respond(cmd["id"], typ, nil)
		default:
			log(fmt.Sprintf("%s %v %v", typ, cmd["message"], cmd["sessionPath"]))
			respond(cmd["id"], typ, nil)
		}
	}
	os.Exit(0)
}

// fakeFeatureHost writes the wrapper script that runs this test binary as the fake host, and answers the host's
// baseline system-text lane with an empty text (the fake host has none).
func fakeFeatureHost(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake host is started by a POSIX shell script")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "pig")
	script := fmt.Sprintf("#!/bin/sh\nEQ_FAKE_FEATURE_HOST=1 exec %q -test.run='^TestFakeFeatureHost$' -test.count=1 -- \"$@\"\n", exe)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	key := "pig\x00" + bin + "\x00"
	baselineMu.Lock()
	baselineCache[key] = ""
	baselineMu.Unlock()
	t.Cleanup(func() {
		baselineMu.Lock()
		delete(baselineCache, key)
		baselineMu.Unlock()
	})
	return bin
}

func runFeatureLane(t *testing.T, body string) *Trace {
	t.Helper()
	sc, err := loadSC(t, body)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := RunLane(sc, Lane{Name: "pig-go", Host: "pig", Bin: fakeFeatureHost(t)}, Options{StartupTimeout: 30 * time.Second, StepTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func events(tr *Trace, ch string) []string {
	var out []string
	for _, e := range tr.Events {
		if e.Ch == ch {
			out = append(out, string(e.Data))
		}
	}
	return out
}

func indexOf(tr *Trace, ch, contains string) int {
	for i, e := range tr.Events {
		if e.Ch == ch && strings.Contains(string(e.Data), contains) {
			return i
		}
	}
	return -1
}

func TestScenarioFeaturesEndToEnd(t *testing.T) {
	tr := runFeatureLane(t, `{
	  "name": "features", "settleMs": 1, "tailMs": -1,
	  "model": {"contextWindow": 2000, "maxTokens": 100, "input": ["text", "image"]},
	  "models": [{"id": "small", "contextWindow": 500}],
	  "settings": {"compaction": {"enabled": true, "reserveTokens": 200, "keepRecentTokens": 1}, "retry": {"enabled": false}},
	  "session": {"files": {"seed.jsonl": "{\"type\":\"session\",\"id\":\"0d0c7e0e-1c3e-4d8a-9b57-8a1e5d9d9c11\"}\n"}},
	  "extensions": [
	    {"name": "after.ts", "source": "export default function(pi){}"},
	    {"name": "before.ts", "source": "export default function(pi){}", "position": "before"}],
	  "capture": ["response", "agent_start", "agent_end", "message_end", "compaction_end"],
	  "llm": [
	    {"text": "one", "hold": "go", "usage": {"input": 9, "output": 3}},
	    {"text": "two", "hold": "stall"}],
	  "steps": [
	    {"name": "entries", "rpc": {"type": "get_entries"}, "save": {"user": "data.entries.[message.role=user].id", "leaf": "data.leafId"}},
	    {"name": "switch", "rpc": {"type": "switch_session", "sessionPath": "{{sessions}}/seed.jsonl"}},
	    {"name": "queue", "rpc": {"type": "prompt", "message": "first"},
	     "triggers": [{"when": {"event": "agent_start"}, "rpc": {"type": "steer", "message": "queued {{var:user}}"}, "release": ["go"]}]},
	    {"name": "abort", "rpc": {"type": "prompt", "message": "second"},
	     "triggers": [{"when": {"llmRequest": 2}, "rpc": {"type": "abort"}}],
	     "captureFiles": ["received.log"]}
	  ],
	  "captureFiles": ["args.txt", "agent:settings.json", "agent:models.json", "sessions:seed.jsonl"]
	}`)
	if e, failed := tr.Failed(); failed {
		t.Fatalf("lane failed: %s", e.Data)
	}
	// The steer reached the host before the held model turn answered.
	steer := indexOf(tr, ChResponse, `"command":"steer"`)
	msgEnd := indexOf(tr, ChHost, `"type":"message_end"`)
	if steer < 0 || msgEnd < 0 || steer > msgEnd {
		t.Errorf("steer response at %d, first message_end at %d", steer, msgEnd)
	}
	if got := events(tr, ChLLM); len(got) != 2 || !strings.Contains(got[0], `"model":"eq-1"`) {
		t.Errorf("llm events %q", got)
	}
	// The abort cancelled the stalled second turn: the host reports it as aborted.
	if indexOf(tr, ChHost, `"stopReason":"aborted"`) < 0 {
		t.Error("no aborted message_end")
	}
	// get_entries is captured here, with its ids relabeled in sorted-key order, and the later whole compaction_end
	// refers to the same entry by the same label.
	if indexOf(tr, ChResponse, `"data":{"entries":[{"id":"<id:1>","message":{"role":"user"},"type":"message"},{"id":"<id:2>"`) < 0 ||
		indexOf(tr, ChHost, `"firstKeptEntryId":"<id:1>"`) < 0 {
		t.Errorf("relabeled ids: %q %q", events(tr, ChResponse), events(tr, ChHost))
	}
	files := events(tr, ChFiles)
	if len(files) != 5 {
		t.Fatalf("%d files events: %q", len(files), files)
	}
	for i, e := range tr.Events {
		if e.Ch == ChFiles {
			if e.Step != "04-abort" {
				t.Errorf("the per-step capture is recorded under %q", e.Step)
			}
			break
		}
		_ = i
	}
	wantLog := `{"capture":"workspace:received.log","files":[{"path":"received.log","text":"get_entries\nswitch_session <nil> <tmp>/sessions/seed.jsonl\nprompt first\nsteer queued a1b2c3d4 <nil>\nprompt second\nabort\n"}]}`
	if files[0] != wantLog {
		t.Errorf("received.log\n got %s\nwant %s", files[0], wantLog)
	}
	for _, frag := range []string{`"text":"--mode\nrpc\n--session-dir\n<tmp>/sessions\n--no-extensions`, `-e\n<tmp>/helpers/before.ts\n-e\n<tmp>/helpers/after.ts`} {
		if !strings.Contains(files[1], frag) {
			t.Errorf("args.txt lacks %s: %s", frag, files[1])
		}
	}
	if strings.Contains(files[1], "--no-session") {
		t.Errorf("a session scenario still passed --no-session: %s", files[1])
	}
	if !strings.Contains(files[2], `\"reserveTokens\": 200`) || !strings.Contains(files[3], `\"contextWindow\":2000`) || !strings.Contains(files[3], `\"id\":\"small\"`) {
		t.Errorf("settings/models files: %s | %s", files[2], files[3])
	}
	if !strings.Contains(files[4], `"path":"seed.jsonl"`) || !strings.Contains(files[4], `"id":"<uuid>"`) {
		t.Errorf("seed session: %s", files[4])
	}
	last := tr.Events[len(tr.Events)-1]
	if last.Ch != ChFiles || last.Step != "99-files" {
		t.Errorf("the final capture is not the last event: %s %s", last.Step, last.Ch)
	}
}

func TestATriggerThatNeverFiresFailsTheStep(t *testing.T) {
	tr := runFeatureLane(t, `{"name":"never","settleMs":1,"tailMs":-1,"llm":[{"text":"a"}],"steps":[
	  {"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"event":"compaction_start"},"rpc":{"type":"abort"}}]}]}`)
	e, failed := tr.Failed()
	if !failed || !strings.Contains(string(e.Data), "trigger 0 never fired (compaction_start event number 1 matching null)") {
		t.Errorf("failed %v: %s", failed, e.Data)
	}
}

func TestAnUnsavedPathFailsTheStep(t *testing.T) {
	tr := runFeatureLane(t, `{"name":"nopath","settleMs":1,"tailMs":-1,"steps":[
	  {"rpc":{"type":"get_state"},"save":{"v":"data.nothing"}}]}`)
	e, failed := tr.Failed()
	if !failed || !strings.Contains(string(e.Data), `save v: path \"data.nothing\" found nothing`) {
		t.Errorf("failed %v: %s", failed, e.Data)
	}
}

// A scenario that uses none of the new fields gets the host arguments, agent files and models.json it always got.
func TestDefaultScenarioKeepsItsHostArguments(t *testing.T) {
	tr := runFeatureLane(t, `{"name":"default","settleMs":1,"tailMs":-1,"llm":[{"text":"a"}],"steps":[{"rpc":{"type":"get_state"}}],"captureFiles":["args.txt","agent:*"]}`)
	if e, failed := tr.Failed(); failed {
		t.Fatalf("lane failed: %s", e.Data)
	}
	files := events(tr, ChFiles)
	if len(files) != 2 ||
		!strings.Contains(files[0], `--mode\nrpc\n--no-session\n--no-extensions\n--no-skills\n--no-prompt-templates\n--no-themes\n--no-context-files\n--offline\n--provider\neq-llm\n--model\neq-1\n`) {
		t.Errorf("host arguments: %q", files)
	}
	if strings.Contains(files[1], "settings.json") || !strings.Contains(files[1], `"path":"models.json"`) {
		t.Errorf("agent files: %s", files[1])
	}
}
