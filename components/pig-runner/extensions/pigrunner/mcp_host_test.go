package pigrunner_test

// The MCP server through the fake host: opt-in, registration, authentication, the overlay
// game_start opens, shutdown, and a whole game played over HTTP by the fake classifier.

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp/gamemcptest"
	"github.com/MichaelKinsy/pigpen/pigrunner"
)

// registered is the server a host call registered.
type registered struct {
	name, url, token string
	config           map[string]any
}

func registrations(t *testing.T, h *Host) []registered {
	t.Helper()
	var out []registered
	for _, call := range h.CallsTo("registerMcpServer") {
		config, _ := call.Args["config"].(map[string]any)
		headers, _ := config["headers"].(map[string]any)
		bearer, _ := headers["Authorization"].(string)
		out = append(out, registered{
			name: fmt.Sprint(call.Args["name"]), url: fmt.Sprint(config["url"]),
			token: strings.TrimPrefix(bearer, "Bearer "), config: config,
		})
	}
	return out
}

// hostWithOverlay answers ui.custom like a terminal that keeps the overlay open until
// release is closed, then closes it with the game's own score.
func hostWithOverlay(t *testing.T, opts HostOptions) (*Host, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	closeOverlay := func() { once.Do(func() { close(release) }) }
	opts.OnCall = func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.custom" {
			<-release
			return map[string]any{"ok": true, "result": map[string]any{"score": 5.0, "highScore": 5.0}}, ""
		}
		return nil, ""
	}
	h := StartHost(t, pigrunner.Extension(), opts)
	// Cleanups run last in, first out: let the overlay close and the game's ticker stop
	// before the host goes, so no ticker outlives the test.
	t.Cleanup(func() {
		closeOverlay()
		waitFor(t, "the game ticker to stop", func() bool { return tickers() == 0 })
		// The SDK disposes the component before the overlay's caller saves the high score and
		// reports it; the temporary home must outlive that save.
		if len(h.CallsTo("ui.custom")) > 0 {
			waitFor(t, "the score report", func() bool { return scoreReported(h) })
		}
	})
	return h, closeOverlay
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// mcpOn turns the server on and registers its removal, so no listener outlives the test.
func mcpOn(t *testing.T, h *Host, command string) registered {
	t.Helper()
	if err := h.Command(command, "mcp on"); err != "" {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Command(command, "mcp off") })
	return registrations(t, h)[len(registrations(t, h))-1]
}

func refused(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return true
}

func hostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestMCPIsOffUntilAsked(t *testing.T) {
	withHome(t)
	h := StartHost(t, pigrunner.Extension(), HostOptions{OnCall: closesWith(map[string]any{"score": 1.0, "highScore": 1.0})})
	if err := h.Command("runner", ""); err != "" {
		t.Fatal(err)
	}
	if err := h.Command("runner", "mcp status"); err != "" {
		t.Fatal(err)
	}
	if n := len(h.CallsTo("registerMcpServer")); n != 0 {
		t.Fatalf("%d MCP servers registered without being asked", n)
	}
	if n := len(h.CallsTo("event.subscribe")); n != 0 {
		t.Fatalf("%d event subscriptions without being asked", n)
	}
	notes := h.CallsTo("ui.notify")
	if got := notes[len(notes)-1].Args["message"]; got != "pig-runner MCP server off" {
		t.Fatalf("status = %v", got)
	}
}

