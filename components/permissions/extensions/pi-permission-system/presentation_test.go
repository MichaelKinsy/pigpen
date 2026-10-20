package pi_permission_system

import "testing"

func lgPayload(kind string, mod func(*PromptRequestFacts)) PromptPayload {
	tool := "read"
	p := PromptPayload{Kind: kind, Request: PromptRequestFacts{Surface: "read", ToolName: &tool, Value: "read"}}
	if mod != nil {
		mod(&p.Request)
	}
	return p
}

func lgFacts(p PromptPayload) string { return lgJSON(RenderReviewLogFacts(p)) }

func TestReviewLogRendererTwins(t *testing.T) {
	const f = "presentation/review-log-renderer"
	tw(t, f, "records the gate surface and the rule that fired", func(t *testing.T) {
		eq(t, lgFacts(lgPayload("tool", func(r *PromptRequestFacts) { r.Surface = "bash"; r.MatchedPattern = sp("rm *") })), `{"surface":"bash","matchedPattern":"rm *"}`, "facts")
	})
	tw(t, f, "omits a fact the ask does not carry rather than writing a null", func(t *testing.T) {
		eq(t, lgFacts(lgPayload("tool", func(r *PromptRequestFacts) { r.Surface = "skill" })), `{"surface":"skill"}`, "facts")
	})
	tw(t, f, "records the unit a wrapper will actually run", func(t *testing.T) {
		got := lgFacts(lgPayload("tool", func(r *PromptRequestFacts) {
			r.Surface, r.MatchedPattern, r.ExecutedUnit = "bash", sp("<indirection-bash-wrapper>"), sp("aws s3 rm s3://bucket")
		}))
		eq(t, got, `{"surface":"bash","matchedPattern":"<indirection-bash-wrapper>","executedUnit":"aws s3 rm s3://bucket"}`, "facts")
	})
	tw(t, f, "records the nested context an offending bash unit ran in", func(t *testing.T) {
		got := lgFacts(lgPayload("tool", func(r *PromptRequestFacts) {
			r.Surface, r.MatchedPattern, r.CommandContext = "bash", sp("*"), sp("command_substitution")
		}))
		eq(t, got, `{"surface":"bash","matchedPattern":"*","commandContext":"command_substitution"}`, "facts")
	})
	tw(t, f, "records the name a shell alias invoked bash under", func(t *testing.T) {
		got := lgFacts(lgPayload("tool", func(r *PromptRequestFacts) {
			r.Surface, r.MatchedPattern, r.InvokedToolName = "bash", sp("*"), sp("exec_command")
		}))
		eq(t, got, `{"surface":"bash","matchedPattern":"*","invokedToolName":"exec_command"}`, "facts")
	})
	tw(t, f, "records the requesting session when the ask was forwarded", func(t *testing.T) {
		got := lgFacts(lgPayload("tool", func(r *PromptRequestFacts) {
			r.Surface, r.MatchedPattern = "bash", sp("*")
			r.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("child-7")}
		}))
		eq(t, got, `{"surface":"bash","matchedPattern":"*","forwarded":true,"requesterSessionId":"child-7"}`, "facts")
	})
	tw(t, f, "marks a local ask with no forwarding fields at all", func(t *testing.T) {
		eq(t, lgFacts(lgPayload("tool", func(r *PromptRequestFacts) { r.Surface = "read" })), `{"surface":"read"}`, "facts")
	})
	tw(t, f, "persists no evidence and no annotations", func(t *testing.T) {
		p := lgPayload("tool", func(r *PromptRequestFacts) { r.Surface, r.MatchedPattern = "external_directory", sp("*") })
		p.Evidence = []PromptEvidence{{Label: "working directory", Text: "/repo"}, {Label: "external path", Text: "/etc/hosts"}}
		p.Annotations = []PromptAnnotation{{Source: "judge", Text: "looks risky"}}
		eq(t, lgFacts(p), `{"surface":"external_directory","matchedPattern":"*"}`, "facts")
	})
	t.Run("a forwarded ask without a session id states only that it was forwarded", func(t *testing.T) {
		eq(t, lgFacts(lgPayload("tool", func(r *PromptRequestFacts) { r.Requester.Forwarded = true })), `{"surface":"read","forwarded":true}`, "facts")
	})
	t.Run("an empty string is a fact, only null is omitted", func(t *testing.T) {
		eq(t, lgFacts(lgPayload("tool", func(r *PromptRequestFacts) { r.MatchedPattern = sp("") })), `{"surface":"read","matchedPattern":""}`, "facts")
	})
}

