package powerline_footer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// regressionCtx is createSegmentContext of remaining-regressions.test.ts.
func regressionCtx(mods ...func(*segmentContext)) segmentContext {
	return newCtx(segmentOptions{}, append([]func(*segmentContext){func(c *segmentContext) {
		c.Model = &modelInfo{ID: "claude-sonnet-4", Name: "Claude Sonnet 4"}
		c.CWD = "/tmp/project"
	}}, mods...)...)
}

type recordTheme struct{}

func (recordTheme) Fg(token, text string) (string, error) {
	return "<" + token + ">" + text + "</" + token + ">", nil
}

func TestRemainingRegressions(t *testing.T) {
	withNerdFonts(t, "0")
	tw(t, "remaining-regressions", "session segment shows the current display name when available", func(t *testing.T) {
		r := renderSegment("session", regressionCtx(func(c *segmentContext) { c.SessionID, c.SessionName = "12345678-abcdef", "My working session" }))
		eq(t, strings.HasSuffix(stripAnsi(r.Content), "My working session"), true, "name")
		eq(t, contains(r.Content, "12345678"), false, "no id")
		eq(t, r.Visible, true, "visible")
	})
	tw(t, "remaining-regressions", "session segment falls back to the short ID or new when unnamed", func(t *testing.T) {
		unnamed := renderSegment("session", regressionCtx(func(c *segmentContext) { c.SessionID = "12345678-abcdef" }))
		emptyName := renderSegment("session", regressionCtx(func(c *segmentContext) { c.SessionID, c.SessionName = "12345678-abcdef", "" }))
		fresh := renderSegment("session", regressionCtx())
		eq(t, strings.HasSuffix(stripAnsi(unnamed.Content), "12345678"), true, "short id")
		eq(t, emptyName.Content, unnamed.Content, "empty name")
		eq(t, strings.HasSuffix(stripAnsi(fresh.Content), "new"), true, "new")
		eq(t, unnamed.Visible && fresh.Visible, true, "visible")
	})
	tw(t, "remaining-regressions", "model segment can show provider-qualified ids", func(t *testing.T) {
		q := func(id, name, provider string) string {
			return stripAnsi(renderSegment("model", regressionCtx(func(c *segmentContext) {
				c.Model = &modelInfo{ID: id, Name: name, Provider: provider}
				c.Options = segmentOptions{Model: &modelOptions{Display: s("qualified")}}
			})).Content)
		}
		eq(t, stripAnsi(renderSegment("model", regressionCtx()).Content), "Sonnet 4", "normal")
		eq(t, q("claude-sonnet-4", "Claude Sonnet 4", "anthropic"), "anthropic/claude-sonnet-4", "qualified")
		eq(t, q("openai/gpt-4.1", "GPT 4.1", "openai"), "openai/gpt-4.1", "already qualified")
		eq(t, q("deepseek/deepseek-v4-flash", "DeepSeek V4 Flash", "commandcode"), "commandcode/deepseek/deepseek-v4-flash", "nested id")
	})
	tw(t, "remaining-regressions", "self-colored custom items preserve ANSI resets and skip configured color", func(t *testing.T) {
		status := "\x1b[32m50%\x1b[0m"
		r := renderSegment("custom:usage", regressionCtx(func(c *segmentContext) {
			c.ExtensionStatuses = []statusEntry{{"usage", status}}
			c.CustomItems = map[string]customItem{"usage": {ID: "usage", StatusKey: "usage", Position: "right", Color: "warning", SelfColorize: true, HideWhenMissing: true, ExcludeFromStatuses: true}}
			c.Theme = recordTheme{}
		}))
		eq(t, r.Content, status, "content")
		eq(t, r.Visible, true, "visible")
	})
	tw(t, "remaining-regressions", "extension statuses use one padded dot separator", func(t *testing.T) {
		r := renderSegment("extension_statuses", regressionCtx(func(c *segmentContext) {
			c.ExtensionStatuses = []statusEntry{{"first", "ready"}, {"second", "waiting"}}
		}))
		eq(t, stripAnsi(r.Content), "ready · waiting", "content")
		eq(t, r.Visible, true, "visible")
	})
	tw(t, "remaining-regressions", "extension statuses isolate colors from separators and subsequent statuses", func(t *testing.T) {
		for _, ending := range []string{"\x1b[0m", "\x1b[39m", ""} {
			r := renderSegment("extension_statuses", regressionCtx(func(c *segmentContext) {
				c.ExtensionStatuses = []statusEntry{{"mcp", "\x1b[36mMCP" + ending}, {"tempo", "Time"}, {"sync", "\x1b[32mSync\x1b[0m"}}
			}))
			eq(t, r.Content, "\x1b[0m\x1b[36mMCP\x1b[0m · \x1b[0mTime\x1b[0m · \x1b[0m\x1b[32mSync\x1b[0m", "content")
			eq(t, stripAnsi(r.Content), "MCP · Time · Sync", "plain")
		}
	})
	tw(t, "remaining-regressions", "cost segment supports subscription display modes and converted currencies", func(t *testing.T) {
		setCurrencyRatesForTest(map[string]float64{"CNY": 7.2})
		t.Cleanup(resetCurrencyRatesForTest)
		sub := func(mode string, cost float64) renderedSegment {
			return renderSegment("cost", regressionCtx(func(c *segmentContext) {
				c.UsingSubscription, c.Usage = true, usageStats{Cost: cost}
				if mode != "" {
					c.Options = segmentOptions{Cost: &costOptions{SubscriptionDisplay: s(mode)}}
				}
			}))
		}
		eq(t, sub("", 0.42), renderedSegment{"(sub)", true}, "subscription")
		eq(t, sub("reported-cost", 0.42), renderedSegment{"$0.42", true}, "reported")
		eq(t, sub("both", 0.42), renderedSegment{"$0.42 (sub)", true}, "both")
		eq(t, sub("reported-cost", 0), renderedSegment{"(sub)", true}, "zero reported")
		eq(t, sub("both", 0), renderedSegment{"(sub)", true}, "zero both")
		eq(t, renderSegment("cost", regressionCtx(func(c *segmentContext) { c.Usage = usageStats{Cost: 0.42, SubagentCost: 0.58} })), renderedSegment{"$1.00", true}, "subagent")
		eq(t, renderSegment("cost", regressionCtx(func(c *segmentContext) {
			c.Usage = usageStats{Cost: 1, SubagentCost: 0.25}
			c.Options = segmentOptions{Cost: &costOptions{Currency: s("CNY")}}
		})), renderedSegment{"¥9.00", true}, "converted")
	})
	tw(t, "remaining-regressions", "context segment shows used tokens, maximum, and percentage", func(t *testing.T) {
		r := renderSegment("context_pct", regressionCtx(func(c *segmentContext) { c.ContextTokens, c.ContextPercent, c.ContextWindow = f(4500), f(1.7), 272000 }))
		eq(t, stripAnsi(r.Content), "◫ 4.5k/272k (1.7%) AC", "content")
		eq(t, r.Visible, true, "visible")
	})
	tw(t, "remaining-regressions", "Nerd Font context icon uses stable database glyph", func(t *testing.T) {
		eq(t, nerdIcons.Context, "\uF1C0", "glyph")
	})
	tskip(t, "remaining-regressions", "startup welcome predicate respects powerline.welcome false", "the welcome header and overlay are not ported (named exclusion in PORT.md), so shouldShowStartupWelcome has no caller; the powerline.welcome setting itself is parsed and twinned in the config tests")
	tskip(t, "remaining-regressions", "stale ctx guard handles old and new Pi messages on agent_end", "isStaleExtensionContextError matches the message text of Pi's stale-context error, which the Go SDK does not raise (G14 in PORT.md); the Go host reports a replaced session through its own error, and the source-text assertions have no Go counterpart")
	tskip(t, "remaining-regressions", "queue delivery tracks acknowledgement and requeues unstarted messages on shutdown", "asserts the TypeScript source of the prompt queue, which is not ported (named exclusion in PORT.md)")
	tskip(t, "remaining-regressions", "editor-adjacent widgets cache queue and last-prompt work", "asserts the TypeScript source of the queue-preview and last-prompt widgets, which are not ported (named exclusion in PORT.md)")
	tskip(t, "remaining-regressions", "unknown context estimates are event-scoped and cleared before compaction", "asserts the TypeScript source of the reload-time estimate (estimateUnknownContextUsage), which needs Pi's estimateTokens and is not ported (named exclusion in PORT.md)")
}

