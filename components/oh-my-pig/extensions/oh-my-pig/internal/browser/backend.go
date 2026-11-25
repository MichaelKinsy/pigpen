// Package browser implements omp's `browser` eval prelude, tab lifecycle
// supervision, backend registry, and the CDP/Chromium backend.
//
// This file defines the backend contract that browser backends (including
// internal/ternbrowser for Tern picture-in-picture) implement. Exported
// names and signatures in this file are stable: add only, do not break.
//
// upstream: packages/coding-agent/src/tools/browser/tab-protocol.ts,
//
//	packages/coding-agent/src/tools/browser/tab-call.ts,
//	packages/coding-agent/src/tools/browser/registry.ts
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Kind identifies a browser backend family.
// upstream: BrowserKindTag in registry.ts:43
type Kind string

const (
	KindHeadless  Kind = "headless"
	KindSpawned   Kind = "spawned"
	KindConnected Kind = "connected"
	KindRelay     Kind = "relay"
	KindCmux      Kind = "cmux"
	KindTern      Kind = "tern"
	KindWebKit    Kind = "webkit"
)

// Viewport describes page layout dimensions and device metrics.
// upstream: EmulationViewport in emulation.ts:9, ReadyInfo.viewport in tab-protocol.ts:194
type Viewport struct {
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor,omitempty"`
	IsMobile          bool    `json:"isMobile,omitempty"`
	HasTouch          bool    `json:"hasTouch,omitempty"`
}

// ScrollState describes the viewport scroll position and document scroll bounds.
// upstream: Observation.scroll in tab-protocol.ts:24
type ScrollState struct {
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	ScrollWidth  float64 `json:"scrollWidth"`
	ScrollHeight float64 `json:"scrollHeight"`
}

// ObservationEntry is one interactive (or included) element returned by observe().
// upstream: ObservationEntry in tab-protocol.ts:10
type ObservationEntry struct {
	ID           int      `json:"id"`
	Role         string   `json:"role"`
	Name         string   `json:"name,omitempty"`
	Value        any      `json:"value,omitempty"`
	Description  string   `json:"description,omitempty"`
	Keyshortcuts string   `json:"keyshortcuts,omitempty"`
	States       []string `json:"states"`
}

// Observation is the structured page snapshot returned by tab.observe().
// upstream: Observation in tab-protocol.ts:20
type Observation struct {
	URL      string             `json:"url"`
	Title    string             `json:"title,omitempty"`
	Viewport Viewport           `json:"viewport"`
	Scroll   ScrollState        `json:"scroll"`
	Elements []ObservationEntry `json:"elements"`
}

