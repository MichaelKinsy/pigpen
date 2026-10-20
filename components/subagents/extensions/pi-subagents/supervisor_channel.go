package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/intercom/native-supervisor-channel.ts: the file channel between a child's `contact_supervisor` and the parent's
// `subagent_supervisor`. A child writes a request file into its own mailbox (a directory per run, agent and child index), waits
// for a reply file, and the parent's channel discovers the request, shows it in the conversation and answers it.

const (
	nativeSupervisorToolName       = "subagent_supervisor"
	supervisorRequestsDir          = "requests"
	supervisorRepliesDir           = "replies"
	maxSupervisorMessageBytes      = 64 * 1024
	defaultSupervisorAskTimeout    = 10 * time.Minute
	supervisorChannelPoll          = 250 * time.Millisecond
	supervisorChannelSafetyPoll    = 5 * time.Second
	staleEmptyChannelAge           = 60 * time.Second
	staleEmptyChannelCleanupEvery  = 60 * time.Second
	maxSupervisorCorrelations      = 256
	supervisorCorrelationRetention = 10 * time.Minute
)

func supervisorChannelRoot() string { return filepath.Join(tempRootDir(), "supervisor-channels") }

var safeSegmentUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
var safeSegmentEdges = regexp.MustCompile(`^-+|-+$`)

func safeSegment(value string) string {
	s := safeSegmentEdges.ReplaceAllString(safeSegmentUnsafe.ReplaceAllString(jsTrim(value), "-"), "")
	if s == "" {
		return "unknown"
	}
	return s
}

func resolveSupervisorChannelDir(runID, agent string, childIndex int) string {
	return filepath.Join(supervisorChannelRoot(), safeSegment(runID)+"-"+safeSegment(agent)+"-"+strconv.Itoa(childIndex))
}

