package tintinweb_subagents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twin-style coverage of behaviors of the original that the first mutation run found untested: each case is the
// original's behavior for the keyword, key or text named, with its own input and expected value.

func wfObjectSchema(prop map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"v": prop}}
}

func TestWorkflowSchemaKeywords(t *testing.T) {
	valid := func(schema map[string]any, v any) bool {
		return wfCompileOK(t, schema).Check(map[string]any{"v": v}) == ""
	}
	t.Run("enum", func(t *testing.T) {
		s := wfObjectSchema(map[string]any{"enum": []any{"x", "y"}})
		eq(t, valid(s, "x"), true)
		eq(t, valid(s, "z"), false)
	})
	t.Run("minimum is inclusive", func(t *testing.T) {
		s := wfObjectSchema(map[string]any{"type": "number", "minimum": float64(1)})
		eq(t, valid(s, float64(1)), true)
		eq(t, valid(s, float64(0)), false)
	})
	t.Run("integer rejects a fraction", func(t *testing.T) {
		s := wfObjectSchema(map[string]any{"type": "integer"})
		eq(t, valid(s, float64(2)), true)
		eq(t, valid(s, 1.5), false)
	})
	t.Run("additionalProperties false", func(t *testing.T) {
		s := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}, "additionalProperties": false}
		c := wfCompileOK(t, s)
		eq(t, c.Check(map[string]any{"a": float64(1)}), "")
		eq(t, c.Check(map[string]any{"a": float64(1), "b": float64(2)}) != "", true)
	})
	t.Run("anyOf needs one match, not all", func(t *testing.T) {
		s := wfObjectSchema(map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}})
		eq(t, valid(s, "s"), true)
		eq(t, valid(s, float64(1)), true)
		eq(t, valid(s, true), false)
	})
	t.Run("uniqueItems", func(t *testing.T) {
		s := wfObjectSchema(map[string]any{"type": "array", "uniqueItems": true})
		eq(t, valid(s, []any{float64(1), float64(2)}), true)
		eq(t, valid(s, []any{float64(1), float64(1)}), false)
	})
	t.Run("an unknown type is not a schema this runtime can validate", func(t *testing.T) {
		eq(t, compileWorkflowSchema(wfObjectSchema(map[string]any{"type": "bogus"})).OK, false)
	})
	t.Run("only the first errors are quoted back", func(t *testing.T) {
		props := map[string]any{}
		value := map[string]any{}
		for _, k := range []string{"p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09", "p10"} {
			props[k] = map[string]any{"type": "string"}
			value[k] = float64(1)
		}
		verdict := wfCompileOK(t, map[string]any{"type": "object", "properties": props}).Check(value)
		eq(t, strings.Count(verdict, "$.p"), workflowMaxReportedErrors)
	})
}

func TestWorkflowJournalLines(t *testing.T) {
	t.Run("a blank line between entries is skipped", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "j.jsonl")
		appendWorkflowJournal(path, workflowJournalEntry{Index: 0, Key: "a", OK: true, Text: wfStr("x")})
		appendWorkflowJournal(path, workflowJournalEntry{Index: 1, Key: "b", OK: false})
		data, _ := os.ReadFile(path)
		_ = os.WriteFile(path, []byte(strings.Replace(string(data), "\n", "\n\n", 1)), 0o644)
		eq(t, len(readWorkflowJournal(path)), 2)
	})
	t.Run("a non-string text and a false resumed are not entries", func(t *testing.T) {
		_, ok := workflowJournalEntryOf(map[string]any{"index": float64(0), "key": "k", "ok": true, "text": float64(5)})
		eq(t, ok, false)
		_, ok = workflowJournalEntryOf(map[string]any{"index": float64(-1), "key": "k", "ok": true})
		eq(t, ok, false)
		_, ok = workflowJournalEntryOf(map[string]any{"index": float64(0), "key": "k", "ok": true, "resumed": false})
		eq(t, ok, false)
		e, ok := workflowJournalEntryOf(map[string]any{"index": float64(0), "key": "k", "ok": true, "resumed": true})
		eq(t, ok && e.Resumed, true)
	})
	t.Run("a written line keeps <, > and & as JSON.stringify does", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "j.jsonl")
		appendWorkflowJournal(path, workflowJournalEntry{Index: 0, Key: "<a&b>", OK: true})
		data, _ := os.ReadFile(path)
		eq(t, strings.Contains(string(data), "<a&b>"), true)
	})
}

