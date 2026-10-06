package ponytail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cases the original's tests do not carry: branches a mutation of this port showed unchecked (port/mutations.json),
// the status indicator the scenarios keep hidden, and the host calls the extension makes.

func TestNormalizers(t *testing.T) {
	eq(t, normalizeMode(" ULTRA "), "ultra")
	eq(t, normalizeMode("review"), "")
	eq(t, normalizeMode("nope"), "")
	eq(t, normalizeConfigMode("Review"), "review")
	eq(t, normalizeConfigMode("nope"), "")
	eq(t, normalizePersistedMode("off"), "off")
	eq(t, normalizePersistedMode(" Review"), "review")
	eq(t, normalizePersistedMode(""), "")
	eq(t, normalizeMode("\ufeff lite\u00a0"), "lite") // JavaScript's trim
}

func TestDeactivationPhrases(t *testing.T) {
	for _, yes := range []string{"stop ponytail", "Stop Ponytail.", "  NORMAL MODE!?  ", "normal mode...", "stop ponytail \t"} {
		eq(t, isDeactivationCommand(yes), true)
	}
	for _, no := range []string{"please stop ponytail", "normal mode please", "stop ponytail and continue", "", "normal  mode"} {
		eq(t, isDeactivationCommand(no), false)
	}
}

func TestConfigDirChoice(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	eq(t, configDir(), filepath.Join("/xdg", "ponytail"))
	t.Setenv("XDG_CONFIG_HOME", "")
	eq(t, configDir(), filepath.Join("/home/u", ".config", "ponytail"))
}

func TestDefaultModeResolution(t *testing.T) {
	dir := configHome(t)
	eq(t, getDefaultMode(), "full")
	writeConfig(t, dir, `{"defaultMode":"LITE"}`)
	eq(t, getDefaultMode(), "lite")
	setEnv(t, "PONYTAIL_DEFAULT_MODE", "Ultra") // the environment wins, case aside
	eq(t, getDefaultMode(), "ultra")
	setEnv(t, "PONYTAIL_DEFAULT_MODE", "review") // review is never a default
	eq(t, getDefaultMode(), "lite")
	unsetEnv(t, "PONYTAIL_DEFAULT_MODE")
	for _, bad := range []string{`{"defaultMode":"review"}`, `{"defaultMode":7}`, `{nope`, `[1]`, `null`, ``} {
		writeConfig(t, dir, bad)
		eq(t, getDefaultMode(), "full")
	}
	writeConfig(t, dir, "\xef\xbb\xbf"+`{"defaultMode":"ultra"}`) // a byte-order mark
	eq(t, getDefaultMode(), "ultra")
}

func TestFlags(t *testing.T) {
	dir := configHome(t)
	eq(t, [2]bool{getQuietStartup(), getHideStatus()}, [2]bool{false, false})
	writeConfig(t, dir, `{"hideStatus":true,"quietStartup":"yes"}`) // only a JSON true counts
	eq(t, [2]bool{getQuietStartup(), getHideStatus()}, [2]bool{false, true})
	writeConfig(t, dir, "\xef\xbb\xbf"+`{"quietStartup":true}`)
	eq(t, getQuietStartup(), true)
	for _, v := range []string{"", "0", "false", "No", " FALSE "} {
		setEnv(t, "PONYTAIL_HIDE_STATUS", v) // a set variable decides, even over a config that says true
		writeConfig(t, dir, `{"hideStatus":true}`)
		eq(t, getHideStatus(), false)
	}
	for _, v := range []string{"1", "true", "yes", "anything"} {
		setEnv(t, "PONYTAIL_HIDE_STATUS", v)
		writeConfig(t, dir, `{}`)
		eq(t, getHideStatus(), true)
	}
}