func TestMCPOnRegistersAServerOnLoopbackAndOffRemovesIt(t *testing.T) {
	withHome(t)
	h := StartHost(t, pigrunner.Extension(), HostOptions{})
	mcpOn(t, h, "runner")
	regs := registrations(t, h)
	if len(regs) != 1 {
		t.Fatalf("registrations = %+v", regs)
	}
	r := regs[0]
	if r.name != "games-runner" || r.config["type"] != "http" || r.config["exposure"] != "codemode" {
		t.Errorf("registration = %+v", r)
	}
	if desc, _ := r.config["description"].(string); !strings.Contains(desc, "PiG Runner") || !strings.Contains(desc, "game_state") {
		t.Errorf("description %q does not say what the server offers", desc)
	}
	host, _, err := net.SplitHostPort(hostPort(t, r.url))
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("server url %q is not on 127.0.0.1", r.url)
	}
	if len(r.token) < 64 {
		t.Fatalf("bearer token %q is too short", r.token)
	}
	if !strings.HasPrefix(fmt.Sprint(r.config["headers"].(map[string]any)["Authorization"]), "Bearer ") {
		t.Errorf("headers = %v", r.config["headers"])
	}

	authed := gamemcptest.New(r.url, r.token)
	tools, err := authed.Tools()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tools, ","); got != "games_list,game_start,game_state,game_act,game_score" {
		t.Fatalf("tools = %s", got)
	}
	if status, _, err := gamemcptest.New(r.url, "not-the-token").Request("tools/list", map[string]any{}); err == nil || status != 401 {
		t.Errorf("a wrong token got status %d, err %v", status, err)
	}
	if status, _, err := gamemcptest.New(r.url, "").Request("tools/list", map[string]any{}); err == nil || status != 401 {
		t.Errorf("no token got status %d, err %v", status, err)
	}
	list, err := authed.Call("games_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	games := list.([]any)
	game := games[0].(map[string]any)
	if len(games) != 1 || game["name"] != "pig-runner" || fmt.Sprint(game["actions"]) != "[jump duck restart none stop]" || !strings.Contains(game["instructions"].(string), "decide_now") {
		t.Fatalf("games_list = %v", list)
	}

	// Typing it twice changes nothing.
	if err := h.Command("runner", "mcp on"); err != "" {
		t.Fatal(err)
	}
	if n := len(h.CallsTo("registerMcpServer")); n != 1 {
		t.Errorf("a second `mcp on` registered %d servers", n)
	}

	if err := h.Command("pig-runner", "mcp off"); err != "" {
		t.Fatal(err)
	}
	unreg := h.CallsTo("unregisterMcpServer")
	if len(unreg) != 1 || unreg[0].Args["name"] != "games-runner" {
		t.Fatalf("unregister calls = %+v", unreg)
	}
	if !refused(hostPort(t, r.url)) {
		t.Fatal("the port still listens after `mcp off`")
	}
	if len(h.CallsTo("event.unsubscribe")) != len(h.CallsTo("event.subscribe")) {
		t.Errorf("subscribe/unsubscribe: %d/%d", len(h.CallsTo("event.subscribe")), len(h.CallsTo("event.unsubscribe")))
	}
}

