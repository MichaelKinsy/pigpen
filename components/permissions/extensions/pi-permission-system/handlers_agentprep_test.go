package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/before-agent-start.test.ts.

// apSections is the ordered `sections` collection of the prompt options (a map is enough: the tests read it whole).
type apSections struct{ values map[string]string }

func (s *apSections) Set(name, value string) { s.values[name] = value }

// apManager is the permission manager of the original's real session fixture: check answers allow unless a test says otherwise, and
// isToolFullyDenied is a function of the tool name.
type apManager struct {
	*fakeManager
	denied func(tool string) bool
}

func (m apManager) IsToolFullyDenied(tool string, agent *string) bool {
	m.fakeManager.IsToolFullyDenied(tool, agent)
	return m.denied != nil && m.denied(tool)
}

// apRegistry is makeStatefulToolRegistry: getActive reads back whatever the last setActive accepted, filtered by the registered
// set, as Pi's setActiveToolsByName does. guidelines are the promptGuidelines of the default registry's tools.
type apRegistry struct {
	registered map[string]bool
	order      []string
	active     []string
	guidelines map[string][]string
	setCalls   [][]string
}

func newAPRegistry(active []string, registered ...string) *apRegistry {
	r := &apRegistry{registered: map[string]bool{}, active: append([]string{}, active...), guidelines: map[string][]string{}}
	if len(registered) == 0 {
		registered = active
	}
	for _, n := range registered {
		r.register(n)
	}
	return r
}

func (r *apRegistry) register(n string) {
	if !r.registered[n] {
		r.registered[n] = true
		r.order = append(r.order, n)
	}
}

func (r *apRegistry) unregister(n string) {
	delete(r.registered, n)
	for i, x := range r.order {
		if x == n {
			r.order = append(r.order[:i:i], r.order[i+1:]...)
			break
		}
	}
}

func (r *apRegistry) GetAll() ([]any, error) {
	out := []any{}
	for _, n := range r.order {
		rec := newObject()
		rec.set("name", n)
		if g, ok := r.guidelines[n]; ok {
			rec.set("promptGuidelines", stringsToAny(g))
		}
		out = append(out, rec)
	}
	return out, nil
}
func (r *apRegistry) GetActive() ([]string, error) { return append([]string{}, r.active...), nil }
func (r *apRegistry) SetActive(names []string) {
	r.setCalls = append(r.setCalls, append([]string{}, names...))
	r.active = nil
	for _, n := range names {
		if r.registered[n] {
			r.active = append(r.active, n)
		}
	}
}

// defaultAPRegistry is makeToolRegistry(): read and bash registered with their guidelines, both active.
func defaultAPRegistry() *apRegistry {
	r := newAPRegistry([]string{"read", "bash"})
	r.guidelines["read"] = []string{"Use read to examine files."}
	r.guidelines["bash"] = []string{"Use bash for file operations."}
	return r
}

type apDetector struct{ child bool }

func (d apDetector) IsSubagent(SubagentDetectionContext) (bool, error) { return d.child, nil }

type apRig struct {
	handler  *AgentPrepHandler
	rig      *sessionRig
	manager  apManager
	registry *apRegistry
	logger   *gfLogger
	session  *PermissionSession
	order    *recordedOrder
	prepared []SessionContext
}

// apTurnPrep records the order of prepare against the agent-name lookup.
type apTurnPrep struct {
	inner TurnPreparation
	rig   *apRig
}

func (p apTurnPrep) Prepare(ctx SessionContext) error {
	p.rig.order.add("prepare")
	p.rig.prepared = append(p.rig.prepared, ctx)
	return p.inner.Prepare(ctx)
}

type apOpts struct {
	denied   func(string) bool
	registry *apRegistry
	child    bool
}

