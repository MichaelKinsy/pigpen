package lsp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/lsp/client.ts

// LspResponseError represents a JSON-RPC error returned by an LSP server.
// Message includes the numeric code so substring checks match `-32601`.
type LspResponseError struct {
	Code    int
	HasCode bool
	Data    any
	Msg     string
}

func (e *LspResponseError) Error() string {
	if e.HasCode {
		return fmt.Sprintf("LSP error %d: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("LSP error: %s", e.Msg)
}

// LspRequestTimeoutError signals a request that exceeded its deadline.
type LspRequestTimeoutError struct {
	Method    string
	TimeoutMs int64
}

func (e *LspRequestTimeoutError) Error() string {
	return fmt.Sprintf("LSP request %s timed out after %dms", e.Method, e.TimeoutMs)
}

const (
	defaultRequestTimeout  = 30 * time.Second
	warmupTimeout          = 5 * time.Second
	shutdownTimeout        = 5 * time.Second
	exitTimeout            = 1 * time.Second
	sessionEndShutdownWait = 1 * time.Second
	sessionEndExitWait     = 250 * time.Millisecond
	projectLoadTimeout     = 15 * time.Second
	projectLoadQuietWindow = 500 * time.Millisecond
	initFailureBackoff     = 3 * time.Minute
	openFileIdleDuration   = 5 * time.Minute
	idleCheckInterval      = 60 * time.Second
	watchedFilesTimeout    = 2 * time.Second
	stdinWriteFallbackWait = 5 * time.Second
	stderrRingLimit        = 16 * 1024
	// outputDrainWait bounds the wait, once the server has exited, for what it wrote last to be read: a process
	// the server started may keep the pipes open for as long as it lives.
	outputDrainWait = 200 * time.Millisecond
)

type openFileState struct {
	version    int
	languageID string
	syncedHash string
	diskStamp  string
	diskHash   string
	lastUsedAt time.Time
	reopenedAt *time.Time
}

type documentBuildStatus struct {
	busySeq int
	idleSeq int
}

type pendingRequest struct {
	ch     chan rpcResponse
	method string
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

// Client manages one live stdio JSON-RPC connection to a language server.
//
// Lock ordering discipline (to prevent deadlocks):
// NEVER acquire c.mu while holding m.mu, and NEVER acquire m.mu while holding c.mu.
// Snapshot pointers/maps under one lock, release it, then acquire the other.
type editContextEntry struct {
	token uint64
	ctx   sdk.Context
}

type Client struct {
	key     string
	cwd     string
	config  ServerConfig
	trusted bool

	// idleTimeoutMs caches the idle timeout from the trust-scoped config that
	// created this client; idle checks read it without nesting c.mu under m.mu
	// or reloading trusted config for an untrusted workspace.
	idleTimeoutMs atomic.Int64

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	// readDone closes when readLoop has read the server's stdout to its end, stderrDone when captureStderr has
	// read its stderr to its end.
	readDone   chan struct{}
	stderrDone chan struct{}

	writeMu sync.Mutex

	mu                       sync.Mutex
	requestID                int64
	status                   string // "connecting" | "ready" | "error"
	serverCapabilities       map[string]any
	rawCapabilities          json.RawMessage
	dynamicCapabilities      map[string]string // registrationID -> method
	diagnostics              map[string]PublishedDiagnostics
	publishedDiagnostics     map[string]PublishedDiagnostics
	pulledDiagnostics        map[string]PublishedDiagnostics
	diagnosticsVersion       int
	publishesDocumentVersion bool
	buildStatus              map[string]*documentBuildStatus
	buildStatusSeq           int
	openFiles                map[string]*openFileState
	idleClosedFiles          map[string]int
	pendingRequests          map[int64]*pendingRequest
	activeProgressTokens     map[string]struct{}
	addedWorkspaceFolders    map[string]struct{}
	lastActivity             time.Time
	startedAt                time.Time
	cargoManifests           string
	hasCargoManifests        bool
	// editCtxStack records live sdk.Context values of in-flight calls that may
	// trigger server-initiated workspace/applyEdit (e.g. executeCommand, raw
	// request), so handleServerRequest always gates edits with a real context.
	editCtxSeq   uint64
	editCtxStack []editContextEntry
	// projectLoadedCh closes when initial project loading settles or times out.
	projectLoadedCh   chan struct{}
	projectLoadedOnce sync.Once
	projectQuietTimer *time.Timer
	projectMaxTimer   *time.Timer

	// diagNotify broadcasts whenever diagnosticsVersion advances.
	diagNotify chan struct{}

	// exitedCh closes when the child process exits.
	exitedCh chan struct{}
	exitCode int
	hasExit  bool

	stderrMu  sync.Mutex
	stderrBuf bytes.Buffer
}

// =============================================================================
// Client Registry & Per-Session Reference Counting
// =============================================================================

type initFailureEntry struct {
	at      time.Time
	message string
}

type pendingClientEntry struct {
	done     chan struct{}
	cancel   context.CancelFunc
	canceled atomic.Bool
	client   *Client
	err      error
	cwd      string
	config   ServerConfig
}

type ClientManager struct {
	mu                sync.Mutex
	clients           map[string]*Client
	clientLocks       map[string]*pendingClientEntry
	initFailures      map[string]initFailureEntry
	pendingDiskWrites map[string]int
	// sessionRefs tracks which session IDs hold a reference to each client key
	// so lifecycle.End(sessionID) releases only that session's holds and stops
	// a server only when its last holder leaves.
	sessionRefs map[string]map[string]struct{} // clientKey -> set of sessionIDs
	fileOpMu    sync.Mutex
	fileOpLocks map[string]*sync.Mutex
	idleTimeout time.Duration
	idleStopCh  chan struct{}
}

func newClientManager() *ClientManager {
	return &ClientManager{
		clients:           make(map[string]*Client),
		clientLocks:       make(map[string]*pendingClientEntry),
		initFailures:      make(map[string]initFailureEntry),
		pendingDiskWrites: make(map[string]int),
		sessionRefs:       make(map[string]map[string]struct{}),
		fileOpLocks:       make(map[string]*sync.Mutex),
	}
}

var defaultManager = newClientManager()

// AcquireForSession records that sessionID uses client key. Empty sessionID is
// ignored (never mapped to a shared default session).
func (m *ClientManager) AcquireForSession(sessionID, clientKey string) {
	if sessionID == "" || clientKey == "" {
		return
	}
	m.mu.Lock()
	holders, ok := m.sessionRefs[clientKey]
	if !ok {
		holders = make(map[string]struct{})
		m.sessionRefs[clientKey] = holders
	}
	holders[sessionID] = struct{}{}
	m.mu.Unlock()
}

// ReleaseSession releases all server references held by sessionID, shutting down
// only servers whose last holder just left. Safe to call twice or for unknown IDs.
func (m *ClientManager) ReleaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	var toStop []*Client
	var pendingToWait []*pendingClientEntry
	for key, holders := range m.sessionRefs {
		if _, held := holders[sessionID]; !held {
			continue
		}
		delete(holders, sessionID)
		if len(holders) == 0 {
			delete(m.sessionRefs, key)
			if c, ok := m.clients[key]; ok {
				delete(m.clients, key)
				toStop = append(toStop, c)
			}
			if p, ok := m.clientLocks[key]; ok {
				pendingToWait = append(pendingToWait, p)
				p.canceled.Store(true)
				if p.cancel != nil {
					p.cancel()
				}
			}
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, c := range toStop {
		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			m.shutdownClientWithBudget(client, sessionEndShutdownWait, sessionEndExitWait)
		}(c)
	}
	for _, p := range pendingToWait {
		wg.Add(1)
		go func(pending *pendingClientEntry) {
			defer wg.Done()
			<-pending.done
			if pending.client != nil {
				// Only shut down if no other session acquired it meanwhile.
				m.mu.Lock()
				stillHeld := len(m.sessionRefs[pending.client.key]) > 0
				if !stillHeld && m.clients[pending.client.key] == pending.client {
					delete(m.clients, pending.client.key)
				}
				m.mu.Unlock()
				if !stillHeld {
					m.shutdownClientWithBudget(pending.client, sessionEndShutdownWait, sessionEndExitWait)
				}
			}
		}(p)
	}
	wg.Wait()
}

// stableClientKey computes omp's identity-aware client key so configs with
// different args/initOptions/settings/languageId never share a process.
// upstream: packages/coding-agent/src/lsp/client.ts:clientKey
func stableClientKey(cfg ServerConfig, cwd string) string {
	spawnCmd := cfg.ResolvedCommand
	if spawnCmd == "" {
		spawnCmd = cfg.Command
	}
	args := cfg.Args
	if args == nil {
		args = []string{}
	}
	payload := []any{args, cfg.InitOptions, cfg.Settings, cfg.LanguageID}
	encoded, _ := json.Marshal(payload)
	return fmt.Sprintf("%s:%s:%s", spawnCmd, filepath.Clean(cwd), string(encoded))
}

func commandBasename(cmd string) string {
	idx := strings.LastIndexAny(cmd, `/\`)
	if idx == -1 {
		return cmd
	}
	return cmd[idx+1:]
}

func isRustAnalyzerConfig(cfg ServerConfig) bool {
	return commandBasename(cfg.Command) == "rust-analyzer" ||
		commandBasename(cfg.Command) == "rust-analyzer.exe" ||
		commandBasename(cfg.ResolvedCommand) == "rust-analyzer" ||
		commandBasename(cfg.ResolvedCommand) == "rust-analyzer.exe"
}

var clangdBasenameRe = regexp.MustCompile(`(?i)^clangd(?:-\d+)?(?:\.exe)?$`)

func isClangdConfig(cfg ServerConfig) bool {
	return clangdBasenameRe.MatchString(commandBasename(cfg.Command)) ||
		(cfg.ResolvedCommand != "" && clangdBasenameRe.MatchString(commandBasename(cfg.ResolvedCommand)))
}

func clientCapabilities(watchFiles bool) map[string]any {
	symbolKinds := make([]int, 26)
	for i := range 26 {
		symbolKinds[i] = i + 1
	}
	workspace := map[string]any{
		"applyEdit": true,
		"workspaceEdit": map[string]any{
			"documentChanges":    true,
			"resourceOperations": []string{"create", "rename", "delete"},
			"failureHandling":    "abort",
		},
		"configuration":    true,
		"workspaceFolders": true,
		"symbol": map[string]any{
			"dynamicRegistration": false,
			"symbolKind":          map[string]any{"valueSet": symbolKinds},
		},
		"fileOperations": map[string]any{
			"dynamicRegistration": false,
			"willCreate":          false,
			"didCreate":           false,
			"willRename":          true,
			"didRename":           true,
			"willDelete":          false,
			"didDelete":           false,
		},
	}
	if watchFiles {
		workspace["didChangeWatchedFiles"] = map[string]any{
			"dynamicRegistration":    true,
			"relativePatternSupport": true,
		}
	}
	return map[string]any{
		"textDocument": map[string]any{
			"synchronization": map[string]any{
				"didSave":             true,
				"dynamicRegistration": false,
				"willSave":            false,
				"willSaveWaitUntil":   false,
			},
			"hover": map[string]any{
				"contentFormat":       []string{"markdown", "plaintext"},
				"dynamicRegistration": false,
			},
			"definition": map[string]any{
				"dynamicRegistration": false,
				"linkSupport":         true,
			},
			"typeDefinition": map[string]any{
				"dynamicRegistration": false,
				"linkSupport":         true,
			},
			"implementation": map[string]any{
				"dynamicRegistration": false,
				"linkSupport":         true,
			},
			"references": map[string]any{
				"dynamicRegistration": false,
			},
			"documentSymbol": map[string]any{
				"dynamicRegistration":               false,
				"hierarchicalDocumentSymbolSupport": true,
				"symbolKind":                        map[string]any{"valueSet": symbolKinds},
			},
			"rename": map[string]any{
				"dynamicRegistration": false,
				"prepareSupport":      true,
			},
			"codeAction": map[string]any{
				"dynamicRegistration": false,
				"codeActionLiteralSupport": map[string]any{
					"codeActionKind": map[string]any{
						"valueSet": []string{
							"quickfix", "refactor", "refactor.extract", "refactor.inline",
							"refactor.rewrite", "source", "source.organizeImports", "source.fixAll",
						},
					},
				},
				"resolveSupport": map[string]any{
					"properties": []string{"edit"},
				},
			},
			"formatting": map[string]any{
				"dynamicRegistration": false,
			},
			"rangeFormatting": map[string]any{
				"dynamicRegistration": false,
			},
			"publishDiagnostics": map[string]any{
				"relatedInformation":     true,
				"versionSupport":         true,
				"tagSupport":             map[string]any{"valueSet": []int{1, 2}},
				"codeDescriptionSupport": true,
				"dataSupport":            true,
			},
			"diagnostic": map[string]any{
				"dynamicRegistration": true,
			},
		},
		"window": map[string]any{
			"workDoneProgress": true,
		},
		"workspace": workspace,
	}
}

func (m *ClientManager) fileLock(clientKey, uri string) *sync.Mutex {
	k := clientKey + ":" + canonicalURIKey(uri)
	m.fileOpMu.Lock()
	defer m.fileOpMu.Unlock()
	mu, ok := m.fileOpLocks[k]
	if !ok {
		mu = &sync.Mutex{}
		m.fileOpLocks[k] = mu
	}
	return mu
}

func (m *ClientManager) beginPendingDiskWrite(filePath string) {
	key := canonicalURIKey(fileToUri(filePath))
	m.mu.Lock()
	m.pendingDiskWrites[key]++
	m.mu.Unlock()
}

func (m *ClientManager) endPendingDiskWrite(filePath string) {
	key := canonicalURIKey(fileToUri(filePath))
	m.mu.Lock()
	if c := m.pendingDiskWrites[key]; c > 1 {
		m.pendingDiskWrites[key] = c - 1
	} else {
		delete(m.pendingDiskWrites, key)
	}
	m.mu.Unlock()
}

func (m *ClientManager) isPendingDiskWrite(uri string) bool {
	key := canonicalURIKey(uri)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pendingDiskWrites[key] > 0
}

func (m *ClientManager) snapshotPendingDiskWrites() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pendingDiskWrites) == 0 {
		return nil
	}
	out := make(map[string]bool, len(m.pendingDiskWrites))
	for k, n := range m.pendingDiskWrites {
		if n > 0 {
			out[k] = true
		}
	}
	return out
}

func (m *ClientManager) ClearInitFailure(cfg ServerConfig, cwd string) {
	key := stableClientKey(cfg, cwd)
	m.mu.Lock()
	delete(m.initFailures, key)
	m.mu.Unlock()
}

// GetOrCreateClient returns an initialized client for (cfg, cwd), starting the
// language server lazily on first use and deduplicating concurrent starts.
// Never holds m.mu while acquiring c.mu.
// Callers with a session ID should prefer GetOrCreateClientForSession; this
// wrapper uses an ephemeral synthetic owner tied to the manager's trusted flag.
// upstream: packages/coding-agent/src/lsp/client.ts:getOrCreateClient
func (m *ClientManager) GetOrCreateClient(ctx context.Context, cfg ServerConfig, cwd string, initTimeout time.Duration) (*Client, error) {
	return m.getOrCreateClient(ctx, "", true, 0, cfg, cwd, initTimeout)
}

// GetOrCreateClientForSession is GetOrCreateClient with session reference
// tracking and trust-scoped idle configuration.
func (m *ClientManager) GetOrCreateClientForSession(ctx context.Context, sessionID string, cfg ServerConfig, cwd string, initTimeout time.Duration) (*Client, error) {
	return m.GetOrCreateClientForSessionWithTrust(ctx, sessionID, true, 0, cfg, cwd, initTimeout)
}

// GetOrCreateClientForSessionWithTrust starts or reuses a language server for
// (cfg, cwd) owned by sessionID, recording whether the calling session trusted
// cwd and the idle timeout from its trust-scoped config so background paths
// never reload a trusted config for an untrusted project.
func (m *ClientManager) GetOrCreateClientForSessionWithTrust(
	ctx context.Context,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg ServerConfig,
	cwd string,
	initTimeout time.Duration,
) (*Client, error) {
	return m.getOrCreateClient(ctx, sessionID, trusted, idleTimeoutMs, cfg, cwd, initTimeout)
}

// ephemeralSessionID returns a unique owner token when a caller has no
// resolvable host session ID so the started client is still tracked, never
// shared as an ownerless orphan, and released when the caller returns.
var ephemeralOwnerSeq atomic.Uint64

func newEphemeralSessionID() string {
	return fmt.Sprintf("__ephemeral__:%d", ephemeralOwnerSeq.Add(1))
}

// acquireSessionForCall returns the session owner to use and an optional
// cleanup func that releases an ephemeral owner when sessionID was empty.
func acquireSessionForCall(m *ClientManager, sessionID string) (ownerID string, release func()) {
	if sessionID != "" {
		return sessionID, func() {}
	}
	id := newEphemeralSessionID()
	return id, func() {
		m.ReleaseSession(id)
	}
}

func (m *ClientManager) getOrCreateClient(
	ctx context.Context,
	sessionID string,
	trusted bool,
	idleTimeoutMs int,
	cfg ServerConfig,
	cwd string,
	initTimeout time.Duration,
) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := stableClientKey(cfg, cwd)

	m.mu.Lock()
	if existing, ok := m.clients[key]; ok {
		if sessionID != "" {
			holders, ok := m.sessionRefs[key]
			if !ok {
				holders = make(map[string]struct{})
				m.sessionRefs[key] = holders
			}
			holders[sessionID] = struct{}{}
		}
		m.mu.Unlock()
		existing.touchActivity()
		if idleTimeoutMs > 0 {
			existing.idleTimeoutMs.CompareAndSwap(0, int64(idleTimeoutMs))
		}
		return existing, nil
	}
	if pending, ok := m.clientLocks[key]; ok {
		if sessionID != "" {
			holders, ok := m.sessionRefs[key]
			if !ok {
				holders = make(map[string]struct{})
				m.sessionRefs[key] = holders
			}
			holders[sessionID] = struct{}{}
		}
		m.mu.Unlock()
		select {
		case <-pending.done:
			return pending.client, pending.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail, ok := m.initFailures[key]; ok {
		if time.Since(fail.at) < initFailureBackoff {
			m.mu.Unlock()
			return nil, fmt.Errorf("LSP server %s failed to initialize recently: %s", cfg.Command, fail.message)
		}
		delete(m.initFailures, key)
	}

	initCtx, initCancel := context.WithCancel(ctx)
	pending := &pendingClientEntry{
		done:   make(chan struct{}),
		cancel: initCancel,
		cwd:    cwd,
		config: cfg,
	}
	m.clientLocks[key] = pending
	if sessionID != "" {
		holders, ok := m.sessionRefs[key]
		if !ok {
			holders = make(map[string]struct{})
			m.sessionRefs[key] = holders
		}
		holders[sessionID] = struct{}{}
	}
	m.mu.Unlock()

	client, err := m.startClient(initCtx, key, cfg, cwd, trusted, idleTimeoutMs, initTimeout)
	initAborted := initCtx.Err() != nil || pending.canceled.Load()
	initCancel()

	m.mu.Lock()
	pending.client = client
	pending.err = err
	if m.clientLocks[key] == pending {
		delete(m.clientLocks, key)
	}
	if err == nil && client != nil {
		m.clients[key] = client
		delete(m.initFailures, key)
		m.maybeStartIdleCheckerLocked()
	} else if err != nil {
		// Release the session reference added for this failed init attempt so
		// failed starts do not leave stale entries in sessionRefs.
		if sessionID != "" {
			if holders, ok := m.sessionRefs[key]; ok {
				delete(holders, sessionID)
				if len(holders) == 0 {
					delete(m.sessionRefs, key)
				}
			}
		}
		// Treat both caller cancellation (ctx.Err) and manager-initiated
		// cancellation (ReleaseSession / ShutdownStaleClients / ShutdownAll
		// via pending.cancel -> initCtx.Err) as transient aborts so healthy
		// servers are never negative-cached for 3 minutes.
		isAbort := ctx.Err() != nil || initAborted || pending.canceled.Load()
		isShortTimeout := initTimeout > 0 && strings.Contains(err.Error(), "timed out")
		if !isAbort && !isShortTimeout {
			m.initFailures[key] = initFailureEntry{at: time.Now(), message: err.Error()}
		}
	}
	close(pending.done)
	m.mu.Unlock()
	return client, err
}

func (m *ClientManager) GetActiveOrPendingClient(ctx context.Context, cfg ServerConfig, cwd string) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := stableClientKey(cfg, cwd)
	m.mu.Lock()
	existing := m.clients[key]
	pending := m.clientLocks[key]
	m.mu.Unlock()

	if existing != nil {
		existing.touchActivity()
		return existing, nil
	}
	if pending == nil {
		return nil, nil
	}
	select {
	case <-pending.done:
		if pending.err != nil {
			return nil, nil
		}
		return pending.client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *ClientManager) startClient(ctx context.Context, key string, cfg ServerConfig, cwd string, trusted bool, idleTimeoutMs int, initTimeout time.Duration) (*Client, error) {
	bin := cfg.ResolvedCommand
	if bin == "" {
		bin = cfg.Command
	}
	cmd := exec.Command(bin, cfg.Args...)
	cmd.Dir = cwd
	configureProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create LSP stdin pipe: %w", err)
	}
	// The server's output is read from pipes this client owns. cmd.Wait closes the pipes that StdoutPipe and
	// StderrPipe make as soon as the process exits, whatever is still unread in them: the reader then failed
	// with "file already closed" where the stream ended, and a server's last words on stderr could be lost.
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create LSP stdout pipe: %w", err)
	}
	stderr, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("create LSP stderr pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW

	err = cmd.Start()
	// The server holds the write ends now; this process must not, or the read ends never see the end.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start LSP server %s: %w", cfg.Command, err)
	}

	now := time.Now()
	c := &Client{
		key:                   key,
		cwd:                   cwd,
		config:                cfg,
		stderr:                stderr,
		trusted:               trusted,
		cmd:                   cmd,
		stdin:                 stdin,
		stdout:                stdout,
		status:                "connecting",
		dynamicCapabilities:   make(map[string]string),
		diagnostics:           make(map[string]PublishedDiagnostics),
		publishedDiagnostics:  make(map[string]PublishedDiagnostics),
		pulledDiagnostics:     make(map[string]PublishedDiagnostics),
		openFiles:             make(map[string]*openFileState),
		idleClosedFiles:       make(map[string]int),
		pendingRequests:       make(map[int64]*pendingRequest),
		activeProgressTokens:  make(map[string]struct{}),
		addedWorkspaceFolders: make(map[string]struct{}),
		lastActivity:          now,
		startedAt:             now,
		projectLoadedCh:       make(chan struct{}),
		diagNotify:            make(chan struct{}),
		exitedCh:              make(chan struct{}),
		readDone:              make(chan struct{}),
		stderrDone:            make(chan struct{}),
	}
	c.idleTimeoutMs.Store(int64(idleTimeoutMs))
	c.projectMaxTimer = time.AfterFunc(projectLoadTimeout, c.resolveProjectLoaded)

	go c.captureStderr()
	go c.readLoop(m)
	go c.waitProcess(m)

	initOpts := make(map[string]any)
	if isClangdConfig(cfg) {
		initOpts["clangdFileStatus"] = true
	}
	for k, v := range cfg.InitOptions {
		initOpts[k] = v
	}

	watchFiles := cfg.WatchFiles == nil || *cfg.WatchFiles
	initParams := map[string]any{
		"processId":             os.Getpid(),
		"rootUri":               fileToUri(cwd),
		"rootPath":              cwd,
		"capabilities":          clientCapabilities(watchFiles),
		"initializationOptions": initOpts,
		"workspaceFolders":      c.currentWorkspaceFolders(),
	}

	rawInit, err := c.SendRequest(ctx, "initialize", initParams, initTimeout)
	if err != nil {
		c.failAndKill(m)
		return nil, err
	}
	var initResult struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if len(rawInit) == 0 || string(rawInit) == "null" {
		c.failAndKill(m)
		return nil, errors.New("Failed to initialize LSP: no response")
	}
	if err := json.Unmarshal(rawInit, &initResult); err != nil {
		c.failAndKill(m)
		return nil, fmt.Errorf("Failed to initialize LSP: %w", err)
	}
	var capsMap map[string]any
	if len(initResult.Capabilities) > 0 && string(initResult.Capabilities) != "null" {
		_ = json.Unmarshal(initResult.Capabilities, &capsMap)
	}

	c.mu.Lock()
	c.serverCapabilities = capsMap
	c.rawCapabilities = append(json.RawMessage(nil), initResult.Capabilities...)
	c.mu.Unlock()

	if err := c.SendNotification(ctx, "initialized", map[string]any{}); err != nil {
		c.failAndKill(m)
		return nil, err
	}
	settings := cfg.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	if err := c.SendNotification(ctx, "workspace/didChangeConfiguration", map[string]any{"settings": settings}); err != nil {
		c.failAndKill(m)
		return nil, err
	}

	c.mu.Lock()
	c.status = "ready"
	c.mu.Unlock()

	if isRustAnalyzerConfig(cfg) {
		go func() {
			manifests := listCargoManifests(cwd)
			c.mu.Lock()
			c.cargoManifests = manifests
			c.hasCargoManifests = true
			c.mu.Unlock()
		}()
	}

	return c, nil
}

func (c *Client) failAndKill(m *ClientManager) {
	c.mu.Lock()
	c.status = "error"
	c.mu.Unlock()
	m.mu.Lock()
	if m.clients[c.key] == c {
		delete(m.clients, c.key)
	}
	m.mu.Unlock()
	c.resolveProjectLoaded()
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		killProcessTree(c.cmd.Process, c.exitedCh, 200*time.Millisecond)
	}
}

func (c *Client) captureStderr() {
	defer close(c.stderrDone)
	r := c.stderr
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			if c.stderrBuf.Len()+n > stderrRingLimit {
				excess := c.stderrBuf.Len() + n - stderrRingLimit
				if excess >= c.stderrBuf.Len() {
					c.stderrBuf.Reset()
				} else {
					kept := append([]byte(nil), c.stderrBuf.Bytes()[excess:]...)
					c.stderrBuf.Reset()
					c.stderrBuf.Write(kept)
				}
			}
			c.stderrBuf.Write(buf[:n])
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

var infoLogLineRe = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2} (?:INF|DBG|VRB)\]`)

func (c *Client) peekStderr() string {
	c.stderrMu.Lock()
	raw := strings.TrimSpace(c.stderrBuf.String())
	c.stderrMu.Unlock()
	if raw == "" {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		if !infoLogLineRe.MatchString(line) {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func (c *Client) waitProcess(m *ClientManager) {
	err := c.cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}

	// The server's last output may still be in the pipes: an answer it sent before it exited, the reason it
	// died. It is read before the exit is reported, and then the pipes are closed, so that nothing more is read
	// from a process the server left behind.
	deadline := time.After(outputDrainWait)
drain:
	for _, done := range [...]chan struct{}{c.stderrDone, c.readDone} {
		select {
		case <-done:
		case <-deadline:
			break drain
		}
	}
	_ = c.stderr.Close()
	_ = c.stdout.Close()

	c.mu.Lock()
	c.exitCode = code
	c.hasExit = true
	pending := c.pendingRequests
	c.pendingRequests = make(map[int64]*pendingRequest)
	c.mu.Unlock()

	close(c.exitedCh)
	c.resolveProjectLoaded()

	m.mu.Lock()
	if m.clients[c.key] == c {
		delete(m.clients, c.key)
	}
	m.mu.Unlock()

	if len(pending) > 0 {
		stderr := c.peekStderr()
		var exitMsg error
		if stderr != "" {
			exitMsg = fmt.Errorf("LSP server exited (code %d): %s", code, stderr)
		} else {
			exitMsg = fmt.Errorf("LSP server exited unexpectedly (code %d)", code)
		}
		for _, p := range pending {
			select {
			case p.ch <- rpcResponse{err: exitMsg}:
			default:
			}
		}
	}
}

func (c *Client) readLoop(m *ClientManager) {
	var framer messageFramer
	buf := make([]byte, 32*1024)
	for {
		n, err := c.stdout.Read(buf)
		if n > 0 {
			if pushErr := framer.push(buf[:n]); pushErr != nil {
				c.rejectAllPending(fmt.Errorf("LSP connection closed: %w", pushErr))
				break
			}
			msgs, drainErr := framer.drain(nil)
			for _, raw := range msgs {
				c.handleIncomingMessage(m, raw)
			}
			if drainErr != nil {
				c.rejectAllPending(fmt.Errorf("LSP connection closed: %w", drainErr))
				break
			}
		}
		if err != nil {
			// os.ErrClosed: waitProcess closed the pipe after the server exited.
			if err != io.EOF && !errors.Is(err, os.ErrClosed) {
				c.rejectAllPending(fmt.Errorf("LSP connection closed: %w", err))
			}
			break
		}
	}
	close(c.readDone)

	select {
	case <-c.exitedCh:
		return
	case <-time.After(100 * time.Millisecond):
	}
	c.mu.Lock()
	exited := c.hasExit
	if !exited {
		c.status = "error"
	}
	c.mu.Unlock()
	if !exited {
		m.mu.Lock()
		if m.clients[c.key] == c {
			delete(m.clients, c.key)
		}
		m.mu.Unlock()
		c.rejectAllPending(errors.New("LSP reader stopped; client torn down"))
		c.resolveProjectLoaded()
		if c.cmd != nil && c.cmd.Process != nil {
			killProcessTree(c.cmd.Process, c.exitedCh, 200*time.Millisecond)
		}
	}
}

func (c *Client) rejectAllPending(err error) {
	c.mu.Lock()
	pending := c.pendingRequests
	c.pendingRequests = make(map[int64]*pendingRequest)
	c.mu.Unlock()
	for _, p := range pending {
		select {
		case p.ch <- rpcResponse{err: err}:
		default:
		}
	}
}

func (c *Client) handleIncomingMessage(m *ClientManager, raw []byte) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	// Disambiguate on `method` FIRST: server request IDs live in the server's
	// own ID space and routinely collide with our client request IDs (#3001).
	if msg.Method != "" {
		if len(msg.ID) > 0 && string(msg.ID) != "null" {
			go c.handleServerRequest(m, msg)
		} else {
			c.handleServerNotification(msg)
		}
		return
	}
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return
	}
	var id int64
	if err := json.Unmarshal(msg.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	p, ok := c.pendingRequests[id]
	if ok {
		delete(c.pendingRequests, id)
		c.lastActivity = time.Now()
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if msg.Error != nil {
		p.ch <- rpcResponse{
			err: &LspResponseError{
				Code:    msg.Error.Code,
				HasCode: true,
				Data:    msg.Error.Data,
				Msg:     msg.Error.Message,
			},
		}
		return
	}
	p.ch <- rpcResponse{result: msg.Result}
}

func (c *Client) handleServerNotification(msg jsonRPCMessage) {
	switch msg.Method {
	case "textDocument/publishDiagnostics":
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.URI == "" {
			return
		}
		key := canonicalURIKey(params.URI)
		rec := PublishedDiagnostics{
			Diagnostics: params.Diagnostics,
			Version:     params.Version,
		}
		c.mu.Lock()
		c.publishedDiagnostics[key] = rec
		if c.supportsDocumentDiagnosticsLocked() {
			pulled := c.pulledDiagnostics[key].Diagnostics
			combined := mergeDiagnosticLists(pulled, params.Diagnostics)
			c.diagnostics[key] = PublishedDiagnostics{Diagnostics: combined, Version: params.Version}
		} else {
			c.diagnostics[key] = rec
		}
		c.diagnosticsVersion++
		if params.Version != nil {
			c.publishesDocumentVersion = true
		}
		close(c.diagNotify)
		c.diagNotify = make(chan struct{})
		c.mu.Unlock()

	case "$/progress":
		var params struct {
			Token any `json:"token"`
			Value *struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.Value == nil {
			return
		}
		tokKey := fmt.Sprint(params.Token)
		c.mu.Lock()
		switch params.Value.Kind {
		case "begin":
			c.activeProgressTokens[tokKey] = struct{}{}
			if c.projectQuietTimer != nil {
				c.projectQuietTimer.Stop()
				c.projectQuietTimer = nil
			}
		case "end":
			delete(c.activeProgressTokens, tokKey)
			if len(c.activeProgressTokens) == 0 {
				if c.projectQuietTimer != nil {
					c.projectQuietTimer.Stop()
				}
				c.projectQuietTimer = time.AfterFunc(projectLoadQuietWindow, func() {
					c.mu.Lock()
					empty := len(c.activeProgressTokens) == 0
					c.mu.Unlock()
					if empty {
						c.resolveProjectLoaded()
					}
				})
			}
		}
		c.mu.Unlock()

	case "textDocument/clangd.fileStatus":
		var params struct {
			URI   string `json:"uri"`
			State string `json:"state"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil || params.URI == "" {
			return
		}
		key := canonicalURIKey(params.URI)
		c.mu.Lock()
		if c.buildStatus == nil {
			c.buildStatus = make(map[string]*documentBuildStatus)
		}
		st, ok := c.buildStatus[key]
		if !ok {
			st = &documentBuildStatus{}
			c.buildStatus[key] = st
		}
		c.buildStatusSeq++
		if params.State == "idle" {
			st.idleSeq = c.buildStatusSeq
		} else {
			st.busySeq = c.buildStatusSeq
		}
		c.mu.Unlock()
	}
}

// pushEditContext records sdkCtx as the active calling context for any
// server-initiated workspace/applyEdit sent while a command or raw request is
// in flight on c. Call the returned pop function via defer.
func (c *Client) pushEditContext(sdkCtx sdk.Context) func() {
	c.mu.Lock()
	c.editCtxSeq++
	tok := c.editCtxSeq
	c.editCtxStack = append(c.editCtxStack, editContextEntry{token: tok, ctx: sdkCtx})
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			for i := len(c.editCtxStack) - 1; i >= 0; i-- {
				if c.editCtxStack[i].token == tok {
					c.editCtxStack = append(c.editCtxStack[:i], c.editCtxStack[i+1:]...)
					break
				}
			}
			c.mu.Unlock()
		})
	}
}

func (c *Client) currentEditContext() (sdk.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.editCtxStack) == 0 {
		var zero sdk.Context
		return zero, false
	}
	return c.editCtxStack[len(c.editCtxStack)-1].ctx, true
}

func (c *Client) handleServerRequest(m *ClientManager, msg jsonRPCMessage) {
	defer func() {
		if r := recover(); r != nil {
			if msg.Method == "workspace/applyEdit" {
				c.sendResponse(msg.ID, map[string]any{
					"applied":       false,
					"failureReason": fmt.Sprintf("internal error handling workspace/applyEdit: %v", r),
				}, nil)
				return
			}
			c.sendResponse(msg.ID, nil, &jsonRPCError{
				Code:    -32603,
				Message: fmt.Sprintf("Internal error handling %s: %v", msg.Method, r),
			})
		}
	}()
	switch msg.Method {
	case "workspace/configuration":
		var params struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		result := make([]any, len(params.Items))
		for i, item := range params.Items {
			if c.config.Settings != nil {
				if v, ok := c.config.Settings[item.Section]; ok {
					result[i] = v
					continue
				}
			}
			result[i] = nil
		}
		c.sendResponse(msg.ID, result, nil)

	case "workspace/workspaceFolders":
		c.sendResponse(msg.ID, c.currentWorkspaceFolders(), nil)

	case "workspace/applyEdit":
		var params struct {
			Edit *WorkspaceEdit `json:"edit"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		if params.Edit == nil {
			c.sendResponse(msg.ID, map[string]any{"applied": false, "failureReason": "No edit provided"}, nil)
			return
		}
		editSDKCtx, ok := c.currentEditContext()
		if !ok || !hasValidGateContext(editSDKCtx) {
			c.sendResponse(msg.ID, map[string]any{
				"applied":       false,
				"failureReason": "refused server-initiated workspace/applyEdit: no active session context to gate mutation",
			}, nil)
			return
		}
		if _, err := m.ApplyWorkspaceEditWithLsp(context.Background(), editSDKCtx, params.Edit, c.cwd); err != nil {
			c.sendResponse(msg.ID, map[string]any{"applied": false, "failureReason": err.Error()}, nil)
			return
		}
		c.sendResponse(msg.ID, map[string]any{"applied": true}, nil)
	case "client/registerCapability":
		var params struct {
			Registrations []struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			} `json:"registrations"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		c.mu.Lock()
		for _, reg := range params.Registrations {
			if reg.ID != "" && reg.Method != "" {
				c.dynamicCapabilities[reg.ID] = reg.Method
			}
		}
		c.mu.Unlock()
		c.sendResponse(msg.ID, nil, nil)

	case "client/unregisterCapability":
		var params struct {
			Unregisterations []struct {
				ID string `json:"id"`
			} `json:"unregisterations"`
			Unregistrations []struct {
				ID string `json:"id"`
			} `json:"unregistrations"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		c.mu.Lock()
		for _, reg := range params.Unregisterations {
			delete(c.dynamicCapabilities, reg.ID)
		}
		for _, reg := range params.Unregistrations {
			delete(c.dynamicCapabilities, reg.ID)
		}
		c.mu.Unlock()
		c.sendResponse(msg.ID, nil, nil)

	case "window/workDoneProgress/create",
		"window/showMessageRequest",
		"workspace/semanticTokens/refresh",
		"workspace/inlayHint/refresh",
		"workspace/codeLens/refresh",
		"workspace/codeAction/refresh",
		"workspace/inlineValue/refresh",
		"workspace/foldingRange/refresh",
		"workspace/diagnostic/refresh":
		c.sendResponse(msg.ID, nil, nil)

	case "window/showDocument":
		c.sendResponse(msg.ID, map[string]any{"success": false}, nil)

	default:
		c.sendResponse(msg.ID, nil, &jsonRPCError{
			Code:    -32601,
			Message: "Method not found: " + msg.Method,
		})
	}
}

// writeJSON encodes and writes one JSON-RPC message bounded by ctx (or a
// fallback timer when ctx has no deadline). If the server stops draining stdin
// and the write exceeds ctx, the wedged process tree is killed to unblock the
// pipe and tear the client down cleanly (upstream: lsp/client.ts:561-659).
func (c *Client) writeJSON(ctx context.Context, msg any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	frame, err := encodeMessageFrame(msg)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_, wErr := c.stdin.Write(frame)
		done <- wErr
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && ctx.Done() == nil {
		timer = time.NewTimer(stdinWriteFallbackWait)
		defer timer.Stop()
		timerCh = timer.C
	}

	select {
	case wErr := <-done:
		return wErr
	case <-ctx.Done():
		c.teardownWedged()
		return ctx.Err()
	case <-timerCh:
		c.teardownWedged()
		return errors.New("LSP stdin write timed out; server wedged")
	}
}

func (c *Client) teardownWedged() {
	c.mu.Lock()
	c.status = "error"
	c.mu.Unlock()
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		killProcessTree(c.cmd.Process, c.exitedCh, 100*time.Millisecond)
	}
}

func (c *Client) sendResponse(id json.RawMessage, result any, rpcErr *jsonRPCError) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
	}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	ctx, cancel := context.WithTimeout(context.Background(), stdinWriteFallbackWait)
	defer cancel()
	_ = c.writeJSON(ctx, resp)
}

func (c *Client) touchActivity() {
	c.mu.Lock()
	c.lastActivity = time.Now()
	c.mu.Unlock()
}

func (c *Client) resolveProjectLoaded() {
	c.projectLoadedOnce.Do(func() {
		if c.projectMaxTimer != nil {
			c.projectMaxTimer.Stop()
		}
		close(c.projectLoadedCh)
	})
}

func (c *Client) currentWorkspaceFolders() []map[string]string {
	c.mu.Lock()
	extra := make([]string, 0, len(c.addedWorkspaceFolders))
	for dir := range c.addedWorkspaceFolders {
		extra = append(extra, dir)
	}
	c.mu.Unlock()
	sort.Strings(extra)

	folders := make([]map[string]string, 0, 1+len(extra))
	add := func(dir string) {
		name := filepath.Base(dir)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "workspace"
		}
		folders = append(folders, map[string]string{
			"uri":  fileToUri(dir),
			"name": name,
		})
	}
	add(c.cwd)
	for _, d := range extra {
		add(d)
	}
	return folders
}

// formatRawCapabilities returns indented JSON preserving server key order and
// without HTML-escaping `<`, `>`, `&` (matching JSON.stringify(caps, null, 2)).
func (c *Client) formatRawCapabilities() string {
	c.mu.Lock()
	raw := append(json.RawMessage(nil), c.rawCapabilities...)
	c.mu.Unlock()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "  ", "  "); err != nil {
		return string(trimmed)
	}
	return buf.String()
}

func (c *Client) SupportsMethod(capability, method string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.supportsMethodLocked(capability, method)
}

func (c *Client) supportsMethodLocked(capability, method string) bool {
	if c.serverCapabilities != nil {
		if v, ok := c.serverCapabilities[capability]; ok && isTruthyCapability(v) {
			return true
		}
	}
	for _, regMethod := range c.dynamicCapabilities {
		if regMethod == method {
			return true
		}
	}
	return false
}

func (c *Client) supportsDocumentDiagnosticsLocked() bool {
	return c.supportsMethodLocked("diagnosticProvider", "textDocument/diagnostic")
}

func (c *Client) SupportsDocumentDiagnostics() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.supportsDocumentDiagnosticsLocked()
}

func isTruthyCapability(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

func (c *Client) saveIncludesText() bool {
	c.mu.Lock()
	caps := c.serverCapabilities
	c.mu.Unlock()
	if caps == nil {
		return false
	}
	syncObj, ok := caps["textDocumentSync"].(map[string]any)
	if !ok {
		return false
	}
	saveObj, ok := syncObj["save"].(map[string]any)
	if !ok {
		return false
	}
	inc, _ := saveObj["includeText"].(bool)
	return inc
}

// SendRequest sends an LSP request and waits for the response, honoring ctx and
// timeout (falling back to 30s when neither ctx deadline nor timeout is set).
// upstream: packages/coding-agent/src/lsp/client.ts:sendRequest
func (c *Client) SendRequest(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := atomic.AddInt64(&c.requestID, 1)
	p := &pendingRequest{
		ch:     make(chan rpcResponse, 1),
		method: method,
	}

	c.mu.Lock()
	c.lastActivity = time.Now()
	c.pendingRequests[id] = p
	c.mu.Unlock()

	effectiveTimeout := timeout
	if effectiveTimeout <= 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			effectiveTimeout = defaultRequestTimeout
		}
	}

	writeCtx := ctx
	var writeCancel context.CancelFunc
	if effectiveTimeout > 0 {
		writeCtx, writeCancel = context.WithTimeout(ctx, effectiveTimeout)
		defer writeCancel()
	}

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeJSON(writeCtx, req); err != nil {
		c.mu.Lock()
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		if effectiveTimeout > 0 && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, &LspRequestTimeoutError{Method: method, TimeoutMs: effectiveTimeout.Milliseconds()}
		}
		return nil, err
	}

	var timer *time.Timer
	var timerCh <-chan time.Time
	if effectiveTimeout > 0 {
		timer = time.NewTimer(effectiveTimeout)
		defer timer.Stop()
		timerCh = timer.C
	}

	select {
	case resp := <-p.ch:
		return resp.result, resp.err
	case <-timerCh:
		c.mu.Lock()
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		cancelCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = c.SendNotification(cancelCtx, "$/cancelRequest", map[string]any{"id": id})
		cancel()
		return nil, &LspRequestTimeoutError{Method: method, TimeoutMs: effectiveTimeout.Milliseconds()}
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		cancelCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = c.SendNotification(cancelCtx, "$/cancelRequest", map[string]any{"id": id})
		cancel()
		return nil, ctx.Err()
	}
}

