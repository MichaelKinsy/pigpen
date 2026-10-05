package a2aext

// The Piglet Binary hosts the listener itself: `pig-a2a --mode rpc` with an a2a.json, a fused a2a extension, and a
// worker that is the same Binary. Set PIG_A2A_BINARY to the executable built from piglets/a2a. Rule 17: a temporary
// HOME, PIG_HOME and PIG_CODING_AGENT_DIR; the model is a local OpenAI-compatible server.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func binaryHome(t *testing.T, bin string, a2aJSON func(port int, work string) string) (env []string, work string, port int, llm *e2eLLM) {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "pig", "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	llm = startE2ELLM(t)
	models := map[string]any{"providers": map[string]any{"e2e": map[string]any{
		"baseUrl": llm.baseURL(), "api": "openai-completions", "apiKey": "e2e-key",
		"models": []any{map[string]any{"id": "e2e-1", "name": "e2e-1", "reasoning": false, "input": []string{"text"},
			"contextWindow": 100000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}},
	}}}
	b, _ := json.Marshal(models)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	work = filepath.Join(home, "workspace")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello from the workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if cfg := a2aJSON(port, work); cfg != "" {
		if err := os.WriteFile(filepath.Join(agent, "a2a.json"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"),
		"PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "A2A_TOKEN_A=" + tokenA, "A2A_TOKEN_B=" + tokenB}
	return env, work, port, llm
}

// binHost is a running Binary in RPC mode: commands go to stdin, JSONL events come back on lines.
type binHost struct {
	stdin io.Writer
	lines chan string
}

// status asks the running Binary what /a2a says (a UI notification event) and returns it.
func (h *binHost) status(t *testing.T) string {
	t.Helper()
	if _, err := io.WriteString(h.stdin, `{"type":"prompt","message":"/a2a"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case l := <-h.lines:
			if strings.Contains(l, "a2a:") && strings.Contains(l, "extension_ui_request") {
				return l
			}
		case <-deadline:
			t.Fatal("the Binary never answered /a2a")
		}
	}
}

func startBinaryHost(t *testing.T, bin string, env []string, work string, extra ...string) *binHost {
	t.Helper()
	t.Helper()
	args := append([]string{"--mode", "rpc", "--offline", "--provider", "e2e", "--model", "e2e-1", "--no-session"}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = work, env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &binHost{stdin: stdin, lines: make(chan string, 256)}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			select {
			case h.lines <- sc.Text():
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return h
}

func waitDial(t *testing.T, addr string, want bool, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			if want {
				return true
			}
		} else if !want {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestBinary_ListenerInsideThePigletBinary(t *testing.T) {
	bin := os.Getenv("PIG_A2A_BINARY")
	if bin == "" {
		t.Skip("set PIG_A2A_BINARY to the Piglet Binary built from piglets/a2a to run the Binary proof")
	}
	env, work, port, llm := binaryHome(t, bin, func(port int, work string) string {
		return fmt.Sprintf(`{"listen":"127.0.0.1:%d","name":"binary-pig","tokens":[{"name":"alice","tokenEnv":"A2A_TOKEN_A","tenant":"team-a"},{"name":"bob","tokenEnv":"A2A_TOKEN_B"}],
		  "worker":{"command":%q,"cwd":%q,"provider":"e2e","model":"e2e-1","tools":["read","grep","find","ls"]}}`, port, bin, work)
	})
	startBinaryHost(t, bin, env, work)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if !waitDial(t, addr, true, 60*time.Second) {
		t.Fatal("the Binary never opened the configured A2A listener")
	}
	base := "http://" + addr

	// Discovery is public; everything else needs a token.
	resp, err := http.Get(base + "/.well-known/agent-card.json")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("card: %v %v", resp, err)
	}
	var card map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&card)
	resp.Body.Close()
	if card["name"] != "binary-pig" {
		t.Fatalf("card %v", card)
	}
	if resp, err = http.Post(base+"/", "application/json", strings.NewReader(`{}`)); err != nil || resp.StatusCode != 401 {
		t.Fatalf("an unauthenticated call must be 401: %v %v", resp, err)
	}
	resp.Body.Close()

	// A task runs a real PiG worker (the same Binary) that calls a read tool (named in a2a.json) against the local model.
	ca := a2aClient(t, base, tokenA)
	task := sendTask(t, ca, &a2a.SendMessageRequest{Message: textMessage("please read the note")})
	if task.Status.State != a2a.TaskStateCompleted || !strings.Contains(taskText(task), "hello from the workspace") {
		t.Fatalf("state %s: %q %+v", task.Status.State, taskText(task), task.Status.Message)
	}
	llm.mu.Lock()
	for _, tools := range llm.tools {
		if strings.Join(tools, ",") != "find,grep,ls,read" {
			t.Fatalf("the worker must get exactly the configured tools; the model was offered %v", tools)
		}
	}
	llm.mu.Unlock()

	// The worker was started without extensions, so it did not open a second listener, and another tenant cannot see the task.
	cb := a2aClient(t, base, tokenB)
	if _, err := cb.GetTask(context.Background(), &a2a.GetTaskRequest{ID: task.ID}); err == nil {
		t.Fatal("bob read alice's task")
	}
}

func TestBinary_ListenerStaysOffWithoutConfiguration(t *testing.T) {
	bin := os.Getenv("PIG_A2A_BINARY")
	if bin == "" {
		t.Skip("set PIG_A2A_BINARY to the Piglet Binary built from piglets/a2a to run the Binary proof")
	}
	env, work, port, _ := binaryHome(t, bin, func(int, string) string { return "" })
	h := startBinaryHost(t, bin, env, work)
	if got := h.status(t); !strings.Contains(got, "listener off") {
		t.Fatalf("the extension is loaded but /a2a says: %s", got)
	}
	_ = port
}

func TestBinary_FlagWithoutTokensIsRefused(t *testing.T) {
	bin := os.Getenv("PIG_A2A_BINARY")
	if bin == "" {
		t.Skip("set PIG_A2A_BINARY to the Piglet Binary built from piglets/a2a to run the Binary proof")
	}
	env, work, port, _ := binaryHome(t, bin, func(int, string) string { return "" })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	h := startBinaryHost(t, bin, env, work, "--a2a-listen", addr)
	if got := h.status(t); !strings.Contains(got, "needs at least one entry in tokens") {
		t.Fatalf("--a2a-listen without tokens must be refused with the reason: %s", got)
	}
}