// ScreenshotResult records a screenshot saved to disk and emitted to the caller.
// upstream: ScreenshotResult in tab-protocol.ts:35
type ScreenshotResult struct {
	Dest     string `json:"dest"`
	MimeType string `json:"mimeType"`
	Bytes    int    `json:"bytes"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// ImageOutput is an inline image emitted by a tab operation (e.g. screenshot).
type ImageOutput struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

// CallStep is one allowlisted method invocation in a tab call chain.
// upstream: TabCallStep in tab-call.ts:8
type CallStep struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

// CallContext carries per-call session facts for a Tab.Call or Tab.Run invocation.
// upstream: SessionSnapshot in tab-protocol.ts:59
type CallContext struct {
	Cwd           string
	ScreenshotDir string
	ExcludeWebP   bool
	TimeoutMs     int
}

// CallResult is the structured output of one Tab.Call or Tab.Run invocation.
// upstream: RunResultOk in tab-protocol.ts:200
type CallResult struct {
	// Value is the JSON-serializable return value of the helper (set as details.value).
	Value any
	// Text is optional human-readable text emitted alongside the result.
	Text string
	// Images are screenshots or other images to surface to the eval cell.
	Images []ImageOutput
	// Screenshots are the saved screenshot metadata entries for details.screenshots.
	Screenshots []ScreenshotResult
}

// TabInfo is the live state snapshot of an open tab.
// upstream: ReadyInfo in tab-protocol.ts:191
type TabInfo struct {
	URL      string   `json:"url"`
	Title    string   `json:"title,omitempty"`
	Viewport Viewport `json:"viewport"`
	TargetID string   `json:"targetId"`
	// Description names the browser instance/surface for the open result line,
	// e.g. "omp's headless Chromium" or "Tern browser picture-in-picture (pane 42)".
	Description string `json:"-"`
	// Notes are extra status lines appended to the open result (e.g. Tern sizing/adopted/off-screen notes).
	Notes []string `json:"-"`
	// Adopted is true when the tab borrows a user-owned page/block that must not be closed on release.
	Adopted bool `json:"-"`
}

// OpenOptions are the normalized options passed to Backend.Open.
// upstream: AcquireTabOptions in tab-supervisor.ts, TernOpenOptions in tern-tab.ts
type OpenOptions struct {
	// Name is the managed tab name (default "main").
	Name string
	// URL is the initial URL to navigate to (optional).
	URL string
	// WaitUntil is "load", "domcontentloaded", "networkidle0", or "networkidle2".
	WaitUntil string
	// Viewport overrides the page layout size when non-nil.
	Viewport *Viewport
	// Target picks an existing user page/block to adopt (URL/title substring or block id).
	Target string
	// Dialogs is "accept", "dismiss", or "" (default: auto-accept alerts/beforeunload, hold confirms/prompts).
	Dialogs string
	// AllowedDomains restricts page requests to matching hostnames / *.domain patterns.
	AllowedDomains []string
	// InitScripts are resolved JavaScript sources installed before document scripts run.
	InitScripts []string
	// DownloadsPath is the absolute download directory, if specified.
	DownloadsPath string
	// UserAgent overrides the tab's User-Agent header.
	UserAgent string
	// Insecure ignores invalid HTTPS certificates for this tab.
	Insecure bool
	// Headless controls window visibility for backends that support both modes.
	Headless bool
	// Cwd is the session working directory for resolving relative paths.
	Cwd string
	// ScreenshotDir is the configured browser.screenshotDir, if any.
	ScreenshotDir string
	// TimeoutMs is the open budget in milliseconds.
	TimeoutMs int
	// Persist opts out of idle-close and settle-freeze.
	Persist bool
	// Endpoint is the CDP HTTP endpoint URL (for KindConnected / KindRelay) or Tern socket path (for KindTern).
	Endpoint string
	// Pane is the Tern pane block ID (for KindTern).
	Pane int
	// Executable is the absolute executable path (for KindSpawned).
	Executable string
	// Argv are extra CLI arguments (for KindSpawned).
	Argv []string
	// Show, when non-nil, explicitly requests initial visibility (e.g. Tern PiP "show": false when parked).
	Show *bool
}

// TargetInfo describes an adoptable user page or Tern browser block returned by browser.targets().
// upstream: TernTarget in tern/blocks.ts:27, CdpTarget in attach.ts:605
type TargetInfo struct {
	Target string `json:"target"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Shown  *bool  `json:"shown,omitempty"`
	Docked *bool  `json:"docked,omitempty"`
	Driven *bool  `json:"driven,omitempty"`
	Tab    string `json:"tab,omitempty"`
}

// TargetList is the return value of Backend.ListTargets.
type TargetList struct {
	Targets []TargetInfo
	// Text is the formatted human-readable listing returned in the prelude text.
	Text string
}

// ManagedTabInfo is the JSON-safe metadata returned by browser.tabs().
// upstream: ManagedTabInfo in tab-supervisor.ts:470
type ManagedTabInfo struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	TargetID string `json:"targetId"`
	Kind     Kind   `json:"kind"`
	Persist  bool   `json:"persist"`
}

// UnavailableError indicates that a backend cannot host a browser right now
// (for example, Tern has no window, is unsupported, or refused connection).
// When an automatic open receives an UnavailableError, the dispatcher falls
// back to the next backend and includes the reason in the open result notes.
// upstream: isTernUnavailable in tern/wire.ts:67, WebKitUnavailableError, CmuxUnavailableError
type UnavailableError struct {
	Backend Kind
	Reason  string
	Err     error
}

func (e *UnavailableError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Backend) + " browser backend is unavailable"
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// IsUnavailable reports whether err indicates a backend cannot host a browser
// and automatic selection should fall back.
func IsUnavailable(err error) bool {
	var ue *UnavailableError
	return errors.As(err, &ue)
}

// Backend opens tabs and optionally lists adoptable targets for one browser kind.
// Backends register themselves via RegisterBackend(kind, backend).
type Backend interface {
	// Available probes whether this backend can open a browser right now.
	// When ok is false, reason explains why for diagnostics or fallback notes.
	Available(ctx context.Context) (ok bool, reason string)

	// Open creates or adopts a tab with the given options.
	// On an environment/connection unavailability that should trigger automatic
	// fallback, return an *UnavailableError.
	Open(ctx context.Context, opts OpenOptions) (Tab, error)
}

// TargetLister is an optional interface implemented by Backends that expose
// adoptable user pages or browser blocks (Tern, CDP connected endpoints, relay).
type TargetLister interface {
	// ListTargets lists adoptable pages/blocks. takenByTarget maps target IDs
	// (e.g. Tern block IDs as strings) to the managed tab name holding them.
	ListTargets(ctx context.Context, opts OpenOptions, takenByTarget map[string]string) (TargetList, error)
}

