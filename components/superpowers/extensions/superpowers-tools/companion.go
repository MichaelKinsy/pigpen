package superpowers_tools

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A Go port of the visual companion of the brainstorming Skill: skills/brainstorming/scripts/server.cjs (obra/superpowers
// v7.0.0) with start-server.sh and stop-server.sh. The server runs inside the extension process instead of as a detached
// Node process, so its lifetime is the session's and there is no owner-PID watchdog.

// The browser-side assets are the original's, byte for byte (written here by port/adapt.py).
//
//go:embed companion_frame.html
var frameTemplate string

//go:embed companion_helper.js
var helperScript string

const (
	wsMagic             = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxFramePayload     = 10 * 1024 * 1024
	opText, opClose     = 0x01, 0x08
	opPing, opPong      = 0x09, 0x0A
	companionVersion    = "7.0.0"
	brandImageURL       = "https://primeradiant.com/brand/superpowers-visual-brainstorming-logo.png"
	defaultIdleTimeout  = 4 * time.Hour
	defaultCheckPeriod  = 60 * time.Second
	forbiddenPageHTML   = "<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><title>Session key required</title>\n<style>body { font-family: system-ui, sans-serif; padding: 2rem; max-width: 800px; margin: 0 auto; }\nh1 { color: #333; } p { color: #666; } code { background: #f0f0f0; padding: 0.1em 0.3em; border-radius: 4px; }</style>\n</head>\n<body><h1>Session key required</h1>\n<p>This page needs the full URL your coding agent gave you, including the\n<code>?key=&hellip;</code> part. Copy the complete URL and open it again.</p></body></html>"
	waitingPageTemplate = "<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><title>Brainstorm Companion</title>\n<style>\nbody { font-family: system-ui, sans-serif; padding: 2rem; max-width: 800px; margin: 0 auto; }\nh1 { color: #333; } p { color: #666; }\n.brand { display: flex; align-items: center; min-width: 0; overflow: hidden; margin-bottom: 1.5rem; color: #666; font-size: 0.9rem; line-height: 1; }\n.brand a { color: inherit; text-decoration: none; display: flex; align-items: center; gap: 0.5rem; min-width: 0; max-width: 100%; line-height: 1; }\n.brand-copy { display: block; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; line-height: 1; transform: translateY(-1px); }\n.brand-logo { display: block; height: 1em; width: auto; max-width: 180px; filter: invert(1); }\n</style>\n</head>\n<body><!-- BRANDING --><h1>Brainstorm Companion</h1>\n<p>Waiting for the agent to push a screen...</p></body></html>"
)

var mimeTypes = map[string]string{
	".html": "text/html", ".css": "text/css", ".js": "application/javascript", ".json": "application/json",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml",
}

var tokenRe = regexp.MustCompile(`(?i)^[0-9a-f]{32,}$`)

// CompanionOptions are the options of start-server.sh and the BRAINSTORM_* environment of server.cjs.
type CompanionOptions struct {
	ProjectDir   string // --project-dir: session files under <dir>/.superpowers/brainstorm/<id>, port and key persisted
	Host         string // --host, default 127.0.0.1
	URLHost      string // --url-host
	IdleTimeout  time.Duration
	Open         bool // --open (BRAINSTORM_OPEN)
	Port         int  // BRAINSTORM_PORT
	Token        string
	SessionDir   string // BRAINSTORM_DIR (set by Start)
	CheckPeriod  time.Duration
	Env          func(string) string // environment lookup (tests replace it)
	OpenCommand  func(name string, args ...string) error
	portFile     string
	tokenFile    string
	platformGOOS string
}

// Companion is one running server.
type Companion struct {
	opts       CompanionOptions
	host       string
	urlHost    string
	contentDir string
	stateDir   string
	listener   net.Listener
	server     *http.Server
	logFile    *os.File

	mu           sync.Mutex
	port         int
	token        string
	tokenSource  string
	cookieName   string
	clients      map[net.Conn]bool
	lastActivity time.Time
	browserOpen  bool
	known        map[string]bool
	mtimes       map[string]time.Time
	stopped      chan struct{}
	stopOnce     sync.Once
	telemetryOff bool
	idle         time.Duration
	ephemeral    bool // the session directory is ours, under the temp directory (no --project-dir)
}

func (o *CompanionOptions) env(name string) string {
	if o.Env != nil {
		return o.Env(name)
	}
	return os.Getenv(name)
}

func isTruthyEnv(v string) bool {
	n := strings.ToLower(strings.TrimSpace(v))
	switch n {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func randomHexToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// randomPort is a variable so a test can make the old fixed fallback port known and occupy it.
var randomPort = func() int {
	n, _ := rand.Int(rand.Reader, big.NewInt(16383))
	return 49152 + int(n.Int64())
}

func computeAcceptKey(clientKey string) string {
	sum := sha1.Sum([]byte(clientKey + wsMagic))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func encodeFrame(opcode byte, payload []byte) []byte {
	n := len(payload)
	var header []byte
	switch {
	case n < 126:
		header = []byte{0x80 | opcode, byte(n)}
	case n < 65536:
		header = []byte{0x80 | opcode, 126, byte(n >> 8), byte(n)}
	default:
		header = make([]byte, 10)
		header[0], header[1] = 0x80|opcode, 127
		binary.BigEndian.PutUint64(header[2:], uint64(n))
	}
	return append(header, payload...)
}

var errFrameTooBig = errors.New("WebSocket frame payload exceeds maximum allowed size")

// decodeFrame returns (opcode, payload, bytes consumed); consumed is 0 when the buffer holds less than a frame.
func decodeFrame(buf []byte) (byte, []byte, int, error) {
	if len(buf) < 2 {
		return 0, nil, 0, nil
	}
	opcode := buf[0] & 0x0F
	if buf[1]&0x80 == 0 {
		return 0, nil, 0, errors.New("Client frames must be masked")
	}
	length := uint64(buf[1] & 0x7F)
	offset := 2
	switch length {
	case 126:
		if len(buf) < 4 {
			return 0, nil, 0, nil
		}
		length, offset = uint64(binary.BigEndian.Uint16(buf[2:])), 4
	case 127:
		if len(buf) < 10 {
			return 0, nil, 0, nil
		}
		length, offset = binary.BigEndian.Uint64(buf[2:]), 10
		if length > maxFramePayload {
			return 0, nil, 0, errFrameTooBig
		}
	}
	if length > maxFramePayload {
		return 0, nil, 0, errFrameTooBig
	}
	total := offset + 4 + int(length)
	if len(buf) < total {
		return 0, nil, 0, nil
	}
	mask := buf[offset : offset+4]
	data := make([]byte, length)
	for i := range data {
		data[i] = buf[offset+4+i] ^ mask[i%4]
	}
	return opcode, data, total, nil
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(s)
}

func (c *Companion) brandMarkup() string {
	version := htmlEscape(companionVersion)
	text, logo := "Superpowers v"+version, ""
	if c.telemetryOff {
		text = "Prime Radiant Superpowers v" + version
	} else {
		logo = `<img class="brand-logo" src="` + brandImageURL + "?v=" + url.QueryEscape(companionVersion) + `" alt="Prime Radiant" referrerpolicy="no-referrer" decoding="async">`
	}
	return `<div class="brand"><a href="https://github.com/obra/superpowers">` + logo + `<span class="brand-copy">` + text + `</span></a></div>`
}

func (c *Companion) renderBranding(html string) string {
	return strings.ReplaceAll(html, "<!-- BRANDING -->", c.brandMarkup())
}

func isFullDocument(html string) bool {
	t := strings.ToLower(strings.TrimLeft(html, " \t\n\r\f\v\u00a0\ufeff"))
	return strings.HasPrefix(t, "<!doctype") || strings.HasPrefix(t, "<html")
}

func (c *Companion) wrapInFrame(content string) string {
	return strings.Replace(c.renderBranding(frameTemplate), "<!-- CONTENT -->", content, 1)
}

func (c *Companion) isRegularFileInContentDir(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !singleLink(info) {
		return false
	}
	realDir, err1 := filepath.EvalSymlinks(c.contentDir)
	realFile, err2 := filepath.EvalSymlinks(path)
	return err1 == nil && err2 == nil && strings.HasPrefix(realFile, realDir+string(filepath.Separator))
}

func (c *Companion) newestScreen() string {
	entries, _ := os.ReadDir(c.contentDir)
	type screen struct {
		path  string
		mtime time.Time
	}
	var screens []screen
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".html") {
			continue
		}
		p := filepath.Join(c.contentDir, name)
		if !c.isRegularFileInContentDir(p) {
			continue
		}
		if info, err := os.Stat(p); err == nil {
			screens = append(screens, screen{p, info.ModTime()})
		}
	}
	if len(screens) == 0 {
		return ""
	}
	sort.SliceStable(screens, func(i, j int) bool { return screens[i].mtime.After(screens[j].mtime) })
	return screens[0].path
}

func timingSafeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (c *Companion) currentToken() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, c.cookieName
}

// isAuthorized: the session key as ?key= (when present it alone decides) or as the session cookie.
func (c *Companion) isAuthorized(r *http.Request) bool {
	token, cookieName := c.currentToken()
	if q := r.URL.Query(); q.Has("key") {
		key := q.Get("key")
		return key != "" && timingSafeEqual(key, token)
	}
	if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" && timingSafeEqual(cookie.Value, token) {
		return true
	}
	return false
}

