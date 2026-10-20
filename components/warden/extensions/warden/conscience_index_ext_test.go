package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Layer-1 tests of `/warden index` through the fake host: the session model is played by the host's
// sendUserMessage answer, which writes the index files the prompt names.

type ixRun struct {
	*csExt
	skills []map[string]any
	tools  []map[string]any
	// write is what the "model" does when it gets the prompt; nil writes nothing.
	write   func(globalPath, projectPath string)
	prompts []string
}

func newIxRun(t *testing.T, o csOpts) *ixRun {
	t.Helper()
	r := &ixRun{csExt: newCsExt(t, o)}
	r.skills = csDefaultSkillEvents()
	r.tools = []map[string]any{{"name": "search_graph", "description": "Search the code graph"}}
	r.mu.Lock()
	r.host2 = func(method string, args map[string]any) (map[string]any, string, bool) {
		switch method {
		case "getSystemPromptOptions":
			return map[string]any{"cwd": r.cwd, "skills": r.skills}, "", true
		case "getAllTools":
			return map[string]any{"tools": r.tools}, "", true
		case "sendUserMessage":
			content, _ := args["content"].(string)
			r.mu.Lock()
			r.prompts = append(r.prompts, content)
			write := r.write
			r.mu.Unlock()
			if write != nil {
				// The model works while the host call is open: the host's lock must not be held then.
				r.harness.mu.Unlock()
				defer r.harness.mu.Lock()
				g := regexp.MustCompile(`Global index: (.+)`).FindStringSubmatch(content)
				p := regexp.MustCompile(`Project index: (.+)`).FindStringSubmatch(content)
				write(strings.TrimSpace(g[1]), strings.TrimSpace(p[1]))
			}
			return map[string]any{}, "", true
		}
		return nil, "", false
	}
	r.mu.Unlock()
	return r
}

func ixModelEntry(name, kind, scope string) map[string]any {
	return map[string]any{
		"kind": kind, "name": name, "scope": scope, "sourceHash": "model-wrote-this", "role": "evidence", "lead": "Search the graph",
		"useWhen": []any{"When you need definitions", "When grep cannot find code"}, "notWhen": []any{}, "inputs": "a query",
		"examples": []any{"find the loader", "search for calls"}, "sourceQuality": "weak", "improve": "add examples",
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *ixRun) lastSay() string {
	if ns := r.notices(); len(ns) > 0 {
		return ns[len(ns)-1]
	}
	return ""
}

// Go-only: the command needs a UI (extension.ts 2959).
func TestIndexCommandNeedsAnInteractiveSession(t *testing.T) {
	r := newIxRun(t, csOpts{noUI: true})
	r.host.Command("warden", "index")
	ms := r.messages()
	if len(ms) == 0 || ms[len(ms)-1].Content != "Index needs an interactive session to run." {
		t.Errorf("%v", ms)
	}
	if len(r.prompts) != 0 {
		t.Error("nothing is sent to the agent")
	}
}

// Go-only: no skills, no index (extension.ts 2972).
func TestIndexCommandWithNoSkillsSaysSo(t *testing.T) {
	r := newIxRun(t, csOpts{})
	r.skills = nil
	r.host.Command("warden", "index")
	if got := r.lastSay(); got != "No skills available. Run a prompt first so Pi discovers installed skills, then retry /warden index." {
		t.Errorf("%q", got)
	}
}

// Go-only: a declined dialog changes nothing.
func TestIndexCommandDeclinedDoesNothing(t *testing.T) {
	r := newIxRun(t, csOpts{})
	r.confirm = false
	r.host.Command("warden", "index")
	if len(r.prompts) != 0 {
		t.Errorf("a declined dialog sends nothing: %v", r.prompts)
	}
	if len(r.host.CallsTo("ui.confirm")) != 1 {
		t.Error("the dialog was shown once")
	}
	c := r.host.CallsTo("ui.confirm")[0].Args
	if !strings.Contains(c["message"].(string), "(1 skills)") || !strings.Contains(c["message"].(string), "(1 tools,") || c["title"] != "Build capability index?" {
		t.Errorf("%v", c)
	}
}

// Go-only: the model writes a full-shape index; warden validates it, recomputes the skill hashes, stamps the
// metadata and reports.
func TestIndexCommandBuildsTheIndexAndTellsWhatItBuilt(t *testing.T) {
	skillFile := csWriteSkill(t, "impeccable", "---\nname: impeccable\ndescription: UI design\n---\n\nBody.")
	r := newIxRun(t, csOpts{})
	r.skills = []map[string]any{csSkillEvent("impeccable", "UI design", skillFile)}
	r.write = func(g, p string) {
		writeJSONFile(t, g, map[string]any{"formatVersion": 1, "builtAt": "x", "model": "x", "entries": []any{ixModelEntry("impeccable", "skill", "global"), ixModelEntry("search_graph", "tool", "global")}})
		writeJSONFile(t, p, map[string]any{"formatVersion": 1, "builtAt": "x", "model": "x", "entries": []any{}})
	}
	r.host.Command("warden", "index")
	want := "Index built: 2 entries (2 global, 0 project).\n\nThese descriptions would recommend better with a rewrite:\n  impeccable (skill): add examples\n  search_graph (tool): add examples"
	if got := r.lastSay(); got != want {
		t.Errorf("report:\n%s\nwant:\n%s", got, want)
	}
	d := indexDirs{Getenv: r.w.opts.Getenv, DataDir: r.w.dataDirOf()}
	_ = d
	idx := r.w.indexDirsOf()
	g := readIndex(idx.path("global", ""))
	if g == nil || len(g.Entries) != 2 || g.Model != "prov/mod" || g.BuiltAt == "x" {
		t.Fatalf("the file is rewritten with the metadata of this session: %+v", g)
	}
	if g.Entries[0].SourceHash != fileContentHash(skillFile) || g.Entries[1].SourceHash != "model-wrote-this" {
		t.Errorf("a skill's hash is recomputed from its file, a tool's is kept: %+v", g.Entries)
	}
	if !strings.Contains(r.prompts[0], "# Build a capability index") || !strings.Contains(r.prompts[0], "- impeccable: UI design (file: "+skillFile+")") || !strings.Contains(r.prompts[0], "- search_graph: Search the code graph") {
		t.Errorf("the prompt:\n%s", r.prompts[0])
	}
	// The conscience reads the new index at once: the candidate for the skill now carries its entry.
	r.set(csAdvance(3))
	r.prompt("design a landing page", r.skills)
	state := r.requests()[len(r.requests())-1].State["candidates"].(map[string]any)["c1"].(map[string]any)
	if state["lead"] != "Search the graph" || state["unindexed"] != nil {
		t.Errorf("candidate state %v", state)
	}
	if st, err := os.Stat(idx.path("global", "")); err != nil || st.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("the file is owner-only: %v %v", st, err)
	}
}

