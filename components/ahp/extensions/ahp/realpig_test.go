package ahp

// The real-host proof: the extension, built and loaded by a real `pig`, serving a real PiG
// session whose model is a scripted local server, driven by a WebSocket AHP client. It runs only
// when PIGPEN_AHP_REAL=1 and PIG_BIN names the pig executable; everything happens under a
// temporary HOME, PIG_HOME and PIG_CODING_AGENT_DIR (nothing of the caller's is read or written).

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/ws"
)

type realOpts struct {
	script   string
	settings func(port int, token string) string
	args     []string
	root     string // reuse a previous run's directory (its sessions included)
	noFlag   bool   // start without --ahp
}

type realPig struct {
	root   string
	stdin  io.WriteCloser
	stop   func()
	t      *testing.T
	port   int
	token  string
	client *ws.Client
	mu     sync.Mutex
	id     int
	notes  []map[string]any
}

func freeTCPPort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startRealPig starts fakellm and pig (RPC mode, stdin held open) and returns once the listener
// accepts connections. settingsBody is the AHP settings file.
func startRealPig(t *testing.T, o realOpts) *realPig {
	t.Helper()
	pigBin := os.Getenv("PIGPEN_AHP_PIG") // a Piglet Binary with the extension fused in, else PIG_BIN
	if pigBin == "" {
		pigBin = os.Getenv("PIG_BIN")
	}
	if os.Getenv("PIGPEN_AHP_REAL") != "1" || pigBin == "" {
		t.Skip("real-pig proof: set PIGPEN_AHP_REAL=1 and PIG_BIN")
	}
	root := o.root
	if root == "" {
		root = t.TempDir()
	}
	home, agent, work := filepath.Join(root, "home"), filepath.Join(root, "agent"), filepath.Join(root, "work")
	pighome := filepath.Join(root, "pighome")
	for _, d := range []string{home, agent, work, pighome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// the scripted model
	fake := filepath.Join(root, "fakellm")
	build := exec.Command("go", "build", "-o", fake, ".")
	build.Dir = filepath.Join("..", "..", "proof", "tools", "fakellm")
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building fakellm: %v\n%s", err, out)
	}
	scriptPath := filepath.Join(root, "turns.json")
	if err := os.WriteFile(scriptPath, []byte(o.script), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := exec.Command(fake, "-script", scriptPath)
	stdout, _ := llm.StdoutPipe()
	if err := llm.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = llm.Process.Kill(); _ = llm.Wait() })
	buf := make([]byte, 256)
	n, _ := stdout.Read(buf)
	baseURL := strings.TrimSpace(strings.TrimPrefix(string(buf[:n]), "LISTENING "))
	models := fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":"openai-completions","apiKey":"k","models":[{"id":"fake-1","name":"fake-1","reasoning":false,"input":["text"],"contextWindow":100000,"maxTokens":4096,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, baseURL)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}

	port, token := freeTCPPort(t), "proof-token"
	settings := filepath.Join(root, "ahp-settings.json")
	if o.settings != nil {
		if err := os.WriteFile(settings, []byte(o.settings(port, token)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--mode", "rpc", "--provider", "fake", "--model", "fake-1", "--offline", "--ahp-settings", settings}
	if os.Getenv("PIGPEN_AHP_FUSED") != "1" {
		// PIG_BIN is a plain pig: load the extension from this directory. With a Piglet Binary that
		// has the extension fused in (PIGPEN_AHP_FUSED=1) it is already there.
		args = append(args, "--no-extensions", "-e", ".")
	}
	if !o.noFlag {
		args = append(args, "--ahp")
	}
	args = append(args, o.args...)
	// the extension directory is this package's directory
	here, _ := os.Getwd()
	for i, a := range args {
		if a == "-e" {
			args[i+1] = here
		}
	}
	cmd := exec.Command(pigBin, args...)
	cmd.Dir = work
	cmd.Env = []string{"HOME=" + home, "PIG_HOME=" + pighome, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent,
		"PATH=" + os.Getenv("PATH"), "GOCACHE=" + os.Getenv("GOCACHE"), "GOMODCACHE=" + os.Getenv("GOMODCACHE"), "GOTOOLCHAIN=local", "GOFLAGS=" + os.Getenv("GOFLAGS")}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	logFile, _ := os.Create(filepath.Join(root, "pig.log"))
	var stopOnce sync.Once
	var stopFn func()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopFn = func() {
		stopOnce.Do(func() {
			_ = stdin.Close()
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		})
	}
	t.Cleanup(func() {
		stopFn()
		if t.Failed() {
			if b, err := os.ReadFile(filepath.Join(root, "pig.log")); err == nil {
				if len(b) > 3000 {
					b = b[:3000]
				}
				t.Logf("pig output (first 3000 bytes):\n%s", b)
			}
		}
	})
	p := &realPig{t: t, port: port, token: token, root: root, stdin: stdin, stop: stopFn}
	if o.noFlag {
		return p
	}
	deadline := time.Now().Add(120 * time.Second) // the first run builds the extension
	for {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the listener never came up: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return p
}

// pigLog is everything pig printed so far.
func (p *realPig) pigLog() string {
	b, _ := os.ReadFile(filepath.Join(p.root, "pig.log"))
	return string(b)
}

// rpcPrompt types a prompt into the session, as a local user would.
func (p *realPig) rpcPrompt(text string) {
	line, _ := json.Marshal(map[string]any{"type": "prompt", "message": text})
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func (p *realPig) connect(origin string) (*ws.Client, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return ws.Dial(fmt.Sprintf("ws://127.0.0.1:%d/?token=%s", p.port, p.token), header)
}

func (p *realPig) open() {
	c, _, err := p.connect("")
	if err != nil {
		p.t.Fatal(err)
	}
	p.client = c
	p.t.Cleanup(func() { c.Close() })
}

// request sends a JSON-RPC request and returns its result or error object, keeping every
// notification that arrives meanwhile.
func (p *realPig) request(method string, params map[string]any) (json.RawMessage, map[string]any) {
	p.t.Helper()
	p.mu.Lock()
	p.id++
	id := p.id
	p.mu.Unlock()
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err := p.client.WriteText(frame); err != nil {
		p.t.Fatal(err)
	}
	for {
		msg := p.next(30 * time.Second)
		if got, ok := msg["id"]; ok && int(got.(float64)) == id {
			if e, bad := msg["error"].(map[string]any); bad {
				return nil, e
			}
			raw, _ := json.Marshal(msg["result"])
			return raw, nil
		}
	}
}

func (p *realPig) notify(method string, params map[string]any) {
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err := p.client.WriteText(frame); err != nil {
		p.t.Fatal(err)
	}
}

func (p *realPig) next(timeout time.Duration) map[string]any {
	msg, err := p.tryNext(timeout)
	if err != nil {
		p.t.Fatalf("reading from the host: %v", err)
	}
	return msg
}

func (p *realPig) tryNext(timeout time.Duration) (map[string]any, error) {
	raw, err := p.client.ReadMessage(timeout)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	_ = json.Unmarshal(raw, &msg)
	if _, isResponse := msg["id"]; !isResponse {
		p.notes = append(p.notes, msg)
	}
	return msg, nil
}

// awaitAction reads until an action of type on channel arrives.
func (p *realPig) awaitAction(channel, actionType string, timeout time.Duration) map[string]any {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	check := func(n map[string]any) map[string]any {
		params, _ := n["params"].(map[string]any)
		action, _ := params["action"].(map[string]any)
		if params["channel"] == channel && action["type"] == actionType {
			return action
		}
		return nil
	}
	for _, n := range p.notes {
		if a := check(n); a != nil {
			return a
		}
	}
	for time.Now().Before(deadline) {
		msg, err := p.tryNext(time.Until(deadline))
		if err != nil {
			break
		}
		if a := check(msg); a != nil {
			return a
		}
	}
	var seen []string
	for _, n := range p.notes {
		params, _ := n["params"].(map[string]any)
		action, _ := params["action"].(map[string]any)
		seen = append(seen, fmt.Sprintf("%v %v %v", n["method"], params["channel"], action["type"]))
	}
	p.t.Fatalf("timed out waiting for %s on %s; saw:\n%s", actionType, channel, strings.Join(seen, "\n"))
	return nil
}

func TestRealPigServesTheLiveSession(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[{"chunks":["Hello ","from PiG"]}]`, settings: basicSettings})

	// a missing token, and a foreign browser origin, are refused
	if _, resp, err := ws.Dial(fmt.Sprintf("ws://127.0.0.1:%d/", p.port), nil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a connection without the token must be refused (err=%v resp=%v)", err, resp)
	}
	if _, resp, err := p.connect("https://evil.example"); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Origin must be refused (err=%v resp=%v)", err, resp)
	}

	p.open()
	raw, rpcErr := p.request("initialize", map[string]any{"channel": "ahp-root://", "clientId": "real-proof", "protocolVersions": []string{"0.9.0"}, "initialSubscriptions": []string{"ahp-root://"}})
	if rpcErr != nil {
		t.Fatalf("initialize: %v", rpcErr)
	}
	var init struct {
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	_ = json.Unmarshal(raw, &init)
	if init.ServerInfo.Name != "pigpen-ahp" {
		t.Fatalf("serverInfo %s", raw)
	}

	// nothing beyond the session is exposed
	if _, e := p.request("resourceRead", map[string]any{"channel": "ahp-root://", "uri": "file:///etc/hostname"}); e == nil || e["code"].(float64) != -32601 {
		t.Fatalf("the filesystem must not be exposed by default: %v", e)
	}
	if _, e := p.request("createTerminal", map[string]any{"channel": "ahp-terminal:/x", "claim": map[string]any{"kind": "client", "clientId": "real-proof"}}); e == nil || e["code"].(float64) != -32601 {
		t.Fatalf("terminals must not be exposed by default: %v", e)
	}

	// the running session is in the catalogue
	raw, rpcErr = p.request("listSessions", map[string]any{"channel": "ahp-root://"})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var list struct{ Items []struct{ Resource string } }
	_ = json.Unmarshal(raw, &list)
	if len(list.Items) != 1 || !strings.HasPrefix(list.Items[0].Resource, "ahp-session:/") {
		t.Fatalf("catalogue %s", raw)
	}
	session := list.Items[0].Resource
	chat := "ahp-chat:/" + strings.TrimPrefix(session, "ahp-session:/")
	if _, e := p.request("subscribe", map[string]any{"channel": session}); e != nil {
		t.Fatalf("subscribe session: %v", e)
	}
	if _, e := p.request("subscribe", map[string]any{"channel": chat}); e != nil {
		t.Fatalf("subscribe chat: %v", e)
	}

	// a remote turn drives the real agent loop against the scripted model
	p.notify("dispatchAction", map[string]any{"channel": chat, "clientSeq": 1, "action": map[string]any{
		"type": "chat/turnStarted", "turnId": "remote-1", "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message": map[string]any{"text": "say hello", "origin": map[string]any{"kind": "user"}}}})
	p.awaitAction(chat, "chat/turnComplete", 20*time.Second)

	raw, rpcErr = p.request("subscribe", map[string]any{"channel": chat})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if !strings.Contains(string(raw), "Hello from PiG") {
		t.Fatalf("the completed turn does not carry the model's answer: %s", raw)
	}
}

func basicSettings(port int, token string) string {
	return fmt.Sprintf(`{"port": %d, "token": %q, "host": "127.0.0.1"}`, port, token)
}

// handshake opens a connection and initialises it; it returns the live session and its chat.
func (p *realPig) handshake() (session, chat string) {
	p.open()
	if _, e := p.request("initialize", map[string]any{"channel": "ahp-root://", "clientId": "real-proof", "protocolVersions": []string{"0.9.0"}, "initialSubscriptions": []string{"ahp-root://"}}); e != nil {
		p.t.Fatalf("initialize: %v", e)
	}
	raw, e := p.request("listSessions", map[string]any{"channel": "ahp-root://"})
	if e != nil {
		p.t.Fatal(e)
	}
	var list struct{ Items []struct{ Resource string } }
	_ = json.Unmarshal(raw, &list)
	if len(list.Items) != 1 {
		p.t.Fatalf("catalogue %s", raw)
	}
	session = list.Items[0].Resource
	chat = "ahp-chat:/" + strings.TrimPrefix(session, "ahp-session:/")
	for _, ch := range []string{session, chat} {
		if _, e := p.request("subscribe", map[string]any{"channel": ch}); e != nil {
			p.t.Fatalf("subscribe %s: %v", ch, e)
		}
	}
	return session, chat
}

func TestRealPigListenerIsOffUnlessAsked(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[]`, noFlag: true})
	p.rpcPrompt("/ahp status")
	testkit.Eventually(t, "the status notification", func() bool { return strings.Contains(p.pigLog(), "AHP listener is off") })
	if _, err := os.Stat(filepath.Join(p.root, "ahp-settings.json")); err == nil {
		t.Fatal("the settings file (with its token) must not be created unless the listener is started")
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.port), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("a listener is open although none was asked for")
	}
	// /ahp start is the explicit request
	p.rpcPrompt("/ahp start")
	testkit.Eventually(t, "the listener to start", func() bool { return strings.Contains(p.pigLog(), "AHP listening on ws://127.0.0.1:") })
	// the notice leads with the address as VS Code's "Agents: Add Remote Agent Host..." and SSH launcher read it
	// (the settings file, and its generated token and port, only came into being with this /ahp start)
	vscode := regexp.MustCompile(`AHP listening on ws://127\.0\.0\.1:\d+\?tkn=[A-Za-z0-9_-]+ - for VS Code`)
	testkit.Eventually(t, "the VS Code form in the notice", func() bool { return vscode.MatchString(p.pigLog()) })
}

func TestRealPigMirrorsALocalPrompt(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[{"chunks":["typed ","locally"]}]`, settings: basicSettings})
	_, chat := p.handshake()
	p.rpcPrompt("typed into the session")
	p.awaitAction(chat, "chat/turnStarted", 20*time.Second)
	p.awaitAction(chat, "chat/turnComplete", 20*time.Second)
	raw, e := p.request("subscribe", map[string]any{"channel": chat})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "typed into the session") || !strings.Contains(string(raw), "typed locally") {
		t.Fatalf("the local turn is not in the chat: %s", raw)
	}
}

func TestRealPigOptInFilesystemAndTerminal(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[]`, settings: func(port int, token string) string {
		return fmt.Sprintf(`{"port": %d, "token": %q, "filesystem": {"enabled": true}, "terminals": {"enabled": true}}`, port, token)
	}})
	p.handshake()
	work := filepath.Join(p.root, "work")
	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, e := p.request("resourceRead", map[string]any{"channel": "ahp-root://", "uri": "file://" + filepath.ToSlash(filepath.Join(work, "note.txt"))})
	if e != nil || !strings.Contains(string(raw), "inside") {
		t.Fatalf("a file inside the working directory must be readable: %s %v", raw, e)
	}
	// the default root is the session's working directory: nothing outside it, not even beside it
	if _, e := p.request("resourceRead", map[string]any{"channel": "ahp-root://", "uri": "file:///etc/hostname"}); e == nil || e["code"].(float64) != -32009 {
		t.Fatalf("a file outside the roots must be refused: %v", e)
	}
	sibling := filepath.Join(p.root, "beside-work.txt")
	if err := os.WriteFile(sibling, []byte("beside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e := p.request("resourceRead", map[string]any{"channel": "ahp-root://", "uri": "file://" + filepath.ToSlash(sibling)}); e == nil || e["code"].(float64) != -32009 {
		t.Fatalf("a file beside the working directory must be refused: %v", e)
	}
	// a terminal: claimed by this client, output flows back
	terminal := "ahp-terminal:/" + "0a3c1e52-6b7d-4f3a-9d10-0000000000aa"
	if _, e := p.request("createTerminal", map[string]any{"channel": terminal, "claim": map[string]any{"kind": "client", "clientId": "real-proof"}, "cwd": "file://" + filepath.ToSlash(work)}); e != nil {
		t.Fatalf("createTerminal: %v", e)
	}
	if _, e := p.request("subscribe", map[string]any{"channel": terminal}); e != nil {
		t.Fatal(e)
	}
	p.notify("dispatchAction", map[string]any{"channel": terminal, "clientSeq": 1, "action": map[string]any{"type": "terminal/input", "data": "echo pty-$((6*7))\r"}})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := p.tryNext(time.Until(deadline))
		if err != nil {
			break
		}
		if strings.Contains(fmt.Sprint(msg), "pty-42") {
			return
		}
	}
	t.Fatal("the shell's output never arrived")
}