// SendNotification writes a one-way LSP notification bounded by ctx.
func (c *Client) SendNotification(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.touchActivity()
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeJSON(ctx, notif)
}

// WaitForProjectLoaded waits for $/progress project indexing to settle, or ctx cancel.
func (c *Client) WaitForProjectLoaded(ctx context.Context) error {
	select {
	case <-c.projectLoadedCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// =============================================================================
// Document Synchronization (didOpen / didChange / didSave / didClose / Reconcile)
// =============================================================================

func documentSignature(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:8])
}

func diskStamp(filePath string) (string, bool) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), true
}

// closeIdleFiles closes open documents unused for >5m. Snapshots pending disk
// writes under m.mu BEFORE acquiring c.mu (strict lock-order safety).
func (m *ClientManager) closeIdleFiles(c *Client) {
	pendingWrites := m.snapshotPendingDiskWrites()
	cutoff := time.Now().Add(-openFileIdleDuration)
	c.mu.Lock()
	var toClose []string
	for key, info := range c.openFiles {
		if info.lastUsedAt.After(cutoff) {
			continue
		}
		if pendingWrites[key] {
			continue
		}
		delete(c.openFiles, key)
		delete(c.diagnostics, key)
		delete(c.pulledDiagnostics, key)
		delete(c.publishedDiagnostics, key)
		c.idleClosedFiles[key] = info.version
		toClose = append(toClose, fileToUri(key))
	}
	c.mu.Unlock()

	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, uri := range toClose {
		_ = c.SendNotification(closeCtx, "textDocument/didClose", map[string]any{
			"textDocument": map[string]string{"uri": uri},
		})
	}
}

