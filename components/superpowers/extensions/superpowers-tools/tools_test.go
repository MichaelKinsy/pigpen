package superpowers_tools

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func maskedFrame(opcode byte, payload []byte) []byte {
	mask := []byte{1, 2, 3, 4}
	n := len(payload)
	var h []byte
	switch {
	case n < 126:
		h = []byte{0x80 | opcode, 0x80 | byte(n)}
	case n < 65536:
		h = []byte{0x80 | opcode, 0x80 | 126, byte(n >> 8), byte(n)}
	default:
		h = make([]byte, 10)
		h[0], h[1] = 0x80|opcode, 0x80|127
		binary.BigEndian.PutUint64(h[2:], uint64(n))
	}
	h = append(h, mask...)
	for i, b := range payload {
		h = append(h, b^mask[i%4])
	}
	return h
}

func TestAcceptKey(t *testing.T) {
	if got := computeAcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("accept key %q", got)
	}
}

func TestFrames(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 200, 65535, 65536, 70000} {
		payload := bytes.Repeat([]byte("a"), n)
		op, got, used, err := decodeFrame(maskedFrame(opText, payload))
		if err != nil || op != opText || !bytes.Equal(got, payload) || used == 0 {
			t.Errorf("decode %d: op=%d err=%v used=%d", n, op, err, used)
		}
		enc := encodeFrame(opText, payload)
		if enc[0] != 0x81 || (n < 126 && enc[1] != byte(n)) || (n >= 126 && n < 65536 && enc[1] != 126) || (n >= 65536 && enc[1] != 127) {
			t.Errorf("encode %d header % x", n, enc[:4])
		}
	}
	if _, _, _, err := decodeFrame([]byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}); err == nil || err.Error() != "Client frames must be masked" {
		t.Errorf("unmasked frame: %v", err)
	}
	if _, _, used, err := decodeFrame(maskedFrame(opText, []byte("hello"))[:6]); used != 0 || err != nil {
		t.Errorf("partial frame: used=%d err=%v", used, err)
	}
	big := make([]byte, 10)
	big[0], big[1] = 0x81, 0x80|127
	binary.BigEndian.PutUint64(big[2:], maxFramePayload+1)
	if _, _, _, err := decodeFrame(big); err != errFrameTooBig {
		t.Errorf("oversize: %v", err)
	}
	binary.BigEndian.PutUint64(big[2:], maxFramePayload)
	if _, _, used, err := decodeFrame(big); used != 0 || err != nil {
		t.Errorf("max size waits for the rest: used=%d err=%v", used, err)
	}
	if op, _, _, _ := decodeFrame(maskedFrame(opPing, []byte("x"))); op != opPing {
		t.Errorf("ping opcode %d", op)
	}
}