func TestFactVocabularyTwins(t *testing.T) {
	const f = "presentation/fact-vocabulary"
	of := func(kind, value string, ev ...PromptEvidence) PromptPayload {
		p := lgPayload(kind, func(r *PromptRequestFacts) { r.Value = value })
		p.Evidence = ev
		return p
	}
	ext := func(path string, resolved *string) PromptEvidence {
		return PromptEvidence{Label: "external path", Text: path, Detail: resolved}
	}
	tw(t, f, "flags the decision-relevant value for a single-value ask", func(t *testing.T) {
		eq(t, FlaggedElements(of("path", "/etc/hosts")), []string{"/etc/hosts"}, "flagged")
	})
	tw(t, f, "flags the command for a bash ask", func(t *testing.T) {
		eq(t, FlaggedElements(of("bash", "rm -rf build")), []string{"rm -rf build"}, "flagged")
	})
	tw(t, f, "flags the escaping paths rather than the command for a bash external-directory ask", func(t *testing.T) {
		p := of("bash_external_directory", "diff /etc/hosts ~/.ssh/config", PromptEvidence{Label: "working directory", Text: "/repo"}, ext("/etc/hosts", nil), ext("~/.ssh/config", sp("/home/me/.ssh/config")))
		eq(t, FlaggedElements(p), []string{"/etc/hosts", "~/.ssh/config"}, "flagged")
	})
	tw(t, f, "flags nothing when the value is empty", func(t *testing.T) {
		eq(t, FlaggedElements(of("tool", "")), []string{}, "flagged")
	})
	t.Run("valueLabel", func(t *testing.T) {
		for _, c := range [][2]string{{"bash", "command"}, {"bash_external_directory", "command"}, {"mcp", "target"}, {"tool", "tool"}, {"path", "path"},
			{"external_directory", "path"}, {"skill", "skill"}, {"skill_read", "skill"}} {
			t.Run("labels a "+c[0]+" ask's value "+c[1], func(t *testing.T) { eq(t, ValueLabel(of(c[0], "value")), c[1], c[0]) })
		}
		for _, c := range [][2]string{{"bash", "command"}, {"skill", "skill"}, {"read", "value"}} {
			t.Run("infers a payload-less forwarded ask's label from its "+c[0]+" surface", func(t *testing.T) {
				p := of("forwarded", "value")
				p.Request.Surface = c[0]
				eq(t, ValueLabel(p), c[1], c[0])
			})
		}
	})
	tw(t, f, "labels the escaping paths a bash external-directory ask flags", func(t *testing.T) {
		eq(t, FlaggedElementLabel(of("bash_external_directory", "cmd")), "path", "label")
	})
	t.Run("flaggedElementLabel agrees with the value label for single-value asks", func(t *testing.T) {
		for _, k := range []string{"bash", "mcp", "path", "skill"} {
			p := of(k, "value")
			eq(t, FlaggedElementLabel(p), ValueLabel(p), k)
		}
	})
	t.Run("describeBashCommandContext", func(t *testing.T) {
		for _, c := range [][2]string{{"command_substitution", "command substitution"}, {"process_substitution", "process substitution"}, {"subshell", "subshell"}} {
			t.Run("names a "+c[0]+" context", func(t *testing.T) {
				got, ok := DescribeBashCommandContext(sp(c[0]))
				eq(t, ok, true, "ok")
				eq(t, got, c[1], "label")
			})
		}
	})
	tw(t, f, "names no context for a current-shell command", func(t *testing.T) {
		_, ok := DescribeBashCommandContext(nil)
		eq(t, ok, false, "none")
	})
}