func (c *Client) trackOpenedFile(uri, languageID string, version int, content string) {
	key := canonicalURIKey(uri)
	now := time.Now()
	c.mu.Lock()
	_, wasClosed := c.idleClosedFiles[key]
	if wasClosed {
		delete(c.idleClosedFiles, key)
	}
	var reopenedAt *time.Time
	if wasClosed {
		t := now
		reopenedAt = &t
	}
	c.openFiles[key] = &openFileState{
		version:    version,
		languageID: languageID,
		syncedHash: documentSignature(content),
		lastUsedAt: now,
		reopenedAt: reopenedAt,
	}
	c.lastActivity = now
	c.mu.Unlock()
}

func (c *Client) OpenFileVersion(uri string) (int, bool) {
	key := canonicalURIKey(uri)
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.openFiles[key]
	if !ok {
		return 0, false
	}
	return st.version, true
}

func (c *Client) DiagnosticsVersion() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diagnosticsVersion
}

func (c *Client) EnsureFileOpen(ctx context.Context, m *ClientManager, filePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	uri := fileToUri(filePath)
	key := canonicalURIKey(uri)

	c.mu.Lock()
	if info, ok := c.openFiles[key]; ok {
		info.lastUsedAt = time.Now()
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	lock := m.fileLock(c.key, uri)
	lock.Lock()
	defer lock.Unlock()

	c.mu.Lock()
	if info, ok := c.openFiles[key]; ok {
		info.lastUsedAt = time.Now()
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	content, err := readTextFromDisk(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.closeIdleFiles(c)

	langID := c.config.LanguageID
	if langID == "" {
		langID = detectLanguageId(filePath)
	}
	c.mu.Lock()
	version := c.idleClosedFiles[key] + 1
	c.mu.Unlock()

	err = c.SendNotification(ctx, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": langID,
			"version":    version,
			"text":       content,
		},
	})
	if err != nil {
		return err
	}
	c.trackOpenedFile(uri, langID, version, content)
	return nil
}

// ReconcileFileFromDisk ensures the server holds the current disk contents of
// filePath, pushing didChange when an external edit modified an open file.
// Returns true if stale diagnostics were dropped and fresh ones should be awaited.
// upstream: packages/coding-agent/src/lsp/client.ts:reconcileFileFromDisk
func (c *Client) ReconcileFileFromDisk(ctx context.Context, m *ClientManager, filePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	uri := fileToUri(filePath)
	key := canonicalURIKey(uri)

	c.mu.Lock()
	info, open := c.openFiles[key]
	_, reopening := c.idleClosedFiles[key]
	if open {
		info.lastUsedAt = time.Now()
	}
	c.mu.Unlock()

	if !open {
		if err := c.EnsureFileOpen(ctx, m, filePath); err != nil {
			return false, err
		}
		c.mu.Lock()
		_, nowOpen := c.openFiles[key]
		c.mu.Unlock()
		return reopening && nowOpen, nil
	}
	return c.syncOpenFileFromDisk(ctx, m, filePath, uri, true)
}

func (c *Client) syncOpenFileFromDisk(ctx context.Context, m *ClientManager, filePath, uri string, throwUnreadable bool) (bool, error) {
	if m.isPendingDiskWrite(uri) {
		return false, nil
	}
	lock := m.fileLock(c.key, uri)
	lock.Lock()
	defer lock.Unlock()

	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := canonicalURIKey(uri)
	c.mu.Lock()
	info, open := c.openFiles[key]
	var prevStamp, prevDiskHash, prevSyncedHash string
	if open {
		prevStamp = info.diskStamp
		prevDiskHash = info.diskHash
		prevSyncedHash = info.syncedHash
	}
	c.mu.Unlock()

	if !open {
		return false, nil
	}

	stamp, hasStamp := diskStamp(filePath)
	if hasStamp && prevStamp == stamp && prevDiskHash == prevSyncedHash && prevSyncedHash != "" {
		return false, nil
	}

	content, err := readTextFromDisk(filePath)
	if err != nil {
		if !os.IsNotExist(err) && (throwUnreadable || ctx.Err() != nil) {
			return false, err
		}
		if !m.isPendingDiskWrite(uri) {
			_ = c.CloseDocument(ctx, uri)
		}
		return false, nil
	}
	if m.isPendingDiskWrite(uri) {
		return false, nil
	}

	sig := documentSignature(content)
	c.mu.Lock()
	if info, ok := c.openFiles[key]; ok {
		if hasStamp {
			info.diskStamp = stamp
			info.diskHash = sig
		}
		if sig == info.syncedHash {
			c.mu.Unlock()
			return false, nil
		}
		delete(c.diagnostics, key)
		delete(c.pulledDiagnostics, key)
		info.version++
		version := info.version
		c.mu.Unlock()

		if err := c.SendNotification(ctx, "textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]string{{"text": content}},
		}); err != nil {
			return false, err
		}
		c.mu.Lock()
		if cur, ok := c.openFiles[key]; ok {
			cur.syncedHash = sig
		}
		c.lastActivity = time.Now()
		c.mu.Unlock()
		return true, nil
	}
	c.mu.Unlock()
	return false, nil
}

