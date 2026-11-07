package posthorse

// Twins of test/renderers.test.ts that run the real tools (execute(...) of the original) through the Posthorse
// fixture and draw their results with the registered cards (lane E).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// uiAttach is a uiHost over the fixture's host: it is given Pi's theme and expand keybinding.
func uiAttach(t *testing.T, f *fixture) *uiHost {
	t.Helper()
	u := &uiHost{t: t, h: f.H()}
	u.setTheme(uiDark)
	u.state(map[string]any{
		"keybindings": map[string]any{
			"definitions":  map[string]any{"app.tools.expand": map[string]any{"defaultKeys": "ctrl+o", "description": "Expand tools"}},
			"userBindings": map[string]any{},
		},
	})
	return u
}

// uiResultOf is the `{content, details}` a card receives from a tool result.
func uiResultOf(res fixResult) map[string]any {
	content := make([]any, len(res.Content))
	for i, part := range res.Content {
		content[i] = map[string]any(part)
	}
	out := map[string]any{"content": content}
	if res.Details != nil {
		out["details"] = map[string]any(res.Details)
	}
	return out
}

// uiUsageOpt is a usage with explicit null percent: `{tokens, contextWindow: window, percent: null}`.
func uiUsageOpt(window int, tokens *int) fixOpt {
	return func(s *fixState) {
		s.modelWindow = window
		s.usage = &fixUsage{Tokens: tokens, Window: window}
	}
}

// uiCwd is the original's context(cwd): the working directory is also the session directory (`getSessionDir: () => cwd`), so a
// history search over all sessions reads that directory and not the shared os.TempDir().
func uiCwd(dir string) fixOpt {
	return func(s *fixState) {
		withCwd(dir)(s)
		withSessionDir(dir)(s)
	}
}

// uiRun is `execute(name, args, ctx)`: the original's helper builds a fresh extension for every call.
func uiRun(t *testing.T, name string, args rec, opts ...fixOpt) fixResult {
	t.Helper()
	return newFixture(t, opts...).Run(name, args)
}

func uiNumber(n int) string { return jsToLocaleInt(n) }

func uiRealDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func uiDetailsLen(t *testing.T, details rec) int {
	t.Helper()
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

func TestTwinRenderersUiTools(t *testing.T) {
	// renderers.test.ts:263
	tw(t, "renderers", "notes show page range and next offset even when the preview fills the compact card", func(t *testing.T) {
		dir := uiRealDir(t)
		f := newFixture(t, uiCwd(dir))
		body := "START " + strings.Repeat("n", 56_176) + " END"
		f.Run("notes", rec{"op": "write", "path": "ledger.md", "content": body})
		args := rec{"op": "read", "path": "ledger.md"}
		output := f.Run("notes", args)
		snapshot, _ := json.Marshal(output.Raw)
		u := uiAttach(t, f)
		c := u.card("notes", args)
		c.updateResult(uiResultOf(output), false, false)
		compact := uiText(t, c, 40)
		end := 40_000 - u16len("File: "+filepath.Join(dir, ".pi", "notes", "ledger.md")+"\n")
		uiMatch(t, fmt.Sprintf(`(?s)0[–-]%s.*56,186`, regexp.QuoteMeta(uiNumber(end))), compact)
		uiMatch(t, fmt.Sprintf(`offset %s`, uiNumber(end)), compact)
		uiMatch(t, `START`, compact)
		uiNoMatch(t, `File:`, compact) // the path header must not displace the compact content preview
		if rows := len(uiLines(t, c, 40)); rows > 10 {
			t.Fatalf("%d rows", rows)
		}
		if !c.click() { // the visible body also expands (the click itself is Pi's shell)
			t.Fatal("the click is handled")
		}
		uiMatch(t, fmt.Sprintf(`continue with offset %d`, end), uiText(t, c, 80))
		if !strings.Contains(uiText(t, c, 196), filepath.Join(dir, ".pi", "notes", "ledger.md")) {
			t.Fatal("the expanded card shows the note path")
		}
		after, _ := json.Marshal(output.Raw)
		if string(after) != string(snapshot) {
			t.Fatal("rendering must not mutate model-visible output")
		}
		if n := uiDetailsLen(t, output.Details); n >= 200 {
			t.Fatalf("page metadata must not duplicate the note: %d", n)
		}
	})

	// renderers.test.ts:339
	tw(t, "renderers", "empty history has one compact summary and keeps the returned text on expansion", func(t *testing.T) {
		f := newFixture(t, uiCwd(uiRealDir(t)))
		args := rec{"op": "search", "query": "absent", "all": true}
		output := f.Run("history", args)
		u := uiAttach(t, f)
		c := u.card("history", args)
		c.updateResult(uiResultOf(output), false, false)
		uiMatch(t, `No matches · all sessions`, uiText(t, c, 80))
		uiNoMatch(t, `No history matches`, uiText(t, c, 80))
		c.setExpanded(true)
		uiMatch(t, `No history matches "absent"\.`, uiText(t, c, 80))
		if !reflect.DeepEqual(output.Content, []rec{{"type": "text", "text": `No history matches "absent".`}}) {
			t.Fatalf("content %v", output.Content)
		}
	})

	// renderers.test.ts:358
	tw(t, "renderers", "history searches lead with content and retain every entry and recovery identifier expanded", func(t *testing.T) {
		entries := make([]rec, 30)
		for i := range entries {
			entries[i] = rec{
				"type": "message", "id": fmt.Sprintf("entry-%d", i), "timestamp": "2026-09-06T17:43:46.741Z",
				"message": rec{"role": "user", "content": fmt.Sprintf("Relay station %d: %s", i, strings.Repeat("route details ", 20))},
			}
		}
		f := newFixture(t, uiCwd(uiRealDir(t)), withSession(fxIndexedSession(entries...)), withEmptyProjection())
		args := rec{"op": "search", "query": "Relay station", "limit": float64(30)}
		output := f.Run("history", args)
		u := uiAttach(t, f)
		c := u.card("history", args)
		c.updateResult(uiResultOf(output), false, false)
		uiMatch(t, `30 matches`, uiText(t, c, 40))
		uiMatch(t, `Relay station 29`, uiText(t, c, 40))
		uiMatch(t, `Relay station 28`, uiText(t, c, 40))
		uiNoMatch(t, `2026-09-06T17`, uiText(t, c, 40))
		if rows := len(uiLines(t, c, 40)); rows > 10 {
			t.Fatalf("%d rows", rows)
		}
		c.setExpanded(true)
		expanded := uiText(t, c, 196)
		for i := range 30 {
			uiMatch(t, fmt.Sprintf(`\[entry-%d\]`, i), expanded)
		}
		if strings.Index(expanded, "Relay station 29") >= strings.Index(expanded, "2026-09-06T17") {
			t.Fatal("content leads, the header follows")
		}
		if n := uiDetailsLen(t, output.Details); n >= 2000 {
			t.Fatalf("search display data is numeric boundaries, not copied bodies: %d", n)
		}
	})

	// renderers.test.ts:393
	tw(t, "renderers", "paginated searches retain accurate counts, spans, identifiers and continuation in native cards", func(t *testing.T) {
		entries := make([]rec, 60)
		for i := range entries {
			entries[i] = rec{
				"type": "message", "id": fmt.Sprintf("entry-%d", i), "timestamp": "2026-09-06T17:43:46.741Z",
				"message": rec{"role": "user", "content": fmt.Sprintf("needle station %d %s", i, strings.Repeat("r", 500))},
			}
		}
		args := rec{"op": "search", "query": "needle", "limit": float64(50)}
		// Late in the window, the remaining budget rather than the result limit ends the page.
		late := newFixture(t, uiCwd(uiRealDir(t)), withSession(fxIndexedSession(entries...)), withEmptyProjection(), withUsage(100_000, 78_000))
		output := late.Run("history", args)
		kind, _ := output.Details["kind"].(string)
		if kind != "history-search" {
			t.Fatalf("kind %q", kind)
		}
		spans, _ := output.Details["entries"].([]any)
		footerLength := int(output.Details["footerLength"].(float64))
		if len(spans) >= 50 || footerLength <= 0 {
			t.Fatalf("entries %d footer %d", len(spans), footerLength)
		}
		raw := output.Text()
		if u16len(raw) > 20_000 {
			t.Fatalf("raw %d", u16len(raw))
		}
		sum := -1
		for _, span := range spans {
			sum += int(span.(map[string]any)["length"].(float64)) + 1
		}
		if sum != u16len(raw)-footerLength {
			t.Fatalf("spans cover %d, body %d", sum, u16len(raw)-footerLength)
		}
		u := uiAttach(t, late)
		c := u.card("history", args)
		c.updateResult(uiResultOf(output), false, false)
		uiMatch(t, fmt.Sprintf(`%d matches`, len(spans)), uiText(t, c, 80))
		uiMatch(t, `More results; continue with cursor`, uiText(t, c, 80))
		c.setExpanded(true)
		uiMatch(t, `\[entry-59\]`, uiText(t, c, 196))
		uiMatch(t, `\[More results; continue with cursor`, uiText(t, c, 196))
		cursor := regexp.MustCompile(`cursor "([^"]+)"`).FindStringSubmatch(u16slice(raw, u16len(raw)-footerLength, u16len(raw)))
		if cursor == nil {
			t.Fatal("the footer carries the cursor")
		}
		uiMatch(t, regexp.QuoteMeta(cursor[1]), uiText(t, c, 196))

		dir := uiRealDir(t)
		f := newFixture(t, uiCwd(dir))
		rows := make([]string, 30)
		for i := range rows {
			rows[i] = fmt.Sprintf("needle %d %s", i, strings.Repeat("n", 150))
		}
		// Every execute(...) of the original builds a fresh extension (setup()), so each call here does too.
		uiRun(t, "notes", rec{"op": "write", "path": "ledger.md", "content": strings.Join(rows, "\n")}, uiCwd(dir))
		lateOpts := []fixOpt{uiCwd(dir), withUsage(100_000, 98_000), withEnabled(false), withReserve(16_384)}
		searchResult := uiRun(t, "notes", rec{"op": "search", "query": "needle"}, lateOpts...)
		if kind := searchResult.Details["kind"]; kind != "notes-search" {
			t.Fatalf("kind %v", kind)
		}
		page := searchResult.Details["page"].(map[string]any)
		count := int(searchResult.Details["count"].(float64))
		if page["end"].(float64) >= page["total"].(float64) {
			t.Fatalf("the page ends before the text: %v", page)
		}
		un := uiAttach(t, f)
		notes := un.card("notes", map[string]any{"op": "search", "query": "needle"})
		notes.updateResult(uiResultOf(searchResult), false, false)
		uiMatch(t, fmt.Sprintf(`%d matches returned`, count), uiText(t, notes, 80))
		uiMatch(t, `Next offset`, uiText(t, notes, 80))
		notes.setExpanded(true)
		uiMatch(t, fmt.Sprintf(`continue with offset %d`, int(page["end"].(float64))), uiText(t, notes, 80))

		more := make([]string, 300)
		for i := range more {
			more[i] = fmt.Sprintf("needle %d %s", i, strings.Repeat("m", 150))
		}
		uiRun(t, "notes", rec{"op": "write", "path": "more.md", "content": strings.Join(more, "\n")}, lateOpts...)
		partial := uiRun(t, "notes", rec{"op": "search", "query": "needle"}, lateOpts...)
		if partial.Details["kind"] != "notes-search" || partial.Details["more"] != true || partial.Details["page"] == nil {
			t.Fatalf("details %v", partial.Details)
		}
		partialCard := un.card("notes", map[string]any{"op": "search", "query": "needle"})
		partialCard.updateResult(uiResultOf(partial), false, false)
		total := int(partial.Details["page"].(map[string]any)["total"].(float64))
		// a search that stopped early must not show its count so far as the total
		uiMatch(t, fmt.Sprintf(`of %s\+\s+Next offset`, regexp.QuoteMeta(uiNumber(total))), uiText(t, partialCard, 80))
	})

	// renderers.test.ts:460
	tw(t, "renderers", "context summaries preserve approximation and native disabled, unsupported, unknown states", func(t *testing.T) {
		tokens1000 := 1000
		for _, tc := range []struct {
			tokens   *int
			window   int
			enabled  bool
			expected string
		}{
			{&tokens1000, 100_000, true, `≈.*rollover`},
			{&tokens1000, 100_000, false, `(?i)disabled`},
			{&tokens1000, 8192, true, `(?i)unsupported`},
			{nil, 100_000, true, `(?i)unknown|not known`},
		} {
			f := newFixture(t, uiCwd(uiRealDir(t)), uiUsageOpt(tc.window, tc.tokens), withEnabled(tc.enabled), withReserve(16_384))
			output := f.Run("get_context_remaining", rec{})
			u := uiAttach(t, f)
			c := u.card("get_context_remaining", map[string]any{})
			c.updateResult(uiResultOf(output), false, false)
			uiMatch(t, tc.expected, uiText(t, c, 80))
			uiNoMatch(t, `0%|hard limit`, uiText(t, c, 80))
			if tc.tokens != nil {
				uiMatch(t, `configured context limit`, uiText(t, c, 80))
			}
			c.setExpanded(true)
			if !regexp.MustCompile(`native\s+estimate`).MatchString(uiText(t, c, 196)) && tc.tokens != nil {
				t.Fatalf("expanded text lacks the native estimate: %q", uiText(t, c, 196))
			}
		}
	})

	// renderers.test.ts:479
	tw(t, "renderers", "new_context remains a conditional request and only its committed message says the window started", func(t *testing.T) {
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = fmt.Sprintf("Handoff line %d", i+1)
		}
		handoff := strings.Join(lines, "\n")
		args := rec{"handoff": handoff}
		f := newFixture(t, uiCwd(uiRealDir(t)))
		output := f.Run("new_context", args)
		uiMatch(t, `(?s)Requested.*foreground tools succeed.*Background work continues`, output.Text())
		u := uiAttach(t, f)
		c := u.card("new_context", args)
		c.updateResult(uiResultOf(output), false, false)
		uiMatch(t, `(?s)Requested.*foreground tools succeed`, uiText(t, c, 80))
		uiMatch(t, `Background work continues`, uiText(t, c, 80))
		uiNoMatch(t, `(?i)committed|window started`, uiText(t, c, 80))
		c.setExpanded(true)
		uiMatch(t, `Handoff line 60`, uiText(t, c, 80))

		content := "Context window window-2 starts here. Earlier conversation is not available in this window.\n\nHandoff from the previous window:\n" + handoff
		committed := u.message("context-window", content, 0)
		committed.message["details"] = map[string]any{"windowId": "window-2", "tokensBefore": float64(1000)}
		uiMatch(t, `history`, uiText(t, committed, 80))
		if rows := len(uiLines(t, committed, 40)); rows > 10 {
			t.Fatalf("%d rows", rows)
		}
		uiNoMatch(t, `Handoff line 60`, uiText(t, committed, 80))
		// Global expansion resets the local choice (ui.ts:24-27): expanded, then collapsed, then expanded again.
		committed.setExpanded(true)
		uiMatch(t, `Handoff line 60`, uiText(t, committed, 80))
		committed.setExpanded(false)
		uiNoMatch(t, `Handoff line 60`, uiText(t, committed, 80))
		committed.setExpanded(true)
		uiMatch(t, `Handoff line 60`, uiText(t, committed, 80))
		committed.setExpanded(false)
		committed.outputPad = 2 // setOutputPad(2); invalidate(): padding and theme changes keep the choice
		uiNoMatch(t, `Handoff line 60`, uiText(t, committed, 80))
		if !strings.HasSuffix(content, handoff) {
			t.Fatal("the message content is untouched")
		}
		t.Run("click toggles the message's local state", func(t *testing.T) {
			t.Skip("the original answers a left click through a pi-tui MouseRegion over per-message state (ui.ts:14-17,74-80); the Go SDK delivers no mouse event and no message identity to a message renderer (docs/plan/extension-component-kit.md: renderer views take no mouse), so only the global expansion is observable here (see lane-UiLane.md, finding 1)")
		})
	})

	// renderers.test.ts:564
	tw(t, "renderers", "history pages keep paging, metadata, and native image attachments without copied bodies", func(t *testing.T) {
		image := fxImage("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "image/png")
		body := "ENTRY HEAD " + strings.Repeat("route ", 8000) + " ENTRY TAIL"
		entry := func(images int) rec {
			content := []any{fxText(body)}
			for range images {
				content = append(content, image)
			}
			return rec{"type": "message", "id": "picture-entry", "timestamp": "2026-09-06T17:43:46Z", "message": rec{"role": "user", "content": content}}
		}
		args := rec{"op": "read", "id": "picture-entry"}
		f := newFixture(t, uiCwd(uiRealDir(t)), withSession(fxIndexedSession(entry(1))), withEmptyProjection())
		output := f.Run("history", args)
		u := uiAttach(t, f)
		c := u.card("history", map[string]any(args))
		c.updateResult(uiResultOf(output), false, false)
		c.setShowImages(false)
		uiMatch(t, `Next offset 40,000`, uiText(t, c, 40))
		uiMatch(t, `1 image attached`, uiText(t, c, 40))
		if rows := len(uiLines(t, c, 40)); rows > 10 {
			t.Fatalf("%d rows", rows)
		}
		if !reflect.DeepEqual(output.Content[1:], []rec{image}) {
			t.Fatalf("content %v", output.Content[1:])
		}
		if n := uiDetailsLen(t, output.Details); n >= 200 {
			t.Fatalf("details %d", n)
		}
		c.setExpanded(true)
		expanded := uiText(t, c, 196)
		if strings.Index(expanded, "ENTRY HEAD") >= strings.Index(expanded, "2026-09-06T17") {
			t.Fatal("content leads, the header follows")
		}
		uiMatch(t, `\[picture-entry\]`, expanded)
		uiMatch(t, `More remains; call history read`, expanded)
		continuation := uiRun(t, "history", rec{"op": "read", "id": "picture-entry", "offset": float64(40_000)},
			uiCwd(uiRealDir(t)), withSession(fxIndexedSession(entry(1))), withEmptyProjection())
		if len(continuation.Content) != 1 {
			t.Fatalf("continuation content %d", len(continuation.Content))
		}

		many := newFixture(t, uiCwd(uiRealDir(t)), withSession(fxIndexedSession(entry(13))), withEmptyProjection())
		imagePage := many.Run("history", args)
		if imagePage.Details["kind"] != "history-read" || imagePage.Details["imageEnd"] == nil {
			t.Fatalf("details %v", imagePage.Details)
		}
		end := int(imagePage.Details["end"].(float64))
		imageEnd := int(imagePage.Details["imageEnd"].(float64))
		c.setExpanded(false)
		c.updateResult(uiResultOf(imagePage), false, false)
		for _, width := range []int{24, 100} {
			compact := uiText(t, c, width)
			uiMatch(t, fmt.Sprintf(`Next offset %s`, regexp.QuoteMeta(uiNumber(end))), compact)
			uiMatch(t, fmt.Sprintf(`imageOffset %d`, imageEnd), compact)
			if n := len(regexp.MustCompile(`Next offset`).FindAllString(compact, -1)); n != 1 {
				t.Fatalf("Next offset appears %d times at %d columns", n, width)
			}
		}
		c.setExpanded(true)
		uiMatch(t, fmt.Sprintf(`and offset %d and imageOffset %d`, end, imageEnd), uiText(t, c, 196))
	})
}