func TestBrowserLauncher(t *testing.T) {
	none := func(string) string { return "" }
	display := func(k string) string {
		if k == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	cases := []struct {
		goos, release string
		env           func(string) string
		bin           string
		ok            bool
	}{
		{"darwin", "", none, "open", true},
		{"windows", "", none, "rundll32.exe", true},
		{"linux", "5.15.0-microsoft-standard-WSL2", none, "rundll32.exe", true},
		{"linux", "6.1.0", display, "xdg-open", true},
		{"linux", "6.1.0", none, "", false},
	}
	for _, c := range cases {
		bin, _, ok := browserLauncher("http://x", c.goos, c.release, c.env)
		if bin != c.bin || ok != c.ok {
			t.Errorf("%s %s: %q %v", c.goos, c.release, bin, ok)
		}
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func start(t *testing.T, mutate func(*CompanionOptions)) (*Companion, map[string]any) {
	t.Helper()
	opts := CompanionOptions{SessionDir: filepath.Join(t.TempDir(), "session"), Token: "", Env: envOf(nil), CheckPeriod: 20 * time.Millisecond}
	if mutate != nil {
		mutate(&opts)
	}
	c, info, err := StartCompanion(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop("test") })
	return c, info
}

func get(t *testing.T, target string, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func TestHTTPAuthAndPages(t *testing.T) {
	c, info := start(t, nil)
	base := fmt.Sprintf("http://127.0.0.1:%d", int(info["port"].(int)))
	token, _ := c.currentToken()
	if !strings.HasPrefix(info["url"].(string), fmt.Sprintf("http://localhost:%d/?key=", info["port"])) || len(token) != 64 {
		t.Fatalf("info %v", info)
	}

	status, h, body := get(t, base+"/", nil)
	if status != 403 || !strings.Contains(body, "Session key required") || h.Get("X-Frame-Options") != "DENY" || h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("Content-Security-Policy") != "frame-ancestors 'none'" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("no key: %d %v %q", status, h, body)
	}
	if status, _, _ := get(t, base+"/?key=wrong", nil); status != 403 {
		t.Errorf("wrong key: %d", status)
	}
	if status, _, _ := get(t, base+"/?key=", map[string]string{"Cookie": "brainstorm-key-x=" + token}); status != 403 {
		t.Errorf("empty key wins over a cookie: %d", status)
	}

	status, h, body = get(t, base+"/?key="+token, nil)
	if status != 200 || !strings.Contains(body, "sessionStorage.setItem('brainstorm-session-key', \""+token+"\")") || !strings.Contains(body, "location.replace('/')") {
		t.Errorf("bootstrap: %d %q", status, body)
	}
	cookie := fmt.Sprintf("brainstorm-key-%d=%s; HttpOnly; SameSite=Strict; Path=/", int(info["port"].(int)), token)
	if h.Get("Set-Cookie") != cookie {
		t.Errorf("cookie %q, want %q", h.Get("Set-Cookie"), cookie)
	}

	auth := map[string]string{"Cookie": fmt.Sprintf("brainstorm-key-%d=%s", int(info["port"].(int)), token)}
	status, _, body = get(t, base+"/", auth)
	if status != 200 || !strings.Contains(body, "Waiting for the agent to push a screen...") || !strings.Contains(body, "Superpowers v7.0.0") || !strings.Contains(body, "primeradiant.com/brand") || !strings.HasSuffix(body, "</script>") && !strings.Contains(body, helperScript) {
		t.Errorf("waiting page: %d", status)
	}

	// content: a fragment is framed, a full document is served as is, the newest file wins
	content := info["screen_dir"].(string)
	os.WriteFile(filepath.Join(content, "a.html"), []byte("<h2>Fragment</h2>"), 0o644)
	_, _, body = get(t, base+"/", auth)
	if !strings.Contains(body, "<h2>Fragment</h2>") || !strings.Contains(body, "Brainstorm") && !strings.Contains(frameTemplate, "<!-- CONTENT -->") {
		t.Errorf("fragment not framed: %.200q", body)
	}
	if strings.Count(body, helperScript) != 1 {
		t.Errorf("helper injected %d times", strings.Count(body, helperScript))
	}
	later := time.Now().Add(time.Hour)
	os.WriteFile(filepath.Join(content, "b.html"), []byte("<!DOCTYPE html><html><body>Full</body></html>"), 0o644)
	os.Chtimes(filepath.Join(content, "b.html"), later, later)
	_, _, body = get(t, base+"/", auth)
	if !strings.HasPrefix(body, "<!DOCTYPE html><html><body>Full") || !strings.HasSuffix(body, "</script>\n</body></html>") {
		t.Errorf("full document: %.120q ... %q", body, body[len(body)-30:])
	}

	// /files/
	os.WriteFile(filepath.Join(content, "pic.png"), []byte("PNG"), 0o644)
	os.WriteFile(filepath.Join(content, ".hidden"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(content, "data.bin"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(content, "dir"), 0o755)
	for path, want := range map[string]int{"/files/pic.png": 200, "/files/data.bin": 200, "/files/.hidden": 404, "/files/": 404, "/files/dir": 404, "/files/nope": 404, "/files/../state/server-info": 404, "/other": 404} {
		if status, _, _ := get(t, base+path, auth); status != want {
			t.Errorf("%s: %d, want %d", path, status, want)
		}
	}
	if _, h, _ := get(t, base+"/files/pic.png", auth); h.Get("Content-Type") != "image/png" {
		t.Errorf("png type %q", h.Get("Content-Type"))
	}
	if _, h, _ := get(t, base+"/files/data.bin", auth); h.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("bin type %q", h.Get("Content-Type"))
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "secret.html")
		os.WriteFile(outside, []byte("secret"), 0o644)
		os.Symlink(outside, filepath.Join(content, "link.html"))
		if status, _, _ := get(t, base+"/files/link.html", auth); status != 404 {
			t.Errorf("symlink served: %d", status)
		}
		os.Symlink(filepath.Join(content, "pic.png"), filepath.Join(content, "alias.png"))
		if status, _, _ := get(t, base+"/files/alias.png", auth); status != 404 {
			t.Errorf("symlink to a file inside the content directory served: %d", status)
		}
	}
}

func dial(t *testing.T, info map[string]any, token string, origin string) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", info["port"]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := fmt.Sprintf("GET /?key=%s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n", token, info["port"])
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	conn.Write([]byte(req + "\r\n"))
	rd := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, _ := rd.ReadString('\n')
	return conn, rd, line
}

func readFrame(t *testing.T, rd *bufio.Reader) (byte, string) {
	t.Helper()
	head := make([]byte, 2)
	if _, err := io.ReadFull(rd, head); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	n := int(head[1] & 0x7f)
	payload := make([]byte, n)
	io.ReadFull(rd, payload)
	return head[0] & 0x0f, string(payload)
}

func TestWebSocket(t *testing.T) {
	c, info := start(t, nil)
	token, _ := c.currentToken()
	port := info["port"].(int)

	conn, rd, status := dial(t, info, token, fmt.Sprintf("http://127.0.0.1:%d", port))
	if !strings.HasPrefix(status, "HTTP/1.1 101") {
		t.Fatalf("upgrade: %q", status)
	}
	for {
		l, _ := rd.ReadString('\n')
		if l == "\r\n" {
			break
		}
		if strings.HasPrefix(l, "Sec-WebSocket-Accept:") && !strings.Contains(l, "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") {
			t.Errorf("accept header %q", l)
		}
	}
	// a choice event is appended to state/events; one without a choice only reaches the log
	conn.Write(maskedFrame(opText, []byte(`{"type":"click","choice":"a","text":"A"}`)))
	conn.Write(maskedFrame(opText, []byte(`{"type":"click"}`)))
	conn.Write(maskedFrame(opText, []byte(`not json`)))
	conn.Write(maskedFrame(opPing, []byte("hi")))
	if op, p := readFrame(t, rd); op != opPong || p != "hi" {
		t.Errorf("pong %d %q", op, p)
	}
	events := filepath.Join(info["state_dir"].(string), "events")
	if raw, _ := os.ReadFile(events); string(raw) != `{"choice":"a","text":"A","type":"click"}`+"\n" {
		t.Errorf("events %q", raw)
	}
	logRaw, _ := os.ReadFile(filepath.Join(info["state_dir"].(string), "server.log"))
	if !strings.Contains(string(logRaw), `"source":"user-event"`) || !strings.Contains(string(logRaw), `"type":"server-started"`) {
		t.Errorf("log %q", logRaw)
	}

	// a new screen clears events and broadcasts a reload; an update only broadcasts
	screen := filepath.Join(info["screen_dir"].(string), "one.html")
	os.WriteFile(screen, []byte("<p>1</p>"), 0o644)
	if op, p := readFrame(t, rd); op != opText || p != `{"type":"reload"}` {
		t.Errorf("reload %d %q", op, p)
	}
	if _, err := os.Stat(events); err == nil {
		t.Error("a new screen must remove state/events")
	}
	conn.Write(maskedFrame(opText, []byte(`{"choice":"b"}`)))
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(screen, []byte("<p>2 changed</p>"), 0o644)
	if _, p := readFrame(t, rd); p != `{"type":"reload"}` {
		t.Errorf("update reload %q", p)
	}
	if _, err := os.Stat(events); err != nil {
		t.Error("an updated screen keeps state/events")
	}
	// a close frame is answered with a close frame
	conn.Write(maskedFrame(opClose, nil))
	if op, _ := readFrame(t, rd); op != opClose {
		t.Errorf("close answer %d", op)
	}

	// rejected upgrades: no key, wrong origin
	for name, args := range map[string][2]string{"wrong key": {"nope", ""}, "foreign origin": {token, "http://evil.example"}} {
		conn, rd, line := dial(t, info, args[0], args[1])
		if strings.HasPrefix(line, "HTTP/1.1 101") {
			t.Errorf("%s: upgraded", name)
		}
		_ = conn
		_ = rd
	}
	// an unmasked client frame closes the socket
	conn2, rd2, _ := dial(t, info, token, "")
	for {
		l, _ := rd2.ReadString('\n')
		if l == "\r\n" || l == "" {
			break
		}
	}
	conn2.Write([]byte{0x81, 0x01, 'x'})
	if op, _ := readFrame(t, rd2); op != opClose {
		t.Errorf("unmasked frame answer %d", op)
	}
	// an unsupported opcode closes with 1003
	conn3, rd3, _ := dial(t, info, token, "")
	for {
		l, _ := rd3.ReadString('\n')
		if l == "\r\n" || l == "" {
			break
		}
	}
	conn3.Write(maskedFrame(0x02, []byte("bin")))
	if op, p := readFrame(t, rd3); op != opClose || p != "\x03\xeb" {
		t.Errorf("binary frame answer %d %q", op, p)
	}
}

func TestLifecycleAndPersistence(t *testing.T) {
	// idle timeout stops the server and leaves server-stopped, not server-info
	c, info := start(t, func(o *CompanionOptions) { o.IdleTimeout = 150 * time.Millisecond })
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("idle timeout did not stop the server")
	}
	state := info["state_dir"].(string)
	if _, err := os.Stat(filepath.Join(state, "server-info")); err == nil {
		t.Error("server-info must be removed")
	}
	raw, _ := os.ReadFile(filepath.Join(state, "server-stopped"))
	var stopped map[string]any
	if json.Unmarshal(raw, &stopped) != nil || stopped["reason"] != "idle timeout" {
		t.Errorf("server-stopped %q", raw)
	}

	// --project-dir keeps the port and the key for a restart; server-info is owner-only
	project := t.TempDir()
	c1, i1 := start(t, func(o *CompanionOptions) { o.SessionDir = ""; o.ProjectDir = project })
	tok1, _ := c1.currentToken()
	if !strings.HasPrefix(i1["screen_dir"].(string), filepath.Join(project, ".superpowers", "brainstorm")) {
		t.Errorf("screen_dir %v", i1["screen_dir"])
	}
	if fi, err := os.Stat(filepath.Join(i1["state_dir"].(string), "server-info")); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("server-info: %v %v", err, fi)
	}
	c1.Stop("restart")
	c2, i2 := start(t, func(o *CompanionOptions) { o.SessionDir = ""; o.ProjectDir = project })
	tok2, _ := c2.currentToken()
	if i2["port"] != i1["port"] || tok1 != tok2 {
		t.Errorf("restart: port %v then %v, key reused %v", i1["port"], i2["port"], tok1 == tok2)
	}
	// a second server wanting the same port falls back to another and keeps the shared files
	c3, i3 := start(t, func(o *CompanionOptions) { o.SessionDir = ""; o.ProjectDir = project })
	tok3, _ := c3.currentToken()
	if i3["port"] == i2["port"] || tok3 == tok2 {
		t.Errorf("fallback: port %v/%v key reused %v", i2["port"], i3["port"], tok3 == tok2)
	}
	if got := readTrim(filepath.Join(project, ".superpowers", "brainstorm", ".last-port")); got != fmt.Sprint(i2["port"]) {
		t.Errorf(".last-port %q overwritten by the fallback", got)
	}
	// an explicit key refuses the fallback
	if _, _, err := StartCompanion(CompanionOptions{SessionDir: t.TempDir(), Port: i2["port"].(int), Token: "explicit", Env: envOf(nil)}); err == nil || !strings.Contains(err.Error(), "refusing fallback with explicit token") {
		t.Errorf("explicit token: %v", err)
	}
}