func (c *Client) ReconcileOpenFilesFromDisk(ctx context.Context, m *ClientManager, excludeURI string, throwUnreadable bool) (int, error) {
	excludeKey := ""
	if excludeURI != "" {
		excludeKey = canonicalURIKey(excludeURI)
	}
	c.mu.Lock()
	keys := make([]string, 0, len(c.openFiles))
	for k := range c.openFiles {
		if k != excludeKey {
			keys = append(keys, k)
		}
	}
	c.mu.Unlock()

	count := 0
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		uri := fileToUri(k)
		changed, err := c.syncOpenFileFromDisk(ctx, m, k, uri, throwUnreadable)
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

// SyncContent pushes in-memory file content via didOpen or didChange.
// upstream: packages/coding-agent/src/lsp/client.ts:syncContent
func (c *Client) SyncContent(ctx context.Context, m *ClientManager, filePath, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	uri := fileToUri(filePath)
	key := canonicalURIKey(uri)
	lock := m.fileLock(c.key, uri)
	lock.Lock()
	defer lock.Unlock()

	c.mu.Lock()
	delete(c.diagnostics, key)
	delete(c.pulledDiagnostics, key)
	info, open := c.openFiles[key]
	c.mu.Unlock()

	if !open {
		m.closeIdleFiles(c)
		langID := c.config.LanguageID
		if langID == "" {
			langID = detectLanguageId(filePath)
		}
		c.mu.Lock()
		version := c.idleClosedFiles[key] + 1
		c.mu.Unlock()

		if err := c.SendNotification(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": langID,
				"version":    version,
				"text":       content,
			},
		}); err != nil {
			return err
		}
		c.trackOpenedFile(uri, langID, version, content)
		return nil
	}

	c.mu.Lock()
	info.lastUsedAt = time.Now()
	info.version++
	version := info.version
	c.mu.Unlock()

	if err := c.SendNotification(ctx, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]string{{"text": content}},
	}); err != nil {
		return err
	}

	c.mu.Lock()
	info.syncedHash = documentSignature(content)
	c.lastActivity = time.Now()
	c.mu.Unlock()
	return nil
}

