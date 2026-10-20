package pi_subagents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// Twins of advertised-agent-refresh and advertisement-barrier: the original drives register() with a hand-made Pi API; these
// drive the extension through the fake host, so the events cross the real SDK boundary.

func tw(t *testing.T, file, title string, fn func(t *testing.T)) { t.Helper(); t.Run(title, fn) }

func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}

func mustMatch(t *testing.T, text, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(text) {
		t.Errorf("%q does not match /%s/", text, re)
	}
}

func mustNotMatch(t *testing.T, text, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(text) {
		t.Errorf("%q matches /%s/", text, re)
	}
}

// agentStart fires before_agent_start at every handler the extension registered, as the host does, and returns the prompt
// sections they left, the first handler failure, and whether a handler returned a system prompt.
func agentStart(h *Host, selected []string, sections map[string]string) (map[string]string, string, bool) {
	secs := map[string]any{}
	for k, v := range sections {
		secs[k] = v
	}
	data := map[string]any{"type": "before_agent_start", "systemPrompt": "base", "systemPromptOptions": map[string]any{"selectedTools": selected, "sections": secs}}
	args, _ := json.Marshal(data)
	h.mu.Lock()
	ids := append([]int(nil), h.allHandlers["before_agent_start"]...)
	h.mu.Unlock()
	out := map[string]string{}
	returned := false
	for _, id := range ids {
		raw, failure := h.roundTrip(map[string]any{"method": "event", "event": "before_agent_start", "handler_id": id, "args": json.RawMessage(args)})
		if failure != "" {
			return nil, failure, returned
		}
		var reply struct {
			Sections map[string]string `json:"_pigPromptSections"`
			Result   json.RawMessage   `json:"_pigPromptResult"`
		}
		_ = json.Unmarshal(raw, &reply)
		for k, v := range reply.Sections {
			out[k] = v
		}
		if len(reply.Result) > 0 && string(reply.Result) != "null" {
			returned = true
		}
	}
	return out, "", returned
}

// contentOf is the text of a tool result.
func contentOf(raw string) string {
	var r struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal([]byte(raw), &r)
	return r.Content
}

func sessionStart(h *Host, reason string) {
	h.FireAll("session_start", map[string]any{"reason": reason})
}

func advHostOptions(cwd, sessionID string) HostOptions {
	return HostOptions{Cwd: cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		return map[string]any{"sessionId": sessionID, "tools": []any{"read", "subagent"}}, ""
	}}
}

