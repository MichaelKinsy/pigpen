package posthorse

// Twins of the core cases of test/posthorse.test.ts (lane A): budgets, guidance, paging reservations,
// the new_context and get_context_remaining tools, and the notes-directory resolution through the notes tool.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// coreMatch is `assert.match(text, /pattern/)`.
func coreMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q to match /%s/", text, pattern)
	}
}

// coreNoMatch is `assert.doesNotMatch(text, /pattern/)`.
func coreNoMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q not to match /%s/", text, pattern)
	}
}

// coreFails is `assert.rejects(run(...), /pattern/)`: the tool must throw an error matching pattern.
func coreFails(t *testing.T, f *fixture, name string, params rec, pattern string) {
	t.Helper()
	_, failure := f.RunErr(name, params)
	if failure == "" {
		t.Fatalf("tool %s must reject (%v)", name, params)
	}
	coreMatch(t, failure, pattern)
}

// coreUsage is `usageContext(base, window, tokens, reserveTokens, enabled)` (posthorse.test.ts:309-323).
func coreUsage(f *fixture, window, tokens, reserve int, enabled bool) *fixture {
	return f.With(withUsage(window, tokens), withModelWindow(window), withReserve(reserve), withEnabled(enabled))
}

// coreTurnStart is `handlers.get("turn_start")?.({}, ctx)`.
func coreTurnStart(f *fixture) { f.Fire("turn_start", rec{}) }

// coreBranch is the unit-test branch context of the paging cases: an implicit-ancestry session, no
// session file, an empty model projection (`sessionIdentity()`, `emptyProjection`, `getBranch`).
func coreBranch(dir string, entries ...rec) []fixOpt {
	return []fixOpt{withCwd(dir), withSession(fxIndexedSession(entries...)), withEmptyProjection(), withNoSessionFile(), withSessionDir(dir)}
}