// NotifySaved sends textDocument/didSave for an open file.
// upstream: packages/coding-agent/src/lsp/client.ts:notifySaved
func (c *Client) NotifySaved(ctx context.Context, filePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	uri := fileToUri(filePath)
	key := canonicalURIKey(uri)
	c.mu.Lock()
	info, open := c.openFiles[key]
	if open {
		info.lastUsedAt = time.Now()
	}
	c.mu.Unlock()
	if !open {
		return nil
	}
	params := map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}
	if c.saveIncludesText() {
		if data, err := os.ReadFile(filePath); err == nil {
			params["text"] = string(data)
		}
	}
	if err := c.SendNotification(ctx, "textDocument/didSave", params); err != nil {
		return err
	}
	c.touchActivity()
	return nil
}

// CloseDocument sends textDocument/didClose and removes tracking state.
func (c *Client) CloseDocument(ctx context.Context, uri string) error {
	key := canonicalURIKey(uri)
	err := c.SendNotification(ctx, "textDocument/didClose", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	})
	c.mu.Lock()
	delete(c.openFiles, key)
	delete(c.diagnostics, key)
	delete(c.pulledDiagnostics, key)
	delete(c.publishedDiagnostics, key)
	c.mu.Unlock()
	return err
}

func (c *Client) RefreshFile(ctx context.Context, m *ClientManager, filePath string) error {
	_, err := c.refreshDocument(ctx, m, filePath, "use")
	return err
}

