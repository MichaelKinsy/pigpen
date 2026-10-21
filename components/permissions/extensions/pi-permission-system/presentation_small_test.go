package pi_permission_system

import (
	"strings"
	"testing"
)

const (
	testBEL = "\x07"
	testESC = "\x1b"
)

func TestPromptNotificationTwins(t *testing.T) {
	const f = "presentation/prompt-notification"
	notice := PromptNotice{Title: "pi", Body: "Permission Required"}
	const dash = "\u2014"
	makeRequest := func(mod func(*NoticeRequestFacts)) NoticeRequestFacts {
		r := NoticeRequestFacts{ToolName: sp("bash"), Surface: "bash"}
		if mod != nil {
			mod(&r)
		}
		return r
	}
	named := NotificationSession{Name: sp("refactor-auth"), Cwd: "/w/repo"}
	describe := func(title string, r NoticeRequestFacts, s NotificationSession) PromptNotice {
		return DescribePromptNotice(title, r, s)
	}

	tw(t, f, "writes a bare BEL for bell", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"bell"}, notice), testBEL, "bell")
	})
	tw(t, f, "writes the title ahead of the body into OSC 9's single field", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc9"}, PromptNotice{Title: "the title", Body: "the body"}), testESC+"]9;the title: the body"+testBEL, "osc9")
	})
	tw(t, f, "writes the notice's title and body into OSC 777's two fields", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc777"}, PromptNotice{Title: "the title", Body: "the body"}), testESC+"]777;notify;the title;the body"+testBEL, "osc777")
	})
	tw(t, f, "concatenates the channels in configured order", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc777", "bell"}, notice), testESC+"]777;notify;pi;Permission Required"+testBEL+testBEL, "order")
	})
	tw(t, f, "renders nothing for an empty list", func(t *testing.T) {
		eq(t, RenderPromptNotification(nil, notice), "", "nil")
		eq(t, RenderPromptNotification([]string{}, notice), "", "empty")
	})
	// A control character in a field could terminate the sequence early or start a new one, so none survives into any OSC field.
	hostile := "a" + testBEL + "b" + testESC + "c\x7fd;e"
	tw(t, f, "drops control characters and keeps semicolons in osc9", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc9"}, PromptNotice{Title: hostile, Body: hostile}), testESC+"]9;abcd;e: abcd;e"+testBEL, "osc9")
	})
	tw(t, f, "drops control characters and turns semicolons into colons in the osc777 body", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc777"}, PromptNotice{Title: "pi", Body: hostile}), testESC+"]777;notify;pi;abcd:e"+testBEL, "osc777")
	})
	tw(t, f, "drops control characters and turns semicolons into colons in the osc777 title", func(t *testing.T) {
		eq(t, RenderPromptNotification([]string{"osc777"}, PromptNotice{Title: hostile, Body: "body"}), testESC+"]777;notify;abcd:e;body"+testBEL, "osc777")
	})

	tw(t, f, "names the session", func(t *testing.T) {
		eq(t, describe("Permission Required", makeRequest(nil), named).Title, "pi "+dash+" refactor-auth", "title")
	})
	tw(t, f, "falls back to the working directory's name for an unnamed session", func(t *testing.T) {
		eq(t, describe("Permission Required", makeRequest(nil), NotificationSession{Cwd: "/w/pi-packages"}).Title, "pi "+dash+" pi-packages", "title")
	})
	tw(t, f, "treats an empty session name as unnamed", func(t *testing.T) {
		eq(t, describe("Permission Required", makeRequest(nil), NotificationSession{Name: sp(""), Cwd: "/w/pi-packages"}).Title, "pi "+dash+" pi-packages", "title")
	})
	tw(t, f, "is bare pi when neither a name nor a directory name exists", func(t *testing.T) {
		eq(t, describe("Permission Required", makeRequest(nil), NotificationSession{Cwd: "/"}).Title, "pi", "title")
	})

	tw(t, f, "names the tool after the dialog title", func(t *testing.T) {
		eq(t, describe("Permission Required", makeRequest(nil), named).Body, "Permission Required: bash", "body")
	})
	tw(t, f, "names the tool rather than the surface that gated it", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) { r.ToolName, r.Surface = sp("read"), "path_read" })
		eq(t, describe("Permission Required", r, named).Body, "Permission Required: read", "body")
	})
	tw(t, f, "names the surface when the ask is not tool-shaped", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) { r.ToolName, r.Surface = nil, "skill" })
		eq(t, describe("Permission Required", r, named).Body, "Permission Required: skill", "body")
	})
	tw(t, f, "names a local requesting agent", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) { r.Requester = PromptRequester{AgentName: sp("planner")} })
		eq(t, describe("Permission Required", r, named).Body, "Permission Required: bash (planner)", "body")
	})
	tw(t, f, "names the subagent a forwarded ask came from", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) {
			r.ToolName, r.Surface = sp("read"), "read"
			r.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("child-1")}
		})
		eq(t, describe("Permission Required (Subagent)", r, named).Body, "Permission Required (Subagent): read (scout)", "body")
	})
	tw(t, f, "names the agent alone when the ask carries no tool or surface", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) {
			r.ToolName, r.Surface = nil, ""
			r.Requester = PromptRequester{AgentName: sp("scout"), Forwarded: true}
		})
		eq(t, describe("Permission Required (Subagent)", r, named).Body, "Permission Required (Subagent): scout", "body")
	})
	tw(t, f, "is the dialog title alone when nothing names the ask", func(t *testing.T) {
		r := makeRequest(func(r *NoticeRequestFacts) {
			r.ToolName, r.Surface = nil, ""
			r.Requester = PromptRequester{AgentName: sp(""), Forwarded: true}
		})
		eq(t, describe("Permission Required (Subagent)", r, named).Body, "Permission Required (Subagent)", "body")
	})

	// One request per ask kind, as its gate shapes it; every one carries the decision-relevant value, the matched rule, and the
	// executed unit, none of which a notification history may hold.
	tw(t, f, "holds no request value for a ${kind} ask", func(t *testing.T) {
		cases := []struct {
			kind, surface, body string
			tool                *string
		}{
			{"bash", "bash", "bash", sp("bash")},
			{"mcp", "mcp", "mcp", sp("mcp")},
			{"tool", "write", "write", sp("write")},
			{"path", "path_read", "read", sp("read")},
			{"external_directory", "external_directory_read", "read", sp("read")},
			{"bash_external_directory", "external_directory_write", "bash", sp("bash")},
			{"skill", "skill", "skill", nil},
			{"skill_read", "skill", "read", sp("read")},
			{"forwarded", "bash", "bash", nil},
		}
		for _, c := range cases {
			t.Run("holds no request value for a "+c.kind+" ask", func(t *testing.T) {
				// The notice only ever sees toolName, surface and the requester; the value, rule and unit are not parameters.
				got := describe("Permission Required", makeRequest(func(r *NoticeRequestFacts) { r.ToolName, r.Surface = c.tool, c.surface }), named)
				eq(t, got.Body, "Permission Required: "+c.body, "body")
				if strings.Contains(RenderPromptNotification([]string{"bell", "osc9", "osc777"}, got), "SECRET") {
					t.Fatal("a notification carries a request value")
				}
			})
		}
	})
}