func makeAgentPrep(o apOpts) *apRig {
	r := makeRealSession(nil)
	a := &apRig{rig: r, logger: &gfLogger{}, order: &recordedOrder{}, session: r.session}
	a.manager = apManager{r.pm, o.denied}
	a.registry = o.registry
	if a.registry == nil {
		a.registry = defaultAPRegistry()
	}
	session := NewPermissionSession(r.paths, r.fwd, a.manager, r.rules, r.store, r.auth, PathFlavorForPlatform("linux"))
	a.session = session
	resolver := NewPermissionResolver(a.manager, r.rules)
	turnPrep := apTurnPrep{NewSessionTurnPrep(session, func() {}, &fakeAnnouncer{order: a.order}, &fakeIssueReporter{}), a}
	a.handler = NewAgentPrepHandler(turnPrep, session, resolver, a.registry, a.logger, apDetector{o.child})
	return a
}

type apSkill struct {
	name     string
	disabled bool
}

func (s apSkill) path() string { return "/skills/" + s.name + "/SKILL.md" }

// formatSkillsForPrompt is Pi's catalogue rendering: only the skills a model may invoke are listed.
func formatSkillsForPrompt(skills []apSkill) string {
	var listed []apSkill
	for _, s := range skills {
		if !s.disabled {
			listed = append(listed, s)
		}
	}
	if len(listed) == 0 {
		return ""
	}
	lines := []string{"\n\nThe following skills provide specialized instructions for specific tasks.", "<available_skills>"}
	for _, s := range listed {
		lines = append(lines, "  <skill>", "    <name>"+s.name+"</name>", "    <description>Description of "+s.name+"</description>",
			"    <location>"+s.path()+"</location>", "  </skill>")
	}
	return strings.Join(append(lines, "</available_skills>"), "\n")
}

type apEvent struct {
	payload  BeforeAgentStartPayload
	sections *apSections
}

func (e apEvent) skillNames() []string {
	names := []string{}
	for _, s := range e.payload.Options.Skills {
		names = append(names, s.Name)
	}
	return names
}

// makeAPEvent is makeEvent: a fresh event per call, so a handler's option mutations never carry over.
func makeAPEvent(prompt string, mut ...func(*PromptOptions, *apSections)) apEvent {
	sections := &apSections{values: map[string]string{}}
	options := &PromptOptions{ToolSnippets: map[string]string{}, Sections: sections}
	for _, m := range mut {
		m(options, sections)
	}
	return apEvent{payload: BeforeAgentStartPayload{SystemPrompt: prompt, Options: options}, sections: sections}
}

func withSkills(skills ...apSkill) func(*PromptOptions, *apSections) {
	return func(o *PromptOptions, _ *apSections) {
		for _, s := range skills {
			o.Skills = append(o.Skills, PromptSkill{Name: s.name, Raw: s})
		}
	}
}

func skillEvent(skills ...apSkill) apEvent {
	return makeAPEvent("You are an assistant."+formatSkillsForPrompt(skills), withSkills(skills...))
}

func (a *apRig) handle(t *testing.T, e BeforeAgentStartPayload, ctx SessionContext) {
	t.Helper()
	if err := a.handler.Handle(e, ctx); err != nil {
		t.Fatal(err)
	}
}

func (a *apRig) denySkill(name string) {
	a.rig.pm.checkFn = func(intent ResolvedAccessIntent) PermissionCheckResult {
		if it, ok := intent.(ToolAccessIntent); ok && it.Surface == "skill" {
			if n, _ := ToRecord(it.Input).str("name"); n == name {
				return PermissionCheckResult{State: "deny", ToolName: "skill", Source: "tool", Origin: "builtin"}
			}
		}
		return PermissionCheckResult{State: "allow", ToolName: "read", Source: "tool", Origin: "builtin"}
	}
}

func apCtx() SessionContext {
	ctx, _ := trustedCtx(true)
	return ctx
}

