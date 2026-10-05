package acp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/pirpc"
)

// Version is the adapter version reported in agentInfo.
var Version = "0.1.0"

const (
	modelConfigID        = "model"
	thoughtLevelConfigID = "thought_level"
	listPageSize         = 50
	maxChangelogChars    = 20000
)

// Agent implements the ACP agent methods over pig RPC children (PiAcpAgent of the original).
type Agent struct {
	*agentState
	// schedule runs fn once the response of the request being handled has been sent
	// (the original's setTimeout(fn, 0) after session/new and session/load).
	schedule func(fn func())
}

// agentState is shared by the per-request views of an Agent.
type agentState struct {
	conn     Conn
	sessions SessionRegistry
	store    Store
	spawn    SpawnFunc

	// piCommand and piArgs select the pig executable and extra arguments (--piglet ...).
	piCommand string
	piArgs    []string

	mu             sync.Mutex
	lastSessionCwd string
	restoreMu      sync.Mutex

	// changelogPath finds the changelog for /changelog (a test seam).
	changelogPath func() string
}

// Scoped returns a view of the agent whose deferred work goes to schedule.
func (a *Agent) Scoped(schedule func(fn func())) *Agent {
	c := *a
	c.schedule = schedule
	return &c
}

// NewAgent returns an agent that talks to the client over conn. Its sessions start
// `pig --mode rpc` children and are recorded in the session map under PIG_HOME.
func NewAgent(conn Conn) *Agent {
	st := &agentState{conn: conn}
	st.store = NewFileStore("")
	st.spawn = st.defaultSpawn
	st.sessions = NewSessionManager(st.spawn, st.store)
	return &Agent{agentState: st}
}

// Configure selects the pig executable ("" = the default) and extra pig arguments.
func (a *Agent) Configure(piCommand string, piArgs []string) {
	a.piCommand, a.piArgs = piCommand, piArgs
}

func (st *agentState) defaultSpawn(p SpawnParams) (Proc, error) {
	proc, err := pirpc.SpawnWithArgs(p.Cwd, p.PiCommand, p.SessionPath, st.piArgs)
	if err != nil {
		return nil, err
	}
	return proc, nil
}

// pigCommand is the executable to start: the configured one, else the environment override.
func (st *agentState) pigCommand() string {
	if st.piCommand != "" {
		return st.piCommand
	}
	return envFlag("PIG_ACP_PIG_COMMAND", "PI_ACP_PI_COMMAND")
}

func (a *Agent) defer_(fn func()) {
	if a.schedule != nil {
		a.schedule(fn)
		return
	}
	go fn()
}

// Dispose kills every session's pig child.
func (a *Agent) Dispose() { a.sessions.DisposeAll() }

func envFlag(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// Initialize negotiates the protocol version and advertises only what is implemented.
func (a *Agent) Initialize(req InitializeRequest) (InitializeResponse, error) {
	terminalAuth := false
	if meta, ok := req.ClientCapabilities["_meta"].(map[string]any); ok {
		terminalAuth = meta["terminal-auth"] == true
	}
	return InitializeResponse{
		// Only protocol version 1 is spoken: a client asking for another gets 1 and decides.
		ProtocolVersion: ProtocolVersion,
		AgentInfo:       map[string]any{"name": "pig-acp", "title": "PiG ACP adapter", "version": Version},
		AuthMethods:     AuthMethods(terminalAuth),
		AgentCapabilities: AgentCapabilities{
			LoadSession:     true,
			McpCapabilities: McpCapabilities{HTTP: false, SSE: false},
			PromptCapabilities: PromptCapabilities{
				Image: true, Audio: false,
				EmbeddedContext: envFlag("PIG_ACP_ENABLE_EMBEDDED_CONTEXT", "PI_ACP_ENABLE_EMBEDDED_CONTEXT") == "true",
			},
			SessionCapabilities: SessionCapabilities{List: map[string]any{}, Delete: map[string]any{}},
		},
	}, nil
}

func (a *Agent) setLastCwd(cwd string) {
	a.mu.Lock()
	a.lastSessionCwd = cwd
	a.mu.Unlock()
}

func (a *Agent) getLastCwd() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastSessionCwd
}