func TestUrlHostsAndBranding(t *testing.T) {
	_, info := start(t, func(o *CompanionOptions) { o.Host = "127.0.0.1"; o.URLHost = "::1" })
	if !strings.HasPrefix(info["url"].(string), "http://[::1]:") {
		t.Errorf("ipv6 url host %v", info["url"])
	}
	c, info := start(t, func(o *CompanionOptions) { o.Env = envOf(map[string]string{"DISABLE_TELEMETRY": "1"}) })
	token, _ := c.currentToken()
	_, _, waiting := get(t, fmt.Sprintf("http://127.0.0.1:%d/", info["port"]), map[string]string{"Cookie": fmt.Sprintf("brainstorm-key-%d=%s", info["port"], token)})
	if !strings.Contains(waiting, "Prime Radiant Superpowers v7.0.0") || strings.Contains(waiting, "primeradiant.com/brand") {
		t.Errorf("telemetry-off branding: %.300q", waiting)
	}
	for v, want := range map[string]bool{"": false, "0": false, "false": false, " No ": false, "OFF": false, "1": true, "yes": true, "anything": true} {
		if isTruthyEnv(v) != want {
			t.Errorf("isTruthyEnv(%q) != %v", v, want)
		}
	}
}

func TestOpensTheBrowserOnTheFirstScreenOnlyWhenAsked(t *testing.T) {
	run := func(open bool, host string, env map[string]string) []string {
		var mu sync.Mutex
		var calls []string
		c, info := start(t, func(o *CompanionOptions) {
			o.Open, o.Host, o.Env, o.platformGOOS = open, host, envOf(env), "darwin"
			o.OpenCommand = func(name string, args ...string) error {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, name+" "+strings.Join(args, " "))
				return nil
			}
		})
		_ = c
		os.WriteFile(filepath.Join(info["screen_dir"].(string), "x.html"), []byte("<p>x</p>"), 0o644)
		time.Sleep(400 * time.Millisecond)
		os.WriteFile(filepath.Join(info["screen_dir"].(string), "y.html"), []byte("<p>y</p>"), 0o644)
		time.Sleep(400 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
	if calls := run(false, "", nil); len(calls) != 0 {
		t.Errorf("not asked: %v", calls)
	}
	if calls := run(true, "0.0.0.0", nil); len(calls) != 0 {
		t.Errorf("non-loopback bind: %v", calls)
	}
	calls := run(true, "", nil)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "open http://localhost:") || !strings.Contains(calls[0], "/?key=") {
		t.Errorf("asked: %v", calls)
	}
	if calls := run(true, "", map[string]string{"BRAINSTORM_OPEN_CMD": "my-opener"}); len(calls) != 1 || !strings.HasPrefix(calls[0], "sh -c my-opener \"http://localhost:") {
		t.Errorf("operator launcher: %v", calls)
	}
}

