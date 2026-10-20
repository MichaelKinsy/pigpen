package pi_permission_system

import "testing"

// Twins of test/access-intent/{tool-kind,effect,tool-input-path}.test.ts.

func TestToolKindTwins(t *testing.T) {
	const f = "access-intent/tool-kind"
	tw(t, f, "classifies bash", func(t *testing.T) { eqv(t, ClassifyToolKind("bash"), ToolKindBash) })
	tw(t, f, "classifies mcp", func(t *testing.T) { eqv(t, ClassifyToolKind("mcp"), ToolKindMcp) })
	tw(t, f, "classifies skill", func(t *testing.T) { eqv(t, ClassifyToolKind("skill"), ToolKindSkill) })
	tw(t, f, "classifies every path-bearing built-in tool as path", func(t *testing.T) {
		for tool := range PathBearingTools {
			eqv(t, ClassifyToolKind(tool), ToolKindPath)
		}
	})
	tw(t, f, "classifies an arbitrary extension tool as extension", func(t *testing.T) {
		eqv(t, ClassifyToolKind("task"), ToolKindExtension)
		eqv(t, ClassifyToolKind("third_party_tool"), ToolKindExtension)
	})
	tw(t, f, "classifies the special path surfaces as extension", func(t *testing.T) {
		eqv(t, ClassifyToolKind("path"), ToolKindExtension)
		eqv(t, ClassifyToolKind("external_directory"), ToolKindExtension)
	})
	t.Run("Pi MCP tool names", func(t *testing.T) {
		for _, n := range []string{"mcp__a__b", "mcp__danger_srv__wipe", "mcp__a__b__c", "mcp__srv__get_x_1a2b3c4d"} {
			eqv(t, ClassifyToolKind(n), ToolKindMcpTool)
		}
		for _, n := range []string{"mcp", "mcp__", "mcp__a", "mcp__a__", "mcp____b", "mcp_a__b", "xmcp__a__b"} {
			eqv(t, ClassifyToolKind(n) == ToolKindMcpTool, false)
		}
		tw(t, f, "trims surrounding whitespace before classifying", func(t *testing.T) {
			eqv(t, ClassifyToolKind(" mcp__a__b "), ToolKindMcpTool)
		})
	})
	tw(t, f, "trims surrounding whitespace before classifying", func(t *testing.T) {
		eqv(t, ClassifyToolKind(" bash "), ToolKindBash)
		eqv(t, ClassifyToolKind("\tmcp\n"), ToolKindMcp)
		eqv(t, ClassifyToolKind("  read  "), ToolKindPath)
	})

	tw(t, f, "is true when the tool itself is mcp", func(t *testing.T) { eqv(t, IsMcpCheck(McpKindFields{"mcp", "tool"}), true) })
	tw(t, f, "is true when the winning rule matched on the mcp surface", func(t *testing.T) {
		eqv(t, IsMcpCheck(McpKindFields{"some-server:some-tool", "mcp"}), true)
		eqv(t, IsMcpCheck(McpKindFields{"read", "mcp"}), true)
	})
	tw(t, f, "is false for a bash check", func(t *testing.T) { eqv(t, IsMcpCheck(McpKindFields{"bash", "bash"}), false) })
	tw(t, f, "is false for a plain tool check", func(t *testing.T) {
		eqv(t, IsMcpCheck(McpKindFields{"read", "tool"}), false)
		eqv(t, IsMcpCheck(McpKindFields{"task", "default"}), false)
	})
	eqv(t, IsProxyMcpCheck(McpKindFields{"mcp", "tool"}), true)
	eqv(t, IsProxyMcpCheck(McpKindFields{"mcp__a__b", "tool"}), false)

	execAlias := ShellToolsConfig{"exec_command": {CommandArgument: "cmd", WorkdirArgument: "workdir"}}
	inv := func(t *testing.T, tool, input string, a ShellToolsConfig) *ShellInvocation {
		return ResolveShellInvocation(tool, jo(t, input), a)
	}
	same := func(t *testing.T, got *ShellInvocation, cmd string, workdir *string) {
		t.Helper()
		if got == nil {
			t.Fatal("got null")
		}
		eqv(t, got.Command, cmd)
		eqv(t, derefOr(got.Workdir, "<undef>"), derefOr(workdir, "<undef>"))
	}
	t.Run("native bash", func(t *testing.T) {
		tw(t, f, "extracts the command with no workdir", func(t *testing.T) { same(t, inv(t, "bash", `{"command":"git status"}`, nil), "git status", nil) })
		tw(t, f, "trims the command", func(t *testing.T) { same(t, inv(t, "bash", `{"command":"  git status  "}`, nil), "git status", nil) })
		tw(t, f, "yields an empty command when absent or non-string", func(t *testing.T) {
			same(t, inv(t, "bash", `{}`, nil), "", nil)
			same(t, inv(t, "bash", `{"command":42}`, nil), "", nil)
		})
		tw(t, f, "resolves regardless of the shellTools map", func(t *testing.T) { same(t, inv(t, "bash", `{"command":"ls"}`, execAlias), "ls", nil) })
	})
	t.Run("aliased shell tool", func(t *testing.T) {
		tw(t, f, "extracts command and workdir from the mapped arguments", func(t *testing.T) {
			same(t, inv(t, "exec_command", `{"cmd":"npm install","workdir":"/etc"}`, execAlias), "npm install", ptr("/etc"))
		})
		tw(t, f, "omits workdir when the alias declares no workdirArgument", func(t *testing.T) {
			a := ShellToolsConfig{"exec_command": {CommandArgument: "cmd"}}
			same(t, inv(t, "exec_command", `{"cmd":"npm install","workdir":"/etc"}`, a), "npm install", nil)
		})
		tw(t, f, "omits workdir when the mapped workdir argument is absent", func(t *testing.T) {
			same(t, inv(t, "exec_command", `{"cmd":"npm install"}`, execAlias), "npm install", nil)
		})
		tw(t, f, "yields an empty command when the mapped command argument is absent", func(t *testing.T) {
			same(t, inv(t, "exec_command", `{"workdir":"/etc"}`, execAlias), "", ptr("/etc"))
		})
		tw(t, f, "trims the extracted command and workdir", func(t *testing.T) {
			same(t, inv(t, "exec_command", `{"cmd":"  npm install  ","workdir":"  /etc  "}`, execAlias), "npm install", ptr("/etc"))
		})
	})
	t.Run("non-shell tools", func(t *testing.T) {
		tw(t, f, "returns null for an unaliased extension tool", func(t *testing.T) {
			eqv(t, inv(t, "exec_command", `{"cmd":"npm install"}`, nil) == nil, true)
			eqv(t, inv(t, "read", `{"path":"a.txt"}`, execAlias) == nil, true)
		})
		tw(t, f, "returns null when the map names a different tool", func(t *testing.T) {
			eqv(t, inv(t, "other_tool", `{"cmd":"npm install"}`, execAlias) == nil, true)
		})
		// A plain JS object inherits Object.prototype: `aliases["constructor"]` is truthy.
		same(t, inv(t, "constructor", `{"undefined":" x "}`, execAlias), "x", nil)
	})
}