func (c *Client) ReopenFile(ctx context.Context, m *ClientManager, filePath string) error {
	_, err := c.refreshDocument(ctx, m, filePath, "reopen")
	return err
}

func (c *Client) RebuildFile(ctx context.Context, m *ClientManager, filePath string) (bool, error) {
	return c.refreshDocument(ctx, m, filePath, "rebuild")
}

func (c *Client) RefreshOpenFiles(ctx context.Context, m *ClientManager) error {
	c.mu.Lock()
	paths := make([]string, 0, len(c.openFiles))
	for k := range c.openFiles {
		paths = append(paths, k)
	}
	c.mu.Unlock()
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _ = c.refreshDocument(ctx, m, p, "resync")
	}
	return nil
}

func (c *Client) refreshDocument(ctx context.Context, m *ClientManager, filePath, purpose string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	uri := fileToUri(filePath)
	key := canonicalURIKey(uri)
	lock := m.fileLock(c.key, uri)
	lock.Lock()
	defer lock.Unlock()

	fromDisk := purpose == "reopen" || purpose == "rebuild"
	if fromDisk && m.isPendingDiskWrite(uri) {
		return false, nil
	}

	c.mu.Lock()
	delete(c.diagnostics, key)
	delete(c.pulledDiagnostics, key)
	info, open := c.openFiles[key]
	if open && purpose != "resync" {
		info.lastUsedAt = time.Now()
	}
	c.mu.Unlock()

	if !open {
		if purpose != "resync" {
			lock.Unlock()
			err := c.EnsureFileOpen(ctx, m, filePath)
			lock.Lock()
			return false, err
		}
		return false, nil
	}

	content, err := readTextFromDisk(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			if !m.isPendingDiskWrite(uri) {
				_ = c.CloseDocument(ctx, uri)
			}
			return false, nil
		}
		return false, err
	}
	if fromDisk && m.isPendingDiskWrite(uri) {
		return false, nil
	}

	c.mu.Lock()
	info.version++
	version := info.version
	langID := info.languageID
	c.mu.Unlock()

	switch purpose {
	case "reopen":
		if err := c.CloseDocument(ctx, uri); err != nil {
			return false, err
		}
		c.mu.Lock()
		c.idleClosedFiles[key] = version
		c.mu.Unlock()
		if err := c.SendNotification(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": langID,
				"version":    version,
				"text":       content,
			},
		}); err != nil {
			return false, err
		}
		c.trackOpenedFile(uri, langID, version, content)
		saveParams := map[string]any{"textDocument": map[string]string{"uri": uri}}
		if c.saveIncludesText() {
			saveParams["text"] = content
		}
		return false, c.SendNotification(ctx, "textDocument/didSave", saveParams)

	case "rebuild":
		detour := content + "\n"
		if err := c.SendNotification(ctx, "textDocument/didChange", map[string]any{
			"textDocument":    map[string]any{"uri": uri, "version": version},
			"contentChanges":  []map[string]string{{"text": detour}},
			"wantDiagnostics": true,
		}); err != nil {
			return false, err
		}
		c.mu.Lock()
		info.syncedHash = documentSignature(detour)
		info.version++
		restored := info.version
		c.mu.Unlock()
		if err := c.SendNotification(ctx, "textDocument/didChange", map[string]any{
			"textDocument":    map[string]any{"uri": uri, "version": restored},
			"contentChanges":  []map[string]string{{"text": content}},
			"wantDiagnostics": true,
		}); err != nil {
			return false, err
		}
		c.mu.Lock()
		info.syncedHash = documentSignature(content)
		c.lastActivity = time.Now()
		c.mu.Unlock()
		return true, nil

	default:
		if err := c.SendNotification(ctx, "textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]string{{"text": content}},
		}); err != nil {
			return false, err
		}
		saveParams := map[string]any{"textDocument": map[string]string{"uri": uri}}
		if c.saveIncludesText() {
			saveParams["text"] = content
		}
		if err := c.SendNotification(ctx, "textDocument/didSave", saveParams); err != nil {
			return false, err
		}
		c.mu.Lock()
		info.syncedHash = documentSignature(content)
		c.lastActivity = time.Now()
		c.mu.Unlock()
		return false, nil
	}
}