func TestStopCompanion(t *testing.T) {
	if got := StopCompanion(""); got["error"] != "Usage: stop-server.sh <session_dir>" {
		t.Errorf("no argument: %v", got)
	}
	if got := StopCompanion(t.TempDir()); got["status"] != "not_running" {
		t.Errorf("unknown dir: %v", got)
	}
	project, err := os.MkdirTemp(".", "persist")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(project) })
	c, info := start(t, func(o *CompanionOptions) { o.SessionDir = ""; o.ProjectDir = project })
	companionsMu.Lock()
	companions[c.SessionDir()] = c
	companionsMu.Unlock()
	if got := StopCompanion(c.SessionDir()); got["status"] != "stopped" {
		t.Errorf("stop: %v", got)
	}
	// a persistent (project) session directory is kept, with server-stopped
	if _, err := os.Stat(filepath.Join(info["state_dir"].(string), "server-stopped")); err != nil {
		t.Errorf("server-stopped missing: %v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Error("the server is still running")
	}
	if raw, _ := os.ReadFile(filepath.Join(info["state_dir"].(string), "server-stopped")); !strings.Contains(string(raw), `"reason":"stop-server.sh"`) {
		t.Errorf("server-stopped %q", raw)
	}
	if got := StopCompanion(c.SessionDir()); got["status"] != "not_running" {
		t.Errorf("second stop: %v", got)
	}
	// a record without a server (a previous session) is stale
	stale := t.TempDir()
	os.MkdirAll(filepath.Join(stale, "state"), 0o755)
	os.WriteFile(filepath.Join(stale, "state", "server-info"), []byte("{}"), 0o600)
	if got := StopCompanion(stale); got["status"] != "stale_pid" {
		t.Errorf("stale: %v", got)
	}
}