func TestWorkflowSavedNames(t *testing.T) {
	t.Run("a name with a slash or past 128 characters is unsafe", func(t *testing.T) {
		eq(t, wfIsUnsafeName("a/b"), true)
		eq(t, wfIsUnsafeName(strings.Repeat("a", 128)), false)
		eq(t, wfIsUnsafeName(strings.Repeat("a", 129)), true)
	})
	t.Run("a symlink is not read", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(dir, "real.js")
		_ = os.WriteFile(real, []byte("x"), 0o644)
		link := filepath.Join(dir, "link.js")
		if err := os.Symlink(real, link); err != nil {
			t.Skip("no symlinks here")
		}
		_, ok := wfSafeReadFile(link)
		eq(t, ok, false)
		_, ok = wfSafeReadFile(real)
		eq(t, ok, true)
	})
	t.Run("the listing is sorted and leaves out a non-workflow", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		cwd := t.TempDir()
		writeFile(t, filepath.Join(cwd, ".pi", "workflows", "zeta.js"), fileScript)
		writeFile(t, filepath.Join(cwd, ".pi", "workflows", "alpha.js"), fileScript)
		writeFile(t, filepath.Join(cwd, ".pi", "workflows", "utils.js"), "const x = 1;\n")
		for _, n := range []string{"mid", "beta", "omega", "gamma"} {
			writeFile(t, filepath.Join(cwd, ".pi", "workflows", n+".js"), fileScript)
		}
		eq(t, listSavedWorkflows(cwd), []string{"alpha", "beta", "gamma", "mid", "omega", "zeta"})
	})
	t.Run("an unknown name lists the saved ones", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		cwd := t.TempDir()
		writeFile(t, filepath.Join(cwd, ".pi", "workflows", "nightly.js"), fileScript)
		r := resolveWorkflowSource(workflowRef{Name: "nope"}, cwd)
		eq(t, r.OK, false)
		eq(t, strings.Contains(r.Message, "nightly"), true)
	})
	t.Run("a relative scriptPath is joined to the project directory", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		cwd := t.TempDir()
		writeFile(t, filepath.Join(cwd, "flows", "wf.js"), fileScript)
		eq(t, resolveWorkflowSource(workflowRef{ScriptPath: filepath.Join("flows", "wf.js")}, cwd).OK, true)
	})
}

func TestWorkflowMetaScanning(t *testing.T) {
	t.Run("a phase needs a non-blank title", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a', description: 'b', phases: [{ title: '  ' }] }\nreturn 1", "phases\\[0\\]\\.title")
		wfMetaErr(t, "export const meta = { name: 'a', description: 'b', phases: [{ detail: 'x' }] }\nreturn 1", "phases\\[0\\]\\.title")
	})
	t.Run("the declaration must start its line", func(t *testing.T) {
		wfMetaErr(t, "const x = 1; export const meta = { name: 'a', description: 'b' }\nreturn 1", "must begin with")
	})
	t.Run("a slash after a value divides, after an operator it opens a regex", func(t *testing.T) {
		eq(t, isMetaRegexPosition("a / b", 2), false)
		eq(t, isMetaRegexPosition("(a) / b", 4), false)
		eq(t, isMetaRegexPosition("a = /x/", 4), true)
	})
}

func TestWorkflowScriptCannotBreakTheRuntime(t *testing.T) {
	t.Run("patching Array.prototype and Promise does not break the runtime", func(t *testing.T) {
		script := "const boom = function () { throw new Error(\"patched\"); };\n" +
			"Array.prototype.push = boom; Array.prototype.join = boom; Array.prototype.indexOf = boom; Array.prototype.map = boom;\n" +
			"Promise.prototype.catch = boom; globalThis.Promise = boom;\n" +
			"log(\"hi\", 1);\n" +
			"const r = await agent(\"p\", { label: \"l\", effort: \"low\" });\n" +
			"const all = await parallel([async () => 1]);\n" +
			"return r;"
		res := wfRun(t, script, func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, runCompleted)
		eq(t, res.Value, "ok:p")
	})
}

