package pi_subagents

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LSP diagnostics evidence (watchdog/lsp-diagnostics.ts): the changed TypeScript and JavaScript files are opened in a
// typescript-language-server (the project's, else the one on PATH) and the diagnostics it publishes become review evidence.

type watchdogLspRequest struct {
	Cwd          string
	Root         string
	ChangedPaths []string
	Config       wdLsp
	Signal       <-chan struct{}
}

type watchdogLspFunc func(req watchdogLspRequest) wdLspResult

type wdLspTarget struct {
	relPath, absPath, uri, languageID string
}

type wdLspCommand struct {
	command string
	args    []string
	label   string
}

var wdLspExtensions = map[string]string{
	".ts": "typescript", ".tsx": "typescriptreact", ".mts": "typescript", ".cts": "typescript",
	".js": "javascript", ".jsx": "javascriptreact", ".mjs": "javascript", ".cjs": "javascript",
}

const (
	wdLspProvider        = "typescript-language-server"
	wdLspMaxMessage      = 500
	wdLspMaxStderr       = 2_000
	wdLspShutdownTimeout = 250 * time.Millisecond
)

func wdIsPathInsideRoot(absPath, root string) bool {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return false
	}
	return rel == "" || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func wdLanguageID(filePath string) string {
	return wdLspExtensions[strings.ToLower(filepath.Ext(filePath))]
}

var wdSpaceRuns = regexp.MustCompile(`\s+`)

func wdTrimDiagnosticMessage(message string) string {
	n := jsTrim(wdSpaceRuns.ReplaceAllString(message, " "))
	if wdLen(n) > wdLspMaxMessage {
		return wdSlice(n, 0, wdLspMaxMessage-1) + "…"
	}
	return n
}

func wdSeverityFromLsp(v float64, ok bool) string {
	if ok {
		switch v {
		case 1:
			return "error"
		case 2:
			return "warning"
		case 3:
			return "info"
		}
	}
	return "hint"
}

func wdPathExecutable(filePath string) bool {
	st, err := os.Stat(filePath)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode().Perm()&0o111 != 0
}

func wdPathExecutableNames(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".EXE;.CMD;.BAT"
	}
	var exts []string
	for _, e := range strings.Split(pathext, ";") {
		if e != "" {
			exts = append(exts, e)
		}
	}
	names := []string{name}
	for _, e := range exts {
		names = append(names, name+strings.ToLower(e))
	}
	for _, e := range exts {
		names = append(names, name+strings.ToUpper(e))
	}
	return names
}

func wdResolveTypeScriptLanguageServer(root string) *wdLspCommand {
	for _, name := range wdPathExecutableNames(wdLspProvider) {
		local := filepath.Join(root, "node_modules", ".bin", name)
		if wdPathExecutable(local) {
			return &wdLspCommand{command: local, args: []string{"--stdio"}, label: wdLspProvider + " (project)"}
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range wdPathExecutableNames(wdLspProvider) {
			candidate := filepath.Join(dir, name)
			if wdPathExecutable(candidate) {
				return &wdLspCommand{command: candidate, args: []string{"--stdio"}, label: wdLspProvider}
			}
		}
	}
	return nil
}

func wdFileURI(absPath string) string {
	p := filepath.ToSlash(absPath)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func wdCollectTargetFiles(root string, changedPaths []string, maxFiles int) (targets []wdLspTarget, skipped []string, err error) {
	skipped = []string{}
	for _, changed := range changedPaths {
		rel := wdNormalizeRelPath(changed)
		abs, _ := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
		lang := wdLanguageID(abs)
		if lang == "" || !wdIsPathInsideRoot(abs, root) {
			skipped = append(skipped, rel)
			continue
		}
		st, statErr := os.Stat(abs)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				skipped = append(skipped, rel)
				continue
			}
			return nil, nil, statErr
		}
		if !st.Mode().IsRegular() || len(targets) >= maxFiles {
			skipped = append(skipped, rel)
			continue
		}
		targets = append(targets, wdLspTarget{relPath: rel, absPath: abs, uri: wdFileURI(abs), languageID: lang})
	}
	return targets, skipped, nil
}

func wdDiagnosticIdentity(d wdLspDiagnostic) string {
	return wdSHA256(strings.Join([]string{d.Path, d.Severity, d.Source, d.Code, d.Message}, "\n"))
}