func ensureSupervisorChannelDir(channelDir string) error {
	for _, d := range []string{supervisorRequestsDir, supervisorRepliesDir} {
		if err := os.MkdirAll(filepath.Join(channelDir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func supervisorRequestPath(channelDir, requestID string) string {
	return filepath.Join(channelDir, supervisorRequestsDir, safeSegment(requestID)+".json")
}

func supervisorReplyPath(channelDir, requestID string) string {
	return filepath.Join(channelDir, supervisorRepliesDir, safeSegment(requestID)+".json")
}

func supervisorReasonHeading(reason string) string {
	switch reason {
	case "interview_request":
		return "Subagent requests a structured supervisor interview."
	case "progress_update":
		return "Subagent progress update."
	}
	return "Subagent needs a supervisor decision."
}

// supervisorAskTimeout is PI_INTERCOM_ASK_TIMEOUT_MS when it is a positive number, else ten minutes.
func supervisorAskTimeout() time.Duration {
	if f, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("PI_INTERCOM_ASK_TIMEOUT_MS")), 64); err == nil && f > 0 && f < 1e15 {
		return time.Duration(f * float64(time.Millisecond))
	}
	return defaultSupervisorAskTimeout
}

// supervisorRequest is the request file a child writes.
type supervisorRequest struct {
	Type                  string   `json:"type"`
	ID                    string   `json:"id"`
	CreatedAt             float64  `json:"createdAt"`
	ExpiresAt             *float64 `json:"expiresAt,omitempty"`
	Reason                string   `json:"reason"`
	Message               string   `json:"message"`
	ExpectsReply          bool     `json:"expectsReply"`
	OrchestratorTarget    string   `json:"orchestratorTarget,omitempty"`
	OrchestratorSessionID string   `json:"orchestratorSessionId,omitempty"`
	RunID                 string   `json:"runId"`
	Agent                 string   `json:"agent"`
	ChildIndex            int      `json:"childIndex"`
	ToolCallID            string   `json:"toolCallId,omitempty"`
	ChildTarget           string   `json:"childTarget,omitempty"`
	Interview             any      `json:"interview,omitempty"`
}

// pendingSupervisorRequest is a request the parent registered.
type pendingSupervisorRequest struct {
	supervisorRequest
	ChannelDir  string
	RequestFile string
}

type supervisorReply struct {
	Type      string  `json:"type"`
	RequestID string  `json:"requestId"`
	CreatedAt float64 `json:"createdAt"`
	Message   string  `json:"message"`
}

// childSupervisorMetadata is ChildSupervisorMetadata: what a child needs to reach its supervisor.
type childSupervisorMetadata struct {
	ChannelDir            string `json:"channelDir"`
	RunID                 string `json:"runId"`
	Agent                 string `json:"agent"`
	ChildIndex            int    `json:"childIndex"`
	OrchestratorTarget    string `json:"orchestratorTarget,omitempty"`
	OrchestratorSessionID string `json:"orchestratorSessionId"`
	ChildTarget           string `json:"childTarget,omitempty"`
}

// supervisorChildMetadataJSON is the metadata a launch gives a child (the role file's `supervisor`); the channel directory is the
// one the parent will poll for that run, agent and index.
func supervisorChildMetadataJSON(runID, agent string, childIndex int, orchestratorTarget, orchestratorSessionID, childTarget string) json.RawMessage {
	b, _ := json.Marshal(childSupervisorMetadata{ChannelDir: resolveSupervisorChannelDir(runID, agent, childIndex), RunID: runID, Agent: agent, ChildIndex: childIndex,
		OrchestratorTarget: orchestratorTarget, OrchestratorSessionID: orchestratorSessionID, ChildTarget: childTarget})
	return b
}

// ---- the child side ----

// supervisorToolResult is what a supervisor tool returns: the text and the details.
type supervisorToolResult struct {
	Text    string
	Details map[string]any
}

// contactSupervisorParams are the arguments of contact_supervisor.
type contactSupervisorParams struct {
	Reason       string
	Message      string
	Interview    any
	HasInterview bool
}

func parseStructuredSupervisorReply(message string) (value any, parseErr string) {
	trimmed := jsTrim(message)
	if m := fencedJSON.FindStringSubmatch(trimmed); m != nil {
		trimmed = jsTrim(m[1])
	}
	v, err := parseJSON([]byte(trimmed))
	if err != nil {
		return nil, "SyntaxError: " + err.Error()
	}
	return v, ""
}

var fencedJSON = regexp.MustCompile("(?is)^```(?:json)?\\s*(.*?)\\s*```$")

var errSupervisorCancelled = errors.New("Supervisor request cancelled.")

// waitForSupervisorReply polls for the reply file of a request every 250 ms until the deadline or a cancellation.
func waitForSupervisorReply(channelDir, requestID string, deadline time.Time, cancelled func() bool) (*supervisorReply, error) {
	file := supervisorReplyPath(channelDir, requestID)
	for !time.Now().After(deadline) {
		if cancelled != nil && cancelled() {
			return nil, errSupervisorCancelled
		}
		if b, err := os.ReadFile(file); err == nil {
			var r supervisorReply
			if err := json.Unmarshal(b, &r); err != nil {
				return nil, err
			}
			if r.Type == "subagent.supervisor.reply" && r.RequestID == requestID {
				return &r, nil
			}
		}
		step := 250 * time.Millisecond
		if rest := time.Until(deadline); rest < step {
			step = max(rest, 0)
		}
		time.Sleep(step)
		if cancelled != nil && cancelled() {
			return nil, errSupervisorCancelled
		}
	}
	return nil, errors.New("Timed out waiting for supervisor reply.")
}

// sendSupervisorRequest is contact_supervisor: it writes the request and, for a decision or an interview, waits for the reply.
func sendSupervisorRequest(p contactSupervisorParams, md childSupervisorMetadata, cancelled func() bool, toolCallID string) (supervisorToolResult, error) {
	if jsTrim(p.Message) == "" && p.Reason != "interview_request" {
		return supervisorToolResult{}, errors.New("message is required for supervisor decisions and progress updates.")
	}
	if err := ensureSupervisorChannelDir(md.ChannelDir); err != nil {
		return supervisorToolResult{}, err
	}
	requestID := randomUUID()
	expectsReply := p.Reason != "progress_update"
	created := time.Now()
	replyDeadline := created.Add(supervisorAskTimeout())
	req := supervisorRequest{Type: "subagent.supervisor.request", ID: requestID, CreatedAt: float64(created.UnixMilli()), Reason: p.Reason, Message: jsTrim(p.Message), ExpectsReply: expectsReply,
		OrchestratorTarget: md.OrchestratorTarget, OrchestratorSessionID: md.OrchestratorSessionID, RunID: md.RunID, Agent: md.Agent, ChildIndex: md.ChildIndex,
		ToolCallID: toolCallID, ChildTarget: md.ChildTarget}
	if expectsReply {
		req.ExpiresAt = new(float64(replyDeadline.UnixMilli()))
	}
	if p.HasInterview {
		req.Interview = p.Interview
	}
	if b, _ := json.MarshalIndent(req, "", "\t"); len(b) > maxSupervisorMessageBytes {
		return supervisorToolResult{}, errors.New("Supervisor request is too large.")
	}
	if err := asyncWriteJSON(supervisorRequestPath(md.ChannelDir, requestID), req); err != nil {
		return supervisorToolResult{}, err
	}
	if !expectsReply {
		return supervisorToolResult{Text: "Supervisor progress update queued.", Details: map[string]any{"delivered": true, "requestId": requestID, "reason": p.Reason}}, nil
	}
	reply, err := waitForSupervisorReply(md.ChannelDir, requestID, replyDeadline, cancelled)
	if err != nil {
		removeSupervisorRequestFile(supervisorRequestPath(md.ChannelDir, requestID))
		return supervisorToolResult{}, err
	}
	details := map[string]any{"requestId": requestID, "reason": p.Reason}
	if p.Reason == "interview_request" {
		if v, perr := parseStructuredSupervisorReply(reply.Message); perr != "" {
			details["structuredReplyParseError"] = perr
		} else {
			details["structuredReply"] = v
		}
	}
	return supervisorToolResult{Text: "**Reply from supervisor:**\n" + reply.Message, Details: details}, nil
}

func removeSupervisorRequestFile(file string) { _ = os.Remove(file) }

// supervisorClientHost is the part of a child's extension host the client tool needs.
type supervisorClientHost interface {
	HasTool(name string) bool
	RegisterTool(t *supervisorTool)
}

// supervisorTool is a tool the channel registers: Execute gets the call id, the arguments and a cancellation probe.
type supervisorTool struct {
	Name    string
	Execute func(id string, params map[string]any, cancelled func() bool) (supervisorToolResult, error)
}

func contactSupervisorParamsOf(m map[string]any) contactSupervisorParams {
	p := contactSupervisorParams{}
	p.Reason, _ = m["reason"].(string)
	p.Message, _ = m["message"].(string)
	if v, has := m["interview"]; has {
		p.Interview, p.HasInterview = v, true
	}
	return p
}

// registerNativeSupervisorClient registers the child-side `contact_supervisor` tool (once; with no metadata, never).
func registerNativeSupervisorClient(host supervisorClientHost, md *childSupervisorMetadata) {
	if md == nil || host.HasTool("contact_supervisor") {
		return
	}
	host.RegisterTool(&supervisorTool{Name: "contact_supervisor", Execute: func(id string, params map[string]any, cancelled func() bool) (supervisorToolResult, error) {
		return sendSupervisorRequest(contactSupervisorParamsOf(params), *md, cancelled, id)
	}})
}

const contactSupervisorSchema = `{"type":"object","properties":{"reason":{"type":"string","enum":["need_decision","interview_request","progress_update"]},"message":{"type":"string"},"interview":{"type":"object","additionalProperties":true}},"required":["reason"],"additionalProperties":false}`

func init() {
	childRoleExtras = append(childRoleExtras, func(e *sdk.Extension, cfg *childRoleConfig) error {
		if len(cfg.Supervisor) == 0 {
			return nil
		}
		var md childSupervisorMetadata
		if err := json.Unmarshal(cfg.Supervisor, &md); err != nil {
			return err
		}
		var params sdk.Schema
		if err := json.Unmarshal([]byte(contactSupervisorSchema), &params); err != nil {
			return err
		}
		e.RegisterTool(sdk.ToolDefinition{Name: "contact_supervisor", Label: "Contact Supervisor", Parameters: params,
			Description: "Contact the parent/supervisor session for a blocking decision, structured interview, or progress update.",
			Execute: func(ctx sdk.Context, in map[string]any) (any, error) {
				done := ctx.Done()
				cancelled := func() bool {
					select {
					case <-done:
						return true
					default:
						return false
					}
				}
				res, err := sendSupervisorRequest(contactSupervisorParamsOf(in), md, cancelled, ctx.ToolCallID())
				if err != nil {
					return nil, sdk.NewToolError(err.Error())
				}
				return sdk.ToolResult{Content: res.Text, Details: res.Details}, nil
			}})
		return nil
	})
}

// ---- the parent side ----

// supervisorEntry is a directory entry of the channel's file system.
type supervisorEntry struct {
	Name          string
	IsDir, IsFile bool
}

// supervisorFS is the file system of the channel, so a test can count scans or make one fail.
type supervisorFS interface {
	ReadDir(dir string) ([]supervisorEntry, error)
	ReadFile(p string) ([]byte, error)
	Exists(p string) bool
	// MtimeMs is the modification time in ms (0 when it cannot be read).
	MtimeMs(p string) float64
	Remove(p string) error
	Rmdir(dir string) error
	MkdirAll(dir string) error
}

type osSupervisorFS struct{}

func (osSupervisorFS) ReadDir(dir string) ([]supervisorEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]supervisorEntry, len(entries))
	for i, e := range entries {
		out[i] = supervisorEntry{Name: e.Name(), IsDir: e.IsDir(), IsFile: e.Type().IsRegular()}
	}
	return out, nil
}
func (osSupervisorFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (osSupervisorFS) Exists(p string) bool              { _, err := os.Stat(p); return err == nil }
func (osSupervisorFS) MtimeMs(p string) float64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return float64(st.ModTime().UnixNano()) / 1e6
}
func (osSupervisorFS) Remove(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
func (osSupervisorFS) Rmdir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "rmdir", Path: dir, Err: syscall.ENOTDIR}
	}
	return os.Remove(dir)
}
func (osSupervisorFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

// codedError is an error with a Node-style code (ENOENT, UNKNOWN, ...).
type codedError struct {
	Code string
	Msg  string
}

func (e *codedError) Error() string { return e.Msg }

func errorCode(err error) string {
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, fs.ErrExist):
		return "EEXIST"
	}
	return ""
}

