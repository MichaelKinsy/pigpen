package tintinweb_subagents

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twins of test/conversation-viewer.test.ts.

// testSession is a session whose messages the test owns (and may mutate in place, as streaming does).
type testSession struct {
	mu       sync.Mutex
	messages []map[string]any
	subs     int
}

func mockSession(messages ...map[string]any) *testSession { return &testSession{messages: messages} }

func (s *testSession) Messages() []map[string]any { return s.messages }
func (s *testSession) Subscribe(func()) func() {
	s.mu.Lock()
	s.subs++
	s.mu.Unlock()
	return func() {}
}

func textMsg(role, text string) obj {
	return obj{"role": role, "content": []any{obj{"type": "text", "text": text}}}
}

func userMsg(text string) obj      { return obj{"role": "user", "content": text} }
func assistantMsg(text string) obj { return textMsg("assistant", text) }
func resultMsg(text string) obj {
	return obj{"role": "toolResult", "toolUseId": "t1", "content": []any{obj{"type": "text", "text": text}}}
}

func mockRecordView(mut func(*agentView)) func() agentView {
	r := agentView{ID: "test-1", Type: "general-purpose", Description: "test agent", Status: "running", StartedAt: nowMs()}
	if mut != nil {
		mut(&r)
	}
	return func() agentView { return r }
}

func newViewer(rows int, session viewerSession, rec func() agentView, act func() *agentActivity, th uiTheme, opts viewerOptions) (*conversationViewer, *fakeHost) {
	h := &fakeHost{rows: rows}
	return newConversationViewer(h, session, rec, act, th, func() {}, opts), h
}

// countingMarkdown wraps the viewer's Markdown constructor, as the original's mock subclass of Markdown does.
type markdownProbe struct {
	constructions int
	renderCalls   int
	throws        bool
}

func (p *markdownProbe) install(v *conversationViewer) {
	inner := v.newMarkdown
	v.newMarkdown = func(text string, th *tui.MarkdownTheme, style *tui.DefaultTextStyle) markdownComponent {
		p.constructions++
		return &probedMarkdown{inner: inner(text, th, style), p: p}
	}
}

type probedMarkdown struct {
	inner markdownComponent
	p     *markdownProbe
}

func (m *probedMarkdown) SetText(t string) { m.inner.SetText(t) }
func (m *probedMarkdown) Render(w int) []string {
	m.p.renderCalls++
	if m.p.throws {
		panic("Maximum call stack size exceeded")
	}
	return m.inner.Render(w)
}