// watchdogLspLedger remembers the diagnostics of earlier boundaries so a review sees only the fresh ones.
type watchdogLspLedger struct{ seen map[string]map[string]bool }

func newWatchdogLspLedger() *watchdogLspLedger {
	return &watchdogLspLedger{seen: map[string]map[string]bool{}}
}

func (l *watchdogLspLedger) reset() { l.seen = map[string]map[string]bool{} }

func (l *watchdogLspLedger) reduce(result wdLspResult) wdLspResult {
	if result.Status == "disabled" || result.Status == "unavailable" || result.Status == "failed" {
		return result
	}
	currentByPath := map[string]map[string]bool{}
	fresh := []wdLspDiagnostic{}
	for _, d := range result.Diagnostics {
		id := wdDiagnosticIdentity(d)
		current := currentByPath[d.Path]
		if current == nil {
			current = map[string]bool{}
			currentByPath[d.Path] = current
		}
		current[id] = true
		if !l.seen[d.Path][id] {
			fresh = append(fresh, d)
		}
	}
	for p, ids := range currentByPath {
		l.seen[p] = ids
	}
	if result.Status == "ok" {
		for _, checked := range result.CheckedPaths {
			if _, ok := currentByPath[checked]; !ok {
				delete(l.seen, checked)
			}
		}
	}
	result.Diagnostics = fresh
	return result
}

func wdFormatDiagnostic(d wdLspDiagnostic) string {
	code := ""
	if d.Code != "" {
		code = " " + d.Code
	}
	return d.Path + ":" + itoa(d.Line) + ":" + itoa(d.Column) + " " + d.Severity + code + " " + d.Source + ": " + d.Message
}

func wdActionableDiagnostics(result wdLspResult) []wdLspDiagnostic {
	var out []wdLspDiagnostic
	for _, d := range result.Diagnostics {
		if d.Severity == "error" || d.Severity == "warning" {
			out = append(out, d)
		}
	}
	return out
}

func formatWatchdogLspDiagnosticsBlock(result wdLspResult) string {
	actionable := wdActionableDiagnostics(result)
	if len(actionable) == 0 {
		return ""
	}
	lines := []string{"LSP diagnostics:"}
	for _, d := range actionable {
		lines = append(lines, "- "+wdFormatDiagnostic(d))
	}
	return strings.Join(lines, "\n")
}

func watchdogWarningFromLspDiagnostics(result wdLspResult) *wdWarning {
	actionable := wdActionableDiagnostics(result)
	if len(actionable) == 0 {
		return nil
	}
	var errs []wdLspDiagnostic
	for _, d := range actionable {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	severity, kind, count, primary := "concern", "warning", len(actionable), actionable[0]
	if len(errs) > 0 {
		severity, kind, count, primary = "blocker", "error", len(errs), errs[0]
	}
	plural, files := "s", "files"
	if count == 1 {
		plural, files = "", "file"
	}
	shown := actionable
	if len(shown) > 5 {
		shown = shown[:5]
	}
	lines := make([]string, len(shown))
	for i, d := range shown {
		lines[i] = wdFormatDiagnostic(d)
	}
	evidence := strings.Join(lines, "\n")
	if evidence == "" {
		evidence = wdFormatDiagnostic(primary)
	}
	return &wdWarning{Severity: severity, Category: "correctness", Importance: "high", Source: "lsp",
		Summary:           "LSP found " + itoa(count) + " " + kind + plural + " in changed " + files + ".",
		Evidence:          evidence,
		RecommendedAction: "Fix the reported diagnostics or explain why they are expected before accepting the change."}
}

// ── JSON-RPC client ──

type wdLspPending struct{ ch chan wdLspReply }

type wdLspReply struct {
	result any
	err    error
}

type wdLspClient struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	mu          sync.Mutex
	nextID      int
	pending     map[int]*wdLspPending
	diagnostics map[string][]any
	stderr      string
	exited      bool
	terminating bool
	exitCh      chan struct{}
	writeMu     sync.Mutex
	// fatal is the protocol error that ended the session: a request made after it fails with it, whenever it was made.
	fatal error
}

