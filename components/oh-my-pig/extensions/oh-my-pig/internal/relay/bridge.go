package relay

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// upstream: packages/coding-agent/src/tools/browser/relay/bridge.ts:1-2852

// RelaySocket is the transport-agnostic WebSocket surface RelayBridge writes to.
// upstream: bridge.ts:55-58
type RelaySocket interface {
	Send(text string)
	Close()
}

// FrontApp represents the app in front of the user when input reaches a tab.
// upstream: bridge.ts:480-483
type FrontApp interface {
	KeepInFront(duration time.Duration) (int, error)
}

// PanelSources supplies local state for side-panel calls.
// Collab rooms do not exist in PiG/oh-my-pig, so the default implementation
// returns an empty host list and refuses collabLink explicitly; installs reads
// from ReadRelayInstalls.
// upstream: bridge.ts:153-167
type PanelSources struct {
	ListHosts   func() ([]PanelCollabHost, error)
	ControlLink func(instanceID string) (string, error)
	Installs    func() ([]PanelInstall, error)
}

// GroupConfig configures Chrome tab grouping for claimed tabs.
type GroupConfig struct {
	Title string
	Color string
}

// DefaultGroupConfig is omp's default tab group appearance.
// upstream: server.ts:79
var DefaultGroupConfig = GroupConfig{Title: "omp", Color: "cyan"}

// BridgeOptions configures NewRelayBridge.
// upstream: bridge.ts:551-563
type BridgeOptions struct {
	Log             func(message string, data map[string]any)
	Group           *GroupConfig
	UpdateExtension func(extensionID string) (bool, error)
	InstalledStamp  func(extensionID string) (*RelayExtensionStamp, error)
	PanelSources    *PanelSources
	// Clock overrides time.Now when non-nil (for deterministic tests).
	Clock func() time.Time
	// Timeouts override defaults when > 0 (for deterministic tests).
	RPCTimeout          time.Duration
	PageBoundRPCTimeout time.Duration
	LateRPCKeep         time.Duration
	OverlayRPCTimeout   time.Duration
	OverlayHideWait     time.Duration
	AffineReconnectWait time.Duration
	ChromeDetachReport  time.Duration
}

const (
	defaultRPCTimeout          = 20 * time.Second
	defaultPageBoundRPCTimeout = 306 * time.Second // BROWSER_PAGE_PROTOCOL_TIMEOUT_MS (301s) + 5s, bridge.ts:373
	defaultLateRPCKeep         = 60 * time.Second
	defaultAffineReconnectWait = 12 * time.Second
	defaultChromeDetachReport  = 1 * time.Second
	defaultOverlayRPCTimeout   = 5 * time.Second
	defaultOverlayHideWait     = 300 * time.Millisecond
	popupAfterInput            = 1 * time.Second
	keepFrontDuration          = 500 * time.Millisecond

	cdpErrorMethodNotFound = -32601
	cdpErrorServer         = -32000

	maxHiddenFrames     = 16
	hiddenFrameFieldMax = 100
)

var (
	ineligibleURLRE        = regexp.MustCompile(`(?i)^(chrome|devtools|edge|view-source|chrome-extension|chrome-untrusted|chrome-search):`)
	targetIDRE             = regexp.MustCompile(`^(TAB|PAGE)([^.]+)\.(\d+)$`)
	detachedCommandErrorRE = regexp.MustCompile(`Detached while handling command`)
)

// tabWideSwitches are CDP `{ enabled }` switches Chrome keeps per debugger session.
// upstream: bridge.ts:82
var tabWideSwitches = []string{
	"Page.setInterceptFileChooserDialog",
	"Emulation.setFocusEmulationEnabled",
}

func isTabWideSwitch(method string) bool {
	return slices.Contains(tabWideSwitches, method)
}

type runtimeState string

const (
	runtimeDefault  runtimeState = "default"
	runtimeEnabled  runtimeState = "enabled"
	runtimeDisabled runtimeState = "disabled"
)