func TestMCPUsageIsReported(t *testing.T) {
	withHome(t)
	h := StartHost(t, pigrunner.Extension(), HostOptions{})
	for _, args := range []string{"mcp", "mcp maybe", "mcp on now"} {
		if err := h.Command("runner", args); err != "" {
			t.Fatal(err)
		}
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 3 {
		t.Fatalf("notifications = %+v", notes)
	}
	for _, n := range notes {
		if n.Args["message"] != "Usage: /runner mcp on|off|status" || n.Args["level"] != "warning" {
			t.Errorf("notification = %+v", n.Args)
		}
	}
	if len(h.CallsTo("registerMcpServer")) != 0 || len(h.CallsTo("ui.custom")) != 0 {
		t.Error("a bad argument started something")
	}
}

func TestSessionShutdownStopsAndUnregistersTheServer(t *testing.T) {
	withHome(t)
	h := StartHost(t, pigrunner.Extension(), HostOptions{})
	r := mcpOn(t, h, "runner")
	// The extension subscribed to session_shutdown only now; give the fake host its handler id.
	subs := h.CallsTo("event.subscribe")
	if len(subs) != 1 || subs[0].Args["event"] != "session_shutdown" {
		t.Fatalf("subscriptions = %+v", subs)
	}
	h.mu.Lock()
	h.handlers["session_shutdown"] = int(subs[0].Args["handlerId"].(float64))
	h.mu.Unlock()

	h.Fire("session_shutdown", map[string]any{"reason": "reload"})
	if n := len(h.CallsTo("unregisterMcpServer")); n != 1 {
		t.Fatalf("unregister calls = %d", n)
	}
	if !refused(hostPort(t, r.url)) {
		t.Fatal("the port still listens after session_shutdown")
	}
}

func TestEnvironmentVariableStartsTheServerOnSessionStart(t *testing.T) {
	withHome(t)
	t.Setenv("PIG_GAMES_MCP", "1")
	h := StartHost(t, pigrunner.Extension(), HostOptions{})
	if !h.Registered("session_start") {
		t.Fatal("PIG_GAMES_MCP=1 registered no session_start handler")
	}
	if n := len(h.CallsTo("registerMcpServer")); n != 0 {
		t.Fatalf("registered before the session started: %d", n)
	}
	h.Fire("session_start", map[string]any{"reason": "startup"})
	regs := registrations(t, h)
	if len(regs) != 1 || regs[0].name != "games-runner" {
		t.Fatalf("registrations = %+v", regs)
	}
	if _, err := gamemcptest.New(regs[0].url, regs[0].token).Tools(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentVariableOffLeavesNoHandler(t *testing.T) {
	withHome(t)
	t.Setenv("PIG_GAMES_MCP", "0")
	h := StartHost(t, pigrunner.Extension(), HostOptions{})
	if len(h.handlers) != 0 {
		t.Fatalf("handlers = %v", h.handlers)
	}
}

func TestGameStartOpensTheSameOverlayAndGameToolsFollowIt(t *testing.T) {
	withHome(t)
	h, release := hostWithOverlay(t, HostOptions{})
	r := mcpOn(t, h, "runner")
	c := gamemcptest.New(r.url, r.token)

	// Before game_start the game tools say there is nothing to read.
	if _, err := c.Call("game_state", nil); err == nil || !strings.Contains(err.Error(), "game_start") {
		t.Fatalf("game_state before start: %v", err)
	}
	if _, err := c.Call("game_start", map[string]any{"name": "angry-pigs"}); err == nil {
		t.Fatal("game_start accepted another game's name")
	}
	if len(h.CallsTo("ui.custom")) != 0 {
		t.Fatal("a rejected game_start opened an overlay")
	}

	started, err := c.CallObject("game_start", map[string]any{"name": "pig-runner"})
	if err != nil || started["ok"] != true {
		t.Fatalf("game_start = %v, %v", started, err)
	}
	waitFor(t, "the overlay", func() bool { return len(h.CallsTo("ui.custom")) == 1 })
	a := h.CallsTo("ui.custom")[0].Args
	if a["title"] != "PiG Runner" || a["overlay"] != true || a["widthFraction"] != 1.0 || a["heightFraction"] != 1.0 {
		t.Errorf("overlay options = %v", a)
	}

	state, err := c.CallObject("game_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if state["running"] != true {
		t.Errorf("game_state after game_start = %v: the title screen is still up", state)
	}
	for _, key := range []string{"score", "speed", "next_obstacle", "pig"} {
		if _, ok := state[key]; !ok {
			t.Errorf("game_state lacks %q: %v", key, state)
		}
	}
	if _, err := c.Call("game_act", map[string]any{"action": "jump", "confidence": 0.8}); err != nil {
		t.Fatal(err)
	}
	score, err := c.CallObject("game_score", nil)
	if err != nil || score["decisions"] != 1.0 || score["game_over"] != false {
		t.Fatalf("game_score = %v, %v", score, err)
	}

	// Closing the overlay (Esc, in a terminal) ends the game for the agent too.
	release()
	waitFor(t, "the notification", func() bool { return len(h.CallsTo("ui.notify")) >= 2 })
	waitFor(t, "the game to close", func() bool { _, err := c.Call("game_state", nil); return err != nil })
}

// The host may hand the server to a running script before registerMcpServer returns: game_start
// must work from the moment the host holds the token.
func TestGameStartWorksAsSoonAsTheHostHoldsTheToken(t *testing.T) {
	withHome(t)
	release := make(chan struct{})
	var once sync.Once
	started := make(chan error, 1)
	h := StartHost(t, pigrunner.Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "registerMcpServer":
			config, _ := args["config"].(map[string]any)
			headers, _ := config["headers"].(map[string]any)
			bearer, _ := headers["Authorization"].(string)
			_, err := gamemcptest.New(fmt.Sprint(config["url"]), strings.TrimPrefix(bearer, "Bearer ")).Call("game_start", map[string]any{"name": "pig-runner"})
			started <- err
		case "ui.custom":
			<-release
			return CustomResult(map[string]any{"score": 0.0, "highScore": 0.0}), ""
		}
		return nil, ""
	}})
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		waitFor(t, "the game ticker to stop", func() bool { return tickers() == 0 })
		// The SDK disposes the component before the overlay's caller saves the high score and
		// reports it; the temporary home must outlive that save.
		waitFor(t, "the score report", func() bool { return scoreReported(h) })
	})
	mcpOn(t, h, "runner")
	if err := <-started; err != nil {
		t.Fatalf("game_start while the host registered the server: %v", err)
	}
	waitFor(t, "the overlay", func() bool { return len(h.CallsTo("ui.custom")) == 1 })
}

func TestGameStartWithoutAUIFails(t *testing.T) {
	withHome(t)
	off := false
	h := StartHost(t, pigrunner.Extension(), HostOptions{Mode: "print", HasUI: &off})
	r := mcpOn(t, h, "runner")
	_, err := gamemcptest.New(r.url, r.token).Call("game_start", map[string]any{"name": "pig-runner"})
	if err == nil || !strings.Contains(err.Error(), "no terminal UI") {
		t.Fatalf("game_start without a UI: %v", err)
	}
	if n := len(h.CallsTo("ui.custom")); n != 0 {
		t.Fatalf("%d overlays without a UI", n)
	}
}

