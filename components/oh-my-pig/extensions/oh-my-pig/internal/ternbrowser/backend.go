package ternbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Compile-time interface assertions.
var (
	_ browser.Backend      = (*Backend)(nil)
	_ browser.TargetLister = (*Backend)(nil)
	_ browser.Tab          = (*Tab)(nil)
)

var (
	sharedBackendOnce sync.Once
	sharedBackend     *Backend
)

// SharedBackend returns the process-wide Tern Backend instance so repeated
// Register(e) calls across sessions never replace a live session's client/tabs.
func SharedBackend() *Backend {
	sharedBackendOnce.Do(func() {
		sharedBackend = NewBackend()
	})
	return sharedBackend
}

// Register registers the shared Tern browser backend with the browser dispatcher.
// Session-end cleanup is owned by internal/browser's lifecycle hook (which calls
// Tab.Close on all open tabs). Safe to call multiple times across sessions;
// performs no I/O at registration time.
func Register(_ *sdk.Extension) {
	browser.RegisterBackend(string(browser.KindTern), SharedBackend())
}

// Availability reports whether the Tern browser backend can be used in the
// current environment.
// upstream: packages/coding-agent/src/tools/browser/tern/kind.ts:43
func Availability(settingEnabled bool, lookup func(string) (string, bool)) (socket string, pane int, reason string, ok bool) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	flagVal, _ := lookup("PI_BROWSER_TERN")
	if !ternwire.ParseFlag(flagVal, settingEnabled) {
		return "", 0, "Tern browser backend is disabled", false
	}
	socket, pane, ok = ternwire.PaneFromEnv(lookup)
	if !ok {
		return "", 0, "Tern browser backend requires running inside a Tern pane (TERN_PANE_SOCKET and TERN_PANE)", false
	}
	return socket, pane, "", true
}

// Backend manages a shared ternwire.Client and all open Tern browser PiP tabs.
type Backend struct {
	mu             sync.Mutex
	dialMu         sync.Mutex // B4: dial serialized separately so Tab.Close never blocks on dial.
	settingEnabled bool
	lookup         func(string) (string, bool)
	client         *ternwire.Client
	socketPath     string
	pane           int
	tabs           map[string]*Tab
	taken          map[int]string
}

// NewBackend creates a Tern browser backend.
func NewBackend() *Backend {
	return &Backend{
		settingEnabled: true,
		lookup:         os.LookupEnv,
		tabs:           make(map[string]*Tab),
		taken:          make(map[int]string),
	}
}

// Available probes whether Tern can open a browser right now.
func (b *Backend) Available(_ context.Context) (bool, string) {
	_, _, reason, ok := Availability(b.settingEnabled, b.lookup)
	return ok, reason
}

// pruneDeadTabsLocked removes tabs that are no longer alive (B1).
func (b *Backend) pruneDeadTabsLocked() {
	for name, tab := range b.tabs {
		if !tab.Alive() {
			delete(b.tabs, name)
			if b.taken[tab.block] == name {
				delete(b.taken, tab.block)
			}
		}
	}
}

func (b *Backend) ensureClient(ctx context.Context, opts browser.OpenOptions) (*ternwire.Client, int, error) {
	b.mu.Lock()
	if b.client != nil && b.client.Connected() && (opts.Endpoint == "" || b.socketPath == opts.Endpoint) {
		c, p := b.client, b.pane
		if opts.Pane > 0 {
			p = opts.Pane
		}
		b.mu.Unlock()
		return c, p, nil
	}
	settingEnabled := b.settingEnabled
	lookup := b.lookup
	b.mu.Unlock()

	socket := opts.Endpoint
	pane := opts.Pane
	if socket == "" {
		var reason string
		var ok bool
		socket, pane, reason, ok = Availability(settingEnabled, lookup)
		if !ok {
			return nil, 0, &browser.UnavailableError{
				Backend: browser.KindTern,
				Reason:  reason,
			}
		}
	}

	// B4: do not hold b.mu while dialing.
	b.dialMu.Lock()
	defer b.dialMu.Unlock()

	b.mu.Lock()
	if b.client != nil && b.client.Connected() && b.socketPath == socket {
		c, p := b.client, b.pane
		if pane > 0 {
			p = pane
		}
		b.mu.Unlock()
		return c, p, nil
	}
	b.mu.Unlock()

	client, err := ternwire.Dial(ctx, socket)
	if err != nil {
		if ternwire.IsUnavailable(err) {
			return nil, 0, &browser.UnavailableError{
				Backend: browser.KindTern,
				Reason:  err.Error(),
				Err:     err,
			}
		}
		return nil, 0, err
	}

	b.mu.Lock()
	b.client = client
	b.socketPath = socket
	b.pane = pane
	b.mu.Unlock()
	return client, pane, nil
}

// ListTargets lists all browser blocks in the Tern window.
// upstream: packages/coding-agent/src/tools/browser/tern/blocks.ts:58-80
func (b *Backend) ListTargets(ctx context.Context, opts browser.OpenOptions, takenByTarget map[string]string) (browser.TargetList, error) {
	client, _, err := b.ensureClient(ctx, opts)
	if err != nil {
		return browser.TargetList{}, err
	}
	blocks, err := ListTernBlocks(ctx, client)
	if err != nil {
		return browser.TargetList{}, wrapUnavailable(err)
	}

	taken := make(map[int]string, len(takenByTarget))
	b.mu.Lock()
	b.pruneDeadTabsLocked()
	for blk, tabName := range b.taken {
		taken[blk] = tabName
	}
	b.mu.Unlock()
	for k, v := range takenByTarget {
		if id, err := strconv.Atoi(k); err == nil {
			taken[id] = v
		}
	}

	ternTargets := TernTargets(blocks, taken)
	targets := make([]browser.TargetInfo, 0, len(ternTargets))
	for _, tt := range ternTargets {
		docked := tt.Docked
		targets = append(targets, browser.TargetInfo{
			Target: tt.Target,
			Title:  tt.Title,
			URL:    tt.URL,
			Shown:  tt.Shown,
			Docked: &docked,
			Driven: tt.Driven,
			Tab:    tt.Tab,
		})
	}
	return browser.TargetList{
		Targets: targets,
		Text:    DescribeTernTargets(blocks, taken),
	}, nil
}

// Open opens a new browser PiP over the current Tern pane (at about:blank,
// configured before the first real navigation), or adopts opts.Target.
// If opts.Show is non-nil it is passed through verbatim (e.g. false opens parked);
// otherwise opts.Headless == true opens parked ("show": false).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:594-665
func (b *Backend) Open(ctx context.Context, opts browser.OpenOptions) (browser.Tab, error) {
	show := opts.Show
	if show == nil && opts.Headless {
		show = new(false)
	}
	return b.OpenWithShow(ctx, opts, show)
}

// OpenWithShow opens a Tern browser tab with an explicit optional "show" flag
// (nil omits "show" so Tern uses its default; false opens parked).
func (b *Backend) OpenWithShow(ctx context.Context, opts browser.OpenOptions, show *bool) (browser.Tab, error) {
	client, pane, err := b.ensureClient(ctx, opts)
	if err != nil {
		return nil, err
	}

	name := opts.Name
	if name == "" {
		name = "main"
	}
	totalTimeout := 30 * time.Second
	if opts.TimeoutMs > 0 {
		totalTimeout = time.Duration(opts.TimeoutMs) * time.Millisecond
	}
	startedAt := time.Now()
	remaining := func() time.Duration {
		rem := totalTimeout - time.Since(startedAt)
		if rem < time.Millisecond {
			return time.Millisecond
		}
		return rem
	}

	if opts.Target != "" {
		return b.adoptBlock(ctx, client, pane, name, opts, remaining)
	}
	return b.openPiP(ctx, client, pane, name, opts, show, totalTimeout, remaining)
}