// Go-only: the model wrote nothing.
func TestIndexCommandWhenTheModelWritesNothing(t *testing.T) {
	r := newIxRun(t, csOpts{})
	idx := r.w.indexDirsOf()
	writeJSONFile(t, idx.path("global", ""), map[string]any{"formatVersion": 1, "builtAt": "old", "model": "old", "entries": []any{}})
	writeJSONFile(t, idx.path("project", r.cwd), map[string]any{"formatVersion": 1, "builtAt": "old", "model": "old", "entries": []any{}})
	r.host.Command("warden", "index")
	if got := r.lastSay(); got != "The model did not write the index; nothing was changed." {
		t.Errorf("%q", got)
	}
}

// Go-only: a rejected entry keeps the old files and names the entry and the field.
func TestIndexCommandRejectsABadEntryAndKeepsTheOldFiles(t *testing.T) {
	r := newIxRun(t, csOpts{})
	idx := r.w.indexDirsOf()
	bad := ixModelEntry("impeccable", "skill", "global")
	bad["useWhen"] = []any{"only one"}
	r.write = func(g, p string) {
		writeJSONFile(t, g, map[string]any{"formatVersion": 1, "builtAt": "x", "model": "x", "entries": []any{bad}})
	}
	r.host.Command("warden", "index")
	if got := r.lastSay(); got != "Index rejected:\n  global index: entry \"impeccable\": useWhen must have 2 to 4 items\nOld files kept." {
		t.Errorf("%q", got)
	}
	if g := readIndex(idx.path("global", "")); g == nil || g.BuiltAt != "x" {
		t.Error("the model's file stays as it is; no validated copy replaces it")
	}
}

// Go-only: a file with only an `entries` array is not an index file for the original (readIndex wants the full
// shape), so the run reports nothing built and keeps no entries.
func TestIndexCommandEntriesOnlyFileBuildsNothingLikeTheOriginal(t *testing.T) {
	r := newIxRun(t, csOpts{})
	r.write = func(g, p string) {
		writeJSONFile(t, g, map[string]any{"entries": []any{ixModelEntry("impeccable", "skill", "global")}})
	}
	r.host.Command("warden", "index")
	if got := r.lastSay(); !strings.HasPrefix(got, "Index built: 0 entries (0 global, 0 project).") {
		t.Errorf("%q", got)
	}
}

// Go-only: while the model writes, the action guard lets exactly the two files through, and the flag is gone
// afterwards even when the run fails.
func TestIndexCommandExemptsItsFilesOnlyWhileItRuns(t *testing.T) {
	r := newIxRun(t, csOpts{})
	var during map[string]any
	r.write = func(g, p string) {
		during = r.toolCall("write", "w1", map[string]any{"path": g, "content": "{}"})
		r.toolCall("write", "w2", map[string]any{"path": filepath.Join(filepath.Dir(g), "other.json"), "content": "{}"})
	}
	r.host.Command("warden", "index")
	if during != nil {
		t.Errorf("the index file is allowed while it is written: %v", during)
	}
	if n := strings.Count(r.traceText(), "index write, expected"); n != 1 {
		t.Errorf("only the index file is exempt: %d\n%s", n, r.traceText())
	}
	r.w.conscience.mu.Lock()
	running := r.w.conscience.indexRunning
	r.w.conscience.mu.Unlock()
	if running {
		t.Error("the flag clears when the run ends")
	}
	// A failed run clears it too.
	r.mu.Lock()
	r.host2 = func(method string, args map[string]any) (map[string]any, string, bool) {
		switch method {
		case "getSystemPromptOptions":
			return map[string]any{"cwd": r.cwd, "skills": r.skills}, "", true
		case "sendUserMessage":
			return nil, "the agent is busy", true
		}
		return nil, "", false
	}
	r.mu.Unlock()
	r.host.Command("warden", "index")
	r.w.conscience.mu.Lock()
	running = r.w.conscience.indexRunning
	r.w.conscience.mu.Unlock()
	if running || !strings.HasPrefix(r.lastSay(), "pi-warden index failed: ") {
		t.Errorf("running %v, said %q", running, r.lastSay())
	}
}
