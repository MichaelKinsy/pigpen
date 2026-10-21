package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/gates/tool-call-gate-pipeline.test.ts and gate-producer-failure.test.ts.

// gfGateInputs is makeGateInputs: recording stubs with the original's defaults.
type gfGateInputs struct {
	skills     func() []SkillPromptEntry
	infra      func() InfrastructureReadScope
	limits     func() ToolPreviewFormatterOptions
	normalizer func() *PathNormalizer
	aliases    func() ShellToolsConfig
	calls      map[string]int
}

func (g *gfGateInputs) hit(n string) { g.calls[n]++ }
func (g *gfGateInputs) GetActiveSkillEntries() []SkillPromptEntry {
	g.hit("skills")
	return g.skills()
}
func (g *gfGateInputs) GetInfrastructureReadScope() InfrastructureReadScope {
	g.hit("infra")
	return g.infra()
}
func (g *gfGateInputs) GetToolPreviewLimits() ToolPreviewFormatterOptions {
	g.hit("limits")
	return g.limits()
}
func (g *gfGateInputs) GetPathNormalizer() *PathNormalizer {
	g.hit("normalizer")
	return g.normalizer()
}
func (g *gfGateInputs) GetShellToolAliases() ShellToolsConfig {
	g.hit("aliases")
	return g.aliases()
}

func makeGateInputs(t *testing.T, mut ...func(*gfGateInputs)) *gfGateInputs {
	t.Helper()
	mockRealpath(t, identityRealpath)
	n := NewPathNormalizer(PathFlavorForPlatform(goos()), "/test/cwd")
	g := &gfGateInputs{
		skills: func() []SkillPromptEntry { return nil },
		infra: func() InfrastructureReadScope {
			return InfrastructureReadScope{Dirs: []string{}, ExcludedDirs: []string{}}
		},
		limits: func() ToolPreviewFormatterOptions {
			return ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: 500, ToolTextSummaryMaxLength: 100}
		},
		normalizer: func() *PathNormalizer { return n },
		aliases:    func() ShellToolsConfig { return nil },
		calls:      map[string]int{},
	}
	for _, m := range mut {
		m(g)
	}
	return g
}

// gfRunSpy records the gates a runner is asked to run (vi.spyOn(runner, "run")), delegating or answering a fixed outcome.
type gfRunSpy struct {
	inner GateRunning
	fixed *GateOutcome
	gates []GateResult
}

func (s *gfRunSpy) Run(gate GateResult, agent *string) (GateOutcome, error) {
	s.gates = append(s.gates, gate)
	if s.fixed != nil {
		return *s.fixed, nil
	}
	return s.inner.Run(gate, agent)
}