func TestWriteDefaultModeKeepsTheFile(t *testing.T) {
	dir := configHome(t)
	writeConfig(t, dir, `{"zeta":{"a":[1,{"b":2}],"c":null},"defaultMode":"lite","alpha":"x"}`)
	w, err := writeDefaultMode(" Ultra ")
	eq(t, [2]any{w, err}, [2]any{"ultra", nil})
	data, _ := os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data), "{\n  \"zeta\": {\n    \"a\": [\n      1,\n      {\n        \"b\": 2\n      }\n    ],\n    \"c\": null\n  },\n  \"defaultMode\": \"ultra\",\n  \"alpha\": \"x\"\n}")
	// a file that is not an object is replaced; a new field goes last; review is refused without touching anything
	writeConfig(t, dir, `[1,2]`)
	writeDefaultMode("lite")
	data, _ = os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data), "{\n  \"defaultMode\": \"lite\"\n}")
	w, err = writeDefaultMode("review")
	eq(t, [2]any{w, err}, [2]any{"", nil})
	data2, _ := os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data2), string(data))
	writeConfig(t, dir, `{"b":1,"a":2}`)
	writeDefaultMode("full")
	data, _ = os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data), "{\n  \"b\": 1,\n  \"a\": 2,\n  \"defaultMode\": \"full\"\n}")
	// a missing directory is created
	fresh := t.TempDir()
	setEnv(t, "XDG_CONFIG_HOME", filepath.Join(fresh, "deep", "er"))
	w, err = writeDefaultMode("lite")
	eq(t, [2]any{w, err}, [2]any{"lite", nil})
}

func TestParsingEdges(t *testing.T) {
	eq(t, parsePonytailCommand("", "lite"), command{Type: "set-mode", Mode: "lite"})
	eq(t, parsePonytailCommand("   ", "bogus"), command{Type: "set-mode", Mode: "full"})
	eq(t, parsePonytailCommand("  ULTRA  ", ""), command{Type: "set-mode", Mode: "ultra"})
	eq(t, parsePonytailCommand("status now", "full"), command{Type: "status"})
	eq(t, parsePonytailCommand("default", "full"), command{Type: "invalid", Reason: "invalid-default-mode"})
	eq(t, parsePonytailCommand("default  ULTRA extra", "full"), command{Type: "set-default", Mode: "ultra"})
	eq(t, parsePonytailCommand("review", "full"), command{Type: "invalid", Reason: "invalid-mode", Mode: "review"})
	eq(t, parsePonytailCommand("Bogus word", "full"), command{Type: "invalid", Reason: "invalid-mode", Mode: "bogus"})
	eq(t, parsePonytailCommand("off", "full"), command{Type: "set-mode", Mode: "off"})
}

func TestSessionModeEdges(t *testing.T) {
	junk := obj{"type": "custom", "customType": "other", "data": obj{"mode": "lite"}}
	bad := obj{"type": "custom", "customType": "ponytail-mode", "data": obj{"mode": "nope"}}
	msg := obj{"type": "message", "customType": "ponytail-mode", "data": obj{"mode": "ultra"}}
	eq(t, resolveSessionMode([]obj{customEntry("lite"), junk, bad, msg}, "full"), "lite") // skips what is not a valid ponytail entry
	eq(t, resolveSessionMode([]any{customEntry("ultra"), "junk", nil}, "full"), "ultra")
	eq(t, resolveSessionMode([]obj{}, "BOGUS"), "full")
	eq(t, resolveSessionMode([]obj{}, " Lite "), "lite")
	eq(t, resolveSessionMode([]obj{{"type": "custom", "customType": "ponytail-mode"}}, "ultra"), "ultra")
}

func TestFilterEdges(t *testing.T) {
	body := "---\r\nname: x\r\n---\r\n\r\n| **Lite** | l |\r\n| **other** | o |\r\n| notbold | n |\r\n- Lite: \"example\"\r\n- other: \"kept\"\r\n- full: not quoted\r\nend"
	got := filterSkillBodyForMode(body, "full")
	eq(t, got, "| **other** | o |\n| notbold | n |\n- other: \"kept\"\n- full: not quoted\nend")
	eq(t, strings.Contains(filterSkillBodyForMode(body, "lite"), "| **Lite** | l |"), true)
	eq(t, strings.Contains(filterSkillBodyForMode(body, "off"), "| **Lite** | l |"), false) // off keeps no level's lines
	eq(t, strings.Contains(filterSkillBodyForMode(body, "bogus"), "- Lite:"), false)        // an unknown mode reads as the default
	eq(t, strings.Contains(filterSkillBodyForMode("---\nname: a\n---\n\u00a0\nbody", "full"), "body"), true)
	eq(t, filterSkillBodyForMode("---\nx\n---\u00a0\u3000body", "full"), "body")
}