type cdpCommand struct {
	ID        int            `json:"id"`
	Method    string         `json:"method"`
	Params    map[string]any `json:"params,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type sessionRef struct {
	kind            string // "tab" or "page"
	tabKey          string
	tabID           int
	runtimeState    runtimeState
	runtimeContexts map[int]struct{}
	runtimeEnabling *promise[struct{}]
	runtimeEpoch    int
	autoAttach      bool
	switches        map[string]struct{}
}

type targetInfo struct {
	TargetID        string `json:"targetId"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	URL             string `json:"url"`
	Attached        bool   `json:"attached"`
	CanAccessOpener bool   `json:"canAccessOpener"`
}

type cdpConnection struct {
	id               int
	socket           RelaySocket
	discover         bool
	autoAttach       bool
	sessions         map[string]*sessionRef
	sessionOrder     []string // insertion order so childTargetSessionForTab is deterministic
	claims           map[string]struct{}
	childParents     map[string]string
	explicitChildren map[string]struct{}
}

func newCdpConnection(id int, socket RelaySocket) *cdpConnection {
	return &cdpConnection{
		id:               id,
		socket:           socket,
		sessions:         make(map[string]*sessionRef),
		claims:           make(map[string]struct{}),
		childParents:     make(map[string]string),
		explicitChildren: make(map[string]struct{}),
	}
}

func (c *cdpConnection) addSession(id string, ref *sessionRef) {
	if _, exists := c.sessions[id]; !exists {
		c.sessionOrder = append(c.sessionOrder, id)
	}
	c.sessions[id] = ref
}

func (c *cdpConnection) deleteSession(id string) {
	delete(c.sessions, id)
	c.sessionOrder = slices.DeleteFunc(c.sessionOrder, func(s string) bool { return s == id })
}

func (c *cdpConnection) sessionsForTab(tabKey, kind string) []string {
	var out []string
	for _, id := range c.sessionOrder {
		ref := c.sessions[id]
		if ref != nil && ref.tabKey == tabKey && (kind == "" || ref.kind == kind) {
			out = append(out, id)
		}
	}
	return out
}

func (c *cdpConnection) childTargetSessionForTab(tabKey string) string {
	for _, id := range c.sessionOrder {
		ref := c.sessions[id]
		if ref != nil && ref.tabKey == tabKey && ref.kind == "page" && ref.autoAttach {
			return id
		}
	}
	return ""
}

type extReplacedError struct{}

func (extReplacedError) Error() string { return "extension socket replaced" }

type rpcTimeoutError struct{ msg string }

func (e rpcTimeoutError) Error() string { return e.msg }

type promise[T any] struct {
	done           chan struct{}
	val            T
	err            error
	once           sync.Once
	mu             sync.Mutex
	settled        bool
	after          func(T, error)
	afterRan       bool
	onSettleLocked func(T, error)
}

func (p *promise[T]) setAfter(fn func(T, error)) {
	p.mu.Lock()
	p.after = fn
	if p.settled && !p.afterRan && fn != nil {
		p.afterRan = true
		v, err := p.val, p.err
		p.mu.Unlock()
		fn(v, err)
		return
	}
	p.mu.Unlock()
}

func (p *promise[T]) runAfterIfReady() {
	p.mu.Lock()
	if p.after == nil || p.afterRan {
		p.mu.Unlock()
		return
	}
	p.afterRan = true
	fn, v, err := p.after, p.val, p.err
	p.mu.Unlock()
	fn(v, err)
}

func newPromise[T any]() *promise[T] {
	return &promise[T]{done: make(chan struct{})}
}

