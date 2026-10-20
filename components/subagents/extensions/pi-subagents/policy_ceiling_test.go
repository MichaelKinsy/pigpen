package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func ceilingOf(t *testing.T, s string) *resolvedCeiling {
	t.Helper()
	c, err := parseSubagentCapabilityCeiling(js(t, s), "capability ceiling")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSubagentCapabilityCeiling(t *testing.T) {
	tw(t, "capability-ceiling", "intersects active registrations for an exact session", func(t *testing.T) {
		sid := "cap-1"
		first, err := registerSubagentCapabilityCeiling(sid, "plan", js(t, `{"allowedTools":["read","grep","write"]}`))
		if err != nil {
			t.Fatal(err)
		}
		second, _ := registerSubagentCapabilityCeiling(sid, "review", js(t, `{"allowedTools":["read","grep"],"denyExtensions":true}`))
		equal(t, resolveSubagentCapabilityCeiling(sid, nil), ceilingOf(t, `{"version":1,"allowedTools":["grep","read"],"denyExtensions":true,"sources":["plan","review"]}`), "intersection")
		if resolveSubagentCapabilityCeiling(sid+"-other", nil) != nil {
			t.Error("other session has a ceiling")
		}
		second.dispose()
		first.dispose()
	})
	tw(t, "capability-ceiling", "keeps explicit empty allowlists distinct from unrestricted state", func(t *testing.T) {
		c := intersectSubagentCapabilityCeilings(ceilingOf(t, `{"version":1,"allowedTools":[],"denyExtensions":false,"sources":["test"]}`))
		equal(t, c.AllowedTools, []string{}, "empty list")
		if !c.HasAllowedTools {
			t.Error("lost the empty allowlist")
		}
		if intersectSubagentCapabilityCeilings() != nil {
			t.Error("no ceilings must give undefined")
		}
	})
	tw(t, "capability-ceiling", "rejects malformed policy and disposed updates", func(t *testing.T) {
		_, err := registerSubagentCapabilityCeiling("x", "x", js(t, `{}`))
		if err == nil {
			t.Fatal("empty ceiling accepted")
		}
		polMatches(t, err.Error(), `allowedTools, allowedAgents, or denyExtensions`)
		h, _ := registerSubagentCapabilityCeiling("disposed", "test", js(t, `{"denyExtensions":true}`))
		h.dispose()
		err = h.update(js(t, `{"allowedTools":["read"]}`))
		if err == nil {
			t.Fatal("disposed handle updated")
		}
		polMatches(t, err.Error(), `disposed`)
	})
	tw(t, "capability-ceiling", "round-trips inherited policy and rejects malformed payloads", func(t *testing.T) {
		input := ceilingOf(t, `{"version":1,"allowedTools":["read"],"denyExtensions":true,"sources":["plan"]}`)
		got, err := decodeSubagentCapabilityCeiling(encodeSubagentCapabilityCeiling(input))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got, input, "round trip")
		_, err = decodeSubagentCapabilityCeiling("not-base64-json")
		if err == nil {
			t.Fatal("garbage decoded")
		}
		polMatches(t, err.Error(), `Invalid inherited capability ceiling`)
		_, err = parseSubagentCapabilityCeiling(js(t, `{"version":1,"allowedTools":["read"],"denyExtensions":true}`), "capability ceiling")
		if err == nil {
			t.Fatal("no sources accepted")
		}
		polMatches(t, err.Error(), `sources`)
		_, err = parseSubagentCapabilityCeiling(js(t, `{"version":2,"allowedTools":["read"],"denyExtensions":true,"sources":["plan"]}`), "capability ceiling")
		if err == nil {
			t.Fatal("version 2 accepted")
		}
		polMatches(t, err.Error(), `version`)
	})
	tw(t, "capability-ceiling", "combines the inherited child runtime ceiling with exact-session registrations", func(t *testing.T) {
		sid := "current-1"
		h, _ := registerSubagentCapabilityCeiling(sid, "local", js(t, `{"allowedTools":["grep","read"]}`))
		defer h.dispose()
		inherited, err := decodeSubagentCapabilityCeiling(encodeSubagentCapabilityCeiling(ceilingOf(t, `{"version":1,"allowedTools":["read","write"],"denyExtensions":true,"sources":["ancestor"]}`)))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, resolveSubagentCapabilityCeiling(sid, inherited), ceilingOf(t, `{"version":1,"allowedTools":["read"],"denyExtensions":true,"sources":["ancestor","local"]}`), "with inherited")
		equal(t, resolveCurrentSubagentCapabilityCeiling(sid), ceilingOf(t, `{"version":1,"allowedTools":["grep","read"],"denyExtensions":false,"sources":["local"]}`), "current")
	})
}

