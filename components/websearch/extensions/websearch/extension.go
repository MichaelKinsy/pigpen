package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Extension returns the websearch extension: the tools, the /search command and the session
// lifecycle of pi-web-access's index.ts, on the public PiG Go SDK.
//
// An invalid web-search.json makes the original throw at registration; an extension cannot fail
// its load here, so nothing is registered and the problem is shown at session start instead.
func Extension() *sdk.Extension {
	e := sdk.New("websearch")
	h := &sdkHost{}
	rt, err := NewRuntime(h)
	var problem string
	if err != nil {
		problem = "websearch: " + err.Error()
	} else if w := rt.Warning(); w != "" {
		problem = "websearch: " + w
	}
	var once sync.Once
	report := func(ctx sdk.Context) {
		if problem != "" {
			once.Do(func() { ctx.Notify(problem, "warning") })
		}
	}
	if rt == nil {
		e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			report(ctx)
			return nil, nil
		})
		return e
	}

	// registerCuratorRunLifecycle: these runs end the approval of the remaining searches.
	for _, event := range curatorRunResetEvents {
		e.OnEvent(event, func(ctx sdk.Context, _ map[string]any) (any, error) {
			rt.CuratorRun().Reset()
			return nil, nil
		})
	}
	e.Shortcut(rt.Config().CurateShortcut, "Review search results", func(ctx sdk.Context) error {
		h.set(ctx)
		rt.CurateShortcut(ctx)
		return nil
	})
	e.Shortcut(rt.Config().ActivityShortcut, "Toggle web search activity", func(ctx sdk.Context) error {
		h.set(ctx)
		rt.ToggleActivityWidget(ctxWidgetUI{ctx})
		return nil
	})

	for _, spec := range rt.Tools() {
		spec := spec
		e.RegisterTool(sdk.ToolDefinition{
			Name:          spec.Name,
			Label:         spec.Label,
			Description:   spec.Description,
			PromptSnippet: spec.PromptSnippet,
			Parameters:    spec.ParametersMap(),
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				h.set(ctx)
				cctx, cancel := bridgeContext(ctx)
				defer cancel()
				cctx = WithToolCallID(cctx, ctx.ToolCallID())
				out, err := spec.Execute(cctx, params, func(partial ToolOutput) { _ = ctx.OnUpdate(toToolResult(partial)) })
				if err != nil {
					return nil, err
				}
				return toToolResult(out), nil
			},
		})
	}

	for _, name := range rt.RenderedTools() {
		name := name
		e.SetToolRenderers(name, sdk.ToolRenderers{
			CallView: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.View, error) {
				text, err := rt.callText(name, args, ctx.UITheme())
				if err != nil {
					return kit.View{}, err
				}
				return kit.View{Root: kit.NewText(text, 0, 0)}, nil
			},
			ResultView: func(ctx sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, _ int) (kit.View, error) {
				node, err := rt.RenderResult(name, result.Content, result.Details, opts.Expanded, opts.IsPartial, ctx.UITheme())
				if err != nil {
					return kit.View{}, err
				}
				return kit.View{Root: node}, nil
			},
		})
	}

	sessionChanged := func(ctx sdk.Context, _ map[string]any) (any, error) {
		h.set(ctx)
		report(ctx)
		// A session that cannot be read still starts: the tools work, only restore and the
		// transcript-based tool selection are skipped, and the user is told.
		entries, err := customEntries(ctx)
		if err != nil {
			ctx.Notify("websearch: could not restore stored results: "+err.Error(), "warning")
		}
		rt.SessionStarted(entries)
		rt.ResetActivity(ctxWidgetUI{ctx})
		if sc, err := ctx.SessionManager().BuildSessionContext(); err != nil {
			ctx.Notify("websearch: could not read the session transcript: "+err.Error(), "warning")
		} else {
			rt.SelectFromSession(sc.Messages)
		}
		return nil, nil
	}
	e.OnEvent(sdk.EventSessionStart, sessionChanged)
	e.OnEvent(sdk.EventSessionTree, sessionChanged)
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		h.set(ctx)
		rt.BeforeAgentStart()
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		rt.SessionShutdown()
		rt.ShutdownActivity()
		return nil, nil
	})

	for _, name := range rt.Commands() {
		switch name {
		case "websearch":
			e.RegisterCommand(name, sdk.CommandOptions{
				Description: "Open web search curator",
				Handler: func(ctx sdk.Context, args string) error {
					h.set(ctx)
					return rt.WebsearchCommand(sdkSideCall{ctx}, args)
				},
			})
		case "curator":
			e.RegisterCommand(name, sdk.CommandOptions{
				Description: "Toggle or configure the search curator workflow",
				Handler: func(ctx sdk.Context, args string) error {
					h.set(ctx)
					rt.CuratorCommand(args)
					return nil
				},
			})
		case "google-account":
			e.RegisterCommand(name, sdk.CommandOptions{
				Description: "Show the active Google account for Gemini Web",
				Handler: func(ctx sdk.Context, _ string) error {
					h.set(ctx)
					cctx, cancel := bridgeContext(ctx)
					defer cancel()
					return rt.GoogleAccountCommand(cctx)
				},
			})
		case "search":
			e.RegisterCommand(name, sdk.CommandOptions{
				Description: "Browse stored web search results",
				Handler: func(ctx sdk.Context, _ string) error {
					h.set(ctx)
					rt.SearchCommand(ctxUI{ctx})
					return nil
				},
			})
		}
	}
	return e
}