func (a *Agent) cleanupFailedNewSession(sessionID string, state map[string]any) {
	a.sessions.Close(sessionID)
	file := ""
	if f, ok := state["sessionFile"].(string); ok && jsTrim(f) != "" {
		file = f
	} else if e := a.store.Get(sessionID); e != nil {
		file = e.SessionFile
	}
	if jsTrim(file) != "" {
		// Best effort: the auth or internal error is the primary result.
		_ = os.Remove(file)
	}
	a.store.Delete(sessionID)
}

type storedRef struct{ cwd, sessionFile string }

func (a *Agent) findStoredSession(sessionID string) *storedRef {
	if e := a.store.Get(sessionID); e != nil && e.Cwd != "" && e.SessionFile != "" {
		return &storedRef{e.Cwd, e.SessionFile}
	}
	ps := FindPiSession(sessionID)
	if ps == nil {
		return nil
	}
	a.store.Upsert(StoredSession{SessionID: sessionID, Cwd: ps.Cwd, SessionFile: ps.SessionFile})
	return &storedRef{ps.Cwd, ps.SessionFile}
}

func spawnErrorToRequestError(err error) error {
	var sc interface{ SpawnCode() string }
	if errors.As(err, &sc) {
		data := map[string]any{}
		if c := sc.SpawnCode(); c != "" {
			data["code"] = c
		}
		return ErrInternal(data, err.Error())
	}
	return err
}

// restoreSession returns the live session, or starts a child around the stored session file.
func (a *Agent) restoreSession(sessionID string, cwd string, mcp []any) (ActiveSession, error) {
	// One restore at a time: a second request for the same session waits, then finds it live.
	a.restoreMu.Lock()
	defer a.restoreMu.Unlock()
	if s := a.sessions.MaybeGet(sessionID); s != nil {
		return s, nil
	}
	stored := a.findStoredSession(sessionID)
	if stored == nil {
		return nil, ErrInvalidParams(nil, "Unknown sessionId: "+sessionID)
	}
	if cwd == "" {
		cwd = stored.cwd
	}
	proc, err := a.spawn(SpawnParams{Cwd: cwd, SessionPath: stored.sessionFile, PiCommand: a.pigCommand()})
	if err != nil {
		return nil, spawnErrorToRequestError(err)
	}
	if mcp == nil {
		mcp = []any{}
	}
	sess := a.sessions.GetOrCreate(sessionID, SessionCreateParams{Cwd: cwd, McpServers: mcp, Conn: a.conn, Proc: proc, FileCommands: LoadSlashCommands(cwd)})
	a.setLastCwd(cwd)
	a.store.Upsert(StoredSession{SessionID: sessionID, Cwd: cwd, SessionFile: stored.sessionFile})
	return sess, nil
}

// ---- session configuration (models, thinking levels) ----

type sessionConfiguration struct {
	ConfigOptions []ConfigOption
	Models        *ModelState
	Modes         ModeState
}

func getThinkingState(proc Proc, state map[string]any) (ModeState, error) {
	if state == nil {
		var err error
		if state, err = proc.GetState(); err != nil {
			return ModeState{}, err
		}
	}
	available, err := proc.GetAvailableThinkingLevels()
	if err != nil {
		return ModeState{}, err
	}
	current, _ := state["thinkingLevel"].(string)
	found := false
	for _, l := range available {
		if l == current {
			found = true
		}
	}
	if current == "" || !found {
		return ModeState{}, errors.New("pi returned a thinking level absent from available levels")
	}
	modes := make([]Mode, len(available))
	for i, id := range available {
		modes[i] = Mode{ID: id, Name: "Thinking: " + id}
	}
	return ModeState{CurrentModeID: current, AvailableModes: modes}, nil
}