// The fake classifier plays a real game over MCP, as the demo script does with Jev: read
// game_state, answer "which action avoids the next obstacle", send game_act with the
// probability. The pig must survive obstacles, in real time.
func TestFakeClassifierPlaysTheRunnerOverMCP(t *testing.T) {
	withHome(t)
	h, _ := hostWithOverlay(t, HostOptions{})
	r := mcpOn(t, h, "runner")
	c := gamemcptest.New(r.url, r.token)
	if _, err := c.Call("game_start", map[string]any{"name": "pig-runner"}); err != nil {
		t.Fatal(err)
	}

	const play = 9 * time.Second // obstacles begin after 3 s of running
	begin := time.Now()
	obstacles, lastKind, lastDistance := 0, "none", 0.0
	var scoreSeen float64
	for time.Since(begin) < play {
		state, err := c.CallObject("game_state", nil)
		if err != nil {
			t.Fatal(err)
		}
		// The next obstacle is a new one when it is farther away than the one before, or when
		// the road was empty.
		next := state["next_obstacle"].(map[string]any)
		if kind, distance := next["kind"].(string), next["distance_px"].(float64); kind != "none" {
			if lastKind == "none" || distance > lastDistance+5 {
				obstacles++
			}
			lastKind, lastDistance = kind, distance
		} else {
			lastKind = "none"
		}
		scoreSeen = state["score"].(float64)
		action, p := pigrunner.FakeJev(state)
		if _, err := c.Call("game_act", map[string]any{"action": action, "confidence": p}); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(begin)

	score, err := c.CallObject("game_score", nil)
	if err != nil {
		t.Fatal(err)
	}
	decisions := score["decisions"].(float64)
	t.Logf("fake classifier: %.0f decisions in %.1f s = %.0f decisions/s, score %v, obstacles met %d", decisions, elapsed.Seconds(), decisions/elapsed.Seconds(), score["score"], obstacles)
	if score["game_over"] != false {
		t.Fatalf("the pig crashed: %v", score)
	}
	if scoreSeen < 30 || score["score"].(float64) < scoreSeen {
		t.Errorf("score %v after %v", score["score"], play)
	}
	if obstacles < 3 {
		t.Errorf("only %d obstacles came by in %v", obstacles, play)
	}
	if decisions < 100 {
		t.Errorf("only %v decisions", decisions)
	}
}

// The demo's contract over MCP in real time: one decision at a time, each taking 0.7 s as a
// Jev classification does, from the rule a classifier would apply to the state's words. The
// run waits at each obstacle for the answer, so the pig survives 20 s of play; then stop ends
// it and game_score reports the final score.
func TestSlowRulePlayerSurvivesTwentySecondsOverMCPThenStops(t *testing.T) {
	if testing.Short() {
		t.Skip("plays 20 s in real time")
	}
	withHome(t)
	h, _ := hostWithOverlay(t, HostOptions{})
	r := mcpOn(t, h, "runner")
	c := gamemcptest.New(r.url, r.token)
	if _, err := c.Call("game_start", map[string]any{"name": "pig-runner"}); err != nil {
		t.Fatal(err)
	}
	const play, latency = 20 * time.Second, 700 * time.Millisecond
	begin := time.Now()
	holds := 0
	for time.Since(begin) < play {
		state, err := c.CallObject("game_state", nil)
		if err != nil {
			t.Fatal(err)
		}
		if state["game_over"] == true {
			t.Fatalf("the pig crashed after %v: %v", time.Since(begin), state)
		}
		if state["decide_now"] == true {
			holds++
		}
		action, p := pigrunner.FakeJev(state)
		time.Sleep(latency)
		if _, err := c.Call("game_act", map[string]any{"action": action, "confidence": p}); err != nil {
			t.Fatal(err)
		}
	}
	stop, err := c.CallObject("game_act", map[string]any{"action": "stop"})
	if err != nil {
		t.Fatal(err)
	}
	score, err := c.CallObject("game_score", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("slow rule player: %v decisions, %d answered holds, final score %v, high %v", score["decisions"], holds, stop["score"], stop["high_score"])
	if stop["stopped"] != true || score["stopped"] != true || score["game_over"] != false || score["score"] != stop["score"] {
		t.Fatalf("stop = %v, game_score = %v", stop, score)
	}
	if holds < 3 || stop["score"].(float64) < 20 {
		t.Errorf("only %d obstacles answered, score %v", holds, stop["score"])
	}
}

// scoreReported reports whether the game told the user its score, which it does after saving
// the high score when the overlay closes.
func scoreReported(h *Host) bool {
	for _, note := range h.CallsTo("ui.notify") {
		if message, _ := note.Args["message"].(string); strings.Contains(message, " score ") {
			return true
		}
	}
	return false
}