func (b *Backend) openPiP(ctx context.Context, client *ternwire.Client, pane int, name string, opts browser.OpenOptions, show *bool, totalTimeout time.Duration, remaining func() time.Duration) (*Tab, error) {
	vp := browser.Viewport{Width: defaultViewportW, Height: defaultViewportH, DeviceScaleFactor: 1}
	var requestedVP *browser.Viewport
	if opts.Viewport != nil && opts.Viewport.Width > 0 && opts.Viewport.Height > 0 {
		vp = *opts.Viewport
		if vp.DeviceScaleFactor == 0 {
			vp.DeviceScaleFactor = 1
		}
		requestedVP = new(vp)
	}

	var opened struct {
		Block *int `json:"block"`
	}
	err := client.RequestWithOptions(ctx, "browser", opOpenReq{
		Op:     "open",
		Owner:  pane,
		URL:    blankURL,
		Width:  vp.Width,
		Height: vp.Height,
		Show:   show,
	}, &opened, ternwire.RequestOptions{
		Timeout: totalTimeout,
		// S6: OnLateAnswer runs in its own goroutine; uses detached 5s context.
		OnLateAnswer: func(raw json.RawMessage) {
			var late struct {
				Block *int `json:"block"`
			}
			if json.Unmarshal(raw, &late) == nil && late.Block != nil {
				cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = client.RequestWithOptions(cctx, "browser", opBlockOnlyReq{
					Op:    "close",
					Block: *late.Block,
				}, nil, ternwire.RequestOptions{Timeout: 5 * time.Second})
			}
		},
	})
	if err != nil {
		return nil, wrapUnavailable(err)
	}
	if opened.Block == nil {
		return nil, &ternwire.Error{
			Kind:    ternwire.ErrProtocol,
			Message: fmt.Sprintf("%s answered the browser open without a block id", client.Product()),
		}
	}

	tab := newTab(b, client, pane, *opened.Block, name, vp, requestedVP, nil)

	// upstream: tern-tab.ts:618-627 — configure before first real navigation, close on failure.
	if err := tab.configureOpen(ctx, opts, remaining); err != nil {
		_ = tab.Close(context.Background(), false)
		return nil, err
	}

	b.registerTab(name, tab)
	return tab, nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:638-665
func (b *Backend) adoptBlock(ctx context.Context, client *ternwire.Client, pane int, name string, opts browser.OpenOptions, remaining func() time.Duration) (*Tab, error) {
	blocks, err := ListTernBlocks(ctx, client)
	if err != nil {
		return nil, wrapUnavailable(err)
	}

	// Reserve the picked block atomically under b.mu so concurrent adopts of the same target cannot both win.
	b.mu.Lock()
	b.pruneDeadTabsLocked()
	picked := PickTernBlock(blocks, opts.Target, b.taken)
	if picked == nil {
		takenCopy := make(map[int]string, len(b.taken))
		for k, v := range b.taken {
			takenCopy[k] = v
		}
		b.mu.Unlock()
		return nil, NoTernBlockError(blocks, opts.Target, takenCopy)
	}
	b.taken[picked.Block] = name
	b.mu.Unlock()

	vp := browser.Viewport{}
	if opts.Viewport != nil {
		vp = *opts.Viewport
	}
	adoptedCopy := *picked
	tab := newTab(b, client, pane, picked.Block, name, vp, opts.Viewport, &adoptedCopy)

	cleanupReserve := func() {
		b.mu.Lock()
		if b.taken[picked.Block] == name && b.tabs[name] != tab {
			delete(b.taken, picked.Block)
		}
		b.mu.Unlock()
	}

	// Skip old events using `next` cursor, refresh state, apply only caller-given settings.
	var evAns struct {
		Next *int `json:"next"`
	}
	if err := tab.opWithTimeout(ctx, "events", opEventsReq{Op: "events", Block: tab.block, After: 0}, &evAns, remaining()); err != nil {
		cleanupReserve()
		_ = tab.Close(context.Background(), false)
		return nil, wrapUnavailable(err)
	}
	if evAns.Next != nil {
		tab.mu.Lock()
		tab.cursor = *evAns.Next
		tab.mu.Unlock()
	}

	if _, err := tab.refreshState(ctx); err != nil {
		cleanupReserve()
		_ = tab.Close(context.Background(), false)
		return nil, wrapUnavailable(err)
	}
	if opts.Dialogs != "" {
		if err := tab.op(ctx, "dialogs", opDialogsReq{Op: "dialogs", Block: tab.block, Policy: opts.Dialogs}, nil); err != nil {
			cleanupReserve()
			_ = tab.Close(context.Background(), false)
			return nil, wrapUnavailable(err)
		}
	}
	if err := tab.applySettings(ctx, opts); err != nil {
		cleanupReserve()
		_ = tab.Close(context.Background(), false)
		return nil, err
	}
	if opts.Viewport != nil && picked.Owner != nil && opts.Viewport.Width > 0 && opts.Viewport.Height > 0 {
		if err := tab.SetViewport(ctx, *opts.Viewport); err != nil {
			cleanupReserve()
			_ = tab.Close(context.Background(), false)
			return nil, err
		}
	}
	if err := tab.applyCapture(ctx); err != nil {
		cleanupReserve()
		_ = tab.Close(context.Background(), false)
		return nil, err
	}
	if opts.URL != "" {
		if err := tab.Goto(ctx, opts.URL, opts.WaitUntil, remaining()); err != nil {
			cleanupReserve()
			_ = tab.Close(context.Background(), false)
			return nil, err
		}
	}

	b.registerTab(name, tab)
	return tab, nil
}

// registerTab records tab under name, closing any superseded live tab with the same name (B2).
func (b *Backend) registerTab(name string, tab *Tab) {
	b.mu.Lock()
	prev := b.tabs[name]
	b.tabs[name] = tab
	b.taken[tab.block] = name
	b.mu.Unlock()
	if prev != nil && prev != tab && prev.block != tab.block {
		_ = prev.Close(context.Background(), false)
	}
}

func wrapUnavailable(err error) error {
	if ternwire.IsUnavailable(err) {
		return &browser.UnavailableError{
			Backend: browser.KindTern,
			Reason:  err.Error(),
			Err:     err,
		}
	}
	return err
}

// Tab is a single browser tab backed by a Tern browser picture-in-picture block.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:483
type Tab struct {
	backend          *Backend
	client           *ternwire.Client
	product          string
	name             string
	pane             int
	block            int
	adopted          *BlockEntry
	requestedVP      *browser.Viewport
	nativeConsole    bool
	nativeNetwork    bool
	observesNatively bool
	netLog           *NetworkLog

	mu                 sync.Mutex
	url                string
	title              string
	shown              *bool
	viewport           browser.Viewport
	cursor             int
	events             []TernEvent
	consoleEntries     []ConsoleEntry
	consoleSeq         int
	consoleDropped     int
	pendingDialog      *DialogState
	frameURLs          map[string]string
	downloads          map[int]*downloadTrack
	completedDownloads []BrowserDownload
	unclaimedDownloads []BrowserDownload
	downloadDir        string
	captureInDocument  bool
	initScripts        []initScriptEntry
	nextInitScript     int
	allowedDomains     []string
	emulation          map[string]any
	pageEmulation      PageEmulation
	deviceUserAgent    string
	pointer            *[2]float64
	pointerSpace       PointerSpace
	buttons            map[string]bool
	heldMods           []Modifier
	observedRefs       map[int]string
	observedRects      map[int][4]float64
	frameRoots         map[string]string
	goneReason         string
	goneErr            error
	closed             bool
	actionBudgetMs     int
}

func newTab(b *Backend, client *ternwire.Client, pane, block int, name string, vp browser.Viewport, requestedVP *browser.Viewport, adopted *BlockEntry) *Tab {
	nativeConsole := client.Supports("console")
	nativeNetwork := client.Supports("network")
	t := &Tab{
		backend:          b,
		client:           client,
		product:          client.Product(),
		name:             name,
		pane:             pane,
		block:            block,
		adopted:          adopted,
		requestedVP:      requestedVP,
		nativeConsole:    nativeConsole,
		nativeNetwork:    nativeNetwork,
		observesNatively: nativeConsole && nativeNetwork,
		netLog:           NewNetworkLog(client.Product(), nativeNetwork),
		url:              blankURL,
		viewport:         vp,
		consoleSeq:       1,
		frameURLs:        make(map[string]string),
		downloads:        make(map[int]*downloadTrack),
		nextInitScript:   1,
		emulation:        make(map[string]any),
		buttons:          make(map[string]bool),
		observedRefs:     make(map[int]string),
		observedRects:    make(map[int][4]float64),
		frameRoots:       make(map[string]string),
	}
	if adopted != nil {
		if adopted.URL != "" {
			t.url = adopted.URL
		}
		t.title = adopted.Title
		t.shown = adopted.Shown
	}
	return t
}

// Block returns the Tern block ID.
func (t *Tab) Block() int { return t.block }

// Gone returns the gone-block reason once latched (S8), or "".
func (t *Tab) Gone() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.goneReason
}

// Shown returns the last observed shown flag.
func (t *Tab) Shown() *bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shown
}

// URL returns the last observed URL.
func (t *Tab) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

func (t *Tab) nativeDom() bool {
	return t.client.Supports("dom")
}

// op sends a typed block op, checking gone-block latch (S8), dialog blocking,
// and unresponsive/not_found translation.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:858-967
func (t *Tab) op(ctx context.Context, opName string, req any, out any) error {
	return t.opWithTimeout(ctx, opName, req, out, 30*time.Second)
}

