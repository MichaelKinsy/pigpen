package gamemcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fakeInstance is a game that records what the agent did.
type fakeInstance struct {
	mu      sync.Mutex
	acted   []string
	closed  bool
	stopped int
	failAct error
}

func (f *fakeInstance) State() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"running": true, "acted": len(f.acted)}
}

func (f *fakeInstance) Act(action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAct != nil {
		return f.failAct
	}
	f.acted = append(f.acted, action)
	return nil
}

func (f *fakeInstance) Score() Score { return Score{Score: 7, HighScore: 9} }

func (f *fakeInstance) Stop() Score {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped++
	return Score{Score: 8, HighScore: 9}
}

func (f *fakeInstance) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func fakeGame(inst *fakeInstance, opened *int) Game {
	return Game{
		Name:         "fake-game",
		Description:  "A fake game.",
		Instructions: "Jump over things.",
		Actions:      []string{"jump", "duck"},
		ScoreFile:    "state/fake.json",
		Open: func(_ sdk.Context, agent *Agent) (Instance, error) {
			*opened++
			return inst, nil
		},
	}
}

func startFake(t *testing.T) (*Server, *Session, *fakeInstance, *int) {
	t.Helper()
	inst := &fakeInstance{}
	opened := new(int)
	session := NewSession(fakeGame(inst, opened))
	server, err := StartServer("games-fake", "A fake game.", session.Tools())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, session, inst, opened
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func post(t *testing.T, server *Server, token string, body string, mutate ...func(*http.Request)) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header, data}
}

func rpc(t *testing.T, server *Server, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	r := post(t, server, server.Token(), string(body))
	if r.status != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, r.status, r.body)
	}
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("%s: %v: %s", method, err, r.body)
	}
	return out
}

func call(t *testing.T, server *Server, tool string, args map[string]any) map[string]any {
	t.Helper()
	out := rpc(t, server, "tools/call", map[string]any{"name": tool, "arguments": args})
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result: %v", tool, out)
	}
	return result
}

