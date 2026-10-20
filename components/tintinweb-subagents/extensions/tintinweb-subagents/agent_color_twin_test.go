package tintinweb_subagents

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of test/agent-color.test.ts and test/agent-color-surfaces.test.ts.

func TestAgentColorTwins(t *testing.T) {
	const file = "agent-color"
	th := tagTheme()

	t.Run("resolveAgentColor", func(t *testing.T) {
		tw(t, file, "resolves Claude Code names and Agency Agents aliases", func(t *testing.T) {
			for name, want := range map[string]string{"purple": "#827DBD", "neon-cyan": "#06B6D4", "slate": "#64748B"} {
				got, ok := resolveAgentColor(name)
				if !ok || got != want {
					t.Errorf("%s: %q %v, want %q", name, got, ok, want)
				}
			}
		})
		tw(t, file, "normalizes six-digit hex and rejects unsupported values", func(t *testing.T) {
			if got, _ := resolveAgentColor(" #8b5cf6 "); got != "#8B5CF6" {
				t.Errorf("got %q", got)
			}
			for _, bad := range []string{"not-a-color", "#123", ""} {
				if got, ok := resolveAgentColor(bad); ok {
					t.Errorf("%q resolved to %q", bad, got)
				}
			}
		})
	})

	t.Run("renderAgentNameLabel", func(t *testing.T) {
		tw(t, file, "renders a padded truecolor badge with readable foreground", func(t *testing.T) {
			ansi := th
			ansi.bold = func(s string) string { return "\x1b[1m" + s + "\x1b[22m" }
			badge := renderAgentNameLabel("Code Reviewer", "purple", ansi, agentNameStyle{Bold: true})
			hasStr(t, badge, "\x1b[48;2;130;125;189m")
			hasStr(t, badge, " Code Reviewer ")
			if w := widthx.VisibleWidth(badge); w != len("Code Reviewer")+2 {
				t.Errorf("width %d", w)
			}
			// Badge text follows the background's luminance: #827DBD is light enough for black, #1E3A8A is not.
			hasStr(t, badge, "\x1b[38;2;0;0;0m")
			hasStr(t, renderAgentNameLabel("Tester", "navy", th, agentNameStyle{}), "\x1b[38;2;255;255;255m")
		})
		tw(t, file, "judges contrast against the effective color in 256-color mode", func(t *testing.T) {
			ansi := th
			ansi.mode = "256color"
			label := renderAgentNameLabel("Reviewer", "#C430C4", ansi, agentNameStyle{})
			hasStr(t, label, "\x1b[48;5;170m")
			hasStr(t, label, "\x1b[38;5;16m")
			hasStr(t, renderAgentNameLabel("Reviewer", "#808080", ansi, agentNameStyle{}), "\x1b[48;5;244m")
		})
		tw(t, file, "restores an enclosing tool background after the badge", func(t *testing.T) {
			label := renderAgentNameLabel("Reviewer", "purple", th, agentNameStyle{RestoreBackground: "\x1b[48;2;1;2;3m"})
			if !strings.HasSuffix(label, "\x1b[39m\x1b[48;2;1;2;3m") {
				t.Errorf("%q", label)
			}
		})
		tw(t, file, "resets the background when the caller paints none", func(t *testing.T) {
			if label := renderAgentNameLabel("Reviewer", "purple", th, agentNameStyle{}); !strings.HasSuffix(label, "\x1b[39m\x1b[49m") {
				t.Errorf("%q", label)
			}
		})
		tw(t, file, "preserves existing theme styling without a valid color", func(t *testing.T) {
			eqStr(t, renderAgentNameLabel("Agent", "", th, agentNameStyle{FallbackColor: "toolTitle", Bold: true}), "<toolTitle>*Agent*</toolTitle>")
			eqStr(t, renderAgentNameLabel("Agent", "invalid", th, agentNameStyle{FallbackColor: "muted"}), "<muted>Agent</muted>")
		})
	})
}

