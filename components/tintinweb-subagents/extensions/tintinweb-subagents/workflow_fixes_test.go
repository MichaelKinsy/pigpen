package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Not twins: the cases the review of the workflow slice found the port got wrong against the original (V8 and Node
// behaviour the goja engine has to be made to show, meta null handling, the task directory and the child block).

func TestWorkflowEngineFixes(t *testing.T) {
	t.Run("a stack overflow after the first await fails the run", func(t *testing.T) {
		res := wfRun(t, "await 1;\nfunction f() { return f() + 1; }\nf();\nreturn 1;", nil)
		eq(t, res.Status, runFailed)
		eq(t, res.Error, "Maximum call stack size exceeded")
	})
	t.Run("a stack overflow before the first await fails the run", func(t *testing.T) {
		res := wfRun(t, "function f() { return f() + 1; }\nf();\nreturn 1;", nil)
		eq(t, res.Status, runFailed)
		eq(t, res.Error, "Maximum call stack size exceeded")
	})
	t.Run("an unhandled promise rejection fails the run", func(t *testing.T) {
		res := wfRun(t, "Promise.reject(new Error(\"unhandled boom\"));\nawait new Promise(() => {});\nreturn 1;", nil)
		eq(t, res.Status, runFailed)
		eq(t, res.Error, "unhandled boom")
	})
	t.Run("a rejection handled in the same turn does not", func(t *testing.T) {
		res := wfRun(t, "const p = Promise.reject(new Error(\"x\"));\np.catch(() => {});\nreturn \"ok\";", nil)
		eq(t, res.Status, runCompleted)
		eq(t, res.Value, "ok")
	})
	t.Run("a non-Error rejection reports Node's text", func(t *testing.T) {
		res := wfRun(t, "Promise.reject(\"plain\");\nawait new Promise(() => {});", nil)
		eq(t, res.Status, runFailed)
		eq(t, strings.Contains(res.Error, `The promise rejected with the reason "plain".`), true)
	})
	t.Run("scripts are sloppy, as the original compiles them", func(t *testing.T) {
		res := wfRun(t, "implicitGlobal = 5;\nfunction plain() { return this === globalThis; }\nreturn [implicitGlobal, plain(), 010];", nil)
		eq(t, res.Status, runCompleted)
		eq(t, res.ValueJSON != nil && string(res.ValueJSON) == "[5,true,8]", true)
	})
	t.Run("a stack overflow after an agent answers fails the run", func(t *testing.T) {
		res := wfRun(t, "const r = await agent(\"x\");\nfunction f() { return f() + 1; }\nf();\nreturn r;", func(o *workflowRunOptions) { o.Host = wfStubHost(nil) })
		eq(t, res.Status, runFailed)
		eq(t, res.Error, "Maximum call stack size exceeded")
	})
	t.Run("a nested workflow is sloppy too", func(t *testing.T) {
		child := wfHead + "implicitGlobal = 7;\nreturn [implicitGlobal, 010];"
		host := newNestingHost(map[string]string{"kid": child}, nil)
		res := wfRun(t, "return await workflow(\"kid\");", func(o *workflowRunOptions) { o.Host = host })
		eq(t, res.Status, runCompleted)
		eq(t, string(res.ValueJSON), "[7,8]")
	})
	t.Run("meta is evaluated sloppy too", func(t *testing.T) {
		_, _ = wfMustMeta(t, "export const meta = { name: 'a', description: 'b', whenToUse: 'c' + 010 }\nreturn 1")
	})
}

func TestWorkflowMetaNullAndEmpty(t *testing.T) {
	const head = "export const meta = { name: 'a', description: 'b', "
	t.Run("null phases, detail, model and whenToUse are rejected", func(t *testing.T) {
		wfMetaErr(t, head+"phases: null }\nreturn 1", "`meta.phases` must be an array")
		wfMetaErr(t, head+"phases: [{ title: 'x', detail: null }] }\nreturn 1", "meta.phases\\[0\\].detail` must be a string")
		wfMetaErr(t, head+"phases: [{ title: 'x', model: null }] }\nreturn 1", "meta.phases\\[0\\].model` must be a string")
		wfMetaErr(t, head+"whenToUse: null }\nreturn 1", "`meta.whenToUse` must be a string")
	})
	t.Run("undefined stays absent", func(t *testing.T) {
		meta, _ := wfMustMeta(t, head+"whenToUse: undefined, phases: undefined }\nreturn 1")
		eq(t, meta.WhenToUse == nil && meta.Phases == nil, true)
	})
	t.Run("empty strings and an empty phases list are kept", func(t *testing.T) {
		meta, _ := wfMustMeta(t, head+"whenToUse: '', phases: [{ title: 'x', detail: '', model: '' }] }\nreturn 1")
		eq(t, meta.WhenToUse, sp(""))
		eq(t, meta.Phases, []workflowPhaseMeta{{Title: "x", Detail: sp(""), Model: sp("")}})
		raw, _ := json.Marshal(meta)
		eq(t, string(raw), `{"name":"a","description":"b","whenToUse":"","phases":[{"title":"x","detail":"","model":""}]}`)
		empty, _ := wfMustMeta(t, head+"phases: [] }\nreturn 1")
		raw, _ = json.Marshal(empty)
		eq(t, string(raw), `{"name":"a","description":"b","phases":[]}`)
		none, _ := wfMustMeta(t, "export const meta = { name: 'a', description: 'b' }\nreturn 1")
		raw, _ = json.Marshal(none)
		eq(t, string(raw), `{"name":"a","description":"b"}`)
	})
}

func TestWorkflowSessionTaskDirEncoding(t *testing.T) {
	root := filepath.Join(t.TempDir())
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	for _, c := range []struct{ cwd, want string }{
		{"/home/user/project", "home-user-project"},
		{`C:\Users\foo\project`, "Users-foo-project"},
		{`d:\x`, "x"},
		{`\\server\share\project`, "server-share-project"},
		{"a-b/c", "a-b-c"}, // a dash at index 1 is not a drive prefix
	} {
		dir, err := workflowSessionTaskDir(c.cwd, "sess")
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(filepath.Join(os.TempDir(), "pi-subagents-"+wfUID()), dir)
		eq(t, filepath.ToSlash(rel), c.want+"/sess/tasks")
	}
}

func TestWorkflowChildBlockSplice(t *testing.T) {
	env := envInfo{Platform: "linux"}
	block := "# Environment\nWorking directory: /w\nNot a git repository\nPlatform: linux"
	// The agent's own instructions quote the block: the real one is the last, so the block goes after it.
	system := block + "\n\nINSTRUCTIONS quote:\n" + block + "\nend"
	got := withWorkflowChildBlock(system, nil, "/w", env)
	eq(t, got, block+"\n\nINSTRUCTIONS quote:\n"+block+workflowChildBlock+"\nend")
}

func TestWorkflowChildHasNoHandle(t *testing.T) {
	r := startRig(t)
	owned := r.app.mgr.spawnOwned("run-1", "general-purpose", "d", "worker", childSpec{Prompt: "p"}, false)
	eq(t, [2]string{owned.Handle, owned.Alias}, [2]string{"", ""})
	top := r.app.mgr.spawn("general-purpose", "d", "", childSpec{Prompt: "p"}, false)
	// The owned child took no name a top-level sibling could otherwise have.
	eq(t, top.Handle, "general-purpose")
}

// racingControl settles the task while Pause runs outside the task's lock.
type racingControl struct {
	stubControl
	task   *workflowTask
	settle string
}

func (c *racingControl) Pause()  { c.task.mu.Lock(); c.task.Status = c.settle; c.task.mu.Unlock() }
func (c *racingControl) Resume() { c.task.mu.Lock(); c.task.Status = c.settle; c.task.mu.Unlock() }

func TestWorkflowTaskStatusRace(t *testing.T) {
	t.Run("a run that settles while it is being paused keeps its status", func(t *testing.T) {
		task, _ := runningTask()
		task.Control = &racingControl{task: task, settle: runCompleted}
		eq(t, pauseWorkflowTask(task, 5_000), false)
		eq(t, task.Status, runCompleted)
	})
	t.Run("a run that settles while it is being resumed keeps its status", func(t *testing.T) {
		task, _ := runningTask()
		task.Status = runPaused
		task.Control = &racingControl{task: task, settle: runKilled}
		eq(t, resumeWorkflowTask(task, 5_000), false)
		eq(t, task.Status, runKilled)
	})
}

func TestWorkflowUTF16PrefixCut(t *testing.T) {
	eq(t, wfUTF16Prefix("ab😀cd", 3), "ab\uFFFD") // cut inside the pair
	eq(t, wfUTF16Prefix("ab😀cd", 4), "ab😀")
	eq(t, wfUTF16Prefix("abc", 9), "abc")
}

func TestWorkflowCPUCount(t *testing.T) {
	eq(t, wfCPUCount() >= runtime.NumCPU(), true)
}

func TestWorkflowMenuIsRefused(t *testing.T) {
	t.Run("the description is the original's, byte for byte (pigeq compares the request)", func(t *testing.T) {
		rendered := renderWorkflowToolDescription(currentRegistry())
		eq(t, strings.Contains(rendered, "Use /agents → Workflows to watch live progress."), true)
	})
	t.Run("resuming a running workflow does not name the menu", func(t *testing.T) {
		tasks := newWorkflowTaskStore()
		task := createWorkflowTask(workflowTaskInit{ID: "wf_run", StartTime: 1})
		tasks.set(task)
		target := resolveResumeTarget("wf_run", tasks)
		eq(t, strings.Contains(target.Message, "is still running"), true)
		eq(t, strings.Contains(target.Message, "/agents"), false)
	})
	t.Run("/agents lists the runs and refuses the menu with the unified text", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		w.start()
		text := w.run(obj{"script": inlineScript + "await new Promise(() => {});\n"})
		id := w.taskID(text)
		w.Command("agents", "")
		out := w.noticeTexts()
		eq(t, strings.Contains(out, "Workflows (1):"), true)
		eq(t, strings.Contains(out, id+" from-inline"), true)
		eq(t, strings.Contains(out, refusalText("the Workflows menu (stop, pause, skip and retry of a run)")), true)
	})
}