func text(result map[string]any) string {
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestToolsList(t *testing.T) {
	server, _, _, _ := startFake(t)
	out := rpc(t, server, "tools/list", nil)
	var names []string
	for _, tool := range out["result"].(map[string]any)["tools"].([]any) {
		entry := tool.(map[string]any)
		names = append(names, entry["name"].(string))
		if entry["description"] == "" || entry["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool %v lacks a description or an object input schema", entry["name"])
		}
	}
	want := []string{"games_list", "game_start", "game_state", "game_act", "game_score"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestInitializeNegotiatesTheVersion(t *testing.T) {
	server, _, _, _ := startFake(t)
	for _, c := range []struct{ ask, want string }{
		{"2025-06-18", "2025-06-18"}, {"2024-11-05", "2024-11-05"}, {"1999-01-01", supportedVersions[0]}, {"", supportedVersions[0]},
	} {
		out := rpc(t, server, "initialize", map[string]any{"protocolVersion": c.ask, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
		result := out["result"].(map[string]any)
		if result["protocolVersion"] != c.want {
			t.Errorf("asked %q: version %v, want %v", c.ask, result["protocolVersion"], c.want)
		}
		if result["serverInfo"].(map[string]any)["name"] != "games-fake" || result["capabilities"].(map[string]any)["tools"] == nil {
			t.Errorf("initialize result = %v", result)
		}
	}
}

// The session id goes to the client in a header, where logs and UIs may show it: it must give
// away nothing of the bearer token.
func TestSessionIDIsNoPartOfTheToken(t *testing.T) {
	server, _, _, _ := startFake(t)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`
	first := post(t, server, server.Token(), initialize).header.Get("Mcp-Session-Id")
	if len(first) < 32 {
		t.Fatalf("session id %q", first)
	}
	if again := post(t, server, server.Token(), initialize).header.Get("Mcp-Session-Id"); again != first {
		t.Errorf("session id changed from %q to %q", first, again)
	}
	for i := 0; i+8 <= len(first); i++ {
		if strings.Contains(server.Token(), first[i:i+8]) {
			t.Fatalf("session id %q shares %q with the token", first, first[i:i+8])
		}
	}
	other, _, _, _ := startFake(t)
	if id := post(t, other, other.Token(), initialize).header.Get("Mcp-Session-Id"); id == first {
		t.Error("two servers share a session id")
	}
}

func TestEveryRequestNeedsTheBearerToken(t *testing.T) {
	server, _, _, _ := startFake(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for name, token := range map[string]string{"missing": "", "wrong": "0000", "prefix": server.Token()[:10]} {
		r := post(t, server, token, body)
		if r.status != http.StatusUnauthorized || !strings.HasPrefix(r.header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s token: status %d, challenge %q", name, r.status, r.header.Get("WWW-Authenticate"))
		}
	}
	// Another scheme with the right secret is still no bearer token.
	r := post(t, server, "", body, func(req *http.Request) { req.Header.Set("Authorization", "Basic "+server.Token()) })
	if r.status != http.StatusUnauthorized {
		t.Errorf("basic scheme: status %d", r.status)
	}
	// The other methods are guarded too, and the tool never ran.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequest(method, server.URL(), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: status %d", method, resp.StatusCode)
		}
	}
	if r := post(t, server, server.Token(), body); r.status != http.StatusOK {
		t.Errorf("right token: status %d", r.status)
	}
}

func TestTokenAndPortAreRandomPerServer(t *testing.T) {
	a, _, _, _ := startFake(t)
	b, _, _, _ := startFake(t)
	if a.Token() == b.Token() || a.Addr() == b.Addr() {
		t.Fatalf("two servers share a token or an address: %s %s", a.Addr(), b.Addr())
	}
	if len(a.Token()) < 64 {
		t.Fatalf("token has %d characters", len(a.Token()))
	}
}

func TestServerListensOnLoopbackOnly(t *testing.T) {
	server, _, _, _ := startFake(t)
	host, _, err := net.SplitHostPort(server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("listens on %q", host)
	}
	if !strings.HasPrefix(server.URL(), "http://127.0.0.1:") {
		t.Fatalf("url = %s", server.URL())
	}
	// A request that arrives under another Host name (DNS rebinding) is refused, token or not.
	r := post(t, server, server.Token(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`, func(req *http.Request) { req.Host = "evil.example:" + strings.Split(server.Addr(), ":")[1] })
	if r.status != http.StatusForbidden {
		t.Errorf("foreign Host header: status %d", r.status)
	}
	// A peer that is not loopback is refused by the check itself.
	req, _ := http.NewRequest(http.MethodPost, server.URL(), bytes.NewReader(nil))
	req.RemoteAddr = "192.0.2.7:4000"
	if server.localRequest(req) {
		t.Error("a non-loopback peer was accepted")
	}
	req.RemoteAddr = "127.0.0.1:4000"
	if !server.localRequest(req) {
		t.Error("a loopback peer on the listener's own address was refused")
	}
}

func TestCloseStopsListening(t *testing.T) {
	server, _, _, _ := startFake(t)
	addr := server.Addr()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("the port still accepts connections after Close")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestProtocolEdges(t *testing.T) {
	server, _, _, _ := startFake(t)
	if r := post(t, server, server.Token(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.status != http.StatusAccepted || len(r.body) != 0 {
		t.Errorf("notification: status %d body %q", r.status, r.body)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL(), nil)
	req.Header.Set("Authorization", "Bearer "+server.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status %d, want 405 (no server stream)", resp.StatusCode)
	}
	out := rpc(t, server, "no/such", nil)
	if out["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method: %v", out)
	}
	out = rpc(t, server, "tools/call", map[string]any{"name": "nope"})
	if out["error"] == nil {
		t.Errorf("unknown tool: %v", out)
	}
	if r := post(t, server, server.Token(), `{`); r.status != http.StatusOK || !strings.Contains(string(r.body), "-32700") {
		t.Errorf("malformed JSON: %d %s", r.status, r.body)
	}
	r := post(t, server, server.Token(), `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
	var batch []map[string]any
	if err := json.Unmarshal(r.body, &batch); err != nil || len(batch) != 2 {
		t.Errorf("batch: %v %s", err, r.body)
	}
}

func TestGamesListAndStart(t *testing.T) {
	server, session, inst, opened := startFake(t)
	list := call(t, server, "games_list", nil)
	var games []info
	if err := json.Unmarshal([]byte(text(list)), &games); err != nil || len(games) != 1 {
		t.Fatalf("games_list text = %q (%v)", text(list), err)
	}
	if g := games[0]; g.Name != "fake-game" || g.Description != "A fake game." || g.Instructions != "Jump over things." || strings.Join(g.Actions, ",") != "jump,duck,none,stop" {
		t.Fatalf("game = %+v", g)
	}
	if list["structuredContent"].(map[string]any)["games"] == nil {
		t.Fatalf("structuredContent = %v", list["structuredContent"])
	}

	// Nothing is open before game_start, and game_state says so as a tool error (game_score
	// answers with the saved score, see TestScoreWithoutAGameAndTheInstructions).
	for _, tool := range []string{"game_state"} {
		r := call(t, server, tool, nil)
		if r["isError"] != true || !strings.Contains(text(r), "game_start") {
			t.Errorf("%s before start: %v", tool, r)
		}
	}
	if r := call(t, server, "game_act", map[string]any{"action": "jump"}); r["isError"] != true {
		t.Errorf("game_act before start: %v", r)
	}
	if r := call(t, server, "game_start", map[string]any{"name": "other"}); r["isError"] != true || *opened != 0 {
		t.Errorf("unknown game opened: %v (opened %d)", r, *opened)
	}

	start := call(t, server, "game_start", map[string]any{"name": "fake-game"})
	if start["isError"] != false || text(start) != `{"game":"fake-game","ok":true}` || *opened != 1 {
		t.Fatalf("game_start = %v (opened %d)", start, *opened)
	}
	if line := session.Agent().Line(); line != "AI playing: no decision yet · decisions 0" {
		t.Errorf("HUD line after start = %q", line)
	}
	// A second start while the overlay is open opens nothing new.
	call(t, server, "game_start", map[string]any{"name": "fake-game"})
	if *opened != 1 {
		t.Errorf("a second game_start opened another overlay")
	}
	state := call(t, server, "game_state", nil)
	if state["structuredContent"].(map[string]any)["running"] != true {
		t.Errorf("game_state = %v", state)
	}

	// Once the user closes the overlay the game is gone, and game_start opens a fresh one.
	inst.mu.Lock()
	inst.closed = true
	inst.mu.Unlock()
	if r := call(t, server, "game_state", nil); r["isError"] != true {
		t.Errorf("game_state after the overlay closed: %v", r)
	}
	inst.mu.Lock()
	inst.closed = false
	inst.mu.Unlock()
	call(t, server, "game_start", map[string]any{"name": "fake-game"})
	if *opened != 2 {
		t.Errorf("game_start after close opened %d overlays", *opened)
	}
}

func TestActValidatesCountsAndShowsConfidence(t *testing.T) {
	server, session, inst, _ := startFake(t)
	call(t, server, "game_start", map[string]any{"name": "fake-game"})

	for name, args := range map[string]map[string]any{
		"unknown action":      {"action": "fly"},
		"missing action":      {},
		"confidence above 1":  {"action": "jump", "confidence": 1.5},
		"negative confidence": {"action": "jump", "confidence": -0.1},
		"wrong type":          {"action": 3},
	} {
		if r := call(t, server, "game_act", args); r["isError"] != true {
			t.Errorf("%s accepted: %v", name, r)
		}
	}
	if session.Agent().Decisions() != 0 || len(inst.acted) != 0 {
		t.Fatalf("a rejected action counted: decisions %d, acted %v", session.Agent().Decisions(), inst.acted)
	}

	r := call(t, server, "game_act", map[string]any{"action": "jump", "confidence": 0.934})
	if text(r) != `{"decisions":1,"ok":true}` {
		t.Errorf("game_act = %s", text(r))
	}
	if line := session.Agent().Line(); line != "AI playing: last decision jump p=0.93 · decisions 1" {
		t.Errorf("HUD line = %q", line)
	}
	call(t, server, "game_act", map[string]any{"action": "duck"})
	if line := session.Agent().Line(); line != "AI playing: last decision duck · decisions 2" {
		t.Errorf("HUD line without confidence = %q", line)
	}
	// "none" is a decision; it reaches the game, which presses nothing for it.
	call(t, server, "game_act", map[string]any{"action": "none", "confidence": 1})
	if line := session.Agent().Line(); line != "AI playing: last decision none p=1 · decisions 3" {
		t.Errorf("HUD line for none = %q", line)
	}
	if got := strings.Join(inst.acted, ","); got != "jump,duck,none" {
		t.Errorf("the game was asked to %q", got)
	}

	score := call(t, server, "game_score", nil)
	if text(score) != `{"decisions":3,"game_over":false,"high_score":9,"open":true,"score":7,"stopped":false}` {
		t.Errorf("game_score = %s", text(score))
	}
}

// stop ends the play: the game is asked to stop once, the HUD shows the final score, later
// actions are refused, and game_score keeps answering with the final score after the overlay
// closes, the high score at least the saved one.
func TestStopEndsThePlayAndTheScoreOutlivesTheOverlay(t *testing.T) {
	inst := &fakeInstance{}
	opened := new(int)
	game := fakeGame(inst, opened)
	game.SavedHighScore = func(string) int { return 12 }
	session := NewSession(game)
	server, err := StartServer("games-fake", game.ServerInstructions(), session.Tools())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	call(t, server, "game_start", map[string]any{"name": "fake-game"})
	call(t, server, "game_act", map[string]any{"action": "jump"})

	stop := call(t, server, "game_act", map[string]any{"action": "stop"})
	if text(stop) != `{"decisions":1,"high_score":9,"ok":true,"score":8,"stopped":true}` || inst.stopped != 1 {
		t.Fatalf("stop = %s (stopped %d)", text(stop), inst.stopped)
	}
	if line := session.Agent().Line(); line != "AI stopped · final score 8 · high score 9 · decisions 1 · any key closes" {
		t.Errorf("HUD line after stop = %q", line)
	}
	for _, action := range []string{"jump", "stop"} {
		if r := call(t, server, "game_act", map[string]any{"action": action}); r["isError"] != true || !strings.Contains(text(r), "stopped") {
			t.Errorf("%s after stop: %v", action, r)
		}
	}
	if inst.stopped != 1 || strings.Join(inst.acted, ",") != "jump" {
		t.Errorf("after stop the game was asked to %v, stopped %d times", inst.acted, inst.stopped)
	}
	if r := call(t, server, "game_score", nil); text(r) != `{"decisions":1,"game_over":false,"high_score":12,"open":true,"score":8,"stopped":true}` {
		t.Errorf("game_score after stop = %s", text(r))
	}
	// wait_closed_ms waits for the key that closes the overlay, and no longer.
	begin := time.Now()
	if r := call(t, server, "game_score", map[string]any{"wait_closed_ms": 200}); !strings.Contains(text(r), `"open":true`) || time.Since(begin) < 200*time.Millisecond {
		t.Errorf("game_score waiting on an open overlay = %s after %v", text(r), time.Since(begin))
	}
	time.AfterFunc(100*time.Millisecond, func() {
		inst.mu.Lock()
		inst.closed = true
		inst.mu.Unlock()
	})
	begin = time.Now()
	if r := call(t, server, "game_score", map[string]any{"wait_closed_ms": 5000}); text(r) != `{"decisions":1,"game_over":false,"high_score":12,"open":false,"score":8,"stopped":true}` || time.Since(begin) > 2*time.Second {
		t.Errorf("game_score after the overlay closed = %s after %v", text(r), time.Since(begin))
	}
	if r := call(t, server, "game_score", map[string]any{"wait_closed_ms": 60000}); r["isError"] != true {
		t.Errorf("a wait beyond the limit: %v", r)
	}
}

// Before any game, game_score reads the saved high score, and its description says where it is
// saved; the server's MCP instructions carry the game's instructions.
func TestScoreWithoutAGameAndTheInstructions(t *testing.T) {
	game := fakeGame(&fakeInstance{}, new(int))
	game.SavedHighScore = func(string) int { return 31 }
	session := NewSession(game)
	server, err := StartServer("games-fake", game.ServerInstructions(), session.Tools())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if r := call(t, server, "game_score", nil); text(r) != `{"decisions":0,"game_over":false,"high_score":31,"open":false,"score":0,"stopped":false}` {
		t.Errorf("game_score before a game = %s", text(r))
	}
	if d := session.scoreDescription(); !strings.Contains(d, "<config home>/state/fake.json") {
		t.Errorf("game_score description %q", d)
	}
	if got := game.ServerInstructions(); got != "A fake game.\n\nJump over things." {
		t.Errorf("server instructions %q", got)
	}
}

func TestAgentLineIsEmptyUntilAnAgentPlays(t *testing.T) {
	var a Agent
	if a.Line() != "" {
		t.Fatalf("idle line = %q", a.Line())
	}
	a.Begin()
	a.Note("fire", nil)
	a.Begin()
	if a.Decisions() != 0 {
		t.Fatalf("Begin kept %d decisions", a.Decisions())
	}
}

func TestEnvEnabled(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "true": true, " Yes ": true, "ON": true, "": false, "0": false, "no": false, "2": false} {
		if got := envEnabled(value); got != want {
			t.Errorf("envEnabled(%q) = %v", value, got)
		}
	}
}