func getModelState(proc Proc, state, data map[string]any, haveState, haveModels bool) *ModelState {
	if !haveModels {
		if d, err := proc.GetAvailableModels(); err == nil {
			data = d
		} else {
			data = nil
		}
	}
	raw, _ := data["models"].([]any)
	available := []AdvertisedModel{}
	for _, item := range raw {
		m := asObject(item)
		provider := jsTrim(jsStr(m["provider"], ""))
		id := jsTrim(jsStr(m["id"], ""))
		if provider == "" || id == "" {
			continue
		}
		name := jsStr(m["name"], id)
		available = append(available, AdvertisedModel{ModelID: provider + "/" + id, Name: provider + "/" + name})
	}
	if !haveState {
		if s, err := proc.GetState(); err == nil {
			state = s
		} else {
			state = nil
		}
	}
	current := ""
	if model := asObject(state["model"]); model != nil {
		provider := jsTrim(jsStr(model["provider"], ""))
		id := jsTrim(jsStr(model["id"], ""))
		if provider != "" && id != "" {
			current = provider + "/" + id
		}
	}
	if len(available) == 0 && current == "" {
		return nil
	}
	if current == "" {
		current = "default"
		if len(available) > 0 {
			current = available[0].ModelID
		}
	}
	return &ModelState{AvailableModels: available, CurrentModelID: current}
}

func buildConfigOptions(models *ModelState, modes ModeState) []ConfigOption {
	thought := ConfigOption{Type: "select", ID: thoughtLevelConfigID, Category: "thought_level", Name: "Thinking",
		Description: "Set the reasoning effort for this session", CurrentValue: modes.CurrentModeID}
	for _, m := range modes.AvailableModes {
		thought.Options = append(thought.Options, ConfigSelectOption{Value: m.ID, Name: m.Name, Description: m.Description})
	}
	out := []ConfigOption{thought}
	if models != nil && len(models.AvailableModels) > 0 {
		model := ConfigOption{Type: "select", ID: modelConfigID, Category: "model", Name: "Model",
			Description: "Select the model for this session", CurrentValue: models.CurrentModelID}
		for _, m := range models.AvailableModels {
			model.Options = append(model.Options, ConfigSelectOption{Value: m.ModelID, Name: m.Name, Description: m.Description})
		}
		out = append([]ConfigOption{model}, out...)
	}
	return out
}

// getSessionConfiguration reads models and thinking levels. pre carries values the caller already
// has (state, models); nil means "ask pig".
func getSessionConfiguration(proc Proc, state, models map[string]any, haveModels bool) (sessionConfiguration, error) {
	haveState := state != nil
	if !haveState {
		s, err := proc.GetState()
		if err != nil {
			return sessionConfiguration{}, err
		}
		state, haveState = s, true
	}
	ms := getModelState(proc, state, models, haveState, haveModels)
	modes, err := getThinkingState(proc, state)
	if err != nil {
		return sessionConfiguration{}, err
	}
	return sessionConfiguration{ConfigOptions: buildConfigOptions(ms, modes), Models: ms, Modes: modes}, nil
}

func (a *Agent) emitConfigOptionsUpdate(sessionID string, proc Proc) ([]ConfigOption, error) {
	cfg, err := getSessionConfiguration(proc, nil, nil, false)
	if err != nil {
		return nil, err
	}
	if err := a.conn.SessionUpdate(sessionID, Update{"sessionUpdate": "current_mode_update", "currentModeId": cfg.Modes.CurrentModeID}); err != nil {
		return nil, err
	}
	if err := a.conn.SessionUpdate(sessionID, Update{"sessionUpdate": "config_option_update", "configOptions": cfg.ConfigOptions}); err != nil {
		return nil, err
	}
	return cfg.ConfigOptions, nil
}

