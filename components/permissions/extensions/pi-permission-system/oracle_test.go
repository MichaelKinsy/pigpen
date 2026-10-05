package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Tests against the original's own decisions, recorded by port/record-probe.sh from port/probe/cases.json into
// testdata/oracle.json: the bash surface's verdict for 646 commands under 11 configs, the config schema for 91 files, and the
// wrapper classification for 48 command units.

type oracleRecord struct {
	Bash []struct {
		Config         string          `json:"config"`
		Permission     json.RawMessage `json:"permission"`
		Yolo           bool            `json:"yolo"`
		Command        string          `json:"command"`
		State          string          `json:"state"`
		MatchedPattern *string         `json:"matchedPattern"`
		Reason         *string         `json:"reason"`
	} `json:"bash"`
	Config []struct {
		Raw        string   `json:"raw"`
		Issues     []string `json:"issues"`
		ParseError *string  `json:"parseError"`
		YoloMode   *bool    `json:"yoloMode"`
		ShellTools []string `json:"shellTools"`
		Permission *string  `json:"permissionJSON"`
	} `json:"config"`
	Wrapper []struct {
		Unit string  `json:"unit"`
		Kind *string `json:"kind"`
	} `json:"wrapper"`
}

func loadOracle(t *testing.T) oracleRecord {
	t.Helper()
	data, err := os.ReadFile("testdata/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var r oracleRecord
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// portDifference is a case where the port does not give the original's decision, and blocks instead: "unsupported" (a command
// the port cannot read without a parser) or "stricter" (an ask where the original's floor exemption for a wrapper running a
// pure reader allows; the exemption is not ported).
type portDifference struct {
	Config  string `json:"config"`
	Command string `json:"command"`
	Kind    string `json:"kind"`
}

// TestBashGateAgainstOriginal: the port never allows a command the original asks about or denies; where it decides, it gives the
// original's decision and text; and the cases where it refuses instead are exactly the ones listed in
// testdata/port-differences.json (reviewed by hand: each is a command the port cannot read, a port guard word, or the floor
// exemption).
func TestBashGateAgainstOriginal(t *testing.T) {
	r := loadOracle(t)
	var listed []portDifference
	data, err := os.ReadFile("testdata/port-differences.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &listed); err != nil {
		t.Fatal(err)
	}
	var got []portDifference
	for _, c := range r.Bash {
		yolo := "false"
		if c.Yolo {
			yolo = "true"
		}
		cfg := parseConfig([]byte(`{"yoloMode":`+yolo+`,"permission":`+string(c.Permission)+`}`), "/cfg.json")
		if cfg.Issue != "" {
			t.Fatalf("%s: %s", c.Config, cfg.Issue)
		}
		p := NewPolicy(cfg)
		v := p.Decide("bash", map[string]any{"command": c.Command})
		name := c.Config + ": " + c.Command
		if c.State != "allow" && !v.Block {
			t.Errorf("%s: the original says %s, the port allows", name, c.State)
			continue
		}
		var want Verdict
		switch c.State {
		case "ask":
			want = asked("bash")
		case "deny":
			text := "Denied by policy: 'bash'"
			if c.MatchedPattern != nil {
				text += " (rule '" + *c.MatchedPattern + "')"
			}
			text += "."
			if c.Reason != nil && *c.Reason != "" {
				text += " Reason: " + *c.Reason + "."
			}
			want = Verdict{Block: true, Reason: tagged(text)}
		}
		switch {
		case v == want:
		case strings.Contains(v.Reason, "(Go port) cannot evaluate this 'bash' call"):
			got = append(got, portDifference{c.Config, c.Command, "unsupported"})
		case c.State == "allow" && v == asked("bash"):
			got = append(got, portDifference{c.Config, c.Command, "stricter"})
		default:
			t.Errorf("%s: got %#v, want %#v", name, v, want)
		}
	}
	key := func(d portDifference) string { return d.Config + "\x00" + d.Command + "\x00" + d.Kind }
	set := func(ds []portDifference) []string {
		var out []string
		for _, d := range ds {
			out = append(out, key(d))
		}
		sort.Strings(out)
		return out
	}
	if os.Getenv("PERMISSIONS_WRITE_DIFFERENCES") != "" {
		b, _ := json.MarshalIndent(got, "", " ")
		os.WriteFile("testdata/port-differences.json", append(b, '\n'), 0o644)
	}
	eq(t, set(got), set(listed), "the cases where the port refuses instead of deciding")
	if len(r.Bash) != 646 {
		t.Errorf("%d bash cases, want 646", len(r.Bash))
	}
}

// TestConfigSchemaAgainstOriginal: a file is used when the original's schema accepts it, and refused whole, with the original's
// messages, when it does not. A file that does not parse is refused too; its message is Go's, not JavaScript's.
func TestConfigSchemaAgainstOriginal(t *testing.T) {
	r := loadOracle(t)
	for _, c := range r.Config {
		got := parseConfig([]byte(c.Raw), "/cfg.json")
		switch {
		case c.ParseError != nil:
			if !strings.HasPrefix(got.Issue, "Failed to read config at '/cfg.json': ") {
				t.Errorf("%q: issue %q, want a read failure (the original: %s)", c.Raw, got.Issue, *c.ParseError)
			}
		case len(c.Issues) > 0:
			eq(t, got.Issue, strings.Join(c.Issues, "\n"), c.Raw)
		default:
			eq(t, got.Issue, "", c.Raw)
			eq(t, got.Yolo, c.YoloMode != nil && *c.YoloMode, c.Raw+" yoloMode")
			eq(t, strings.Join(got.ShellTools, ","), strings.Join(c.ShellTools, ","), c.Raw+" shellTools")
			// the permission map as the schema outputs it: the surfaces it names first (key order decides which rule matches last)
			perm := "null"
			if got.Permission != nil {
				perm = marshalJSON(got.Permission, "")
			}
			eq(t, perm, *c.Permission, c.Raw+" permission")
			continue
		}
		if got.Permission != nil || got.Yolo || got.ShellTools != nil {
			t.Errorf("%q: a refused file is used: %#v", c.Raw, got)
		}
	}
	if len(r.Config) != 91 {
		t.Errorf("%d config cases, want 91", len(r.Config))
	}
}

func TestWrapperClassificationAgainstOriginal(t *testing.T) {
	for _, c := range loadOracle(t).Wrapper {
		want := ""
		if c.Kind != nil {
			want = *c.Kind
		}
		eq(t, classifyWrapperWords(strings.Fields(c.Unit)), want, c.Unit)
	}
}

// upstreamWords splits a unit as the upstream test's words() does: a quoted span is one word, carrying its quotes.
func upstreamWords(unit string) []string {
	return regexp.MustCompile(`"[^"]*"|'[^']*'|\S+`).FindAllString(unit, -1)
}

func TestWrapperAnalysis(t *testing.T) {
	const f = "access-intent/bash/wrapper-analysis"
	// classifyWrapperWords: the upstream it.each tables (their titles are parametrized, so they run as plain subtests).
	for _, u := range []string{"eval rm", "bash -c rm", "sh -c rm", "dash -c rm", "zsh -c rm", "ksh -c rm", "bash -ec rm", "bash -xc rm", "/bin/bash -c rm"} {
		t.Run("flags "+u, func(t *testing.T) { eq(t, classifyWrapperWords(upstreamWords(u)), "opaque-payload", u) })
	}
	for _, u := range []string{"sudo aws s3 ls", "env FOO=bar aws", "xargs grep foo", "timeout 10 grep foo", "nice -n 5 make", "doas ls", "flock /tmp/lock ls"} {
		t.Run("flags "+u, func(t *testing.T) { eq(t, classifyWrapperWords(upstreamWords(u)), "indirection", u) })
	}
	for _, u := range []string{"find . -exec grep foo {} ;", "find . -execdir rm {} ;", "fd -x rm", "fd --exec-batch rm"} {
		t.Run("flags the exec-conditional "+u, func(t *testing.T) { eq(t, classifyWrapperWords(upstreamWords(u)), "indirection", u) })
	}
	for _, u := range []string{"ls -la", "grep -c foo file", "git status"} {
		t.Run("does not flag "+u, func(t *testing.T) { eq(t, classifyWrapperWords(upstreamWords(u)), "", u) })
	}
	tw(t, f, "does not flag a shell running a script file", func(t *testing.T) {
		eq(t, classifyWrapperWords(upstreamWords("bash script.sh")), "", "script")
	})
	tw(t, f, "does not flag a -c cluster after the end-of-options marker", func(t *testing.T) {
		eq(t, classifyWrapperWords(upstreamWords("bash -- -c")), "", "--")
	})
	tw(t, f, "does not flag a bare search", func(t *testing.T) {
		eq(t, classifyWrapperWords(upstreamWords("find . -name x")), "", "bare search")
	})
	tw(t, f, "does not flag an empty word list", func(t *testing.T) {
		eq(t, classifyWrapperWords(nil), "", "empty")
	})
}

func TestDetectPermissiveBashFallbackTwins(t *testing.T) {
	const f = "config/detect-permissive-bash-fallback"
	tw(t, f, "warns when top-level '*' is allow and bash is absent", func(t *testing.T) {
		issue := detectPermissiveBashFallback(jo(t, `{"*":"allow"}`))
		eq(t, issue != "", true, "defined")
		eq(t, strings.Contains(issue, "bash"), true, "names bash")
		eq(t, strings.Contains(issue, "allow"), true, "names allow")
	})
	tw(t, f, "warns when top-level '*' is allow and bash map has no '*' key", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":{"git *":"ask"}}`)) != "", true, "defined")
	})
	tw(t, f, "does not warn when bash is a bare string surface", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":"ask"}`)), "", "undefined")
	})
	tw(t, f, "does not warn when bash map has an explicit '*' key", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow","bash":{"*":"ask","git *":"allow"}}`)), "", "undefined")
	})
	tw(t, f, "does not warn when top-level '*' is not allow", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"*":"ask"}`)), "", "undefined")
	})
	tw(t, f, "does not warn when top-level '*' is absent", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(jo(t, `{"bash":{"git *":"ask"}}`)), "", "undefined")
	})
	tw(t, f, "does not warn when permission is undefined", func(t *testing.T) {
		eq(t, detectPermissiveBashFallback(nil), "", "undefined")
	})
}

// A tool that shellTools routes through the bash surface is gated on bash in the original; the port does not port the aliases,
// so it refuses the tool instead of deciding it by its own name.
func TestShellToolAliasIsRefused(t *testing.T) {
	c := parseConfig([]byte(`{"shellTools":{"exec_command":{"commandArgument":"cmd"}},"permission":{"*":"allow",`+allowPaths+`,"bash":{"*":"allow","rm *":"deny"}}}`), "/cfg.json")
	eq(t, c.Issue, "", "valid")
	p := NewPolicy(c)
	v := p.Decide("exec_command", map[string]any{"cmd": "rm -rf x"})
	eq(t, v.Block && strings.Contains(v.Reason, "shellTools"), true, "an aliased shell tool is refused")
	eq(t, p.Decide("other_tool", map[string]any{"cmd": "rm -rf x"}), Verdict{}, "a tool that is not aliased is decided by its name")
}

// A deny with an empty reason has no reason clause (agent-renderer.ts reasonClause).
func TestDenyWithEmptyReason(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"allow","bar":{"*":{"action":"deny","reason":""}}}}`)
	eq(t, p.Decide("bar", nil).Reason, "[pi-permission-system] Denied by policy: 'bar' (rule '*').", "no reason clause")
}

// The config file is read once per session start and turn: a file that breaks the schema is reported and not used.
func TestLoadConfigRefusesASchemaViolation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	p := globalConfigPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"permission":{"*":"allow","bash":{"*":"allow","rm *":"dney"}}}`), 0o644)
	c := loadConfig()
	eq(t, c.Issue, "Invalid config value at 'permission.bash': Invalid input", "issue")
	eq(t, NewPolicy(c).Decide("bash", map[string]any{"command": "rm x"}).Block, true, "rm is not allowed by a refused file")
	os.WriteFile(p, []byte("{\n  // comments are allowed\n  \"permission\": {\"*\": \"deny\"} /* end */\n}"), 0o644)
	c = loadConfig()
	eq(t, c.Issue, "", "comments")
	eq(t, NewPolicy(c).rules[0].Action, "deny", "the commented file is used")
	// a file that cannot be read is reported, not taken for a missing one
	os.Remove(p)
	os.Mkdir(p, 0o755)
	eq(t, strings.HasPrefix(loadConfig().Issue, "Failed to read config at '"+p+"': "), true, "unreadable")
}