// =============================================================================
// Workspace Edit Reconciliation & Watched Files Notifications
// =============================================================================

type WatchedFileChange struct {
	FilePath string
	Type     FileChangeType
}

// snapshotReadyClientsForWorkspace collects clients for workspace without
// holding m.mu while locking c.mu (strict lock-order safety).
func (m *ClientManager) snapshotReadyClientsForWorkspace(workspace string) []*Client {
	m.mu.Lock()
	candidates := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		cAbs, _ := filepath.Abs(c.cwd)
		if cAbs == workspace {
			candidates = append(candidates, c)
		}
	}
	m.mu.Unlock()

	var active []*Client
	for _, c := range candidates {
		c.mu.Lock()
		ready := c.status == "ready"
		c.mu.Unlock()
		if ready {
			active = append(active, c)
		}
	}
	return active
}

func (m *ClientManager) NotifyWorkspaceWatchedFiles(ctx context.Context, cwd string, changes []WatchedFileChange) error {
	if len(changes) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace, _ := filepath.Abs(cwd)
	active := m.snapshotReadyClientsForWorkspace(workspace)
	if len(active) == 0 {
		return nil
	}

	// Snapshot pending disk writes BEFORE acquiring any c.mu.
	pendingWrites := m.snapshotPendingDiskWrites()

	sendCtx, cancel := context.WithTimeout(ctx, watchedFilesTimeout)
	defer cancel()

	for _, c := range active {
		var events []FileEvent
		allChanged := true
		for _, ch := range changes {
			if !isPathInsideWorkspace(ch.FilePath, workspace) {
				continue
			}
			uri := fileToUri(ch.FilePath)
			key := canonicalURIKey(uri)
			if ch.Type != FileChangeChanged {
				allChanged = false
			}
			isPending := pendingWrites[key]
			c.mu.Lock()
			pub, hasPub := c.diagnostics[key]
			open, hasOpen := c.openFiles[key]
			current := ch.Type == FileChangeChanged &&
				isPending &&
				hasPub && pub.Version != nil && hasOpen && *pub.Version == open.version
			if !current {
				delete(c.diagnostics, key)
				delete(c.pulledDiagnostics, key)
			}
			c.mu.Unlock()
			events = append(events, FileEvent{URI: uri, Type: ch.Type})
		}
		if len(events) == 0 {
			continue
		}
		_ = c.SendNotification(sendCtx, "workspace/didChangeWatchedFiles", map[string]any{"changes": events})
		if !allChanged {
			_ = c.RefreshOpenFiles(sendCtx, m)
		}
	}
	return nil
}

func isPathInsideWorkspace(filePath, workspace string) bool {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		absFile = filePath
	}
	rel, err := filepath.Rel(workspace, absFile)
	if err != nil {
		return false
	}
	return rel == "" || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func uriIsWithin(uri, root string) bool {
	if uri == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(uri, prefix)
}

func (m *ClientManager) reconcileExecutedChanges(ctx context.Context, executed []ExecutedWorkspaceChange, cwd string) error {
	if len(executed) == 0 {
		return nil
	}
	finalURIs := make(map[string]struct{})
	deletedRoots := make(map[string]struct{})
	var watched []WatchedFileChange
	for _, ch := range executed {
		switch ch.Kind {
		case "edit":
			finalURIs[ch.URI] = struct{}{}
			watched = append(watched, WatchedFileChange{FilePath: uriToFile(ch.URI), Type: FileChangeChanged})
		case "create":
			finalURIs[ch.URI] = struct{}{}
			watched = append(watched, WatchedFileChange{FilePath: uriToFile(ch.URI), Type: FileChangeCreated})
		case "rename":
			deletedRoots[ch.OldURI] = struct{}{}
			finalURIs[ch.NewURI] = struct{}{}
			watched = append(watched,
				WatchedFileChange{FilePath: uriToFile(ch.OldURI), Type: FileChangeDeleted},
				WatchedFileChange{FilePath: uriToFile(ch.NewURI), Type: FileChangeCreated},
			)
		case "delete":
			deletedRoots[ch.URI] = struct{}{}
			watched = append(watched, WatchedFileChange{FilePath: uriToFile(ch.URI), Type: FileChangeDeleted})
		}
	}

	workspace, _ := filepath.Abs(cwd)
	active := m.snapshotReadyClientsForWorkspace(workspace)

	for _, c := range active {
		c.mu.Lock()
		openKeys := make([]string, 0, len(c.openFiles))
		for k := range c.openFiles {
			openKeys = append(openKeys, k)
		}
		c.mu.Unlock()

		for _, k := range openKeys {
			u := fileToUri(k)
			for root := range deletedRoots {
				if uriIsWithin(u, root) {
					_ = c.CloseDocument(ctx, u)
					break
				}
			}
		}
		for u := range finalURIs {
			key := canonicalURIKey(u)
			c.mu.Lock()
			_, open := c.openFiles[key]
			c.mu.Unlock()
			if open {
				_ = c.RefreshFile(ctx, m, uriToFile(u))
			}
		}
	}
	return m.NotifyWorkspaceWatchedFiles(ctx, cwd, watched)
}