func startWdLspClient(command wdLspCommand, root string) (*wdLspClient, error) {
	cmd := exec.Command(command.command, command.args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &wdLspClient{cmd: cmd, stdin: stdin, nextID: 1, pending: map[int]*wdLspPending{}, diagnostics: map[string][]any{}, exitCh: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.stderr += string(buf[:n])
				if wdLen(c.stderr) > wdLspMaxStderr {
					c.stderr = wdSliceFrom(c.stderr, wdLen(c.stderr)-wdLspMaxStderr)
				}
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		c.readStdout(stdout)
		err := cmd.Wait()
		c.mu.Lock()
		c.exited = true
		c.mu.Unlock()
		msg := "language server exited"
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() >= 0 {
				msg += " with code " + itoa(exit.ExitCode())
			} else {
				msg += " signal " + strings.TrimPrefix(exit.String(), "signal: ")
			}
		} else if err == nil {
			msg += " with code 0"
		}
		c.mu.Lock()
		fatal := c.fatal
		c.mu.Unlock()
		if fatal != nil {
			c.rejectPending(fatal)
		} else {
			c.rejectPending(errors.New(msg))
		}
		close(c.exitCh)
	}()
	return c, nil
}

func (c *wdLspClient) rejectPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int]*wdLspPending{}
	c.mu.Unlock()
	for _, p := range pending {
		p.ch <- wdLspReply{err: err}
	}
}

func (c *wdLspClient) stderrTail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return jsTrim(c.stderr)
}

func (c *wdLspClient) send(payload *jsObject) error {
	c.mu.Lock()
	exited := c.exited
	c.mu.Unlock()
	if exited {
		return errors.New("language server already exited")
	}
	body := marshalJSON(payload, "")
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := io.WriteString(c.stdin, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
	return err
}

var wdContentLengthRe = regexp.MustCompile(`(?i)content-length:\s*(\d+)`)

func (c *wdLspClient) readStdout(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		header := ""
		for !strings.HasSuffix(header, "\r\n\r\n") {
			b, err := br.ReadByte()
			if err != nil {
				return
			}
			header += string(b)
		}
		m := wdContentLengthRe.FindStringSubmatch(header)
		if m == nil {
			continue
		}
		length, _ := strconv.Atoi(m[1])
		body := make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		parsed, err := parseJSON(body)
		if err != nil {
			c.failProtocol(fmt.Errorf("Invalid LSP JSON-RPC response: %s", err.Error()))
			return
		}
		c.handleMessage(parsed)
	}
}

func (c *wdLspClient) handleMessage(message any) {
	if wdPropV(message, "method") == "textDocument/publishDiagnostics" {
		params := wdPropV(message, "params")
		uri, ok := wdPropV(params, "uri").(string)
		diags, ok2 := wdPropV(params, "diagnostics").([]any)
		if ok && ok2 {
			c.mu.Lock()
			c.diagnostics[uri] = diags
			c.mu.Unlock()
		}
		return
	}
	id, ok := wdPropV(message, "id").(float64)
	if !ok {
		return
	}
	c.mu.Lock()
	p := c.pending[int(id)]
	delete(c.pending, int(id))
	c.mu.Unlock()
	if p == nil {
		return
	}
	if e := wdPropV(message, "error"); jsTruthy(e) {
		msg, _ := wdPropV(e, "message").(string)
		if msg == "" {
			msg = "LSP request " + jsNumber(id) + " failed"
		}
		p.ch <- wdLspReply{err: errors.New(msg)}
		return
	}
	p.ch <- wdLspReply{result: wdPropV(message, "result")}
}

func (c *wdLspClient) failProtocol(err error) {
	c.mu.Lock()
	exited := c.exited
	if !exited && c.fatal == nil {
		c.fatal = err
	}
	c.mu.Unlock()
	if exited {
		return
	}
	c.rejectPending(err)
	c.kill()
}

func (c *wdLspClient) kill() {
	c.mu.Lock()
	if c.exited || c.terminating {
		c.mu.Unlock()
		return
	}
	c.terminating = true
	c.mu.Unlock()
	terminate(c.cmd.Process)
}

func (c *wdLspClient) notify(method string, params any) error {
	return c.send(wdMsg("jsonrpc", "2.0", "method", method, "params", params))
}