// --- render-graphs.js ---

const skillWithGraphs = "# Skill\n\n```dot\ndigraph flow_a {\n  rankdir=LR;\n  a -> b;\n}\n```\n\ntext\n\n```dot\n  digraph second {\n    x -> y;\n  }\n  ```\n\n```dot\nnot a digraph\n```\n"

func fakeDot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in dot is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dot")
	os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = -V ]; then echo 'dot - graphviz version fake' >&2; exit 0; fi\necho '<svg>'; cat; echo '</svg>'\n"), 0o755)
	return path
}

func TestExtractDotBlocks(t *testing.T) {
	blocks := extractDotBlocks(skillWithGraphs)
	if len(blocks) != 3 || blocks[0].name != "flow_a" || blocks[1].name != "second" || blocks[2].name != "graph_3" {
		t.Fatalf("blocks %+v", blocks)
	}
	if blocks[1].content != "digraph second {\n    x -> y;\n  }" {
		t.Errorf("content %q", blocks[1].content)
	}
	if got := extractGraphBody(blocks[0].content); got != "a -> b;" {
		t.Errorf("body %q (rankdir is removed)", got)
	}
	if extractGraphBody("graph x { }") != "" {
		t.Error("a graph that is not a digraph has no body")
	}
	want := "digraph my_skill_combined {\n  rankdir=TB;\n  compound=true;\n  newrank=true;\n\n  subgraph cluster_0 {\n    label=\"flow_a\";\n      a -> b;\n  }\n\n  subgraph cluster_1 {\n    label=\"second\";\n      x -> y;\n  }\n}"
	if got := combineGraphs(blocks[:2], "my_skill"); got != want {
		t.Errorf("combined:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderGraphs(t *testing.T) {
	dot := fakeDot(t)
	skill := filepath.Join(t.TempDir(), "my-skill")
	os.MkdirAll(skill, 0o755)
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(skillWithGraphs), 0o644)
	out, err := RenderGraphs(skill, false, dot)
	if err != nil || !strings.HasPrefix(out, "Found 3 diagram(s) in my-skill/SKILL.md\n  Rendered: flow_a.svg\n  Rendered: second.svg\n  Rendered: graph_3.svg\n\nOutput: ") {
		t.Fatalf("separate: %v %q", err, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(skill, "diagrams", "flow_a.svg")); string(raw) != "<svg>\ndigraph flow_a {\n  rankdir=LR;\n  a -> b;\n}</svg>\n" && !strings.HasPrefix(string(raw), "<svg>\ndigraph flow_a") {
		t.Errorf("svg %q", raw)
	}
	out, err = RenderGraphs(skill, true, dot)
	if err != nil || !strings.Contains(out, "  Rendered: my_skill_combined.svg\n  Source: my_skill_combined.dot\n") {
		t.Fatalf("combined: %v %q", err, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(skill, "diagrams", "my_skill_combined.dot")); !strings.HasPrefix(string(raw), "digraph my_skill_combined {") {
		t.Errorf("combined source %q", raw)
	}
	empty := filepath.Join(t.TempDir(), "plain")
	os.MkdirAll(empty, 0o755)
	os.WriteFile(filepath.Join(empty, "SKILL.md"), []byte("no graphs"), 0o644)
	if out, err := RenderGraphs(empty, false, dot); err != nil || !strings.HasPrefix(out, "No ```dot blocks found in ") {
		t.Errorf("no blocks: %v %q", err, out)
	}
	if _, err := RenderGraphs(filepath.Join(t.TempDir(), "missing"), false, dot); err == nil || !strings.Contains(err.Error(), "SKILL.md not found") {
		t.Errorf("missing skill: %v", err)
	}
	if _, err := RenderGraphs(skill, false, filepath.Join(t.TempDir(), "no-dot")); err == nil || !strings.Contains(err.Error(), "graphviz (dot) not found") {
		t.Errorf("no dot: %v", err)
	}
}

// TestAgainstNodeOriginal runs the unmodified Node originals and the port on the same fixtures. It needs node and the
// original tree (SUPERPOWERS_NODE_ORIGINAL: the directory holding brainstorming/ and writing-skills/), which a Package
// build does not have; imladris sets it.
func TestAgainstNodeOriginal(t *testing.T) {
	root := os.Getenv("SUPERPOWERS_NODE_ORIGINAL")
	if root == "" {
		t.Skip("SUPERPOWERS_NODE_ORIGINAL is not set: no Node original to compare with")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is required when SUPERPOWERS_NODE_ORIGINAL is set")
	}
	t.Run("render-graphs.js output and files", func(t *testing.T) {
		dot := fakeDot(t)
		for _, combine := range []bool{false, true} {
			nodeSkill, goSkill := filepath.Join(t.TempDir(), "my-skill"), filepath.Join(t.TempDir(), "my-skill")
			for _, d := range []string{nodeSkill, goSkill} {
				os.MkdirAll(d, 0o755)
				os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(skillWithGraphs), 0o644)
			}
			args := []string{filepath.Join(root, "writing-skills", "render-graphs.js"), nodeSkill}
			if combine {
				args = append(args, "--combine")
			}
			cmd := exec.Command(node, args...)
			cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(dot)+string(os.PathListSeparator)+os.Getenv("PATH"))
			want, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			got, err := RenderGraphs(goSkill, combine, dot)
			if err != nil {
				t.Fatal(err)
			}
			norm := func(s, dir string) string { return strings.ReplaceAll(s, dir, "<dir>") }
			if norm(got, goSkill) != norm(string(want), nodeSkill) {
				t.Errorf("combine=%v stdout\nnode: %q\ngo:   %q", combine, norm(string(want), nodeSkill), norm(got, goSkill))
			}
			nodeFiles, _ := filepath.Glob(filepath.Join(nodeSkill, "diagrams", "*"))
			goFiles, _ := filepath.Glob(filepath.Join(goSkill, "diagrams", "*"))
			if len(nodeFiles) != len(goFiles) || len(nodeFiles) == 0 {
				t.Fatalf("files %v vs %v", nodeFiles, goFiles)
			}
			for i := range nodeFiles {
				a, _ := os.ReadFile(nodeFiles[i])
				b, _ := os.ReadFile(goFiles[i])
				if filepath.Base(nodeFiles[i]) != filepath.Base(goFiles[i]) || !bytes.Equal(a, b) {
					t.Errorf("combine=%v %s differs:\n%q\n%q", combine, filepath.Base(nodeFiles[i]), a, b)
				}
			}
		}
	})
	t.Run("server.cjs and the port answer the same requests the same way", func(t *testing.T) {
		const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		fixture := func(dir string) {
			os.MkdirAll(filepath.Join(dir, "content"), 0o755)
			os.WriteFile(filepath.Join(dir, "content", "a.html"), []byte("<h2>Fragment</h2>"), 0o644)
			os.WriteFile(filepath.Join(dir, "content", "pic.png"), []byte("PNG"), 0o644)
			os.WriteFile(filepath.Join(dir, "content", "data.bin"), []byte("BIN"), 0o644)
		}
		nodeDir, goDir := t.TempDir(), filepath.Join(t.TempDir(), "s")
		fixture(nodeDir)
		fixture(goDir)
		listener, _ := net.Listen("tcp", "127.0.0.1:0")
		nodePort := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		// server.cjs reads the version of the package three levels above it; give it the tree it expects.
		tree := t.TempDir()
		scripts := filepath.Join(tree, "skills", "brainstorming", "scripts")
		os.MkdirAll(scripts, 0o755)
		for _, f := range []string{"server.cjs", "helper.js", "frame-template.html"} {
			raw, err := os.ReadFile(filepath.Join(root, "brainstorming", "scripts", f))
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(scripts, f), raw, 0o644)
		}
		os.WriteFile(filepath.Join(tree, "package.json"), []byte(`{"version":"`+companionVersion+`"}`), 0o644)
		cmd := exec.Command(node, filepath.Join(scripts, "server.cjs"))
		cmd.Env = append(os.Environ(), "BRAINSTORM_DIR="+nodeDir, fmt.Sprintf("BRAINSTORM_PORT=%d", nodePort), "BRAINSTORM_TOKEN="+token, "BRAINSTORM_HOST=127.0.0.1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(filepath.Join(nodeDir, "state", "server-info")); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		c, info, err := StartCompanion(CompanionOptions{SessionDir: goDir, Token: token, Env: envOf(nil)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Stop("test") })
		goPort := info["port"].(int)
		cookie := func(port int) map[string]string {
			return map[string]string{"Cookie": fmt.Sprintf("brainstorm-key-%d=%s", port, token)}
		}
		type probe struct {
			path    string
			headers func(int) map[string]string
		}
		none := func(int) map[string]string { return nil }
		for _, p := range []probe{{"/", none}, {"/?key=bad", none}, {"/?key=" + token, none}, {"/", cookie}, {"/files/pic.png", cookie}, {"/files/data.bin", cookie}, {"/files/.x", cookie}, {"/files/", cookie}, {"/nope", cookie}} {
			ns, nh, nb := get(t, fmt.Sprintf("http://127.0.0.1:%d%s", nodePort, p.path), p.headers(nodePort))
			gs, gh, gb := get(t, fmt.Sprintf("http://127.0.0.1:%d%s", goPort, p.path), p.headers(goPort))
			fix := func(s string, port int) string { return strings.ReplaceAll(s, fmt.Sprint(port), "<port>") }
			if ns != gs || fix(nb, nodePort) != fix(gb, goPort) {
				t.Errorf("%s: node %d %.160q, go %d %.160q", p.path, ns, fix(nb, nodePort), gs, fix(gb, goPort))
			}
			for _, h := range []string{"Content-Type", "Referrer-Policy", "Cache-Control", "X-Frame-Options", "Content-Security-Policy", "Cross-Origin-Resource-Policy"} {
				if nh.Get(h) != gh.Get(h) {
					t.Errorf("%s header %s: node %q, go %q", p.path, h, nh.Get(h), gh.Get(h))
				}
			}
			if fix(nh.Get("Set-Cookie"), nodePort) != fix(gh.Get("Set-Cookie"), goPort) {
				t.Errorf("%s Set-Cookie: node %q, go %q", p.path, nh.Get("Set-Cookie"), gh.Get("Set-Cookie"))
			}
		}
	})
}