func TestToolCallGatePipelineTwins(t *testing.T) {
	const f = "handlers/gates/tool-call-gate-pipeline"
	type parseCall struct {
		command string
		workdir *string
	}
	// newPipeline builds a pipeline whose BashProgram parse is the original's mock: it records the call and answers a canned program.
	newPipeline := func(t *testing.T, resolver ScopedPermissionResolver, inputs *gfGateInputs, program func() *BashProgram, ex ToolAccessExtractorLookup) (*ToolCallGatePipeline, *[]parseCall) {
		t.Helper()
		p := NewToolCallGatePipeline(resolver, inputs, nil, ex)
		calls := &[]parseCall{}
		p.parseBash = func(command string, n *PathNormalizer, o *BashProgramOptions) (*BashProgram, error) {
			*calls = append(*calls, parseCall{command, o.Workdir})
			return program(), nil
		}
		return p, calls
	}
	mockProgram := func(command string) func() *BashProgram {
		return func() *BashProgram { return &BashProgram{sourceCommand: command} }
	}
	echo := mockProgram("echo hello")
	eval := func(t *testing.T, p *ToolCallGatePipeline, tcc ToolCallContext, runner GateRunning) GateOutcome {
		t.Helper()
		out, err := p.Evaluate(tcc, runner)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	readTcc := func(input *jsObject) ToolCallContext { return makeTcc(tccTool("read", input)) }

	t.Run("evaluate — non-bash tool", func(t *testing.T) {
		tw(t, f, "returns allow when all gates pass", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eq(t, eval(t, p, readTcc(newObject()), runner), gfAllow, "outcome")
		})
		tw(t, f, "returns block when the tool gate denies", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, _ := newPipeline(t, makeResolver(makeCheckResult(ckState("deny", "*"))), makeGateInputs(t), echo, nil)
			if out := eval(t, p, readTcc(newObject()), runner); out.Action != "block" {
				t.Errorf("outcome %+v", out)
			}
		})
		tw(t, f, "short-circuits after the first blocking gate without evaluating later ones", func(t *testing.T) {
			runner, _ := makeGateRunner()
			spy := &gfRunSpy{inner: runner, fixed: &GateOutcome{Action: "block", Reason: "first gate blocked"}}
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eq(t, eval(t, p, readTcc(newObject()), spy), GateOutcome{Action: "block", Reason: "first gate blocked"}, "outcome")
			// Pipeline looped to the first gate, got block, and stopped: not all 6 gates.
			eq(t, len(spy.gates), 1, "runs")
		})
		tw(t, f, "calls getToolPreviewLimits() during evaluate", func(t *testing.T) {
			runner, _ := makeGateRunner()
			in := makeGateInputs(t)
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), in, echo, nil)
			eval(t, p, readTcc(newObject()), runner)
			eq(t, in.calls["limits"] > 0, true, "called")
		})
		tw(t, f, "calls getInfrastructureReadScope() during evaluate", func(t *testing.T) {
			runner, _ := makeGateRunner()
			in := makeGateInputs(t)
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), in, echo, nil)
			eval(t, p, readTcc(newObject()), runner)
			eq(t, in.calls["infra"] > 0, true, "called")
		})
		tw(t, f, "calls getActiveSkillEntries() during evaluate", func(t *testing.T) {
			runner, _ := makeGateRunner()
			in := makeGateInputs(t)
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), in, echo, nil)
			eval(t, p, readTcc(newObject()), runner)
			eq(t, in.calls["skills"] > 0, true, "called")
		})
		tw(t, f, "does not call BashProgram.parse for non-bash tools", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eval(t, p, readTcc(newObject()), runner)
			eq(t, len(*calls), 0, "parses")
		})
	})

	t.Run("evaluate — a deny pre-empts a gate that would prompt", func(t *testing.T) {
		// The `path` gate (#2) asks and the per-tool gate (#6) denies, which is the reported ordering.
		askingPathDenyingTool := func() *gfResolver {
			r := makeResolver()
			r.fn = func(intent AccessIntent) PermissionCheckResult {
				if gfIntentSurface(intent) == "read" {
					return makeCheckResult(ckState("deny", "secret*"))
				}
				return makeCheckResult(ckState("ask", "*"))
			}
			return r
		}
		tw(t, f, "runs the denying gate first and never reaches the asking one", func(t *testing.T) {
			runner, deps := makeGateRunner()
			spy := &gfRunSpy{inner: runner}
			p, _ := newPipeline(t, askingPathDenyingTool(), makeGateInputs(t), echo, nil)
			out := eval(t, p, readTcc(obj("path", "secrets.txt")), spy)
			if out.Action != "block" {
				t.Errorf("outcome %+v", out)
			}
			eq(t, len(spy.gates), 1, "runs")
			d, ok := spy.gates[0].(*GateDescriptor)
			eq(t, ok && d.Surface == "read", true, "first gate surface")
			eq(t, len(deps.escalate.calls), 0, "escalations")
		})
		tw(t, f, "still prompts when no gate denies", func(t *testing.T) {
			runner, deps := makeGateRunner()
			p, _ := newPipeline(t, makeResolver(makeCheckResult(ckState("ask", "*"))), makeGateInputs(t), echo, nil)
			eval(t, p, readTcc(obj("path", "secrets.txt")), runner)
			eq(t, len(deps.escalate.calls) > 0, true, "escalated")
		})
	})

	t.Run("evaluate — bash tool", func(t *testing.T) {
		bashTcc := func(cmd string, mut ...func(*ToolCallContext)) ToolCallContext {
			return makeTcc(append([]func(*ToolCallContext){tccTool("bash", obj("command", cmd))}, mut...)...)
		}
		tw(t, f, "returns allow when the bash command is permitted", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, _ := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eq(t, eval(t, p, bashTcc("echo hello"), runner), gfAllow, "outcome")
		})
		tw(t, f, "parses BashProgram exactly once per evaluate for bash tools with a command", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eval(t, p, bashTcc("echo hello"), runner)
			eq(t, *calls, []parseCall{{"echo hello", nil}}, "parses")
		})
		tw(t, f, "does not parse BashProgram when the bash command is empty", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eval(t, p, bashTcc(""), runner)
			eq(t, len(*calls), 0, "parses")
		})
		tw(t, f, "parses a bash command with no policy input — candidacy is not rule-driven (#645)", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eval(t, p, bashTcc("cat id_rsa", func(c *ToolCallContext) { c.AgentName = sp("my-agent") }), runner)
			eq(t, *calls, []parseCall{{"cat id_rsa", nil}}, "parses")
		})
	})

	t.Run("evaluate — aliased shell tool (#574)", func(t *testing.T) {
		execAliases := func() ShellToolsConfig {
			return ShellToolsConfig{"exec_command": ShellToolAlias{CommandArgument: "cmd", WorkdirArgument: "workdir"}}
		}
		withAliases := func(g *gfGateInputs) { g.aliases = execAliases }
		execTcc := func(input *jsObject) ToolCallContext { return makeTcc(tccTool("exec_command", input)) }
		withCommand := func(text string) func() *BashProgram {
			return func() *BashProgram {
				return &BashProgram{sourceCommand: text, commandUnits: []BashCommand{{Text: text}}}
			}
		}
		tw(t, f, "consults getShellToolAliases and parses the aliased command argument", func(t *testing.T) {
			runner, _ := makeGateRunner()
			in := makeGateInputs(t, withAliases)
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), in, echo, nil)
			eval(t, p, execTcc(obj("cmd", "npm install")), runner)
			eq(t, in.calls["aliases"] > 0, true, "consulted")
			eq(t, *calls, []parseCall{{"npm install", nil}}, "parses")
		})
		tw(t, f, "threads the aliased workdir argument into BashProgram.parse (#574)", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t, withAliases), echo, nil)
			eval(t, p, execTcc(obj("cmd", "cat file", "workdir", "/etc")), runner)
			eq(t, *calls, []parseCall{{"cat file", sp("/etc")}}, "parses")
		})
		tw(t, f, "resolves the aliased per-tool check on the bash surface, never the tool's own", func(t *testing.T) {
			runner, _ := makeGateRunner()
			resolver := makeResolver(makeCheckResult(func(c *PermissionCheckResult) { c.Source, c.Command = "bash", sp("npm install") }))
			p, _ := newPipeline(t, resolver, makeGateInputs(t, withAliases), withCommand("npm install"), nil)
			eval(t, p, execTcc(obj("cmd", "npm install")), runner)
			var bashCall AccessIntent
			for _, c := range resolver.calls {
				if gfIntentSurface(c) == "bash" {
					bashCall = c
					break
				}
				if gfIntentSurface(c) == "exec_command" {
					t.Errorf("resolved the tool's own surface")
				}
			}
			eq(t, bashCall, AccessIntent(BashCommandAccessIntent{Surface: "bash", Command: "npm install", Spellings: nil}), "bash call")
		})
		tw(t, f, "blocks an aliased command denied on the bash surface", func(t *testing.T) {
			runner, _ := makeGateRunner()
			resolver := makeResolver()
			resolver.fn = func(intent AccessIntent) PermissionCheckResult {
				if gfIntentSurface(intent) == "bash" {
					return makeCheckResult(ckState("deny", "npm *"), func(c *PermissionCheckResult) { c.Source, c.Command = "bash", sp("npm install") })
				}
				return makeCheckResult()
			}
			p, _ := newPipeline(t, resolver, makeGateInputs(t, withAliases), withCommand("npm install"), nil)
			if out := eval(t, p, execTcc(obj("cmd", "npm install")), runner); out.Action != "block" {
				t.Errorf("outcome %+v", out)
			}
		})
		tw(t, f, "does not treat an extension tool as a shell without a shellTools alias", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, calls := newPipeline(t, makeResolver(makeCheckResult()), makeGateInputs(t), echo, nil)
			eval(t, p, execTcc(obj("cmd", "npm install")), runner)
			eq(t, len(*calls), 0, "parses")
		})
	})

	t.Run("evaluate — customExtractors threading (#352)", func(t *testing.T) {
		extractors := gfExtractors{"ffgrep", "target"}
		ffTcc := func() ToolCallContext { return makeTcc(tccTool("ffgrep", obj("target", "/test/project/secret.env"))) }
		tw(t, f, "forwards extractors so a custom-shaped tool is path-gated", func(t *testing.T) {
			// Deny only the cross-cutting `path` surface, so a block can only come from the path gate seeing the extracted path.
			runner, _ := makeGateRunner()
			p, _ := newPipeline(t, makeSurfaceDenyingResolver("path"), makeGateInputs(t), echo, extractors)
			if out := eval(t, p, ffTcc(), runner); out.Action != "block" {
				t.Errorf("outcome %+v", out)
			}
		})
		tw(t, f, "without extractors the custom-shaped tool is not path-gated", func(t *testing.T) {
			runner, _ := makeGateRunner()
			p, _ := newPipeline(t, makeSurfaceDenyingResolver("path"), makeGateInputs(t), echo, nil)
			eq(t, eval(t, p, ffTcc(), runner), gfAllow, "outcome")
		})
	})

	t.Run("evaluate — per-tool path-bearing gate (#502)", func(t *testing.T) {
		perTool := func(r *gfResolver) AccessIntent {
			for _, c := range r.calls {
				if gfIntentSurface(c) == "read" {
					return c
				}
			}
			return nil
		}
		tw(t, f, "emits an access-path intent on the tool-name surface for a path-bearing tool", func(t *testing.T) {
			runner, _ := makeGateRunner()
			resolver := makeResolver(makeCheckResult())
			p, _ := newPipeline(t, resolver, makeGateInputs(t), echo, nil)
			eval(t, p, readTcc(obj("path", "/test/cwd/foo.ts")), runner)
			_, ok := perTool(resolver).(AccessPathAccessIntent)
			eq(t, ok, true, "access-path intent")
		})
		tw(t, f, "keeps a path-bearing tool with no path on the tool intent", func(t *testing.T) {
			runner, _ := makeGateRunner()
			resolver := makeResolver(makeCheckResult())
			p, _ := newPipeline(t, resolver, makeGateInputs(t), echo, nil)
			eval(t, p, readTcc(newObject()), runner)
			_, ok := perTool(resolver).(ToolAccessIntent)
			eq(t, ok, true, "tool intent")
		})
		tw(t, f, "blocks when a per-tool rule matches the symlink-resolved form", func(t *testing.T) {
			// /test/cwd/foo.env is a symlink to /vault/foo.env; the per-tool rule is keyed on the resolved target, which is only
			// reachable via matchValues().
			in := makeGateInputs(t)
			mockRealpath(t, func(p string) (string, error) {
				if p == "/test/cwd/foo.env" {
					return "/vault/foo.env", nil
				}
				return p, nil
			})
			runner, _ := makeGateRunner()
			resolver := makeResolver()
			resolver.fn = func(intent AccessIntent) PermissionCheckResult {
				if it, ok := intent.(AccessPathAccessIntent); ok && it.Surface == "read" {
					for _, v := range it.Path.MatchValues() {
						if v == "/vault/foo.env" {
							return makeCheckResult(ckState("deny", "*.env"))
						}
					}
				}
				return makeCheckResult()
			}
			p, _ := newPipeline(t, resolver, in, echo, nil)
			if out := eval(t, p, readTcc(obj("path", "/test/cwd/foo.env")), runner); out.Action != "block" {
				t.Errorf("outcome %+v", out)
			}
		})
	})
}

func TestGateProducerFailureTwins(t *testing.T) {
	const f = "handlers/gates/gate-producer-failure"
	tw(t, f, "propagates the throw to the fail-closed boundary even when an earlier gate denies", func(t *testing.T) {
		// Before deny pre-emption the pipeline produced gates lazily and stopped at the first block, so a throw in a later producer
		// was unreachable on this call. The eager pass reaches it, and it must still surface to the fail-closed boundary, which
		// blocks, rather than be swallowed into an allow (#452, #899). `path_read` is producer 2's surface for a read tool, ahead of
		// the throwing producer 5.
		runner, _ := makeGateRunner()
		p := NewToolCallGatePipeline(makeSurfaceDenyingResolver("path_read"), makeGateInputs(t), nil, nil)
		p.bashPathGate = func(ToolCallContext, *BashProgram, ScopedPermissionResolver, *PathNormalizer) GateResult {
			panic("gate producer exploded")
		}
		msg, panicked := panicMessage(func() {
			_, _ = p.Evaluate(makeTcc(tccTool("read", obj("path", "notes.txt"))), runner)
		})
		eq(t, panicked, true, "panicked")
		if !strings.Contains(msg, "gate producer exploded") {
			t.Errorf("panic %q", msg)
		}
	})
}
