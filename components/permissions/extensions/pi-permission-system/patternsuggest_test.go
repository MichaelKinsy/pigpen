package pi_permission_system

import "testing"

func TestPatternSuggestTwins(t *testing.T) {
	const f = "presentation/pattern-suggest"
	bash := func(command, want string) func(*testing.T) {
		return func(t *testing.T) { eq(t, SuggestBashPattern(command), want, "pattern") }
	}
	// suggestBashPattern
	// git arity=2: include the subcommand in the prefix.
	tw(t, f, "returns <command> <subcommand> * using the arity table", bash("git status --short", "git status *"))
	// npm run arity=3: prefix covers all three tokens, so a trailing wildcard.
	tw(t, f, "appends trailing * when arity covers all tokens (multi-word script name)", bash("npm run build", "npm run build*"))
	tw(t, f, "returns the exact command when there are no arguments", bash("ls", "ls"))
	// git arity=2, tokens=["git","log"], prefix covers all, so a trailing wildcard.
	tw(t, f, "trims leading and trailing whitespace before lookup", bash("  git log  ", "git log*"))
	tw(t, f, "handles empty string gracefully", bash("", ""))
	tw(t, f, "falls back to first-word prefix for unknown commands", bash("mytool --verbose run", "mytool *"))
	tw(t, f, "returns first-word * for known arity-1 commands with args", bash("rm -rf node_modules", "rm *"))
	tw(t, f, "produces tighter pattern for docker compose than plain docker", bash("docker compose up --build", "docker compose up *"))
	tw(t, f, "strips leading comment lines and suggests based on the actual command", bash("# Check debug logs\nfind /home -path '*debug*' -type f", "find *"))
	tw(t, f, "strips multiple leading comment lines", bash("# Step 1\n# Step 2\ngit status --short", "git status *"))
	tw(t, f, "returns empty for comment-only input", bash("# just a comment", ""))
	tw(t, f, "handles mixed comment and command lines", bash("# description\nrm -rf ./build; echo done", "rm *"))

	// suggestMcpPattern
	mcp := func(target, want string) func(*testing.T) {
		return func(t *testing.T) { eq(t, SuggestMcpPattern(target), want, "pattern") }
	}
	tw(t, f, "suggests server:* for a qualified target (colon-separated)", mcp("exa:search", "exa:*"))
	tw(t, f, "suggests server_* for a munged target (underscore-separated)", mcp("exa_search", "exa_*"))
	tw(t, f, "suggests * for a bare 'mcp' target", mcp("mcp", "*"))
	tw(t, f, "suggests * for a plain tool name with no server prefix", mcp("search", "*"))
	// Qualified names contain ':'; the colon check runs first.
	tw(t, f, "prefers colon over underscore when both are present", mcp("my-server:some_tool", "my-server:*"))

	// suggestSessionPattern
	session := func(surface, value, pattern string) func(*testing.T) {
		return func(t *testing.T) {
			got := SuggestSessionPattern(surface, value)
			eq(t, got.Surface, surface, "surface")
			eq(t, got.Pattern, pattern, "pattern")
		}
	}
	tw(t, f, "returns arity-aware subcommand pattern for multi-word command", session("bash", "git status --short", "git status *"))
	tw(t, f, "returns exact command for single-word bash command", session("bash", "ls", "ls"))
	tw(t, f, "returns mcp surface with server:* for qualified target", session("mcp", "exa:search", "exa:*"))
	tw(t, f, "returns mcp surface with server_* for munged target", session("mcp", "exa_search", "exa_*"))
	tw(t, f, "returns * for bare mcp target", session("mcp", "mcp", "*"))
	tw(t, f, "returns exact skill name as pattern", session("skill", "librarian", "librarian"))
	tw(t, f, "returns * for a path-bearing tool with no resolved path", session("read", "*", "*"))
	tw(t, f, "label shows tool name when pattern is *", func(t *testing.T) {
		eq(t, SuggestSessionPattern("find", "*").Label, `Yes, allow tool "find" for this session`, "label")
	})
	tw(t, f, "returns * for extension tools", session("my_extension_tool", "*", "*"))
	tw(t, f, "bash label includes surface prefix and pattern", func(t *testing.T) {
		eq(t, SuggestSessionPattern("bash", "git status").Label, `Yes, allow bash "git status*" for this session`, "label")
	})
	tw(t, f, "mcp label includes surface prefix and pattern", func(t *testing.T) {
		eq(t, SuggestSessionPattern("mcp", "exa:search").Label, `Yes, allow mcp tool "exa:*" for this session`, "label")
	})
	tw(t, f, "skill label includes surface prefix", func(t *testing.T) {
		eq(t, SuggestSessionPattern("skill", "librarian").Label, `Yes, allow skill "librarian" for this session`, "label")
	})
	tw(t, f, "tool label shows tool name when value is *", func(t *testing.T) {
		eq(t, SuggestSessionPattern("edit", "*").Label, `Yes, allow tool "edit" for this session`, "label")
	})

	// suggestPathSessionPattern
	tw(t, f, "records every pattern and labels a directory pair by its contents glob", func(t *testing.T) {
		eq(t, SuggestPathSessionPattern("ls", []string{"/r/a", "/r/a/*"}), PathSessionSuggestion{
			Surface: "ls", Patterns: []string{"/r/a", "/r/a/*"}, Label: `Yes, allow ls "/r/a/*" for this session`,
		}, "suggestion")
	})
	tw(t, f, "passes the caller-derived pattern through unchanged", func(t *testing.T) {
		eq(t, SuggestPathSessionPattern("edit", []string{"/outside/project/*"}), PathSessionSuggestion{
			Surface: "edit", Patterns: []string{"/outside/project/*"}, Label: `Yes, allow edit "/outside/project/*" for this session`,
		}, "suggestion")
	})
	tw(t, f, "preserves a windows-shaped pattern verbatim", func(t *testing.T) {
		// The derivation belongs to the caller's PathNormalizer; this module must not re-interpret the separators it is handed.
		eq(t, SuggestPathSessionPattern("read", []string{`c:\projects\app\src\*`}), PathSessionSuggestion{
			Surface: "read", Patterns: []string{`c:\projects\app\src\*`}, Label: `Yes, allow read "c:\projects\app\src\*" for this session`,
		}, "suggestion")
	})

	// describeGrantTarget
	g := func(surface, pattern string) ApprovalGrant { return ApprovalGrant{Surface: surface, Pattern: pattern} }
	tw(t, f, "quotes the pattern when there is exactly one grant", func(t *testing.T) {
		eq(t, DescribeGrantTarget([]ApprovalGrant{g("external_directory_write", "/tmp/*")}), `"/tmp/*"`, "target")
	})
	tw(t, f, "counts the grants when there are several", func(t *testing.T) {
		got := DescribeGrantTarget([]ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_read", "/outside/b/*"), g("external_directory_read", "/outside/c/*")})
		eq(t, got, "3 paths", "target")
	})
	tw(t, f, "quotes the shared glob when several grants derive the same pattern", func(t *testing.T) {
		got := DescribeGrantTarget([]ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_read", "/outside/a/*")})
		eq(t, got, `"/outside/a/*"`, "target")
	})
	tw(t, f, "names a directory approval by its contents glob", func(t *testing.T) {
		eq(t, DescribeGrantTarget([]ApprovalGrant{g("external_directory", "/outside/a"), g("external_directory", "/outside/a/*")}), `"/outside/a/*"`, "target")
	})

	// buildDirectionalSessionLabels
	tw(t, f, "names the proven direction and the widened pair for a read", func(t *testing.T) {
		eq(t, BuildDirectionalSessionLabels("read", `"/tmp/*"`), DirectionalSessionLabels{
			SessionLabel: `Yes, allow reads to "/tmp/*" for this session`,
			WidenedLabel: `Yes, allow reads and writes to "/tmp/*" for this session`,
		}, "labels")
	})
	tw(t, f, "names the proven direction and the widened pair for a write", func(t *testing.T) {
		eq(t, BuildDirectionalSessionLabels("write", `"/tmp/*"`), DirectionalSessionLabels{
			SessionLabel: `Yes, allow writes to "/tmp/*" for this session`,
			WidenedLabel: `Yes, allow reads and writes to "/tmp/*" for this session`,
		}, "labels")
	})
	tw(t, f, "carries a multi-grant target through both labels", func(t *testing.T) {
		eq(t, BuildDirectionalSessionLabels("read", "3 paths"), DirectionalSessionLabels{
			SessionLabel: "Yes, allow reads to 3 paths for this session",
			WidenedLabel: "Yes, allow reads and writes to 3 paths for this session",
		}, "labels")
	})

	// buildForwardedScopeLabels
	tw(t, f, "names the requesting subagent on the least-privilege option", func(t *testing.T) {
		eq(t, BuildForwardedScopeLabels(sp("Explore"), []ApprovalGrant{g("bash", "git *")}).SubagentLabel, "This subagent ('Explore') only", "label")
	})
	tw(t, f, "falls back to an anonymous subagent label", func(t *testing.T) {
		eq(t, BuildForwardedScopeLabels(nil, []ApprovalGrant{g("bash", "git *")}).SubagentLabel, "This subagent only", "label")
	})
	tw(t, f, "names the surface and the pattern for a single grant", func(t *testing.T) {
		eq(t, BuildForwardedScopeLabels(sp("Explore"), []ApprovalGrant{g("bash", "git *")}).ServingSessionLabel,
			"The whole session \u2014 allow bash \"git *\" for parent and all subagents", "label")
	})
	tw(t, f, "names every path rather than the first when a grant covers several", func(t *testing.T) {
		got := BuildForwardedScopeLabels(sp("Explore"), []ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("external_directory_read", "/outside/b/*")})
		eq(t, got.ServingSessionLabel, "The whole session \u2014 allow external_directory 2 paths for parent and all subagents", "label")
	})
	tw(t, f, "names the shared family rather than a directional member", func(t *testing.T) {
		// The scope labels are built before the dialog runs, so a direction here could contradict a width the human chooses inside it.
		got := BuildForwardedScopeLabels(sp("Explore"), []ApprovalGrant{g("external_directory_write", "/tmp/*")})
		eq(t, got.ServingSessionLabel, "The whole session \u2014 allow external_directory \"/tmp/*\" for parent and all subagents", "label")
	})
	tw(t, f, "omits the surface when the grants do not share a family", func(t *testing.T) {
		got := BuildForwardedScopeLabels(sp("Explore"), []ApprovalGrant{g("external_directory_read", "/outside/a/*"), g("path_write", "/outside/b/*")})
		eq(t, got.ServingSessionLabel, "The whole session \u2014 allow 2 paths for parent and all subagents", "label")
	})
}