func TestRealPigServesAResumedSessionWithItsHistory(t *testing.T) {
	first := startRealPig(t, realOpts{script: `[{"text":"the first answer"}]`, noFlag: true})
	first.rpcPrompt("the first question")
	testkit.Eventually(t, "the first turn to settle", func() bool { return strings.Contains(first.pigLog(), `"type":"agent_settled"`) })
	first.stop()

	second := startRealPig(t, realOpts{script: `[{"text":"the second answer"}]`, settings: basicSettings, root: first.root, args: []string{"--continue"}})
	_, chat := second.handshake()
	raw, e := second.request("subscribe", map[string]any{"channel": chat})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "the first question") || !strings.Contains(string(raw), "the first answer") {
		t.Fatalf("the resumed session's history was not served: %s", raw)
	}
	// and the resumed session keeps working through AHP
	second.notify("dispatchAction", map[string]any{"channel": chat, "clientSeq": 1, "action": map[string]any{
		"type": "chat/turnStarted", "turnId": "remote-2", "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message": map[string]any{"text": "the second question", "origin": map[string]any{"kind": "user"}}}})
	second.awaitAction(chat, "chat/turnComplete", 20*time.Second)
	raw, _ = second.request("subscribe", map[string]any{"channel": chat})
	if !strings.Contains(string(raw), "the second answer") {
		t.Fatalf("the second turn's answer is missing: %s", raw)
	}
}

