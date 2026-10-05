package websearch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
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
				out, err := spec.Execute(cctx, params, func(partial ToolOutput) { _ = ctx.OnUpdate(toToolResult(partial)) })
				if err != nil {
					return nil, err
				}
				return toToolResult(out), nil
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
		return nil, nil
	})

	for _, name := range rt.Commands() {
		e.RegisterCommand(name, sdk.CommandOptions{
			Description: "Browse stored web search results",
			Handler: func(ctx sdk.Context, _ string) error {
				h.set(ctx)
				rt.SearchCommand(ctxUI{ctx})
				return nil
			},
		})
	}
	return e
}

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

// bridgeContext gives the runtime a context.Context that is cancelled with the request.
func bridgeContext(ctx sdk.Context) (context.Context, context.CancelFunc) {
	c, cancel := context.WithCancel(context.Background())
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
