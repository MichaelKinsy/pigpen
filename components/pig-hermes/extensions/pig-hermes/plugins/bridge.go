package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const (
	defaultLoadTimeout = 5 * time.Second
	defaultCallTimeout = 30 * time.Second
	maxPromptChars     = 8000
	pluginMessageType  = "hermes-plugin"
)

// Wire registration payloads returned by the Python worker's load_plugin RPC.
type wireToolDef struct {
	Name        string         `json:"name"`
	Toolset     string         `json:"toolset"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Emoji       string         `json:"emoji"`
	Override    bool           `json:"override"`
}

type wireHookDef struct {
	RefID      string `json:"ref_id"`
	HookName   string `json:"hook_name"`
	HoldStream bool   `json:"hold_stream"`
}

type wireCommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ArgsHint    string `json:"args_hint"`
}

type wirePromptSectionDef struct {
	ID       string `json:"id"`
	Position string `json:"position"`
	MaxChars int    `json:"max_chars"`
	Dynamic  bool   `json:"dynamic"`
	Content  string `json:"content"`
}

type wireLoadResult struct {
	Key            string                 `json:"key"`
	Tools          []wireToolDef          `json:"tools"`
	Hooks          []wireHookDef          `json:"hooks"`
	Commands       []wireCommandDef       `json:"commands"`
	PromptSections []wirePromptSectionDef `json:"prompt_sections"`
}

type rpcRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type rpcError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// Worker manages one long-lived python3 subprocess hosting a single Hermes Python plugin.
type Worker struct {
	key    string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex
	pendMu  sync.Mutex
	pending map[int64]chan rpcResponse
	nextID  atomic.Int64

	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
}

// StartWorker spawns a python3 worker subprocess running workerScript.
func StartWorker(pythonBin, pluginKey, workdir string, env []string) (*Worker, error) {
	if strings.TrimSpace(pythonBin) == "" {
		bin, err := findPython3()
		if err != nil {
			return nil, err
		}
		pythonBin = bin
	}
	cmd := exec.Command(pythonBin, "-u", "-c", workerScript)
	if workdir != "" {
		cmd.Dir = workdir
	}
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = io.Discard
	setProcGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin %q stdin pipe: %w", pluginKey, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("plugin %q stdout pipe: %w", pluginKey, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("plugin %q start python3: %w", pluginKey, err)
	}

	w := &Worker{
		key:     pluginKey,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan rpcResponse),
		done:    make(chan struct{}),
	}
	go w.readLoop()
	return w, nil
}

func findPython3() (string, error) {
	for _, candidate := range []string{"python3", "python"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("python3 not found on PATH")
}

func (w *Worker) readLoop() {
	defer close(w.done)
	scanner := bufio.NewScanner(w.stdout)
	// Allow up to 4 MB single NDJSON response frames.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		w.pendMu.Lock()
		ch, ok := w.pending[resp.ID]
		if ok {
			delete(w.pending, resp.ID)
		}
		w.pendMu.Unlock()
		if ok {
			ch <- resp
			close(ch)
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	w.failPending(err)
}

func (w *Worker) failPending(err error) {
	w.pendMu.Lock()
	w.closeErr = err
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- rpcResponse{
			ID: id,
			Error: &rpcError{
				Type:    "PluginHostUnavailable",
				Message: fmt.Sprintf("plugin worker %q closed: %v", w.key, err),
			},
		}
		close(ch)
	}
	w.pendMu.Unlock()
}

// Call sends one NDJSON JSON-RPC request to the Python worker and waits for the matching response.
func (w *Worker) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if w == nil || w.closed.Load() {
		return nil, fmt.Errorf("plugin worker %q is closed", w.key)
	}
	id := w.nextID.Add(1)
	ch := make(chan rpcResponse, 1)

	w.pendMu.Lock()
	if w.closeErr != nil {
		err := w.closeErr
		w.pendMu.Unlock()
		return nil, fmt.Errorf("plugin worker %q unavailable: %w", w.key, err)
	}
	w.pending[id] = ch
	w.pendMu.Unlock()

	payload, err := json.Marshal(rpcRequest{
		ID:     id,
		Method: method,
		Params: params,
	})
	if err != nil {
		w.pendMu.Lock()
		delete(w.pending, id)
		w.pendMu.Unlock()
		return nil, err
	}
	payload = append(payload, '\n')

	w.writeMu.Lock()
	_, werr := w.stdin.Write(payload)
	w.writeMu.Unlock()
	if werr != nil {
		w.pendMu.Lock()
		delete(w.pending, id)
		w.pendMu.Unlock()
		return nil, fmt.Errorf("plugin worker %q write: %w", w.key, werr)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("plugin worker %q closed while awaiting %s", w.key, method)
		}
		if resp.Error != nil {
			if resp.Error.Type != "" {
				return nil, fmt.Errorf("%s: %s", resp.Error.Type, resp.Error.Message)
			}
			return nil, errors.New(resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		w.pendMu.Lock()
		delete(w.pending, id)
		w.pendMu.Unlock()
		return nil, fmt.Errorf("plugin %q RPC %s: %w", w.key, method, ctx.Err())
	case <-w.done:
		return nil, fmt.Errorf("plugin worker %q exited during %s", w.key, method)
	}
}

// PID returns the OS process ID of the running python3 worker (0 if unavailable).
func (w *Worker) PID() int {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return 0
	}
	return w.cmd.Process.Pid
}

// Close gracefully requests shutdown, then kills the entire process group to guarantee zero leaked PIDs.
func (w *Worker) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		// Best-effort graceful shutdown RPC so on_unload callbacks can run.
		if !w.closed.Load() {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, _ = w.Call(ctx, "shutdown", nil)
			cancel()
		}
		w.closed.Store(true)
		_ = w.stdin.Close()

		waitDone := make(chan struct{})
		go func() {
			_ = w.cmd.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
			// Ensure any detached descendants started by the plugin are also reaped.
			killProcGroup(w.cmd)
		case <-time.After(750 * time.Millisecond):
			killProcGroup(w.cmd)
			select {
			case <-waitDone:
			case <-time.After(750 * time.Millisecond):
			}
		}
		_ = w.stdout.Close()
	})
	return nil
}

// Manager holds all loaded plugins and live Python workers for one extension instance.
type Manager struct {
	mu      sync.RWMutex
	paths   paths.ProfilePaths
	cfg     *config.Config
	cwd     string
	plugins map[string]*PluginInfo
	workers []*Worker
}

// NewManager creates an empty plugin Manager.
func NewManager(p paths.ProfilePaths, cfg *config.Config, cwd string) *Manager {
	if p.Home == "" {
		p = paths.ResolveProfilePaths("")
	}
	if cfg == nil {
		cfg = config.Default()
	}
	if strings.TrimSpace(cwd) == "" {
		cwd, _ = os.Getwd()
	}
	return &Manager{
		paths:   p,
		cfg:     cfg,
		cwd:     cwd,
		plugins: make(map[string]*PluginInfo),
	}
}

// Shutdown cleanly terminates every spawned Python plugin worker process group.
func (m *Manager) Shutdown() {
	if m == nil {
		return
	}
	m.mu.Lock()
	ws := m.workers
	m.workers = nil
	m.mu.Unlock()
	for _, w := range ws {
		_ = w.Close()
	}
}

// LoadedInfos returns a snapshot of plugin infos tracked by this manager.
func (m *Manager) LoadedInfos() []PluginInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.plugins))
	for k := range m.plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]PluginInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, *m.plugins[k])
	}
	return out
}

// Register discovers enabled Hermes plugins, loads portable MCP/skills and
// Python plugin workers, translates their tools/hooks/commands/prompt sections
// into PiG SDK primitives, and registers the /plugins slash command.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	_ = RegisterWithManager(ext, p, cfg)
}

// RegisterWithManager performs the same registration as Register and returns
// the underlying Manager for lifecycle control and inspection.
func RegisterWithManager(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) *Manager {
	if ext == nil {
		return nil
	}
	cwd, _ := os.Getwd()
	mgr := NewManager(p, cfg, cwd)
	mgr.loadAndRegister(ext)
	registerSlashCommand(ext, mgr)

	ext.OnSessionShutdown(func(_ sdk.Context, _ map[string]any) (any, error) {
		mgr.Shutdown()
		return nil, nil
	})
	return mgr
}

func (m *Manager) loadAndRegister(ext *sdk.Extension) {
	discovered := Discover(m.paths, m.cwd)

	// Record all discovered plugins for inspection.
	m.mu.Lock()
	for i := range discovered {
		cp := discovered[i]
		m.plugins[cp.Manifest.CanonicalKey()] = &cp
	}
	m.mu.Unlock()

	pythonBin, pyErr := findPython3()

	for i := range discovered {
		info := discovered[i]
		if info.Gate.Action != "load" && info.Gate.Action != "load_now" {
			continue
		}
		key := info.Manifest.CanonicalKey()

		// 1. Portable plugin: register MCP servers (if any) and expose bundled skills.
		if info.Manifest.Portable {
			registerPortableMCPServers(ext, info.Manifest, m.cfg)
			registerPortableSkillsSection(ext, info.Manifest)
			m.markLoaded(key, nil, nil, nil, nil, "")
		}

		// 2. Python plugin: start NDJSON worker and translate tools/hooks/commands/prompt sections.
		entryPath := info.Manifest.EntryPath()
		if entryPath == "" {
			continue
		}
		if pyErr != nil {
			m.markLoaded(key, nil, nil, nil, nil, fmt.Sprintf("python3 unavailable: %v", pyErr))
			continue
		}
		m.loadPythonPlugin(ext, pythonBin, info, entryPath)
	}
}

func (m *Manager) markLoaded(key string, tools, hooks, cmds, prompts []string, loadErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.plugins[key]; ok {
		if loadErr != "" {
			cur.LoadError = loadErr
			cur.Loaded = false
		} else {
			cur.Loaded = true
			cur.RegisteredTools = tools
			cur.RegisteredHooks = hooks
			cur.RegisteredCmds = cmds
			cur.RegisteredPrompt = prompts
		}
	}
}

func (m *Manager) loadPythonPlugin(ext *sdk.Extension, pythonBin string, info PluginInfo, entryPath string) {
	key := info.Manifest.CanonicalKey()
	stateDir := PluginDataDir(m.paths.Home, key, info.Manifest.Portable)

	extraEnv := []string{
		"HERMES_HOME=" + m.paths.Home,
		"HERMES_PROFILE=" + m.paths.ProfileName,
	}
	w, err := StartWorker(pythonBin, key, info.Manifest.Dir, extraEnv)
	if err != nil {
		m.markLoaded(key, nil, nil, nil, nil, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultLoadTimeout)
	rawRes, err := w.Call(ctx, "load_plugin", map[string]any{
		"key":          key,
		"dir":          info.Manifest.Dir,
		"entry":        entryPath,
		"manifest":     info.Manifest,
		"settings":     info.Settings,
		"state_dir":    stateDir,
		"profile_name": m.paths.ProfileName,
	})
	cancel()
	if err != nil {
		_ = w.Close()
		m.markLoaded(key, nil, nil, nil, nil, err.Error())
		return
	}

	var loaded wireLoadResult
	if err := json.Unmarshal(rawRes, &loaded); err != nil {
		_ = w.Close()
		m.markLoaded(key, nil, nil, nil, nil, fmt.Sprintf("invalid load_plugin response: %v", err))
		return
	}

	m.mu.Lock()
	m.workers = append(m.workers, w)
	m.mu.Unlock()

	var toolNames, hookNames, cmdNames, promptNames []string

	// Translate plugin tools -> ext.RegisterTool.
	for _, td := range loaded.Tools {
		toolDef := td
		if strings.TrimSpace(toolDef.Name) == "" {
			continue
		}
		toolNames = append(toolNames, toolDef.Name)
		schema := sdk.Schema{
			"type":       "object",
			"properties": map[string]any{},
		}
		if len(toolDef.Parameters) > 0 {
			schema = sdk.Schema(toolDef.Parameters)
		}
		label := toolDef.Name
		if toolDef.Emoji != "" {
			label = toolDef.Emoji + " " + toolDef.Name
		}
		desc := toolDef.Description
		if desc == "" {
			desc = fmt.Sprintf("Hermes plugin %s tool %s", key, toolDef.Name)
		}
		ext.RegisterTool(sdk.ToolDefinition{
			Name:        toolDef.Name,
			Label:       label,
			Description: desc,
			Parameters:  schema,
			Execute: func(sctx sdk.Context, params map[string]any) (any, error) {
				callCtx, cancelCall := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
				defer cancelCall()
				resRaw, callErr := w.Call(callCtx, "call_tool", map[string]any{
					"name": toolDef.Name,
					"args": params,
				})
				if callErr != nil {
					return sdk.ToolResult{
						Content: callErr.Error(),
						IsError: true,
					}, nil
				}
				return formatToolRPCResult(resRaw), nil
			},
		})
	}

	// Translate plugin hooks -> PiG SDK events.
	for _, hd := range loaded.Hooks {
		hookDef := hd
		hookNames = append(hookNames, hookDef.HookName)
		bindPluginHook(ext, w, hookDef)
	}

	// Translate plugin system prompt sections -> EventBeforeAgentStart.
	for _, ps := range loaded.PromptSections {
		secDef := ps
		secName := SanitizeSectionName(key + "_" + secDef.ID)
		promptNames = append(promptNames, secName)
		bindPluginPromptSection(ext, w, secName, secDef)
	}

	// Translate plugin slash commands -> ext.RegisterCommand.
	for _, cd := range loaded.Commands {
		cmdDef := cd
		cleanName := strings.TrimPrefix(strings.TrimSpace(cmdDef.Name), "/")
		if cleanName == "" {
			continue
		}
		cmdNames = append(cmdNames, cleanName)
		desc := cmdDef.Description
		if desc == "" {
			desc = fmt.Sprintf("Hermes plugin %s command", key)
		}
		ext.RegisterCommand(cleanName, sdk.CommandOptions{
			Description: desc,
			Handler: func(sctx sdk.Context, args string) error {
				callCtx, cancelCall := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
				defer cancelCall()
				resRaw, callErr := w.Call(callCtx, "call_command", map[string]any{
					"name": cleanName,
					"args": args,
				})
				if callErr != nil {
					showPluginMessage(sctx, fmt.Sprintf("Plugin command /%s failed: %v", cleanName, callErr))
					return nil
				}
				text := extractRPCResultString(resRaw)
				if text != "" {
					showPluginMessage(sctx, text)
				}
				return nil
			},
		})
	}

	m.markLoaded(key, toolNames, hookNames, cmdNames, promptNames, "")
}

func bindPluginHook(ext *sdk.Extension, w *Worker, hookDef wireHookDef) {
	switch hookDef.HookName {
	case "pre_tool_call":
		ext.OnEvent(sdk.EventToolCall, func(sctx sdk.Context, data map[string]any) (any, error) {
			toolName, _ := data["toolName"].(string)
			toolArgs := data["input"]
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
			defer cancel()
			raw, err := w.Call(callCtx, "call_hook", map[string]any{
				"ref_id": hookDef.RefID,
				"payload": map[string]any{
					"tool_name": toolName,
					"args":      toolArgs,
				},
			})
			if err != nil {
				// Hermes pre_tool_call is fail-closed.
				return map[string]any{
					"block":  true,
					"reason": fmt.Sprintf("plugin pre_tool_call error: %v", err),
				}, nil
			}
			var wrapper struct {
				Result any `json:"result"`
			}
			if json.Unmarshal(raw, &wrapper) == nil {
				if m, ok := wrapper.Result.(map[string]any); ok {
					action, _ := m["action"].(string)
					if strings.EqualFold(action, "block") {
						reason, _ := m["message"].(string)
						if reason == "" {
							reason, _ = m["reason"].(string)
						}
						if reason == "" {
							reason = "blocked by Hermes plugin pre_tool_call hook"
						}
						return map[string]any{
							"block":  true,
							"reason": reason,
						}, nil
					}
				}
			}
			return nil, nil
		})

	case "post_tool_call", "transform_tool_result":
		ext.OnEvent(sdk.EventToolResult, func(sctx sdk.Context, data map[string]any) (any, error) {
			toolName, _ := data["toolName"].(string)
			toolArgs := data["input"]
			toolRes := flattenToolResultContent(data["content"])
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
			defer cancel()
			raw, err := w.Call(callCtx, "call_hook", map[string]any{
				"ref_id": hookDef.RefID,
				"payload": map[string]any{
					"tool_name": toolName,
					"args":      toolArgs,
					"result":    toolRes,
					"is_error":  data["isError"],
				},
			})
			if err != nil {
				// post_tool_call / transform_tool_result are fail-open.
				return nil, nil
			}
			if hookDef.HookName == "transform_tool_result" {
				var wrapper struct {
					Result any `json:"result"`
				}
				if json.Unmarshal(raw, &wrapper) == nil {
					if replacement, ok := wrapper.Result.(string); ok && replacement != "" {
						data["content"] = []map[string]any{{"type": "text", "text": replacement}}
					}
				}
			}
			return nil, nil
		})

	case "on_session_start":
		ext.OnSessionStart(func(sctx sdk.Context, data map[string]any) (any, error) {
			sessionID := ""
			if id, err := sctx.GetSessionID(); err == nil {
				sessionID = id
			}
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
			defer cancel()
			_, _ = w.Call(callCtx, "call_hook", map[string]any{
				"ref_id": hookDef.RefID,
				"payload": map[string]any{
					"session_id": sessionID,
					"platform":   "cli",
				},
			})
			return nil, nil
		})

	case "on_session_end":
		ext.OnSessionShutdown(func(sctx sdk.Context, data map[string]any) (any, error) {
			sessionID := ""
			if id, err := sctx.GetSessionID(); err == nil {
				sessionID = id
			}
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), 5*time.Second)
			defer cancel()
			_, _ = w.Call(callCtx, "call_hook", map[string]any{
				"ref_id": hookDef.RefID,
				"payload": map[string]any{
					"session_id": sessionID,
					"platform":   "cli",
				},
			})
			return nil, nil
		})

	case "on_pre_compress", "post_compaction":
		eventName := sdk.EventSessionBeforeCompact
		if hookDef.HookName == "post_compaction" {
			eventName = sdk.EventSessionCompact
		}
		ext.OnEvent(eventName, func(sctx sdk.Context, data map[string]any) (any, error) {
			sessionID := ""
			if id, err := sctx.GetSessionID(); err == nil {
				sessionID = id
			}
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
			defer cancel()
			_, _ = w.Call(callCtx, "call_hook", map[string]any{
				"ref_id": hookDef.RefID,
				"payload": map[string]any{
					"session_id": sessionID,
				},
			})
			return nil, nil
		})
	}
}

func bindPluginPromptSection(ext *sdk.Extension, w *Worker, secName string, secDef wirePromptSectionDef) {
	ext.OnEvent(sdk.EventBeforeAgentStart, func(sctx sdk.Context, data map[string]any) (any, error) {
		sections := extractPromptSections(data)
		if sections == nil {
			return nil, nil
		}
		content := secDef.Content
		if secDef.Dynamic {
			callCtx, cancel := context.WithTimeout(contextFromSDK(sctx), defaultCallTimeout)
			raw, err := w.Call(callCtx, "render_prompt_section", map[string]any{
				"id": secDef.ID,
				"context": map[string]any{
					"cwd": safeCwd(sctx),
				},
			})
			cancel()
			if err == nil {
				var res struct {
					Content string `json:"content"`
				}
				if json.Unmarshal(raw, &res) == nil {
					content = res.Content
				}
			}
		}
		content = strings.TrimSpace(content)
		if content == "" {
			return nil, nil
		}
		limit := secDef.MaxChars
		if limit <= 0 || limit > maxPromptChars {
			limit = maxPromptChars
		}
		if len(content) > limit {
			content = content[:limit]
		}
		sections.Set(secName, content)
		return nil, nil
	})
}

var validMCPNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func registerPortableMCPServers(ext *sdk.Extension, m Manifest, cfg *config.Config) {
	if ext == nil || len(m.MCPServers) == 0 {
		return
	}
	names := make([]string, 0, len(m.MCPServers))
	for name := range m.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		// Native config.yaml MCP servers win on name collision.
		if cfg != nil && cfg.MCPServers != nil {
			if _, exists := cfg.MCPServers[name]; exists {
				continue
			}
		}
		if !validMCPNameRe.MatchString(name) {
			continue
		}
		srv := m.MCPServers[name]
		enabled := true
		mcpCfg := sdk.McpServerConfig{
			Enabled:  &enabled,
			Exposure: sdk.McpExposureDirect,
		}
		if srv.URL != "" {
			u, err := url.Parse(srv.URL)
			if err != nil || u == nil || u.Host == "" {
				continue
			}
			scheme := strings.ToLower(u.Scheme)
			if scheme != "http" && scheme != "https" {
				continue
			}
			mcpCfg.Type = "http"
			mcpCfg.URL = srv.URL
			if len(srv.Headers) > 0 {
				mcpCfg.Headers = orderedStringsFromMap(srv.Headers)
			}
		} else if srv.Command != "" {
			mcpCfg.Type = "stdio"
			cmdPath := srv.Command
			if strings.HasPrefix(cmdPath, "./") || strings.HasPrefix(cmdPath, "../") {
				cmdPath = filepath.Join(m.Dir, cmdPath)
			}
			mcpCfg.Command = cmdPath
			mcpCfg.Args = srv.Args
			cwd := strings.TrimSpace(srv.Cwd)
			if cwd == "" {
				cwd = m.Dir
			} else if !filepath.IsAbs(cwd) {
				cwd = filepath.Join(m.Dir, cwd)
			}
			mcpCfg.Cwd = cwd
			envCopy := make(map[string]string, len(srv.Env)+2)
			for k, v := range srv.Env {
				envCopy[k] = v
			}
			if _, exists := envCopy["PLUGIN_ROOT"]; !exists && m.Dir != "" {
				envCopy["PLUGIN_ROOT"] = m.Dir
			}
			if _, exists := envCopy["PLUGIN_DATA"]; !exists && m.Dir != "" {
				envCopy["PLUGIN_DATA"] = filepath.Join(m.Dir, ".data")
			}
			mcpCfg.Env = orderedStringsFromMap(envCopy)
		} else {
			continue
		}
		_ = ext.RegisterMcpServer(name, mcpCfg)
	}
}

func registerPortableSkillsSection(ext *sdk.Extension, m Manifest) {
	if ext == nil || len(m.Skills) == 0 {
		return
	}
	secName := SanitizeSectionName(m.CanonicalKey() + "_skills")
	var b strings.Builder
	fmt.Fprintf(&b, "Plugin %s skills:\n", m.CanonicalKey())
	for _, sk := range m.Skills {
		if sk.Description != "" {
			fmt.Fprintf(&b, "- %s:%s (%s): %s\n", m.Name, sk.Name, filepath.Base(sk.Path), sk.Description)
		} else {
			fmt.Fprintf(&b, "- %s:%s (%s)\n", m.Name, sk.Name, filepath.Base(sk.Path))
		}
	}
	body := strings.TrimSpace(b.String())
	ext.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, data map[string]any) (any, error) {
		sections := extractPromptSections(data)
		if sections == nil {
			return nil, nil
		}
		sections.Set(secName, body)
		return nil, nil
	})
}

func orderedStringsFromMap(m map[string]string) *sdk.OrderedStrings {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, m[k])
	}
	return sdk.NewOrderedStrings(pairs...)
}

func formatToolRPCResult(raw json.RawMessage) sdk.ToolResult {
	var wrapper struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return sdk.ToolResult{Content: string(raw)}
	}
	switch v := wrapper.Result.(type) {
	case nil:
		return sdk.ToolResult{Content: ""}
	case string:
		return sdk.ToolResult{Content: v}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return sdk.ToolResult{Content: fmt.Sprint(v)}
		}
		return sdk.ToolResult{Content: string(b)}
	}
}

func extractRPCResultString(raw json.RawMessage) string {
	var wrapper struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return string(raw)
	}
	if wrapper.Result == nil {
		return ""
	}
	if s, ok := wrapper.Result.(string); ok {
		return s
	}
	b, err := json.Marshal(wrapper.Result)
	if err != nil {
		return fmt.Sprint(wrapper.Result)
	}
	return string(b)
}

func extractPromptSections(data map[string]any) *sdk.SystemPromptSections {
	if data == nil {
		return nil
	}
	opts, ok := data["systemPromptOptions"].(map[string]any)
	if !ok || opts == nil {
		return nil
	}
	if sec, ok := opts["sections"].(*sdk.SystemPromptSections); ok && sec != nil {
		return sec
	}
	if sec, ok := opts["sections"].(sdk.SystemPromptSections); ok {
		cp := sec
		opts["sections"] = &cp
		return &cp
	}
	sec := &sdk.SystemPromptSections{}
	opts["sections"] = sec
	return sec
}

func contextFromSDK(ctx sdk.Context) context.Context {
	var done <-chan struct{}
	func() {
		defer func() { _ = recover() }()
		done = ctx.Done()
	}()
	if done == nil {
		return context.Background()
	}
	c, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-done:
			cancel()
		case <-c.Done():
		}
	}()
	return c
}

func safeCwd(ctx sdk.Context) (out string) {
	defer func() {
		if recover() != nil || strings.TrimSpace(out) == "" {
			out, _ = os.Getwd()
		}
	}()
	if cwd := strings.TrimSpace(ctx.Cwd()); cwd != "" {
		return cwd
	}
	cwd, _ := os.Getwd()
	return cwd
}

func showPluginMessage(ctx sdk.Context, text string) {
	no := false
	if err := ctx.SendMessage(pluginMessageType, text, true, sdk.SendMessageOptions{TriggerTurn: &no}); err != nil {
		ctx.Notify(text, "info")
	}
}

func flattenToolResultContent(raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	if blocks, ok := raw.([]any); ok {
		var parts []string
		for _, b := range blocks {
			if m, ok := b.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok && txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	return string(b)
}
