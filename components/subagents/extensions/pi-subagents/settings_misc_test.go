package pi_subagents

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/timeout-defaults, model-resolution, model-resolution-diagnostic, thinking-ceiling, config-dir-runtime
// and package-manifest.

func eqTimeout(t *testing.T, got launchTimeout, want launchTimeout, what string) {
	t.Helper()
	eq(t, got, want, what)
}

func TestTimeoutDefaults(t *testing.T) {
	const f = "timeout-defaults"
	to := func(ms int) launchTimeout { return launchTimeout{timeoutMs: ms, set: true} }
	p := func(kv ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	oneChain := l{m{"agent": "worker", "task": "y"}}
	const ninety = 90 * 60 * 1000
	tw(t, f, "async runs default to DEFAULT_ASYNC_TIMEOUT_MS when no explicit/agent timeout", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p(), true, 0), to(defaultAsyncTimeoutMs), "async")
	})
	tw(t, f, "foreground runs default to DEFAULT_FOREGROUND_TIMEOUT_MS when no explicit/agent timeout", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p(), false, 0), to(defaultForegroundTimeoutMs), "foreground")
	})
	tw(t, f, "explicit timeoutMs wins over the async default", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("timeoutMs", 5000.0), true, 0), to(5000), "explicit")
	})
	tw(t, f, "maxRuntimeMs alias wins over the async default", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("maxRuntimeMs", 7000.0), true, 0), to(7000), "alias")
	})
	tw(t, f, "rejects non-positive timeouts", func(t *testing.T) {
		r := resolveSingleAgentLaunchTimeout(p("timeoutMs", 0.0), true, 0)
		eq(t, strings.Contains(r.err, "positive integer"), true, "error")
	})
	tw(t, f, "rejects explicit timeouts above the maximum schedulable timer delay", func(t *testing.T) {
		for _, key := range []string{"timeoutMs", "maxRuntimeMs"} {
			eq(t, resolveSingleAgentLaunchTimeout(p(key, 2147483648.0), true, 0).err, key+" must be a positive integer no larger than 2147483647.", key)
		}
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("timeoutMs", 2147483647.0), true, 0), to(2147483647), "max")
	})
	tw(t, f, "rejects mismatched alias values", func(t *testing.T) {
		r := resolveSingleAgentLaunchTimeout(p("timeoutMs", 1000.0, "maxRuntimeMs", 2000.0), true, 0)
		eq(t, strings.Contains(r.err, "aliases"), true, "error")
	})
	tw(t, f, "does not apply the async default to async chains (children are bounded individually)", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "chain", oneChain), true, 0), launchTimeout{}, "chain")
	})
	tw(t, f, "does not apply the async default to async parallel tasks", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "tasks", oneChain), true, 0), launchTimeout{}, "tasks")
	})
	tw(t, f, "does not apply the async default to async workflowScript", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("workflowScript", "return runs.run('a', { agent: 'worker', task: 'x' })"), true, 0), launchTimeout{}, "workflow")
	})
	tw(t, f, "explicit top-level timeout still applies to async chains", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "chain", oneChain, "timeoutMs", 5000.0), true, 0), to(5000), "chain")
	})
	tw(t, f, "applies the global config default in place of the foreground backstop", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p(), false, ninety), to(ninety), "foreground")
	})
	tw(t, f, "applies the global config default to composite foreground runs (parallel/chain)", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "tasks", oneChain), false, ninety), to(ninety), "tasks")
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "chain", oneChain), false, ninety), to(ninety), "chain")
	})
	tw(t, f, "applies the global config default in place of the single-agent async backstop", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p(), true, ninety), to(ninety), "async")
	})
	tw(t, f, "lets an explicit call timeout win over the global config default", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("timeoutMs", 5000.0), false, ninety), to(5000), "explicit")
	})
	tw(t, f, "keeps composite async runs unbounded even with a global config default", func(t *testing.T) {
		eqTimeout(t, resolveSingleAgentLaunchTimeout(p("agent", "worker", "task", "x", "tasks", oneChain), true, ninety), launchTimeout{}, "composite")
	})
	tw(t, f, "returns a positive integer unchanged", func(t *testing.T) { eq(t, resolveConfigDefaultTimeoutMs(float64(ninety)), ninety, "value") })
	tw(t, f, "treats unset config as no default", func(t *testing.T) { eq(t, resolveConfigDefaultTimeoutMs(nil), 0, "unset") })
	tw(t, f, "ignores non-positive, non-integer, and non-numeric values", func(t *testing.T) {
		for _, v := range []any{0.0, -1.0, 1.5, "600000", math.NaN()} {
			eq(t, resolveConfigDefaultTimeoutMs(v), 0, "ignored")
		}
	})
	tw(t, f, "accepts the maximum schedulable timer delay but ignores anything larger", func(t *testing.T) {
		eq(t, resolveConfigDefaultTimeoutMs(2147483647.0), 2147483647, "max")
		eq(t, resolveConfigDefaultTimeoutMs(2147483648.0), 0, "over")
		eq(t, resolveConfigDefaultTimeoutMs(9007199254740991.0), 0, "safe integer")
	})
}