// request sends a request and waits for the reply, the timeout or the abort signal.
func (c *wdLspClient) request(method string, params any, timeout time.Duration, signal <-chan struct{}) (any, error) {
	if wdClosed(signal) {
		return nil, errors.New("aborted")
	}
	c.mu.Lock()
	if c.fatal != nil {
		err := c.fatal
		c.mu.Unlock()
		return nil, err
	}
	id := c.nextID
	c.nextID++
	p := &wdLspPending{ch: make(chan wdLspReply, 1)}
	c.pending[id] = p
	c.mu.Unlock()
	if err := c.send(wdMsg("jsonrpc", "2.0", "id", float64(id), "method", method, "params", params)); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		fatal := c.fatal
		c.mu.Unlock()
		if fatal != nil {
			return nil, fatal
		}
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-p.ch:
		return r.result, r.err
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, errors.New(method + " timed out")
	case <-signal:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, errors.New("aborted")
	}
}

func (c *wdLspClient) shutdown() {
	c.mu.Lock()
	exited, terminating := c.exited, c.terminating
	c.mu.Unlock()
	if exited {
		return
	}
	if !terminating {
		if _, err := c.request("shutdown", nil, wdLspShutdownTimeout, nil); err != nil {
			c.kill()
		} else {
			_ = c.notify("exit", nil)
		}
	}
	select {
	case <-c.exitCh:
	case <-time.After(wdLspShutdownTimeout):
		_ = c.cmd.Process.Kill()
	}
	_ = c.stdin.Close()
}

func wdConvertDiagnostics(target wdLspTarget, diagnostics []any) []wdLspDiagnostic {
	var out []wdLspDiagnostic
	for _, d := range diagnostics {
		message, ok := wdPropV(d, "message").(string)
		start := wdPropV(wdPropV(d, "range"), "start")
		if !ok || !jsTruthy(start) {
			continue
		}
		line, _ := wdPropV(start, "line").(float64)
		character, _ := wdPropV(start, "character").(float64)
		sev, sevOK := wdPropV(d, "severity").(float64)
		source, _ := wdPropV(d, "source").(string)
		if source == "" {
			source = wdLspProvider
		}
		diag := wdLspDiagnostic{Path: target.relPath, Line: int(maxFloat(1, line+1)), Column: int(maxFloat(1, character+1)),
			Severity: wdSeverityFromLsp(sev, sevOK), Source: source, Message: wdTrimDiagnosticMessage(message)}
		if code, present := wdProp(d, "code"); present {
			switch x := code.(type) {
			case string:
				diag.Code = x
			case float64:
				diag.Code = jsNumber(x)
			case bool:
				diag.Code = strconv.FormatBool(x)
			case nil:
				diag.Code = "null"
			}
		}
		out = append(out, diag)
	}
	return out
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func wdInitializeParams(root string) *jsObject {
	rootURI := wdFileURI(root)
	name := filepath.Base(root)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "workspace"
	}
	return wdMsg("processId", float64(os.Getpid()), "rootUri", rootURI,
		"capabilities", wdMsg("textDocument", wdMsg("publishDiagnostics", wdMsg("relatedInformation", false, "versionSupport", true)),
			"workspace", wdMsg("configuration", false, "workspaceFolders", true)),
		"workspaceFolders", []any{wdMsg("uri", rootURI, "name", name)})
}

func (c *wdLspClient) hasAll(targets []wdLspTarget) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range targets {
		if _, ok := c.diagnostics[t.uri]; !ok {
			return false
		}
	}
	return true
}

func wdWaitForDiagnostics(c *wdLspClient, targets []wdLspTarget, timeout time.Duration, signal <-chan struct{}) bool {
	started := time.Now()
	for !wdClosed(signal) && time.Since(started) < timeout {
		if c.hasAll(targets) {
			return true
		}
		wait := timeout - time.Since(started)
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		select {
		case <-time.After(wait):
		case <-signal:
		}
	}
	return c.hasAll(targets)
}