func (p *promise[T]) resolve(v T) {
	p.once.Do(func() {
		p.mu.Lock()
		p.val = v
		p.settled = true
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *promise[T]) reject(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		p.settled = true
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *promise[T]) wait() (T, error) {
	<-p.done
	return p.val, p.err
}

type extHelloInfo struct {
	userAgent             string
	browserVersion        string
	discardedTabsProtocol int
	ompVersion            *string
	ompBuild              *string
	reloadProtocol        int
	downloadsProtocol     int
	overlayProtocol       int
	panelProtocol         int
}

type extInstance struct {
	instanceID  string
	code        string
	socket      RelaySocket
	info        *extHelloInfo
	extensionID string
	updating    bool
	busy        bool
	goneSince   time.Time
	reconnected chan struct{}
}

type realSessionEntry struct {
	parent string
	event  map[string]any
}

type frontSnapshot struct {
	app FrontApp
	at  time.Time
}

type tabState struct {
	instanceID              string
	extCode                 string
	tabID                   int
	tabKey                  string
	url                     string
	title                   string
	active                  bool
	discarded               bool
	windowID                int
	pinned                  bool
	groupID                 int
	attached                bool
	refusal                 string
	announced               bool
	attaching               *promise[bool]
	attachCancelled         bool
	attachGeneration        int
	cancelAttach            func()
	retryAttachAfterDiscard bool
	detaching               *promise[struct{}]
	reattachedAfterDetach   bool
	closing                 bool
	unsettledSwitches       map[string]struct{}
	grouped                 bool
	grouping                bool
	ompGroupID              *int
	groupOptOut             bool
	frontAtInput            *frontSnapshot
	realSessions            map[string]*realSessionEntry
	realSessionOrder        []string
	runtimeContexts         map[int]map[string]any
	runtimeContextOrder     []int
	rootRuntimeEnabled      bool
	rootRuntimeEnabling     *promise[struct{}]
	runtimeGeneration       int
	chromeDetach            chan struct{}
	hiddenFrames            []HiddenFrame
	overlayMounted          bool
	overlayQueue            chan struct{} // closed when latest overlay RPC finishes
	overlayCaptures         int
	screencasts             map[string]struct{}
	overlayHidden           chan struct{}
}

func newTabState(instanceID, extCode string, tabID int, snap TabSnapshot) *tabState {
	closedCh := make(chan struct{})
	close(closedCh)
	return &tabState{
		instanceID:        instanceID,
		extCode:           extCode,
		tabID:             tabID,
		tabKey:            tabKeyOf(extCode, tabID),
		url:               snap.URL,
		title:             snap.Title,
		active:            snap.Active,
		discarded:         snap.Discarded,
		windowID:          snap.WindowID,
		pinned:            snap.Pinned,
		groupID:           snap.GroupID,
		unsettledSwitches: make(map[string]struct{}),
		realSessions:      make(map[string]*realSessionEntry),
		runtimeContexts:   make(map[int]map[string]any),
		chromeDetach:      make(chan struct{}),
		overlayQueue:      closedCh,
		overlayHidden:     closedCh,
		screencasts:       make(map[string]struct{}),
	}
}

func (t *tabState) update(snap TabSnapshot) {
	t.url = snap.URL
	t.title = snap.Title
	t.active = snap.Active
	t.discarded = snap.Discarded
	t.windowID = snap.WindowID
	t.pinned = snap.Pinned
	t.groupID = snap.GroupID
}

func (t *tabState) setRealSession(child, parent string, event map[string]any) {
	if _, exists := t.realSessions[child]; !exists {
		t.realSessionOrder = append(t.realSessionOrder, child)
	}
	t.realSessions[child] = &realSessionEntry{parent: parent, event: event}
}

func (t *tabState) deleteRealSession(child string) {
	delete(t.realSessions, child)
	t.realSessionOrder = slices.DeleteFunc(t.realSessionOrder, func(s string) bool { return s == child })
}

func (t *tabState) setRuntimeContext(id int, params map[string]any) {
	if _, exists := t.runtimeContexts[id]; !exists {
		t.runtimeContextOrder = append(t.runtimeContextOrder, id)
	}
	t.runtimeContexts[id] = params
}

func (t *tabState) deleteRuntimeContext(id int) {
	delete(t.runtimeContexts, id)
	t.runtimeContextOrder = slices.DeleteFunc(t.runtimeContextOrder, func(x int) bool { return x == id })
}

func (t *tabState) clearRuntimeContexts() {
	clear(t.runtimeContexts)
	t.runtimeContextOrder = nil
}

// InstanceCode derives the deterministic 8-char code embedded in relay target IDs.
// upstream: bridge.ts:217-220
func InstanceCode(instanceID, browserSession string) string {
	identity := instanceID
	if browserSession != "" {
		identity = instanceID + "/" + browserSession
	}
	sum := sha256.Sum256([]byte(identity))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:8]
}

func tabKeyOf(extCode string, tabID int) string {
	return fmt.Sprintf("%s:%d", extCode, tabID)
}

func tabTargetIDFromKey(key string) string {
	return "TAB" + strings.Replace(key, ":", ".", 1)
}

func pageTargetIDFromKey(key string) string {
	return "PAGE" + strings.Replace(key, ":", ".", 1)
}

func parseTargetID(targetID string) (key string, kind string, ok bool) {
	m := targetIDRE.FindStringSubmatch(targetID)
	if len(m) != 4 {
		return "", "", false
	}
	k := "page"
	if m[1] == "TAB" {
		k = "tab"
	}
	return m[2] + ":" + m[3], k, true
}

func canOpenWindow(method string, params map[string]any) bool {
	switch method {
	case "Input.dispatchMouseEvent", "Input.emulateTouchFromMouseEvent":
		t, _ := params["type"].(string)
		return t == "mousePressed" || t == "mouseReleased"
	case "Input.dispatchKeyEvent":
		t, _ := params["type"].(string)
		return t == "keyDown" || t == "rawKeyDown"
	case "Input.dispatchTouchEvent":
		t, _ := params["type"].(string)
		return t == "touchStart" || t == "touchEnd"
	case "Input.synthesizeTapGesture":
		return true
	default:
		return false
	}
}

func isOverlayButton(s string) bool {
	switch s {
	case "left", "middle", "right", "back", "forward", "none":
		return true
	}
	return false
}

// overlayInputOf maps a root-session CDP input command to an OverlayInput or nil.
// upstream: bridge.ts:431-444
func overlayInputOf(method string, params map[string]any) *OverlayInput {
	if method == "Input.dispatchKeyEvent" || method == "Input.insertText" {
		return &OverlayInput{Kind: "key"}
	}
	if method != "Input.dispatchMouseEvent" || params == nil {
		return nil
	}
	t, _ := params["type"].(string)
	var kind string
	switch t {
	case "mouseMoved":
		kind = "move"
	case "mousePressed":
		kind = "press"
	case "mouseReleased":
		kind = "release"
	case "mouseWheel":
		kind = "wheel"
	default:
		return nil
	}
	x, okX := toFloat64(params["x"])
	y, okY := toFloat64(params["y"])
	if !okX || !okY {
		return nil
	}
	if x == CancelledReleasePoint && y == CancelledReleasePoint {
		return nil
	}
	if kind != "press" {
		return &OverlayInput{Kind: kind, X: &x, Y: &y}
	}
	btn, _ := params["button"].(string)
	if !isOverlayButton(btn) {
		btn = "none"
	}
	cc := 1
	if v, ok := toFloat64(params["clickCount"]); ok {
		cc = int(v)
	}
	return &OverlayInput{Kind: kind, X: &x, Y: &y, Button: btn, ClickCount: &cc}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func isChildFrame(frame any) bool {
	m, ok := frame.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m["parentId"].(string)
	return ok
}

// singleLine collapses control/format/separator characters to single spaces and trims to max runes.
// upstream: bridge.ts:2847-2852
func singleLine(text string, maxRunes int) string {
	var b strings.Builder
	inSpace := false
	for _, r := range text {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	s := strings.TrimSpace(b.String())
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return s
}

type pendingRPC struct {
	ch    *promise[json.RawMessage]
	timer *time.Timer
}

type lateRPC struct {
	onResult func(json.RawMessage)
	timer    *time.Timer
}

type queuedSend struct {
	sock RelaySocket
	text string
}

// RelayBridge multiplexes downstream CDP connections over one chrome.debugger
// attachment per tab held by the connected Chrome extension(s).
// upstream: packages/coding-agent/src/tools/browser/relay/bridge.ts:490-2839
type RelayBridge struct {
	mu       sync.Mutex
	sendMu   sync.Mutex
	outbox   []queuedSend
	closebox []RelaySocket

	tabs              map[string]*tabState
	tabOrder          []string
	conns             map[int]*cdpConnection
	connSeq           int
	sessionSeq        int
	rpcSeq            int
	instances         map[string]*extInstance
	instanceOrder     []string
	socketInstance    map[RelaySocket]string
	socketExtensionID map[RelaySocket]string
	updateExtension   func(extensionID string) (bool, error)
	installedStamp    func(extensionID string) (*RelayExtensionStamp, error)
	stampChecks       map[string]struct{}
	updateTried       map[string]map[string]struct{}
	lastHelloInstance string
	closeOnHello      map[string]string
	pendingRPC        map[string]*pendingRPC
	lateRPC           map[string]*lateRPC
	realSessionTabs   map[string]string
	logFn             func(message string, data map[string]any)
	group             *GroupConfig
	groupQueue        []*tabState
	groupDraining     bool
	switchChains      map[string]chan struct{}
	readFrontApp      func() (FrontApp, error)
	keepingFrontUntil time.Time
	panelSources      PanelSources

	relayVersion        string
	now                 func() time.Time
	rpcTimeout          time.Duration
	pageBoundRPCTimeout time.Duration
	lateRPCKeep         time.Duration
	overlayRPCTimeout   time.Duration
	overlayHideWait     time.Duration
	affineReconnectWait time.Duration
	chromeDetachReport  time.Duration
}

// NewRelayBridge constructs a RelayBridge.
// upstream: bridge.ts:551-569
func NewRelayBridge(opts BridgeOptions) *RelayBridge {
	logFn := opts.Log
	if logFn == nil {
		logFn = func(string, map[string]any) {}
	}
	ps := PanelSources{
		ListHosts: func() ([]PanelCollabHost, error) {
			return []PanelCollabHost{}, nil
		},
		ControlLink: func(instanceID string) (string, error) {
			return "", fmt.Errorf("collab is not available in %s (no active Collab host matches %s)", ExtensionName, instanceID)
		},
		Installs: func() ([]PanelInstall, error) {
			rows, err := ReadRelayInstalls(InstallLocations{})
			if err != nil {
				return nil, err
			}
			out := make([]PanelInstall, len(rows))
			for i, r := range rows {
				out[i] = PanelInstall{Dir: r.Dir, Version: r.Version, Missing: r.Missing}
			}
			return out, nil
		},
	}
	if opts.PanelSources != nil {
		if opts.PanelSources.ListHosts != nil {
			ps.ListHosts = opts.PanelSources.ListHosts
		}
		if opts.PanelSources.ControlLink != nil {
			ps.ControlLink = opts.PanelSources.ControlLink
		}
		if opts.PanelSources.Installs != nil {
			ps.Installs = opts.PanelSources.Installs
		}
	}
	nowFn := opts.Clock
	if nowFn == nil {
		nowFn = time.Now
	}
	b := &RelayBridge{
		tabs:                make(map[string]*tabState),
		conns:               make(map[int]*cdpConnection),
		instances:           make(map[string]*extInstance),
		socketInstance:      make(map[RelaySocket]string),
		socketExtensionID:   make(map[RelaySocket]string),
		updateExtension:     opts.UpdateExtension,
		installedStamp:      opts.InstalledStamp,
		stampChecks:         make(map[string]struct{}),
		updateTried:         make(map[string]map[string]struct{}),
		closeOnHello:        make(map[string]string),
		pendingRPC:          make(map[string]*pendingRPC),
		lateRPC:             make(map[string]*lateRPC),
		realSessionTabs:     make(map[string]string),
		logFn:               logFn,
		group:               opts.Group,
		switchChains:        make(map[string]chan struct{}),
		panelSources:        ps,
		relayVersion:        RelayVersion,
		now:                 nowFn,
		rpcTimeout:          defaultRPCTimeout,
		pageBoundRPCTimeout: defaultPageBoundRPCTimeout,
		lateRPCKeep:         defaultLateRPCKeep,
		overlayRPCTimeout:   defaultOverlayRPCTimeout,
		overlayHideWait:     defaultOverlayHideWait,
		affineReconnectWait: defaultAffineReconnectWait,
		chromeDetachReport:  defaultChromeDetachReport,
	}
	if opts.RPCTimeout > 0 {
		b.rpcTimeout = opts.RPCTimeout
	}
	if opts.PageBoundRPCTimeout > 0 {
		b.pageBoundRPCTimeout = opts.PageBoundRPCTimeout
	}
	if opts.LateRPCKeep > 0 {
		b.lateRPCKeep = opts.LateRPCKeep
	}
	if opts.OverlayRPCTimeout > 0 {
		b.overlayRPCTimeout = opts.OverlayRPCTimeout
	}
	if opts.OverlayHideWait > 0 {
		b.overlayHideWait = opts.OverlayHideWait
	}
	if opts.AffineReconnectWait > 0 {
		b.affineReconnectWait = opts.AffineReconnectWait
	}
	if opts.ChromeDetachReport > 0 {
		b.chromeDetachReport = opts.ChromeDetachReport
	}
	return b
}

// KeepFrontAppWith registers a callback that reads the frontmost user application
// before window-opening input so Chrome focus steals can be undone.
// upstream: bridge.ts:1593-1595
func (b *RelayBridge) KeepFrontAppWith(read func() (FrontApp, error)) {
	b.mu.Lock()
	b.readFrontApp = read
	b.mu.Unlock()
}

// Ready reports true once at least one extension completed hello and is not updating.
// upstream: bridge.ts:572-577
func (b *RelayBridge) Ready() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range b.instanceOrder {
		inst := b.instances[id]
		if inst != nil && inst.socket != nil && inst.info != nil && !inst.updating {
			return true
		}
	}
	return false
}

// CDPClientCount returns the number of connected downstream CDP clients.
// upstream: bridge.ts:638-640
func (b *RelayBridge) CDPClientCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

func (b *RelayBridge) lastHelloLocked() *extInstance {
	if b.lastHelloInstance != "" {
		if inst := b.instances[b.lastHelloInstance]; inst != nil && inst.socket != nil {
			return inst
		}
	}
	for _, id := range b.instanceOrder {
		if inst := b.instances[id]; inst != nil && inst.socket != nil {
			return inst
		}
	}
	return nil
}

// VersionInfo builds the JSON payload for GET /json/version.
// upstream: bridge.ts:643-677
func (b *RelayBridge) VersionInfo(wsURL string) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var info *extHelloInfo
	if lh := b.lastHelloLocked(); lh != nil {
		info = lh.info
	}
	hasCompatible := false
	var oldestPtr *string
	for _, id := range b.instanceOrder {
		inst := b.instances[id]
		if inst == nil || inst.socket == nil || inst.info == nil || inst.info.discardedTabsProtocol != DiscardedTabsProtocolVersion {
			continue
		}
		if inst.updating {
			continue
		}
		hasCompatible = true
		ompVer := ""
		if inst.info.ompVersion != nil {
			ompVer = *inst.info.ompVersion
		}
		if oldestPtr == nil ||
			IsOlderOmpVersion(ompVer, *oldestPtr) ||
			(*oldestPtr != "" && !IsOmpVersion(*oldestPtr)) {
			v := ompVer
			oldestPtr = &v
		}
	}
	browserVer := "Chrome/unknown"
	ua := ""
	if info != nil {
		if info.browserVersion != "" {
			browserVer = info.browserVersion
		}
		ua = info.userAgent
	}
	extProto := "0"
	if hasCompatible {
		extProto = fmt.Sprintf("%d", DiscardedTabsProtocolVersion)
	}
	oldestExt := ""
	if oldestPtr != nil {
		oldestExt = *oldestPtr
	}
	return map[string]any{
		"Browser":                           browserVer,
		"Protocol-Version":                  "1.3",
		"User-Agent":                        ua,
		"V8-Version":                        "",
		"WebKit-Version":                    "",
		"webSocketDebuggerUrl":              wsURL,
		"ompRelayVersion":                   RelayVersion,
		"ompRelayDiscardedTabsProtocol":     fmt.Sprintf("%d", DiscardedTabsProtocolVersion),
		"ompExtensionDiscardedTabsProtocol": extProto,
		"ompRelayCdpClients":                len(b.conns),
		"ompExtensionVersion":               oldestExt,
	}
}

// ListTargets builds the JSON payload for GET /json and GET /json/list.
// upstream: bridge.ts:680-694
func (b *RelayBridge) ListTargets() []map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]map[string]string, 0, len(b.tabOrder))
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab == nil || !b.eligibleLocked(tab) {
			continue
		}
		out = append(out, map[string]string{
			"id":        pageTargetIDFromKey(tab.tabKey),
			"type":      "page",
			"title":     tab.title,
			"url":       tab.url,
			"active":    fmt.Sprintf("%t", tab.active),
			"discarded": fmt.Sprintf("%t", tab.discarded),
		})
	}
	return out
}