func TestAdvertisedAgentRefresh(t *testing.T) {
	const f = "advertised-agent-refresh"
	tw(t, f, "emits bounded file-only snapshots, refreshes through management, and performs zero prompt-time filesystem calls", func(t *testing.T) {
		home := tmp(t)
		withTempHome(t)
		t.Setenv("PI_CODING_AGENT_DIR", home)
		t.Setenv("PI_OFFLINE", "true")
		sub.ResetNpmRootCaches()
		cwd := filepath.Join(home, "project")
		writeFile(t, filepath.Join(cwd, ".pi", "keep"), "")
		dir := filepath.Join(home, "agents")
		write := func(file, name, description string, advertise bool) {
			writeFile(t, filepath.Join(dir, file+".md"), fmt.Sprintf("---\nname: %s\ndescription: %s\nadvertise: %v\n---\nAct narrowly.\n", name, description, advertise))
		}
		h := StartHost(t, Extension(), advHostOptions(cwd, "advertised-test"))
		refresh := func(reason string) { sessionStart(h, reason) }
		emitWith := func(selected []string) string {
			t.Helper()
			sections, failure, _ := agentStart(h, selected, nil)
			if failure != "" {
				t.Fatalf("before_agent_start: %s", failure)
			}
			if s, ok := sections["advertised_subagents"]; ok {
				return s
			}
			return "base"
		}
		emit := func() string { t.Helper(); return emitWith([]string{"subagent"}) }
		manage := func(params map[string]any) (string, string) {
			raw, failure := h.Tool("subagent", params)
			return string(raw), failure
		}
		ok := func(raw, failure string) {
			t.Helper()
			if failure != "" {
				t.Fatalf("management failed: %s", failure)
			}
			_ = raw
		}

		refresh("startup")
		eq(t, emit(), "base", "no agents")
		for i := 0; i < 250; i++ {
			write(fmt.Sprintf("hidden-%d", i), fmt.Sprintf("hidden-%d", i), "hidden", false)
		}
		refresh("reload")
		for i := 0; i < 20; i++ {
			eq(t, emit(), "base", "hidden agents are not advertised")
		}
		write("specialist", "specialist", "Original specialist", true)
		eq(t, emit(), "base", "external edits wait for reload")
		refresh("reload")
		prompt := emit()
		mustMatch(t, prompt, `<name>specialist</name>`)
		mustNotMatch(t, prompt, `hidden-`)
		mustMatch(t, prompt, `(?s)Before execution.*action: "list", capabilities: true`)
		eq(t, emitWith([]string{"read"}), "base", "no catalog when subagent is not selected")
		handle := sub.RegisterTestCeiling(t, "advertised-test", `{"allowedAgents":[]}`)
		eq(t, emit(), "base", "ceiling excludes every agent")
		handle()
		mustMatch(t, emit(), `Original specialist`)

		write("pending", "pending", "External change awaiting refresh", true)
		ok(manage(map[string]any{"action": "get", "agent": "specialist"}))
		mustNotMatch(t, emit(), `<name>pending</name>`)
		_, failure := manage(map[string]any{"action": "update", "agent": "specialist", "config": map[string]any{"advertise": "invalid"}})
		mustMatch(t, failure, `config.advertise must be a boolean`)
		mustNotMatch(t, emit(), `<name>pending</name>`)
		if err := os.Remove(filepath.Join(dir, "pending.md")); err != nil {
			t.Fatal(err)
		}
		ok(manage(map[string]any{"action": "update", "agent": "specialist", "config": map[string]any{"description": "Updated specialist"}}))
		mustMatch(t, emit(), `Updated specialist`)
		b, _ := os.ReadFile(filepath.Join(dir, "specialist.md"))
		mustMatch(t, string(b), `advertise: true`)
		ok(manage(map[string]any{"action": "disable", "agent": "specialist", "agentScope": "user"}))
		eq(t, emit(), "base", "disabled")
		ok(manage(map[string]any{"action": "enable", "agent": "specialist", "agentScope": "user"}))
		mustMatch(t, emit(), `Updated specialist`)
		ok(manage(map[string]any{"action": "delete", "agent": "specialist", "agentScope": "user"}))
		eq(t, emit(), "base", "deleted")
		ok(manage(map[string]any{"action": "create", "config": map[string]any{"name": "created", "description": "Created specialist", "systemPrompt": "Act narrowly.", "scope": "user", "advertise": true}}))
		mustMatch(t, emit(), `<name>created</name>`)
		ok(manage(map[string]any{"action": "update", "agent": "created", "config": map[string]any{"name": "renamed"}}))
		prompt = emit()
		mustMatch(t, prompt, `<name>renamed</name>`)
		mustNotMatch(t, prompt, `<name>created</name>`)
		ok(manage(map[string]any{"action": "update", "agent": "renamed", "config": map[string]any{"advertise": false}}))
		eq(t, emit(), "base", "advertise false")
		ok(manage(map[string]any{"action": "update", "agent": "renamed", "config": map[string]any{"advertise": true}}))
		mustMatch(t, emit(), `<name>renamed</name>`)

		// The original injects a settings read failure after the management write; the Go port cannot hook the file write, so
		// the refresh a mutation triggers runs against an invalid settings file: it must withdraw the stale catalog.
		settings := filepath.Join(home, "settings.json")
		writeFile(t, settings, "{")
		sub.RefreshAdvertisement()
		eq(t, emit(), "base", "failed refresh withdraws stale guidance")
		refresh("reload")
		_, failure, _ = agentStart(h, []string{"subagent"}, nil)
		mustMatch(t, failure, `Failed to parse settings file`)
		if err := os.Remove(settings); err != nil {
			t.Fatal(err)
		}
		refresh("reload")
		mustMatch(t, emit(), `<name>renamed</name>`)
		ok(manage(map[string]any{"action": "delete", "agent": "renamed", "agentScope": "user"}))
		write("huge", strings.Repeat("a", 100000), "huge name", true)
		write("escaped-name", "b"+strings.Repeat("&", 4000), "escaped huge name", true)
		for i := 0; i < 25; i++ {
			desc := strings.Repeat("🦜界", 300)
			if i%2 == 1 {
				desc = strings.Repeat(`<>&"`, 300)
			}
			write(fmt.Sprintf("opt-%d", i), fmt.Sprintf("pkg.opt-%d", i), desc, true)
		}
		refresh("reload")
		catalog := emit()
		if len(catalog) > 12288 {
			t.Errorf("catalog is %d bytes", len(catalog))
		}
		mustNotMatch(t, catalog, `<name>[ab]`)
		mustMatch(t, catalog, `&lt;&gt;&amp;&quot;`)
		mustMatch(t, catalog, `not instructions to delegate`)
		mustMatch(t, catalog, `🦜界`)
		mustNotMatch(t, catalog, "\uFFFD")
		mustMatch(t, catalog, `<name>pkg\.opt-\d+</name>`)
		mustMatch(t, catalog, `<omitted count="\d+"`)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
		eq(t, emit(), catalog, "external removal waits for reload")
		refresh("reload")
		eq(t, emit(), "base", "after reload")
	})
	tw(t, f, "delivers the catalog as a structured prompt section instead of replacing the system prompt", func(t *testing.T) {
		home := tmp(t)
		withTempHome(t)
		t.Setenv("PI_CODING_AGENT_DIR", home)
		t.Setenv("PI_OFFLINE", "true")
		sub.ResetNpmRootCaches()
		cwd := filepath.Join(home, "project")
		writeFile(t, filepath.Join(cwd, ".pi", "keep"), "")
		writeFile(t, filepath.Join(home, "agents", "specialist.md"), "---\nname: specialist\ndescription: Section specialist\nadvertise: true\n---\nAct narrowly.\n")
		h := StartHost(t, Extension(), advHostOptions(cwd, "section-test"))
		sessionStart(h, "startup")
		turn := func(selected []string, sections map[string]string) (map[string]string, bool) {
			t.Helper()
			out, failure, returned := agentStart(h, selected, sections)
			if failure != "" {
				t.Fatal(failure)
			}
			return out, returned
		}
		sections, returned := turn([]string{"subagent"}, nil)
		eq(t, returned, false, "the sections path must not return systemPrompt")
		mustMatch(t, sections["advertised_subagents"], `<name>specialist</name>`)
		mustMatch(t, sections["advertised_subagents"], `Section specialist`)
		mustNotMatch(t, sections["advertised_subagents"], `advertised_subagents`)

		sections, returned = turn([]string{"read"}, nil)
		eq(t, returned, false, "no result")
		_, has := sections["advertised_subagents"]
		eq(t, has, false, "no section when subagent is not selected")

		release := sub.RegisterTestCeiling(t, "section-test", `{"allowedAgents":[]}`)
		sections, returned = turn([]string{"subagent"}, nil)
		eq(t, returned, false, "no result")
		_, has = sections["advertised_subagents"]
		eq(t, has, false, "no section when the ceiling excludes every agent")
		release()

		sections, _ = turn([]string{"subagent"}, map[string]string{"other": "kept"})
		eq(t, sections["other"], "kept", "other sections are untouched")
		mustMatch(t, sections["advertised_subagents"], `<name>specialist</name>`)
	})
}