// ctxWidgetUI adapts an SDK context to WidgetUI.
type ctxWidgetUI struct{ ctx sdk.Context }

func (u ctxWidgetUI) SetWidget(key string, lines []string) {
	if lines == nil {
		_ = u.ctx.SetWidget(key, nil)
		return
	}
	_ = u.ctx.SetWidget(key, lines)
}

func (u ctxWidgetUI) Theme() WidgetTheme { return u.ctx.UITheme() }

// ctxUI adapts an SDK context to CommandUI.
type ctxUI struct{ ctx sdk.Context }

func (u ctxUI) Notify(message, level string) { u.ctx.Notify(message, level) }
func (u ctxUI) Select(title string, options []string) (string, bool) {
	choice, ok, err := u.ctx.Select(title, options)
	return choice, ok && err == nil
}

// customEntries reads the branch's web-search-results entries (restoreFromSession).
func customEntries(ctx sdk.Context) ([]CustomEntry, error) {
	branch, err := ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil, err
	}
	return entriesFromBranch(branch), nil
}

// entriesFromBranch keeps the web-search-results custom entries of a session branch.
func entriesFromBranch(branch []map[string]any) []CustomEntry {
	var out []CustomEntry
	for _, entry := range branch {
		if entry["type"] != "custom" || entry["customType"] != "web-search-results" {
			continue
		}
		data, err := json.Marshal(entry["data"])
		if err != nil {
			continue
		}
		out = append(out, CustomEntry{CustomType: "web-search-results", Data: data})
	}
	return out
}

// toToolResult converts a tool output: text blocks become the result text, images follow it.
func toToolResult(o ToolOutput) sdk.ToolResult {
	var texts []string
	var images []sdk.ImageContent
	for _, b := range o.Content {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image":
			images = append(images, sdk.ImageContent{Data: b.Data, MimeType: b.MimeType})
		}
	}
	res := sdk.ToolResult{Content: strings.Join(texts, "\n\n"), Images: images, IsError: o.IsError}
	if o.Details != nil {
		res.Details = o.Details
	}
	return res
}

// bridgeContext gives the runtime a context.Context that is cancelled with the request and carries
// the session's model state for side calls (page answers).
func bridgeContext(ctx sdk.Context) (context.Context, context.CancelFunc) {
	c, cancel := context.WithCancel(WithSideCallContext(context.Background(), sdkSideCall{ctx}))
	done := ctx.Done()
	if done == nil {
		return c, cancel
	}
	go func() {
		select {
		case <-done:
			cancel()
		case <-c.Done():
		}
	}()
	return c, cancel
}

// sdkHost implements Host, ToolActivator and UIHost over the most recent SDK context. A retained
// context stays valid after its request completes, which is what background fetches need.
type sdkHost struct {
	mu   sync.Mutex
	ctx  sdk.Context
	have bool
}

func (h *sdkHost) set(ctx sdk.Context) {
	h.mu.Lock()
	h.ctx, h.have = ctx, true
	h.mu.Unlock()
}

func (h *sdkHost) get() (sdk.Context, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctx, h.have
}

func (h *sdkHost) AppendEntry(customType string, data any) {
	if ctx, ok := h.get(); ok {
		_ = ctx.AppendEntry(customType, data)
	}
}

func (h *sdkHost) SendMessage(customType, content string, triggerTurn bool) {
	if ctx, ok := h.get(); ok {
		_ = ctx.SendMessage(customType, content, true, sdk.SendMessageOptions{TriggerTurn: &triggerTurn})
	}
}

func (h *sdkHost) AllToolNames() []string {
	ctx, ok := h.get()
	if !ok {
		return nil
	}
	tools, err := ctx.GetAllTools()
	if err != nil {
		return nil
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func (h *sdkHost) ActiveToolNames() []string {
	ctx, ok := h.get()
	if !ok {
		return nil
	}
	names, _ := ctx.GetActiveTools()
	return names
}

func (h *sdkHost) SetActiveTools(names []string) error {
	if ctx, ok := h.get(); ok {
		ctx.SetActiveTools(names)
	}
	return nil
}

func (h *sdkHost) HasUI() bool {
	ctx, ok := h.get()
	return !ok || ctx.HasUI()
}

// Notify, SendCustomMessage and Exec make sdkHost a CuratorHost.
func (h *sdkHost) Notify(message, level string) {
	if ctx, ok := h.get(); ok {
		ctx.Notify(message, level)
	}
}

func (h *sdkHost) SendCustomMessage(customType string, content []map[string]any, details map[string]any, triggerTurn bool, deliverAs string) {
	ctx, ok := h.get()
	if !ok {
		return
	}
	msg := sdk.CustomMessage{CustomType: customType, Content: content, Display: true}
	if details != nil {
		msg.Details = details
	}
	_ = ctx.SendCustomMessage(msg, sdk.SendMessageOptions{TriggerTurn: &triggerTurn, DeliverAs: deliverAs})
}

func (h *sdkHost) Exec(command string, args []string) (int, string, error) {
	ctx, ok := h.get()
	if !ok {
		return 0, "", errors.New("no session to run the command in")
	}
	res, err := ctx.Exec(command, args)
	if err != nil {
		return 0, "", err
	}
	if res == nil {
		return 0, "", nil
	}
	return res.ExitCode, res.Stderr, nil
}