func (b *RelayBridge) eligibleLocked(tab *tabState) bool {
	if tab.refusal != "" {
		return false
	}
	inst := b.instances[tab.instanceID]
	if inst == nil || inst.socket == nil || inst.info == nil || inst.info.discardedTabsProtocol != DiscardedTabsProtocolVersion {
		return false
	}
	if tab.url == "" {
		return true
	}
	return !ineligibleURLRE.MatchString(tab.url)
}

func (b *RelayBridge) tabInfo(tab *tabState, attached bool) targetInfo {
	u := tab.url
	if u == "" {
		u = "about:blank"
	}
	return targetInfo{
		TargetID:        tabTargetIDFromKey(tab.tabKey),
		Type:            "tab",
		Title:           tab.title,
		URL:             u,
		Attached:        attached,
		CanAccessOpener: false,
	}
}

func (b *RelayBridge) pageInfo(tab *tabState, attached bool) targetInfo {
	u := tab.url
	if u == "" {
		u = "about:blank"
	}
	return targetInfo{
		TargetID:        pageTargetIDFromKey(tab.tabKey),
		Type:            "page",
		Title:           tab.title,
		URL:             u,
		Attached:        attached,
		CanAccessOpener: false,
	}
}

func (b *RelayBridge) enqueueSendLocked(sock RelaySocket, payload any) {
	if sock == nil {
		return
	}
	var text string
	if s, ok := payload.(string); ok {
		text = s
	} else {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		text = string(data)
	}
	b.outbox = append(b.outbox, queuedSend{sock: sock, text: text})
}