func setSessionModel(proc Proc, requested string) error {
	provider, modelID := "", ""
	if strings.Contains(requested, "/") {
		parts := strings.Split(requested, "/")
		provider, modelID = parts[0], strings.Join(parts[1:], "/")
	} else {
		modelID = requested
	}
	if provider == "" {
		data, err := proc.GetAvailableModels()
		if err != nil {
			return err
		}
		models, _ := data["models"].([]any)
		for _, item := range models {
			m := asObject(item)
			if jsString(m["id"]) == modelID {
				provider, modelID = jsString(m["provider"]), jsString(m["id"])
				break
			}
		}
	}
	if provider == "" || modelID == "" {
		return ErrInvalidParams(nil, "Unknown modelId: "+requested)
	}
	return proc.SetModel(provider, modelID)
}

// ---- session lifecycle ----

func (a *Agent) commandsTask(sess ActiveSession, enableSkills bool, fileCommands []FileSlashCommand) func() {
	return func() {
		// Publish real context usage now that the client knows the session id (clients ignore
		// notifications for unknown sessions), so the window size is right before the first prompt.
		sess.PublishContextUsage()
		var commands []AvailableCommand
		if data, err := sess.Proc().GetCommands(); err == nil {
			commands = ToAvailableCommandsFromPiGetCommands(data, PiCommandsOptions{EnableSkillCommands: enableSkills})
		} else {
			// Fall back to the prompt template files.
			commands = ToAvailableCommands(fileCommands)
		}
		_ = a.conn.SessionUpdate(sess.ID(), Update{"sessionUpdate": "available_commands_update", "availableCommands": MergeCommands(commands, BuiltinAvailableCommands())})
	}
}

func messageOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func authRequired() *RequestError {
	return ErrAuthRequired(map[string]any{"authMethods": AuthMethods(true)}, "Configure an API key or log in with an OAuth provider.")
}

// NewSession starts a pig child for a new session.
func (a *Agent) NewSession(req NewSessionRequest) (NewSessionResponse, error) {
	if !filepath.IsAbs(req.Cwd) {
		return NewSessionResponse{}, ErrInvalidParams(nil, "cwd must be an absolute path: "+req.Cwd)
	}
	a.setLastCwd(req.Cwd)
	fileCommands := LoadSlashCommands(req.Cwd)
	enableSkills := GetEnableSkillCommands(req.Cwd)

	// pig has no MCP client inside this adapter: servers are accepted and stored, not started.
	sess, err := a.sessions.Create(SessionCreateParams{Cwd: req.Cwd, McpServers: req.McpServers, Conn: a.conn, FileCommands: fileCommands, PiCommand: a.pigCommand()})
	if err != nil {
		return NewSessionResponse{}, err
	}
	proc := sess.Proc()

	// State and models once, in parallel, to cut startup latency.
	var state, models map[string]any
	var stateErr, modelsErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); state, stateErr = proc.GetState() }()
	go func() { defer wg.Done(); models, modelsErr = proc.GetAvailableModels() }()
	wg.Wait()
	if stateErr != nil {
		state = nil
	}

	if modelsErr != nil {
		a.cleanupFailedNewSession(sess.ID(), state)
		if ae := MaybeAuthRequiredError(modelsErr); ae != nil {
			return NewSessionResponse{}, ae
		}
		return NewSessionResponse{}, ErrInternal(map[string]any{}, messageOf(modelsErr))
	}
	// No models after spawning means pig has no credentials.
	if raw, _ := models["models"].([]any); len(raw) == 0 {
		a.cleanupFailedNewSession(sess.ID(), state)
		return NewSessionResponse{}, authRequired()
	}
	if stateErr != nil && MaybeAuthRequiredError(stateErr) != nil {
		a.cleanupFailedNewSession(sess.ID(), state)
		return NewSessionResponse{}, authRequired()
	}
	var cfg sessionConfiguration
	if stateErr != nil {
		err = stateErr
	} else {
		cfg, err = getSessionConfiguration(proc, state, models, true)
	}
	if err != nil {
		a.cleanupFailedNewSession(sess.ID(), state)
		if ae := MaybeAuthRequiredError(err); ae != nil {
			return NewSessionResponse{}, ae
		}
		return NewSessionResponse{}, ErrInternal(map[string]any{}, messageOf(err))
	}

	// PiG has no npm update check (`pig update` uses a signed manifest), so quietStartup simply
	// suppresses the startup block: see PORT.md, gap A18.
	prelude := ""
	if !GetQuietStartup(req.Cwd) {
		prelude = BuildStartupInfo(req.Cwd, a.pigCommand())
	}
	if prelude != "" {
		sess.SetStartupInfo(prelude)
	}

	// Within one ACP connection (one editor window) only one live pig child is kept, so a client
	// that starts new sessions without closing old ones does not leak processes.
	a.sessions.CloseAllExcept(sess.ID())

	var startup any
	if prelude != "" {
		startup = prelude
	}
	resp := NewSessionResponse{SessionID: sess.ID(), ConfigOptions: cfg.ConfigOptions, Models: cfg.Models, Modes: cfg.Modes,
		Meta: map[string]any{"piAcp": map[string]any{"startupInfo": startup}}}

	// Sent after the response: some clients ignore notifications for a session id they have not seen.
	if prelude != "" {
		a.defer_(sess.SendStartupInfoIfPending)
	}
	a.defer_(a.commandsTask(sess, enableSkills, fileCommands))
	return resp, nil
}