var resolutionModels = []ModelInfo{
	{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini"},
	{Provider: "anthropic", ID: "claude-sonnet-4", FullID: "anthropic/claude-sonnet-4"},
	{Provider: "openai", ID: "shared", FullID: "openai/shared"},
	{Provider: "anthropic", ID: "shared", FullID: "anthropic/shared"},
	{Provider: "huggingface", ID: "org/model-v1", FullID: "huggingface/org/model-v1"},
}

func mustRes(t *testing.T, s string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestModelResolution(t *testing.T) {
	const f = "model-resolution"
	models := resolutionModels
	explicit := &ResolveSubagentModelOverrideOptions{Source: "explicit"}
	tw(t, f, "resolves exactly one configured model and preserves thinking suffixes", func(t *testing.T) {
		r, err := resolveModelSelection("gpt-5-mini", models, "", nil)
		eq(t, []any{r, err}, []any{ModelSelectionEvidence{"openai/gpt-5-mini", "gpt-5-mini"}, nil}, "selection")
		eq(t, resolveModelCandidate("gpt-5-mini:high", models, ""), "openai/gpt-5-mini:high", "suffix")
	})
	tw(t, f, "inherits the current parent model for omitted, false, empty, and inherit values", func(t *testing.T) {
		parent := normalizeParentModel(m{"provider": "anthropic", "id": "claude-sonnet-4"})
		for _, requested := range []any{nil, false, "", "  ", "inherit", " inherit "} {
			r, err := resolveSubagentModelOverride(requested, parent, models, "", nil)
			eq(t, mustRes(t, r, err), "anthropic/claude-sonnet-4", "requested")
		}
		r, err := resolveEffectiveSubagentModel(nil, nil, parent, models, "", nil)
		eq(t, mustRes(t, r, err), "anthropic/claude-sonnet-4", "effective")
		r, err = resolveSubagentModelOverride("inherit", nil, models, "", nil)
		eq(t, mustRes(t, r, err), "", "no parent")
	})
	tw(t, f, "resolves explicit models against the registry instead of the parent", func(t *testing.T) {
		parent := &ParentModel{"anthropic", "claude-sonnet-4"}
		r, err := resolveSubagentModelOverride("gpt-5-mini", parent, models, "", explicit)
		eq(t, mustRes(t, r, err), "openai/gpt-5-mini", "bare")
		r, err = resolveSubagentModelOverride("openai/gpt-5-mini", parent, models, "", explicit)
		eq(t, mustRes(t, r, err), "openai/gpt-5-mini", "qualified")
	})
	tw(t, f, "uses provider preference for ambiguous bare and owner/name ids", func(t *testing.T) {
		eq(t, resolveModelCandidate("shared", models, ""), "shared", "ambiguous")
		eq(t, resolveModelCandidate("shared", models, "anthropic"), "anthropic/shared", "preferred")
		eq(t, resolveModelCandidate("org/model-v1", models, ""), "huggingface/org/model-v1", "owner/name")
	})
	tw(t, f, "rejects unknown explicit/configured models and suggests a unique alternate provider", func(t *testing.T) {
		_, err := resolveSubagentModelOverride("openai/claude-sonnet-4", nil, models, "", explicit)
		eq(t, err != nil && regexp.MustCompile(`Unknown subagent model 'openai/claude-sonnet-4'.*Did you mean 'anthropic/claude-sonnet-4'`).MatchString(err.Error()), true, "suggestion")
		_, err = resolveModelSelection("missing", models, "", nil)
		eq(t, err != nil && strings.Contains(err.Error(), "Unknown subagent model 'missing'"), true, "unknown")
		r, err := resolveEffectiveSubagentModel("missing", "gpt-5-mini", nil, models, "", &ResolveSubagentModelOverrideOptions{Source: "inherited"})
		eq(t, mustRes(t, r, err), "missing", "inherited keeps")
	})
	tw(t, f, "normalizes registry spelling without switching a qualified provider", func(t *testing.T) {
		eq(t, normalizeModelSegment("GPT_5--MINI"), "gpt-5-mini", "segment")
		eq(t, fuzzyResolveModel("GPT_5_MINI", models, ""), "openai/gpt-5-mini", "fuzzy")
		eq(t, resolveModelCandidate("openai/claude-sonnet-4", models, ""), "openai/claude-sonnet-4", "qualified")
	})
	tw(t, f, "resolves ids that already carry their provider prefix within that provider", func(t *testing.T) {
		reg := []ModelInfo{
			{Provider: "openrouter", ID: "openrouter/auto-beta", FullID: "openrouter/openrouter/auto-beta"},
			{Provider: "openrouter", ID: "auto", FullID: "openrouter/auto"},
			{Provider: "other", ID: "openrouter/free", FullID: "other/openrouter/free"},
		}
		sel := func(model string, r []ModelInfo) (string, error) {
			e, err := resolveModelSelection(model, r, "", nil)
			return e.Model, err
		}
		for model, want := range map[string]string{"openrouter/auto-beta": "openrouter/openrouter/auto-beta", "OpenRouter/Auto_Beta": "openrouter/openrouter/auto-beta", "openrouter/auto": "openrouter/auto"} {
			got, err := sel(model, reg)
			eq(t, mustRes(t, got, err), want, model)
		}
		_, err := sel("openrouter/free", reg)
		eq(t, err != nil && strings.Contains(err.Error(), "Unknown subagent model 'openrouter/free'"), true, "free")
		// An ordinary id that already resolved keeps winning over a provider-prefixed one.
		collision := append(append([]ModelInfo{}, reg...), ModelInfo{Provider: "openrouter", ID: "auto_beta", FullID: "openrouter/auto_beta"})
		got, err := sel("OpenRouter/Auto-Beta", collision)
		eq(t, mustRes(t, got, err), "openrouter/auto_beta", "collision")
	})
	tw(t, f, "fuzzy matches case, separators, dates, and owner/name ids", func(t *testing.T) {
		reg := []ModelInfo{
			{Provider: "openai", ID: "GPT_5.Mini-2025-10-01", FullID: "openai/GPT_5.Mini-2025-10-01"},
			{Provider: "huggingface", ID: "Org/Model_One", FullID: "huggingface/Org/Model_One"},
		}
		eq(t, fuzzyResolveModel("gpt-5-mini", reg, ""), "openai/GPT_5.Mini-2025-10-01", "case")
		eq(t, fuzzyResolveModel("openai/gpt-5-mini-20251001", reg, ""), "openai/GPT_5.Mini-2025-10-01", "date")
		eq(t, fuzzyResolveModel("org/model-one", reg, ""), "huggingface/Org/Model_One", "owner/name")
		eq(t, fuzzyResolveModel("missing", reg, ""), "", "missing")
	})
	tw(t, f, "enforces explicit and strict scopes while warning for inherited violations", func(t *testing.T) {
		scope := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Allow: []string{"anthropic/*"}, Enforce: new(true)}}, "worker", nil)
		_, err := resolveModelSelection("openai/gpt-5-mini", models, "", &ResolveModelSelectionOptions{Scope: scope, Origin: "explicit"})
		eq(t, err != nil && strings.Contains(err.Error(), "outside the configured subagent model scope"), true, "explicit")
		var warnings []string
		r, err := resolveSubagentModelOverride("openai/gpt-5-mini", nil, models, "", &ResolveSubagentModelOverrideOptions{Scope: scope, Source: "inherited",
			OnWarn: func(v ModelScopeViolation) { warnings = append(warnings, v.Message) }})
		eq(t, mustRes(t, r, err), "openai/gpt-5-mini", "inherited")
		eq(t, len(warnings), 1, "warnings")
		strict := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Allow: []string{"anthropic/*"}, Enforce: new(true), Strict: new(true)}}, "worker", nil)
		_, err = resolveModelSelection("openai/gpt-5-mini", models, "", &ResolveModelSelectionOptions{Scope: strict, Origin: "inherited"})
		eq(t, err != nil && strings.Contains(err.Error(), "outside the configured subagent model scope"), true, "strict")
	})
	tw(t, f, "fails closed when enforced inherit has no parent model", func(t *testing.T) {
		scope := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Allow: []string{"inherit"}, Enforce: new(true)}}, "worker", nil)
		_, err := resolveModelSelection("", models, "", &ResolveModelSelectionOptions{Scope: scope})
		eq(t, err != nil && strings.Contains(err.Error(), "'inherit' requires a current parent session model"), true, "fails closed")
	})
	tw(t, f, "accepts exact, bare, leaf, and declared alias response ids", func(t *testing.T) {
		eq(t, formatSubagentModelVerificationError("openai/gpt-5-mini:high", "gpt-5-mini", models, nil), "", "bare")
		eq(t, formatSubagentModelVerificationError("huggingface/org/model-v1", "model-v1", models, nil), "", "leaf")
		eq(t, formatSubagentModelVerificationError("openai/gpt-5-mini", "gateway-model", models, map[string][]string{"openai/gpt-5-mini": {"gateway-model"}}), "", "alias")
	})
	tw(t, f, "rejects a different response route and does not apply another model's alias", func(t *testing.T) {
		eq(t, strings.Contains(formatSubagentModelVerificationError("openai/gpt-5-mini", "anthropic/claude-sonnet-4", models, nil), "model_verification_failed"), true, "different")
		eq(t, strings.Contains(formatSubagentModelVerificationError("openai/gpt-5-mini", "gateway-model", models, map[string][]string{"anthropic/claude-sonnet-4": {"gateway-model"}}), "model_verification_failed"), true, "other alias")
	})
	tw(t, f, "detects common context overflow errors", func(t *testing.T) {
		for _, e := range []string{"maximum context length exceeded", "context window overflow", "too many tokens", "context_length_exceeded", "input too long"} {
			eq(t, isContextOverflow(e), true, e)
		}
	})
	tw(t, f, "does not classify tool failures, provider failures, or empty input as overflow", func(t *testing.T) {
		eq(t, isContextOverflow("bash failed (exit 1): input too long"), false, "tool")
		eq(t, isContextOverflow("429 rate limit exceeded"), false, "rate")
		eq(t, isContextOverflow(""), false, "empty")
	})
}