func setSecurityHeaders(w http.ResponseWriter, contentType string) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if contentType != "" {
		h.Set("Content-Type", contentType)
	} else {
		h["Content-Type"] = nil // the original sends none; net/http would sniff one
	}
}

func (c *Companion) touch() {
	c.mu.Lock()
	c.lastActivity = time.Now()
	c.mu.Unlock()
}

func (c *Companion) bootstrapPage(key string) string {
	jsonKey, _ := json.Marshal(key)
	return "<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><title>Opening Brainstorm Companion</title></head>\n<body>\n<script>\ntry { sessionStorage.setItem('brainstorm-session-key', " + string(jsonKey) + "); } catch (e) {}\nlocation.replace('/');\n</script>\n</body>\n</html>"
}

func (c *Companion) handle(w http.ResponseWriter, r *http.Request) {
	if !c.isAuthorized(r) {
		setSecurityHeaders(w, "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, forbiddenPageHTML)
		return
	}
	c.touch()
	token, cookieName := c.currentToken()
	w.Header().Set("Set-Cookie", cookieName+"="+token+"; HttpOnly; SameSite=Strict; Path=/")
	path := r.URL.Path
	key := r.URL.Query().Get("key")
	switch {
	case r.Method == http.MethodGet && path == "/" && key != "" && timingSafeEqual(key, token):
		setSecurityHeaders(w, "text/html; charset=utf-8")
		_, _ = io.WriteString(w, c.bootstrapPage(key))
	case r.Method == http.MethodGet && path == "/":
		html := ""
		if screen := c.newestScreen(); screen != "" {
			raw, _ := os.ReadFile(screen)
			html = string(raw)
			if !isFullDocument(html) {
				html = c.wrapInFrame(html)
			}
		} else {
			html = c.renderBranding(waitingPageTemplate)
		}
		injection := "<script>\n" + helperScript + "\n</script>"
		if strings.Contains(html, "</body>") {
			html = strings.Replace(html, "</body>", injection+"\n</body>", 1)
		} else {
			html += injection
		}
		setSecurityHeaders(w, "text/html; charset=utf-8")
		_, _ = io.WriteString(w, html)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/files/"):
		name := filepath.Base(strings.ReplaceAll(path[7:], "\\", "/"))
		file := filepath.Join(c.contentDir, name)
		if path[7:] == "" || name == "." || strings.HasPrefix(name, ".") || !c.isRegularFileInContentDir(file) {
			setSecurityHeaders(w, "")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "Not found")
			return
		}
		ct := mimeTypes[strings.ToLower(filepath.Ext(file))]
		if ct == "" {
			ct = "application/octet-stream"
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			setSecurityHeaders(w, "")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "Not found")
			return
		}
		setSecurityHeaders(w, ct)
		_, _ = w.Write(raw)
	default:
		setSecurityHeaders(w, "")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "Not found")
	}
}