// isMissingSupervisorDirectory is a missing directory: ENOENT, or UNKNOWN where Windows reports a vanished directory that way.
func isMissingSupervisorDirectory(err error, platform string) bool {
	code := errorCode(err)
	return code == "ENOENT" || (platform == "windows" && code == "UNKNOWN")
}

// supervisorTimers starts the channel's timers; each start returns the function that clears it.
type supervisorTimers interface {
	Interval(fn func(), d time.Duration) (clear func())
	Immediate(fn func()) (clear func())
}

type realSupervisorTimers struct{}

func (realSupervisorTimers) Interval(fn func(), d time.Duration) func() {
	return realControlTimers{}.Every(d, fn)
}
func (realSupervisorTimers) Immediate(fn func()) func() {
	var cancelled atomic.Bool
	go func() {
		if !cancelled.Load() {
			fn()
		}
	}()
	return func() { cancelled.Store(true) }
}

// supervisorHost is the parent session the channel talks to (the original's pi).
type supervisorHost struct {
	// GetAllTools lists the names of the registered tools (nil: none).
	GetAllTools func() []string
	// RegisterTool registers the parent's tool (called once, unless one with the name exists).
	RegisterTool func(t *supervisorTool)
	// SendMessage puts a custom message in the conversation; it may fail (no UI, a stale context).
	SendMessage func(msg sdk.CustomMessage, triggerTurn bool) error
	// AppendEntry journals an entry; nil when the host cannot.
	AppendEntry func(customType string, data any) error
	// Emit sends an event on the host's event bus; nil when there is none.
	Emit func(channel string, payload any)
}

func (h supervisorHost) hasTool(name string) bool {
	if h.GetAllTools == nil {
		return false
	}
	for _, t := range h.GetAllTools() {
		if t == name {
			return true
		}
	}
	return false
}

// supervisorDeps are the options of createNativeSupervisorChannel.
type supervisorDeps struct {
	// GetChannelDirs lists the owned live (or final-drain) mailboxes; only a completed poll retires the snapshot.
	GetChannelDirs func() (dirs []string, retire func())
	// GetCurrentOwnerStates are the retained scheduled states of the current runtime owner.
	GetCurrentOwnerStates func() []*SubagentState
	// Platform is "darwin", "windows" or "linux" (the host's by default).
	Platform string
	// Watch watches a directory (a native watcher); nil when the platform has none: the channel then polls while there is demand.
	Watch  func(dir string, listener func()) (closeWatch func(), err error)
	Timers supervisorTimers
	FS     supervisorFS
	// StateLock guards the session state the channel reads (nil: the state is not shared).
	StateLock sync.Locker
	Now       func() time.Time
}

type supervisorLifecycle string

const (
	lifecyclePending    supervisorLifecycle = "pending"
	lifecycleResolved   supervisorLifecycle = "resolved"
	lifecycleExpired    supervisorLifecycle = "expired"
	lifecycleInactive   supervisorLifecycle = "inactive"
	lifecycleMissing    supervisorLifecycle = "missing"
	lifecycleWrongSessn supervisorLifecycle = "wrong-session"
)

type supervisorCorrelation struct {
	request   *pendingSupervisorRequest
	resolved  bool
	updatedAt time.Time
}

// rmutex is a mutex a goroutine may take again (the channel's host callbacks call back into its tool).
type rmutex struct {
	mu    sync.Mutex
	owner atomic.Int64
	depth int
}

func goroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	f := strings.Fields(string(buf[:n]))
	if len(f) < 2 {
		return -1
	}
	id, _ := strconv.ParseInt(f[1], 10, 64)
	return id
}

func (m *rmutex) Lock() {
	id := goroutineID()
	if m.owner.Load() == id {
		m.depth++
		return
	}
	m.mu.Lock()
	m.owner.Store(id)
	m.depth = 1
}

func (m *rmutex) Unlock() {
	m.depth--
	if m.depth == 0 {
		m.owner.Store(0)
		m.mu.Unlock()
	}
}

// nativeSupervisorChannel is the parent's channel (the object createNativeSupervisorChannel returns).
type nativeSupervisorChannel struct {
	host  supervisorHost
	state *SubagentState
	deps  supervisorDeps
	mu    rmutex

	pending      map[string]*pendingSupervisorRequest
	pendingOrder []string
	corr         map[string]*supervisorCorrelation
	corrOrder    []string
	seenFiles    map[string]bool

	requestWatchers map[string]func()
	rootWatcher     func()
	poller          func()
	safetyPoller    func()
	deferredRefresh func()
	started         bool
	lastStaleClean  time.Time
}