func (t *Tab) opWithTimeout(ctx context.Context, opName string, req any, out any, timeout time.Duration) error {
	t.mu.Lock()
	goneErr := t.goneErr
	dialog := t.dialogStateLocked()
	t.mu.Unlock()

	if goneErr != nil {
		return goneErr
	}
	if dialogMode := pageOpDialog[opName]; dialogMode != "" && dialog.Open {
		return dialogBlocksPageError(t.product+" "+opName, dialog, "is open")
	}

	err := t.client.RequestWithOptions(ctx, "browser", req, out, ternwire.RequestOptions{Timeout: timeout})
	if err != nil {
		err = t.translateUnresponsive(ctx, err)
		err = t.translateBlockFailure(ctx, err)
		return err
	}
	if pageOpDialog[opName] == "answers" {
		_ = t.pull(ctx)
	}
	return nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:873-895
func (t *Tab) translateUnresponsive(ctx context.Context, err error) error {
	var te *ternwire.Error
	if !errors.As(err, &te) || !t.client.Supports("responsive") {
		return err
	}
	cause := te.Message
	if te.Kind == ternwire.ErrTimeout {
		sctx, cancel := context.WithTimeout(ctx, time.Second)
		var st struct {
			Responsive *bool `json:"responsive"`
		}
		serr := t.client.RequestWithOptions(sctx, "browser", opBlockOnlyReq{Op: "state", Block: t.block}, &st, ternwire.RequestOptions{Timeout: time.Second})
		cancel()
		if serr != nil || st.Responsive == nil || *st.Responsive {
			return err
		}
		cause += ": the page is unresponsive, a script keeps its web process busy"
	} else if te.Kind != ternwire.ErrUnresponsive {
		return err
	}
	return &ternwire.Error{
		Kind: ternwire.ErrUnresponsive,
		Message: fmt.Sprintf(
			"%s. In tab %s, tab.reload() or tab.goto() ends that web process and loads the page in a new one; closing the tab ends it too.",
			cause, jsonQuote(t.name),
		),
	}
}

// translateBlockFailure confirms via `blocks` whether a `not_found` means the
// block itself is gone, latching goneErr so no more ops are sent for it (S8).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:902-928
func (t *Tab) translateBlockFailure(ctx context.Context, err error) error {
	t.mu.Lock()
	if t.goneErr != nil {
		ge := t.goneErr
		t.mu.Unlock()
		return ge
	}
	t.mu.Unlock()

	var te *ternwire.Error
	if !errors.As(err, &te) || te.Kind != ternwire.ErrNotFound {
		return err
	}
	bctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	blocks, berr := ListTernBlocks(bctx, t.client)
	cancel()
	if berr != nil {
		return err
	}
	for _, b := range blocks {
		if b.Block == t.block {
			// Block is still listed: this not_found is the op's own error (e.g. stale ref).
			return err
		}
	}

	quoted := jsonQuote(t.name)
	adoptedSuffix := ""
	if t.adopted != nil {
		adoptedSuffix = " (the user's)"
	}
	blkDesc := fmt.Sprintf("its %s browser block %d%s", t.product, t.block, adoptedSuffix)
	var reason, reopen string
	if t.product == "Tern" {
		reason = fmt.Sprintf("%s is no longer in this Tern window (the user closed it, or its tab or window closed; omp did not close it)", blkDesc)
		reopen = fmt.Sprintf(`Reopen it with browser.open({ name: %s, app: "tern", url }); browser.targets({ app: "tern" }) lists the live blocks, and passing one's id as target adopts it.`, quoted)
	} else {
		reason = fmt.Sprintf("%s is no longer in omp's WebKit host", blkDesc)
		reopen = fmt.Sprintf(`Reopen it with browser.open({ name: %s, app: "own", url }).`, quoted)
	}
	goneErr := fmt.Errorf("Tab %s is gone: %s. omp closed the tab. %s\n(%s)", quoted, reason, reopen, te.Message)

	t.mu.Lock()
	t.goneReason = reason
	t.goneErr = goneErr
	t.closed = true
	t.mu.Unlock()

	if t.backend != nil {
		t.backend.mu.Lock()
		if t.backend.tabs[t.name] == t {
			delete(t.backend.tabs, t.name)
		}
		if t.backend.taken[t.block] == t.name {
			delete(t.backend.taken, t.block)
		}
		t.backend.mu.Unlock()
	}
	return goneErr
}

// eval evaluates source(args...) and unwraps `{value: ...}` (fixes S4).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1040-1047
func (t *Tab) eval(ctx context.Context, source string, args []any, world string, frame string, out any) error {
	cleanArgs := withoutTrailingNil(args)
	if cleanArgs == nil {
		cleanArgs = []any{}
	}
	var env struct {
		Value json.RawMessage `json:"value"`
	}
	req := opEvalReq{
		Op:       "eval",
		Block:    t.block,
		Function: source,
		Args:     cleanArgs,
		World:    world,
		Frame:    frame,
	}
	if err := t.op(ctx, "eval", req, &env); err != nil {
		return err
	}
	if out != nil && len(env.Value) > 0 && string(env.Value) != "null" {
		if err := json.Unmarshal(env.Value, out); err != nil {
			return fmt.Errorf("%s eval value decode failed: %w", t.product, err)
		}
	}
	return nil
}

// kit calls globalThis.__ompTernKit[method](...args) in the isolated world,
// installing TernKitSource on demand if missing.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1060-1076
func (t *Tab) kit(ctx context.Context, method string, args []any, frame string, out any) error {
	if args == nil {
		args = []any{}
	}
	for range 2 {
		var res struct {
			Missing bool            `json:"missing"`
			Value   json.RawMessage `json:"value"`
		}
		err := t.eval(ctx, kitCallSource, []any{method, args}, "isolated", frame, &res)
		if err != nil {
			var te *ternwire.Error
			if errors.As(err, &te) && te.Kind == ternwire.ErrJS {
				return fmt.Errorf("%s", kitErrorText(te))
			}
			return err
		}
		if res.Missing {
			if err := t.eval(ctx, "function () {\n"+TernKitSource+"\n}", nil, "isolated", frame, nil); err != nil {
				return err
			}
			continue
		}
		if out != nil && len(res.Value) > 0 {
			if err := json.Unmarshal(res.Value, out); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("The %s page kit could not be installed in this document", t.product)
}

// evalUser evaluates user source/function in the page world, rewriting JS errors.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1079-1090
func (t *Tab) evalUser(ctx context.Context, label string, source string, args []any, frame string, out any) error {
	err := t.eval(ctx, source, args, "page", frame, out)
	if err != nil {
		var te *ternwire.Error
		if errors.As(err, &te) && te.Kind == ternwire.ErrJS {
			return fmt.Errorf("%s threw a JavaScript exception:\n%s", label, pageErrorText(te))
		}
		return err
	}
	return nil
}

// dom sends a native DOM read and validates engine-level {error}.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1055
func (t *Tab) dom(ctx context.Context, read string, req any, out any, timeout time.Duration) error {
	var raw json.RawMessage
	if err := t.opWithTimeout(ctx, "dom", req, &raw, timeout); err != nil {
		return err
	}
	return CheckDomReply(t.product, read, raw, out)
}

// input sends trusted input events.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1099-1102
func (t *Tab) input(ctx context.Context, steps []InputStep) error {
	if len(steps) == 0 {
		return nil
	}
	return t.op(ctx, "input", opInputReq{
		Op:     "input",
		Block:  t.block,
		Events: steps,
	}, nil)
}

// pull fetches events after t.cursor and ingests them, advancing t.cursor by event.seq (fixes A/events).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1105-1134
func (t *Tab) pull(ctx context.Context) error {
	t.mu.Lock()
	after := t.cursor
	t.mu.Unlock()

	var ans struct {
		Events  []map[string]any `json:"events"`
		Dropped int              `json:"dropped"`
	}
	if err := t.opWithTimeout(ctx, "events", opEventsReq{Op: "events", Block: t.block, After: after}, &ans, 10*time.Second); err != nil {
		return err
	}
	t.ingestEvents(ans.Events, ans.Dropped)
	return nil
}

func (t *Tab) ingestEvents(rawEvents []map[string]any, dropped int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, raw := range rawEvents {
		seqF, ok1 := numVal(raw["seq"])
		evType, ok2 := raw["type"].(string)
		if !ok1 || !ok2 {
			continue
		}
		seq := int(seqF)
		if seq <= t.cursor {
			continue
		}
		t.cursor = seq
		ev := TernEvent{Seq: seq, Type: evType, Raw: raw}
		t.events = append(t.events, ev)
		t.ingestOneLocked(ev)
	}
	if len(t.events) > eventLogLimit {
		t.events = t.events[len(t.events)-eventLogLimit:]
	}
	if dropped > 0 {
		t.netLog.ForgetInflight()
	}
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1137-1320
func (t *Tab) ingestOneLocked(ev TernEvent) {
	switch ev.Type {
	case "url", "committed":
		if u, ok := ev.Raw["url"].(string); ok {
			t.url = u
		}
		if ev.Type == "committed" {
			t.pendingDialog = nil
			clear(t.frameURLs)
			t.captureInDocument = t.captureNeededLocked()
			t.netLog.ForgetInflight()
		}
	case "title":
		if title, ok := ev.Raw["title"].(string); ok {
			t.title = title
		}
	case "frame":
		name, ok1 := ev.Raw["name"].(string)
		u, ok2 := ev.Raw["url"].(string)
		if ok1 && ok2 {
			t.frameURLs[name] = u
		}
	case "dialog":
		if ev.Raw["handled"] == nil {
			kind, _ := ev.Raw["kind"].(string)
			if kind == "" {
				kind = "alert"
			}
			msg, _ := ev.Raw["message"].(string)
			ds := &DialogState{Open: true, Type: kind, Message: msg}
			if kind == "prompt" {
				def, _ := ev.Raw["default"].(string)
				ds.DefaultValue = def
			}
			t.pendingDialog = ds
		}
	case "download":
		t.ingestDownloadLocked(ev.Raw)
	case "response":
		if t.nativeNetwork {
			if _, ok := numVal(ev.Raw["id"]); ok {
				t.netLog.IngestNative(ev.Raw)
				return
			}
		}
		t.netLog.IngestNavigationResponse(ev.Raw)
	case "request", "requestFinished":
		if t.nativeNetwork {
			t.netLog.IngestNative(ev.Raw)
		}
	case "requestFailed":
		if t.nativeNetwork {
			if failed := t.netLog.IngestNative(ev.Raw); failed != nil {
				ft := failed.FailureText
				if ft == "" {
					ft = "request failed"
				}
				t.pushConsoleLocked(ConsoleEntry{
					TS:       time.Now().UnixMilli(),
					Type:     "requestfailed",
					Level:    "error",
					Text:     fmt.Sprintf("%s: %s", ft, failed.URL),
					Location: failed.URL,
				})
			}
		}
	case "console":
		if t.nativeConsole {
			t.ingestNativeConsoleLocked(ev.Raw)
		}
	case "blocked":
		if u, ok := ev.Raw["url"].(string); ok {
			t.netLog.IngestBlocked(u)
		}
	case "message":
		t.ingestMessageLocked(ev.Raw)
	}
}

func (t *Tab) ingestNativeConsoleLocked(raw map[string]any) {
	text, _ := raw["text"].(string)
	var loc string
	if u, ok := raw["url"].(string); ok && u != "" {
		loc = u
		if ln, ok := numVal(raw["line"]); ok {
			loc = fmt.Sprintf("%s:%d", u, int(ln))
		}
	}
	if raw["source"] == "javascript" && raw["level"] == "error" {
		t.pushConsoleLocked(ConsoleEntry{
			TS:       time.Now().UnixMilli(),
			Type:     "pageerror",
			Level:    "error",
			Text:     text,
			Location: loc,
		})
		return
	}
	t.pushConsoleLocked(ConsoleEntry{
		TS:       time.Now().UnixMilli(),
		Type:     "console",
		Level:    consoleLevel(raw["level"]),
		Text:     text,
		Location: loc,
		Args:     []any{text},
	})
}

func (t *Tab) ingestMessageLocked(raw map[string]any) {
	if raw["world"] != "page" {
		return
	}
	body, ok := raw["body"].(string)
	if !ok {
		return
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return
	}
	if msg["omp"] != "tern" {
		return
	}
	if d, ok := numVal(msg["dropped"]); ok {
		t.consoleDropped += int(d)
	}
	ts := time.Now().UnixMilli()
	if f, ok := numVal(msg["ts"]); ok {
		ts = int64(f)
	}
	loc, _ := msg["location"].(string)
	kind, _ := msg["kind"].(string)
	if t.nativeConsole && (kind == "console" || kind == "pageerror") {
		return
	}
	switch kind {
	case "console":
		text, _ := msg["text"].(string)
		args, _ := msg["args"].([]any)
		if args == nil {
			args = []any{}
		}
		t.pushConsoleLocked(ConsoleEntry{
			TS:       ts,
			Type:     "console",
			Level:    consoleLevel(msg["level"]),
			Text:     text,
			Location: loc,
			Args:     args,
		})
	case "pageerror":
		text, _ := msg["text"].(string)
		stack, _ := msg["stack"].(string)
		t.pushConsoleLocked(ConsoleEntry{
			TS:       ts,
			Type:     "pageerror",
			Level:    "error",
			Text:     text,
			Location: loc,
			Stack:    stack,
		})
	case "requestfailed":
		if !t.netLog.IngestPageMessage(msg) {
			return
		}
		errStr, _ := msg["error"].(string)
		if errStr == "" {
			errStr = "request failed"
		}
		u, _ := msg["url"].(string)
		t.pushConsoleLocked(ConsoleEntry{
			TS:       ts,
			Type:     "requestfailed",
			Level:    "error",
			Text:     fmt.Sprintf("%s: %s", errStr, u),
			Location: u,
		})
	case "request", "response":
		t.netLog.IngestPageMessage(msg)
	}
}

func (t *Tab) pushConsoleLocked(entry ConsoleEntry) {
	entry.Seq = t.consoleSeq
	t.consoleSeq++
	t.consoleEntries = append(t.consoleEntries, entry)
	for len(t.consoleEntries) > consoleLimit {
		t.consoleEntries = t.consoleEntries[1:]
		t.consoleDropped++
	}
}

func (t *Tab) ingestDownloadLocked(raw map[string]any) {
	idF, ok := numVal(raw["id"])
	if !ok {
		return
	}
	id := int(idF)
	known := t.downloads[id]
	entry := &downloadTrack{State: "started"}
	if known != nil {
		entry.URL = known.URL
		entry.Path = known.Path
	}
	if u, ok := raw["url"].(string); ok {
		entry.URL = u
	}
	if p, ok := raw["path"].(string); ok {
		entry.Path = p
	}
	if st, ok := raw["state"].(string); ok && st != "" {
		entry.State = st
	}
	if e, ok := raw["error"].(string); ok {
		entry.Err = e
	}
	t.downloads[id] = entry
	if entry.State != "finished" || entry.Path == "" {
		return
	}
	var sz int64
	if fi, err := os.Stat(entry.Path); err == nil {
		sz = fi.Size()
	}
	dl := BrowserDownload{
		Path:              entry.Path,
		SuggestedFilename: filepath.Base(entry.Path),
		URL:               entry.URL,
		Bytes:             sz,
	}
	t.completedDownloads = append(t.completedDownloads, dl)
	t.unclaimedDownloads = append(t.unclaimedDownloads, dl)
}

func (t *Tab) dialogStateLocked() DialogState {
	if t.pendingDialog == nil {
		return DialogState{Open: false}
	}
	return *t.pendingDialog
}

func (t *Tab) eventsSince(since int) []TernEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []TernEvent
	for _, ev := range t.events {
		if ev.Seq > since {
			out = append(out, ev)
		}
	}
	return out
}

func (t *Tab) poll(ctx context.Context, label string, timeout time.Duration, pull bool, check func() (bool, error), timeoutNote func() string) error {
	deadline := time.Now().Add(timeout)
	delay := 25 * time.Millisecond
	for {
		if pull {
			if err := t.pull(ctx); err != nil {
				return err
			}
		}
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			note := ""
			if timeoutNote != nil {
				if n := timeoutNote(); n != "" {
					note = ": " + n
				}
			}
			return fmt.Errorf("%s timed out after %dms%s", label, timeout.Milliseconds(), note)
		}
		if err := sleepCtx(ctx, min(delay, left)); err != nil {
			return err
		}
		delay = min(100*time.Millisecond, time.Duration(float64(delay)*1.5))
	}
}

// refreshState reads url, title, shown, width, height, loading, back, forward.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1355-1365
type ternStateReply struct {
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	Shown      *bool    `json:"shown"`
	Width      *float64 `json:"width"`
	Height     *float64 `json:"height"`
	Loading    *bool    `json:"loading"`
	Back       *bool    `json:"back"`
	Forward    *bool    `json:"forward"`
	Responsive *bool    `json:"responsive"`
}

func (t *Tab) refreshState(ctx context.Context) (ternStateReply, error) {
	var st ternStateReply
	if err := t.opWithTimeout(ctx, "state", opBlockOnlyReq{Op: "state", Block: t.block}, &st, 10*time.Second); err != nil {
		return st, err
	}
	t.mu.Lock()
	if st.URL != "" {
		t.url = st.URL
	}
	if st.Title != "" {
		t.title = st.Title
	}
	if st.Shown != nil {
		t.shown = st.Shown
	}
	if st.Width != nil && st.Height != nil {
		t.viewport.Width = int(math.Round(*st.Width))
		t.viewport.Height = int(math.Round(*st.Height))
	}
	t.mu.Unlock()
	return st, nil
}

// ─── Configuration and scripts ────────────────────────────────────────

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1369-1400
func (t *Tab) configureOpen(ctx context.Context, opts browser.OpenOptions, remaining func() time.Duration) error {
	// Wait for the PiP's initial about:blank load before configuring and navigating.
	err := t.poll(ctx, fmt.Sprintf("The %s page's initial about:blank load", t.product), remaining(), true, func() (bool, error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		for _, ev := range t.events {
			if ev.Type == "loaded" || ev.Type == "failed" {
				return true, nil
			}
		}
		return false, nil
	}, nil)
	if err != nil {
		return err
	}

	policy := opts.Dialogs
	if policy == "" {
		policy = "default"
	}
	if err := t.op(ctx, "dialogs", opDialogsReq{Op: "dialogs", Block: t.block, Policy: policy}, nil); err != nil {
		return err
	}
	if err := t.applySettings(ctx, opts); err != nil {
		return err
	}
	if err := t.syncScripts(ctx); err != nil {
		return err
	}
	if opts.URL != "" && opts.URL != blankURL {
		if err := t.Goto(ctx, opts.URL, opts.WaitUntil, remaining()); err != nil {
			return err
		}
	}
	_, _ = t.refreshState(ctx)
	return nil
}

func (t *Tab) applySettings(ctx context.Context, opts browser.OpenOptions) error {
	if len(opts.AllowedDomains) > 0 {
		norm, err := NormalizeAllowedDomains(opts.AllowedDomains)
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.allowedDomains = norm
		t.mu.Unlock()
		if err := t.op(ctx, "allow", opAllowReq{Op: "allow", Block: t.block, Hosts: norm}, nil); err != nil {
			return err
		}
	}
	if opts.UserAgent != "" {
		t.mu.Lock()
		t.emulation["userAgent"] = opts.UserAgent
		t.mu.Unlock()
		if err := t.op(ctx, "agent", opAgentReq{Op: "agent", Block: t.block, Value: new(opts.UserAgent)}, nil); err != nil {
			return err
		}
	}
	if opts.Insecure {
		if err := t.op(ctx, "insecure", opBoolValueReq{Op: "insecure", Block: t.block, Value: true}, nil); err != nil {
			return err
		}
	}
	if opts.DownloadsPath != "" {
		if err := t.enableDownloads(ctx, opts.DownloadsPath); err != nil {
			return err
		}
	}
	if len(opts.InitScripts) > 0 {
		t.mu.Lock()
		for _, src := range opts.InitScripts {
			t.initScripts = append(t.initScripts, initScriptEntry{
				ID:     fmt.Sprintf("tern-init-%d", t.nextInitScript),
				Source: src,
			})
			t.nextInitScript++
		}
		t.mu.Unlock()
	}
	return nil
}

func (t *Tab) captureNeededLocked() bool {
	if !t.observesNatively {
		return true
	}
	pe := t.pageEmulation
	return len(t.netLog.Rules()) > 0 || pe.Geolocation != nil || pe.Locale != "" || (pe.Offline != nil && *pe.Offline)
}

// buildScripts builds the upstream [{source, world, frames, at}] script list (fixes A/scripts).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1415-1471
func (t *Tab) buildScripts() []scriptSpec {
	t.mu.Lock()
	captureNeeded := t.captureNeededLocked()
	pe := t.pageEmulation
	initScripts := append([]initScriptEntry(nil), t.initScripts...)
	t.mu.Unlock()

	var list []scriptSpec
	if captureNeeded {
		list = append(list, scriptSpec{
			Source: TernCaptureScript(CaptureConfig{Routes: t.netLog.Rules(), Emulation: pe}),
			World:  "page",
			Frames: "all",
			At:     "start",
		})
	}
	for _, s := range initScripts {
		list = append(list, scriptSpec{
			Source: s.Source,
			World:  "page",
			Frames: "all",
			At:     "start",
		})
	}
	kitSrc := TernKitSource
	if t.nativeDom() {
		kitSrc = TernFrameAnnounceSource
	}
	list = append(list, scriptSpec{
		Source: kitSrc,
		World:  "isolated",
		Frames: "all",
		At:     "start",
	})
	return list
}

func (t *Tab) syncScripts(ctx context.Context) error {
	return t.op(ctx, "scripts", opScriptsReq{
		Op:      "scripts",
		Block:   t.block,
		Scripts: t.buildScripts(),
	}, nil)
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1485-1501
func (t *Tab) applyCapture(ctx context.Context) error {
	if err := t.syncScripts(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	needed := t.captureNeededLocked()
	if needed {
		t.captureInDocument = true
	}
	inDoc := t.captureInDocument
	pe := t.pageEmulation
	t.mu.Unlock()
	if !inDoc {
		return nil
	}
	installer := ternCaptureUpdater
	if needed {
		installer = TernCaptureInstaller
	}
	cfg := CaptureConfig{Routes: t.netLog.Rules(), Emulation: pe}
	if cfg.Routes == nil {
		cfg.Routes = []RouteRule{}
	}
	frames := []string{""}
	var children []struct {
		Path string `json:"path"`
	}
	if err := t.kit(ctx, "frames", nil, "", &children); err == nil {
		for _, c := range children {
			frames = append(frames, c.Path)
		}
	}
	for _, f := range frames {
		_ = t.eval(ctx, installer, []any{cfg}, "page", f, nil)
	}
	return nil
}

// ─── Navigation ───────────────────────────────────────────────────────

// Goto navigates and waits for waitUntil (default "load"), reloading when
// requested equals the current address.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1517-1660
func (t *Tab) Goto(ctx context.Context, rawURL string, waitUntil string, timeout time.Duration) error {
	if waitUntil == "" {
		waitUntil = "load"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	label := fmt.Sprintf("tab.goto(%s)", jsonQuote(rawURL))
	return t.navigate(ctx, label, func() error {
		if SameAddress(rawURL, t.URL()) {
			return t.opWithTimeout(ctx, "nav", opNavReq{Op: "nav", Block: t.block, Go: "reload"}, nil, timeout)
		}
		return t.opWithTimeout(ctx, "goto", opGotoReq{Op: "goto", Block: t.block, URL: rawURL}, nil, timeout)
	}, waitUntil, timeout, rawURL != blankURL)
}

func (t *Tab) navigate(ctx context.Context, label string, start func() error, waitUntil string, timeout time.Duration, leavesBlank bool) error {
	if err := t.pull(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	since := t.cursor
	startURL := t.url
	t.mu.Unlock()

	if err := start(); err != nil {
		return err
	}
	return t.awaitNavigation(ctx, label, since, waitUntil, timeout, false, startURL, leavesBlank)
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1517-1611
func (t *Tab) awaitNavigation(ctx context.Context, label string, since int, waitUntil string, timeout time.Duration, requireChange bool, startURL string, leavesBlank bool) error {
	iteration := 0
	var idleSince time.Time
	settledByState := false
	sawBlank := false

	err := t.poll(ctx, label, timeout, true, func() (bool, error) {
		iteration++
		all := t.eventsSince(since)
		var events []TernEvent
		for _, ev := range all {
			evURL, _ := ev.Raw["url"].(string)
			blank := leavesBlank && evURL == blankURL && (ev.Type == "committed" || ev.Type == "loaded" || ev.Type == "url")
			if blank {
				sawBlank = true
				continue
			}
			events = append(events, ev)
		}
		for _, ev := range events {
			if ev.Type == "failed" {
				msg, _ := ev.Raw["message"].(string)
				if msg == "" {
					msg = "load failed"
				}
				return false, fmt.Errorf("%s failed: %s", label, msg)
			}
			if ev.Type == "blocked" {
				u, _ := ev.Raw["url"].(string)
				return false, fmt.Errorf("%s was blocked by allowed_domains: %s", label, u)
			}
		}
		committed := slices.ContainsFunc(events, func(e TernEvent) bool { return e.Type == "committed" })
		changed := committed || slices.ContainsFunc(events, func(e TernEvent) bool { return e.Type == "url" })
		loaded := slices.ContainsFunc(events, func(e TernEvent) bool { return e.Type == "loaded" })

		if !loaded && iteration%4 == 0 {
			st, _ := t.refreshState(ctx)
			curURL := t.URL()
			arrived := !(leavesBlank && (curURL == blankURL || (startURL == blankURL && !changed)))
			if st.Loading != nil && !*st.Loading && arrived && (changed || (!requireChange && curURL != startURL)) {
				loaded = true
				settledByState = true
			}
		}
		if requireChange && !changed && !settledByState {
			return false, nil
		}
		if waitUntil == "domcontentloaded" {
			if loaded {
				return true, nil
			}
			if !committed {
				return false, nil
			}
			var ready string
			if err := t.eval(ctx, "function () { return document.readyState; }", nil, "page", "", &ready); err == nil && ready != "loading" && ready != "" {
				return true, nil
			}
			return false, nil
		}
		if !loaded {
			return false, nil
		}
		if waitUntil == "load" {
			return true, nil
		}
		inflight := 0
		if t.nativeNetwork {
			inflight = t.netLog.Inflight()
		} else {
			var n float64
			if err := t.eval(ctx, fmt.Sprintf("function () { const c = globalThis[%q]; return c ? c.inflight : 0; }", TernCaptureGlobal), nil, "page", "", &n); err == nil {
				inflight = int(n)
			}
		}
		maxInflight := 2
		if waitUntil == "networkidle0" {
			maxInflight = 0
		}
		if inflight > maxInflight {
			idleSince = time.Time{}
			return false, nil
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		}
		return time.Since(idleSince) >= navigationIdleDuration, nil
	}, func() string {
		if sawBlank {
			return fmt.Sprintf("%s reported an about:blank load since the goto, not the requested page", t.product)
		}
		return fmt.Sprintf("the page is at %s", t.URL())
	})
	if err != nil {
		return err
	}
	_, _ = t.refreshState(ctx)
	return nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1662-1685
func (t *Tab) historyNav(ctx context.Context, goDir string, waitUntil string) (string, error) {
	if goDir != "reload" {
		st, err := t.refreshState(ctx)
		if err != nil {
			return "", err
		}
		if goDir == "back" && st.Back != nil && !*st.Back {
			return t.URL(), nil
		}
		if goDir == "forward" && st.Forward != nil && !*st.Forward {
			return t.URL(), nil
		}
	}
	if waitUntil == "" {
		waitUntil = "load"
	}
	if err := t.navigate(ctx, fmt.Sprintf("tab.%s()", goDir), func() error {
		return t.op(ctx, "nav", opNavReq{Op: "nav", Block: t.block, Go: goDir}, nil)
	}, waitUntil, 30*time.Second, false); err != nil {
		return "", err
	}
	return t.URL(), nil
}

// SetViewport resizes the PiP page and produces upstream's docked-block explanation on failure.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1760-1792
func (t *Tab) SetViewport(ctx context.Context, vp browser.Viewport) error {
	w := vp.Width
	h := vp.Height
	err := t.op(ctx, "viewport", opViewportReq{Op: "viewport", Block: t.block, Width: w, Height: h}, nil)
	if err != nil {
		return t.viewportFailure(ctx, err, w, h)
	}
	t.mu.Lock()
	t.viewport = vp
	t.pointerSpace.Resized(w, h)
	t.mu.Unlock()
	return nil
}

func (t *Tab) viewportFailure(ctx context.Context, err error, w, h int) error {
	var te *ternwire.Error
	if t.product != "Tern" || !errors.As(err, &te) || te.Kind != ternwire.ErrFailed {
		return err
	}
	bctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	blocks, berr := ListTernBlocks(bctx, t.client)
	cancel()
	if berr != nil {
		return err
	}
	var entry *BlockEntry
	for i := range blocks {
		if blocks[i].Block == t.block {
			entry = &blocks[i]
			break
		}
	}
	if entry == nil || entry.Owner != nil {
		return err
	}
	_, _ = t.refreshState(ctx)
	t.mu.Lock()
	now := fmt.Sprintf("%dx%d", t.viewport.Width, t.viewport.Height)
	t.mu.Unlock()
	how := "omp opened it as a picture-in-picture; it was docked since (by the user, or by Tern when the pane it floated over closed, was parked or moved)"
	if t.adopted != nil {
		how = "It is the user's block, docked in their layout"
	}
	return fmt.Errorf(
		`%s browser block %d of tab %s is docked, so its page takes the block's size (%s now) and cannot be set to %dx%d. %s. For a fixed viewport, open a tab with browser.open({ name, app: "own", url, viewport: { width: %d, height: %d } }), or ask the user to pop the block out as a picture-in-picture, then call emulate({ viewport }) again.`,
		t.product, t.block, jsonQuote(t.name), now, w, h, how, w, h,
	)
}

// ─── Selectors, targets, native DOM reads ─────────────────────────────

type boundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (t *Tab) actionTimeout(explicitMs int) time.Duration {
	if explicitMs > 0 {
		return time.Duration(explicitMs) * time.Millisecond
	}
	t.mu.Lock()
	b := t.actionBudgetMs
	t.mu.Unlock()
	if b > 0 {
		return time.Duration(b) * time.Millisecond
	}
	return 10 * time.Second
}

func (t *Tab) observedRef(id int) (string, error) {
	t.mu.Lock()
	ref, ok := t.observedRefs[id]
	t.mu.Unlock()
	if !ok {
		return "", unknownIDErr(id)
	}
	return ref, nil
}

func (t *Tab) frameOffset(ctx context.Context, frame string) (float64, float64, error) {
	if frame == "" {
		return 0, 0, nil
	}
	parts := strings.Split(frame, ".")
	var x, y float64
	for depth := range parts {
		parent := ""
		if depth > 0 {
			parent = strings.Join(parts[:depth], ".")
		}
		idx, _ := strconv.Atoi(parts[depth])
		var origin *struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		if err := t.kit(ctx, "frameOrigin", []any{idx}, parent, &origin); err != nil {
			return 0, 0, err
		}
		if origin == nil {
			return 0, 0, fmt.Errorf("Frame %s is no longer in the page", frame)
		}
		x += origin.X
		y += origin.Y
	}
	return x, y, nil
}

// target waits until spec is actionable for action, via native DOM resolve or page kit target().
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1825-2051
func (t *Tab) target(ctx context.Context, label string, spec Selector, action string, frame string, explicitTimeoutMs int) (boundingBox, string, *DomNode, error) {
	if t.nativeDom() {
		return t.resolveTargetNative(ctx, label, spec, action, frame, explicitTimeoutMs, false)
	}
	timeout := t.actionTimeout(explicitTimeoutMs)
	started := time.Now()
	delay := 30 * time.Millisecond
	var last struct {
		OK     bool    `json:"ok"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
		Reason string  `json:"reason"`
		Count  int     `json:"count"`
		Detail string  `json:"detail"`
	}
	for {
		last = struct {
			OK     bool    `json:"ok"`
			X      float64 `json:"x"`
			Y      float64 `json:"y"`
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
			Reason string  `json:"reason"`
			Count  int     `json:"count"`
			Detail string  `json:"detail"`
		}{}
		if err := t.kit(ctx, "target", []any{spec, action}, frame, &last); err != nil {
			return boundingBox{}, "", nil, err
		}
		if last.OK {
			ox, oy, err := t.frameOffset(ctx, frame)
			if err != nil {
				return boundingBox{}, "", nil, err
			}
			return boundingBox{X: last.X + ox, Y: last.Y + oy, Width: last.Width, Height: last.Height}, "", nil, nil
		}
		elapsed := time.Since(started)
		if elapsed+delay >= timeout {
			break
		}
		if last.Reason == "missing" && explicitTimeoutMs == 0 && elapsed >= zeroMatchFailFast {
			break
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return boundingBox{}, "", nil, err
		}
		delay = min(250*time.Millisecond, time.Duration(float64(delay)*1.5))
	}
	return boundingBox{}, "", nil, targetTimeout(label, time.Since(started), last.Reason, last.Count, last.Detail)
}

func (t *Tab) queryForNative(ctx context.Context, spec Selector, frame string) (*DomQuery, error) {
	if spec.Engine == "id" {
		ref, err := t.observedRef(spec.ID)
		if err != nil {
			return nil, err
		}
		return &DomQuery{By: "ref", Value: ref}, nil
	}
	if spec.Engine == "ariaRef" {
		ref, err := t.refThroughKit(ctx, spec, frame)
		if err != nil || ref == "" {
			return nil, err
		}
		return &DomQuery{By: "ref", Value: ref}, nil
	}
	q, err := DomQueryFor(spec)
	if err != nil {
		return nil, err
	}
	if frame != "" && q.By != "ref" {
		root, err := t.frameRootNative(ctx, frame)
		if err != nil {
			return nil, err
		}
		q.Root = root
	}
	return &q, nil
}

func (t *Tab) refOfToken(ctx context.Context, token string) (string, error) {
	var m DomMatches
	err := t.dom(ctx, "query", opDomQueryReq{
		Op: "dom", Block: t.block, Read: "query",
		By: "attribute", Attribute: HandleAttribute, Value: token, Limit: 1,
	}, &m, 10*time.Second)
	if err != nil || len(m.Nodes) == 0 {
		return "", err
	}
	return m.Nodes[0].Ref, nil
}

func (t *Tab) refThroughKit(ctx context.Context, spec Selector, frame string) (string, error) {
	var token string
	if err := t.kit(ctx, "mark", []any{spec}, frame, &token); err != nil || token == "" {
		return "", err
	}
	defer func() { _ = t.kit(context.Background(), "unmark", []any{token}, frame, nil) }()
	return t.refOfToken(ctx, token)
}

func (t *Tab) frameRootNative(ctx context.Context, frame string) (string, error) {
	t.mu.Lock()
	known := t.frameRoots[frame]
	t.mu.Unlock()
	if known != "" {
		var m DomMatches
		if err := t.dom(ctx, "query", opDomQueryReq{Op: "dom", Block: t.block, Read: "query", By: "ref", Value: known, Limit: 1}, &m, 10*time.Second); err == nil && len(m.Nodes) > 0 {
			return known, nil
		}
		t.mu.Lock()
		delete(t.frameRoots, frame)
		t.mu.Unlock()
	}
	cut := strings.LastIndex(frame, ".")
	parent := ""
	idx := frame
	if cut >= 0 {
		parent = frame[:cut]
		idx = frame[cut+1:]
	}
	var tokens []string
	if err := t.kit(ctx, "markAll", []any{Selector{Engine: "pierce", Query: "iframe,frame"}}, parent, &tokens); err != nil {
		return "", err
	}
	defer func() {
		if len(tokens) > 0 {
			_ = t.kit(context.Background(), "unmark", []any{tokens}, parent, nil)
		}
	}()
	for _, tok := range tokens {
		var hosted string
		if err := t.kit(ctx, "frameOf", []any{Selector{Engine: "handle", Token: tok}}, parent, &hosted); err == nil && hosted == idx {
			ref, err := t.refOfToken(ctx, tok)
			if err != nil {
				return "", err
			}
			if ref != "" {
				t.mu.Lock()
				t.frameRoots[frame] = ref
				t.mu.Unlock()
				return ref, nil
			}
		}
	}
	return "", fmt.Errorf("Frame %s is no longer in the page", frame)
}

func (t *Tab) queryNative(ctx context.Context, spec Selector, frame string, limit int, include []string, visibleOnly bool) (DomMatches, error) {
	q, err := t.queryForNative(ctx, spec, frame)
	if err != nil {
		return DomMatches{}, err
	}
	if q == nil {
		return DomMatches{}, nil
	}
	var m DomMatches
	err = t.dom(ctx, "query", opDomQueryReq{
		Op: "dom", Block: t.block, Read: "query",
		By: q.By, Value: q.Value, Name: q.Name, Attribute: q.Attribute, Exact: q.Exact,
		Root: q.Root, Limit: limit, Include: include, VisibleOnly: visibleOnly,
	}, &m, 10*time.Second)
	if err != nil {
		return DomMatches{}, err
	}
	if spec.Engine == "id" && len(m.Nodes) == 0 {
		return DomMatches{}, staleIDErr(spec.ID)
	}
	return m, nil
}

func (t *Tab) locateNative(ctx context.Context, spec Selector, frame string) (ref string, count int, found bool, err error) {
	if spec.Engine == "ref" {
		return spec.Ref, 1, true, nil
	}
	if spec.Engine == "id" {
		r, err := t.observedRef(spec.ID)
		if err != nil {
			return "", 0, false, err
		}
		return r, 1, true, nil
	}
	m, err := t.queryNative(ctx, spec, frame, 1, nil, false)
	if err != nil {
		return "", 0, false, err
	}
	if len(m.Nodes) == 0 || m.Nodes[0].Ref == "" {
		return "", 0, false, nil
	}
	return m.Nodes[0].Ref, m.Total, true, nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:1967-2051
func (t *Tab) resolveTargetNative(ctx context.Context, label string, spec Selector, action string, frame string, explicitTimeoutMs int, requireEnabled bool) (boundingBox, string, *DomNode, error) {
	timeout := t.actionTimeout(explicitTimeoutMs)
	resAction := resolveActions[action]
	if resAction == "" {
		resAction = "none"
	}
	started := time.Now()
	delay := 30 * time.Millisecond
	wheels := 0
	recentred := false
	var lastReason, lastDetail string
	var lastCount int

	for {
		ref, count, found, err := t.locateNative(ctx, spec, frame)
		if err != nil {
			return boundingBox{}, "", nil, err
		}
		if !found {
			lastReason, lastCount, lastDetail = "missing", 0, ""
		} else {
			stable := 0
			if resAction != "none" || wheels > 0 {
				stable = resolveStableUpdates
			}
			remMs := max(1, min(resolveSettleMs, int((timeout-time.Since(started)).Milliseconds())))
			var resolved DomResolved
			err := t.dom(ctx, "resolve", opDomResolveReq{
				Op: "dom", Block: t.block, Read: "resolve",
				Ref: ref, Action: resAction, Stable: new(stable), Timeout: remMs,
			}, &resolved, timeout)
			if err != nil {
				return boundingBox{}, "", nil, err
			}
			if resolved.State == "ok" {
				if resolved.Point == nil || resolved.Node == nil {
					return boundingBox{}, "", nil, &ternwire.Error{
						Kind:    ternwire.ErrProtocol,
						Message: fmt.Sprintf("%s answered dom resolve `ok` without a point and node", t.product),
					}
				}
				if !requireEnabled || !slices.Contains(resolved.Node.States, "disabled") {
					return boundingBox{
						X:      resolved.Point[0],
						Y:      resolved.Point[1],
						Width:  resolved.Node.Rect[2],
						Height: resolved.Node.Rect[3],
					}, ref, resolved.Node, nil
				}
				lastReason, lastCount, lastDetail = "disabled", count, ""
			} else if resolved.Scroll != nil && ((resolved.State == "offViewport" && wheels < scrollWheelLimit) || (resolved.State == "covered" && !recentred)) {
				if resolved.State == "covered" {
					recentred = true
				}
				wheels++
				sc := resolved.Scroll
				if err := t.input(ctx, []InputStep{{
					Type: "wheel", X: sc.At[0], Y: sc.At[1], DX: sc.DX, DY: sc.DY, Mods: []Modifier{},
				}}); err != nil {
					return boundingBox{}, "", nil, err
				}
				continue
			} else if resolved.State == "detached" {
				if spec.Engine == "id" {
					return boundingBox{}, "", nil, staleIDErr(spec.ID)
				}
				lastReason, lastCount, lastDetail = "missing", 0, ""
				if spec.Engine == "ref" {
					break
				}
			} else if resolved.State == "covered" {
				detail := ""
				if resolved.CoveredBy != nil {
					detail = DescribeDomNode(*resolved.CoveredBy)
				}
				lastReason, lastCount, lastDetail = "covered", count, detail
			} else {
				lastReason, lastCount, lastDetail = resolved.State, count, ""
			}
		}
		elapsed := time.Since(started)
		if elapsed+delay >= timeout {
			break
		}
		if lastReason == "missing" && explicitTimeoutMs == 0 && elapsed >= zeroMatchFailFast {
			break
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return boundingBox{}, "", nil, err
		}
		delay = min(250*time.Millisecond, time.Duration(float64(delay)*1.5))
	}
	return boundingBox{}, "", nil, targetTimeout(label, time.Since(started), lastReason, lastCount, lastDetail)
}

// markRef stamps a kit handle token on a native ref for kit-only helpers (select, styles, etc.).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2094-2124
func (t *Tab) markRef(ctx context.Context, ref string, frame string) (string, string, error) {
	describeRef := func() (*DomNode, error) {
		var m DomMatches
		err := t.dom(ctx, "query", opDomQueryReq{
			Op: "dom", Block: t.block, Read: "query",
			By: "ref", Value: ref, Include: []string{"attributes"},
		}, &m, 10*time.Second)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		return &m.Nodes[0], nil
	}
	node, err := describeRef()
	if err != nil {
		return "", "", err
	}
	if node == nil {
		return "", "", fmt.Errorf("Element handle selector no longer resolves")
	}
	candidates := Selector{Engine: "pierce", Query: DomCandidateSelector(*node)}

	markIn := func(p string) (string, error) {
		var tokens []string
		if err := t.kit(ctx, "markAll", []any{candidates}, p, &tokens); err != nil || len(tokens) == 0 {
			return "", err
		}
		var own string
		n2, _ := describeRef()
		var carried []string
		if n2 != nil {
			carried = strings.Fields(n2.Attributes[HandleAttribute])
		}
		for _, tok := range tokens {
			if slices.Contains(carried, tok) {
				own = tok
				break
			}
		}
		var rest []string
		for _, tok := range tokens {
			if tok != own {
				rest = append(rest, tok)
			}
		}
		if len(rest) > 0 {
			_ = t.kit(ctx, "unmark", []any{rest}, p, nil)
		}
		return own, nil
	}

	if own, err := markIn(frame); err == nil && own != "" {
		return own, frame, nil
	}
	if frame == "" {
		var children []struct {
			Path string `json:"path"`
		}
		if err := t.kit(ctx, "frames", nil, "", &children); err == nil {
			for _, child := range children {
				if own, err := markIn(child.Path); err == nil && own != "" {
					return own, child.Path, nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("This helper runs in the page, and page script cannot reach the element (it sits in a closed shadow root or in a frame of another site)")
}

func (t *Tab) onKit(ctx context.Context, spec Selector, frame string, fn func(kitSpec Selector, kitFrame string) error) error {
	if !t.nativeDom() || (spec.Engine != "ref" && spec.Engine != "id") {
		return fn(spec, frame)
	}
	ref := spec.Ref
	if spec.Engine == "id" {
		var err error
		ref, err = t.observedRef(spec.ID)
		if err != nil {
			return err
		}
	}
	tok, kitFrame, err := t.markRef(ctx, ref, frame)
	if err != nil {
		return err
	}
	defer func() { _ = t.kit(context.Background(), "unmark", []any{tok}, kitFrame, nil) }()
	return fn(Selector{Engine: "handle", Token: tok}, kitFrame)
}

// ─── Mouse & keyboard helpers ─────────────────────────────────────────

func (t *Tab) validateButton(name string) (string, error) {
	if name == "" {
		return "left", nil
	}
	switch name {
	case "left", "right", "middle":
		return name, nil
	case "back", "forward":
		return "", unsupportedErr(t.product, "mouseDown", fmt.Sprintf("the %s mouse button cannot be synthesised (left, right, middle only)", name))
	default:
		return "", fmt.Errorf("unknown mouse button %q", name)
	}
}

func (t *Tab) mouseStep(action string, x, y float64, button string, clicks *int) InputStep {
	t.mu.Lock()
	mods := append([]Modifier{}, t.heldMods...)
	t.mu.Unlock()
	return InputStep{
		Type:   "mouse",
		Action: action,
		X:      x,
		Y:      y,
		Button: button,
		Clicks: clicks,
		Mods:   mods,
	}
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2202-2209
func (t *Tab) clickAtPoint(ctx context.Context, x, y float64, button string, count int) error {
	if count < 1 {
		count = 1
	}
	steps := make([]InputStep, 0, 1+count*2)
	steps = append(steps, t.mouseStep("move", x, y, button, new(0)))
	for c := 1; c <= count; c++ {
		steps = append(steps,
			t.mouseStep("down", x, y, button, new(c)),
			t.mouseStep("up", x, y, button, new(c)),
		)
	}
	t.mu.Lock()
	t.pointer = &[2]float64{x, y}
	t.mu.Unlock()
	return t.input(ctx, steps)
}

func (t *Tab) viewportCentre(ctx context.Context) (float64, float64, error) {
	if t.nativeDom() {
		var snap DomSnapshot
		if err := t.dom(ctx, "snapshot", opDomSnapshotReq{Op: "dom", Block: t.block, Read: "snapshot", Limit: 1}, &snap, 10*time.Second); err == nil && snap.Viewport.Width > 0 {
			return float64(snap.Viewport.Width) / 2, float64(snap.Viewport.Height) / 2, nil
		}
	}
	var geo struct {
		InnerWidth  float64 `json:"innerWidth"`
		InnerHeight float64 `json:"innerHeight"`
	}
	if err := t.kit(ctx, "geometry", nil, "", &geo); err == nil && geo.InnerWidth > 0 {
		return geo.InnerWidth / 2, geo.InnerHeight / 2, nil
	}
	t.mu.Lock()
	w, h := t.viewport.Width, t.viewport.Height
	t.mu.Unlock()
	if w <= 0 {
		w = defaultViewportW
	}
	if h <= 0 {
		h = defaultViewportH
	}
	return float64(w) / 2, float64(h) / 2, nil
}

// ─── browser.Tab interface methods ────────────────────────────────────

// Info returns the tab's current state snapshot with omp's description & notes.
// upstream: packages/coding-agent/src/tools/browser/browser.ts:1006-1064
func (t *Tab) Info() browser.TabInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	desc, notes := ternOpenDescriptionAndNotes(t.pane, t.block, t.adopted, t.url, t.title, t.shown, t.requestedVP, t.viewport)
	return browser.TabInfo{
		URL:         t.url,
		Title:       t.title,
		Viewport:    t.viewport,
		TargetID:    strconv.Itoa(t.block),
		Description: desc,
		Notes:       notes,
		Adopted:     t.adopted != nil,
	}
}

// Reuse applies open-time updates when an existing named tab is re-opened.
func (t *Tab) Reuse(ctx context.Context, opts browser.OpenOptions) (string, error) {
	if opts.Viewport != nil && opts.Viewport.Width > 0 && opts.Viewport.Height > 0 {
		if err := t.SetViewport(ctx, *opts.Viewport); err != nil {
			return "", err
		}
	}
	if opts.Dialogs != "" {
		if err := t.op(ctx, "dialogs", opDialogsReq{Op: "dialogs", Block: t.block, Policy: opts.Dialogs}, nil); err != nil {
			return "", err
		}
	}
	if opts.URL != "" {
		timeout := 30 * time.Second
		if opts.TimeoutMs > 0 {
			timeout = time.Duration(opts.TimeoutMs) * time.Millisecond
		}
		if err := t.Goto(ctx, opts.URL, opts.WaitUntil, timeout); err != nil {
			return "", err
		}
	}
	return "", nil
}

// Alive reports whether the tab is open, not latched gone (S8), and its client is connected.
func (t *Tab) Alive() bool {
	t.mu.Lock()
	closed := t.closed
	gone := t.goneErr != nil
	t.mu.Unlock()
	return !closed && !gone && t.client != nil && t.client.Connected()
}

// Run refuses arbitrary Puppeteer JS execution on the Go Tern backend.
func (t *Tab) Run(_ context.Context, _ browser.CallContext, _ string, _ string, _ []json.RawMessage) (*browser.CallResult, error) {
	return nil, unsupportedErr(t.product, "run", "oh-my-pig has no Puppeteer/Node runtime; use tab.evaluate() or direct tab helpers instead")
}

// Close closes the PiP (or releases an adopted block) using a detached context
// with a 5s timeout, marking closed only on success / not_found / closed, and
// clearing scripts when an older Tern answers `invalid` for `release` (fixes S5).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:742-760
func (t *Tab) Close(_ context.Context, _ bool) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	adopted := t.adopted != nil
	block := t.block
	t.mu.Unlock()

	detachCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opName := "close"
	if adopted {
		opName = "release"
	}
	err := t.client.RequestWithOptions(detachCtx, "browser", opBlockOnlyReq{
		Op:    opName,
		Block: block,
	}, nil, ternwire.RequestOptions{Timeout: 5 * time.Second})

	if err != nil {
		var te *ternwire.Error
		if errors.As(err, &te) {
			if te.Kind == ternwire.ErrNotFound || te.Kind == ternwire.ErrClosed {
				err = nil
			} else if adopted && te.Kind == ternwire.ErrInvalid {
				// Older Tern without `release`: clear omp's scripts best-effort.
				_ = t.client.RequestWithOptions(detachCtx, "browser", opScriptsReq{
					Op:      "scripts",
					Block:   block,
					Scripts: []scriptSpec{},
				}, nil, ternwire.RequestOptions{Timeout: 5 * time.Second})
				err = nil
			}
		}
	}
	if err != nil {
		return err
	}

	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()

	if t.backend != nil {
		t.backend.mu.Lock()
		if t.backend.tabs[t.name] == t {
			delete(t.backend.tabs, t.name)
		}
		if t.backend.taken[block] == t.name {
			delete(t.backend.taken, block)
		}
		t.backend.mu.Unlock()
	}
	return nil
}

// ─── Helper methods invoked from Call ─────────────────────────────────

func (t *Tab) enableDownloads(ctx context.Context, dir string) error {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("omp-downloads-tern-%d", t.block))
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	t.mu.Lock()
	already := t.downloadDir == abs
	t.mu.Unlock()
	if already {
		return nil
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if err := t.op(ctx, "downloads", opDownloadsReq{Op: "downloads", Block: t.block, Dir: abs}, nil); err != nil {
		return err
	}
	t.mu.Lock()
	t.downloadDir = abs
	t.mu.Unlock()
	return nil
}

// cookiesForURLs filters the daemon cookie store to urls (defaulting to current page URL) (fixes S2).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3422-3442
func (t *Tab) allCookies(ctx context.Context) ([]BrowserCookie, error) {
	var ans struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := t.op(ctx, "cookies", opBlockOnlyReq{Op: "cookies", Block: t.block}, &ans); err != nil {
		return nil, err
	}
	out := make([]BrowserCookie, 0, len(ans.Cookies))
	for _, raw := range ans.Cookies {
		if c, ok := cookieFromTern(raw); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (t *Tab) cookiesForURLs(ctx context.Context, rawURLs []string) ([]BrowserCookie, error) {
	if len(rawURLs) == 0 {
		rawURLs = []string{t.URL()}
	}
	parsed := make([]*url.URL, 0, len(rawURLs))
	for _, r := range rawURLs {
		u, err := url.Parse(r)
		if err != nil || u.Scheme == "" {
			return nil, fmt.Errorf("tab.cookies() received an invalid URL %s", jsonQuote(r))
		}
		parsed = append(parsed, u)
	}
	all, err := t.allCookies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BrowserCookie, 0, len(all))
	for _, c := range all {
		for _, u := range parsed {
			if cookieMatchesURL(c, u) {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// setCookies sends each cookie wrapped in `{cookie:{...}}` via opSetCookieReq (fixes S1 & A/setCookie).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3445-3477
func (t *Tab) setCookies(ctx context.Context, inputs []map[string]any) error {
	for _, raw := range inputs {
		name, _ := raw["name"].(string)
		val, _ := raw["value"].(string)
		if name == "" {
			return fmt.Errorf("tab.setCookies() received malformed cookie input")
		}
		domain, _ := raw["domain"].(string)
		cPath, _ := raw["path"].(string)
		rawURL, _ := raw["url"].(string)
		secure, hasSecure := raw["secure"].(bool)
		httpOnly, _ := raw["httpOnly"].(bool)

		base := rawURL
		if base == "" && domain == "" {
			base = t.URL()
		}
		if base != "" {
			u, err := url.Parse(base)
			if err != nil || u.Hostname() == "" {
				return fmt.Errorf("tab.setCookies() needs a domain or url for cookie %s", jsonQuote(name))
			}
			if domain == "" {
				domain = u.Hostname()
			}
			if cPath == "" {
				cPath = "/"
			}
			if !hasSecure {
				secure = strings.EqualFold(u.Scheme, "https")
			}
		}
		if domain == "" {
			return fmt.Errorf("tab.setCookies() needs a domain or url for cookie %s", jsonQuote(name))
		}
		if cPath == "" {
			cPath = "/"
		}
		var expires *float64
		if f, ok := numVal(raw["expires"]); ok && f >= 0 {
			expires = new(f)
		}
		var sameSite *string
		if ss, ok := raw["sameSite"].(string); ok && (ss == "Strict" || ss == "Lax" || ss == "None") {
			sameSite = new(ss)
		}
		if err := t.op(ctx, "setCookie", opSetCookieReq{
			Op:    "setCookie",
			Block: t.block,
			Cookie: ternCookiePayload{
				Name:     name,
				Value:    val,
				Domain:   domain,
				Path:     cPath,
				Expires:  expires,
				HTTPOnly: httpOnly,
				Secure:   secure,
				SameSite: sameSite,
			},
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// clearCookies deletes only cookies matching the current page URL (and optional names) (fixes S2).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3480-3489
func (t *Tab) clearCookies(ctx context.Context, names []string) error {
	cookies, err := t.cookiesForURLs(ctx, nil)
	if err != nil {
		return err
	}
	var wanted map[string]bool
	if len(names) > 0 {
		wanted = make(map[string]bool, len(names))
		for _, n := range names {
			wanted[n] = true
		}
	}
	for _, c := range cookies {
		if wanted != nil && !wanted[c.Name] {
			continue
		}
		if err := t.op(ctx, "deleteCookie", opDeleteCookieReq{
			Op:     "deleteCookie",
			Block:  t.block,
			Name:   c.Name,
			Domain: c.Domain,
			Path:   c.Path,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// captureBytes captures PNG/JPEG bytes using upstream `{scale, format, quality?, rect?, full?}` (fixes A/capture).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3021-3058
func (t *Tab) captureBytes(ctx context.Context, sel *Selector, fullPage bool, format string, quality *int, scale float64, frame string) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	if scale <= 0 {
		t.mu.Lock()
		scale = t.viewport.DeviceScaleFactor
		t.mu.Unlock()
		if scale <= 0 {
			scale = 1
		}
	}
	req := opCaptureReq{
		Op:     "capture",
		Block:  t.block,
		Scale:  scale,
		Format: format,
	}
	if format == "jpeg" {
		q := 80
		if quality != nil {
			q = *quality
		}
		req.Quality = new(q)
	}
	if sel != nil {
		if _, _, _, err := t.target(ctx, fmt.Sprintf("tab.screenshot(%s)", jsonQuote(sel.Query)), *sel, "point", frame, 0); err != nil {
			return nil, err
		}
		box, err := t.readBox(ctx, *sel, frame)
		if err != nil {
			return nil, err
		}
		if box == nil {
			return nil, fmt.Errorf("Screenshot selector did not resolve to a visible element")
		}
		req.Rect = &captureRect{X: box.X, Y: box.Y, Width: box.Width, Height: box.Height}
	} else if fullPage {
		req.Full = true
	}
	if t.product != "WebKit" {
		_ = t.eval(ctx, "function () { return new Promise(resolve => requestAnimationFrame(() => resolve(true))); }", nil, "isolated", "", nil)
	}
	var ans struct {
		Data string `json:"data"`
	}
	if err := t.op(ctx, "capture", req, &ans); err != nil {
		return nil, err
	}
	if ans.Data == "" {
		return nil, fmt.Errorf("%s capture answered without image data", t.product)
	}
	return base64.StdEncoding.DecodeString(ans.Data)
}

func (t *Tab) readBox(ctx context.Context, spec Selector, frame string) (*boundingBox, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, nil, false)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		r := m.Nodes[0].Rect
		if r[2] <= 0 || r[3] <= 0 {
			return nil, nil
		}
		return &boundingBox{X: r[0], Y: r[1], Width: r[2], Height: r[3]}, nil
	}
	var box *boundingBox
	if err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "box"}, kf, &box)
	}); err != nil {
		return nil, err
	}
	if box != nil && frame != "" {
		ox, oy, err := t.frameOffset(ctx, frame)
		if err != nil {
			return nil, err
		}
		box.X += ox
		box.Y += oy
	}
	return box, nil
}
