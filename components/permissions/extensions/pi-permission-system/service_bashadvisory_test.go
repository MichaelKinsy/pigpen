package pi_permission_system

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Twins of test/service/bash-advisory-check.test.ts.

// advisoryResolver is makeBashResolver: resolve dispatches on the bash command text, so a test can give each decomposed unit its own
// decision. calls records every intent.
type advisoryResolver struct {
	byCommand map[string]PermissionCheckResult
	fallback  PermissionCheckResult
	calls     []AccessIntent
}

func (r *advisoryResolver) Resolve(intent AccessIntent) PermissionCheckResult {
	r.calls = append(r.calls, intent)
	command, ok := "", false
	switch it := intent.(type) {
	case ToolAccessIntent:
		if it.Surface == "bash" {
			command, _ = ToRecord(it.Input).str("command")
			ok = true
		}
	case BashCommandAccessIntent:
		command, ok = it.Command, true
	}
	if ok {
		if result, found := r.byCommand[command]; found {
			return result
		}
	}
	return r.fallback
}

func makeBashResolver(byCommand map[string]PermissionCheckResult) *advisoryResolver {
	return &advisoryResolver{byCommand: byCommand, fallback: makeCheckResult(func(c *PermissionCheckResult) { c.ToolName = "bash" })}
}

func advResult(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
	return makeCheckResult(append([]func(*PermissionCheckResult){func(c *PermissionCheckResult) { c.State, c.ToolName = state, "bash" }}, mut...)...)
}

func withPattern(p string) func(*PermissionCheckResult) {
	return func(c *PermissionCheckResult) { c.MatchedPattern = sp(p) }
}