func createNativeSupervisorChannel(host supervisorHost, state *SubagentState, deps supervisorDeps) *nativeSupervisorChannel {
	if deps.Platform == "" {
		deps.Platform = runtime.GOOS
	}
	if deps.Timers == nil {
		deps.Timers = realSupervisorTimers{}
	}
	if deps.FS == nil {
		deps.FS = osSupervisorFS{}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &nativeSupervisorChannel{host: host, state: state, deps: deps, pending: map[string]*pendingSupervisorRequest{}, corr: map[string]*supervisorCorrelation{},
		seenFiles: map[string]bool{}, requestWatchers: map[string]func(){}}
}

func (c *nativeSupervisorChannel) now() time.Time { return c.deps.Now() }

// withState runs fn with the session state locked (when the state is shared).
func (c *nativeSupervisorChannel) withState(fn func()) {
	if c.deps.StateLock != nil {
		c.deps.StateLock.Lock()
		defer c.deps.StateLock.Unlock()
	}
	fn()
}

func (c *nativeSupervisorChannel) addPending(r *pendingSupervisorRequest) {
	if _, ok := c.pending[r.ID]; !ok {
		c.pendingOrder = append(c.pendingOrder, r.ID)
	}
	c.pending[r.ID] = r
}

func (c *nativeSupervisorChannel) removePending(id string) {
	if _, ok := c.pending[id]; !ok {
		return
	}
	delete(c.pending, id)
	for i, o := range c.pendingOrder {
		if o == id {
			c.pendingOrder = append(c.pendingOrder[:i], c.pendingOrder[i+1:]...)
			break
		}
	}
}

func (c *nativeSupervisorChannel) pendingList() []*pendingSupervisorRequest {
	out := make([]*pendingSupervisorRequest, 0, len(c.pendingOrder))
	for _, id := range c.pendingOrder {
		out = append(out, c.pending[id])
	}
	return out
}

// hasPendingID is `channel.pending.has(id)`.
func (c *nativeSupervisorChannel) hasPendingID(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[id]
	return ok
}

func (c *nativeSupervisorChannel) pendingIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.pendingOrder...)
}

func correlationKey(runID, agent string, childIndex int, toolCallID string) string {
	if toolCallID == "" {
		return ""
	}
	b, _ := json.Marshal([]any{runID, agent, childIndex, toolCallID})
	return string(b)
}

func (c *nativeSupervisorChannel) pruneCorrelations() {
	now := c.now()
	for _, k := range append([]string{}, c.corrOrder...) {
		if cr := c.corr[k]; cr != nil && cr.resolved && now.Sub(cr.updatedAt) > supervisorCorrelationRetention {
			c.dropCorrelation(k)
		}
	}
	for len(c.corr) > maxSupervisorCorrelations && len(c.corrOrder) > 0 {
		c.dropCorrelation(c.corrOrder[0])
	}
}

func (c *nativeSupervisorChannel) dropCorrelation(k string) {
	delete(c.corr, k)
	for i, o := range c.corrOrder {
		if o == k {
			c.corrOrder = append(c.corrOrder[:i], c.corrOrder[i+1:]...)
			break
		}
	}
}

func (c *nativeSupervisorChannel) setCorrelation(r *pendingSupervisorRequest, resolved bool) {
	k := correlationKey(r.RunID, r.Agent, r.ChildIndex, r.ToolCallID)
	if k == "" || !r.ExpectsReply {
		return
	}
	if _, ok := c.corr[k]; ok {
		// a Map keeps the first insertion position when a key is set again
	} else {
		c.corrOrder = append(c.corrOrder, k)
	}
	c.corr[k] = &supervisorCorrelation{request: r, resolved: resolved, updatedAt: c.now()}
	c.pruneCorrelations()
}

func (c *nativeSupervisorChannel) rememberPending(r *pendingSupervisorRequest) {
	c.setCorrelation(r, false)
}
func (c *nativeSupervisorChannel) rememberResolved(r *pendingSupervisorRequest) {
	c.setCorrelation(r, true)
}

func (c *nativeSupervisorChannel) observeLifecycle(r *pendingSupervisorRequest, lc supervisorLifecycle) {
	if lc != lifecycleWrongSessn {
		c.rememberResolved(r)
	}
}

// ---- state accessors ----

func (c *nativeSupervisorChannel) ownerMatches(r *supervisorRequest) bool {
	var owner string
	c.withState(func() { owner = c.state.SupervisorOwnerSessionID })
	return owner != "" && r.OrchestratorSessionID == owner
}

// rememberedForegroundChild is the detached foreground child a request is from: the one with its index and agent, else the one at its
// index.
func rememberedForegroundChild(r *supervisorRequest, st *SubagentState) (*ForegroundRunRecord, *ForegroundChildRecord) {
	run := st.ForegroundRuns[r.RunID]
	if run == nil {
		return nil, nil
	}
	for i := range run.Children {
		if run.Children[i].Index == r.ChildIndex && run.Children[i].Agent == r.Agent {
			return run, &run.Children[i]
		}
	}
	if r.ChildIndex >= 0 && r.ChildIndex < len(run.Children) {
		return run, &run.Children[r.ChildIndex]
	}
	return nil, nil
}

func (c *nativeSupervisorChannel) markForegroundAttention(r *supervisorRequest) {
	c.withState(func() {
		run, child := rememberedForegroundChild(r, c.state)
		if run == nil || child.Status != "detached" {
			return
		}
		now := c.now().UnixMilli()
		run.UpdatedAt = now
		child.ActivityState = "needs_attention"
		child.LastActivityAt = int64(r.CreatedAt)
		child.CurrentTool = "contact_supervisor"
		child.CurrentToolStartedAt = int64(r.CreatedAt)
		child.UpdatedAt = now
	})
}

func (c *nativeSupervisorChannel) clearForegroundAttention(r *supervisorRequest) {
	for _, p := range c.pending {
		if p.ExpectsReply && p.RunID == r.RunID && p.Agent == r.Agent && p.ChildIndex == r.ChildIndex {
			return
		}
	}
	c.withState(func() {
		run, child := rememberedForegroundChild(r, c.state)
		if run == nil || child.Status != "detached" {
			return
		}
		now := c.now().UnixMilli()
		run.UpdatedAt = now
		child.ActivityState = ""
		child.LastActivityAt = now
		child.CurrentTool = ""
		child.CurrentToolStartedAt = 0
		child.UpdatedAt = now
	})
}

func requestExpiresAt(r *supervisorRequest, now time.Time) float64 {
	if r.ExpiresAt != nil {
		return *r.ExpiresAt
	}
	if r.CreatedAt != 0 {
		return r.CreatedAt + float64(supervisorAskTimeout().Milliseconds())
	}
	return float64(now.UnixMilli())
}

func asyncJobFinished(status string) bool {
	return status == "complete" || status == "failed" || status == "paused"
}

// requestRunInactive is whether the run a request is from can no longer be waiting on it.
func requestRunInactive(r *supervisorRequest, st *SubagentState) bool {
	if _, ok := st.ForegroundControls[r.RunID]; ok {
		return false
	}
	if run, child := rememberedForegroundChild(r, st); run != nil {
		return child.Status != "detached"
	}
	job := st.AsyncJobs[r.RunID]
	if job == nil {
		return false
	}
	if asyncJobFinished(job.Status) {
		return true
	}
	if r.ChildIndex >= 0 && r.ChildIndex < len(job.Steps) {
		switch job.Steps[r.ChildIndex].Status {
		case "complete", "completed", "failed", "paused":
			return true
		}
	}
	return false
}

