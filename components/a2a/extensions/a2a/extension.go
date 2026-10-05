// Package a2aext is PiG's A2A (Agent2Agent) adapter: it serves PiG tasks to A2A
// agents and lets PiG call remote A2A agents. It is built on the upstream Go SDK
// github.com/a2aproject/a2a-go/v2 (Apache-2.0) and is pinned to A2A protocol 1.0.
//
// The listener is off unless configured (a2a.json, PIG_A2A_LISTEN or --a2a-listen).
// See the Package README for configuration, authentication and tenant boundaries.
package a2aext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Options replaces the extension's dependencies (tests).
type Options struct {
	// Getenv reads the environment (default os.Getenv).
	Getenv func(string) string
	// Worker runs served tasks (default: a `pig --mode rpc` child process per task).
	Worker Worker
}

// Extension returns the a2a extension.
func Extension() *sdk.Extension { return ExtensionWith(Options{}) }

type manager struct {
	getenv func(string) string
	worker Worker

	mu      sync.Mutex
	server  *Server
	cfg     Config
	cfgErr  error
	remotes *Remotes
}

// ExtensionWith returns the extension with explicit dependencies.
func ExtensionWith(o Options) *sdk.Extension {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	m := &manager{getenv: o.Getenv, worker: o.Worker}
	e := sdk.New("a2a")

	e.Flag("a2a-listen", sdk.FlagOptions{
		Description: "Serve PiG tasks over A2A on host:port (needs tokens in a2a.json unless the address is loopback with insecureNoAuth)",
		Type:        sdk.FlagString,
	})

	e.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		m.start(ctx)
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		reason, _ := data["reason"].(string)
		switch reason {
		case "new", "resume", "fork":
			return nil, nil // a session switch inside one process keeps serving
		}
		m.stop()
		return nil, nil
	})

	e.Command("a2a", "Show the A2A listener and remote agents", func(ctx sdk.Context, args string) error {
		ctx.Notify(m.status(ctx), "info")
		return nil
	})

	e.ToolWithGuidelines("a2a_agents", "List the remote A2A (Agent2Agent) agents configured for this PiG, with each agent's card: name, description and skills.",
		sdk.Schema{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		[]string{"Use a2a_agents to discover which remote agents exist before calling a2a_send."},
		func(ctx sdk.Context, params map[string]any) (any, error) { return m.toolAgents(ctx) })

	e.ToolWithGuidelines("a2a_send", "Send a task to a remote A2A agent and wait for its answer. Returns the task state, ids and the agent's reply. Reuse contextId to continue the same conversation.",
		sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"agent":     map[string]any{"type": "string", "description": "Name of a configured remote (see a2a_agents)."},
				"message":   map[string]any{"type": "string", "description": "The task or question, as plain text."},
				"contextId": map[string]any{"type": "string", "description": "Continue this conversation (from an earlier a2a_send)."},
				"taskId":    map[string]any{"type": "string", "description": "Continue this task, when it is waiting for input."},
			},
			"required": []string{"agent", "message"}, "additionalProperties": false,
		},
		[]string{"The remote agent is another agent, not a tool: send it a self-contained request. Data you send leaves this machine."},
		func(ctx sdk.Context, params map[string]any) (any, error) { return m.toolSend(ctx, params) })

	e.ToolWithGuidelines("a2a_task", "Get, list or cancel tasks on a remote A2A agent.",
		sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"agent":  map[string]any{"type": "string", "description": "Name of a configured remote."},
				"action": map[string]any{"type": "string", "enum": []string{"get", "cancel", "list"}},
				"taskId": map[string]any{"type": "string", "description": "Required for get and cancel."},
			},
			"required": []string{"agent", "action"}, "additionalProperties": false,
		},
		nil,
		func(ctx sdk.Context, params map[string]any) (any, error) { return m.toolTask(ctx, params) })
	return e
}

func (m *manager) agentDir(ctx sdk.Context) string {
	if d := m.getenv("PIG_CODING_AGENT_DIR"); d != "" {
		return d
	}
	return filepath.Join(ctx.ConfigHome(), "agent")
}

// load reads the configuration and replaces the remote client set.
func (m *manager) load(ctx sdk.Context) (Config, error) {
	flag := ""
	if v, err := ctx.GetFlag("a2a-listen"); err == nil {
		flag, _ = v.(string)
	}
	cfg, err := LoadConfig(LoadOptions{ConfigHome: m.agentDir(ctx), Getenv: m.getenv, FlagListen: flag})
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg, m.cfgErr = cfg, err
	if err == nil {
		m.remotes = NewRemotes(cfg.Remotes, m.getenv)
	}
	return cfg, err
}

// current returns the loaded configuration, loading it on first use.
func (m *manager) current(ctx sdk.Context) (Config, *Remotes, error) {
	m.mu.Lock()
	loaded := m.remotes != nil || m.cfgErr != nil
	m.mu.Unlock()
	if !loaded {
		if _, err := m.load(ctx); err != nil {
			return Config{}, nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, m.remotes, m.cfgErr
}

func (m *manager) start(ctx sdk.Context) {
	m.mu.Lock()
	running := m.server != nil
	m.mu.Unlock()
	if running {
		return
	}
	cfg, err := m.load(ctx)
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return
	}
	if !cfg.Enabled() {
		return
	}
	worker := m.worker
	if worker == nil {
		worker, err = NewProcessWorker(cfg.Worker, cfg.StateDir, m.getenv)
		if err != nil {
			ctx.Notify(err.Error(), "error")
			return
		}
	}
	srv, err := NewServer(cfg, worker, m.getenv)
	if err == nil {
		err = srv.Start()
	}
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return
	}
	m.mu.Lock()
	m.server = srv
	m.mu.Unlock()
	ctx.Notify(fmt.Sprintf("a2a: listening on %s (A2A %s, %d token(s), %d concurrent task(s))", srv.Addr(), ProtocolVersion, len(cfg.Tokens), cfg.MaxConcurrentTasks), "info")
}