func TestBashAdvisoryCheckTwins(t *testing.T) {
	const f = "service/bash-advisory-check"
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	tw(t, f, "resolves the whole command as a single bash tool intent", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		defer bashparse.ResetWarmBashParser()
		r := makeBashResolver(nil)
		ResolveBashAdvisoryCheck("cd /repo && npm install x", sp("my-agent"), r)
		eq(t, len(r.calls), 1, "one resolve")
		it, ok := r.calls[0].(ToolAccessIntent)
		eq(t, ok, true, "tool intent")
		eq(t, it.Surface, "bash", "surface")
		eq(t, marshalJSON(it.Input.(*jsObject), ""), `{"command":"cd /repo && npm install x"}`, "input")
		eq(t, *it.AgentName, "my-agent", "agent")
	})

	warm := func(t *testing.T) {
		t.Helper()
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
	}

	tw(t, f, "decomposes a chained command and returns the most-restrictive unit", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{
			"cd /repo":      advResult("allow"),
			"npm install x": advResult("deny", withPattern("npm *")),
		})
		result := ResolveBashAdvisoryCheck("cd /repo && npm install x", nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, str(result.MatchedPattern), "npm *", "pattern")
		// each unit is evaluated on the bash surface
		found := false
		for _, c := range r.calls {
			if it, ok := c.(BashCommandAccessIntent); ok && it.Command == "npm install x" {
				found = true
				eq(t, it, bashIntent("npm install x", nil, nil), "intent")
			}
		}
		eq(t, found, true, "the unit was resolved as a bash-command intent")
	})
	tw(t, f, "floors an opaque wrapper allow to ask", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{`bash -c "rm -rf /"`: advResult("allow")})
		result := ResolveBashAdvisoryCheck(`bash -c "rm -rf /"`, nil, r)
		eq(t, result.State, "ask", "state")
		eq(t, str(result.MatchedPattern), "<opaque-bash-wrapper>", "pattern")
	})
	// The advisory answer must never be weaker than the gate's, and it reaches that by delegating to the same ResolveBashCommandCheck
	// (#309): these pin that the delegation carries #803's exemption rather than the advisory path growing a branch of its own.
	tw(t, f, "answers a transparent wrapper by the inner command's rule", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{
			"xargs grep -l foo": advResult("allow", func(c *PermissionCheckResult) { c.Command, c.MatchedPattern = sp("xargs grep -l foo"), sp("*") }),
			"grep -l foo":       advResult("allow", func(c *PermissionCheckResult) { c.Command, c.MatchedPattern = sp("grep -l foo"), sp("grep *") }),
		})
		result := ResolveBashAdvisoryCheck("xargs grep -l foo", nil, r)
		eq(t, result.State, "allow", "state")
		eq(t, str(result.MatchedPattern), "grep *", "pattern")
		eq(t, str(result.Command), "xargs grep -l foo", "command")
	})
	tw(t, f, "still floors an indirection wrapper running a mutator", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{"xargs rm -rf": advResult("allow")})
		result := ResolveBashAdvisoryCheck("xargs rm -rf", nil, r)
		eq(t, result.State, "ask", "state")
		eq(t, str(result.MatchedPattern), "<indirection-bash-wrapper>", "pattern")
	})
	tw(t, f, "floors a command whose parse could not be resolved", func(t *testing.T) {
		warm(t)
		// valid bash the grammar cannot parse (a heredoc redirect with `2>&1` and a pipe): the advisory path shares
		// ResolveBashCommandCheck, so it must not answer weaker than the gate (#309, #840)
		result := ResolveBashAdvisoryCheck("git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG", nil, makeBashResolver(nil))
		eq(t, result.State, "ask", "state")
		eq(t, str(result.MatchedPattern), "<unparsed-bash-subtree>", "pattern")
	})
	tw(t, f, "reports the deny covering a command the parse dropped (#875)", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{"rm -rf /tmp/x": advResult("deny", withPattern("rm -rf *"))})
		result := ResolveBashAdvisoryCheck("git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG", nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, str(result.MatchedPattern), "rm -rf *", "pattern")
	})
	tw(t, f, "reports the deny covering a command after a heredoc the grammar cannot parse", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{"rm -rf /tmp/x": advResult("deny", withPattern("rm -rf *"))})
		result := ResolveBashAdvisoryCheck("cat <<EOF ; rm -rf /tmp/x\nb\nEOF", nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, str(result.MatchedPattern), "rm -rf *", "pattern")
	})
	tw(t, f, "fails closed for a non-empty command that parses to zero units", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(nil)
		result := ResolveBashAdvisoryCheck("> out.txt", nil, r)
		eq(t, result.State, "ask", "state")
		eq(t, str(result.MatchedPattern), "<unparseable-bash-command>", "pattern")
		// the whole command is resolved once, to see whether a deny covers it
		eq(t, len(r.calls), 1, "one resolve")
	})
	tw(t, f, "reports the explicit deny for an unparseable command covered by a deny rule", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{"> out.txt": advResult("deny", withPattern("> *"))})
		result := ResolveBashAdvisoryCheck("> out.txt", nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, str(result.MatchedPattern), "> *", "pattern")
	})
	tw(t, f, "evaluates a nested command inside a substitution", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{
			"echo $(rm -rf /)": advResult("allow"),
			"rm -rf /":         advResult("deny", withPattern("rm *")),
		})
		result := ResolveBashAdvisoryCheck("echo $(rm -rf /)", nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, result.CommandContext, BashContextCommandSubstitution, "context")
	})
	tw(t, f, "evaluates a nested command hosted in a redirect target (#741)", func(t *testing.T) {
		warm(t)
		r := makeBashResolver(map[string]PermissionCheckResult{
			`echo "hello world"`: advResult("allow", withPattern("echo *")),
			"rm *.txt":           advResult("deny", withPattern("rm *")),
		})
		result := ResolveBashAdvisoryCheck(`echo "hello world" > $(rm *.txt)`, nil, r)
		eq(t, result.State, "deny", "state")
		eq(t, str(result.MatchedPattern), "rm *", "pattern")
		eq(t, result.CommandContext, BashContextCommandSubstitution, "context")
	})
}