func TestModelResolutionDiagnostic(t *testing.T) {
	const f = "model-resolution-diagnostic"
	const model = "pengepul/commandcode/deepseek/deepseek-v4.1-flash"
	tw(t, f, "matches core's model-resolution failure text and leaves other failures alone", func(t *testing.T) {
		core := `Model "pengepul/commandcode/deepseek/deepseek-v4.1-flash:max" not found. Use --list-models to see available models.`
		provider := `Unknown provider "pengepul/commandcode". Use --list-models to see available providers/models.`
		eq(t, isChildModelResolutionFailure(core), true, "core")
		eq(t, isChildModelResolutionFailure("  "+provider+"  "), true, "provider")
		eq(t, isChildModelResolutionFailure(`Model "openai/gpt-5" is temporarily unavailable (HTTP 503).`), false, "503")
		eq(t, isChildModelResolutionFailure(`Model "openai/gpt-5" is disabled for this account.`), false, "disabled")
		eq(t, isChildModelResolutionFailure("Subagent session failed before its first turn."), false, "first turn")
		eq(t, isChildModelResolutionFailure(""), false, "empty")
	})
	tw(t, f, "names the foreground provider-inheritance rule, the model, and the remedies", func(t *testing.T) {
		text := formatChildModelResolutionDiagnostic(ChildModelResolutionDiagnostic{Agent: "provider-model-worker", Model: model, Host: "parent"})
		hasRe(t, text, `Agent 'provider-model-worker' ran as a foreground child, which never loads the parent's ambient extensions but inherits the providers they registered`)
		hasRe(t, text, "If 'pengepul/commandcode/deepseek/deepseek-v4[.]1-flash' is served by a provider extension, check the parent's `/model` list and the child extension diagnostics")
		noRe(t, text, "`async: true`")
		hasRe(t, text, "`subagentOnlyExtensions` or `extensions` in the agent frontmatter")
		noRe(t, text, "background child without the ambient extensions")
		noRe(t, text, "Capability ceiling")
	})
	tw(t, f, "tells a foreground child that a capability ceiling denies the extension", func(t *testing.T) {
		text := formatChildModelResolutionDiagnostic(ChildModelResolutionDiagnostic{Agent: "provider-model-worker", Model: model, Host: "parent",
			CapabilityCeiling: &DiagnosticCeiling{DenyExtensions: true, Sources: []string{"policy-fixture"}}})
		hasRe(t, text, "Capability ceiling from policy-fixture denies extensions")
		hasRe(t, text, "`async: true` does not help either")
		hasRe(t, text, "Relax the capability ceiling to allow extensions")
		noRe(t, text, "must run as background children")
		noRe(t, text, "load the provider extension explicitly")
	})
	tw(t, f, "tells a background child that a capability ceiling denies the extension", func(t *testing.T) {
		text := formatChildModelResolutionDiagnostic(ChildModelResolutionDiagnostic{Model: model, Host: "runner", CapabilityCeiling: &DiagnosticCeiling{DenyExtensions: true}})
		hasRe(t, text, "^Subagent ran as a background child without the ambient extensions")
		hasRe(t, text, "Capability ceiling from an unnamed source denies extensions")
		hasRe(t, text, "does not enable ambient loading")
		hasRe(t, text, "Relax the capability ceiling to allow extensions")
		noRe(t, text, "leave `extensions` unset so the child loads the ambient extensions")
	})
	tw(t, f, "explains a background child launched without the ambient extensions", func(t *testing.T) {
		text := formatChildModelResolutionDiagnostic(ChildModelResolutionDiagnostic{Model: model, Host: "runner"})
		hasRe(t, text, "^Subagent ran as a background child without the ambient extensions")
		hasRe(t, text, "that extension is not loaded for this child: list it in `subagentOnlyExtensions` or `extensions` in the agent frontmatter")
		hasRe(t, text, "leave `extensions` unset")
		noRe(t, text, "foreground child")
		noRe(t, text, "Capability ceiling")
	})
	tw(t, f, "defaults to the background branch and tolerates a missing model", func(t *testing.T) {
		text := formatChildModelResolutionDiagnostic(ChildModelResolutionDiagnostic{})
		hasRe(t, text, "^Subagent ran as a background child without the ambient extensions")
		hasRe(t, text, "If this model is served by a provider extension")
		noRe(t, text, "Capability ceiling")
	})
}

