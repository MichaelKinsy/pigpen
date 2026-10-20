package pi_subagents

import (
	"io"

	"os/exec"

	"regexp"

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

func wdIsPathInsideRoot(absPath, root string) bool { panic("not implemented") }

func wdLanguageID(filePath string) string { panic("not implemented") }

var wdSpaceRuns = regexp.MustCompile(`\s+`)

func wdTrimDiagnosticMessage(message string) string { panic("not implemented") }

func wdSeverityFromLsp(v float64, ok bool) string { panic("not implemented") }

func wdPathExecutable(filePath string) bool { panic("not implemented") }

func wdPathExecutableNames(name string) []string { panic("not implemented") }

func wdResolveTypeScriptLanguageServer(root string) *wdLspCommand { panic("not implemented") }

func wdFileURI(absPath string) string { panic("not implemented") }

func wdCollectTargetFiles(root string, changedPaths []string, maxFiles int) (targets []wdLspTarget, skipped []string, err error) {
	panic("not implemented")
}

func wdDiagnosticIdentity(d wdLspDiagnostic) string { panic("not implemented") }

// watchdogLspLedger remembers the diagnostics of earlier boundaries so a review sees only the fresh ones.
type watchdogLspLedger struct{ seen map[string]map[string]bool }

func newWatchdogLspLedger() *watchdogLspLedger { panic("not implemented") }

func (l *watchdogLspLedger) reset() { panic("not implemented") }

func (l *watchdogLspLedger) reduce(result wdLspResult) wdLspResult { panic("not implemented") }

func wdFormatDiagnostic(d wdLspDiagnostic) string { panic("not implemented") }

func wdActionableDiagnostics(result wdLspResult) []wdLspDiagnostic { panic("not implemented") }

func formatWatchdogLspDiagnosticsBlock(result wdLspResult) string { panic("not implemented") }

func watchdogWarningFromLspDiagnostics(result wdLspResult) *wdWarning { panic("not implemented") }

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
	panic("not implemented")
}

func (c *wdLspClient) rejectPending(err error) { panic("not implemented") }

func (c *wdLspClient) stderrTail() string { panic("not implemented") }

func (c *wdLspClient) send(payload *jsObject) error { panic("not implemented") }

var wdContentLengthRe = regexp.MustCompile(`(?i)content-length:\s*(\d+)`)

func (c *wdLspClient) readStdout(r io.Reader) { panic("not implemented") }

func (c *wdLspClient) handleMessage(message any) { panic("not implemented") }

func (c *wdLspClient) failProtocol(err error) { panic("not implemented") }

func (c *wdLspClient) kill() { panic("not implemented") }

func (c *wdLspClient) notify(method string, params any) error { panic("not implemented") }

// request sends a request and waits for the reply, the timeout or the abort signal.
func (c *wdLspClient) request(method string, params any, timeout time.Duration, signal <-chan struct{}) (any, error) {
	panic("not implemented")
}

func (c *wdLspClient) shutdown() { panic("not implemented") }

func wdConvertDiagnostics(target wdLspTarget, diagnostics []any) []wdLspDiagnostic {
	panic("not implemented")
}

func maxFloat(a, b float64) float64 { panic("not implemented") }

func wdInitializeParams(root string) *jsObject { panic("not implemented") }

func (c *wdLspClient) hasAll(targets []wdLspTarget) bool { panic("not implemented") }

func wdWaitForDiagnostics(c *wdLspClient, targets []wdLspTarget, timeout time.Duration, signal <-chan struct{}) bool {
	panic("not implemented")
}

func wdCollectWithLanguageServer(root string, targets []wdLspTarget, skipped []string, command wdLspCommand, config wdLsp, signal <-chan struct{}) wdLspResult {
	panic("not implemented")
}

func collectWatchdogLspDiagnostics(req watchdogLspRequest) wdLspResult { panic("not implemented") }