func (c *Companion) allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return r.Host != "" && origin == "http://"+r.Host
}

func (c *Companion) upgrade(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !c.isAuthorized(r) || !c.allowedOrigin(r) || key == "" {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + computeAcceptKey(key) + "\r\n\r\n")
	_ = rw.Flush()
	c.mu.Lock()
	c.clients[conn] = true
	c.mu.Unlock()
	go c.readClient(conn, rw.Reader)
}

func (c *Companion) dropClient(conn net.Conn) {
	c.mu.Lock()
	delete(c.clients, conn)
	c.mu.Unlock()
}

func (c *Companion) readClient(conn net.Conn, rd *bufio.Reader) {
	defer c.dropClient(conn)
	defer conn.Close()
	var buf []byte
	chunk := make([]byte, 32*1024)
	for {
		n, err := rd.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for len(buf) > 0 {
				opcode, payload, used, derr := decodeFrame(buf)
				if derr != nil {
					_, _ = conn.Write(encodeFrame(opClose, nil))
					return
				}
				if used == 0 {
					break
				}
				buf = buf[used:]
				switch opcode {
				case opText:
					c.handleMessage(string(payload))
				case opClose:
					_, _ = conn.Write(encodeFrame(opClose, nil))
					return
				case opPing:
					_, _ = conn.Write(encodeFrame(opPong, payload))
				case opPong:
				default:
					_, _ = conn.Write(encodeFrame(opClose, []byte{0x03, 0xEB}))
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// handleMessage logs a user event and, when it has a choice, appends it to state/events.
func (c *Companion) handleMessage(text string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		return
	}
	c.touch()
	logged := map[string]any{"source": "user-event"}
	for k, v := range event {
		logged[k] = v
	}
	c.logJSON(logged)
	if choice, ok := event["choice"]; ok && choice != nil && choice != false && choice != "" && choice != float64(0) {
		line, _ := json.Marshal(event)
		if f, err := os.OpenFile(filepath.Join(c.stateDir, "events"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
}

func (c *Companion) logJSON(v any) {
	line, _ := json.Marshal(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.logFile != nil {
		_, _ = c.logFile.Write(append(line, '\n'))
	}
}

func (c *Companion) broadcast(msg any) {
	raw, _ := json.Marshal(msg)
	frame := encodeFrame(opText, raw)
	c.mu.Lock()
	conns := make([]net.Conn, 0, len(c.clients))
	for conn := range c.clients {
		conns = append(conns, conn)
	}
	c.mu.Unlock()
	for _, conn := range conns {
		if _, err := conn.Write(frame); err != nil {
			c.dropClient(conn)
		}
	}
}

func browserLauncher(url, goos, osRelease string, env func(string) string) (string, []string, bool) {
	isWSL := goos == "linux" && strings.Contains(strings.ToLower(osRelease), "microsoft")
	switch {
	case goos == "darwin":
		return "open", []string{url}, true
	case goos == "windows" || isWSL:
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}, true
	case env("DISPLAY") != "" || env("WAYLAND_DISPLAY") != "":
		return "xdg-open", []string{url}, true
	}
	return "", nil, false
}

func (c *Companion) url() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.urlLocked()
}

func (c *Companion) urlLocked() string {
	h := c.urlHost
	if !(strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]")) && strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return "http://" + h + ":" + strconv.Itoa(c.port) + "/?key=" + c.token
}

// maybeOpenBrowser opens the browser the first time a screen is ready: only when asked, on a loopback bind and when no
// browser is connected. BRAINSTORM_OPEN_CMD overrides the launcher (operator input, run through a shell).
func (c *Companion) maybeOpenBrowser() {
	c.mu.Lock()
	if c.browserOpen {
		c.mu.Unlock()
		return
	}
	c.browserOpen = true
	connected := len(c.clients) > 0
	target := c.urlLocked()
	c.mu.Unlock()
	if !c.opts.Open || (c.host != "127.0.0.1" && c.host != "localhost") || connected {
		return
	}
	run := c.opts.OpenCommand
	if run == nil {
		run = func(name string, args ...string) error { return exec.Command(name, args...).Start() }
	}
	if custom := c.opts.env("BRAINSTORM_OPEN_CMD"); custom != "" {
		quoted, _ := json.Marshal(target)
		_ = run("sh", "-c", custom+" "+string(quoted))
		return
	}
	goos := c.opts.platformGOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if bin, args, ok := browserLauncher(target, goos, osRelease(), c.opts.env); ok {
		_ = run(bin, args...)
	}
}

func osRelease() string {
	raw, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.TrimSpace(string(raw))
}

func htmlScreens(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".html") {
			out = append(out, e.Name())
		}
	}
	return out
}

// watch polls the content directory (the original uses fs.watch with a 100 ms debounce per file) and tells a new
// screen from an update: a new file clears state/events, and either broadcasts a reload.
func (c *Companion) watch() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopped:
			return
		case <-ticker.C:
		}
		for _, name := range htmlScreens(c.contentDir) {
			p := filepath.Join(c.contentDir, name)
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			stamp := info.ModTime().Add(time.Duration(info.Size()))
			c.mu.Lock()
			seen := c.known[name]
			changed := !seen || !c.mtimes[name].Equal(stamp)
			c.known[name] = true
			c.mtimes[name] = stamp
			c.mu.Unlock()
			if !changed {
				continue
			}
			c.touch()
			if !seen {
				_ = os.Remove(filepath.Join(c.stateDir, "events"))
				c.logJSON(map[string]any{"type": "screen-added", "file": p})
				c.maybeOpenBrowser()
			} else {
				c.logJSON(map[string]any{"type": "screen-updated", "file": p})
			}
			c.broadcast(map[string]any{"type": "reload"})
		}
	}
}