func TestThinkingCeiling(t *testing.T) {
	const f = "thinking-ceiling"
	tw(t, f, "orders every supported level and rejects malformed ceilings", func(t *testing.T) {
		levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
		for i, lv := range levels {
			got, err := parseThinkingLevel(lv, "thinking level")
			eq(t, []any{got, err}, []any{lv, nil}, lv)
			if i > 0 {
				c, _ := compareThinkingLevels(levels[i-1], lv)
				eq(t, c < 0, true, lv)
			}
		}
		_, err := parseThinkingLevel("turbo", "thinking level")
		eq(t, err != nil && strings.Contains(err.Error(), "expected one of"), true, "turbo")
	})
	tw(t, f, "intersects inherited ceilings and validates concrete model suffixes", func(t *testing.T) {
		a, _ := intersectThinkingCeilings("xhigh", "high")
		b, _ := intersectThinkingCeilings("low", "xhigh")
		eq(t, []string{a, b}, []string{"high", "low"}, "intersections")
		eq(t, assertThinkingWithinCeiling(ThinkingCeilingCheck{Model: "test/model:xhigh", Ceiling: "xhigh", Agent: "worker"}), nil, "within")
		err := assertThinkingWithinCeiling(ThinkingCeilingCheck{Model: "test/model:max", Ceiling: "xhigh", Agent: "worker", RunID: "run-1"})
		eq(t, err != nil && regexp.MustCompile(`Thinking level 'max'.*maximum 'xhigh'.*worker.*run-1`).MatchString(err.Error()), true, "exceeds")
		eq(t, assertThinkingWithinCeiling(ThinkingCeilingCheck{Model: "test/model", ConfigThinking: false, Ceiling: "off"}), nil, "false")
	})
	tw(t, f, "loads maxThinking with project precedence and attaches it to discovered agents", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"maxThinking": "max"}))
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"maxThinking": "xhigh"}))
		d := dAgents(t, e.project, ScopeBoth, "")
		eq(t, d.MaxThinking, "xhigh", "discovered")
		eq(t, findAgent(d.Agents, "worker").MaxThinking, "xhigh", "worker")
	})
	tw(t, f, "fails closed for invalid maxThinking settings", func(t *testing.T) {
		e := newAmEnv(t)
		for _, v := range []any{"", "turbo", 42.0, false} {
			wj(t, e.userSettings(), sub(m{"maxThinking": v}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "maxThinking")
		}
	})
}