func coreReal(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func coreWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func coreNum(n int) string { return strconv.Itoa(n) }

func TestCoreTwins(t *testing.T) {
	tw(t, "posthorse", "each safety evaluation reads native usage and policy once, including unknown pages and final handoff validation", func(t *testing.T) {
		dir := coreReal(t)
		coreWrite(t, filepath.Join(dir, ".pi", "notes", "state.md"), "saved state")
		var policyReads atomic.Int32
		var enabled atomic.Bool
		enabled.Store(true)
		f := newFixture(t, withCwd(dir), withPolicyReader(func(sdk.Context) (compactionPolicy, error) {
			policyReads.Add(1)
			return compactionPolicy{enabled: enabled.Load(), reserveTokens: 16_384}, nil
		}))
		once := func(expectedPolicyReads int, action func()) {
			t.Helper()
			f.ResetReads()
			policyReads.Store(0)
			action()
			if got := f.Reads("getContextUsage"); got != 1 {
				t.Fatalf("one authoritative native acquisition per evaluation: %d reads", got)
			}
			if got := int(policyReads.Load()); got != expectedPolicyReads {
				t.Fatalf("at most one settings acquisition per evaluation, only when needed: %d reads, want %d", got, expectedPolicyReads)
			}
		}
		var guidance string
		once(1, func() { guidance = f.GuidanceFor() })
		coreMatch(t, guidance, `rollover line is 83,617 tokens used`)
		for _, known := range []bool{true, false} {
			if known {
				f.Set(withUsage(100_000, 1000))
			} else {
				f.Set(withUsageUnknown(100_000))
			}
			for _, op := range []string{"read", "list", "search"} {
				once(1, func() {
					coreMatch(t, f.Run("notes", rec{"op": op, "path": "state.md", "query": "saved"}).Text(), `state\.md|saved state`)
				})
			}
			once(1, func() {
				coreMatch(t, f.Run("new_context", rec{"handoff": "continue"}).Text(), `Requested`)
			})
		}
		enabled.Store(false)
		var drafts rec
		once(1, func() {
			drafts = f.TurnEndEvent(rec{
				"message":     rec{"role": "assistant", "stopReason": "toolUse"},
				"toolResults": []any{rec{"toolName": "new_context", "toolCallId": "id"}},
				"entries":     []any{},
				"context":     rec{"pendingMessages": []any{}},
			})
		})
		entries, ok := drafts["entries"].([]any)
		if !ok || len(entries) == 0 {
			t.Fatalf("expected draft entries, got %v", drafts)
		}
		if summary := entries[0].(map[string]any)["summary"]; summary != "continue" {
			t.Fatalf("final handoff evaluation is independent of execute: summary %v", summary)
		}
		reminder := func() rec {
			return rec{"role": "custom", "customType": "posthorse-reminder", "details": rec{"windowId": "initial", "contextWindow": float64(100_000), "reserveTokens": float64(16_384)}}
		}
		messages := make([]rec, 30)
		for i := range messages {
			messages[i] = reminder()
		}
		var filtered rec
		once(1, func() { filtered = f.ContextEvent(messages...) })
		if list, ok := filtered["messages"].([]any); !ok || len(list) != 0 {
			t.Fatalf("a disabled policy filters every reminder: %v", filtered)
		}
		f.Set(withNoUsage())
		once(0, func() {
			coreMatch(t, f.Run("notes", rec{"op": "read", "path": "state.md"}).Text(), `saved state`)
		})
		// "a virtual declaration is not a replacement for the native routed limit"
		coreMatch(t, f.With(withNoUsage(), withModelWindow(2_000_000)).GuidanceFor(), `capacity is unknown`)
	})

	tw(t, "posthorse", "guidance uses request-local sections and preserves custom or forced prompts", func(t *testing.T) {
		f := newFixture(t)
		sections := rec{"other": "Keep this policy"}
		prompt := f.BeforeAgentStart("Custom instructions", rec{"sections": sections, "customPrompt": "Custom instructions"})
		if prompt.Result != nil {
			t.Fatalf("before_agent_start must return undefined, got %v", prompt.Result)
		}
		if sections["other"] != "Keep this policy" {
			t.Fatalf("the handler changed the sections: %v", sections)
		}
		guidance := f.GuidanceFor()
		coreMatch(t, guidance, `Automatic context management \(Posthorse\)`)
		coreMatch(t, guidance, `(?s)goal, progress, decisions, and next steps.*file-editing tools`)
		coreMatch(t, guidance, `verify live state`)
		coreMatch(t, guidance, `Configured context capacity: 100,000 tokens`)
		coreMatch(t, guidance, `rollover line is 83,617 tokens used`)
		coreMatch(t, guidance, `Before reporting a remaining-token count or changing your work plan because of context limits, call get_context_remaining`)
		coreMatch(t, guidance, `If usage is unknown, report it as unknown`)

		forced := f.BeforeAgentStart("", rec{"sections": rec{}, "forceSystemPrompt": "Forced instructions"})
		event := f.contextLike("context_with_system", []rec{{"role": "system", "content": "Forced instructions", "timestamp": float64(0)}})
		if forced.Options["forceSystemPrompt"] != "Forced instructions" {
			t.Fatalf("the forced prompt option changed: %v", forced.Options)
		}
		list, _ := event["messages"].([]any)
		if len(list) < 2 {
			t.Fatalf("the guidance is not delivered as a second message: %v", event)
		}
		if got := list[0].(map[string]any)["content"]; got != "Forced instructions" {
			t.Fatalf("the forced system message changed: %v", got)
		}
		coreMatch(t, fmt.Sprint(list[1].(map[string]any)["content"]), `Configured context capacity: 100,000 tokens`)
	})

	tw(t, "posthorse", "disabled Pi compaction disables automatic Posthorse behavior but not new_context", func(t *testing.T) {
		f := newFixture(t)
		ctx := f.With(withUsage(100_000, 99_000), withEnabled(false))
		guidance := ctx.GuidanceFor()
		coreMatch(t, guidance, `Pi compaction is disabled`)
		coreMatch(t, guidance, `Automatic context management \(Posthorse\)`)
		coreMatch(t, guidance, `Configured context capacity: 100,000 tokens`)
		coreNoMatch(t, guidance, `rollover line is .* tokens used`)
		ctx.TurnEnd()
		if n := len(f.Messages()); n != 0 {
			t.Fatalf("a disabled policy sends no reminder: %d messages", n)
		}
		remaining := ctx.Run("get_context_remaining", rec{}).Text()
		coreMatch(t, remaining, `Automatic rollover is disabled`)
		coreMatch(t, remaining, `1,000 tokens until the configured context limit`)
		coreMatch(t, ctx.Run("new_context", rec{}).Text(), `Requested a fresh Pi context`)
	})

	tw(t, "posthorse", "small-context configurations are unsupported; larger ones derive honest budgets", func(t *testing.T) {
		for _, window := range []int{4096, 8_192, 16_384} {
			f := newFixture(t)
			ctx := coreUsage(f, window, window-500, 16_384, true)
			guidance := ctx.GuidanceFor()
			coreMatch(t, guidance, `unsupported configuration`)
			coreMatch(t, guidance, `Configured context capacity: `+jsToLocaleInt(window)+` tokens`)
			coreMatch(t, guidance, `Lower compaction.reserveTokens in Pi settings or use a larger-context model`)
			coreNoMatch(t, guidance, `% used`)
			ctx.TurnEnd()
			if n := len(f.Messages()); n != 0 {
				t.Fatalf("%d: no reminder, got %d messages", window, n)
			}
			if result := ctx.AutomaticResult(nil); result != nil {
				t.Fatalf("%d: Pi keeps its own compaction, got %v", window, result)
			}
			remaining := ctx.Run("get_context_remaining", rec{}).Text()
			coreMatch(t, remaining, `unsupported configuration`)
			coreMatch(t, remaining, `500 tokens until the configured context limit`)
			coreMatch(t, ctx.Run("new_context", rec{"handoff": "still works"}).Text(), `Requested a fresh Pi context`)
			if oldLimit := min(20_000, window/2*4); oldLimit < 20_000 {
				coreFails(t, ctx, "new_context", rec{"handoff": strings.Repeat("x", oldLimit)}, `(?i)too large for the active model`)
			}
		}

		{
			f := newFixture(t)
			// 32,768 - 16,384 leaves 16,384 usable: line at 16,385 (50%), band 1,638 tokens wide.
			ctx := coreUsage(f, 32_768, 15_000, 16_384, true)
			coreMatch(t, ctx.GuidanceFor(), `rollover line is 16,385 tokens used`)
			ctx.TurnEnd()
			messages := f.Messages()
			if len(messages) != 1 {
				t.Fatalf("expected one reminder, got %d", len(messages))
			}
			coreMatch(t, messages[0].Content, `Routine task-note update`)
			if ctx.AutomaticResult(nil) == nil {
				t.Fatal("expected an automatic result")
			}
		}

		{
			f := newFixture(t)
			large := coreUsage(f, 400_000, 1_000, 64_000, true)
			guidance := large.GuidanceFor()
			coreMatch(t, guidance, `Configured context capacity: 400,000 tokens`)
			coreMatch(t, guidance, `rollover line is 336,001 tokens used`)
			coreMatch(t, guidance, `No routine budget checks are needed`)
			// A 272K window with a 64K reserve has its line at 208,001 (76%).
			sol := coreUsage(f, 272_000, 205_000, 64_000, true)
			switched := sol.GuidanceFor()
			coreMatch(t, switched, `Configured context capacity: 272,000 tokens`)
			coreMatch(t, switched, `rollover line is 208,001 tokens used`)
			coreNoMatch(t, switched, `400,000|336,001`)
			unknownCapacity := f.With(withModelWindow(0), withNoUsage()).GuidanceFor()
			coreMatch(t, unknownCapacity, `Configured context capacity is unknown`)
			coreNoMatch(t, unknownCapacity, `rollover line is .* tokens used`)
			coreMatch(t, sol.Run("get_context_remaining", rec{}).Text(),
				`^≈3,001 tokens until automatic rollover \(line at 208,001\); ≈67,000 tokens until the configured context limit`)
			remaining := coreUsage(f, 100_000, 36_000, 64_000, true).Run("get_context_remaining", rec{}).Text()
			coreMatch(t, remaining,
				`^≈1 tokens until automatic rollover \(line at 36,001\); ≈64,000 tokens until the configured context limit \(36,000/100,000 used, 36%\)\. Best available native estimate\.`)
			coreMatch(t, f.With(withNoUsage()).Run("get_context_remaining", rec{}).Text(), `not known until the next model response`)
		}
	})

	tw(t, "posthorse", "fresh payload budgets count the system prompt and cancel unsafe automatic recovery", func(t *testing.T) {
		f := newFixture(t)
		ctx := coreUsage(f, 32_768, 1000, 16_384, true).With(withSystemPrompt(strings.Repeat("s", 60_000)))
		coreFails(t, ctx, "new_context", rec{"handoff": strings.Repeat("h", 10_000)}, `limit 0`)
		result := ctx.AutomaticResult(nil)
		if len(result) != 1 || result["cancel"] != true {
			t.Fatalf("expected {cancel: true}, got %v", result)
		}
	})

	tw(t, "posthorse", "read pages shrink to the remaining budget and refuse unsafe pages while preserving the offset", func(t *testing.T) {
		dir := coreReal(t)
		f := newFixture(t)
		entry := rec{"type": "message", "id": "long", "parentId": nil, "timestamp": "1", "message": rec{"role": "user", "content": strings.Repeat("h", 25_000)}}
		withBranch := func(ctx *fixture) *fixture { return ctx.With(coreBranch(dir, entry)...).PageContext() }
		withBranch(f).Run("notes", rec{"op": "write", "path": "long.md", "content": strings.Repeat("n", 25_000)})

		// Line at 83,617; 1,500 tokens short of it leaves 500 tokens after the margin: a 2,000-character page.
		tight := withBranch(coreUsage(f, 100_000, 82_117, 16_384, true))
		note := tight.Run("notes", rec{"op": "read", "path": "long.md"}).Text()
		header := "File: " + filepath.Join(dir, ".pi", "notes", "long.md") + "\n"
		end := 2000 - len(header)
		want := header + strings.Repeat("n", end) + "\n[chars 0-" + coreNum(end) + " of 25000; continue with offset " + coreNum(end) + "]"
		if note != want {
			t.Fatalf("tight note page:\n got %q\nwant %q", note, want)
		}
		// These compare independent reads at the same starting usage, not sibling calls.
		coreTurnStart(tight)
		// /\[chars 0-2000 of 25007\] \[user\] h{1993}\nMore remains; call history read with id "long" and offset 2000\.$/
		// (RE2 repeats at most 1000 times, so the h{1993} run is compared as text).
		historyPage := tight.Run("history", rec{"op": "read", "id": "long"}).Text()
		if !strings.HasSuffix(historyPage, "[chars 0-2000 of 25007] [user] "+strings.Repeat("h", 1993)+"\nMore remains; call history read with id \"long\" and offset 2000.") {
			t.Fatalf("history page: %q", historyPage)
		}

		tighter := withBranch(coreUsage(f, 100_000, 82_517, 16_384, true))
		coreFails(t, tighter, "notes", rec{"op": "read", "path": "long.md", "offset": float64(2000)}, `Call new_context first, then retry with offset 2000`)
		coreFails(t, tighter, "history", rec{"op": "read", "id": "long", "offset": float64(2000)}, `Call new_context first, then retry with offset 2000`)

		// Disabled compaction measures against the configured context limit instead of the rollover line.
		disabled := withBranch(coreUsage(f, 100_000, 98_000, 16_384, false))
		coreMatch(t, disabled.Run("notes", rec{"op": "read", "path": "long.md"}).Text(), `continue with offset `+coreNum(4000-len(header)))
		unknown := withBranch(f.With(withModelWindow(4096), withUsageUnknown(4096)))
		coreTurnStart(unknown)
		page := unknown.Run("notes", rec{"op": "read", "path": "long.md"}).Text()
		m := regexp.MustCompile(`continue with offset (\d+)`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no continuation offset in %q", page)
		}
		if n, _ := strconv.Atoi(m[1]); n >= 8192 {
			t.Fatalf("an unknown-usage page must stay inside the window: offset %d", n)
		}
	})

	tw(t, "posthorse", "note and history pages share a batch budget without double-counting consumed pages", func(t *testing.T) {
		dir := coreReal(t)
		root := newFixture(t)
		tokens := 98_000
		root.Set(withUsage(100_000, tokens), withModelWindow(100_000), withReserve(16_384), withEnabled(false))
		entry := rec{"type": "message", "id": "long", "message": rec{"role": "user", "content": strings.Repeat("h", 25_000)}}
		ctx := root.With(coreBranch(dir, entry)...).PageContext()
		ctx.Run("notes", rec{"op": "write", "path": "long.md", "content": strings.Repeat("n", 25_000)})
		ctx.Run("notes", rec{"op": "write", "path": "short.md", "content": strings.Repeat("s", 800)})
		header := "File: " + filepath.Join(dir, ".pi", "notes", "long.md") + "\n"
		coreTurnStart(ctx)
		coreMatch(t, ctx.Run("notes", rec{"op": "read", "path": "long.md"}).Text(), `continue with offset `+coreNum(4000-len(header)))
		coreFails(t, ctx, "history", rec{"op": "read", "id": "long", "offset": float64(4000)}, `retry with offset 4000`)
		coreTurnStart(ctx)
		coreMatch(t, ctx.Run("history", rec{"op": "read", "id": "long"}).Text(), `offset 4000`)
		coreFails(t, ctx, "notes", rec{"op": "read", "path": "long.md", "offset": float64(4000)}, `retry with offset 4000`)

		coreTurnStart(ctx)
		tokens = 97_000
		root.Set(withUsage(100_000, tokens))
		shortHeader := "File: " + filepath.Join(dir, ".pi", "notes", "short.md") + "\n"
		if got := ctx.Run("notes", rec{"op": "read", "path": "short.md"}).Text(); got != shortHeader+strings.Repeat("s", 800) {
			t.Fatalf("short note page: %q", got)
		}
		// Serial execution has already added the complete result to native usage.
		tokens += (len(shortHeader) + 800 + 3) / 4
		root.Set(withUsage(100_000, tokens))
		coreMatch(t, ctx.Run("notes", rec{"op": "read", "path": "long.md"}).Text(), `continue with offset `+coreNum((99_000-tokens)*4-len(header)))
	})

	tw(t, "posthorse", "mixed search/list/read outputs and no-match refusals share one reservation", func(t *testing.T) {
		dir := coreReal(t)
		f := newFixture(t)
		branch := make([]rec, 60)
		for i := range branch {
			branch[i] = rec{"type": "message", "id": fmt.Sprintf("entry-%d", i), "message": rec{"role": "user", "content": "needle " + strings.Repeat("h", 500)}}
		}
		ctx := coreUsage(f, 100_000, 98_000, 16_384, false).With(coreBranch(dir, branch...)...)
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = "needle " + strings.Repeat("n", 300)
		}
		coreWrite(t, filepath.Join(dir, ".pi", "notes", "long.md"), strings.Join(lines, "\n"))
		for _, first := range []string{"history", "notes"} {
			coreTurnStart(ctx)
			// Promise.allSettled over four sibling tool calls: all start before any finishes.
			runs := []*fixRun{
				ctx.StartRun(first, rec{"op": "search", "query": "needle", "limit": float64(50)}),
				ctx.StartRun("notes", rec{"op": "list"}),
				ctx.StartRun("notes", rec{"op": "read", "path": "long.md"}),
				ctx.StartRun("history", rec{"op": "read", "id": "entry-0"}),
			}
			total, rejected := 0, false
			for _, run := range runs {
				result, failure, failed := run.WaitFailed()
				text := result.Text()
				if failed {
					rejected, text = true, failure
				}
				total += (u16len(text) + 3) / 4
			}
			if total > 2000 {
				t.Fatalf("siblings exceeded their shared reservation: %d tokens", total)
			}
			if !rejected {
				t.Fatal("at least one sibling must be refused")
			}
		}
		coreTurnStart(ctx)
		hugeQuery := strings.Repeat("absent", 20_000)
		for _, tool := range []string{"notes", "history"} {
			output := ctx.Run(tool, rec{"op": "search", "query": hugeQuery}).Text()
			if len(output) >= 300 {
				t.Fatalf("a no-match echo cannot reprint an unbounded query: %d characters", len(output))
			}
			coreMatch(t, output, `No (notes|history) match`)
		}
		tight := coreUsage(f, 100_000, 99_750, 16_384, false).With(coreBranch(dir, branch...)...)
		coreTurnStart(tight)
		refusalTokens := 0
		for index := range 20 {
			tool := "history"
			if index%2 != 0 {
				tool = "notes"
			}
			_, failure, failed := tight.RunFailed(tool, rec{"op": "search", "query": hugeQuery, "offset": float64(123)})
			if !failed {
				t.Fatalf("refusal %d: %s search must be rejected", index, tool)
			}
			refusalTokens += (u16len(failure) + 3) / 4
		}
		if refusalTokens > 250 {
			t.Fatalf("even repeated no-match refusals stay inside the remaining budget: %d tokens", refusalTokens)
		}
	})

	tw(t, "posthorse", "notes resolve the repository root from nested directories, worktrees, and plain folders", func(t *testing.T) {
		dir := pnSetup(t)
		repo, main, worktree := filepath.Join(dir, "repo"), filepath.Join(dir, "main"), filepath.Join(dir, "wt")
		plain := filepath.Join(dir, "plain", "sub")
		pnMkdir(t, repo, ".git")
		pnMkdir(t, repo, "packages", "app")
		pnMkdir(t, main, ".git", "worktrees", "wt")
		coreWrite(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
		pnMkdir(t, worktree, "packages", "app")
		coreWrite(t, filepath.Join(worktree, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
		pnMkdir(t, plain)
		f := newFixture(t)
		write := func(cwd string) { f.NotesIn(cwd, rec{"op": "write", "path": "state.md", "content": cwd}) }

		write(filepath.Join(repo, "packages", "app"))
		pnEqual(t, pnRead(t, repo, ".pi", "notes", "state.md"), filepath.Join(repo, "packages", "app"), "repo note")
		pnEqual(t, pnExists(repo, "packages", "app", ".pi"), false, "no nested .pi")
		write(worktree)
		pnEqual(t, pnRead(t, main, ".pi", "notes", "state.md"), worktree, "worktree note in main")
		notePath := filepath.Join(main, ".pi", "notes", "state.md")
		listing := f.NotesIn(worktree, rec{"op": "list"}).Text()
		if !strings.HasPrefix(listing, "Notes directory: "+filepath.Join(main, ".pi", "notes")+"\n") {
			t.Fatalf("listing header: %q", listing)
		}
		coreMatch(t, strings.Split(listing, "\n")[1], `^state\.md  \d+B  \d{4}-\d\d-\d\dT\d\d:\d\dZ$`)
		read := f.NotesIn(worktree, rec{"op": "read", "path": "state.md"}).Text()
		editPath := strings.TrimPrefix(strings.Split(read, "\n")[0], "File: ")
		pnEqual(t, editPath, notePath, "absolute note path")
		// createEditTool(worktree).execute: an exact-text replacement of the file at the returned path.
		current := pnRead(t, editPath)
		coreWrite(t, editPath, strings.Replace(current, worktree, "Updated continuation.", 1))
		pnEqual(t, pnRead(t, notePath), "Updated continuation.", "the returned absolute path edits the shared note, not a worktree-local copy")
		coreWrite(t, filepath.Join(worktree, ".git"), "gitdir: ../main/.git/worktrees/wt\n")
		write(filepath.Join(worktree, "packages", "app"))
		pnEqual(t, pnRead(t, main, ".pi", "notes", "state.md"), filepath.Join(worktree, "packages", "app"), "relative gitdir")
		pnEqual(t, pnExists(worktree, ".pi"), false, "no worktree-local notes")
		write(plain)
		pnEqual(t, pnRead(t, plain, ".pi", "notes", "state.md"), plain, "plain folder")

		// A copied or orphaned worktree must still be able to keep local notes.
		orphan := filepath.Join(dir, "orphan")
		pnMkdir(t, orphan, "nested")
		coreWrite(t, filepath.Join(orphan, ".git"), "gitdir: ../missing/.git/worktrees/orphan\n")
		write(filepath.Join(orphan, "nested"))
		pnEqual(t, pnRead(t, orphan, ".pi", "notes", "state.md"), filepath.Join(orphan, "nested"), "orphan note")
		pnEqual(t, f.NotesIn(orphan, rec{"op": "read", "path": "state.md"}).Text(),
			"File: "+filepath.Join(orphan, ".pi", "notes", "state.md")+"\n"+filepath.Join(orphan, "nested"), "orphan read")
	})

	tw(t, "posthorse", "notes for Pi package checkouts move to the agent directory and survive pi update", func(t *testing.T) {
		// A linked agent directory must retain the same package storage owner.
		dir := coreReal(t)
		agentDir := filepath.Join(dir, "agent")
		pnMkdir(t, dir, "physical-agent")
		pnMust(t, os.Symlink(filepath.Join(dir, "physical-agent"), agentDir))
		owner := filepath.Join(agentDir, "git", "github.com", "acme")
		checkout := filepath.Join(owner, "tool")
		legacy := filepath.Join(checkout, ".pi", "notes")
		pnMkdir(t, checkout, "src")
		pnGit(t, checkout, "init", "-q")
		pnMkdir(t, legacy, "task")
		coreWrite(t, filepath.Join(legacy, "task", "state.md"), "Saved before the move.")
		// The fixture hands the host's agent directory to sdk.AgentDir (PI_CODING_AGENT_DIR in the TS test).
		f := newFixture(t, withAgentDir(agentDir))
		realAgent, err := filepath.EvalSymlinks(agentDir)
		pnMust(t, err)
		moved := filepath.Join(realAgent, "notes", "github.com", "acme", "tool")

		listing := f.NotesIn(filepath.Join(checkout, "src"), rec{"op": "list"}).Text()
		if !strings.HasPrefix(listing, "Notes directory: "+moved+"\ntask/state.md  ") {
			t.Fatalf("listing: %q", listing)
		}
		f.NotesIn(checkout, rec{"op": "append", "path": "task/state.md", "content": "Appended after."})
		pnEqual(t, pnRead(t, legacy, "task", "state.md"), "Saved before the move.\nAppended after.\n",
			"sessions running an earlier Posthorse still reach the notes through the old path")
		// pi update resets a git package to its new commit and deletes everything Git does not track.
		pnGit(t, checkout, "clean", "-fdx", "-q")
		pnEqual(t, pnExists(checkout, ".pi"), false, "cleanup removed the checkout notes")
		pnEqual(t, f.NotesIn(checkout, rec{"op": "read", "path": "task/state.md"}).Text(),
			"File: "+filepath.Join(moved, "task", "state.md")+"\nSaved before the move.\nAppended after.\n", "read after cleanup")

		// A session still running an older Posthorse writes inside the checkout again after the
		// update removed the link. Both versions survive, and the link returns.
		pnMkdir(t, legacy, "task")
		coreWrite(t, filepath.Join(legacy, "task", "state.md"), "Written by an older Posthorse.")
		relisted := f.NotesIn(checkout, rec{"op": "list"}).Text()
		match := regexp.MustCompile(`(?m)^(from-checkout-\d+)/task/state\.md {2}`).FindStringSubmatch(relisted)
		if match == nil {
			t.Fatalf("no preserved copy in %q", relisted)
		}
		pnEqual(t, pnRead(t, moved, match[1], "task", "state.md"), "Written by an older Posthorse.", "older copy")
		pnEqual(t, pnRead(t, legacy, "task", "state.md"), "Saved before the move.\nAppended after.\n", "link returned")

		// Pi cleans only the checkouts, so a folder holding several packages keeps its own notes.
		realOwner, err := filepath.EvalSymlinks(owner)
		pnMust(t, err)
		f.NotesIn(realOwner, rec{"op": "write", "path": "fleet.md", "content": "Owner folder."})
		pnEqual(t, pnRead(t, owner, ".pi", "notes", "fleet.md"), "Owner folder.", "owner folder")
	})

	tw(t, "posthorse", "generic Git URL prefix packages cannot disclose or overwrite each other's notes", func(t *testing.T) {
		project := pnSetup(t)
		// The TS test asks Pi's parseGitUrl for these values and asserts them; they are Pi's documented parse
		// of https://git.example.com/acme/tool.git and https://git.example.com/acme/tool/notes.git.
		host, firstPath, secondPath := "git.example.com", "acme/tool", "acme/tool/notes"
		outer := filepath.Join(project, ".pi", "git", host, filepath.FromSlash(firstPath))
		inner := filepath.Join(project, ".pi", "git", host, filepath.FromSlash(secondPath))
		pnMkdir(t, inner)
		pnGit(t, outer, "init", "-q")
		pnGit(t, inner, "init", "-q")
		f := newFixture(t)
		f.NotesIn(outer, rec{"op": "write", "path": "outer.md", "content": "outer checkpoint"})
		f.NotesIn(inner, rec{"op": "write", "path": "inner.md", "content": "private inner checkpoint"})
		coreNoMatch(t, f.NotesIn(outer, rec{"op": "list"}).Text(), `inner\.md`)
		coreNoMatch(t, f.NotesIn(outer, rec{"op": "search", "query": "checkpoint"}).Text(), `private inner`)
		coreNoMatch(t, f.NotesIn(inner, rec{"op": "list"}).Text(), `outer\.md`)
		f.NotesIn(outer, rec{"op": "write", "path": "notes/inner.md", "content": "outer-owned file"})
		coreMatch(t, f.NotesIn(inner, rec{"op": "read", "path": "inner.md"}).Text(), `private inner checkpoint$`)
		coreMatch(t, f.NotesIn(outer, rec{"op": "read", "path": "notes/inner.md"}).Text(), `outer-owned file$`)
	})

	tw(t, "posthorse", "nested project-package notes do not leak through outer package list or search", func(t *testing.T) {
		project := pnSetup(t)
		outer := filepath.Join(project, ".pi", "git", "github.com", "acme", "outer")
		inner := filepath.Join(outer, ".pi", "git", "github.com", "acme", "inner")
		pnMkdir(t, inner)
		pnGit(t, outer, "init", "-q")
		pnGit(t, inner, "init", "-q")
		f := newFixture(t)
		f.NotesIn(outer, rec{"op": "write", "path": "outer.md", "content": "outer checkpoint"})
		f.NotesIn(inner, rec{"op": "write", "path": "inner.md", "content": "inner checkpoint"})
		outerList := f.NotesIn(outer, rec{"op": "list"}).Text()
		coreMatch(t, outerList, `outer\.md`)
		coreNoMatch(t, outerList, `inner\.md`)
		coreNoMatch(t, f.NotesIn(outer, rec{"op": "search", "query": "checkpoint"}).Text(), `inner checkpoint`)
		coreMatch(t, f.NotesIn(inner, rec{"op": "search", "query": "checkpoint"}).Text(), `inner checkpoint`)
		coreNoMatch(t, f.NotesIn(inner, rec{"op": "list"}).Text(), `outer\.md`)
		// An outer note named like the nested checkout's path remains only an outer note.
		f.NotesIn(outer, rec{"op": "write", "path": ".pi/git/github.com/acme/inner/notes/inner.md", "content": "not the inner note"})
		coreMatch(t, f.NotesIn(inner, rec{"op": "read", "path": "inner.md"}).Text(), `inner checkpoint$`)
	})

	tw(t, "posthorse", "separate Git directories share notes in the common Git directory (${marker})", func(t *testing.T) {
		for _, marker := range []string{"file", "directory symlink"} {
			t.Run(marker, func(t *testing.T) {
				dir := pnSetup(t)
				main, gitdir, worktree := filepath.Join(dir, "main"), filepath.Join(dir, "git-storage"), filepath.Join(dir, "worktree")
				pnGit(t, "", "init", "--separate-git-dir", gitdir, main)
				if marker == "directory symlink" {
					pnMust(t, os.Remove(filepath.Join(main, ".git")))
					pnMust(t, os.Symlink(gitdir, filepath.Join(main, ".git")))
				}
				pnGit(t, main, "-c", "user.name=Posthorse test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgSign=false",
					"commit", "--allow-empty", "-m", "fixture")
				pnGit(t, main, "worktree", "add", "--detach", worktree)
				f := newFixture(t)
				header := "File: " + filepath.Join(gitdir, ".pi", "notes", "shared.md") + "\n"
				f.NotesIn(main, rec{"op": "write", "path": "shared.md", "content": "original state"})
				pnEqual(t, f.NotesIn(worktree, rec{"op": "read", "path": "shared.md"}).Text(), header+"original state", "worktree read")
				appended := f.NotesIn(worktree, rec{"op": "append", "path": "shared.md", "content": "worktree update"}).Text()
				if !strings.Contains(appended, filepath.Join(gitdir, ".pi", "notes", "shared.md")) {
					t.Fatalf("append result does not name the shared note: %q", appended)
				}
				pnEqual(t, f.NotesIn(main, rec{"op": "read", "path": "shared.md"}).Text(), header+"original state\nworktree update\n", "main read")
				pnEqual(t, pnExists(main, ".pi"), false, "notes live in the common Git directory")
				pnEqual(t, pnExists(worktree, ".pi"), false, "writes do not create a worktree-local copy")
			})
		}
	})
}