func (c *nativeSupervisorChannel) runState(r *supervisorRequest) *SubagentState {
	if c.deps.GetCurrentOwnerStates != nil {
		for _, st := range c.deps.GetCurrentOwnerStates() {
			if _, ok := st.AsyncJobs[r.RunID]; ok {
				return st
			}
		}
	}
	return c.state
}

func (c *nativeSupervisorChannel) requestInactive(r *supervisorRequest) bool {
	var inactive bool
	c.withState(func() { inactive = requestRunInactive(r, c.runState(r)) })
	return inactive
}

func (c *nativeSupervisorChannel) requestLifecycle(r *pendingSupervisorRequest, now time.Time) supervisorLifecycle {
	if !c.ownerMatches(&r.supervisorRequest) {
		return lifecycleWrongSessn
	}
	if !c.deps.FS.Exists(r.RequestFile) {
		return lifecycleMissing
	}
	if r.ExpectsReply && c.deps.FS.Exists(supervisorReplyPath(r.ChannelDir, r.ID)) {
		return lifecycleResolved
	}
	if r.ExpectsReply && float64(now.UnixMilli()) > requestExpiresAt(&r.supervisorRequest, now) {
		return lifecycleExpired
	}
	if r.ExpectsReply && c.requestInactive(&r.supervisorRequest) {
		return lifecycleInactive
	}
	return lifecyclePending
}

func (c *nativeSupervisorChannel) cleanupLifecycle(r *pendingSupervisorRequest, lc supervisorLifecycle) {
	switch lc {
	case lifecycleResolved, lifecycleExpired, lifecycleInactive:
		_ = c.deps.FS.Remove(r.RequestFile)
	}
}

func (c *nativeSupervisorChannel) refreshPending() {
	now := c.now()
	for _, r := range c.pendingList() {
		lc := c.requestLifecycle(r, now)
		if lc == lifecyclePending {
			continue
		}
		c.removePending(r.ID)
		c.observeLifecycle(r, lc)
		c.cleanupLifecycle(r, lc)
	}
}

// ---- files ----

func (c *nativeSupervisorChannel) parseRequestFile(file, channelDir string) *pendingSupervisorRequest {
	b, err := c.deps.FS.ReadFile(file)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil || raw == nil {
		return nil
	}
	if raw["type"] != "subagent.supervisor.request" {
		return nil
	}
	id, _ := raw["id"].(string)
	if id == "" {
		return nil
	}
	reason, _ := raw["reason"].(string)
	if !isSupervisorReason(reason) {
		return nil
	}
	message, ok := raw["message"].(string)
	if !ok || (jsTrim(message) == "" && reason != "interview_request") {
		return nil
	}
	runID, ok1 := raw["runId"].(string)
	agent, ok2 := raw["agent"].(string)
	idx, ok3 := raw["childIndex"].(float64)
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	r := supervisorRequest{Type: "subagent.supervisor.request", ID: id, Reason: reason, Message: message, RunID: runID, Agent: agent, ChildIndex: int(idx)}
	r.CreatedAt, _ = raw["createdAt"].(float64)
	if e, ok := raw["expiresAt"].(float64); ok {
		r.ExpiresAt = &e
	}
	r.ExpectsReply, _ = raw["expectsReply"].(bool)
	r.OrchestratorTarget, _ = raw["orchestratorTarget"].(string)
	r.OrchestratorSessionID, _ = raw["orchestratorSessionId"].(string)
	r.ToolCallID, _ = raw["toolCallId"].(string)
	r.ChildTarget, _ = raw["childTarget"].(string)
	if iv, has := raw["interview"]; has {
		r.Interview = iv
	}
	return &pendingSupervisorRequest{supervisorRequest: r, ChannelDir: channelDir, RequestFile: file}
}

type requestFileRef struct{ channelDir, file string }

func (c *nativeSupervisorChannel) listRequestFiles(channelDirs []string, haveDirs bool) ([]requestFileRef, error) {
	if !haveDirs {
		entries, err := c.deps.FS.ReadDir(supervisorChannelRoot())
		if err != nil {
			if isMissingSupervisorDirectory(err, c.deps.Platform) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir {
				channelDirs = append(channelDirs, filepath.Join(supervisorChannelRoot(), e.Name))
			}
		}
	}
	var files []requestFileRef
	for _, dir := range channelDirs {
		reqDir := filepath.Join(dir, supervisorRequestsDir)
		entries, err := c.deps.FS.ReadDir(reqDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsFile && strings.HasSuffix(e.Name, ".json") {
				files = append(files, requestFileRef{dir, filepath.Join(reqDir, e.Name)})
			}
		}
	}
	return files, nil
}

func (c *nativeSupervisorChannel) readDirEntries(dir string) ([]supervisorEntry, bool) {
	entries, err := c.deps.FS.ReadDir(dir)
	if err != nil {
		if isMissingSupervisorDirectory(err, c.deps.Platform) {
			return nil, true
		}
		return nil, false
	}
	return entries, true
}

// isDirNotEmpty is the codes a directory that is not empty, or is busy, reports (ENOTEMPTY, EEXIST, EPERM, EBUSY).
func isDirNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EBUSY) || strings.Contains(strings.ToLower(err.Error()), "not empty")
}

func (c *nativeSupervisorChannel) removeEmptyDirectory(dir string) bool {
	err := c.deps.FS.Rmdir(dir)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if isDirNotEmpty(err) {
		return false
	}
	panic(err) // an unexpected failure ends this cleanup pass; the caller treats it as best effort
}

func (c *nativeSupervisorChannel) removeStaleEmptyChannel(channelDir string, now time.Time) (removed bool) {
	reqDir, repDir := filepath.Join(channelDir, supervisorRequestsDir), filepath.Join(channelDir, supervisorRepliesDir)
	newest := max(c.deps.FS.MtimeMs(channelDir), c.deps.FS.MtimeMs(reqDir), c.deps.FS.MtimeMs(repDir))
	if float64(now.UnixMilli())-newest < float64(staleEmptyChannelAge.Milliseconds()) {
		return false
	}
	if e, ok := c.readDirEntries(reqDir); !ok || len(e) > 0 {
		return false
	}
	if e, ok := c.readDirEntries(repDir); !ok || len(e) > 0 {
		return false
	}
	return c.removeEmptyDirectory(reqDir) && c.removeEmptyDirectory(repDir) && c.removeEmptyDirectory(channelDir)
}