// ApplyWorkspaceEditWithLsp applies edit on disk (gated + queued + hashline refreshed)
// and reconciles affected LSP overlays and watched-file notifications.
// upstream: packages/coding-agent/src/lsp/client.ts:applyWorkspaceEditWithLsp
func (m *ClientManager) ApplyWorkspaceEditWithLsp(ctx context.Context, sdkCtx sdk.Context, edit *WorkspaceEdit, cwd string) ([]string, error) {
	var executed []ExecutedWorkspaceChange
	res, err := applyWorkspaceEdit(sdkCtx, edit, cwd, func(ch ExecutedWorkspaceChange) {
		executed = append(executed, ch)
	})
	if err != nil {
		_ = m.reconcileExecutedChanges(ctx, executed, cwd)
		return nil, err
	}
	_ = m.reconcileExecutedChanges(ctx, executed, cwd)
	return res.Applied, nil
}

// =============================================================================
// Shutdown, Active Status, Idle Reaper
// =============================================================================

func (m *ClientManager) ShutdownClient(c *Client) bool {
	return m.shutdownClientWithBudget(c, shutdownTimeout, exitTimeout)
}

func (m *ClientManager) shutdownClientWithBudget(c *Client, reqWait, exitWait time.Duration) bool {
	m.mu.Lock()
	if m.clients[c.key] == c {
		delete(m.clients, c.key)
	}
	m.mu.Unlock()

	c.rejectAllPending(errors.New("LSP client shutdown"))
	c.resolveProjectLoaded()

	// Graceful LSP shutdown -> exit handshake (bounded by reqWait so a
	// wedged stdin kills the server instead of hanging session_shutdown).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), reqWait)
	_, err := c.SendRequest(shutdownCtx, "shutdown", nil, reqWait)
	cancel()
	if err == nil {
		exitCtx, exitCancel := context.WithTimeout(context.Background(), exitWait)
		_ = c.SendNotification(exitCtx, "exit", nil)
		exitCancel()
		_ = c.stdin.Close()
		select {
		case <-c.exitedCh:
			return true
		case <-time.After(exitWait):
		}
	} else {
		_ = c.stdin.Close()
	}

	if c.cmd != nil && c.cmd.Process != nil {
		killProcessTree(c.cmd.Process, c.exitedCh, exitWait)
	}
	select {
	case <-c.exitedCh:
		return true
	case <-time.After(exitWait):
		return false
	}
}

// ShutdownStaleClients tears down active and mid-initialize clients for cwd
// whose identity is no longer present in configs (used by `lsp reload *`).
func (m *ClientManager) ShutdownStaleClients(cwd string, configs []ServerConfig) ([]string, error) {
	fresh := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		fresh[stableClientKey(cfg, cwd)] = struct{}{}
	}
	resolvedCwd, _ := filepath.Abs(cwd)

	m.mu.Lock()
	var stalePending []*pendingClientEntry
	for key, pending := range m.clientLocks {
		pAbs, _ := filepath.Abs(pending.cwd)
		if pAbs == resolvedCwd {
			if _, keep := fresh[key]; !keep {
				stalePending = append(stalePending, pending)
				pending.canceled.Store(true)
				if pending.cancel != nil {
					pending.cancel()
				}
			}
		}
	}
	m.mu.Unlock()

	for _, p := range stalePending {
		<-p.done
	}

	m.mu.Lock()
	var stale []*Client
	for key, c := range m.clients {
		cAbs, _ := filepath.Abs(c.cwd)
		if cAbs == resolvedCwd {
			if _, keep := fresh[key]; !keep {
				stale = append(stale, c)
			}
		}
	}
	m.mu.Unlock()

	var stopped []string
	var failed []string
	for _, c := range stale {
		if m.ShutdownClient(c) {
			stopped = append(stopped, c.config.Command)
		} else {
			failed = append(failed, c.config.Command)
		}
	}
	if len(failed) > 0 {
		return stopped, fmt.Errorf("Failed to stop LSP server(s) with superseded configuration: %s", strings.Join(failed, ", "))
	}
	return stopped, nil
}

// ShutdownAll cleanly shuts down every active AND mid-initialize LSP client
// (matching lsp/client.ts:2771-2795) and stops the idle reaper. Intended for
// process exit and test teardown; per-session cleanup uses ReleaseSession.
func (m *ClientManager) ShutdownAll() {
	m.mu.Lock()
	if m.idleStopCh != nil {
		close(m.idleStopCh)
		m.idleStopCh = nil
	}
	all := make([]*Client, 0, len(m.clients))
	seen := make(map[*Client]struct{}, len(m.clients))
	for _, c := range m.clients {
		all = append(all, c)
		seen[c] = struct{}{}
	}
	m.clients = make(map[string]*Client)
	m.sessionRefs = make(map[string]map[string]struct{})

	pending := make([]*pendingClientEntry, 0, len(m.clientLocks))
	for _, p := range m.clientLocks {
		pending = append(pending, p)
		p.canceled.Store(true)
		if p.cancel != nil {
			p.cancel()
		}
	}
	m.clientLocks = make(map[string]*pendingClientEntry)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range all {
		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			m.ShutdownClient(client)
		}(c)
	}
	for _, p := range pending {
		wg.Add(1)
		go func(entry *pendingClientEntry) {
			defer wg.Done()
			<-entry.done
			if entry.client != nil {
				m.mu.Lock()
				if m.clients[entry.client.key] == entry.client {
					delete(m.clients, entry.client.key)
				}
				m.mu.Unlock()
				if _, already := seen[entry.client]; !already {
					m.ShutdownClient(entry.client)
				}
			}
		}(p)
	}
	wg.Wait()
	clearLinterClientCache()
}

// ActiveClients snapshots status without nesting c.mu under m.mu.
func (m *ClientManager) ActiveClients() []LspServerStatus {
	m.mu.Lock()
	snapshot := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		snapshot = append(snapshot, c)
	}
	m.mu.Unlock()

	out := make([]LspServerStatus, 0, len(snapshot))
	for _, c := range snapshot {
		c.mu.Lock()
		st := c.status
		c.mu.Unlock()
		out = append(out, LspServerStatus{
			Name:      c.config.Command,
			Status:    st,
			FileTypes: c.config.FileTypes,
		})
	}
	return out
}

func (m *ClientManager) SetIdleTimeout(d time.Duration) {
	m.mu.Lock()
	m.idleTimeout = d
	if d > 0 {
		m.maybeStartIdleCheckerLocked()
	} else if m.idleStopCh != nil {
		close(m.idleStopCh)
		m.idleStopCh = nil
	}
	m.mu.Unlock()
}

func (m *ClientManager) maybeStartIdleCheckerLocked() {
	if m.idleStopCh != nil {
		return
	}
	hasTimeout := m.idleTimeout > 0
	if !hasTimeout {
		for _, c := range m.clients {
			if c.idleTimeoutMs.Load() > 0 {
				hasTimeout = true
				break
			}
		}
	}
	if !hasTimeout {
		return
	}
	stopCh := make(chan struct{})
	m.idleStopCh = stopCh
	go func() {
		ticker := time.NewTicker(idleCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				m.checkIdleClients()
			}
		}
	}()
}

func (m *ClientManager) checkIdleClients() {
	now := time.Now()
	m.mu.Lock()
	override := m.idleTimeout
	snapshot := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		snapshot = append(snapshot, c)
	}
	m.mu.Unlock()

	for _, c := range snapshot {
		timeout := override
		if timeout <= 0 {
			// Read from the trust-scoped config matching the client's own trust
			// state (or its stored idleTimeoutMs), never loading trusted config
			// for an untrusted workspace.
			if ms := configIdleTimeoutMs(GetConfigWithTrust(c.cwd, c.trusted)); ms > 0 {
				timeout = time.Duration(ms) * time.Millisecond
			} else if ms := c.idleTimeoutMs.Load(); ms > 0 {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}
		if timeout <= 0 {
			continue
		}
		c.mu.Lock()
		busy := len(c.pendingRequests) > 0
		idle := !busy && now.Sub(c.lastActivity) > timeout
		c.mu.Unlock()
		if idle {
			m.ShutdownClient(c)
		}
	}
}

func listCargoManifests(root string) string {
	var found []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "target" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Cargo.toml" {
			if rel, err := filepath.Rel(root, p); err == nil {
				found = append(found, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(found)
	return strings.Join(found, "\n")
}

// ShutdownAll shuts down all active and mid-initialize LSP clients in the
// package-default manager (intended for process exit and test cleanup).
func ShutdownAll() {
	defaultManager.ShutdownAll()
}