func readTrim(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// StartCompanion starts a server as start-server.sh does and returns the startup record (the "server-started" JSON).
func StartCompanion(opts CompanionOptions) (*Companion, map[string]any, error) {
	c := &Companion{opts: opts, clients: map[net.Conn]bool{}, known: map[string]bool{}, mtimes: map[string]time.Time{}, stopped: make(chan struct{}), lastActivity: time.Now()}
	c.host = opts.Host
	if c.host == "" {
		c.host = "127.0.0.1"
	}
	c.urlHost = opts.URLHost
	if c.urlHost == "" {
		if c.host == "127.0.0.1" || c.host == "localhost" {
			c.urlHost = "localhost"
		} else {
			c.urlHost = c.host
		}
	}
	c.idle = opts.IdleTimeout
	if c.idle <= 0 {
		c.idle = defaultIdleTimeout
	}
	period := opts.CheckPeriod
	if period <= 0 {
		period = defaultCheckPeriod
	}
	for _, name := range []string{"SUPERPOWERS_DISABLE_TELEMETRY", "DISABLE_TELEMETRY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"} {
		if isTruthyEnv(opts.env(name)) {
			c.telemetryOff = true
		}
	}
	id := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().Unix(), 10)
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		if opts.ProjectDir != "" {
			base := filepath.Join(opts.ProjectDir, ".superpowers", "brainstorm")
			sessionDir = filepath.Join(base, id)
			c.opts.portFile, c.opts.tokenFile = filepath.Join(base, ".last-port"), filepath.Join(base, ".last-token")
		} else {
			sessionDir = filepath.Join(os.TempDir(), "brainstorm-"+id)
			c.ephemeral = true
		}
	}
	c.contentDir, c.stateDir = filepath.Join(sessionDir, "content"), filepath.Join(sessionDir, "state")
	for _, d := range []string{c.contentDir, c.stateDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range htmlScreens(c.contentDir) {
		c.known[name] = true
	}
	_ = os.Remove(filepath.Join(c.stateDir, "server-stopped"))
	logFile, err := os.OpenFile(filepath.Join(c.stateDir, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	c.logFile = logFile

	// Port: explicit, else the one this project last bound, else random. The key follows the same rule.
	c.port = opts.Port
	if c.port == 0 {
		if p, err := strconv.Atoi(readTrim(c.opts.portFile)); err == nil && p > 1023 && p < 65536 {
			c.port = p
		} else {
			c.port = randomPort()
		}
	}
	switch {
	case opts.Token != "":
		c.token, c.tokenSource = opts.Token, "env"
	case tokenRe.MatchString(readTrim(c.opts.tokenFile)):
		c.token, c.tokenSource = readTrim(c.opts.tokenFile), "file"
		_ = os.Chmod(c.opts.tokenFile, 0o600)
	default:
		c.token, c.tokenSource = randomHexToken(), "generated"
	}
	c.cookieName = "brainstorm-key-" + strconv.Itoa(c.port)

	c.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			c.upgrade(w, r)
			return
		}
		c.handle(w, r)
	})}

	fellBack := false
	listen := func() (net.Listener, error) { return net.Listen("tcp", net.JoinHostPort(c.host, strconv.Itoa(c.port))) }
	ln, err := listen()
	if err != nil && isAddrInUse(err) {
		if c.tokenSource == "env" {
			return nil, nil, errors.New("Server failed to bind: preferred port is in use and BRAINSTORM_TOKEN is set; refusing fallback with explicit token")
		}
		fellBack = true
		// DIVERGENCE superpowers-tools #6 (port/PORT.md): server.cjs falls back once to a random port from 49152-65535 and
		// fails if that one is taken too. Here the fallback binds port 0 so the OS picks a free one, and c.port is set to
		// the port it got (below) so the log, server-info, the URL and the cookie name report it. Remove when upstream
		// server.cjs retries or uses an OS-chosen port.
		c.port = 0
		if c.tokenSource == "file" {
			c.token, c.tokenSource = randomHexToken(), "generated-fallback"
		}
		ln, err = listen()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("Server failed to bind: %w", err)
	}
	c.listener = ln
	if fellBack {
		c.port = ln.Addr().(*net.TCPAddr).Port
	}
	c.cookieName = "brainstorm-key-" + strconv.Itoa(c.port)
	if c.opts.portFile != "" && !fellBack {
		_ = os.WriteFile(c.opts.portFile, []byte(strconv.Itoa(c.port)), 0o644)
		if c.opts.tokenFile != "" {
			_ = os.WriteFile(c.opts.tokenFile, []byte(c.token), 0o600)
			_ = os.Chmod(c.opts.tokenFile, 0o600)
		}
	}
	info := map[string]any{"type": "server-started", "port": c.port, "host": c.host, "url_host": c.urlHost, "url": c.url(), "screen_dir": c.contentDir, "state_dir": c.stateDir, "idle_timeout_ms": c.idle.Milliseconds()}
	raw, _ := json.Marshal(info)
	c.logJSON(info)
	_ = os.WriteFile(filepath.Join(c.stateDir, "server-info"), append(raw, '\n'), 0o600)
	go func() { _ = c.server.Serve(ln) }()
	go c.watch()
	go func() {
		t := time.NewTicker(period)
		defer t.Stop()
		for {
			select {
			case <-c.stopped:
				return
			case <-t.C:
				c.mu.Lock()
				idle := time.Since(c.lastActivity) > c.idle
				c.mu.Unlock()
				if idle {
					c.Stop("idle timeout")
					return
				}
			}
		}
	}()
	return c, info, nil
}