func TestCapabilityCeilingAgentAllowlist(t *testing.T) {
	tw(t, "capability-ceiling-agent-allowlist", "parses, round-trips, and intersects allowedAgents", func(t *testing.T) {
		parsed := ceilingOf(t, `{"version":1,"allowedAgents":["worker","reviewer","worker"],"denyExtensions":false,"sources":["plan"]}`)
		equal(t, parsed.AllowedAgents, []string{"reviewer", "worker"}, "allowedAgents")
		back, err := decodeSubagentCapabilityCeiling(encodeSubagentCapabilityCeiling(parsed))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, back, parsed, "round trip")
		equal(t, intersectSubagentCapabilityCeilings(
			ceilingOf(t, `{"version":1,"allowedAgents":["worker","reviewer"],"denyExtensions":false,"sources":["outer"]}`),
			ceilingOf(t, `{"version":1,"allowedAgents":["reviewer","scout"],"denyExtensions":true,"sources":["inner"]}`),
		), ceilingOf(t, `{"version":1,"allowedAgents":["reviewer"],"denyExtensions":true,"sources":["inner","outer"]}`), "agents")
		none := intersectSubagentCapabilityCeilings(
			ceilingOf(t, `{"version":1,"allowedTools":["read"],"denyExtensions":false,"sources":["tools-only"]}`),
			ceilingOf(t, `{"version":1,"allowedAgents":[],"denyExtensions":false,"sources":["none"]}`),
		)
		equal(t, none.AllowedAgents, []string{}, "empty agents")
	})

	tw(t, "capability-ceiling-agent-allowlist", "marks non-allowlisted agents as restricted in list output", func(t *testing.T) {
		e := newMgmtEnv(t)
		sessionID := fmt.Sprintf("allowlist-list-%d", time.Now().UnixNano())
		handle, err := registerSubagentCapabilityCeiling(sessionID, "plan-mode", js(t, `{"allowedAgents":["reviewer"]}`))
		if err != nil {
			t.Fatal(err)
		}
		defer handle.dispose()
		ctx := e.ctx()
		ctx.sessionID = sessionID
		text := handleManagementAction("list", map[string]any{}, ctx).text
		polMatches(t, text, `Executable agents:`)
		polMatches(t, text, `- reviewer `)
		polMatches(t, text, `Restricted agents \(not executable in this session; capability ceiling: plan-mode\):`)
		polMatches(t, text, `- worker `)
	})

	tw(t, "capability-ceiling-agent-allowlist", "rejects a non-allowlisted agent in preflight launch resolution", func(t *testing.T) {
		e := newPfEnv(t)
		r := pfResolve(t, launchContractInput{Agent: "worker", Cwd: e.dir("repo"),
			CapabilityCeiling: ceilingOf(t, `{"version":1,"allowedAgents":["reviewer"],"denyExtensions":false,"sources":["plan-mode"]}`)})
		equal(t, r.OK, false, "refused")
		equal(t, r.Code, "restricted_agent", "code")
		polMatches(t, r.Message, `does not allow agent 'worker'`)
		polMatches(t, r.Message, `Allowed agents: reviewer`)
	})

	tskip(t, "capability-ceiling-agent-allowlist", "includes allowedAgents in the child runtime config and audit metadata",
		"belongs to another slice, not ported: buildInProcessChildLaunch (runs/shared/child-launch.ts) builds the in-process child session's runtime config; the port starts a pig process and has no such launch builder yet (see port/deferred-skips.md)")
	tskip(t, "capability-ceiling-agent-allowlist", "applies the selected agent's descendant ceiling only after its parent-authorized launch",
		"belongs to another slice, not ported: buildInProcessChildLaunch (runs/shared/child-launch.ts) applies descendantAllowedAgents to the child runtime config (see port/deferred-skips.md)")
	tskip(t, "capability-ceiling-agent-allowlist", "propagates the detached runner step snapshot through the common launch seam",
		"belongs to another slice, not ported: buildRunnerChildLaunch (runs/background/runner-child-launch.ts), the detached runner's launch seam over the child launch builder (see port/deferred-skips.md)")
}

func TestNormalizeCapabilityCeilingAllowedAgents(t *testing.T) {
	got, err := normalizeCapabilityCeilingAllowedAgents(js(t, `["worker","reviewer","worker"]`))
	equal(t, got, []string{"reviewer", "worker"}, "names")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"worker"`, `["bad name"]`, `[""]`, `[1]`} {
		if _, err := normalizeCapabilityCeilingAllowedAgents(js(t, bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	_, err = normalizeCapabilityCeilingAllowedAgents(js(t, `["bad name"]`))
	equal(t, fmt.Sprint(err), "Invalid capability ceiling allowedAgents entry 'bad name'.", "message")
}

func TestAssertJSONSchemaObject(t *testing.T) {
	for _, bad := range []string{`null`, `[]`, `"x"`, `1`, `true`} {
		err := assertJSONSchemaObject(js(t, bad), "outputSchema")
		if err == nil || err.Error() != "outputSchema must be a JSON Schema object." {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if err := assertJSONSchemaObject(js(t, `{"type":"object"}`), "gate.schema"); err != nil {
		t.Error(err)
	}
	if err := assertJSONSchemaObject(js(t, `[]`), "gate.schema"); err == nil || err.Error() != "gate.schema must be a JSON Schema object." {
		t.Error(err)
	}
}

// TestJSJSONParseMessages checks jsJSONParse against what Node 24.19.0's JSON.parse said (testdata/jsonparse_golden.json,
// generated by running JSON.parse on each input).
func TestJSJSONParseMessages(t *testing.T) {
	raw, err := os.ReadFile("testdata/jsonparse_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		In  string  `json:"in"`
		Err *string `json:"err"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		_, got := jsJSONParse(c.In)
		want := ""
		if c.Err != nil {
			want = *c.Err
		}
		if got != want {
			t.Errorf("%q: got %q want %q", c.In, got, want)
		}
	}
}