func TestEffectTwins(t *testing.T) {
	const f = "access-intent/effect"
	syntaxRead := TokenEffect{EffectRead, SourceSyntax}
	coreRead := TokenEffect{EffectRead, SourceCore}
	syntaxWrite := TokenEffect{EffectWrite, SourceSyntax}
	retracted := TokenEffect{EffectUnproven, SourceRetracted}
	tw(t, f, "is the fail-closed base case, unproven on both fields", func(t *testing.T) {
		eqv(t, UnprovenEffect, TokenEffect{EffectUnproven, SourceUnproven})
	})
	t.Run("when the two attributions agree", func(t *testing.T) {
		tw(t, f, "keeps the effect and the first attribution's source", func(t *testing.T) { eqv(t, MergeTokenEffects(syntaxRead, coreRead), syntaxRead) })
		tw(t, f, "keeps the first source in the other order too", func(t *testing.T) { eqv(t, MergeTokenEffects(coreRead, syntaxRead), coreRead) })
		tw(t, f, "keeps a write proven twice", func(t *testing.T) { eqv(t, MergeTokenEffects(syntaxWrite, syntaxWrite), syntaxWrite) })
		tw(t, f, "keeps a retraction's blame over a bare unproven", func(t *testing.T) { eqv(t, MergeTokenEffects(retracted, UnprovenEffect), retracted) })
		tw(t, f, "keeps a retraction's blame in either collection order", func(t *testing.T) { eqv(t, MergeTokenEffects(UnprovenEffect, retracted), retracted) })
	})
	t.Run("when the two attributions disagree", func(t *testing.T) {
		tw(t, f, "falls to the fail-closed base case for read against write", func(t *testing.T) {
			eqv(t, MergeTokenEffects(syntaxRead, syntaxWrite), UnprovenEffect)
		})
		tw(t, f, "falls to the fail-closed base case for write against read", func(t *testing.T) {
			eqv(t, MergeTokenEffects(syntaxWrite, coreRead), UnprovenEffect)
		})
		tw(t, f, "lets an unproven attribution retract a proven read", func(t *testing.T) {
			eqv(t, MergeTokenEffects(coreRead, UnprovenEffect), UnprovenEffect)
		})
		tw(t, f, "lets a proven read fall to an already-unproven attribution", func(t *testing.T) {
			eqv(t, MergeTokenEffects(UnprovenEffect, coreRead), UnprovenEffect)
		})
		tw(t, f, "discards a retraction's blame when it disagrees with a proof", func(t *testing.T) {
			eqv(t, MergeTokenEffects(retracted, syntaxWrite), UnprovenEffect)
		})
	})
}

type fakeLookup struct {
	tool      string
	extractor ToolAccessExtractor
	origin    RegistrationOrigin
}

func (l fakeLookup) Resolve(name string) *ResolvedToolAccessExtractor {
	if name == l.tool {
		return &ResolvedToolAccessExtractor{Extractor: l.extractor, Origin: l.origin}
	}
	return nil
}