func isAddrInUse(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && strings.Contains(strings.ToLower(op.Err.Error()), "address already in use")
}

// Stop shuts the server down as server.cjs's shutdown does: server-stopped replaces server-info.
func (c *Companion) Stop(reason string) {
	c.stopOnce.Do(func() {
		c.logJSON(map[string]any{"type": "server-stopped", "reason": reason})
		_ = os.Remove(filepath.Join(c.stateDir, "server-info"))
		stamp, _ := json.Marshal(map[string]any{"reason": reason, "timestamp": time.Now().UnixMilli()})
		_ = os.WriteFile(filepath.Join(c.stateDir, "server-stopped"), append(stamp, '\n'), 0o644)
		close(c.stopped)
		c.mu.Lock()
		for conn := range c.clients {
			_ = conn.Close()
		}
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.server.Shutdown(ctx)
		_ = c.server.Close()
		// http.Server only closes listeners it has tracked, and Serve runs in its own goroutine: a Stop that lands before
		// Serve has started would leave the port bound until that goroutine is scheduled. Close it here as well.
		_ = c.listener.Close()
	})
}

// Done is closed once the server has stopped.
func (c *Companion) Done() <-chan struct{} { return c.stopped }

// SessionDir is the directory holding content/ and state/.
func (c *Companion) SessionDir() string { return filepath.Dir(c.contentDir) }