func TestContextUsage(t *testing.T) {
	core := func(tokens *float64, window float64, pct *float64) *contextUsage {
		return &contextUsage{Tokens: tokens, Window: window, Percent: pct}
	}
	tw(t, "context-usage", "readCoreContextUsage returns Pi context estimates for branch summaries", func(t *testing.T) {
		eq(t, readCoreContextUsage(map[string]any{"tokens": 1250.0, "contextWindow": 5000.0, "percent": 25.0}), core(f(1250), 5000, f(25)), "usage")
	})
	tw(t, "context-usage", "readCoreContextUsage computes percent when Pi returns only token totals", func(t *testing.T) {
		eq(t, readCoreContextUsage(map[string]any{"tokens": 1000.0, "contextWindow": 4000.0}), core(f(1000), 4000, f(25)), "usage")
	})
	tw(t, "context-usage", "readCoreContextUsage preserves Pi's post-compaction unknown state", func(t *testing.T) {
		eq(t, readCoreContextUsage(map[string]any{"tokens": nil, "contextWindow": 5000.0, "percent": nil}), core(nil, 5000, nil), "usage")
	})
	tw(t, "context-usage", "readCoreContextUsage ignores unknown or unusable estimates", func(t *testing.T) {
		eq(t, readCoreContextUsage(nil) == nil, true, "no estimate")
		eq(t, readCoreContextUsage(map[string]any{"contextWindow": 5000.0, "percent": nil}) == nil, true, "tokens undefined")
		eq(t, readCoreContextUsage(map[string]any{"tokens": 100.0, "contextWindow": 0.0, "percent": 0.0}) == nil, true, "zero window")
	})
	tw(t, "context-usage", "core context usage cache reuses a leaf and supports explicit invalidation", func(t *testing.T) {
		cache := &coreContextUsageCache{}
		src := &usageSrc{leaf: "leaf-1", tokens: 100}
		eq(t, fv(tokensOf(cache.get(src))), 100.0, "first")
		src.tokens = 200
		eq(t, fv(tokensOf(cache.get(src))), 100.0, "cached")
		eq(t, src.reads, 1, "reads")
		cache.reset()
		eq(t, fv(tokensOf(cache.get(src))), 200.0, "after reset")
		src.leaf, src.tokens = "leaf-2", 300
		eq(t, fv(tokensOf(cache.get(src))), 300.0, "new leaf")
		eq(t, src.reads, 3, "reads")
	})
	tw(t, "context-usage", "resolveDisplayContextUsage preserves unknown core usage over assistant fallback usage", func(t *testing.T) {
		eq(t, resolveDisplayContextUsage(core(nil, 5000, nil), nil, 4000, 5000), *core(nil, 5000, nil), "unknown")
	})
	tw(t, "context-usage", "resolveDisplayContextUsage uses the approximate estimate for unknown core usage", func(t *testing.T) {
		estimate := core(f(1000), 5000, f(20))
		eq(t, resolveDisplayContextUsage(core(nil, 5000, nil), estimate, 4000, 5000), *estimate, "estimate")
	})
	tw(t, "context-usage", "resolveDisplayContextUsage computes assistant fallback usage when Pi has no current estimate", func(t *testing.T) {
		eq(t, resolveDisplayContextUsage(nil, nil, 1000, 4000), *core(f(1000), 4000, f(25)), "fallback")
	})
	tskip(t, "context-usage", "estimateUnknownContextUsage estimates the active compacted context", "sums Pi's estimateTokens over sessionEntryToContextMessages, internals of @earendil-works/pi-coding-agent that the Go SDK does not expose; the reload-time estimate is a named exclusion in PORT.md")
	tskip(t, "context-usage", "estimateUnknownContextUsage skips sessions with known core usage", "belongs to estimateUnknownContextUsage, a named exclusion in PORT.md (needs Pi's token estimator)")
	tw(t, "context-usage", "estimateInitialContextTokens uses Pi's conservative character estimate", func(t *testing.T) {
		eq(t, estimateInitialContextTokens(nil) == nil, true, "no prompt")
		eq(t, estimateInitialContextTokens(s("")) == nil, true, "empty")
		eq(t, estimateInitialContextTokens(s("   ")) == nil, true, "blank")
		eq(t, estimateInitialContextTokens(s("1234")), ptr(1), "four chars")
		eq(t, estimateInitialContextTokens(s("12345")), ptr(2), "five chars")
	})
}