// Authenticate is a successful no-op: sign-in is the terminal login (`pig-acp --terminal-login`).
func (a *Agent) Authenticate(req AuthenticateRequest) error { return nil }

// Cancel aborts the running turn of a live session; unknown ids are ignored, never restored.
func (a *Agent) Cancel(sessionID string) error {
	s := a.sessions.MaybeGet(sessionID)
	if s == nil {
		return nil
	}
	return s.Cancel()
}

// ListSessions lists the PiG sessions on disk, scoped to a cwd (the request's, else the last used:
// Zed sends none, and pi's /resume picker is project scoped).
func (a *Agent) ListSessions(req ListSessionsRequest) (ListSessionsResponse, error) {
	all := ListPiSessions()
	cwd := a.getLastCwd()
	if req.Cwd != nil {
		cwd = *req.Cwd
	}
	filtered := all
	if cwd != "" {
		filtered = nil
		for _, s := range all {
			if s.Cwd == cwd {
				filtered = append(filtered, s)
			}
		}
	}
	start := 0
	if req.Cursor != nil && *req.Cursor != "" {
		if n, ok := parseIntPrefix(*req.Cursor); ok && n > 0 {
			start = n
		}
	}
	end := start + listPageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	if end > len(filtered) {
		end = len(filtered)
	}
	sessions := []SessionInfo{}
	for _, s := range filtered[start:end] {
		sessions = append(sessions, SessionInfo{SessionID: s.SessionID, Cwd: s.Cwd, Title: s.Title, UpdatedAt: s.UpdatedAt})
	}
	var next *string
	if start+listPageSize < len(filtered) {
		n := strconv.Itoa(start + listPageSize)
		next = &n
	}
	return ListSessionsResponse{Sessions: sessions, NextCursor: next, Meta: map[string]any{}}, nil
}

var intPrefix = regexp.MustCompile(`^\s*[+-]?\d+`)

// parseIntPrefix is Number.parseInt(s, 10) without the NaN: ok is false for no digits.
func parseIntPrefix(s string) (int, bool) {
	m := intPrefix.FindString(s)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(m))
	return n, err == nil
}

func (a *Agent) send(sessionID string, u Update) error { return a.conn.SessionUpdate(sessionID, u) }