var (
	companionsMu sync.Mutex
	companions   = map[string]*Companion{}
)

// StopCompanion is stop-server.sh: stop the server of a session directory and tidy up. A directory with no running
// server of this extension reports not_running; ephemeral (/tmp) session directories are removed.
func StopCompanion(sessionDir string) map[string]any {
	if sessionDir == "" {
		return map[string]any{"error": "Usage: stop-server.sh <session_dir>"}
	}
	companionsMu.Lock()
	c := companions[sessionDir]
	delete(companions, sessionDir)
	companionsMu.Unlock()
	if c == nil {
		if _, err := os.Stat(filepath.Join(sessionDir, "state", "server-info")); err == nil {
			// a record left by a server that is gone (an earlier session of the extension)
			_ = os.Remove(filepath.Join(sessionDir, "state", "server-info"))
			_ = os.WriteFile(filepath.Join(sessionDir, "state", "server-stopped"), []byte(fmt.Sprintf("{\"reason\":\"stale_pid\",\"timestamp\":%d}\n", time.Now().Unix())), 0o644)
			return map[string]any{"status": "stale_pid"}
		}
		return map[string]any{"status": "not_running"}
	}
	c.Stop("stop-server.sh")
	_ = os.Remove(filepath.Join(sessionDir, "state", "server.log"))
	if strings.HasPrefix(sessionDir, "/tmp/") || c.ephemeral {
		_ = os.RemoveAll(sessionDir)
	}
	return map[string]any{"status": "stopped"}
}