func TestShouldExposeToolTwins(t *testing.T) {
	const f = "handlers/before-agent-start"
	tw(t, f, "returns true when some value under the surface is reachable", func(t *testing.T) {
		eq(t, ShouldExposeTool("read", nil, func(string, *string) bool { return false }), true, "exposed")
	})
	tw(t, f, "returns false when every value under the surface is denied", func(t *testing.T) {
		eq(t, ShouldExposeTool("write", nil, func(string, *string) bool { return true }), false, "withheld")
	})
	tw(t, f, "passes agentName through to isToolFullyDenied", func(t *testing.T) {
		agent := "my-agent"
		var got *string
		ShouldExposeTool("read", &agent, func(_ string, a *string) bool { got = a; return false })
		eq(t, got, &agent, "agent")
	})
	tw(t, f, "converts null agentName to undefined for isToolFullyDenied", func(t *testing.T) {
		got := &[]string{"unset"}[0]
		ShouldExposeTool("read", nil, func(_ string, a *string) bool { got = a; return false })
		eq(t, got == nil, true, "nil is undefined")
	})
}

func TestAgentPrepHandlerTwins(t *testing.T) {
	const f = "handlers/before-agent-start"
	all := func(string) bool { return true }
	none := func(string) bool { return false }
	only := func(deniedTool string) func(string) bool { return func(n string) bool { return n == deniedTool } }

	tw(t, f, "prepares the session for the turn before reading its state", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.session.agentNameLookup = func(ActiveAgentContext) (*string, error) {
			a.order.add("resolveAgentName")
			return nil, nil
		}
		ctx := apCtx()
		a.handle(t, makeAPEvent("You are an assistant.").payload, ctx)
		eq(t, a.order.calls[0], "prepare", "prepare first")
		eq(t, a.order.calls[len(a.order.calls)-1], "resolveAgentName", "then the lookup")
		eq(t, a.prepared[0].Cwd, ctx.Cwd, "with ctx")
	})
	tw(t, f, "resolves agent name using systemPrompt", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		var prompt string
		a.session.promptAgentNameLookup = func(p string) *string { prompt = p; return nil }
		a.handle(t, makeAPEvent("<active_agent name='x'>").payload, apCtx())
		eq(t, prompt, "<active_agent name='x'>", "prompt")
	})
	tw(t, f, "filters out denied tools from allowed list", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: all, registry: newAPRegistry([]string{"write", "read"})})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, a.registry.setCalls, [][]string{{}}, "setActive")
	})
	tw(t, f, "includes allowed and ask tools in the active list", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: newAPRegistry([]string{"read", "write"})})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, a.registry.setCalls, [][]string{{"read", "write"}}, "setActive")
	})
	tw(t, f, "does not activate registered tools pi left inactive (find/grep/ls)", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: newAPRegistry([]string{"read", "bash", "edit", "write"}, "read", "bash", "edit", "write", "find", "grep", "ls")})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, a.registry.setCalls, [][]string{{"read", "bash", "edit", "write"}}, "setActive")
	})
	tw(t, f, "calls setActive on every turn (no dedup gate)", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: newAPRegistry([]string{"read"})})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, len(a.registry.setCalls), 2, "setActive calls")
	})

	tw(t, f, "drops a denied skill from the prompt options on every turn, not just the first", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.denySkill("secret")
		for turn := 1; turn <= 2; turn++ {
			e := skillEvent(apSkill{name: "secret"}, apSkill{name: "open"})
			a.handle(t, e.payload, apCtx())
			eq(t, e.skillNames(), []string{"open"}, "turn")
		}
	})
	tw(t, f, "drops a denied skill on a turn whose rendered prompt carries no catalogue yet", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.denySkill("secret")
		e := makeAPEvent("You are an assistant.", withSkills(apSkill{name: "secret"}, apSkill{name: "open"}))
		a.handle(t, e.payload, apCtx())
		eq(t, e.skillNames(), []string{"open"}, "skills")
	})
	tw(t, f, "keeps a skill Pi does not list in the prompt", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.denySkill("secret")
		e := skillEvent(apSkill{name: "secret"}, apSkill{name: "manual-only", disabled: true})
		a.handle(t, e.payload, apCtx())
		eq(t, e.skillNames(), []string{"manual-only"}, "skills")
	})
	tw(t, f, "still drops a denied skill under an operator's custom prompt", func(t *testing.T) {
		const custom = "You are my personal coding assistant."
		a := makeAgentPrep(apOpts{})
		a.denySkill("secret")
		skills := []apSkill{{name: "secret"}, {name: "open"}}
		e := makeAPEvent(custom+formatSkillsForPrompt(skills), withSkills(skills...), func(o *PromptOptions, _ *apSections) { o.CustomPrompt = custom })
		a.handle(t, e.payload, apCtx())
		eq(t, e.skillNames(), []string{"open"}, "skills")
	})
	tw(t, f, "stores only the visible skills for path matching", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.denySkill("secret")
		a.handle(t, skillEvent(apSkill{name: "secret"}, apSkill{name: "open"}).payload, apCtx())
		names := []string{}
		for _, entry := range a.session.GetActiveSkillEntries() {
			names = append(names, entry.Name)
		}
		eq(t, names, []string{"open"}, "entries")
	})
	tw(t, f, "stores resolved skill entries on the session", func(t *testing.T) {
		a := makeAgentPrep(apOpts{})
		a.session.SetActiveSkillEntries([]SkillPromptEntry{makeSkillEntry("stale")})
		a.handle(t, makeAPEvent("You are an assistant.").payload, apCtx())
		eq(t, len(a.session.GetActiveSkillEntries()), 0, "replaced")
	})

	for _, withOptions := range []bool{true, false} {
		title := "normalizes array prompts with systemPromptOptions=%s"
		name := strings.Replace(title, "%s", map[bool]string{true: "true", false: "false"}[withOptions], 1)
		tw(t, f, name, func(t *testing.T) {
			a := makeAgentPrep(apOpts{denied: all, registry: newAPRegistry([]string{"read", "bash"})})
			skills := []apSkill{{name: "open"}}
			prompt := JoinSystemPromptFragments([]string{"<active_agent", "name='worker'>", formatSkillsForPrompt(skills)})
			payload := BeforeAgentStartPayload{SystemPrompt: prompt}
			if withOptions {
				payload = makeAPEvent(prompt, withSkills(skills...)).payload
			}
			a.handle(t, payload, apCtx())
			got, err := a.session.ResolveAgentName(apCtx(), "")
			eq(t, err, nil, "err")
			eq(t, got != nil && *got == "worker", true, "agent")
			active, _ := a.registry.GetActive()
			eq(t, len(active), 0, "active")
			locations := []string{}
			for _, entry := range a.session.GetActiveSkillEntries() {
				locations = append(locations, entry.Location)
			}
			eq(t, locations, []string{skills[0].path()}, "locations")
		})
	}

	tw(t, f, "returns no override, so sections later handlers add still reach the provider", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("bash")})
		if err := a.handler.Handle(makeAPEvent("You are an assistant.").payload, apCtx()); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, f, "leaves the tool surface to Pi, which renders it from the narrowed active set", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("bash")})
		e := makeAPEvent("You are an assistant.", func(o *PromptOptions, _ *apSections) {
			o.ToolSnippets = map[string]string{"read": "Read file contents", "bash": "Run commands"}
		})
		a.handle(t, e.payload, apCtx())
		eq(t, a.registry.setCalls, [][]string{{"read"}}, "setActive")
		eq(t, e.sections.values, map[string]string{}, "sections")
	})

	const custom = "You are my personal coding assistant."
	customOpts := func(mut ...func(*PromptOptions, *apSections)) []func(*PromptOptions, *apSections) {
		return append([]func(*PromptOptions, *apSections){func(o *PromptOptions, _ *apSections) { o.CustomPrompt = custom }}, mut...)
	}
	snippets := func(m map[string]string) func(*PromptOptions, *apSections) {
		return func(o *PromptOptions, _ *apSections) { o.ToolSnippets = m }
	}
	childRules := strings.Join([]string{"- Use read to examine files.", "- Be concise in your responses", "- Show file paths clearly when working with files"}, "\n")

	tw(t, f, "leaves an operator's custom prompt as Pi built it", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: newAPRegistry([]string{"read"})})
		e := makeAPEvent(custom, customOpts(snippets(map[string]string{"read": "Read file contents"}))...)
		a.handle(t, e.payload, apCtx())
		eq(t, e.sections.values, map[string]string{}, "sections")
	})
	tw(t, f, "still filters the active tools under an operator's custom prompt", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("bash")})
		a.handle(t, makeAPEvent(custom, customOpts(snippets(map[string]string{"read": "Read file contents", "bash": "Run commands"}))...).payload, apCtx())
		eq(t, a.registry.setCalls, [][]string{{"read"}}, "setActive")
	})
	tw(t, f, "states the child's tools and rules as prompt sections", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true, registry: func() *apRegistry { r := defaultAPRegistry(); r.active = []string{"read"}; return r }()})
		e := makeAPEvent(custom, customOpts(snippets(map[string]string{"read": "Read file contents"}))...)
		a.handle(t, e.payload, apCtx())
		eq(t, e.sections.values, map[string]string{"tools": "- read: Read file contents", "rules": childRules}, "sections")
	})
	tw(t, f, "drops a denied tool and its guidelines from the child's sections", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true, denied: only("bash")})
		e := makeAPEvent(custom, customOpts(snippets(map[string]string{"read": "Read file contents", "bash": "Run commands"}))...)
		a.handle(t, e.payload, apCtx())
		eq(t, e.sections.values, map[string]string{"tools": "- read: Read file contents", "rules": childRules}, "sections")
	})
	tw(t, f, "carries the rules another extension added to the prompt options", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true})
		e := makeAPEvent(custom, customOpts(func(o *PromptOptions, _ *apSections) { o.PromptGuidelines = []string{"An extension's rule"} })...)
		a.handle(t, e.payload, apCtx())
		eq(t, strings.Contains(e.sections.values["rules"], "- An extension's rule"), true, "rules")
	})
	tw(t, f, "clears a peer's tools section when no allowed tool has a snippet", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true})
		e := makeAPEvent(custom, customOpts(func(_ *PromptOptions, s *apSections) { s.values["tools"] = "- peer: a tool a peer listed" })...)
		a.handle(t, e.payload, apCtx())
		// Pi leaves an empty section out of the prompt.
		v, present := e.sections.values["tools"]
		eq(t, v, "", "tools")
		eq(t, present, true, "stated")
	})
	tw(t, f, "states the same sections on every turn for an unchanged policy", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true})
		mk := func() apEvent {
			return makeAPEvent(custom, customOpts(snippets(map[string]string{"read": "Read file contents"}))...)
		}
		first, second := mk(), mk()
		a.handle(t, first.payload, apCtx())
		a.handle(t, second.payload, apCtx())
		eq(t, second.sections.values, first.sections.values, "sections")
	})
	tw(t, f, "states none when the custom prompt is empty, which Pi reads as none", func(t *testing.T) {
		a := makeAgentPrep(apOpts{child: true})
		e := makeAPEvent("You are an assistant.", func(o *PromptOptions, _ *apSections) {
			o.CustomPrompt = ""
			o.ToolSnippets = map[string]string{"read": "Read file contents"}
		})
		a.handle(t, e.payload, apCtx())
		eq(t, e.sections.values, map[string]string{}, "sections")
	})

	piDefaults := []string{"read", "bash", "edit", "write"}
	launchedWith := append(append([]string{}, piDefaults...), "ls", "find", "grep")
	tw(t, f, "restores a tool after its deny rule is removed, without a restart", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: newAPRegistry(launchedWith)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		active, _ := a.registry.GetActive()
		eq(t, active, launchedWith, "all active")
		a.manager.denied = only("ls")
		a.handler.resolver = NewPermissionResolver(a.manager, a.rig.rules)
		a.handle(t, makeAPEvent("x").payload, apCtx())
		active, _ = a.registry.GetActive()
		eq(t, active, []string{"read", "bash", "edit", "write", "find", "grep"}, "ls withheld")
		a.manager.denied = none
		a.handler.resolver = NewPermissionResolver(a.manager, a.rig.rules)
		a.handle(t, makeAPEvent("x").payload, apCtx())
		active, _ = a.registry.GetActive()
		eq(t, active, launchedWith, "restored")
	})
	// the manager the session and resolver hold is a copy of apManager; its denied func is read through a pointer so a test can swap it
	tw(t, f, "keeps a tool withheld for as long as its deny rule stands", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("ls"), registry: newAPRegistry(launchedWith)})
		for i := 0; i < 3; i++ {
			a.handle(t, makeAPEvent("x").payload, apCtx())
		}
		active, _ := a.registry.GetActive()
		eq(t, inStrings(active, "ls"), false, "ls stays withheld")
	})
	tw(t, f, "does not reactivate a withheld tool that pi unregistered and re-registered", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("ls"), registry: newAPRegistry(launchedWith)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.registry.unregister("ls")
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.registry.register("ls")
		a.manager.denied = none
		a.handler.resolver = NewPermissionResolver(a.manager, a.rig.rules)
		a.handle(t, makeAPEvent("x").payload, apCtx())
		active, _ := a.registry.GetActive()
		eq(t, inStrings(active, "ls"), false, "ls stays withheld")
	})
	tw(t, f, "records the withheld tools on the debug stream when the surface changes", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("ls"), registry: newAPRegistry(launchedWith)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, len(a.logger.debugs), 1, "debug entries")
		eq(t, logJSON(a.logger.debugs[0]), `tool_surface.changed {"exposed":["read","bash","edit","write","find","grep"],"withheld":["ls"],"restored":[]}`, "entry")
	})
	tw(t, f, "records the restored tools when a rule is relaxed", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("ls"), registry: newAPRegistry(launchedWith)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.logger.debugs = nil
		a.manager.denied = none
		a.handler.resolver = NewPermissionResolver(a.manager, a.rig.rules)
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, len(a.logger.debugs), 1, "debug entries")
		eq(t, logJSON(a.logger.debugs[0]), `tool_surface.changed {"exposed":["read","bash","edit","write","ls","find","grep"],"withheld":[],"restored":["ls"]}`, "entry")
	})
	tw(t, f, "stays quiet while the surface is unchanged", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("ls"), registry: newAPRegistry(launchedWith)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.logger.debugs = nil
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.handle(t, makeAPEvent("x").payload, apCtx())
		eq(t, len(a.logger.debugs), 0, "debugs")
	})
	tw(t, f, "does not activate a registered tool pi left inactive when the policy relaxes", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: only("bash"), registry: newAPRegistry(piDefaults, launchedWith...)})
		a.handle(t, makeAPEvent("x").payload, apCtx())
		a.manager.denied = none
		a.handler.resolver = NewPermissionResolver(a.manager, a.rig.rules)
		a.handle(t, makeAPEvent("x").payload, apCtx())
		active, _ := a.registry.GetActive()
		eq(t, active, piDefaults, "active")
	})
}