// LoadSession reattaches to a stored session and replays its history.
func (a *Agent) LoadSession(req LoadSessionRequest) (LoadSessionResponse, error) {
	if !filepath.IsAbs(req.Cwd) {
		return LoadSessionResponse{}, ErrInvalidParams(nil, "cwd must be an absolute path: "+req.Cwd)
	}
	// A client that reloads a live session gets a fresh child, and its commands advertised again.
	a.sessions.Close(req.SessionID)
	a.setLastCwd(req.Cwd)
	stored := a.findStoredSession(req.SessionID)
	if stored == nil {
		return LoadSessionResponse{}, ErrInvalidParams(nil, "Unknown sessionId: "+req.SessionID)
	}
	enableSkills := GetEnableSkillCommands(req.Cwd)
	sess, err := a.restoreSession(req.SessionID, req.Cwd, req.McpServers)
	if err != nil {
		return LoadSessionResponse{}, err
	}
	proc := sess.Proc()
	cfg, err := getSessionConfiguration(proc, nil, nil, false)
	if err != nil {
		// The restored child is closed; the stored history and map entry stay as they were.
		a.sessions.Close(sess.ID())
		return LoadSessionResponse{}, err
	}
	fileCommands := LoadSlashCommands(req.Cwd)
	a.sessions.CloseAllExcept(sess.ID())
	a.store.Upsert(StoredSession{SessionID: req.SessionID, Cwd: req.Cwd, SessionFile: stored.sessionFile})

	data, err := proc.GetMessages()
	if err != nil {
		return LoadSessionResponse{}, err
	}
	messages, _ := data["messages"].([]any)
	for _, item := range messages {
		if err := a.replayMessage(sess.ID(), req.Cwd, asObject(item), item); err != nil {
			return LoadSessionResponse{}, err
		}
	}

	a.defer_(a.commandsTask(sess, enableSkills, fileCommands))
	return LoadSessionResponse{ConfigOptions: cfg.ConfigOptions, Models: cfg.Models, Modes: cfg.Modes,
		Meta: map[string]any{"piAcp": map[string]any{"startupInfo": nil}}}, nil
}

func (a *Agent) replayMessage(sessionID, cwd string, m map[string]any, raw any) error {
	switch jsStr(m["role"], "") {
	case "user":
		if text := NormalizePiMessageText(m["content"]); text != "" {
			return a.send(sessionID, textChunk("user_message_chunk", text))
		}
	case "assistant":
		if text := NormalizePiAssistantText(m["content"]); text != "" {
			return a.send(sessionID, textChunk("agent_message_chunk", text))
		}
	case "toolResult":
		toolName := jsStr(m["toolName"], "tool")
		id := newUUID()
		if v, ok := m["toolCallId"]; ok && v != nil {
			id = jsString(v)
		}
		isError := truthy(m["isError"])
		status := "completed"
		if isError {
			status = "failed"
		}
		if IsBashTool(toolName) {
			text := BashResultText(m)
			title := toolName
			if c, ok := BashCommand(m); ok {
				title = c
			}
			if err := a.send(sessionID, Update{"sessionUpdate": "tool_call", "toolCallId": id, "title": title, "kind": "execute",
				"status": "completed", "content": BashTerminalContent(id), "_meta": BashTerminalInfoMeta(id, cwd)}); err != nil {
				return err
			}
			meta := map[string]any{}
			if text != "" {
				for k, v := range BashTerminalOutputMeta(id, text) {
					meta[k] = v
				}
			}
			for k, v := range BashTerminalExitMeta(id, BashExitCode(m, isError)) {
				meta[k] = v
			}
			return a.send(sessionID, Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "_meta": meta})
		}
		// A synthetic tool call renders historic tool use.
		if err := a.send(sessionID, Update{"sessionUpdate": "tool_call", "toolCallId": id, "title": toolName, "kind": toToolKind(toolName),
			"status": "completed", "rawInput": nil, "rawOutput": raw}); err != nil {
			return err
		}
		var content any
		if text := ToolResultToText(m); text != "" {
			content = textContent(text)
		}
		return a.send(sessionID, Update{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status, "content": content, "rawOutput": raw})
	}
	return nil
}