func TestPermissionPromptsTwins(t *testing.T) {
	const f = "presentation/permission-prompts"
	tw(t, f, "mentions missing tool name and pi.getAllTools()", func(t *testing.T) {
		result := FormatMissingToolNameReason()
		eq(t, strings.Contains(result, "no tool name"), true, "no tool name")
		eq(t, strings.Contains(result, "pi.getAllTools()"), true, "getAllTools")
	})
	tw(t, f, "mentions the unknown tool name and lists available tools", func(t *testing.T) {
		result := FormatUnknownToolReason("phantom", []string{"read", "write"})
		for _, want := range []string{"phantom", "read", "write"} {
			eq(t, strings.Contains(result, want), true, want)
		}
	})
	tw(t, f, "includes MCP hint for non-mcp tool names", func(t *testing.T) {
		eq(t, strings.Contains(FormatUnknownToolReason("my-server:tool", []string{"mcp"}), "mcp"), true, "mcp")
	})
	tw(t, f, "omits the proxy MCP hint for a Pi MCP tool name", func(t *testing.T) {
		eq(t, strings.Contains(FormatUnknownToolReason("mcp__srv__gone", nil), "call the registered 'mcp' tool"), false, "hint")
	})
	tw(t, f, "omits MCP hint when tool name is 'mcp'", func(t *testing.T) {
		eq(t, strings.Contains(FormatUnknownToolReason("mcp", nil), "call the registered 'mcp' tool"), false, "hint")
	})
	tw(t, f, "shows 'none' when no tools are registered", func(t *testing.T) {
		eq(t, strings.Contains(FormatUnknownToolReason("ghost", nil), "none"), true, "none")
	})
	tw(t, f, "caps preview at 10 tools and appends ellipsis for longer lists", func(t *testing.T) {
		tools := make([]string, 15)
		for i := range tools {
			tools[i] = "tool" + jsNumber(float64(i))
		}
		result := FormatUnknownToolReason("ghost", tools)
		eq(t, strings.Contains(result, "..."), true, "ellipsis")
		eq(t, strings.Contains(result, "tool10"), false, "capped")
	})
}