func TestConversationViewerTwins(t *testing.T) {
	const file = "conversation-viewer"
	plain := plainTheme()

	t.Run("ConversationViewer invocation line", func(t *testing.T) {
		// The `↳` metadata row for a record, or "" when the viewer renders none.
		invocationLine := func(inv *invocation) string {
			v, _ := newViewer(30, emptySession{}, mockRecordView(func(r *agentView) { r.Invocation = inv }), nil, plain, viewerOptions{})
			for _, l := range v.Render(200) {
				if i := strings.Index(l, "↳"); i >= 0 {
					return regexp.MustCompile(`\s*│\s*$`).ReplaceAllString(l[i:], "")
				}
			}
			return ""
		}
		tw(t, file, "names the model with its provider", func(t *testing.T) {
			eqStr(t, invocationLine(&invocation{ModelName: "sonnet 4.6", ModelID: "anthropic/claude-sonnet-4-6", Thinking: "high", MaxTurns: intp(60)}),
				"↳ anthropic/claude-sonnet-4-6 · thinking: high · max turns: 60")
		})
		tw(t, file, "falls back to the short label when no canonical id was captured", func(t *testing.T) {
			eqStr(t, invocationLine(&invocation{ModelName: "sonnet 4.6", Thinking: "high"}), "↳ sonnet 4.6 · thinking: high")
		})
		tw(t, file, "discloses a model and level the run did not honor", func(t *testing.T) {
			eqStr(t, invocationLine(&invocation{ModelName: "haiku 4.5", ModelID: "anthropic/claude-haiku-4-5", RequestedModel: "google/gemini-3-pro", Thinking: "low", RequestedThinking: "max"}),
				"↳ anthropic/claude-haiku-4-5 (asked google/gemini-3-pro) · thinking: low (asked max)")
		})
		tw(t, file, "renders no row at all for a record with no invocation", func(t *testing.T) {
			eqStr(t, invocationLine(nil), "")
		})
	})

	t.Run("ConversationViewer cost display", func(t *testing.T) {
		header := func(on bool, cost float64) string {
			rec := mockRecordView(func(r *agentView) { r.Usage = lifetimeUsage{Input: 1000, Output: 200, Cost: cost} })
			v, _ := newViewer(30, emptySession{}, rec, nil, plain, viewerOptions{ShowCost: on})
			return strings.Join(v.Render(200), "\n")
		}
		tw(t, file, "shows the cost beside the token count when enabled", func(t *testing.T) {
			out := header(true, 0.0042)
			hasStr(t, out, "1.2k token")
			hasStr(t, out, "~$0.0042")
		})
		tw(t, file, "shows no cost when disabled", func(t *testing.T) {
			out := header(false, 0.0042)
			hasStr(t, out, "1.2k token")
			lacksStr(t, out, "$")
		})
		tw(t, file, "shows no cost for a model with no pricing data", func(t *testing.T) {
			lacksStr(t, header(true, 0), "$")
		})
	})

	t.Run("ConversationViewer", func(t *testing.T) {
		tw(t, file, "closes with Ctrl+C when not composing", func(t *testing.T) {
			var calls int
			v := newConversationViewer(&fakeHost{rows: 40}, mockSession(), mockRecordView(nil), nil, ansiTheme(), func() { calls++ }, viewerOptions{})
			v.HandleInput("\x03")
			if calls != 1 {
				t.Errorf("done called %d times", calls)
			}
		})

		t.Run("render width safety", func(t *testing.T) {
			widths := []int{40, 80, 120, 216}
			fits := func(t *testing.T, messages []obj, ws []int, status string) {
				for _, w := range ws {
					v, _ := newViewer(30, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = status }), nil, ansiTheme(), viewerOptions{})
					assertAllLinesFit(t, v.Render(w), w)
				}
			}
			tw(t, file, "no line exceeds width with empty messages", func(t *testing.T) {
				fits(t, nil, widths, "running")
			})
			tw(t, file, "no line exceeds width with plain text messages", func(t *testing.T) {
				fits(t, []obj{userMsg("Hello, how are you?"), assistantMsg("I am fine, thank you for asking.")}, widths, "running")
			})
			tw(t, file, "keeps bordered rows exact-width at a double-width truncation boundary", func(t *testing.T) {
				width := 40
				for prefixLength := 0; prefixLength < width; prefixLength++ {
					desc := strings.Repeat("a", prefixLength) + "界more"
					v, _ := newViewer(30, mockSession(), mockRecordView(func(r *agentView) { r.Description = desc }), nil, ansiTheme(), viewerOptions{})
					for _, line := range v.Render(width) {
						if vw := widthx.VisibleWidth(line); vw != width {
							t.Fatalf("prefix %d produced an under-width bordered row (%d): %q", prefixLength, vw, line)
						}
					}
				}
			})
			tw(t, file, "no line exceeds width when text is longer than viewport", func(t *testing.T) {
				long := strings.Repeat("A", 500)
				fits(t, []obj{userMsg(long), assistantMsg(long), resultMsg(long)}, widths, "running")
			})
			tw(t, file, "no line exceeds width with embedded ANSI escape codes in content", func(t *testing.T) {
				ansi := "\x1b[1mBold heading\x1b[22m and \x1b[31mred text\x1b[0m " + strings.Repeat("X", 300)
				fits(t, []obj{resultMsg(ansi)}, widths, "running")
			})
			tw(t, file, "no line exceeds width with long URLs", func(t *testing.T) {
				url := "https://example.com/" + strings.Repeat("a/b/c/d/e/", 30) + "?q=" + strings.Repeat("x", 100)
				fits(t, []obj{assistantMsg("Check this link: " + url)}, widths, "running")
			})
			tw(t, file, "no line exceeds width with wide table-like content", func(t *testing.T) {
				var cols, vals []string
				for i := 0; i < 20; i++ {
					cols = append(cols, fmt.Sprintf("Column%d", i))
					vals = append(vals, "value123")
				}
				header := "| " + strings.Join(cols, " | ") + " |"
				row := "| " + strings.Join(vals, " | ") + " |"
				fits(t, []obj{resultMsg(strings.Join([]string{header, row, row, row}, "\n"))}, widths, "running")
			})
			tw(t, file, "no line exceeds width with bashExecution messages", func(t *testing.T) {
				fits(t, []obj{{"role": "bashExecution", "command": "cat " + strings.Repeat("/very/long/path/", 20) + "file.txt", "output": strings.Repeat("O", 600)}}, widths, "running")
			})
			tw(t, file, "no line exceeds width with running activity indicator", func(t *testing.T) {
				act := &agentActivity{ActiveTools: []activeTool{{"read", "file.ts"}, {"grep", "pattern"}}, ToolUses: 5, ResponseText: strings.Repeat("R", 400)}
				for _, w := range widths {
					v, _ := newViewer(30, mockSession(userMsg("do the thing"), assistantMsg("working on it")), mockRecordView(nil), func() *agentActivity { return act }, ansiTheme(), viewerOptions{})
					assertAllLinesFit(t, v.Render(w), w)
				}
			})
			tw(t, file, "no line exceeds width with tool calls", func(t *testing.T) {
				msg := obj{"role": "assistant", "content": []any{
					obj{"type": "text", "text": "Let me check that."},
					obj{"type": "toolCall", "toolUseId": "t1", "name": "very_long_tool_name_" + strings.Repeat("x", 200), "input": obj{}},
				}}
				fits(t, []obj{msg}, widths, "running")
			})
			tw(t, file, "no line exceeds width at narrow terminal", func(t *testing.T) {
				fits(t, []obj{userMsg("Hello world, this is a normal sentence."), assistantMsg("Sure, here's the answer.")}, []int{8, 10, 15, 20}, "running")
			})
			tw(t, file, "no line exceeds width with mixed ANSI + unicode content", func(t *testing.T) {
				text := "\x1b[32m✓\x1b[0m Test passed — 日本語テスト " + strings.Repeat("あ", 50) + " \x1b[33m⚠\x1b[0m"
				fits(t, []obj{resultMsg(text)}, widths, "running")
			})
		})

		t.Run("Markdown rendering", func(t *testing.T) {
			// viewerFor builds a viewer over messages, tall enough that an assertion reads the whole transcript.
			viewerFor := func(messages []obj, mode viewerMarkdownMode, onMode func(viewerMarkdownMode), rows int) (*conversationViewer, *markdownProbe) {
				opts := viewerOptions{OnMarkdownMode: onMode}
				if mode != "" {
					opts.ViewerMarkdown = func() viewerMarkdownMode { return mode }
				}
				v, _ := newViewer(rows, mockSession(messages...), mockRecordView(func(r *agentView) { r.Status = "completed" }), nil, ansiTheme(), opts)
				p := &markdownProbe{}
				p.install(v)
				return v, p
			}
			render80 := func(v *conversationViewer) string { return stripANSI(strings.Join(v.Render(80), "\n")) }
			assistant := func(text string) []obj { return []obj{assistantMsg(text)} }
			result := func(text string) []obj { return []obj{resultMsg(text)} }

			tw(t, file, "renders assistant Markdown by default instead of raw source markers", func(t *testing.T) {
				v, _ := viewerFor(assistant("# Heading\n\n- first\n- second\n\n**bold**"), "", nil, 200)
				out := render80(v)
				hasStr(t, out, "Heading")
				lacksStr(t, out, "# Heading")
				lacksStr(t, out, "**bold**")
				hasStr(t, out, "bold")
			})
			tw(t, file, "leaves assistant text verbatim under `off`", func(t *testing.T) {
				v, _ := viewerFor(assistant("# Heading\n\n**bold**"), viewerMarkdownOff, nil, 200)
				out := render80(v)
				hasStr(t, out, "# Heading")
				hasStr(t, out, "**bold**")
			})
			tw(t, file, "leaves tool results byte-exact under the default mode", func(t *testing.T) {
				raw := strings.Join([]string{"#!/bin/sh", "# section", "3) alpha", "7) beta", "9) gamma", "Section", "---", "next"}, "\n")
				v, _ := viewerFor(result(raw), "", nil, 200)
				out := render80(v)
				for _, line := range strings.Split(raw, "\n") {
					hasStr(t, out, line)
				}
			})
			tw(t, file, "renders tool-result Markdown under `all`", func(t *testing.T) {
				v, _ := viewerFor(result("## ctx_execute\n\n- one\n- two"), viewerMarkdownAll, nil, 200)
				out := render80(v)
				hasStr(t, out, "ctx_execute")
				lacksStr(t, out, "## ctx_execute")
			})
			tw(t, file, "does not renumber ordered lists even when it does render them", func(t *testing.T) {
				v, _ := viewerFor(result("3) alpha\n7) beta\n9) gamma"), viewerMarkdownAll, nil, 200)
				out := render80(v)
				hasStr(t, out, "3) alpha")
				lacksStr(t, out, "4. beta")
			})
			tw(t, file, "`m` cycles the mode, persists it, and shows it in the footer", func(t *testing.T) {
				var modes []viewerMarkdownMode
				v, _ := viewerFor(assistant("# Heading"), viewerMarkdownAssistant, func(m viewerMarkdownMode) { modes = append(modes, m) }, 200)
				hasStr(t, render80(v), "m md")

				v.HandleInput("m")
				eqStr(t, string(modes[len(modes)-1]), "all")
				hasStr(t, render80(v), "m md+")

				v.HandleInput("m")
				eqStr(t, string(modes[len(modes)-1]), "off")
				off := render80(v)
				hasStr(t, off, "m raw")
				// The override, not just the label, is what took effect.
				hasStr(t, off, "# Heading")

				v.HandleInput("m")
				eqStr(t, string(modes[len(modes)-1]), "assistant")
			})
			tw(t, file, "`m` still cycles when no persist hook is wired", func(t *testing.T) {
				v, _ := viewerFor(assistant("# Heading"), viewerMarkdownAssistant, nil, 200)
				v.HandleInput("m")
				v.HandleInput("m")
				hasStr(t, render80(v), "# Heading")
			})
			tw(t, file, "`m` disarms a pending stop rather than confirming it", func(t *testing.T) {
				var stops int
				v, _ := newViewer(200, mockSession(assistantMsg("hi")), mockRecordView(nil), nil, ansiTheme(), viewerOptions{OnStop: func() { stops++ }})
				v.HandleInput("x")
				v.HandleInput("m")
				v.HandleInput("x")
				if stops != 0 {
					t.Error("stopped")
				}
			})
			tw(t, file, "keeps the footer's navigation hints intact at 80 columns", func(t *testing.T) {
				v, _ := newViewer(200, mockSession(assistantMsg("hi")), mockRecordView(nil), nil, ansiTheme(), viewerOptions{OnStop: func() {}, OnSteer: func(string) {}})
				lines := v.Render(80)
				footer := stripANSI(lines[len(lines)-2])
				hasStr(t, footer, "Enter steer")
				hasStr(t, footer, "x stop")
				hasStr(t, footer, "m md")
				hasStr(t, footer, "Esc close")
			})
			tw(t, file, "caps a tool result at RESULT_MAX_CHARS, not 500, and says what it dropped", func(t *testing.T) {
				var lines []string
				for i := 0; i < 3000; i++ {
					lines = append(lines, fmt.Sprintf("line %d", i))
				}
				v, _ := viewerFor(result(strings.Join(lines, "\n")), "", nil, 4000)
				out := render80(v)
				hasStr(t, out, "line 100") // far past the old 500-char cut
				lacksStr(t, out, "line 2999")
				if !regexp.MustCompile(`\.\.\. \(truncated, [\d.]+[kM]? more characters\)`).MatchString(out) {
					t.Error("no truncation notice")
				}
			})
			tw(t, file, "puts the truncation notice outside the code fence it cut into", func(t *testing.T) {
				text := "```js\n" + strings.Repeat("const a = 1;\n", 2000) + "```"
				v, _ := viewerFor(result(text), viewerMarkdownAll, nil, 4000)
				var note string
				for _, l := range v.buildContentLines(76) {
					if p := stripANSI(l); strings.Contains(p, "... (truncated") {
						note = p
					}
				}
				if !regexp.MustCompile(`^\.\.\. \(truncated, [\d.]+[kM]? more characters\)$`).MatchString(note) {
					t.Errorf("%q", note)
				}
			})
			tw(t, file, "reports the exact omitted character count", func(t *testing.T) {
				// UTF-16 code units, so the astral character here counts as two.
				v, _ := viewerFor(result(strings.Repeat("x", resultMaxChars)+"😀x"), "", nil, 200)
				var got []string
				for _, l := range v.buildContentLines(76) {
					got = append(got, stripANSI(l))
				}
				if !containsString(got, "... (truncated, 3 more characters)") {
					t.Errorf("%q", got)
				}
			})
			tw(t, file, "abbreviates a large omitted count so the notice fits a narrow frame", func(t *testing.T) {
				// The notice goes through truncateToWidth at innerW (width - 4): an exact count runs to seven digits on a
				// multi-megabyte result and pushes the notice past 46, where the unit is cut off.
				v, _ := viewerFor(result(strings.Repeat("x", resultMaxChars)+strings.Repeat("y", 1_100_000)), "", nil, 200)
				var note string
				for _, l := range v.Render(50) {
					if p := stripANSI(l); strings.Contains(p, "truncated,") {
						note = p
					}
				}
				hasStr(t, note, "1.1M more characters)")
			})
			tw(t, file, "rounds into the M bracket rather than reporting 1000k", func(t *testing.T) {
				// 999,999 / 1000 rounds to 1000.0: the bracket has to be picked against the rounded value.
				v, _ := viewerFor(result(strings.Repeat("x", resultMaxChars)+strings.Repeat("y", 999_999)), "", nil, 200)
				var note string
				for _, l := range strings.Split(render80(v), "\n") {
					if strings.Contains(l, "truncated,") {
						note = l
					}
				}
				hasStr(t, note, "1M more characters")
			})
			tw(t, file, "falls back to literal wrapping once for an unsafe streaming prefix", func(t *testing.T) {
				// render() is on the TUI's critical path, so a parser throw must degrade rather than take the overlay down.
				messages := result("# heading")
				v, p := viewerFor(messages, viewerMarkdownAll, nil, 200)
				p.throws = true

				hasStr(t, render80(v), "# heading")
				// An append-only delta keeps the unsafe prefix, so it must stay literal without retrying the parser on
				// every streamed update.
				block := messages[0]["content"].([]any)[0].(obj)
				block["text"] = block["text"].(string) + "\nmore"
				hasStr(t, render80(v), "more")
				if p.renderCalls != 1 {
					t.Errorf("render attempts: %d", p.renderCalls)
				}

				p.throws = false
				hasStr(t, render80(v), "# heading")
				if p.renderCalls != 1 {
					t.Errorf("render attempts: %d", p.renderCalls)
				}

				// Replacing the failed content can remove the unsafe prefix, so it gets one fresh Markdown attempt
				// instead of staying literal forever.
				block["text"] = "## safe"
				replaced := render80(v)
				if p.renderCalls != 2 {
					t.Errorf("render attempts: %d", p.renderCalls)
				}
				hasStr(t, replaced, "safe")
				lacksStr(t, replaced, "## safe")
			})
			tw(t, file, "tracks a tool result that keeps growing past the cap", func(t *testing.T) {
				// The live case: the capped prefix never changes, so the parse is reused, but the character count being
				// held back has to keep moving.
				msg := resultMsg(strings.Repeat("row\n", 4500))
				v, p := viewerFor([]obj{msg}, "", nil, 200)
				elided := func() float64 {
					var all []string
					for _, l := range v.buildContentLines(76) {
						all = append(all, stripANSI(l))
					}
					m := regexp.MustCompile(`truncated, ([\d.]+)([kM]?) more`).FindStringSubmatch(strings.Join(all, "\n"))
					if m == nil {
						return 0
					}
					n, _ := strconv.ParseFloat(m[1], 64)
					switch m[2] {
					case "M":
						return n * 1e6
					case "k":
						return n * 1e3
					}
					return n
				}
				before := elided()
				block := msg["content"].([]any)[0].(obj)
				block["text"] = block["text"].(string) + strings.Repeat("row\n", 1000)
				after := elided()
				if !(before > 0) || !(after > before) {
					t.Errorf("before %v, after %v", before, after)
				}
				if p.constructions != 0 { // default mode: results take the literal path
					t.Errorf("%d Markdown constructions", p.constructions)
				}
			})
			tw(t, file, "leaves a result under the cap untouched", func(t *testing.T) {
				// Deliberately between the old 500-char cap and the new one, so the test discriminates the cap's value.
				text := "head\n" + strings.Repeat("filler line\n", 200) + "tail"
				v, _ := viewerFor(result(text), "", nil, 600)
				out := render80(v)
				if len(text) >= resultMaxChars {
					t.Fatal("fixture too long")
				}
				hasStr(t, out, "head")
				hasStr(t, out, "tail")
				lacksStr(t, out, "truncated")
			})
			tw(t, file, "caps bash output with the same rule as a tool result", func(t *testing.T) {
				v, _ := viewerFor([]obj{{"role": "bashExecution", "command": "yes", "output": strings.Repeat("y\n", 20000)}}, "", nil, 200)
				if !regexp.MustCompile(`\.\.\. \(truncated, [\d.]+[kM]? more characters\)`).MatchString(render80(v)) {
					t.Error("no truncation notice")
				}
			})
			tw(t, file, "keeps tool results dim even when rendering them as Markdown", func(t *testing.T) {
				// Reads the content line directly: every bordered row carries the theme's escape on its `│`.
				v, _ := viewerFor(result("plain result text"), viewerMarkdownAll, nil, 200)
				var line string
				for _, l := range v.buildContentLines(76) {
					if strings.Contains(stripANSI(l), "plain result text") {
						line = l
					}
				}
				hasStr(t, line, "\x1b[38;5;240m")
			})
			tw(t, file, "keeps tool results dim on the literal path too", func(t *testing.T) {
				v, _ := viewerFor(result("plain result text"), "", nil, 200)
				var line string
				for _, l := range v.buildContentLines(76) {
					if strings.Contains(stripANSI(l), "plain result text") {
						line = l
					}
				}
				hasStr(t, line, "\x1b[38;5;240m")
			})
			tw(t, file, "reuses one Markdown per message across renders", func(t *testing.T) {
				v, p := viewerFor(assistant("# Heading"), "", nil, 200)
				v.Render(80)
				afterFirst := p.constructions
				v.Render(80)
				v.Render(80)
				if afterFirst != 1 || p.constructions != afterFirst {
					t.Errorf("constructions: first %d, after %d", afterFirst, p.constructions)
				}
			})
			tw(t, file, "re-renders a message whose text is still streaming", func(t *testing.T) {
				messages := assistant("# One")
				v, p := viewerFor(messages, "", nil, 200)
				hasStr(t, render80(v), "One")
				messages[0]["content"].([]any)[0].(obj)["text"] = "# Two"
				out := render80(v)
				hasStr(t, out, "Two")
				lacksStr(t, out, "One")
				if p.constructions != 1 {
					t.Errorf("constructions: %d", p.constructions)
				}
			})
			tw(t, file, "renders Markdown to fit, so the overwidth clamp never has to cut it", func(t *testing.T) {
				text := "# " + strings.Repeat("Heading ", 20) + "\n\n| a | b |\n|---|---|\n| " + strings.Repeat("x", 90) + " | 2 |\n\n```js\nconst x = " + strings.Repeat("1", 120) + ";\n```"
				// From 20 up: below that the `[Assistant]` role label is itself wider than the viewport, so the clamp
				// legitimately fires on chrome rather than content.
				for _, w := range []int{20, 40, 80, 120} {
					v, _ := newViewer(30, mockSession(assistantMsg(text)), mockRecordView(nil), nil, ansiTheme(), viewerOptions{})
					content := v.buildContentLines(w)
					assertAllLinesFit(t, content, w)
					// truncateToWidth is the #7 backstop, not what keeps these in bounds: if it fires on Markdown
					// output, content is being silently cut.
					for _, l := range content {
						if strings.HasSuffix(stripANSI(l), "...") {
							t.Errorf("width %d: clamp fired on %q", w, l)
						}
					}
				}
			})
		})

		t.Run("safety net against upstream wrapTextWithAnsi bugs", func(t *testing.T) {
			// These call buildContentLines() directly because render() has its own truncation via row(): the safety net
			// in buildContentLines is what prevents the TUI crash, and must clamp independently of render().
			clamp := func(t *testing.T, messages []obj, wrapped string) {
				w := 80
				v, _ := newViewer(30, mockSession(messages...), mockRecordView(nil), nil, ansiTheme(), viewerOptions{})
				v.wrap = func(string, int) []string { return []string{wrapped} }
				assertAllLinesFit(t, v.buildContentLines(w), w)
			}
			tw(t, file, "mock is intercepting wrapTextWithAnsi", func(t *testing.T) {
				v, _ := newViewer(30, mockSession(), mockRecordView(nil), nil, ansiTheme(), viewerOptions{})
				v.wrap = func(string, int) []string { return []string{"MOCK_SENTINEL"} }
				if got := v.wrap("anything", 10); !reflect.DeepEqual(got, []string{"MOCK_SENTINEL"}) {
					t.Errorf("%q", got)
				}
			})
			tw(t, file, "clamps overwidth lines from toolResult content", func(t *testing.T) {
				clamp(t, []obj{resultMsg("output")}, strings.Repeat("X", 80+50))
			})
			tw(t, file, "clamps overwidth lines from user message content", func(t *testing.T) {
				clamp(t, []obj{userMsg("hello")}, strings.Repeat("Y", 80+100))
			})
			tw(t, file, "clamps overwidth lines from assistant message content", func(t *testing.T) {
				// Under the default mode an assistant message is rendered as Markdown, which does not go through the
				// wrap function; the `off` mode is the literal path the original's mock reaches too.
				w := 80
				v, _ := newViewer(30, mockSession(assistantMsg("response")), mockRecordView(nil), nil, ansiTheme(), viewerOptions{ViewerMarkdown: func() viewerMarkdownMode { return viewerMarkdownOff }})
				v.wrap = func(string, int) []string { return []string{strings.Repeat("Z", w+100)} }
				assertAllLinesFit(t, v.buildContentLines(w), w)
			})
			tw(t, file, "clamps overwidth lines from bashExecution output", func(t *testing.T) {
				clamp(t, []obj{{"role": "bashExecution", "command": "ls", "output": "out"}}, strings.Repeat("B", 80+100))
			})
			tw(t, file, "clamps overwidth lines that also contain ANSI codes", func(t *testing.T) {
				clamp(t, []obj{resultMsg("output")}, "\x1b[1m\x1b[31m"+strings.Repeat("W", 80+30)+"\x1b[0m")
			})
		})

		t.Run("stop key", func(t *testing.T) {
			const W = 80
			newStopViewer := func(status string, onStop func()) (*conversationViewer, *fakeHost) {
				return newViewer(30, mockSession(), mockRecordView(func(r *agentView) { r.Status = status }), nil, ansiTheme(), viewerOptions{OnStop: onStop})
			}
			render := func(v *conversationViewer) string { return strings.Join(v.Render(W), "\n") }
			tw(t, file, "two-press x stops a running agent (first arms, second aborts)", func(t *testing.T) {
				var stops int
				v, host := newStopViewer("running", func() { stops++ })
				// Idle footer offers the stop affordance.
				hasStr(t, render(v), "x stop")
				// First press arms (no abort yet) and re-renders.
				v.HandleInput("x")
				if stops != 0 {
					t.Error("stopped on the first press")
				}
				if host.requested() == 0 {
					t.Error("no re-render requested")
				}
				hasStr(t, render(v), "x again to STOP")
				// Second press aborts.
				v.HandleInput("x")
				if stops != 1 {
					t.Errorf("stops: %d", stops)
				}
			})
			tw(t, file, "any other key disarms the confirm", func(t *testing.T) {
				var stops int
				v, _ := newStopViewer("running", func() { stops++ })
				v.HandleInput("x") // arm
				v.HandleInput("j") // scroll → disarm
				hasStr(t, render(v), "x stop")
				lacksStr(t, render(v), "x again to STOP")
				v.HandleInput("x") // arms again, does NOT stop
				if stops != 0 {
					t.Error("stopped")
				}
			})
			tw(t, file, "does not offer or perform stop once the agent is no longer running", func(t *testing.T) {
				var stops int
				v, _ := newStopViewer("completed", func() { stops++ })
				lacksStr(t, render(v), "x stop")
				v.HandleInput("x")
				v.HandleInput("x")
				if stops != 0 {
					t.Error("stopped")
				}
			})
			tw(t, file, "no stop affordance when no onStop handler is provided (read-only history)", func(t *testing.T) {
				v, _ := newStopViewer("running", nil)
				lacksStr(t, render(v), "x stop")
				v.HandleInput("x")
				v.HandleInput("x")
			})
		})

		t.Run("steer composer", func(t *testing.T) {
			const W = 80
			type viewerCase struct {
				v      *conversationViewer
				steers *[]string
			}
			makeViewer := func(status string, withSteer bool) viewerCase {
				steers := &[]string{}
				opts := viewerOptions{}
				if withSteer {
					opts.OnSteer = func(m string) { *steers = append(*steers, m) }
				}
				v, _ := newViewer(30, mockSession(), mockRecordView(func(r *agentView) { r.Status = status }), nil, ansiTheme(), opts)
				return viewerCase{v, steers}
			}
			render := func(v *conversationViewer) string { return strings.Join(v.Render(W), "\n") }
			typeText := func(v *conversationViewer, s string) {
				for _, ch := range s {
					v.HandleInput(string(ch))
				}
			}
			tw(t, file, "offers the steer affordance for a running agent and opens on Enter", func(t *testing.T) {
				c := makeViewer("running", true)
				hasStr(t, render(c.v), "Enter steer")
				c.v.HandleInput("\r") // Enter
				// Composer is shown (its prompt + send/cancel hint), idle footer is gone.
				out := render(c.v)
				hasStr(t, out, "Enter send · Esc cancel")
				lacksStr(t, out, "Enter steer")
			})
			tw(t, file, "typing then Enter sends the trimmed message and closes the composer", func(t *testing.T) {
				c := makeViewer("running", true)
				c.v.HandleInput("\r") // open composer
				typeText(c.v, "  hello  ")
				c.v.HandleInput("\r") // send
				if !reflect.DeepEqual(*c.steers, []string{"hello"}) {
					t.Errorf("%q", *c.steers)
				}
				lacksStr(t, render(c.v), "Enter send") // composer closed
			})
			tw(t, file, "Esc cancels the composer without sending", func(t *testing.T) {
				c := makeViewer("running", true)
				c.v.HandleInput("\r") // open composer
				typeText(c.v, "draft")
				c.v.HandleInput("\x1b") // Esc
				if len(*c.steers) != 0 {
					t.Error("sent")
				}
				lacksStr(t, render(c.v), "Enter send")
			})
			tw(t, file, "an empty submit just returns (like Esc), without calling onSteer", func(t *testing.T) {
				c := makeViewer("running", true)
				c.v.HandleInput("\r") // open composer
				c.v.HandleInput("\r") // empty submit
				if len(*c.steers) != 0 {
					t.Error("sent")
				}
				lacksStr(t, render(c.v), "Enter send") // composer closed
			})
			tw(t, file, "scroll keys are inert while composing (input owns them)", func(t *testing.T) {
				c := makeViewer("running", true)
				c.v.HandleInput("\r") // open composer
				// 'j' would normally scroll, but here it types into the composer.
				c.v.HandleInput("j")
				hasStr(t, render(c.v), "Enter send · Esc cancel")
			})
			tw(t, file, "no steer affordance once the agent is no longer running", func(t *testing.T) {
				c := makeViewer("completed", true)
				lacksStr(t, render(c.v), "Enter steer")
				c.v.HandleInput("\r")
				lacksStr(t, render(c.v), "Enter send")
				if len(*c.steers) != 0 {
					t.Error("sent")
				}
			})
			tw(t, file, "no steer affordance when no onSteer handler is provided", func(t *testing.T) {
				c := makeViewer("running", false)
				lacksStr(t, render(c.v), "Enter steer")
				c.v.HandleInput("\r")
			})
			tw(t, file, "composer rows never exceed width", func(t *testing.T) {
				for _, w := range []int{40, 80, 120} {
					v, _ := newViewer(30, mockSession(), mockRecordView(nil), nil, ansiTheme(), viewerOptions{OnSteer: func(string) {}})
					v.HandleInput("\r") // open composer
					typeText(v, strings.Repeat("x", 200))
					assertAllLinesFit(t, v.Render(w), w)
				}
			})
		})
	})
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var _ = time.Now