func wdCollectWithLanguageServer(root string, targets []wdLspTarget, skipped []string, command wdLspCommand, config wdLsp, signal <-chan struct{}) wdLspResult {
	started := time.Now()
	checked := make([]string, len(targets))
	for i, t := range targets {
		checked[i] = t.relPath
	}
	remaining := func() time.Duration {
		r := time.Duration(config.TimeoutMs)*time.Millisecond - time.Since(started)
		if r < time.Millisecond {
			return time.Millisecond
		}
		return r
	}
	client, err := startWdLspClient(command, root)
	if err != nil {
		return wdLspResult{Status: "failed", Provider: command.label, CheckedPaths: checked, SkippedPaths: skipped, Diagnostics: []wdLspDiagnostic{}, Message: err.Error()}
	}
	stopAbort := make(chan struct{})
	if signal != nil {
		go func() {
			select {
			case <-signal:
				client.kill()
			case <-stopAbort:
			}
		}()
	}
	defer func() {
		close(stopAbort)
		client.shutdown()
	}()
	fail := func(err error) wdLspResult {
		message := err.Error()
		timedOut := strings.Contains(message, "timed out") || message == "aborted"
		status := "failed"
		if timedOut {
			status = "timeout"
		}
		if stderr := client.stderrTail(); stderr != "" {
			message += "; " + stderr
		}
		return wdLspResult{Status: status, Provider: command.label, CheckedPaths: checked, SkippedPaths: skipped, Diagnostics: []wdLspDiagnostic{}, Message: message}
	}
	if _, err := client.request("initialize", wdInitializeParams(root), remaining(), signal); err != nil {
		return fail(err)
	}
	if err := client.notify("initialized", newObject()); err != nil {
		return fail(err)
	}
	for _, target := range targets {
		b, err := os.ReadFile(target.absPath)
		if err != nil {
			return fail(err)
		}
		text := string(b)
		if err := client.notify("textDocument/didOpen", wdMsg("textDocument", wdMsg("uri", target.uri, "languageId", target.languageID, "version", float64(1), "text", text))); err != nil {
			return fail(err)
		}
		if err := client.notify("textDocument/didSave", wdMsg("textDocument", wdMsg("uri", target.uri), "text", text)); err != nil {
			return fail(err)
		}
	}
	complete := wdWaitForDiagnostics(client, targets, remaining(), signal)
	var diagnostics []wdLspDiagnostic
	for _, target := range targets {
		client.mu.Lock()
		raw := client.diagnostics[target.uri]
		client.mu.Unlock()
		diagnostics = append(diagnostics, wdConvertDiagnostics(target, raw)...)
	}
	if len(diagnostics) > config.MaxDiagnostics {
		diagnostics = diagnostics[:config.MaxDiagnostics]
	}
	if diagnostics == nil {
		diagnostics = []wdLspDiagnostic{}
	}
	result := wdLspResult{Status: "ok", Provider: command.label, CheckedPaths: checked, SkippedPaths: skipped, Diagnostics: diagnostics}
	if !complete {
		result.Status = "timeout"
		result.Message = "Timed out waiting " + itoa(config.TimeoutMs) + "ms for fresh LSP diagnostics."
	}
	return result
}

func collectWatchdogLspDiagnostics(req watchdogLspRequest) wdLspResult {
	if !req.Config.Enabled {
		return wdLspResult{Status: "disabled", CheckedPaths: []string{}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}}
	}
	base := req.Root
	if base == "" {
		base = req.Cwd
	}
	root, _ := filepath.Abs(base)
	targets, skipped, err := wdCollectTargetFiles(root, req.ChangedPaths, req.Config.MaxFiles)
	if err != nil {
		return wdLspResult{Status: "failed", CheckedPaths: []string{}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}, Message: err.Error()}
	}
	if len(targets) == 0 {
		return wdLspResult{Status: "skipped", CheckedPaths: []string{}, SkippedPaths: skipped, Diagnostics: []wdLspDiagnostic{}, Message: "No changed TypeScript or JavaScript files to check."}
	}
	command := wdResolveTypeScriptLanguageServer(root)
	if command == nil {
		all := append([]string{}, skipped...)
		for _, t := range targets {
			all = append(all, t.relPath)
		}
		return wdLspResult{Status: "unavailable", Provider: wdLspProvider, CheckedPaths: []string{}, SkippedPaths: all, Diagnostics: []wdLspDiagnostic{},
			Message: wdLspProvider + " was not found in project node_modules/.bin or PATH."}
	}
	return wdCollectWithLanguageServer(root, targets, skipped, *command, req.Config, req.Signal)
}