func skillEntryFixture(name string) SkillPromptEntry {
	return SkillPromptEntry{
		Name: name, Description: "A skill", Location: "/skills/" + name + "/SKILL.md", State: "ask",
		NormalizedLocation: "/skills/" + name + "/SKILL.md", NormalizedBaseDir: "/skills/" + name,
	}
}

func TestSkillAskPayloadTwins(t *testing.T) {
	const f = "presentation/skill-ask-payload"
	tw(t, f, "makes the skill the decision-relevant value", func(t *testing.T) {
		payload := BuildSkillAskPayload("librarian", sp("my-agent"))
		eq(t, payload.Kind, "skill", "kind")
		eq(t, payload.Request, PromptRequestFacts{
			Requester: PromptRequester{AgentName: sp("my-agent")}, Surface: "skill", Value: "librarian",
		}, "request")
		eq(t, payload.Evidence, []PromptEvidence{}, "evidence")
	})
	tw(t, f, "leaves the requester unnamed when no agent is active", func(t *testing.T) {
		eq(t, BuildSkillAskPayload("librarian", nil).Request.Requester, PromptRequester{}, "requester")
	})
	tw(t, f, "keeps the skill as the value and the path as evidence", func(t *testing.T) {
		payload := BuildSkillPathAskPayload(skillEntryFixture("librarian"), "/skills/librarian/SKILL.md", nil)
		eq(t, payload.Kind, "skill_read", "kind")
		eq(t, payload.Request.Value, "librarian", "value")
		eq(t, payload.Evidence, []PromptEvidence{{Label: "read path", Text: "/skills/librarian/SKILL.md"}}, "evidence")
	})
	tw(t, f, "names the requesting agent on the payload", func(t *testing.T) {
		got := BuildSkillPathAskPayload(skillEntryFixture("librarian"), "/skills/librarian/SKILL.md", sp("my-agent")).Request.Requester.AgentName
		eq(t, got, sp("my-agent"), "agent")
	})
}

