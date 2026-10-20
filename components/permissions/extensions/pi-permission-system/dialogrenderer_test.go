package pi_permission_system

import (
	"math"
	"strings"
	"testing"
)

// ppPayload is makePromptPayload (test/helpers/prompt-details-fixtures.ts): a minimal complete payload, with the fixture's defaults
// (kind tool, a read request, no evidence).
func ppPayload(kind string, mod func(*PromptPayload)) PromptPayload {
	tool := "read"
	p := PromptPayload{
		Kind: kind,
		Request: PromptRequestFacts{
			Surface: "read", ToolName: &tool, Value: "read",
		},
		Evidence:    []PromptEvidence{},
		Annotations: []PromptAnnotation{},
	}
	if mod != nil {
		mod(&p)
	}
	return p
}

// ppRender is render() of the dialog-renderer test: the lines of the fixture payload at a complete 200-wide view.
func ppRender(kind string, mod func(*PromptPayload)) []string {
	return RenderPromptDialog(ppPayload(kind, mod), CompleteViewBudget(200), nil).Lines
}

func evid(label, text string, detail *string) PromptEvidence {
	return PromptEvidence{Label: label, Text: text, Detail: detail}
}

func TestDialogRendererTwins(t *testing.T) {
	const f = "presentation/dialog-renderer"
	S := func(s ...string) []string { return s }
	req := func(p *PromptPayload, surface string, tool *string, value string, matched *string) {
		p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = surface, tool, value, matched
	}

	tw(t, f, "renders a bash ask as one aligned fact per line", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) {
			p.Request.Requester = PromptRequester{AgentName: sp("scout")}
			req(p, "bash", sp("bash"), "cat /etc/hosts", sp("*"))
		})
		eq(t, got, S("agent   : scout", "tool    : bash", "rule    : *", "command : cat /etc/hosts"), "lines")
	})
	tw(t, f, "names the gate surface when it differs from the tool", func(t *testing.T) {
		got := ppRender("external_directory", func(p *PromptPayload) { req(p, "external_directory", sp("write"), "/etc/hosts", sp("*")) })
		eq(t, got, S("tool    : write", "surface : external_directory", "rule    : *", "path    : /etc/hosts"), "lines")
	})
	tw(t, f, "omits an agent line for an unnamed local requester, and a surface the value label already names", func(t *testing.T) {
		got := ppRender("path", func(p *PromptPayload) { req(p, "path", sp("read"), "/tmp/x", nil) })
		eq(t, got, S("tool : read", "path : /tmp/x"), "lines")
	})
	tw(t, f, "names a directional path surface, which the value label no longer states", func(t *testing.T) {
		got := ppRender("path", func(p *PromptPayload) { req(p, "path_read", sp("read"), "/tmp/x", nil) })
		eq(t, got, S("tool    : read", "surface : path_read", "path    : /tmp/x"), "lines")
	})
	tw(t, f, "omits the value line when the tool line already carries it", func(t *testing.T) {
		got := ppRender("tool", func(p *PromptPayload) { req(p, "fetch", sp("fetch"), "fetch", sp("*")) })
		eq(t, got, S("tool : fetch", "rule : *"), "lines")
	})
	tw(t, f, "labels an MCP ask's value as the target", func(t *testing.T) {
		got := ppRender("mcp", func(p *PromptPayload) {
			req(p, "mcp__github__create_issue", sp("mcp__github__create_issue"), "github:create_issue", sp("mcp__github__*"))
		})
		eq(t, got, S("tool   : mcp__github__create_issue", "rule   : mcp__github__*", "target : github:create_issue"), "lines")
	})
	tw(t, f, "renders a skill ask without repeating the surface", func(t *testing.T) {
		got := ppRender("skill", func(p *PromptPayload) { req(p, "skill", nil, "deploy", nil) })
		eq(t, got, S("skill : deploy"), "lines")
	})
	tw(t, f, "renders the executed unit of an unstrippable wrapper", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), "xargs grep foo", sp("<indirection-bash-wrapper>"))
			p.Request.ExecutedUnit = sp("grep foo")
		})
		eq(t, got, S("tool    : bash", "rule    : <indirection-bash-wrapper>", "command : xargs grep foo", "runs    : grep foo"), "lines")
	})
	tw(t, f, "names the nested context the offending unit ran in", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), "rm -rf /tmp/x", sp("rm *"))
			p.Request.CommandContext = sp("command_substitution")
		})
		eq(t, got, S("tool    : bash", "rule    : rm *", "command : rm -rf /tmp/x", "context : command substitution"), "lines")
	})
	tw(t, f, "names the invoked tool when a shell alias re-exposes bash", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), "ls", sp("*"))
			p.Request.InvokedToolName = sp("exec_command")
		})
		eq(t, got, S("tool    : bash (invoked as exec_command)", "rule    : *", "command : ls"), "lines")
	})

	tw(t, f, "renders each entry under the core, sharing the label column", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), "rm -rf build", sp("rm *"))
			p.Evidence = []PromptEvidence{evid("full command", "npm run clean && rm -rf build", nil)}
		})
		eq(t, got, S("tool         : bash", "rule         : rm *", "command      : rm -rf build", "full command : npm run clean && rm -rf build"), "lines")
	})
	tw(t, f, "keeps an entry's detail on the entry's own line", func(t *testing.T) {
		got := ppRender("bash_external_directory", func(p *PromptPayload) {
			req(p, "external_directory", sp("bash"), "cat /etc/hosts /tmp/x", sp("*"))
			p.Evidence = []PromptEvidence{evid("working directory", "/repo", nil), evid("external path", "/etc/hosts", sp("/private/etc/hosts")), evid("external path", "/tmp/x", nil)}
		})
		eq(t, got, S(
			"tool              : bash",
			"surface           : external_directory",
			"rule              : *",
			"command           : cat /etc/hosts /tmp/x",
			"working directory : /repo",
			"external path     : /etc/hosts \u2192 /private/etc/hosts",
			"external path     : /tmp/x"), "lines")
	})
	tw(t, f, "renders a skill read's path", func(t *testing.T) {
		got := ppRender("skill_read", func(p *PromptPayload) {
			req(p, "skill", nil, "deploy", nil)
			p.Evidence = []PromptEvidence{evid("read path", "/skills/deploy/SKILL.md", nil)}
		})
		eq(t, got, S("skill     : deploy", "read path : /skills/deploy/SKILL.md"), "lines")
	})

	narrow := DialogBudget{RenderBudget: RenderBudget{MaxRows: math.Inf(1), FieldMaxWidth: 12}, Width: 200}
	bashAsk := func(command string, fullCommand *string) PromptPayload {
		return ppPayload("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), command, sp("*"))
			if fullCommand != nil {
				p.Evidence = []PromptEvidence{evid("full command", *fullCommand, nil)}
			}
		})
	}
	tw(t, f, "clips a core field and marks it, without stating a count", func(t *testing.T) {
		view := RenderPromptDialog(bashAsk("echo the quick brown fox", nil), narrow, nil)
		eq(t, view.Lines, S("tool    : bash", "rule    : *", "command : echo the qui\u2026"), "lines")
		eq(t, view.Elided, true, "elided")
	})
	tw(t, f, "clips an evidence field the same way", func(t *testing.T) {
		view := RenderPromptDialog(bashAsk("echo hi", sp("echo hi && echo there")), narrow, nil)
		eq(t, view.Lines, S("tool         : bash", "rule         : *", "command      : echo hi", "full command : echo hi && e\u2026"), "lines")
		eq(t, view.Elided, true, "elided")
	})
	tw(t, f, "reproduces the field verbatim under the complete view", func(t *testing.T) {
		view := RenderPromptDialog(bashAsk("echo the quick brown fox", nil), CompleteViewBudget(200), nil)
		eq(t, inStrings(view.Lines, "command : echo the quick brown fox"), true, "contains")
		eq(t, view.Elided, false, "elided")
	})
	tw(t, f, "indents a multi-line field under its label", func(t *testing.T) {
		view := RenderPromptDialog(bashAsk("cat <<'EOF'\nfirst line\nsecond line\nEOF", nil), CompleteViewBudget(200), nil)
		eq(t, view.Lines, S("tool    : bash", "rule    : *", "command : cat <<'EOF'", "          first line", "          second line", "          EOF"), "lines")
	})

	askWithEvidence := func(count int) PromptPayload {
		return ppPayload("tool", func(p *PromptPayload) {
			req(p, "fetch", sp("fetch"), "fetch", sp("*"))
			for i := 0; i < count; i++ {
				p.Evidence = append(p.Evidence, evid("input", "entry "+jsNumber(float64(i)), nil))
			}
		})
	}
	budget := func(maxRows, fieldMax float64, width int) DialogBudget {
		return DialogBudget{RenderBudget: RenderBudget{MaxRows: maxRows, FieldMaxWidth: fieldMax}, Width: width}
	}
	tw(t, f, "drops evidence that does not fit and marks the drop", func(t *testing.T) {
		view := RenderPromptDialog(askWithEvidence(6), budget(5, 200, 200), nil)
		eq(t, view.Lines, S("tool  : fetch", "rule  : *", "input : entry 0", "input : entry 1", "\u2026"), "lines")
		eq(t, view.Elided, true, "elided")
	})
	tw(t, f, "marks nothing when every entry fits", func(t *testing.T) {
		view := RenderPromptDialog(askWithEvidence(2), budget(5, 200, 200), nil)
		eq(t, len(view.Lines), 4, "len")
		eq(t, view.Elided, false, "elided")
	})
	tw(t, f, "counts rows after wrapping to the width", func(t *testing.T) {
		payload := ppPayload("tool", func(p *PromptPayload) {
			req(p, "fetch", sp("fetch"), "fetch", sp("*"))
			p.Evidence = []PromptEvidence{evid("input", strings.Repeat("a", 60), nil)}
		})
		// The same entry is one row at a wide width and four once wrapped, so the identical budget admits it only in the first case.
		wide := RenderPromptDialog(payload, budget(5, 200, 200), nil)
		eq(t, wide, DialogView{Lines: S("tool  : fetch", "rule  : *", "input : "+strings.Repeat("a", 60)), Elided: false}, "wide")
		wrapped := RenderPromptDialog(payload, budget(5, 200, 20), nil)
		eq(t, wrapped.Lines, S("tool  : fetch", "rule  : *", "\u2026"), "lines")
		eq(t, wrapped.Elided, true, "elided")
	})
	tw(t, f, "renders the whole core when it alone exceeds the budget", func(t *testing.T) {
		view := RenderPromptDialog(askWithEvidence(3), budget(1, 200, 200), nil)
		eq(t, view.Lines, S("tool  : fetch", "rule  : *"), "lines")
		eq(t, view.Elided, true, "elided")
	})
	hereString := func() string {
		body := strings.TrimSuffix(strings.Repeat("- a finding line about some module in the codebase\n", 200), "\n")
		return "@'\n" + body + "\n'@ | Out-File -FilePath report.md"
	}
	tw(t, f, "bounds the reported forwarded here-string ask (#710)", func(t *testing.T) {
		command := hereString()
		b := budget(24, 400, 120)
		view := RenderPromptDialog(ppPayload("forwarded", func(p *PromptPayload) {
			p.Request.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("abc123")}
			req(p, "bash", nil, command, nil)
			p.Evidence = []PromptEvidence{evid("requested", "Subagent 'scout' requested bash command '"+command+"'.", nil)}
		}), b, nil)
		if float64(len(view.Lines)) > b.MaxRows {
			t.Fatalf("%d lines exceed %v rows", len(view.Lines), b.MaxRows)
		}
		eq(t, view.Lines[0], "subagent  : scout \u00b7 session abc123", "line 0")
		eq(t, view.Lines[1], "surface   : bash", "line 1")
		eq(t, view.Lines[2], "command   : @'", "line 2")
		eq(t, view.Elided, true, "elided")
	})
	tw(t, f, "bounds the same forwarded here-string ask carrying the child's own payload (#745)", func(t *testing.T) {
		command := hereString()
		b := budget(24, 400, 120)
		view := RenderPromptDialog(ppPayload("bash", func(p *PromptPayload) {
			p.Request.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("abc123")}
			req(p, "bash", sp("bash"), command, sp("*"))
			p.Evidence = []PromptEvidence{evid("full command", command, nil), evid("working directory", "/repo", nil)}
		}), b, nil)
		if float64(len(view.Lines)) > b.MaxRows {
			t.Fatalf("%d lines exceed %v rows", len(view.Lines), b.MaxRows)
		}
		eq(t, view.Elided, true, "elided")
	})

	mark := func(text string) string { return "[" + text + "]" }
	tw(t, f, "paints the flagged value and its whole-token occurrences in evidence", func(t *testing.T) {
		view := RenderPromptDialog(ppPayload("bash", func(p *PromptPayload) {
			req(p, "bash", sp("bash"), "ls", sp("*"))
			p.Evidence = []PromptEvidence{evid("full command", "lsof | ls && /usr/bin/lsblk", nil)}
		}), CompleteViewBudget(200), mark)
		eq(t, view.Lines, S("tool         : bash", "rule         : *", "command      : [ls]", "full command : lsof | [ls] && /usr/bin/lsblk"), "lines")
	})
	tw(t, f, "paints each escaping path rather than the command that referenced them", func(t *testing.T) {
		view := RenderPromptDialog(ppPayload("bash_external_directory", func(p *PromptPayload) {
			req(p, "external_directory", sp("bash"), "cat /etc/hosts /etc/hostsbackup", sp("*"))
			p.Evidence = []PromptEvidence{evid("working directory", "/repo", nil), evid("external path", "/etc/hosts", nil)}
		}), CompleteViewBudget(200), mark)
		eq(t, view.Lines, S(
			"tool              : bash",
			"surface           : external_directory",
			"rule              : *",
			"command           : cat [/etc/hosts] /etc/hostsbackup",
			"working directory : /repo",
			"external path     : [/etc/hosts]"), "lines")
	})
	tw(t, f, "leaves the text untouched when no paint is supplied", func(t *testing.T) {
		got := ppRender("bash", func(p *PromptPayload) { req(p, "bash", sp("bash"), "ls", sp("*")) })
		eq(t, got, S("tool    : bash", "rule    : *", "command : ls"), "lines")
	})

	tw(t, f, "names the requesting subagent and its session", func(t *testing.T) {
		got := ppRender("forwarded", func(p *PromptPayload) {
			p.Request.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("abc123")}
			req(p, "bash", nil, "cat /etc/hosts", nil)
		})
		eq(t, got, S("subagent : scout \u00b7 session abc123", "surface  : bash", "command  : cat /etc/hosts"), "lines")
	})
	tw(t, f, "renders a version-skewed request that names neither agent nor session", func(t *testing.T) {
		got := ppRender("forwarded", func(p *PromptPayload) {
			p.Request.Requester = PromptRequester{AgentName: sp(""), Forwarded: true, SessionID: sp("")}
			req(p, "read", nil, "/tmp/x", nil)
		})
		eq(t, got, S("subagent : unknown", "surface  : read", "value    : /tmp/x"), "lines")
	})
}