func TestToolInputPathTwins(t *testing.T) {
	const f = "access-intent/tool-input-path"
	tw(t, f, "returns path for a path-bearing tool", func(t *testing.T) {
		p, ok := GetPathBearingToolPath("read", jo(t, `{"path":"/src/foo.ts"}`))
		eqv(t, ok, true)
		eqv(t, p, "/src/foo.ts")
	})
	tw(t, f, "returns null for a non-path-bearing tool", func(t *testing.T) {
		for _, tool := range []string{"bash", "mcp", "task"} {
			_, ok := GetPathBearingToolPath(tool, jo(t, `{"path":"/src/foo.ts"}`))
			eqv(t, ok, false)
		}
	})
	tw(t, f, "returns null when input has no path", func(t *testing.T) {
		for _, in := range []any{jo(t, `{}`), jo(t, `{"path":""}`), nil} {
			_, ok := GetPathBearingToolPath("read", in)
			eqv(t, ok, false)
		}
	})
	r := func(t *testing.T, tool, in string, ex ToolAccessExtractorLookup) ToolInputPathResult {
		var input any
		if in != "null" {
			input = jo(t, in)
		}
		return GetToolInputPath(tool, input, ex)
	}
	want := func(t *testing.T, got ToolInputPathResult, path string, has bool, src ToolPathSource) {
		t.Helper()
		eqv(t, got, ToolInputPathResult{Path: path, HasPath: has, Source: src})
	}
	byTarget := func(in *jsObject) (string, bool) { s, ok := in.str("target"); return s, ok }
	tw(t, f, "returns input.path for a built-in path-bearing tool", func(t *testing.T) {
		want(t, r(t, "read", `{"path":"/src/foo.ts"}`, nil), "/src/foo.ts", true, ToolPathConvention)
		want(t, r(t, "write", `{"path":"/src/bar.ts"}`, nil), "/src/bar.ts", true, ToolPathConvention)
	})
	tw(t, f, "returns null for bash", func(t *testing.T) {
		want(t, r(t, "bash", `{"path":"/src/foo.ts"}`, nil), "", false, ToolPathConvention)
	})
	tw(t, f, "returns the MCP arguments.path for an mcp call", func(t *testing.T) {
		want(t, r(t, "mcp", `{"arguments":{"path":"/etc/hosts"}}`, nil), "/etc/hosts", true, ToolPathConvention)
	})
	tw(t, f, "returns null for an mcp call without an arguments.path", func(t *testing.T) {
		eqv(t, r(t, "mcp", `{"arguments":{"query":"x"}}`, nil).HasPath, false)
		eqv(t, r(t, "mcp", `{}`, nil).HasPath, false)
	})
	tw(t, f, "returns a Pi MCP tool's top-level path, not arguments.path", func(t *testing.T) {
		want(t, r(t, "mcp__fs__read_file", `{"path":"/etc/hosts"}`, nil), "/etc/hosts", true, ToolPathConvention)
		eqv(t, r(t, "mcp__fs__read_file", `{"arguments":{"path":"/etc/hosts"}}`, nil).HasPath, false)
	})
	tw(t, f, "defaults to input.path for an unregistered extension tool", func(t *testing.T) {
		want(t, r(t, "my-ext", `{"path":"/work/file.txt"}`, nil), "/work/file.txt", true, ToolPathConvention)
	})
	tw(t, f, "returns null for an extension tool without a path", func(t *testing.T) {
		eqv(t, r(t, "my-ext", `{"other":true}`, nil).HasPath, false)
		eqv(t, r(t, "my-ext", `{"path":""}`, nil).HasPath, false)
		eqv(t, r(t, "my-ext", `null`, nil).HasPath, false)
	})
	tw(t, f, "uses a registered extractor's path over the default convention", func(t *testing.T) {
		ex := fakeLookup{"ffgrep", byTarget, RegistrationLocal}
		want(t, r(t, "ffgrep", `{"target":"/etc/passwd"}`, ex), "/etc/passwd", true, ToolPathLocalExtractor)
	})
	tw(t, f, "returns null when a registered extractor declines", func(t *testing.T) {
		ex := fakeLookup{"ffgrep", func(*jsObject) (string, bool) { return "", false }, RegistrationLocal}
		eqv(t, r(t, "ffgrep", `{"target":"x"}`, ex).HasPath, false)
	})
	t.Run("path source", func(t *testing.T) {
		tw(t, f, "names an inherited extractor as the source of its path", func(t *testing.T) {
			ex := fakeLookup{"ffgrep", byTarget, RegistrationInherited}
			want(t, r(t, "ffgrep", `{"target":"/etc/passwd"}`, ex), "/etc/passwd", true, ToolPathInheritedExtractor)
		})
		tw(t, f, "falls back to the convention when no extractor answers", func(t *testing.T) {
			ex := fakeLookup{"other-tool", func(*jsObject) (string, bool) { return "/unused", true }, RegistrationLocal}
			want(t, r(t, "ffgrep", `{"path":"/work/file.txt"}`, ex), "/work/file.txt", true, ToolPathConvention)
		})
	})
}