func (c *nativeSupervisorChannel) cleanupStaleChannels() {
	entries, err := c.deps.FS.ReadDir(supervisorChannelRoot())
	if err != nil {
		return
	}
	now := c.now()
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		func() {
			defer func() { _ = recover() }() // cleanup is opportunistic: an active writer can race it and a later pass sees it
			c.removeStaleEmptyChannel(filepath.Join(supervisorChannelRoot(), e.Name), now)
		}()
	}
}

func (c *nativeSupervisorChannel) cleanupStaleIfDue() {
	if c.deps.GetChannelDirs != nil {
		return // the root owns global retention cleanup
	}
	now := c.now()
	if now.Sub(c.lastStaleClean) < staleEmptyChannelCleanupEvery {
		return
	}
	c.lastStaleClean = now
	c.cleanupStaleChannels()
}

// ---- text ----

func formatPendingLine(r *pendingSupervisorRequest) string {
	hint := ""
	if r.ExpectsReply {
		hint = " Reply: " + supervisorReplyHint(r.ID)
	}
	header := fmt.Sprintf("- %s: %s [%s#%d] %s.%s", r.ID, r.Agent, r.RunID, r.ChildIndex, r.Reason, hint)
	// The request notice can be missed; pending is the parent's only way to read the question again.
	if r.Message != "" {
		return header + "\n  " + strings.ReplaceAll(r.Message, "\n", "\n  ")
	}
	return header
}

func supervisorRequestVisibleText(r *pendingSupervisorRequest) string {
	lines := []string{supervisorReasonHeading(r.Reason), "Run: " + r.RunID, "Agent: " + r.Agent, fmt.Sprintf("Child index: %d", r.ChildIndex), ""}
	if r.Message != "" {
		lines = append(lines, r.Message)
	}
	if r.Reason == "interview_request" {
		lines = append(lines, "", "Structured response requested. Reply with JSON, optionally fenced in ```json, matching the requested interview shape.")
		if r.Interview != nil {
			b, _ := json.MarshalIndent(r.Interview, "", "\t")
			lines = append(lines, string(b))
		}
	}
	if r.ExpectsReply {
		lines = append(lines, "", "Reply with: "+supervisorReplyHint(r.ID))
	}
	runJSON, _ := marshalNoEscape(r.RunID)
	live := fmt.Sprintf(`Live guidance: subagent({ action: "steer", id: %s, index: %d, message: "..." })`, runJSON, r.ChildIndex)
	if r.ExpectsReply {
		live += " (Reply to the pending request first.)"
	}
	lines = append(lines, "", live)
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r")
}

func (c *nativeSupervisorChannel) writeReply(r *pendingSupervisorRequest, message string) (*supervisorReply, error) {
	if jsTrim(message) == "" {
		return nil, errors.New("message is required for supervisor replies.")
	}
	reply := &supervisorReply{Type: "subagent.supervisor.reply", RequestID: r.ID, CreatedAt: float64(c.now().UnixMilli()), Message: jsTrim(message)}
	if err := asyncWriteJSON(supervisorReplyPath(r.ChannelDir, r.ID), reply); err != nil {
		return nil, err
	}
	_ = c.deps.FS.Remove(r.RequestFile)
	return reply, nil
}

func (c *nativeSupervisorChannel) appendReplyEntry(r *pendingSupervisorRequest, reply *supervisorReply) {
	if c.host.AppendEntry == nil {
		return
	}
	data := map[string]any{"requestId": r.ID, "reason": r.Reason, "runId": r.RunID, "agent": r.Agent, "childIndex": r.ChildIndex, "message": reply.Message, "createdAt": reply.CreatedAt}
	if r.ChildTarget != "" {
		data["childTarget"] = r.ChildTarget
	}
	if err := c.host.AppendEntry(supervisorReplyEntryType, data); err != nil {
		// The reply file is authoritative; a UI journal failure must not undo a delivered reply.
		fmt.Fprintln(os.Stderr, "Failed to journal native supervisor reply:", err)
	}
}

type supervisorIntercomParams struct{ Action, To, Message, ReplyTo string }

func (c *nativeSupervisorChannel) resolvePending(p supervisorIntercomParams) (*pendingSupervisorRequest, error) {
	if p.ReplyTo != "" {
		r := c.pending[p.ReplyTo]
		if r == nil {
			return nil, fmt.Errorf("No pending supervisor request found for replyTo '%s'.", p.ReplyTo)
		}
		return r, nil
	}
	var requests []*pendingSupervisorRequest
	for _, r := range c.pendingList() {
		if r.ExpectsReply {
			requests = append(requests, r)
		}
	}
	if p.To != "" {
		to := strings.ToLower(p.To)
		var matches []*pendingSupervisorRequest
		for _, r := range requests {
			if strings.HasPrefix(strings.ToLower(r.ID), to) || strings.ToLower(r.Agent) == to || (r.ChildTarget != "" && strings.ToLower(r.ChildTarget) == to) {
				matches = append(matches, r)
			}
		}
		switch {
		case len(matches) == 1:
			return matches[0], nil
		case len(matches) > 1:
			return nil, fmt.Errorf("Multiple pending supervisor requests match '%s'. Use replyTo.", p.To)
		}
		return nil, fmt.Errorf("No pending supervisor request matches '%s'. Use replyTo.", p.To)
	}
	switch {
	case len(requests) == 1:
		return requests[0], nil
	case len(requests) == 0:
		return nil, errors.New("No pending supervisor requests need a reply.")
	}
	return nil, errors.New("Multiple pending supervisor requests need replies. Use replyTo.")
}

func (c *nativeSupervisorChannel) publicPending() []any {
	out := []any{}
	for _, r := range c.pendingList() {
		out = append(out, map[string]any{"id": r.ID, "runId": r.RunID, "agent": r.Agent, "childIndex": r.ChildIndex, "reason": r.Reason, "expectsReply": r.ExpectsReply})
	}
	return out
}

