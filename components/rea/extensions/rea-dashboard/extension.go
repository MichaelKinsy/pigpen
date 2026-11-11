// Package readashboard is the UI member of the REA harness: a header, a live widget of REA calls, a /rea
// overlay, compact tool cards, and the REA identity in the system prompt. It sees REA only through tool
// events and stateless one-shot runs of the embedded node and rea.mjs; it never sets REA's environment,
// changes the registration of the "rea" MCP server, or opens a second MCP session.
package readashboard

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is the REA dashboard.
func Extension() *sdk.Extension { return build(execREA, time.Now) }

func build(run runner, now func() time.Time) *sdk.Extension {
	ext := sdk.New("rea-dashboard")
	d := newDashboard(run, now)

	// Loading: read the registered servers (on a first run the runtime member registers rea a few seconds later,
	// which mcp_servers_change reports), set the title, and let the refresh loop draw the header.
	ext.OnLoaded(func(ctx sdk.Context) {
		d.attach(ctx)
		d.refreshServer(ctx)
		if ctx.HasUI() {
			ctx.SetTitle("REA · " + filepath.Base(ctx.Cwd()))
		}
		d.requestRefresh()
	})

	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		d.revive()
		d.attach(ctx)
		d.refreshServer(ctx)
		d.restore(ctx)
		d.requestRefresh()
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionTree, func(ctx sdk.Context, _ map[string]any) (any, error) {
		d.attach(ctx)
		d.restore(ctx)
		d.requestRefresh()
		return nil, nil
	})
	ext.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) {
		d.shutdown()
		return nil, nil
	})

	ext.OnMcpServersChange(func(ctx sdk.Context, data map[string]any) (any, error) {
		d.attach(ctx)
		if servers, ok := decodeServers(data["servers"]); ok {
			d.setServer(findREA(servers))
		} else {
			d.refreshServer(ctx)
		}
		return nil, nil
	})

	// The timeline: every REA call, including those a codemode script makes (parentToolCallId).
	ext.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		if ev, ok := decodeToolEvent(data); ok {
			d.attach(ctx)
			d.store.start(ev, d.now())
			d.requestRefresh()
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventToolExecutionUpdate, func(ctx sdk.Context, data map[string]any) (any, error) {
		if ev, ok := decodeToolEvent(data); ok {
			d.attach(ctx)
			d.store.update(ev, d.now())
			d.requestRefresh()
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventToolExecutionEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		ev, ok := decodeToolEvent(data)
		if !ok {
			return nil, nil
		}
		d.attach(ctx)
		entry := d.store.end(ev, d.now())
		d.requestRefresh()
		// A failure to persist costs the call its place after /resume, nothing else.
		_ = ctx.AppendEntry(entryType, entry)
		return nil, nil
	})

	// Identity: one paragraph in a per-run prompt section, appended to the prompt rather than replacing it.
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		d.attach(ctx)
		m := d.model()
		if !m.Server.Present {
			return nil, nil
		}
		options, _ := data["systemPromptOptions"].(map[string]any)
		if sections, ok := options["sections"].(*sdk.SystemPromptSections); ok {
			sections.Set("rea_harness", identityParagraph(m.version()))
		}
		return nil, nil
	})

	ext.ToolRenderer(resolveRenderers)

	ext.Command("rea", "Open the REA dashboard: investigation timeline, targets, providers, about.", func(ctx sdk.Context, args string) error {
		return d.open(ctx, tabFromArgs(args))
	})
	ext.Shortcut("alt+r", "Open the REA dashboard", func(ctx sdk.Context) error {
		return d.open(ctx, tabInvestigation)
	})
	return ext
}

// identityParagraph says what this session is.
func identityParagraph(version string) string {
	rea := "REA (Reverse Engineer Anything)"
	if version != "" {
		rea += " " + version
	}
	return "This is the REA harness: " + rea + " runs here as the MCP server \"rea\". " +
		"Its analysis tools are on that server (tool names mcp__rea__<tool>, normally called from codemode); " +
		"use them to inspect binaries, apps, packages and web targets, and report the evidence and limitations they return."
}

func decodeServers(v any) ([]sdk.RegisteredMcpServer, bool) {
	if v == nil {
		return nil, false
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var servers []sdk.RegisteredMcpServer
	if json.Unmarshal(raw, &servers) != nil {
		return nil, false
	}
	return servers, true
}

// tabFromArgs picks a tab from the word after /rea: any prefix of a tab name.
func tabFromArgs(args string) tabID {
	word := strings.ToLower(strings.TrimSpace(args))
	if word == "" {
		return tabInvestigation
	}
	word, _, _ = strings.Cut(word, " ")
	for t, name := range tabNames {
		if strings.HasPrefix(strings.ToLower(name), word) {
			return tabID(t)
		}
	}
	return tabInvestigation
}

// restore rebuilds the timeline from the rea.call entries of the session's current branch.
func (d *dashboard) restore(ctx sdk.Context) {
	entries, err := ctx.GetEntries()
	if err != nil {
		return
	}
	var keep func(string) bool
	if branch, err := ctx.GetBranch(); err == nil && len(branch) > 0 {
		ids := make(map[string]bool, len(branch))
		for _, e := range branch {
			ids[e.ID] = true
		}
		keep = func(id string) bool { return ids[id] }
	}
	d.store.restore(entries, keep)
}

// open shows the overlay. Without a terminal UI it notifies a one-line summary instead.
func (d *dashboard) open(ctx sdk.Context, tab tabID) error {
	d.attach(ctx)
	d.refreshServer(ctx)
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		d.mu.Lock()
		idle := d.doctor.Status == doctorIdle
		d.mu.Unlock()
		if idle {
			d.doctorNow()
		}
		ctx.Notify(d.model().summary(), "info")
		return nil
	}
	o := newOverlay(d, tab, ctx.Height)
	defer o.Dispose()
	res, err := ctx.Custom(o, sdk.RemoteOverlayOptions{Title: "REA", WidthFraction: 1, HeightFraction: 1, Overlay: true})
	if err != nil {
		return err
	}
	closed, _ := res.(map[string]any)
	target, _ := closed["target"].(string)
	switch closed["action"] {
	case closeInvestigate:
		return d.investigate(ctx, target)
	case closeDerive:
		return d.derive(ctx, target)
	}
	return nil
}

// investigate asks for a question and sends the agent a request naming the target, so every step stays in the
// transcript.
func (d *dashboard) investigate(ctx sdk.Context, target string) error {
	answer, ok, err := ctx.Input("Investigate "+short(target, 60), "What do you want to find out?")
	if err != nil || !ok {
		return err
	}
	question, _ := answer.(string)
	if strings.TrimSpace(question) == "" {
		return nil
	}
	return d.send(ctx, investigateRequest(target, question))
}

// derive asks for the piglet's name and sends the derive-a-harness request. Nothing else changes.
func (d *dashboard) derive(ctx sdk.Context, target string) error {
	name := defaultPigletName(target)
	answer, ok, err := ctx.Input("Derive a piglet that works like "+short(target, 50), name)
	if err != nil || !ok {
		return err
	}
	if typed, _ := answer.(string); strings.TrimSpace(typed) != "" {
		name = sanitizePigletName(typed)
	}
	return d.send(ctx, deriveRequest(name, target))
}

// send delivers a user message: a plain prompt when the agent is idle, else queued after the current run.
func (d *dashboard) send(ctx sdk.Context, text string) error {
	deliverAs := ""
	if idle, err := ctx.IsIdle(); err == nil && !idle {
		deliverAs = "followUp"
	}
	return ctx.SendUserMessage(text, deliverAs)
}