type usageSrc struct {
	leaf   string
	tokens float64
	reads  int
}

func (u *usageSrc) GetLeafID() (*string, error) { return &u.leaf, nil }
func (u *usageSrc) ReadUsage() map[string]any {
	u.reads++
	return map[string]any{"tokens": u.tokens, "contextWindow": 1000.0, "percent": u.tokens / 10}
}

func TestGit(t *testing.T) {
	host := func(remote string) any {
		if h := detectGitHost(&remote); h != nil {
			return *h
		}
		return nil
	}
	tw(t, "git-status", "detectGitHost recognizes known hosts over SSH and HTTPS", func(t *testing.T) {
		for remote, want := range map[string]string{
			"git@github.com:owner/repo.git": "github", "https://github.com/owner/repo.git": "github",
			"ssh://git@gitlab.com/owner/repo.git": "gitlab", "https://gitlab.com/owner/repo": "gitlab",
			"git@bitbucket.org:owner/repo.git": "bitbucket", "https://user@bitbucket.org/owner/repo.git": "bitbucket",
		} {
			eq(t, host(remote), want, remote)
		}
	})
	tw(t, "git-status", "detectGitHost normalizes www and sub-domains", func(t *testing.T) {
		eq(t, host("https://www.github.com/owner/repo"), "github", "www")
		eq(t, host("git@ssh.github.com:owner/repo.git"), "github", "ssh sub-domain")
	})
	tw(t, "git-status", "detectGitHost treats unknown or self-hosted remotes as a generic host", func(t *testing.T) {
		for _, remote := range []string{"git@git.example.com:owner/repo.git", "https://gitea.mycorp.dev/owner/repo.git", "/srv/git/local.git"} {
			eq(t, host(remote), "other", remote)
		}
	})
	tw(t, "git-status", "detectGitHost returns null when there is no remote", func(t *testing.T) {
		eq(t, detectGitHost(nil) == nil, true, "nil")
		eq(t, host(""), nil, "empty")
		eq(t, host("   "), nil, "blank")
	})
	tw(t, "git-optional-locks", "read-only git commands opt out of git's optional index lock", func(t *testing.T) {
		env := readOnlyGitEnv(map[string]string{"PATH": "/usr/bin"})
		eq(t, env["GIT_OPTIONAL_LOCKS"], "0", "locks")
		eq(t, env["PATH"], "/usr/bin", "must extend the ambient environment, not replace it")
	})
	tw(t, "git-optional-locks", "readOnlyGitEnv overrides an inherited GIT_OPTIONAL_LOCKS=1", func(t *testing.T) {
		eq(t, readOnlyGitEnv(map[string]string{"GIT_OPTIONAL_LOCKS": "1"})["GIT_OPTIONAL_LOCKS"], "0", "override")
	})
	tw(t, "git-optional-locks", "the git process the footer spawns receives GIT_OPTIONAL_LOCKS=0", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		os.MkdirAll(bin, 0o755)
		log := filepath.Join(dir, "calls.log")
		script := "#!/bin/sh\necho \"GIT_OPTIONAL_LOCKS=${GIT_OPTIONAL_LOCKS-unset} ARGS=$*\" >> '" + log + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		setenv(t, "PATH", s(bin+":"+os.Getenv("PATH")))
		// Some environments export GIT_OPTIONAL_LOCKS=0 machine-wide; force the opposite so a false pass cannot be inherited.
		setenv(t, "GIT_OPTIONAL_LOCKS", s("1"))
		getGitStatus(s("main"), "full", dir)
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("expected the footer to spawn git: %v", err)
		}
		calls := strings.Split(strings.TrimSpace(string(data)), "\n")
		status := 0
		for _, call := range calls {
			if strings.Contains(call, "ARGS=status --porcelain") {
				status++
			}
			eq(t, strings.Contains(call, "GIT_OPTIONAL_LOCKS=0"), true, "git spawned without the lock opt-out: "+call)
		}
		eq(t, status > 0, true, "expected a status call")
	})
	tskip(t, "git-status", "status refresh preserves dirty coloring data until new counts arrive", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); the Go port reads git synchronously while it builds the footer context, so there is no interim stale state to preserve")
	tskip(t, "git-status", "unchanged git refreshes settle without requesting another render", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); the Go port has no background refresh and so no repaint signal to suppress")
	tskip(t, "git-status", "fallback branch refresh serves stale only within the same cwd", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); the Go port has no stale branch to serve")
	tskip(t, "git-status", "detached branch fallback repaints when lookup resolves to no branch", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); the Go port has no repaint signal; a failed lookup simply yields no branch")
	tw(t, "git-status", "branch lookup works when git lacks branch --show-current", func(t *testing.T) {
		// The original resolves the branch in the background: its first read is null and the read after waitForGitUpdates is
		// "legacy". The Go port reads git synchronously (PORT.md finding 7), so its first read is already the resolved branch.
		cwd := t.TempDir()
		bin := filepath.Join(cwd, "bin")
		os.MkdirAll(bin, 0o755)
		log := filepath.Join(cwd, "git-calls.log")
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nif [ \"$1 $2\" = \"branch --show-current\" ]; then exit 129; fi\nif [ \"$1 $2 $3\" = \"symbolic-ref --short HEAD\" ]; then echo legacy; exit 0; fi\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		setenv(t, "PATH", s(bin+":"+os.Getenv("PATH")))
		invalidateGitBranch()
		t.Cleanup(invalidateGitBranch)
		eq(t, sv(getCurrentBranch(nil, cwd)), "legacy", "branch")
		data, _ := os.ReadFile(log)
		eq(t, regexp.MustCompile(`(?m)^symbolic-ref --short HEAD$`).Match(data), true, "symbolic-ref called: "+string(data))
		eq(t, regexp.MustCompile(`(?m)^branch --show-current$`).Match(data), false, "branch --show-current not called: "+string(data))
	})
	tskip(t, "git-status", "cwd changes clear displayed status, branch and host, including in-flight reads", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); in-flight reads do not exist in the Go port, which re-reads git for each context")
	tskip(t, "git-status", "attached, detached and worktree branch displays follow repository transitions", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); the Go port has no cache whose invalidation these transitions exercise")
	tskip(t, "git-status", "remote refreshes notify only when the displayed host changes", "the original keeps a stale-while-revalidate cache that refreshes git in the background and signals a repaint (subscribeGitUpdates/waitForGitUpdates); remote host changes notify through that repaint signal, which the Go port does not have")
	tskip(t, "git-optional-locks", "footer git polling hides Windows child consoles while preserving read-only env", "mocks Node's child_process.spawn and asserts the Windows windowsHide option; Go sets no console flag (the port runs on POSIX hosts here, and the read-only env is covered by the shim twin)")
	tw(t, "git-optional-locks", "footer git polling handles a synchronous spawn failure", func(t *testing.T) {
		// The original mocks child_process.spawn to throw; here git cannot be started at all (no git on PATH).
		setenv(t, "PATH", s(t.TempDir()))
		invalidateGitStatus()
		t.Cleanup(invalidateGitStatus)
		cwd := t.TempDir()
		fallback := gitStatus{Branch: s("main")}
		eq(t, getGitStatus(s("main"), "full", cwd), fallback, "first read")
		eq(t, getGitStatus(s("main"), "full", cwd), fallback, "second read")
	})
	tskip(t, "git-optional-locks", "bash git completions hide Windows child consoles", "asserts the TypeScript source of bash-mode/completion.ts, which is not ported (named exclusion in PORT.md)")
}

func tokensOf(u *contextUsage) *float64 {
	if u == nil {
		return nil
	}
	return u.Tokens
}
