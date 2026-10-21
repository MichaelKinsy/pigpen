package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of test/workflow-tool.test.ts: the tool's input contract, the flag, the master switch and the collisions,
// driven through the real extension over the fake host. (The host-adapter half is in workflow_host_twin_test.go.)

const (
	inlineScript = "export const meta = { name: \"from-inline\", description: \"inline source\" };\n"
	fileScript   = "export const meta = { name: \"from-file\", description: \"file source\" };\n"
)

// wfRig is a rig whose host also answers the calls a workflow makes: the tool registry, the flag, the session id.
type wfRig struct {
	*rig
	dir      string
	agentDir string
	mu2      sync.Mutex
	allTools []obj // what getAllTools answers; nil makes the call fail (a host where the listing is unavailable)
	failList bool
	flag     any
	active   []string
	setCalls [][]string
}

type wfRigOptions struct {
	settings string // the project's .pi/subagents.json
	hasUI    bool
	flag     any
}

// startWfRig boots the real extension in a hermetic project directory: the settings are read from the process's
// working directory at registration (the workspace of an extension process is fixed), so the test changes into it.
func startWfRig(t *testing.T, opts wfRigOptions) *wfRig {
	t.Helper()
	dir := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	if opts.settings != "" {
		_ = os.MkdirAll(filepath.Join(dir, ".pi"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, ".pi", "subagents.json"), []byte(opts.settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	resetTypes()
	t.Cleanup(resetTypes)
	w := &wfRig{dir: dir, agentDir: agentDir, flag: opts.flag}
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	w.rig = r
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	r.app.register(e)
	hasUI := opts.hasUI
	r.Host = StartHost(t, e, HostOptions{Cwd: dir, HasUI: &hasUI, OnCallValue: w.answer})
	t.Cleanup(func() { r.app.shutdownWorkflows(); r.app.mgr.abortAll() })
	return w
}

func (w *wfRig) answer(method string, args map[string]any) (any, string) {
	w.mu2.Lock()
	defer w.mu2.Unlock()
	switch method {
	case "getAllTools":
		if w.failList {
			return nil, "Extension runtime not initialized."
		}
		return obj{"tools": w.allTools}, ""
	case "getActiveTools":
		active := w.active
		if active == nil {
			active = []string{}
			for name := range w.rig.tools {
				active = append(active, name)
			}
		}
		return obj{"tools": active}, ""
	case "setActiveTools":
		var names []string
		for _, n := range args["tools"].([]any) {
			names = append(names, n.(string))
		}
		w.active = names
		w.setCalls = append(w.setCalls, names)
		return obj{}, ""
	case "getFlag":
		if w.flag != nil {
			return obj{"value": w.flag}, ""
		}
		return obj{}, ""
	case "getSessionID":
		return obj{"sessionId": "sess-1"}, ""
	}
	w.mu2.Unlock()
	defer w.mu2.Lock()
	return w.rig.answer(method, args)
}

func (w *wfRig) start() { w.Fire("session_start", obj{"reason": "startup"}) }

func (w *wfRig) notices() []obj {
	var out []obj
	for _, c := range w.CallsTo("ui.notify") {
		out = append(out, c.Args)
	}
	return out
}

func (w *wfRig) warnings() []string {
	var out []string
	for _, n := range w.notices() {
		if n["level"] == "warning" {
			out = append(out, n["message"].(string))
		}
	}
	return out
}

func (w *wfRig) noticeTexts() string {
	var out []string
	for _, n := range w.notices() {
		out = append(out, n["message"].(string))
	}
	return strings.Join(out, "\n")
}

// run executes the SubagentWorkflow tool and returns its text.
func (w *wfRig) run(params obj) string {
	w.t.Helper()
	return w.must(workflowToolName, params)
}

func (w *wfRig) taskID(text string) string {
	m := regexp.MustCompile(`Task ID: (\S+)`).FindStringSubmatch(text)
	if m == nil {
		w.t.Fatalf("no task id in %q", text)
	}
	return m[1]
}

// notification waits for the completion message a background run sends for taskID.
func (w *wfRig) notification(taskID string) obj {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range w.messages() {
			if msg, _ := m["message"].(map[string]any); msg != nil && strings.Contains(asString(msg["content"]), taskID) {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.t.Fatalf("no notification for %s", taskID)
	return nil
}

func asString(v any) string { s, _ := v.(string); return s }

func (w *wfRig) entries() []obj {
	var out []obj
	for _, c := range w.CallsTo("appendEntry") {
		out = append(out, c.Args)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(t *testing.T, text, pattern string) bool {
	t.Helper()
	return regexp.MustCompile(pattern).MatchString(text)
}

func TestWorkflowTool(t *testing.T) {
	const f = "workflow-tool"

	tw(t, f, "excludes itself from subagents, so a workflow cannot recurse into itself", func(t *testing.T) {
		// The original derives an exclusion list from its tool-name table. Here a child registers nothing at all
		// (TINTINWEB_SUBAGENT_DEPTH), so no tool, the workflow tool included, reaches a child.
		eq(t, workflowToolName, "SubagentWorkflow")
		t.Setenv(depthEnv, "1")
		e := Extension()
		eq(t, e != nil, true)
		host := StartHost(t, e, HostOptions{})
		eq(t, len(host.tools), 0)
	})

	// SubagentWorkflow tool — script vs scriptPath vs name
	tw(t, f, "runs the inline script when only `script` is given, and persists it", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		text := w.run(obj{"script": inlineScript})
		eq(t, has(t, text, `Workflow "from-inline" started`), true)
		var scriptLine string
		for _, l := range strings.Split(text, "\n") {
			if v, ok := strings.CutPrefix(l, "Script: "); ok {
				scriptLine = v
			}
		}
		// the persisted script path is what makes iteration edit-then-rerun
		if scriptLine == "" {
			t.Fatal("no Script line")
		}
		if _, err := os.Stat(scriptLine); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, f, "prefers scriptPath over script when both are given", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		path := filepath.Join(w.dir, "wf.js")
		writeFile(t, path, fileScript)
		text := w.run(obj{"script": inlineScript, "scriptPath": path})
		eq(t, has(t, text, `Workflow "from-file" started`), true)
		eq(t, strings.Contains(text, "from-inline"), false)
	})
	tw(t, f, "resolves a relative scriptPath against the project directory", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, "flows", "wf.js"), fileScript)
		eq(t, has(t, w.run(obj{"scriptPath": filepath.Join("flows", "wf.js")}), `Workflow "from-file" started`), true)
	})
	tw(t, f, "reports a missing scriptPath instead of silently falling back to `script`", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		text := w.run(obj{"script": inlineScript, "scriptPath": filepath.Join(w.dir, "nope.js")})
		eq(t, has(t, text, `Could not read workflow script`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "asks for one of the three when none is given", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		eq(t, has(t, w.run(obj{}), "(?s)Provide `script`.*`scriptPath`.*or `name`"), true)
	})
	tw(t, f, "runs a saved workflow by name from .pi/workflows", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "nightly.js"), fileScript)
		text := w.run(obj{"name": "nightly"})
		eq(t, has(t, text, `Workflow "from-file" started`), true)
		// Reported as its own file, so editing and re-running works on a saved one.
		eq(t, strings.Contains(text, filepath.Join(".pi", "workflows", "nightly.js")), true)
	})
	tw(t, f, "lets script and scriptPath both outrank name", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "nightly.js"), fileScript)
		eq(t, has(t, w.run(obj{"script": inlineScript, "name": "nightly"}), `Workflow "from-inline" started`), true)
	})
	tw(t, f, "names the saved workflows it does have when the name is unknown", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "nightly.js"), fileScript)
		text := w.run(obj{"name": "nightlyy"})
		eq(t, has(t, text, `No saved workflow named "nightlyy"`), true)
		eq(t, has(t, text, `Available: nightly\.`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "falls back to the user's agent dir, and lets the project shadow it", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.agentDir, "workflows", "shared.js"), fileScript)
		eq(t, has(t, w.run(obj{"name": "shared"}), `Workflow "from-file" started`), true)
		// Same name in the project wins — .pi stays the project authority.
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "shared.js"), inlineScript)
		eq(t, has(t, w.run(obj{"name": "shared"}), `Workflow "from-inline" started`), true)
	})
	tw(t, f, "refuses a file in the folder that is not a workflow", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "utils.js"), "module.exports = { helper: 1 };\n")
		text := w.run(obj{"name": "utils"})
		eq(t, has(t, text, `is not a workflow script`), true)
		eq(t, has(t, text, `Nothing was run`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "leaves non-workflow files out of the listing entirely", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "utils.js"), "const x = 1;\n")
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "nightly.js"), fileScript)
		text := w.run(obj{"name": "nope"})
		eq(t, has(t, text, `Available: nightly\.`), true)
		eq(t, has(t, text, `utils`), false)
	})
	tw(t, f, "does not shadow a real workflow with a same-named non-workflow deeper down", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.agentDir, "workflows", "shared.js"), fileScript)
		writeFile(t, filepath.Join(w.dir, ".pi", "workflows", "shared.js"), "// not a workflow\n")
		text := w.run(obj{"name": "shared"})
		eq(t, has(t, text, `is not a workflow script`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "refuses to read a name out of its directories", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, "escaped.js"), fileScript)
		text := w.run(obj{"name": "../escaped"})
		eq(t, has(t, text, `is not a usable workflow name`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "refuses a run id this session never issued", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		text := w.run(obj{"script": inlineScript, "resumeFromRunId": "wf_deadbeef1234"})
		// An unknown id is an error, not a cold start: a caller that asked to resume is expecting not to pay.
		eq(t, has(t, text, `No workflow run "wf_deadbeef1234" in this session`), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "refuses to resume a run that is still going", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		started := w.run(obj{"script": inlineScript + "await new Promise(() => {});\n"})
		eq(t, has(t, w.run(obj{"script": inlineScript, "resumeFromRunId": w.taskID(started)}), `is still running`), true)
	})
	tw(t, f, "accepts a settled run and says plainly that there was nothing to replay", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		runID := w.taskID(w.run(obj{"script": inlineScript}))
		// The run is detached: it has to actually settle before it can be resumed.
		var text string
		for attempt := 0; attempt < 100; attempt++ {
			text = w.run(obj{"script": inlineScript, "resumeFromRunId": runID})
			if !has(t, text, `is still running`) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		// That script spawns no agents, so its journal is empty. Saying so beats reporting a resume that replayed nothing.
		eq(t, has(t, text, "Nothing to replay from "+runID), true)
		eq(t, has(t, text, `started in the background`), true)
	})
	tw(t, f, "ignores title and description rather than rejecting them", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		eq(t, has(t, w.run(obj{"script": inlineScript, "title": "Nightly Audit", "description": "does the thing"}), `Workflow "from-inline" started`), true)
	})
	tw(t, f, "reports a bad `meta` up front rather than as a background run that failed", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		text := w.run(obj{"script": "const x = 1;\n"})
		eq(t, has(t, text, "must begin with .export const meta"), true)
		eq(t, has(t, text, `started`), false)
	})
	tw(t, f, "names the workflow on the call line", func(t *testing.T) {
		// Adaptation: the call line is drawn by the UI lane (a ToolRenderer); this is its data, the name the line carries.
		eq(t, workflowCallName(inlineScript, "", ""), "from-inline")
		eq(t, workflowCallName("", "flows/wf.js", ""), "wf.js")
		eq(t, workflowCallName("", "", "nightly"), "nightly")
	})
	tw(t, f, "actually runs the script in the background and notifies through the agent channel", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		text := w.run(obj{"script": inlineScript + "log(\"scanned 3 files\");\nreturn \"done here\";\n"})
		taskID := w.taskID(text)
		// The tool returned before the run finished — the completion has to arrive on its own, through the same channel
		// a background agent uses.
		sent := w.notification(taskID)
		msg := sent["message"].(map[string]any)
		eq(t, msg["customType"], "subagent-notification")
		opts := sent["options"].(map[string]any)
		eq(t, [2]any{opts["deliverAs"], opts["triggerTurn"]}, [2]any{"followUp", true})
		eq(t, strings.Contains(asString(msg["content"]), "<result>done here</result>"), true)
		task := w.app.workflows().tasks.get(taskID)
		eq(t, strings.Contains(strings.Join(task.Logs, "\n"), "scanned 3 files"), true)
		eq(t, task.Status, "completed")
	})
	tw(t, f, "kills a still-running workflow (and its worker thread) on session shutdown", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		// A never-returning script: only the run's own abort can stop it, so abortAll() over the agent records would
		// leave the worker spinning.
		taskID := w.taskID(w.run(obj{"script": inlineScript + "await new Promise(() => {});\n"}))
		task := w.app.workflows().tasks.get(taskID)
		w.Fire("session_shutdown", obj{})
		sent := w.notification(taskID)
		eq(t, strings.Contains(asString(sent["message"].(map[string]any)["content"]), "<status>Stopped</status>"), true)
		eq(t, task.Status, "killed")
	})
	tw(t, f, "reports a script that threw, rather than a run that quietly ended", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		taskID := w.taskID(w.run(obj{"script": inlineScript + "throw new Error(\"script blew up\");\n"}))
		sent := w.notification(taskID)
		msg := sent["message"].(map[string]any)
		eq(t, strings.Contains(asString(msg["content"]), "<status>Error:"), true)
		details := msg["details"].(map[string]any)
		eq(t, details["status"], "error")
		// The reason has to be the *result* too, not only the status line: that is what the collapsed row shows.
		eq(t, strings.Contains(asString(details["resultPreview"]), "script blew up"), true)
	})
	tw(t, f, "reports a run that could not start at all", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		// A control character is rejected before the worker is created, so this never reaches the run's own error path.
		taskID := w.taskID(w.run(obj{"script": inlineScript + "const x = \"\u0007\";\n"}))
		sent := w.notification(taskID)
		msg := sent["message"].(map[string]any)
		eq(t, strings.Contains(asString(msg["content"]), "control characters"), true)
		eq(t, msg["details"].(map[string]any)["status"], "error")
	})

	// --subagents-workflow-file
	tskip(t, f, "registers the fleet row as soon as a run starts, not when it settles", "the fleet list is a UI surface (ui/fleet-list.ts, the UI lane's G5); the run is registered in the task store the moment the tool starts it, which the other twins here read")
	tskip(t, f, "captures the UI at session_start, before any tool has executed", "asserts that the fleet list hooks terminal input from a UI captured at session_start (ui/fleet-list.ts); the Go extension takes the context from every event (app.touch), and the fleet list is the UI lane's")
	tw(t, f, "registers the flag at activation but does not read it there", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{flag: "ignored-at-activation.js"})
		// The host applies CLI values only AFTER every extension factory has run, so reading here would see the
		// registered default and never the real value: nothing reads the flag before session_start.
		eq(t, len(w.CallsTo("getFlag")), 0)
		eq(t, workflowFileFlag, "subagents-workflow-file")
	})
	tw(t, f, "reads the flag from session_start", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		path := filepath.Join(w.dir, "flow.js")
		writeFile(t, path, fileScript)
		w.flag = path
		w.start()
		eq(t, len(w.CallsTo("getFlag")) > 0, true)
		eq(t, w.CallsTo("getFlag")[0].Args["name"], workflowFileFlag)
	})
	tw(t, f, "explains the `=` form when the flag arrives bare as boolean true", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true, flag: true})
		w.start()
		eq(t, strings.Contains(w.noticeTexts(), "--"+workflowFileFlag+"=<path>"), true)
		eq(t, len(w.entries()), 0)
	})
	tw(t, f, "does nothing at all when the flag was not passed", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.start()
		time.Sleep(100 * time.Millisecond)
		eq(t, len(w.notices()), 0)
		eq(t, len(w.entries()), 0)
	})
	tw(t, f, "reports a script it cannot read without starting a run", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.flag = filepath.Join(w.dir, "absent.js")
		w.start()
		time.Sleep(100 * time.Millisecond)
		eq(t, has(t, w.noticeTexts(), `Could not read`), true)
		eq(t, len(w.entries()), 0)
	})
	tw(t, f, "renders the finished run as a session entry and hands it to the model as next-turn context", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		path := filepath.Join(w.dir, "flow.js")
		writeFile(t, path, fileScript+"log(\"scanned\");\nreturn \"all clear\";\n")
		w.flag = path
		w.start()
		var entry obj
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && entry == nil {
			for _, e := range w.entries() {
				if e["customType"] == workflowEntryType {
					entry = e
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if entry == nil {
			t.Fatal("no entry appended")
		}
		data := entry["data"].(map[string]any)
		eq(t, [2]any{data["name"], data["status"]}, [2]any{"from-file", "completed"})
		// The card is rebuilt from this log, so the entry has to carry it.
		eq(t, data["progress"], []any{map[string]any{"type": "workflow_log", "message": "scanned"}})
		// nextTurn, not followUp: a flag-launched run puts its result in context without forcing a turn.
		var sent obj
		for i := 0; i < 500 && sent == nil; i++ {
			for _, m := range w.messages() {
				if msg, _ := m["message"].(map[string]any); msg != nil && msg["customType"] == "workflow-result" {
					sent = m
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if sent == nil {
			t.Fatal("the run's outcome must reach the model")
		}
		eq(t, sent["options"].(map[string]any)["deliverAs"], "nextTurn")
		eq(t, strings.Contains(asString(sent["message"].(map[string]any)["content"]), "<result>all clear</result>"), true)
	})
	tskip(t, f, "renders the persisted entry through the shared workflow card layout", "draws a TUI card from an entry (ui/workflow-card.ts renderWorkflowEntryCard, the UI lane's); the entry's data shape is what the previous case asserts")

	// the master switch
	tw(t, f, "is on with no setting at all", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		eq(t, w.tools[workflowToolName], true)
		// Nothing else is affected.
		eq(t, w.tools["Agent"], true)
	})
	tw(t, f, "stays off when the setting says so explicitly", func(t *testing.T) {
		// Not registered at all: the model is never told the feature exists.
		eq(t, startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": false}`}).tools[workflowToolName], false)
	})
	tw(t, f, "registers the tool once the setting turns it on", func(t *testing.T) {
		eq(t, startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`}).tools[workflowToolName], true)
	})
	tw(t, f, "refuses the startup flag while off, instead of running the script anyway", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true, settings: `{"workflowsEnabled": false}`})
		path := filepath.Join(w.dir, "flow.js")
		writeFile(t, path, fileScript)
		w.flag = path
		w.start()
		eq(t, has(t, strings.ToLower(w.noticeTexts()), `workflows are off`), true)
		eq(t, len(w.entries()), 0)
	})

	// collisions with another extension
	foreign := func(name string) obj {
		return obj{"name": name, "description": "some other extension's workflow tool", "sourceInfo": obj{"source": "other-ext", "path": "/x/other-ext.ts"}}
	}
	warnMatching := func(w *wfRig, pattern string) bool {
		for _, m := range w.warnings() {
			if regexp.MustCompile(pattern).MatchString(m) {
				return true
			}
		}
		return false
	}
	tw(t, f, "warns when another extension already owns the tool name", func(t *testing.T) {
		// Pi keeps the FIRST registration per tool name, across extensions. Ours never reaches the registry.
		w := startWfRig(t, wfRigOptions{hasUI: true, settings: `{"workflowsEnabled": true}`})
		w.allTools = []obj{{"name": "SubagentWorkflow", "description": "some other extension's workflow tool", "sourceInfo": obj{"source": "other-ext", "path": "/x/other.ts"}}}
		w.start()
		eq(t, warnMatching(w, `already registers a "SubagentWorkflow" tool`), true)
		eq(t, warnMatching(w, `other-ext`), true)
	})
	tw(t, f, "stays quiet when the registered tool is our own", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true, settings: `{"workflowsEnabled": true}`})
		w.allTools = []obj{{"name": "SubagentWorkflow", "description": w.app.workflows().ownDescription, "sourceInfo": obj{"source": "pi-subagents"}}}
		w.start()
		eq(t, warnMatching(w, `SubagentWorkflow`), false)
	})
	tw(t, f, "does not check the tool name while workflows are off", func(t *testing.T) {
		// Nothing of ours is registered, so another extension owning the name is simply not our business to complain about.
		w := startWfRig(t, wfRigOptions{hasUI: true, settings: `{"workflowsEnabled": false}`})
		w.allTools = []obj{{"name": "SubagentWorkflow", "description": "someone else's", "sourceInfo": obj{"source": "other-ext"}}}
		w.start()
		eq(t, warnMatching(w, `SubagentWorkflow`), false)
	})
	tw(t, f, "survives a host where the listing methods are unavailable", func(t *testing.T) {
		// print mode and RPC mode do not bind them; a diagnostic must not be the thing that takes the session down.
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.failList = true
		w.start()
		eq(t, w.warnings(), []string(nil))
	})
	tw(t, f, "withdraws its tool when another extension provides a `Workflow` tool", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		// Registered, because the collision is only visible after every extension has loaded.
		eq(t, w.tools[workflowToolName], true)
		w.allTools = []obj{foreign("Workflow")}
		w.start()
		// Withdrawn for real: pi rebuilds the system prompt from the active set, and session_start runs before any turn.
		if len(w.setCalls) == 0 {
			t.Fatal("setActiveTools was not called")
		}
		for _, n := range w.active {
			if n == workflowToolName {
				t.Fatal("still active")
			}
		}
		// Everything else this extension registered stays.
		eq(t, strings.Contains(strings.Join(w.active, ","), "Agent"), true)
		eq(t, warnMatching(w, `already provides a "Workflow" tool`), true)
		eq(t, warnMatching(w, `other-ext`), true)
	})
	tw(t, f, "names the setting that keeps both", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.allTools = []obj{foreign("Workflow")}
		w.start()
		eq(t, warnMatching(w, `workflowsEnabled`), true)
	})
	tw(t, f, "stands down without withdrawing when the foreign tool took our own name", func(t *testing.T) {
		// First registration wins, so ours never reached the registry: nothing to withdraw, but the menu and the flag
		// would still be live, so the feature comes down anyway.
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.allTools = []obj{foreign("SubagentWorkflow")}
		w.start()
		eq(t, len(w.setCalls), 0)
		eq(t, warnMatching(w, `already provides a "SubagentWorkflow" tool`), true)
	})
	tw(t, f, "refuses the startup flag once it has stood down", func(t *testing.T) {
		// The flag is the same machinery by another door; the collision check runs first so this door closes with the tool.
		w := startWfRig(t, wfRigOptions{hasUI: true})
		path := filepath.Join(w.dir, "flow.js")
		writeFile(t, path, fileScript)
		w.flag = path
		w.allTools = []obj{foreign("Workflow")}
		w.start()
		eq(t, has(t, strings.ToLower(w.noticeTexts()), `workflows are off`), true)
		eq(t, len(w.entries()), 0)
	})
	tw(t, f, "keeps ours when the setting pins workflows on", func(t *testing.T) {
		// A default yields to what else is loaded. A choice does not.
		w := startWfRig(t, wfRigOptions{hasUI: true, settings: `{"workflowsEnabled": true}`})
		w.allTools = []obj{foreign("Workflow")}
		w.start()
		eq(t, len(w.setCalls), 0)
		eq(t, warnMatching(w, `disabled for this session`), false)
		eq(t, w.app.workflows().isEnabled(), true)
	})
	tw(t, f, "does not persist a stand-down as an explicit setting", func(t *testing.T) {
		// The stand-down is scoped to the session that detected it. The value the settings write-back stores is the
		// user's answer, not the effective one (the settings lane's write path reads workflowsSettingValue).
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.allTools = []obj{foreign("Workflow")}
		w.start()
		_, write := w.app.workflows().settingValue()
		eq(t, write, false)
	})
	tw(t, f, "still persists the setting when the user pinned it", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": false}`})
		value, write := w.app.workflows().settingValue()
		eq(t, [2]bool{value, write}, [2]bool{false, true})
	})
	tw(t, f, "ignores tool names that merely contain the word", func(t *testing.T) {
		// A substring test would turn any CI integration into a silent shutdown of this feature.
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.allTools = []obj{foreign("list_workflows"), foreign("github_workflow_run"), foreign("WorkflowTemplate")}
		w.start()
		eq(t, len(w.setCalls), 0)
		eq(t, w.app.workflows().isEnabled(), true)
		eq(t, w.warnings(), []string(nil))
	})
}