type modelReg struct{ all, avail []*workflowModel }

func (r modelReg) Find(provider, id string) *workflowModel {
	for _, m := range r.all {
		if m.Provider == provider && m.ID == id {
			return m
		}
	}
	return nil
}
func (r modelReg) GetAll() []*workflowModel       { return r.all }
func (r modelReg) GetAvailable() []*workflowModel { return r.avail }

func TestWorkflowModelResolution(t *testing.T) {
	haiku := &workflowModel{Provider: "anthropic", ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"}
	other := &workflowModel{Provider: "p", ID: "abc-1", Name: "Great Model"}
	reg := fakeRegistry{models: []*workflowModel{haiku, other}}
	t.Run("an exact provider/id needs the model to be available", func(t *testing.T) {
		hidden := &workflowModel{Provider: "p", ID: "q"}
		got, msg := wfResolveModel("p/q", modelReg{all: []*workflowModel{hidden, other}, avail: []*workflowModel{other}})
		eq(t, got == nil && strings.HasPrefix(msg, "Model not found"), true)
	})
	t.Run("a name match under 60 still resolves", func(t *testing.T) {
		got, _ := wfResolveModel("great", reg)
		eq(t, got, other)
	})
	t.Run("4.5 and 4-5 are one model", func(t *testing.T) {
		plain := &workflowModel{Provider: "p", ID: "claude-haiku-4-5", Name: "Haiku"}
		got, _ := wfResolveModel("claude-haiku-4.5", fakeRegistry{models: []*workflowModel{plain}})
		eq(t, got, plain)
	})
	t.Run("a trailing date stamp is optional", func(t *testing.T) {
		got, _ := wfResolveModel("claude-haiku-4-5-20250929", reg)
		eq(t, got, haiku)
	})
	t.Run("a provider that does not match falls back to the bare id", func(t *testing.T) {
		got, _ := wfResolveModel("other/claude-haiku", reg)
		eq(t, got, haiku)
	})
	t.Run("the not-found list is sorted", func(t *testing.T) {
		_, msg := wfResolveModel("zzz-none", fakeRegistry{models: []*workflowModel{other, haiku}})
		eq(t, strings.Index(msg, "anthropic/claude-haiku-4-5") < strings.Index(msg, "p/abc-1"), true)
	})
	t.Run("describe drops a Claude prefix", func(t *testing.T) {
		name, id := wfDescribeModel(*haiku)
		eq(t, [2]string{name, id}, [2]string{"haiku 4.5", "anthropic/claude-haiku-4-5"})
	})
	t.Run("a foreign tool with no source is named unknown", func(t *testing.T) {
		v := decideWorkflowCollision([]registeredToolInfo{{Name: "Workflow", Description: "d"}}, "own", false)
		eq(t, strings.Contains(v.Message, "(unknown source)"), true)
	})
}

func TestWorkflowTaskTexts(t *testing.T) {
	t.Run("the result text", func(t *testing.T) {
		task := createWorkflowTask(workflowTaskInit{ID: "wf_a", StartTime: 1})
		eq(t, workflowResultText(task), "No output.")
		task.Value, task.ValueJSON = map[string]any{"a": float64(1)}, []byte(`{"a":1}`)
		eq(t, workflowResultText(task), "{\n  \"a\": 1\n}")
	})
	t.Run("the notification escapes the script path, truncates, and names a replay", func(t *testing.T) {
		task := createWorkflowTask(workflowTaskInit{ID: "wf_a", StartTime: 1, ScriptPath: "a<b>.js", ResumedFrom: "wf_old"})
		task.Status = runCompleted
		long := strings.Repeat("x", 5000)
		task.Value, task.ValueJSON = long, []byte(`"`+long+`"`)
		task.ReplayedCount = 2
		text := formatWorkflowNotification(task, 10)
		eq(t, strings.Contains(text, "<script>a&lt;b&gt;.js</script>"), true)
		eq(t, strings.Contains(text, "\n...(truncated)</result>"), true)
		eq(t, strings.Contains(text, strings.Repeat("x", 4001)), false)
		eq(t, strings.Contains(text, ", 2 replayed from wf_old"), true)
	})
	t.Run("the agent count never walks backwards", func(t *testing.T) {
		task := createWorkflowTask(workflowTaskInit{ID: "wf_a", StartTime: 1})
		task.AgentCount = 5
		updateWorkflowProgressBatch(task, wfEntries(wfAgentRow(0)))
		eq(t, task.AgentCount, 5)
	})
	t.Run("resume targets", func(t *testing.T) {
		tasks := newWorkflowTaskStore()
		done := createWorkflowTask(workflowTaskInit{ID: "wf_done", StartTime: 1})
		done.Status = runCompleted
		tasks.set(done)
		eq(t, strings.Contains(resolveResumeTarget("wf_done", tasks).Message, "no journal"), true)
		eq(t, strings.Contains(resolveResumeTarget("wf_nope", tasks).Message, "wf_done"), true)
	})
}

func TestWorkflowRunBehaviors(t *testing.T) {
	stub := func(o *workflowRunOptions) { o.Host = wfStubHost(nil) }
	t.Run("an agent without a label is named by its first 59 characters", func(t *testing.T) {
		res := wfRun(t, "await agent(\""+strings.Repeat("a", 100)+"\");\nreturn 1;", stub)
		eq(t, wfAgentRows(res.Progress)[0].Label, strings.Repeat("a", 59)+"…")
	})
	t.Run("a long result is previewed at 200 characters", func(t *testing.T) {
		res := wfRun(t, "await agent(\""+strings.Repeat("a", 300)+"\");\nreturn 1;", stub)
		prev := wfLastRow(res.Progress).ResultPreview
		eq(t, len([]rune(prev)), workflowPreviewLength)
		eq(t, strings.HasSuffix(prev, "…"), true)
	})
	t.Run("a schema that cannot compile fails the run", func(t *testing.T) {
		// fatal is what stops parallel() from swallowing it into a null
		res := wfRun(t, "return await parallel([async () => agent(\"x\", { schema: { type: \"array\" } })]);", stub)
		eq(t, res.Status, runFailed)
	})
	t.Run("a nested workflow that cannot be loaded is not fatal", func(t *testing.T) {
		res := wfRun(t, "return await parallel([async () => workflow(\"nope\")]);", func(o *workflowRunOptions) { o.Host = newNestingHost(nil, nil) })
		eq(t, res.Status, runCompleted)
		eq(t, string(res.ValueJSON), "[null]")
	})
	t.Run("a run whose context is already cancelled is killed", func(t *testing.T) {
		res := wfRun(t, "return 1;", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.Ctx = wfCancelledCtx() })
		eq(t, res.Status, runKilled)
	})
	t.Run("an agent option other than worktree for isolation is refused by name", func(t *testing.T) {
		res := wfRun(t, "try { await agent(\"x\", { isolation: \"bogus\" }); } catch (e) { return e.message; }", stub)
		eq(t, strings.Contains(asString(res.Value), `must be "worktree"`), true)
	})
	t.Run("the Function constructor is banned", func(t *testing.T) {
		res := wfRun(t, "try { (function () {}).constructor(\"return 1\")(); return \"allowed\"; } catch (e) { return e.name; }", nil)
		eq(t, res.Value, "EvalError")
	})
}