func TestConfigDirRuntime(t *testing.T) {
	const f = "config-dir-runtime"
	setup := func(t *testing.T) {
		t.Helper()
		os.Unsetenv(piCodingAgentPackageRootEnv)
		t.Cleanup(func() { os.Unsetenv(piCodingAgentPackageRootEnv) })
	}
	pkg := func(t *testing.T, root, configDir string) {
		t.Helper()
		wjc(t, filepath.Join(root, "package.json"), m{"name": "@earendil-works/pi-coding-agent", "piConfig": m{"configDir": configDir}})
	}
	tw(t, f, "falls back without importing the Pi peer package at runtime", func(t *testing.T) {
		setup(t)
		eq(t, resolveConfigDirName("", "", ""), ".pi", "resolve")
		eq(t, getConfigDirName(), ".pi", "get")
	})
	tw(t, f, "honors an explicitly provided Pi module shape", func(t *testing.T) {
		setup(t)
		eq(t, resolveConfigDirName(".custom-pi", "", ""), ".custom-pi", "module")
		eq(t, resolveConfigDirName("", "", ""), ".pi", "empty module")
	})
	tw(t, f, "honors Pi package metadata without importing the peer package", func(t *testing.T) {
		setup(t)
		root := filepath.Join(tmp(t), "coding-agent")
		cli := filepath.Join(root, "dist", "cli.js")
		writeFile(t, cli, "")
		pkg(t, root, ".custom-pi")
		eq(t, resolveConfigDirName("", cli, ""), ".custom-pi", "entrypoint walk")
	})
	tw(t, f, "uses an explicit Pi package root before the process entrypoint walk", func(t *testing.T) {
		setup(t)
		root := filepath.Join(tmp(t), "coding-agent-root")
		pkg(t, root, ".root-pi")
		eq(t, resolveConfigDirName("", "", root), ".root-pi", "root")
	})
	tw(t, f, "invalidates cached runtime resolution when the package root changes", func(t *testing.T) {
		setup(t)
		base := tmp(t)
		first, second := filepath.Join(base, "first"), filepath.Join(base, "second")
		pkg(t, first, ".first-pi")
		pkg(t, second, ".second-pi")
		t.Setenv(piCodingAgentPackageRootEnv, first)
		eq(t, getConfigDirName(), ".first-pi", "first")
		eq(t, getConfigDirName(), ".first-pi", "first again")
		t.Setenv(piCodingAgentPackageRootEnv, second)
		eq(t, getConfigDirName(), ".second-pi", "second")
	})
	tw(t, f, "resolves async launches as the default unless explicitly disabled", func(t *testing.T) {
		eq(t, resolveAsyncByDefault(nil), true, "unset")
		eq(t, resolveAsyncByDefault(new(true)), true, "true")
		eq(t, resolveAsyncByDefault(new(false)), false, "false")
	})
	tw(t, f, "canonicalizes watcher paths and preserves the original path when native realpath fails", func(t *testing.T) {
		eq(t, resolveWatchPath(`C:\SHORT~1\watch`, func(string) (string, error) { return `C:\Long Path\watch`, nil }), `C:\Long Path\watch`, "resolved")
		eq(t, resolveWatchPath(`C:\SHORT~1\watch`, func(string) (string, error) { return "", errors.New("missing") }), `C:\SHORT~1\watch`, "kept")
	})
	tskip(t, f, "does not runtime-import the coding agent peer from shared utils", "greps the TypeScript source of src/shared/utils.ts for an import of the Pi peer package; the Go port has no such source file or peer import")
}