func TestAgentPrepHandlerErrors(t *testing.T) {
	// not upstream cases: a failing host read or agent lookup stops the handler before it changes anything, and is never read as
	// "no agent" (the global scope is the least restrictive one)
	t.Run("a failing agent-name lookup is returned and narrows nothing", func(t *testing.T) {
		a := makeAgentPrep(apOpts{denied: func(string) bool { return true }})
		a.session.agentNameLookup = func(ActiveAgentContext) (*string, error) { return nil, errTest }
		if err := a.handler.Handle(makeAPEvent("x").payload, apCtx()); err == nil {
			t.Fatal("the lookup error was swallowed")
		}
		eq(t, len(a.registry.setCalls), 0, "setActive")
	})
	t.Run("a failing tool read is returned", func(t *testing.T) {
		a := makeAgentPrep(apOpts{registry: nil})
		a.handler.toolRegistry = failingAgentTools{}
		if err := a.handler.Handle(makeAPEvent("x").payload, apCtx()); err == nil {
			t.Fatal("the registry error was swallowed")
		}
	})
}

type failingAgentTools struct{}

func (failingAgentTools) GetAll() ([]any, error)       { return nil, errTest }
func (failingAgentTools) GetActive() ([]string, error) { return nil, errTest }
func (failingAgentTools) SetActive([]string)           {}