func TestRealPigCancelsARemoteTurn(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[{"chunks":["a","b","c","d","e","f","g","h","i","j"],"delayMs":1500}]`, settings: basicSettings})
	_, chat := p.handshake()
	p.notify("dispatchAction", map[string]any{"channel": chat, "clientSeq": 1, "action": map[string]any{
		"type": "chat/turnStarted", "turnId": "remote-c", "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message": map[string]any{"text": "go slowly", "origin": map[string]any{"kind": "user"}}}})
	p.awaitAction(chat, "chat/delta", 20*time.Second)
	p.notify("dispatchAction", map[string]any{"channel": chat, "clientSeq": 2, "action": map[string]any{"type": "chat/turnCancelled", "turnId": "remote-c", "duration": 0}})
	// the script would run for 15 s: an abort ends the run within seconds, not at its natural end
	deadline := time.Now().Add(6 * time.Second)
	for !strings.Contains(p.pigLog(), `"type":"agent_end"`) {
		if time.Now().After(deadline) {
			t.Fatal("the agent kept running after the remote cancel")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(p.pigLog(), `"delta":"j"`) {
		t.Fatal("the model's whole answer was streamed although the turn was cancelled")
	}
}

func TestRealPigRefusesANonLoopbackHostWithoutAToken(t *testing.T) {
	p := startRealPig(t, realOpts{script: `[]`, noFlag: true, settings: func(port int, _ string) string {
		return fmt.Sprintf(`{"port": %d, "token": null, "host": "0.0.0.0"}`, port)
	}})
	p.rpcPrompt("/ahp start")
	testkit.Eventually(t, "the refusal", func() bool { return strings.Contains(p.pigLog(), "not loopback, so a token is required") })
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.port), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("a non-loopback listener without a token was opened")
	}
}

// rpcCommand sends an RPC command other than a prompt.
func (p *realPig) rpcCommand(cmd map[string]any) {
	line, _ := json.Marshal(cmd)
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func (p *realPig) listening() bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.port), 300*time.Millisecond)
	if err == nil {
		c.Close()
		return true
	}
	return false
}

// Review (rev-pigpen-ahp): what happens to the listener when the PiG session is replaced.
// With --ahp it serves the new session on the same port; after /ahp start it closes with the
// replaced session, and says so.
func TestRealPigSessionReplacement(t *testing.T) {
	t.Run("--ahp serves the new session", func(t *testing.T) {
		p := startRealPig(t, realOpts{script: `[{"text":"after the replacement"}]`, settings: basicSettings})
		before, _ := p.handshake()
		p.rpcCommand(map[string]any{"type": "new_session"})
		testkit.Eventually(t, "new_session to finish", func() bool { return strings.Contains(p.pigLog(), `"command":"new_session","success":true`) })
		testkit.Eventually(t, "the listener to be back", p.listening)
		p.notes = nil
		after, chat := p.handshake()
		if after == before {
			t.Fatalf("still serving the replaced session %s", before)
		}
		p.notify("dispatchAction", map[string]any{"channel": chat, "clientSeq": 1, "action": map[string]any{
			"type": "chat/turnStarted", "turnId": "remote-n", "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			"message": map[string]any{"text": "are you there", "origin": map[string]any{"kind": "user"}}}})
		p.awaitAction(chat, "chat/turnComplete", 20*time.Second)
	})
	t.Run("/ahp start closes with the session and says so", func(t *testing.T) {
		p := startRealPig(t, realOpts{script: `[]`, noFlag: true, settings: basicSettings})
		p.rpcPrompt("/ahp start")
		testkit.Eventually(t, "the listener to start", func() bool { return strings.Contains(p.pigLog(), "AHP listening on ws://127.0.0.1:") })
		p.rpcCommand(map[string]any{"type": "new_session"})
		testkit.Eventually(t, "new_session to finish", func() bool { return strings.Contains(p.pigLog(), `"command":"new_session","success":true`) })
		if p.listening() {
			t.Fatal("a listener opened with /ahp start must not follow the session into a replacement unasked")
		}
		testkit.Eventually(t, "the notice that the listener closed", func() bool { return strings.Contains(p.pigLog(), "AHP listener stopped") })
	})
}