// execute is the parent's subagent_supervisor tool.
func (c *nativeSupervisorChannel) execute(p supervisorIntercomParams) (supervisorToolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Discover new request files even when demand-gated polling is idle.
	if err := c.poll(); err != nil {
		return supervisorToolResult{}, err
	}
	c.refreshPending()
	switch p.Action {
	case "status":
		return supervisorToolResult{Text: fmt.Sprintf("Native supervisor channel active. Pending replies: %d.", len(c.pending)),
			Details: map[string]any{"active": true, "pending": len(c.pending), "root": supervisorChannelRoot()}}, nil
	case "pending", "list":
		var lines []string
		for _, r := range c.pendingList() {
			if r.ExpectsReply {
				lines = append(lines, formatPendingLine(r))
			}
		}
		text := "No pending supervisor requests."
		if len(lines) > 0 {
			text = strings.Join(lines, "\n")
		}
		return supervisorToolResult{Text: text, Details: map[string]any{"pending": c.publicPending()}}, nil
	case "reply":
		r, err := c.resolvePending(p)
		if err != nil {
			return supervisorToolResult{}, err
		}
		reply, err := c.writeReply(r, p.Message)
		if err != nil {
			return supervisorToolResult{}, err
		}
		c.appendReplyEntry(r, reply)
		c.observeLifecycle(r, lifecycleResolved)
		c.removePending(r.ID)
		c.clearForegroundAttention(&r.supervisorRequest)
		return supervisorToolResult{Text: "Replied to supervisor request " + r.ID + ".", Details: map[string]any{"replyTo": r.ID, "runId": r.RunID, "agent": r.Agent}}, nil
	}
	return supervisorToolResult{}, fmt.Errorf("Unsupported supervisor action: %s", p.Action)
}

func supervisorParamsOf(m map[string]any) supervisorIntercomParams {
	p := supervisorIntercomParams{}
	p.Action, _ = m["action"].(string)
	p.To, _ = m["to"].(string)
	p.Message, _ = m["message"].(string)
	p.ReplyTo, _ = m["replyTo"].(string)
	return p
}

func (c *nativeSupervisorChannel) parentTool() *supervisorTool {
	return &supervisorTool{Name: nativeSupervisorToolName, Execute: func(_ string, params map[string]any, _ func() bool) (supervisorToolResult, error) {
		return c.execute(supervisorParamsOf(params))
	}}
}

// registerTools registers the parent's tool unless the host has one by that name.
func (c *nativeSupervisorChannel) registerTools() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerParentTools()
}

func (c *nativeSupervisorChannel) registerParentTools() {
	if !c.host.hasTool(nativeSupervisorToolName) && c.host.RegisterTool != nil {
		c.host.RegisterTool(c.parentTool())
	}
}

func (c *nativeSupervisorChannel) useNativeWatcher() bool {
	return c.deps.GetChannelDirs == nil && c.deps.Watch != nil && c.deps.Platform != "darwin" && c.deps.Platform != "windows"
}

func (c *nativeSupervisorChannel) hasTransportDemand() bool {
	if len(c.pending) > 0 {
		return true
	}
	demand := false
	c.withState(func() {
		if len(c.state.ForegroundControls) > 0 {
			demand = true
			return
		}
		for _, j := range c.state.AsyncJobs {
			if j.Status == "queued" || j.Status == "running" {
				demand = true
				return
			}
		}
	})
	if demand {
		return true
	}
	if c.deps.GetChannelDirs != nil {
		if dirs, _ := c.deps.GetChannelDirs(); len(dirs) > 0 {
			return true
		}
	}
	if c.deps.GetCurrentOwnerStates != nil {
		for _, st := range c.deps.GetCurrentOwnerStates() {
			c.withState(func() {
				for _, j := range st.AsyncJobs {
					if j.Status == "queued" || j.Status == "running" {
						demand = true
					}
				}
			})
		}
	}
	return demand
}

// pollLogged is poll for a caller that has nowhere to return the scan error to.
func (c *nativeSupervisorChannel) pollLogged() {
	if err := c.poll(); err != nil {
		fmt.Fprintln(os.Stderr, "Supervisor channel scan failed:", err)
	}
}

// poll registers the new requests, surfaces each as a user turn and retires the settled ones; it fails when the channel root (or a
// mailbox list) cannot be scanned.
func (c *nativeSupervisorChannel) poll() error {
	c.cleanupStaleIfDue()
	// Only display notifications require a live UI context, not request registration.
	c.refreshPending()
	now := c.now()
	var dirs []string
	var retire func()
	haveDirs := false
	if c.deps.GetChannelDirs != nil {
		dirs, retire = c.deps.GetChannelDirs()
		haveDirs = true
	}
	files, err := c.listRequestFiles(dirs, haveDirs)
	if err != nil {
		return err
	}
	for _, f := range files {
		if c.seenFiles[f.file] {
			continue
		}
		r := c.parseRequestFile(f.file, f.channelDir)
		if r == nil || !c.ownerMatches(&r.supervisorRequest) {
			continue
		}
		lc := c.requestLifecycle(r, now)
		if lc != lifecyclePending {
			c.seenFiles[f.file] = true
			c.observeLifecycle(r, lc)
			c.cleanupLifecycle(r, lc)
			continue
		}
		c.seenFiles[f.file] = true
		if !r.ExpectsReply {
			// Progress is already visible through child activity: no parent message and no parent model turn.
			_ = c.deps.FS.Remove(r.RequestFile)
			continue
		}
		c.rememberPending(r)
		c.addPending(r)
		c.markForegroundAttention(&r.supervisorRequest)
		// The ask is already queued above. A send failure (no UI, a stale context) must not lose it, nor stop the remaining asks.
		if c.host.SendMessage != nil {
			details := map[string]any{"id": r.ID, "requestId": r.ID, "reason": r.Reason, "expectsReply": r.ExpectsReply, "runId": r.RunID, "agent": r.Agent, "childIndex": r.ChildIndex,
				"requestBody": r.Message, "replyHint": supervisorReplyHint(r.ID)}
			if r.ChildTarget != "" {
				details["childTarget"] = r.ChildTarget
			}
			if r.Interview != nil {
				details["interview"] = r.Interview
			}
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						fmt.Fprintf(os.Stderr, "Failed to surface supervisor request %s as a user turn: %v\n", r.ID, rec)
					}
				}()
				if err := c.host.SendMessage(sdk.CustomMessage{CustomType: supervisorRequestMessageType, Content: supervisorRequestVisibleText(r), Display: true, Details: details}, true); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to surface supervisor request %s as a user turn: %v\n", r.ID, err)
				}
			}()
		}
		if c.host.Emit != nil {
			c.host.Emit(intercomDetachRequestEvent, map[string]any{"requestId": r.ID, "runId": r.RunID, "agent": r.Agent, "childIndex": r.ChildIndex})
		}
		if _, still := c.pending[r.ID]; still {
			c.markForegroundAttention(&r.supervisorRequest)
		}
	}
	if retire != nil {
		retire()
	}
	return nil
}