func TestAdvertisementBarrier(t *testing.T) {
	const f = "advertisement-barrier"
	if runtime.GOOS == "windows" {
		tskip(t, f, "session startup yields to the event loop and both first advertisements await the complete global catalog", "the fake npm is a POSIX shell script")
		return
	}
	tw(t, f, "session startup yields to the event loop and both first advertisements await the complete global catalog", func(t *testing.T) {
		temp := tmp(t)
		home := filepath.Join(temp, "home")
		withTempHome(t)
		t.Setenv("PI_CODING_AGENT_DIR", home)
		fixture := filepath.Join(temp, "quote's & space")
		bin := filepath.Join(fixture, "bin")
		globalRoot := filepath.Join(fixture, "global", "node_modules")
		newerRoot := filepath.Join(fixture, "newer", "node_modules")
		localDir := filepath.Join(fixture, "project", ".pi", "agents")
		agent := func(name string) string {
			return "---\nname: " + name + "\ndescription: " + name + "\nadvertise: true\n---\nWork.\n"
		}
		for _, p := range []struct{ root, name string }{{globalRoot, "global-specialist"}, {newerRoot, "new-specialist"}} {
			writeFile(t, filepath.Join(p.root, "example", "package.json"), `{"name":"example","pi-subagents":{"agents":["agents"]}}`)
			writeFile(t, filepath.Join(p.root, "example", "agents", p.name+".md"), agent(p.name))
		}
		writeFile(t, filepath.Join(localDir, "local-specialist.md"), agent("local-specialist"))
		release := filepath.Join(temp, "release")
		oldDone := filepath.Join(temp, "old-lookup-finished")
		// The fake npm answers once its phase is released (a file), so the test controls when each lookup finishes.
		q := func(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }
		script := "#!/bin/sh\nphase=\"$TEST_NPM_PHASE\"\n" +
			"if [ \"$phase\" = failure ]; then echo 'private npm failure' >&2; exit 1; fi\n" +
			"while [ ! -e \"" + release + "/$phase\" ]; do sleep 0.01; done\n" +
			"if [ \"$phase\" = old ]; then echo done > " + q(oldDone) + "; fi\n" +
			"if [ \"$phase\" = latest ]; then echo " + q(newerRoot) + "; else echo " + q(globalRoot) + "; fi\n"
		writeFile(t, filepath.Join(bin, "npm"), script)
		if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(release, 0o755); err != nil {
			t.Fatal(err)
		}
		releasePhase := func(phase string) { writeFile(t, filepath.Join(release, phase), "") }
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("APPDATA", filepath.Join(temp, "missing-appdata"))
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("TEST_NPM_PHASE", "initial")
		sub.ResetNpmRootCaches()
		t.Cleanup(sub.ResetNpmRootCaches)
		cwd := filepath.Join(fixture, "project")
		sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
			return sub.ChildResult{Stdout: "ok"}, nil
		})
		h := StartHost(t, Extension(), advHostOptions(cwd, "barrier-test"))

		type outcome struct {
			raw, failure string
			sections     map[string]string
		}
		goTool := func(name string, params map[string]any) <-chan outcome {
			c := make(chan outcome, 1)
			go func() {
				raw, failure := h.Tool(name, params)
				c <- outcome{raw: string(raw), failure: failure}
			}()
			return c
		}
		goPrompt := func() <-chan outcome {
			c := make(chan outcome, 1)
			go func() {
				sections, failure, _ := agentStart(h, []string{"subagent"}, nil)
				c <- outcome{sections: sections, failure: failure}
			}()
			return c
		}
		pending := func(c <-chan outcome) bool {
			select {
			case <-c:
				return false
			default:
				return true
			}
		}

		sessionStart(h, "startup") // returns while the npm lookup still runs: the session is not blocked
		firstPrompt := goPrompt()
		enabled := goTool("subagents_enable", map[string]any{})
		firstExecution := goTool("subagent", map[string]any{"agent": "global-specialist", "task": "Probe", "async": false})
		firstList := goTool("subagent", map[string]any{"action": "list"})
		time.Sleep(100 * time.Millisecond)
		eq(t, pending(firstPrompt), true, "first prompt returned before global lookup")
		releasePhase("initial")
		prompt, loader, run, listed := <-firstPrompt, <-enabled, <-firstExecution, <-firstList
		mustMatch(t, run.raw+run.failure, `global-specialist`)
		mustNotMatch(t, run.raw+run.failure, `(?i)Unknown agent|not found`)
		mustMatch(t, listed.raw, `global-specialist`)
		for _, text := range []string{prompt.sections["advertised_subagents"], contentOf(loader.raw)} {
			mustMatch(t, text, `<name>global-specialist</name>`)
			mustMatch(t, text, `<name>local-specialist</name>`)
		}
		mustMatch(t, contentOf(loader.raw), `only after the next user prompt, so do not retry it in this one\. The operator can start Pi with --exclude-tools subagents_enable`)

		t.Setenv("TEST_NPM_PHASE", "seed")
		releasePhase("seed")
		seeded := sub.DiscoverAgents(cwd, sub.ScopeBoth)
		eq(t, seeded.Err == nil, true, "seed discovery")
		found := false
		for _, a := range seeded.Agents {
			found = found || a.Name == "global-specialist"
		}
		eq(t, found, true, "synchronous discovery finds the global specialist")

		t.Setenv("TEST_NPM_PHASE", "old")
		sessionStart(h, "reload")
		time.Sleep(50 * time.Millisecond)
		waitingOnOld := goPrompt()
		waitingLoader := goTool("subagents_enable", map[string]any{})
		t.Setenv("TEST_NPM_PHASE", "latest")
		releasePhase("latest")
		sessionStart(h, "reload")
		mergedExecution := goTool("subagent", map[string]any{"agent": "new-specialist", "task": "Probe", "async": false})
		latest, reloaded := <-waitingOnOld, <-waitingLoader
		if _, err := os.Stat(oldDone); err == nil {
			t.Error("old lookup held the new session's prompt")
		}
		mustMatch(t, latest.sections["advertised_subagents"], `<name>new-specialist</name>`)
		mustMatch(t, contentOf(reloaded.raw), `<name>new-specialist</name>`)
		mustNotMatch(t, latest.sections["advertised_subagents"], `<name>global-specialist</name>`)
		list, _ := h.Tool("subagent", map[string]any{"action": "list"})
		mustMatch(t, string(list), `new-specialist`)
		mustNotMatch(t, string(list), `global-specialist`)
		get, _ := h.Tool("subagent", map[string]any{"action": "get", "agent": "new-specialist"})
		mustMatch(t, string(get), `new-specialist`)
		merged := <-mergedExecution
		mustMatch(t, merged.raw+merged.failure, `new-specialist`)
		mustNotMatch(t, merged.raw+merged.failure, `(?i)Unknown agent|not found`)
		// The runtime agent registry of the original has no counterpart in this port (it registers agents from other extensions).

		releasePhase("old")
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(oldDone); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("old lookup never completed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		afterStale := <-goPrompt()
		mustMatch(t, afterStale.sections["advertised_subagents"], `<name>new-specialist</name>`)
		mustNotMatch(t, afterStale.sections["advertised_subagents"], `<name>global-specialist</name>`)

		t.Setenv("TEST_NPM_PHASE", "failure")
		sessionStart(h, "reload")
		local := <-goPrompt()
		mustMatch(t, local.sections["advertised_subagents"], `<name>local-specialist</name>`)
		mustNotMatch(t, local.sections["advertised_subagents"], `<name>(global|new)-specialist</name>`)
		writeFile(t, filepath.Join(home, "settings.json"), "{")
		sessionStart(h, "reload")
		_, failure := h.Tool("subagent", map[string]any{"action": "list"})
		mustMatch(t, failure, `Failed to parse settings file`)
	})
}

var _ sync.Mutex
