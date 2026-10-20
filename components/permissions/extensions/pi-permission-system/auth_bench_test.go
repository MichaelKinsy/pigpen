package pi_permission_system

import (
	"strings"
	"testing"
)

// Benchmarks of the paths the approval dialog runs on a keystroke or a render, for comparison with the original's cost
// (port/probe/authority-bench.test.ts runs the same operations; port/bench-authority.sh interleaves the two).

func benchView(b *testing.B, payload PromptPayload, width int) (*fakeView, func()) {
	b.Helper()
	v := &fakeView{ui: &promptUIFake{}, tui: &recordingTUI{}, mounted: make(chan struct{})}
	done := make(chan UnattributedDecision, 1)
	v.ui.custom = func(factory PromptComponentFactory, _ PromptCustomOptions) (UnattributedDecision, bool, error) {
		v.component = factory(v.tui, plainTheme{}, expandKeys{ctrlO}, func(d UnattributedDecision) { done <- d })
		close(v.mounted)
		return <-done, true, nil
	}
	v.view = PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "tui", UI: v.ui, Notice: PromptNotice{Title: "pi", Body: "b"}}
	go func() { _, _ = PresentInlinePermissionPrompt(v.view, "Permission Required", payload, nil) }()
	<-v.mounted
	return v, func() { v.component.HandleInput("n"); v.component.HandleInput("n") }
}

func BenchmarkComponentRenderDecision(b *testing.B) {
	v, stop := benchView(b, makeAsk("/repo/src/some/deeply/nested/file.ts"), 80)
	defer stop()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.component.Render(80)
	}
}

func BenchmarkComponentRenderForwardedBounded(b *testing.B) {
	body := strings.TrimSuffix(strings.Repeat("- a finding line about some module in the codebase\n", 200), "\n")
	command := "@'\n" + body + "\n'@ | Out-File -FilePath report.md"
	payload := authPayload(func(p *PromptPayload) {
		p.Kind = "forwarded"
		p.Request.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("abc123")}
		p.Request.Surface, p.Request.ToolName, p.Request.Value = "bash", nil, command
		p.Evidence = []PromptEvidence{{Label: "requested", Text: command}}
	})
	v, stop := benchView(b, payload, 120)
	defer stop()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.component.Render(120)
	}
}

func BenchmarkComponentSession(b *testing.B) {
	ask := makeAsk("/repo/src/file.ts")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := &fakeView{ui: &promptUIFake{}, tui: &recordingTUI{}, mounted: make(chan struct{})}
		done := make(chan UnattributedDecision, 1)
		v.ui.custom = func(factory PromptComponentFactory, _ PromptCustomOptions) (UnattributedDecision, bool, error) {
			v.component = factory(v.tui, plainTheme{}, expandKeys{ctrlO}, func(d UnattributedDecision) { done <- d })
			close(v.mounted)
			return <-done, true, nil
		}
		v.view = PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "tui", UI: v.ui, Notice: PromptNotice{Title: "pi", Body: "b"}}
		finished := make(chan struct{})
		go func() { _, _ = PresentInlinePermissionPrompt(v.view, "Permission Required", ask, nil); close(finished) }()
		<-v.mounted
		v.component.Render(80)
		v.component.HandleInput("y")
		v.component.Render(80)
		v.component.HandleInput("y")
		<-finished
	}
}

func BenchmarkReasonTyping(b *testing.B) {
	v, stop := benchView(b, makeAsk("/repo/src/file.ts"), 80)
	defer stop()
	v.component.HandleInput("r")
	v.component.HandleInput("r")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.component.HandleInput("a")
		v.component.Render(80)
	}
}

func BenchmarkReducePrompt(b *testing.B) {
	c := modelConfig()
	s := InitialPromptState(c)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s = asRenderB(b, ReducePrompt(c, s, nav("down")))
	}
}

func asRenderB(b *testing.B, o PromptOutcome) PromptViewState {
	if o.Decided {
		b.Fatal("decided")
	}
	return o.State
}

func BenchmarkCollapsePastedNewlines(b *testing.B) {
	data := pasteChunk(strings.Repeat("a line of pasted text\r\n", 90))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CollapsePastedNewlines(data)
	}
}

func BenchmarkComposeChainThreeLinks(b *testing.B) {
	terminal := newTerminal()
	links := []NamedAuthorizer{newLink(AuthorizerVerdict{Kind: "defer"}, "a").named(), newLink(AuthorizerVerdict{Kind: "defer"}, "b").named(), newLink(AuthorizerVerdict{Kind: "deny", Reason: sp("no")}, "c").named()}
	query, log, details := &queryFake{}, &fwLogger{}, authDetails()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ComposeAuthorizerChain(links, terminal, query, log).Authorize(details)
	}
}

func BenchmarkPrompterPrompt(b *testing.B) {
	prompter := NewPermissionPrompter(PermissionPrompterDeps{Logger: nopReviewLogger{}})
	terminal := newTerminal()
	details := prompterDetails()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = prompter.Prompt(terminal, details)
	}
}

type nopReviewLogger struct{}

func (nopReviewLogger) Review(string, *jsObject) {}

func BenchmarkLocalAuthorizerAsk(b *testing.B) {
	e := newLocalEnv()
	authorizer := e.authorizer()
	details := localDetails()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = authorizer.Authorize(details)
	}
}