func TestPathAskPayloadTwins(t *testing.T) {
	const f = "presentation/path-ask-payload"
	tw(t, f, "carries the typed path as the decision value and the rule that fired", func(t *testing.T) {
		payload := BuildPathAskPayload(PathAskFacts{ToolName: "read", PathValue: "/etc/passwd", AgentName: sp("my-agent"), MatchedPattern: sp("/etc/*"), Surface: "path_read"})
		eq(t, payload.Kind, "path", "kind")
		eq(t, payload.Request, PromptRequestFacts{
			Requester: PromptRequester{AgentName: sp("my-agent")}, Surface: "path_read", ToolName: sp("read"), Value: "/etc/passwd", MatchedPattern: sp("/etc/*"),
		}, "request")
		eq(t, payload.Evidence, []PromptEvidence{}, "evidence")
	})
	tw(t, f, "discloses the file a rewritten spelling resolves to", func(t *testing.T) {
		got := BuildPathAskPayload(PathAskFacts{ToolName: "read", PathValue: "/x/d'x.txt", ResolvedPath: sp("/x/d\u2019x.txt"), Surface: "path_read"}).Evidence
		eq(t, got, []PromptEvidence{{Label: "resolves to", Text: "/x/d\u2019x.txt"}}, "evidence")
	})
	tw(t, f, "leaves the requester unnamed when no agent is active", func(t *testing.T) {
		got := BuildPathAskPayload(PathAskFacts{ToolName: "read", PathValue: "/etc/passwd", Surface: "path_read"}).Request.Requester
		eq(t, got, PromptRequester{}, "requester")
	})
	tw(t, f, "carries the typed path, the boundary, and the requester", func(t *testing.T) {
		payload := BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{
			PathAskFacts: PathAskFacts{ToolName: "write", PathValue: "/tmp/out.txt", AgentName: sp("my-agent"), Surface: "external_directory_write"}, Cwd: "/projects/my-app",
		})
		eq(t, payload.Kind, "external_directory", "kind")
		eq(t, payload.Request.ToolName, sp("write"), "tool")
		eq(t, payload.Request.Value, "/tmp/out.txt", "value")
		eq(t, payload.Request.Requester.AgentName, sp("my-agent"), "agent")
		eq(t, payload.Evidence, []PromptEvidence{{Label: "working directory", Text: "/projects/my-app"}}, "evidence")
	})
	tw(t, f, "discloses the resolved path as its own entry when it differs", func(t *testing.T) {
		got := BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{
			PathAskFacts: PathAskFacts{ToolName: "read", PathValue: "demo-symlink-passwd", ResolvedPath: sp("/etc/passwd"), Surface: "external_directory_read"}, Cwd: "/projects/my-app",
		}).Evidence
		eq(t, got, []PromptEvidence{{Label: "resolves to", Text: "/etc/passwd"}, {Label: "working directory", Text: "/projects/my-app"}}, "evidence")
	})
	tw(t, f, "omits the disclosure when resolvedPath is undefined", func(t *testing.T) {
		got := BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{
			PathAskFacts: PathAskFacts{ToolName: "read", PathValue: "/etc/passwd", Surface: "external_directory_read"}, Cwd: "/projects/my-app",
		}).Evidence
		eq(t, got, []PromptEvidence{{Label: "working directory", Text: "/projects/my-app"}}, "evidence")
	})
	tw(t, f, "makes the command the value and the paths it reached evidence", func(t *testing.T) {
		payload := BuildBashExternalDirectoryAskPayload(BashExternalDirectoryAskFacts{
			Command: "cat /etc/passwd", ExternalPaths: []ExternalPathDisclosure{{Path: "/etc/passwd"}}, Cwd: "/projects/my-app",
			AgentName: sp("my-agent"), ToolName: "bash", Surface: "external_directory_read",
		})
		eq(t, payload.Kind, "bash_external_directory", "kind")
		eq(t, payload.Request.Value, "cat /etc/passwd", "value")
		// The surface names the direction; the kind stays coarse for the renderer.
		eq(t, payload.Request.Surface, "external_directory_read", "surface")
		eq(t, payload.Request.Requester.AgentName, sp("my-agent"), "agent")
		eq(t, payload.Evidence, []PromptEvidence{{Label: "working directory", Text: "/projects/my-app"}, {Label: "external path", Text: "/etc/passwd"}}, "evidence")
	})
	tw(t, f, "binds each path's canonical alias to its own entry", func(t *testing.T) {
		payload := BuildBashExternalDirectoryAskPayload(BashExternalDirectoryAskFacts{
			Command: "cat a b", ExternalPaths: []ExternalPathDisclosure{{Path: "/a", ResolvedPath: sp("/private/a")}, {Path: "/b"}},
			Cwd: "/repo", Surface: "external_directory", ToolName: "bash",
		})
		eq(t, payload.Evidence, []PromptEvidence{
			{Label: "working directory", Text: "/repo"}, {Label: "external path", Text: "/a", Detail: sp("/private/a")}, {Label: "external path", Text: "/b"},
		}, "evidence")
	})
}