func (c *nativeSupervisorChannel) startPolling() {
	if c.poller != nil {
		return
	}
	c.poller = c.deps.Timers.Interval(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.safePoll()
		if !c.useNativeWatcher() && (c.deps.Platform == "darwin" || c.deps.GetChannelDirs != nil) && !c.hasTransportDemand() {
			if c.poller != nil {
				c.poller()
			}
			c.poller = nil
		}
	}, supervisorChannelPoll)
}

// safePoll is poll for a timer: a failure of the scan ends this pass, never the channel (the original's timers would throw into
// the event loop; a Windows UNKNOWN scan error must not end the polling).
func (c *nativeSupervisorChannel) safePoll() {
	defer func() { _ = recover() }()
	c.pollLogged()
}

func (c *nativeSupervisorChannel) startSafetyPolling() {
	if c.safetyPoller != nil {
		return
	}
	c.safetyPoller = c.deps.Timers.Interval(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.watchExistingRequestDirs()
		c.safePoll()
	}, supervisorChannelSafetyPoll)
}

func (c *nativeSupervisorChannel) watchRequestDir(reqDir string) {
	if _, ok := c.requestWatchers[reqDir]; ok || c.deps.Watch == nil {
		return
	}
	closer, err := c.deps.Watch(reqDir, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.safePoll()
	})
	if err != nil {
		if errorCode(err) != "ENOENT" {
			c.startPolling()
		}
		return
	}
	c.requestWatchers[reqDir] = closer
}

func (c *nativeSupervisorChannel) watchExistingRequestDirs() {
	entries, err := c.deps.FS.ReadDir(supervisorChannelRoot())
	if err != nil {
		if isMissingSupervisorDirectory(err, c.deps.Platform) {
			return
		}
		c.startPolling()
		return
	}
	for _, e := range entries {
		if e.IsDir {
			c.watchRequestDir(filepath.Join(supervisorChannelRoot(), e.Name, supervisorRequestsDir))
		}
	}
}

func (c *nativeSupervisorChannel) scheduleWatcherRefresh() {
	if c.deferredRefresh != nil {
		return
	}
	c.deferredRefresh = c.deps.Timers.Immediate(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.deferredRefresh = nil
		if !c.started {
			return
		}
		c.watchExistingRequestDirs()
		c.safePoll()
	})
}

// activateTransport polls once, and keeps polling while child work is live (where there is no native watcher).
func (c *nativeSupervisorChannel) activateTransport() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.pollLogged()
	if !c.useNativeWatcher() && c.hasTransportDemand() {
		c.startPolling()
	}
}

// findPendingAsks is the pending asks of one child, by a read-only look at its mailbox: nothing is registered, shown, removed or
// replied to.
func (c *nativeSupervisorChannel) findPendingAsks(runID, agent string, childIndex int) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	channelDir := resolveSupervisorChannelDir(runID, agent, childIndex)
	entries, err := c.deps.FS.ReadDir(filepath.Join(channelDir, supervisorRequestsDir))
	if err != nil {
		if isMissingSupervisorDirectory(err, c.deps.Platform) {
			return nil, nil
		}
		return nil, err
	}
	now := c.now()
	var ids []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		r := c.parseRequestFile(filepath.Join(channelDir, supervisorRequestsDir, e.Name), channelDir)
		if r != nil && r.ExpectsReply && r.RunID == runID && r.Agent == agent && r.ChildIndex == childIndex && c.requestLifecycle(r, now) == lifecyclePending {
			ids = append(ids, r.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// hasPendingRequests polls and says whether a reply-bearing request waits (a drain barrier).
func (c *nativeSupervisorChannel) hasPendingRequests() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return false
	}
	c.pollLogged()
	return len(c.pending) > 0
}

// start registers the tool and does the first poll; where the platform has a native watcher it also watches the channel root.
func (c *nativeSupervisorChannel) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.started = true
	c.registerParentTools()
	c.pollLogged()
	if c.deps.GetChannelDirs != nil {
		return // child polling starts only after a descendant launch
	}
	if err := c.deps.FS.MkdirAll(supervisorChannelRoot()); err != nil {
		c.startPolling()
		return
	}
	if !c.useNativeWatcher() {
		if c.deps.Platform == "windows" {
			c.startPolling()
		}
		return
	}
	c.watchExistingRequestDirs()
	closer, err := c.deps.Watch(supervisorChannelRoot(), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.watchExistingRequestDirs()
		c.safePoll()
		c.scheduleWatcherRefresh()
	})
	if err != nil {
		c.startPolling()
		return
	}
	c.rootWatcher = closer
	c.startSafetyPolling()
	c.scheduleWatcherRefresh()
}

func (c *nativeSupervisorChannel) dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = false
	if c.rootWatcher != nil {
		c.rootWatcher()
		c.rootWatcher = nil
	}
	for _, closer := range c.requestWatchers {
		closer()
	}
	c.requestWatchers = map[string]func(){}
	if c.poller != nil {
		c.poller()
		c.poller = nil
	}
	if c.safetyPoller != nil {
		c.safetyPoller()
		c.safetyPoller = nil
	}
	if c.deferredRefresh != nil {
		c.deferredRefresh()
		c.deferredRefresh = nil
	}
	c.pending, c.pendingOrder = map[string]*pendingSupervisorRequest{}, nil
	c.corr, c.corrOrder = map[string]*supervisorCorrelation{}, nil
	c.seenFiles = map[string]bool{}
}

// supervisorRequestState is whether the supervisor request an attention event was raised for is still waiting.
type supervisorRequestState string

const (
	supervisorRequestPending  supervisorRequestState = "pending"
	supervisorRequestResolved supervisorRequestState = "resolved"
	supervisorRequestUnknown  supervisorRequestState = "unknown"
)

// getSupervisorRequestState correlates a control event (a contact_supervisor tool call) with the request it made.
func (c *nativeSupervisorChannel) getSupervisorRequestState(e controlEvent) supervisorRequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.CurrentTool == "intercom" || e.Index == nil || e.ToolCallID == "" {
		return supervisorRequestUnknown
	}
	c.pruneCorrelations()
	key := correlationKey(e.RunID, e.Agent, *e.Index, e.ToolCallID)
	cr := c.corr[key]
	if key == "" || cr == nil {
		return supervisorRequestUnknown
	}
	if cr.resolved {
		return supervisorRequestResolved
	}
	now := c.now()
	r := cr.request
	if !c.deps.FS.Exists(r.RequestFile) || c.deps.FS.Exists(supervisorReplyPath(r.ChannelDir, r.ID)) || float64(now.UnixMilli()) > requestExpiresAt(&r.supervisorRequest, now) ||
		c.requestInactive(&r.supervisorRequest) {
		c.rememberResolved(r)
		return supervisorRequestResolved
	}
	return supervisorRequestPending
}
