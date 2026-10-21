package eq

import (
	"bufio"
	"bytes"
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

// burstEvents is how many tool-result message_end records the fake host writes before it asks the model again. Their
// size makes the burst larger than a pipe's buffer, so the host's last writes land while the harness's reader is
// still working through the earlier ones.
const burstEvents = 400

// TestFakeOrderHost is the fake host's process, not a test: the wrapper script that TestHostOutputIsRecordedBeforeTheRequestThatFollowsIt
// writes runs this test binary with EQ_FAKE_ORDER_HOST set, and the function ends the process itself.
//
// The host is the shape of PiG's agent loop for a tool call that the host rejects: it writes a run of records to its
// stdout and, with nothing in between, sends the next request to the model. It reads its stdin on a goroutine of its
// own, as pi and pig do, so it answers get_state while it waits for the model. In mode "mute" it stops answering once
// it has been asked to run.
func TestFakeOrderHost(t *testing.T) {
	mode := os.Getenv("EQ_FAKE_ORDER_HOST")
	if mode == "" {
		t.Skip("runs as the fake host of TestHostOutputIsRecordedBeforeTheRequestThatFollowsIt")
	}
	var outMu sync.Mutex
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	write := func(v any) {
		b, _ := json.Marshal(v)
		outMu.Lock()
		defer outMu.Unlock()
		out.Write(append(b, '\n'))
		out.Flush()
	}
	respond := func(id any, command string) {
		write(map[string]any{"type": "response", "id": id, "command": command, "success": true, "data": map[string]any{}})
	}
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
		if err := json.Unmarshal(b, &models); err != nil {
			panic(err)
		}
		return models.Providers["eq-llm"].BaseURL
	}
	run := func() {
		write(map[string]any{"type": "agent_start"})
		filler := strings.Repeat("x", 300)
		for i := range burstEvents {
			write(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("burst %04d %s", i, filler)}}}})
		}
		resp, err := http.Post(baseURL()+"/chat/completions", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"next"}]}`))
		if err != nil {
			panic(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		write(map[string]any{"type": "agent_end"})
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	prompted := false
	for in.Scan() {
		var cmd map[string]any
		if json.Unmarshal(in.Bytes(), &cmd) != nil {
			continue
		}
		switch cmd["type"] {
		case "get_state":
			if mode == "mute" && prompted {
				continue
			}
			respond(cmd["id"], "get_state")
		case "prompt":
			prompted = true
			respond(cmd["id"], "prompt")
			go run()
		}
	}
	os.Exit(0)
}

func fakeOrderHost(t *testing.T, mode string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake host is started by a POSIX shell script")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "pig")
	script := fmt.Sprintf("#!/bin/sh\nEQ_FAKE_ORDER_HOST=%s exec %q -test.run='^TestFakeOrderHost$' -test.count=1\n", mode, exe)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func orderScenario() *Scenario {
	return &Scenario{
		Name: "order", LLM: []Turn{{Text: "done"}}, SettleMs: 1, TailMs: -1,
		Steps: []Step{{RPC: map[string]any{"type": "prompt", "message": "go"}}},
	}
}

// A request the host sends right after it wrote output must be recorded after that output. The harness reads the
// host's stdout on one goroutine and serves the model on others, so recording each as it arrived put the request
// first whenever the reader was behind: pigeq's schema-rejections scenario failed about one run in thirty on PiG,
// which writes a rejected call's tool_execution_end, message_end, turn_end and turn_start and asks the model again
// within a millisecond, with pi's golden having the four records first.
func TestHostOutputIsRecordedBeforeTheRequestThatFollowsIt(t *testing.T) {
	bin := fakeOrderHost(t, "answer")
	for round := range 3 {
		tr, err := RunLane(orderScenario(), Lane{Name: "pig-go", Host: "pig", Bin: bin}, Options{StartupTimeout: 30 * time.Second, StepTimeout: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if e, failed := tr.Failed(); failed {
			t.Fatalf("round %d: the lane failed: %s", round, e.Data)
		}
		llm, burst, lastBurst := -1, 0, -1
		for i, e := range tr.Events {
			switch {
			case e.Ch == ChLLM:
				llm = i
			case e.Ch == ChHost && bytes.Contains(e.Data, []byte(`"burst `)):
				burst++
				lastBurst = i
			}
		}
		if burst != burstEvents {
			t.Fatalf("round %d: %d of %d burst records in the trace", round, burst, burstEvents)
		}
		if llm < 0 || llm < lastBurst {
			t.Fatalf("round %d: the request is event %d but the host wrote its last burst record (event %d) before it", round, llm, lastBurst)
		}
	}
}

// Ordering a request after the host's output must not hang a lane whose host cannot answer: the step timeout ends
// the lane as a failure, the held request is released with the host's exit, and the request is still recorded.
func TestAHostThatCannotAnswerEndsTheLaneInsteadOfHanging(t *testing.T) {
	bin := fakeOrderHost(t, "mute")
	// The baseline run of the host's system text would meet the same mute host; the fake host has no system text.
	key := "pig\x00" + bin + "\x00"
	baselineMu.Lock()
	baselineCache[key] = ""
	baselineMu.Unlock()
	t.Cleanup(func() {
		baselineMu.Lock()
		delete(baselineCache, key)
		baselineMu.Unlock()
	})
	start := time.Now()
	tr, err := RunLane(orderScenario(), Lane{Name: "pig-go", Host: "pig", Bin: bin}, Options{StartupTimeout: 30 * time.Second, StepTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var llm bool
	for _, e := range tr.Events {
		llm = llm || e.Ch == ChLLM
	}
	if _, failed := tr.Failed(); !failed || !llm {
		t.Errorf("lane failed %v, request recorded %v; want both", failed, llm)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the lane took %s", elapsed)
	}
}