func TestWorkflowNotificationDetails(t *testing.T) {
	t.Run("a stopped run reports stopped, an error carries its text, a long result is previewed", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		id := w.taskID(w.run(obj{"script": inlineScript + "await new Promise(() => {});\n"}))
		w.Fire("session_shutdown", obj{})
		eq(t, w.notification(id)["message"].(map[string]any)["details"].(map[string]any)["status"], "stopped")

		w2 := startWfRig(t, wfRigOptions{})
		id2 := w2.taskID(w2.run(obj{"script": inlineScript + "throw new Error(\"kaboom\");\n"}))
		d2 := w2.notification(id2)["message"].(map[string]any)["details"].(map[string]any)
		eq(t, strings.Contains(asString(d2["error"]), "kaboom"), true)

		w3 := startWfRig(t, wfRigOptions{})
		id3 := w3.taskID(w3.run(obj{"script": inlineScript + "return \"" + strings.Repeat("y", 600) + "\";\n"}))
		d3 := w3.notification(id3)["message"].(map[string]any)["details"].(map[string]any)
		eq(t, len([]rune(asString(d3["resultPreview"]))), 501)
	})
	t.Run("a string-valued workflowsEnabled pins nothing", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled":"false"}`})
		eq(t, w.app.workflows().isEnabled(), true)
		eq(t, w.app.workflows().isPinned(), false)
	})
}