func TestAgentColorSurfacesTwins(t *testing.T) {
	const file = "agent-color-surfaces"
	const (
		typ        = "colored-reviewer"
		display    = "Code Reviewer"
		purpleBg   = "\x1b[48;2;130;125;189m"
		bgSuccess  = "<toolSuccessBg>"
		deregister = ""
	)
	th := tagTheme()
	register := func(color string) {
		r := newRegistry()
		r.set(&agentConfig{Name: typ, DisplayName: display, Color: color, Description: "Reviews code", Extensions: false, Skills: false, SystemPrompt: "Review code.", PromptMode: "replace", Enabled: true})
		registerAgents(r)
	}
	t.Cleanup(func() { registerAgents(newRegistry()) })
	makeRecord := func() *agentView {
		return &agentView{ID: "review-1", Type: typ, Description: "Review this change", Status: "running", StartedAt: nowMs(), HasSession: true}
	}

	tw(t, file, "renders the registered Agent tool call header with the display name and color", func(t *testing.T) {
		register("purple")
		args := map[string]any{"subagent_type": typ, "description": "Review this change"}
		render := func(partial, isError bool) string {
			return strings.Join(renderAgentCall(args, partial, isError, th, 120), "\n")
		}
		output := render(false, false)
		hasStr(t, output, display)
		hasStr(t, output, purpleBg)
		// The row tint is opened by the line itself and restored after the badge.
		if strings.Index(output, bgSuccess) >= strings.Index(output, purpleBg) {
			t.Error("the tint does not open before the badge")
		}
		if n := len(strings.Split(output, bgSuccess)); n != 3 { // opened once, restored after the badge
			t.Errorf("tint appears %d times, want 2", n-1)
		}
		// Left open on purpose: nothing after the badge may close the background.
		lacksStr(t, output, "\x1b[49m")
		hasStr(t, render(true, false), "<toolPendingBg>")
		hasStr(t, render(false, true), "<toolErrorBg>")

		missing := strings.Join(renderAgentCall(map[string]any{"description": "Review this change"}, false, false, th, 120), "\n")
		hasStr(t, missing, "<toolTitle>*Agent*</toolTitle>")
		lacksStr(t, missing, purpleBg)

		// An agent without a color renders the pre-badge line byte for byte.
		register("")
		eqStr(t, strings.TrimRight(render(false, false), " "), "▸ <toolTitle>*"+display+"*</toolTitle>  <muted>Review this change</muted>")
	})
	tw(t, file, "renders the above-editor Agent widget with the display name and color", func(t *testing.T) {
		register("purple")
		rec := makeRecord()
		w := newAgentWidget(&fixedList{agents: []*agentView{rec}}, activityMap{rec.ID: {TurnCount: 1}}, func() widgetMode { return widgetModeAll }, nil, nil)
		ui := &fakeWidgetUI{}
		w.setUI(ui)
		defer w.dispose()
		w.update()
		output := ui.frame(120, th)
		eqStr(t, ui.placement, "aboveEditor")
		hasStr(t, output, display)
		hasStr(t, output, purpleBg)
	})
	tw(t, file, "renders the FleetView row with the display name and color", func(t *testing.T) {
		register("purple")
		fl := &fixedList{agents: []*agentView{makeRecord()}}
		f := newFleetList(&stubFleetManager{fixedList: fl}, nil, nil, nil, nil)
		ui := &fakeFleetUI{}
		f.setUI(ui)
		defer f.dispose()
		f.update()
		output := strings.Join(ui.render(120, th), "\n")
		hasStr(t, output, display)
		hasStr(t, output, purpleBg)

		register("invalid")
		fallback := strings.Join(ui.render(120, th), "\n")
		hasStr(t, fallback, "<muted>"+display+"</muted>")
		lacksStr(t, fallback, purpleBg)
	})
	tw(t, file, "renders the conversation viewer header with the display name and color", func(t *testing.T) {
		register("purple")
		rec := makeRecord()
		v := newConversationViewer(&fakeHost{rows: 30}, emptySession{}, func() agentView { return *rec }, nil, th, func() {}, viewerOptions{})
		defer v.Dispose()
		output := strings.Join(v.Render(120), "\n")
		hasStr(t, output, display)
		hasStr(t, output, purpleBg)
	})
}