func (b *RelayBridge) enqueueCloseLocked(sock RelaySocket) {
	if sock == nil {
		return
	}
	b.closebox = append(b.closebox, sock)
}

// flush delivers queued socket writes in strict FIFO order without holding b.mu,
// then closes any sockets queued for retirement outside both locks.
func (b *RelayBridge) flush() {
	var closes []RelaySocket
	b.sendMu.Lock()
	for {
		b.mu.Lock()
		batch := b.outbox
		b.outbox = nil
		if len(b.closebox) > 0 {
			closes = append(closes, b.closebox...)
			b.closebox = nil
		}
		b.mu.Unlock()
		if len(batch) == 0 {
			break
		}
		for _, item := range batch {
			item.sock.Send(item.text)
		}
	}
	b.sendMu.Unlock()
	for _, sock := range closes {
		sock.Close()
	}
}

func (b *RelayBridge) replyLocked(conn *cdpConnection, msg cdpCommand, result any) {
	if result == nil {
		result = map[string]any{}
	}
	env := map[string]any{
		"id":     msg.ID,
		"result": result,
	}
	if msg.SessionID != "" {
		env["sessionId"] = msg.SessionID
	}
	b.enqueueSendLocked(conn.socket, env)
}

func (b *RelayBridge) replyErrorLocked(conn *cdpConnection, msg cdpCommand, message string, code int) {
	if code == 0 {
		code = cdpErrorServer
	}
	env := map[string]any{
		"id": msg.ID,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if msg.SessionID != "" {
		env["sessionId"] = msg.SessionID
	}
	b.enqueueSendLocked(conn.socket, env)
}

func (b *RelayBridge) emitLocked(conn *cdpConnection, method string, params any, sessionID string) {
	env := map[string]any{
		"method": method,
		"params": params,
	}
	if sessionID != "" {
		env["sessionId"] = sessionID
	}
	b.enqueueSendLocked(conn.socket, env)
}

func (b *RelayBridge) addTabLocked(tab *tabState) {
	if _, exists := b.tabs[tab.tabKey]; !exists {
		b.tabOrder = append(b.tabOrder, tab.tabKey)
	}
	b.tabs[tab.tabKey] = tab
}

func (b *RelayBridge) deleteTabLocked(tabKey string) {
	delete(b.tabs, tabKey)
	b.tabOrder = slices.DeleteFunc(b.tabOrder, func(k string) bool { return k == tabKey })
}

func (b *RelayBridge) addInstanceLocked(inst *extInstance) {
	if _, exists := b.instances[inst.instanceID]; !exists {
		b.instanceOrder = append(b.instanceOrder, inst.instanceID)
	}
	b.instances[inst.instanceID] = inst
}