func TestInstructions(t *testing.T) {
	for _, m := range []string{"lite", "full", "ultra"} {
		got := getPonytailInstructions(m)
		eq(t, strings.HasPrefix(got, "PONYTAIL MODE ACTIVE — level: "+m+"\n\n# Ponytail"), true)
	}
	eq(t, getPonytailInstructions("review"), "PONYTAIL MODE ACTIVE — level: review. Behavior defined by /ponytail-review skill.")
	eq(t, strings.HasPrefix(getPonytailInstructions("bogus"), "PONYTAIL MODE ACTIVE — level: full\n\n"), true)
	// the three levels differ only in their per-level lines
	eq(t, getPonytailInstructions("lite") != getPonytailInstructions("ultra"), true)
	eq(t, strings.Contains(string(skillBody), "name: pigpen-ponytail"), true)
}

func TestStatusIndicator(t *testing.T) {
	configHome(t)
	r := startRig(t, customEntry("lite"))
	r.start("resume")
	r.agentStart()
	r.Host.Fire("agent_end", obj{})
	eq(t, r.statuses(), []string{"○ 🐴 ponytail: 🌿 LITE", "● 🐴 ponytail: 🌿 LITE", "○ 🐴 ponytail: 🌿 LITE"})
	r.command("ponytail", "full")
	r.command("ponytail", "ultra")
	r.command("ponytail", "off")
	s := r.statuses()
	eq(t, s[3:], []string{"○ 🐴 ponytail: ⚡ FULL", "○ 🐴 ponytail: 🔥 ULTRA", ""}) // off clears it
}

func TestNoticesAndCommands(t *testing.T) {
	configHome(t)
	r := startRig(t)
	r.start("startup")
	eq(t, r.notices(), []string{"Ponytail loaded: full"})
	r.command("ponytail", "lite")
	r.command("ponytail", "status")
	r.command("ponytail", "bogus")
	r.command("ponytail", "default ultra")
	r.command("ponytail", "status")
	eq(t, r.notices()[1:], []string{
		"Ponytail mode set to lite.",
		"Ponytail: current lite • default full",
		"Unknown or unsupported /ponytail mode.",
		"Default Ponytail mode set to ultra.",
		"Ponytail: current lite • default ultra",
	})
	// an environment override keeps the default, and the notice says so
	setEnv(t, "PONYTAIL_DEFAULT_MODE", "lite")
	r.command("ponytail", "default full")
	n := r.notices()
	eq(t, n[len(n)-1], "Saved default full, but env override keeps default at lite.")
	// the bare command sets the default level, full when that is off
	r.command("ponytail", "")
	appended := r.CallsTo("appendEntry")
	eq(t, appended[len(appended)-1].Args["data"], obj{"mode": "lite"})
}

func TestQuietStartupAndTheDeactivationDoesNotNotify(t *testing.T) {
	dir := configHome(t)
	writeConfig(t, dir, `{"quietStartup":true}`)
	r := startRig(t)
	r.start("startup")
	eq(t, len(r.notices()), 0)
	r.command("ponytail", "ultra")
	r.input("normal mode")
	eq(t, r.notices(), []string{"Ponytail mode set to ultra."}) // the phrase switches off silently
	appended := r.CallsTo("appendEntry")
	eq(t, appended[len(appended)-1].Args["data"], obj{"mode": "off"})
	// an input the extension itself sent is ignored
	r2 := startRig(t)
	r2.start("startup")
	r2.command("ponytail", "ultra")
	r2.Host.Fire("input", obj{"text": "normal mode", "source": "extension"})
	_, ok := r2.prompt("BASE")
	eq(t, ok, true)
}