func TestPackageManifest(t *testing.T) {
	const f = "package-manifest"
	tskip(t, f, "the root entrypoint exposes the runtime error flag to TypeScript consumers", "compiles a TypeScript consumer file against index.ts and the Pi type packages with tsc; the Go port ships no TypeScript entrypoint or type declarations")
	tskip(t, f, "published extension APIs use supported package entrypoints", "asserts package.json exports of the TypeScript source package and imports its API entry modules (pi-subagents/agents, /inspectors, /background-work, ...); the Go extension exposes none of these Node modules")
	tskip(t, f, "direct @earendil-works runtime imports are declared for CI installs", "scans the TypeScript sources of src and test for @earendil-works imports against package.json dependencies; there is no TypeScript source in the Go port")
	tskip(t, f, "direct dependency declarations are exact version pins", "checks the npm dependency pins of the original's package.json; the Go module pins its dependencies in go.mod and go.sum")
	tskip(t, f, "host-owned packages are optional peers with supported ranges, not production dependencies", "checks the npm peerDependencies of the original's package.json; the Go extension has no Node peer packages")
	tskip(t, f, "host-owned development packages use the supported SDK baseline", "checks the npm devDependencies of the original's package.json; the Go extension builds against the PiG SDK pinned in go.mod")
	tskip(t, f, "old pi package scope is not used by source or tests", "scans TypeScript sources for the retired @mariozechner/pi- scope; no TypeScript source exists in the Go port")
	tskip(t, f, "Pi package resolution stays export-map safe", "scans TypeScript sources for Node package.json subpath resolution of Pi packages; no Node module resolution exists in the Go port")
}