// Tab is a single live browser tab controlled by a Backend.
//
// All tab helper calls arrive via Call with the validated 1- or 2-step chain
// from omp's tab-call.ts:
//   - Direct value/presence methods (len(chain) == 1):
//     Navigation: url, title, goto, back, forward, reload, pushState
//     Inspection: observe, ariaSnapshot, a11y, extract, text, html, value, attr,
//     count, box, styles, isVisible, isEnabled, isChecked
//     Capture:    screenshot, diffScreenshot, pdf
//     Actions:    click, dblclick, hover, focus, check, uncheck, keyDown, keyUp,
//     mouseMove, mouseDown, mouseUp, clickAt, wheel, highlight,
//     type, fill, press, scroll, drag, scrollIntoView, select, uploadFile
//     Waits:      waitFor, waitForSelector, waitForUrl, waitForText
//     Frames:     frames
//     Dialogs:    dialog, handleDialog, setDialogs
//     Emulation:  emulate, devices
//     Clipboard:  clipboardRead, clipboardWrite, clipboardCopy, clipboardPaste
//     Storage:    cookies, setCookies, clearCookies, storage, setStorage,
//     clearStorage, saveState, loadState
//     Init:       addInitScript, removeInitScript, initScripts
//     Downloads:  waitForDownload, downloads
//     Console:    console, errors, clearConsole
//     Tracing:    traceStart, traceStop, profileStart, profileStop, metrics
//     Network:    route, unroute, routes, requests, request, clearRequests,
//     harStart, harStop, allowedDomains
//     React:      vitals, reactEnable, reactTree, reactInspect, reactRenders, reactSuspense
//     Recording:  recordStart, recordStop, recordRestart, recording
//     WebMCP:     webmcpList, webmcpInvoke, webmcpEvents
//     Execution:  evaluate
//   - Handle chains (len(chain) == 2):
//     chain[0].Method in {"id", "ref"} with chain[1].Method in ELEMENT_METHODS
//     chain[0].Method == "frame"       with chain[1].Method in FRAME_METHODS
type Tab interface {
	// Info returns the tab's current URL, title, viewport, target ID, and open notes.
	Info() TabInfo

	// Reuse applies open-time updates when an existing named tab is re-opened
	// (e.g. navigating to opts.URL, resizing viewport, updating dialog policy).
	// Returns an optional status note for the open result.
	Reuse(ctx context.Context, opts OpenOptions) (note string, err error)

	// Call executes a 1- or 2-step tab helper chain (from action: "call").
	// For TAB_PRESENCE_METHODS (waitFor, waitForSelector), Value must be a bool.
	Call(ctx context.Context, callCtx CallContext, chain []CallStep) (*CallResult, error)

	// Run executes tab.run(fnOrCode, {args, timeout}).
	// Backends without a Puppeteer/Node runtime return an explicit error
	// naming the limitation and pointing to tab.evaluate() / direct helpers.
	Run(ctx context.Context, callCtx CallContext, code string, fn string, args []json.RawMessage) (*CallResult, error)

	// Alive reports whether the underlying page/block/connection is still open.
	Alive() bool

	// Close releases the tab. If kill is true and the backend spawned a dedicated
	// browser process for this tab, terminate it. Borrowed/adopted user pages
	// are released without closing the user's page.
	Close(ctx context.Context, kill bool) error
}

// Freezer is an optional interface implemented by Tabs that support
// settle-freeze on turn end (e.g. Chromium Page.setWebLifecycleState).
type Freezer interface {
	Freeze(ctx context.Context) error
	Unfreeze(ctx context.Context) error
}

var (
	backendMu sync.RWMutex
	backends  = map[string]Backend{}
)

// RegisterBackend registers a browser backend under kind (e.g. "tern",
// "headless", "connected", "spawned", "webkit", "relay", "cmux").
// A later registration for the same kind replaces the earlier one.
func RegisterBackend(kind string, b Backend) {
	backendMu.Lock()
	defer backendMu.Unlock()
	if b == nil {
		delete(backends, kind)
		return
	}
	backends[kind] = b
}

// LookupBackend returns the registered backend for kind.
func LookupBackend(kind string) (Backend, bool) {
	backendMu.RLock()
	defer backendMu.RUnlock()
	b, ok := backends[kind]
	return b, ok
}

// RegisteredBackends returns a snapshot copy of registered backend kinds.
func RegisteredBackends() map[string]Backend {
	backendMu.RLock()
	defer backendMu.RUnlock()
	out := make(map[string]Backend, len(backends))
	for k, v := range backends {
		out[k] = v
	}
	return out
}