func TestAliasQueuesAsFollowUpWhileBusy(t *testing.T) {
	configHome(t)
	r := startRig(t)
	r.mu.Lock()
	r.idle = false
	r.mu.Unlock()
	r.command("ponytail-review", "")
	c := r.CallsTo("sendUserMessage")
	eq(t, len(c), 1)
	eq(t, c[0].Args["content"], "/skill:pigpen-ponytail-review")
	eq(t, c[0].Args["options"], obj{"deliverAs": "followUp"})
	eq(t, r.notices(), []string{"pigpen-ponytail-review queued as follow-up."})
	r.mu.Lock()
	r.idle = true
	r.mu.Unlock()
	r.command("ponytail-audit", "")
	c = r.CallsTo("sendUserMessage")
	eq(t, c[1].Args["options"], obj{"deliverAs": ""}) // idle: sent as an ordinary message
}

func TestOffSessionLeavesThePromptAlone(t *testing.T) {
	configHome(t)
	r := startRig(t, customEntry("off"))
	r.start("resume")
	_, ok := r.prompt("BASE")
	eq(t, ok, false)
	eq(t, len(r.statuses()), 1)
	eq(t, r.statuses()[0], "")
}

func TestCommandDescription(t *testing.T) {
	eq(t, commandDescription(), "Set mode: off|lite|full|ultra. Commands: status, default <mode>")
}

func TestWriteDefaultModeEdges(t *testing.T) {
	dir := configHome(t)
	writeConfig(t, dir, `{"a":1,"b":2,"a":3}`)
	writeDefaultMode("lite")
	data, _ := os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data), "{\n  \"a\": 3,\n  \"b\": 2,\n  \"defaultMode\": \"lite\"\n}") // the last duplicate wins, as in JavaScript
	writeConfig(t, dir, "\xef\xbb\xbf"+`{"keep":true}`)
	writeDefaultMode("full")
	data, _ = os.ReadFile(filepath.Join(dir, "ponytail", "config.json"))
	eq(t, string(data), "{\n  \"keep\": true,\n  \"defaultMode\": \"full\"\n}")
}

func TestASaveFailureIsReported(t *testing.T) {
	dir := configHome(t)
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	setEnv(t, "XDG_CONFIG_HOME", blocker) // a file where the directory should be
	r := startRig(t)
	r.command("ponytail", "default lite")
	n := r.notices()
	eq(t, strings.HasPrefix(n[len(n)-1], "Failed to save default mode: "), true)
	var level any
	for _, c := range r.CallsTo("ui.notify") {
		level = c.Args["level"]
	}
	eq(t, level, "error")
}

func TestSessionStartRefreshesTheDefault(t *testing.T) {
	dir := configHome(t)
	r := startRig(t)
	writeConfig(t, dir, `{"defaultMode":"ultra"}`) // written after the extension loaded
	r.start("startup")
	p, _ := r.prompt("BASE")
	eq(t, strings.Contains(p, "level: ultra"), true)
}

func TestLabelWhitespaceIsTrimmed(t *testing.T) {
	eq(t, strings.Contains(filterSkillBodyForMode("| ** lite ** | x |\n- ultra: \"y\"", "ultra"), "| ** lite ** |"), false)
}

func TestReviewSessionInjectsItsPointer(t *testing.T) {
	configHome(t)
	r := startRig(t, customEntry("review"))
	r.start("resume")
	p, _ := r.prompt("BASE")
	eq(t, p, "BASE\n\nPONYTAIL MODE ACTIVE — level: review. Behavior defined by /ponytail-review skill.")
}

func TestDeactivatingTwiceRecordsOnce(t *testing.T) {
	configHome(t)
	r := startRig(t)
	r.start("startup")
	r.input("normal mode")
	n := len(r.CallsTo("appendEntry"))
	r.input("normal mode")
	r.input("stop ponytail")
	eq(t, len(r.CallsTo("appendEntry")), n) // already off: nothing more to record
}