// DeleteSession removes a session file; unknown ids succeed (session/delete is idempotent).
func (a *Agent) DeleteSession(req DeleteSessionRequest) (map[string]any, error) {
	stored := a.store.Get(req.SessionID)
	ps := FindPiSession(req.SessionID)
	if stored == nil && ps == nil {
		return map[string]any{}, nil
	}
	file := ""
	if stored != nil {
		file = stored.SessionFile
	} else if ps != nil {
		file = ps.SessionFile
	}
	if file != "" {
		_ = os.Remove(file) // best effort
	}
	a.store.Delete(req.SessionID)
	return map[string]any{}, nil
}

// UnstableSetSessionModel selects a model (session/set_model).
func (a *Agent) UnstableSetSessionModel(req SetSessionModelRequest) error {
	sess, err := a.restoreSession(req.SessionID, "", nil)
	if err != nil {
		return err
	}
	if err := setSessionModel(sess.Proc(), req.ModelID); err != nil {
		return err
	}
	if _, err := a.emitConfigOptionsUpdate(sess.ID(), sess.Proc()); err != nil {
		return err
	}
	sess.PublishContextUsage()
	return nil
}

// SetSessionMode maps a mode id to a thinking level.
func (a *Agent) SetSessionMode(req SetSessionModeRequest) (map[string]any, error) {
	sess, err := a.restoreSession(req.SessionID, "", nil)
	if err != nil {
		return nil, err
	}
	mode, ok := req.ModeID.(string)
	if !ok || mode == "" {
		return nil, ErrInvalidParams(nil, "Expected nonempty string modeId")
	}
	if err := sess.Proc().SetThinkingLevel(mode); err != nil {
		return nil, err
	}
	if _, err := a.emitConfigOptionsUpdate(sess.ID(), sess.Proc()); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

// SetSessionConfigOption changes the model or the thinking level.
func (a *Agent) SetSessionConfigOption(req SetSessionConfigOptionRequest) (SetSessionConfigOptionResponse, error) {
	sess, err := a.restoreSession(req.SessionID, "", nil)
	if err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	value, ok := req.Value.(string)
	if !ok {
		return SetSessionConfigOptionResponse{}, ErrInvalidParams(nil, "Expected string value for config option: "+req.ConfigID)
	}
	modelChanged := false
	switch req.ConfigID {
	case modelConfigID:
		if err := setSessionModel(sess.Proc(), value); err != nil {
			return SetSessionConfigOptionResponse{}, err
		}
		modelChanged = true
	case thoughtLevelConfigID:
		if value == "" {
			return SetSessionConfigOptionResponse{}, ErrInvalidParams(nil, "Expected nonempty thinking level")
		}
		if err := sess.Proc().SetThinkingLevel(value); err != nil {
			return SetSessionConfigOptionResponse{}, err
		}
	default:
		return SetSessionConfigOptionResponse{}, ErrInvalidParams(nil, "Unknown config option: "+req.ConfigID)
	}
	opts, err := a.emitConfigOptionsUpdate(sess.ID(), sess.Proc())
	if err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	// A different model can mean a different context window: refresh it now.
	if modelChanged {
		sess.PublishContextUsage()
	}
	return SetSessionConfigOptionResponse{ConfigOptions: opts}, nil
}

// FindChangelog looks for a CHANGELOG.md beside the pig installation: next to the executable or
// one directory up (bin/pig with a CHANGELOG.md at the root). "" when there is none. PiG binaries
// carry no changelog of their own, so this is only found for installs that ship one.
func FindChangelog(pigExecutable string) string {
	if pigExecutable == "" {
		pigExecutable = pirpc.Command("")
	}
	path := pigExecutable
	if !strings.ContainsAny(path, `/\`) {
		p, err := exec.LookPath(path)
		if err != nil {
			return ""
		}
		path = p
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(resolved)
	for _, root := range []string{filepath.Dir(dir), dir} {
		p := filepath.Join(root, "CHANGELOG.md")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func timeNowUTC() time.Time { return time.Now().UTC() }