func TestWorkflowHostResults(t *testing.T) {
	t.Run("a failed record without an error is named by its status, and no tool calls stay unreported", func(t *testing.T) {
		res := wfToSpawnResult(workflowRecord{ID: "a", Status: statusError})
		eq(t, res.Error, "Agent "+statusError+".")
		eq(t, res.ToolCalls == nil, true)
	})
	t.Run("a gate's output is trimmed per stream", func(t *testing.T) {
		m := newStubManager(nil)
		h := hostWith(m, func(d *workflowHostDeps) {
			d.Exec = func(context.Context, string, []string, string, time.Duration) (workflowExecResult, error) {
				return workflowExecResult{Stdout: "  out  \n", Stderr: "\n err \n"}, nil
			}
		})
		g, _ := h.RunGate(context.Background(), "npm test", "", "/x")
		eq(t, g.Output, "out\nerr")
	})
	t.Run("a resume reports the record id and the effective model again", func(t *testing.T) {
		m := newStubManager(func(*spawnCall, func(workflowRecord)) (workflowRecord, error) {
			return record(func(r *workflowRecord) { r.ID = "manager-id-7" }), nil
		})
		m.resume = func(id, prompt string) (workflowRecord, bool, error) {
			return record(func(r *workflowRecord) {
				r.ID = id
				r.Invocation = &workflowInvocation{ModelName: "haiku", ModelID: "anthropic/h"}
			}), true, nil
		}
		h := hostWith(m, nil)
		h.SpawnAgent(wfRequest(nil))
		var got []workflowResolvedInfo
		h.ResumeAgent(context.Background(), "wf-agent-0", "go on", func(i workflowResolvedInfo) { got = append(got, i) })
		eq(t, len(got), 2)
		eq(t, got[0].RecordID, "manager-id-7")
		eq(t, got[1].ModelName, "haiku")
	})
	t.Run("an effective config needs the model name", func(t *testing.T) {
		_, ok := wfResolvedInfo(workflowRecord{Invocation: &workflowInvocation{Thinking: "high"}}, true)
		eq(t, ok, false)
	})
	t.Run("a model the script named that cannot be resolved fails that agent", func(t *testing.T) {
		m := newStubManager(nil)
		h := hostWith(m, func(d *workflowHostDeps) {
			d.Registry = fakeRegistry{models: []*workflowModel{{Provider: "p", ID: "x"}}}
		})
		res, _ := h.SpawnAgent(wfRequest(func(r *workflowSpawnRequest) { r.Model = "no-such-model-anywhere" }))
		eq(t, res.OK, false)
		eq(t, strings.Contains(res.Error, "Model not found"), true)
		eq(t, m.count(), 0)
	})
}

func TestWorkflowRunControl(t *testing.T) {
	t.Run("a second skip of the same agent is refused", func(t *testing.T) {
		stub := newControllableHost()
		var control workflowControl
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "return await agent('one');", func(o *workflowRunOptions) { o.Host = stub; o.OnControl = func(c workflowControl) { control = c } })
		}()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the agent to start")
		eq(t, control.Skip(0), true)
		eq(t, control.Skip(0), false)
		<-done
	})
}