func (m *manager) stop() {
	m.mu.Lock()
	srv := m.server
	m.server = nil
	m.remotes, m.cfgErr = nil, nil // the next use reloads the configuration
	m.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (m *manager) status(ctx sdk.Context) string {
	m.mu.Lock()
	srv := m.server
	m.mu.Unlock()
	cfg, remotes, err := m.current(ctx)
	var b strings.Builder
	switch {
	case srv != nil:
		fmt.Fprintf(&b, "a2a: listening on %s (A2A %s), %d active task(s)", srv.Addr(), ProtocolVersion, srv.ActiveTasks())
		if logs := srv.RecentLogs(); len(logs) > 0 {
			fmt.Fprintf(&b, "\nlast: %s", logs[len(logs)-1])
		}
	case err != nil:
		fmt.Fprintf(&b, "a2a: listener off (%v)", err)
	default:
		b.WriteString("a2a: listener off (set listen in a2a.json, PIG_A2A_LISTEN or --a2a-listen to serve tasks)")
	}
	if remotes != nil {
		if names := remotes.Names(); len(names) > 0 {
			fmt.Fprintf(&b, "\nremotes: %s", strings.Join(names, ", "))
		}
	}
	_ = cfg
	return b.String()
}

// callContext adapts the tool's request context to a context.Context that is cancelled with it.
func callContext(c sdk.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-c.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (m *manager) remotesFor(ctx sdk.Context) (*Remotes, error) {
	_, r, err := m.current(ctx)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	if r == nil || len(r.Names()) == 0 {
		return nil, sdk.NewToolError("a2a: no remotes are configured; add a \"remotes\" object to a2a.json")
	}
	return r, nil
}

func stringParam(params map[string]any, name string, required bool) (string, error) {
	v, ok := params[name]
	if !ok || v == nil {
		if required {
			return "", sdk.NewToolError(fmt.Sprintf("a2a: %s is required", name))
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", sdk.NewToolError(fmt.Sprintf("a2a: %s must be a string", name))
	}
	if required && strings.TrimSpace(s) == "" {
		return "", sdk.NewToolError(fmt.Sprintf("a2a: %s must not be empty", name))
	}
	return s, nil
}

func formatSummary(s TaskSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state: %s\ntaskId: %s\ncontextId: %s", s.State, s.TaskID, s.ContextID)
	if !s.Terminal {
		b.WriteString("\n(the task is not finished; use a2a_task to get it, or send another message with this taskId and contextId)")
	}
	if s.Text != "" {
		b.WriteString("\n\n" + s.Text)
	}
	return b.String()
}

func (m *manager) toolAgents(ctx sdk.Context) (any, error) {
	r, err := m.remotesFor(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := callContext(ctx)
	defer cancel()
	var b strings.Builder
	for _, name := range r.Names() {
		card, err := r.Card(cctx, name)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "- %s: %v\n", name, err)
		default:
			fmt.Fprintf(&b, "- %s: %s (v%s) - %s\n", name, card.Name, card.Version, card.Description)
			for _, sk := range card.Skills {
				fmt.Fprintf(&b, "    skill %s: %s\n", sk.Name, sk.Description)
			}
		}
	}
	return b.String(), nil
}

func (m *manager) toolSend(ctx sdk.Context, params map[string]any) (any, error) {
	agent, err := stringParam(params, "agent", true)
	if err != nil {
		return nil, err
	}
	message, err := stringParam(params, "message", true)
	if err != nil {
		return nil, err
	}
	contextID, _ := stringParam(params, "contextId", false)
	taskID, _ := stringParam(params, "taskId", false)
	r, err := m.remotesFor(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := callContext(ctx)
	defer cancel()
	var soFar strings.Builder
	last := time.Now()
	sum, err := r.Send(cctx, SendArgs{Agent: agent, Message: message, ContextID: contextID, TaskID: taskID}, func(chunk string) {
		soFar.WriteString(chunk)
		if time.Since(last) > 250*time.Millisecond {
			last = time.Now()
			_ = ctx.OnUpdate(soFar.String())
		}
	})
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	return formatSummary(sum), nil
}

func (m *manager) toolTask(ctx sdk.Context, params map[string]any) (any, error) {
	agent, err := stringParam(params, "agent", true)
	if err != nil {
		return nil, err
	}
	action, err := stringParam(params, "action", true)
	if err != nil {
		return nil, err
	}
	r, err := m.remotesFor(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := callContext(ctx)
	defer cancel()
	switch action {
	case "list":
		list, err := r.list(cctx, agent)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		var b strings.Builder
		for _, s := range list {
			fmt.Fprintf(&b, "- %s (%s) context %s\n", s.TaskID, s.State, s.ContextID)
		}
		if b.Len() == 0 {
			return "no tasks", nil
		}
		return b.String(), nil
	case "get", "cancel":
		taskID, err := stringParam(params, "taskId", true)
		if err != nil {
			return nil, err
		}
		var sum TaskSummary
		if action == "get" {
			sum, err = r.GetTask(cctx, agent, taskID)
		} else {
			sum, err = r.CancelTask(cctx, agent, taskID)
		}
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return formatSummary(sum), nil
	}
	return nil, sdk.NewToolError(fmt.Sprintf("a2a: unknown action %q (get, cancel or list)", action))
}

var _ = errors.New
