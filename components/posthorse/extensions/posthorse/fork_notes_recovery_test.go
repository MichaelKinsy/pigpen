package posthorse

// Fork notes and recovery evidence: the cases of test/native-host.ts:837-935 that the notes store owns
// (what an edited, excluded or other-branch receipt lets a fork snapshot and a recovery see), plus the evidence
// helpers (sessionNoteTouches, scopeRecoveryTouches) on their own. The recovery-summary half of both cases
// (compaction text) belongs to the recovery lane and the recorded scenarios.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// codemodeReceipt appends the tool-result message of codemode call parent and returns its entry id.
func (s *forkSide) codemodeReceipt(parent string, details rec) string {
	s.t.Helper()
	message := fxToolResult(parent, "codemode", []any{fxText("Saved")}, false)
	if details != nil {
		message["details"] = details
	}
	return s.sess.appendMessage(message)
}

// native-host.ts:837 "fork notes recover metadata-only nested writes through edited receipts and compaction"
// Layer 1 covers the fork half: an edited receipt still seeds a fork; a receipt deleted by a context edit cannot.
// Not reproduced: the native per-call argument record (nested.arguments undefined), the dump tool and the compaction
// summary (recovery lane / recorded scenario).
func TestFork_recoverMetadataOnlyNestedWritesThroughEditedReceiptsAndCompaction(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	h := newForkSide(t, cwd, w.session(cwd))
	h.mustStart()
	body := strings.Repeat("saved checkpoint ", 1024)
	codemode := h.nextID("codemode")
	args := rec{"code": `await tools.notes({op:"write", path:"metadata-only.md", content:"saved checkpoint ".repeat(1024)}); return "Saved";`}
	h.sess.appendMessage(fxAssistant([]any{fxToolCall(codemode, "codemode", args)}))
	h.f.Fire("tool_call", rec{"toolName": "codemode", "toolCallId": codemode, "input": args})
	if res := h.notesCall(rec{"op": "write", "path": "metadata-only.md", "content": body}, codemode); res.isError {
		t.Fatal(res.text)
	}
	receipt := h.codemodeReceipt(codemode, rec{"nestedCalls": rec{"calls": []any{rec{"name": "notes", "status": "ok"}}}})
	h.f.Fire("tool_execution_end", rec{"toolCallId": codemode, "toolName": "codemode"})
	if got := forkRead(t, filepath.Join(cwd, ".pi", "notes", "metadata-only.md")); got != body {
		t.Fatal("the nested write did not reach the project note")
	}
	_, err := h.sess.appendContextEdit(receipt, rec{"content": []any{fxText("Edited receipt without note content.")}})
	forkMust(t, err)

	child := newForkSide(t, cwd, w.fork(h.sess, cwd))
	child.mustStart()
	copied := child.notes(rec{"op": "read", "path": "metadata-only.md"})
	forkMatch(t, copied, `File: .*/\.forks/`)
	if !strings.HasSuffix(copied, body) {
		t.Fatal("the fork lost the metadata-only note's content")
	}

	_, err = h.sess.appendContextEdit(receipt, nil)
	forkMust(t, err)
	excluded := newForkSide(t, cwd, w.fork(h.sess, cwd))
	excluded.mustStart()
	listing := excluded.notes(rec{"op": "list"})
	m := regexp.MustCompile(`^Notes directory: ([^\n]+)`).FindStringSubmatch(listing)
	if m == nil {
		t.Fatalf("no notes directory in %q", listing)
	}
	if forkExists(filepath.Join(m[1], "metadata-only.md")) {
		t.Fatal("an explicitly deleted causal receipt cannot seed a fork")
	}
}

func forkTouchPaths(touches []noteTouch) []string {
	var out []string
	for _, touch := range touches {
		out = append(out, touch.notePath+"|"+touch.kind)
	}
	return out
}

// native-host.ts:884 "fork notes associate late cancelled background effects with their launch branch"
// Layer 1 covers the ownership half: a note touch recorded on another branch (the effect finished after the user
// navigated away) belongs to the branch that holds its launch receipt, and only while that receipt is not deleted.
// Not reproduced: the background codemode runner, the cancelled ctx.signal, `codemode-background` messages and the
// compaction summaries (recorded scenario; the fixture has no runner).
func TestFork_associateLateCancelledBackgroundEffectsWithTheirLaunchBranch(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	h := newForkSide(t, cwd, w.session(cwd))
	h.mustStart()
	h.sess.appendMessage(fxUser("Start the task."))
	h.sess.appendMessage(fxAssistant([]any{fxText("Seed branch.")}))
	branchPoint := forkLeaf(h.sess)
	path := filepath.Join(cwd, ".pi", "notes", "late.md")

	codemode := h.nextID("codemode")
	args := rec{"code": "// @options: {\"background\": true}\nawait tools.write({path: " + path + ", content: \"late checkpoint\"}); return \"done\";"}
	h.sess.appendMessage(fxAssistant([]any{fxToolCall(codemode, "codemode", args)}))
	h.f.Fire("tool_call", rec{"toolName": "codemode", "toolCallId": codemode, "input": args})
	// The nested native write starts (its tool_call runs) and writes the file while the launch branch is active.
	late := h.nextID("write")
	nestedInput := rec{"path": path, "content": "late checkpoint"}
	if r := h.f.Fire("tool_call", rec{"toolName": "write", "toolCallId": late, "parentToolCallId": codemode, "input": nestedInput}); r != nil && r["block"] == true {
		t.Fatalf("the nested write was blocked: %v", r)
	}
	forkMust(t, os.MkdirAll(filepath.Dir(path), 0o755))
	forkMust(t, os.WriteFile(path, []byte("late checkpoint"), 0o644))
	receipt := h.codemodeReceipt(codemode, nil)
	launchBranch := forkLeaf(h.sess)
	h.f.Fire("tool_execution_end", rec{"toolCallId": codemode, "toolName": "codemode"})

	// The user navigates to the branch point; the cancelled effect's result arrives afterwards.
	h.sess.branch(branchPoint)
	h.f.Fire("tool_result", rec{"toolName": "write", "toolCallId": late, "input": nestedInput, "isError": false, "details": rec{}})

	touches := func() []string {
		evidence := sessionNoteTouches(noteEvidence{branch: h.sess.getBranch(nil), allEntries: h.sess.getEntries()})
		return forkTouchPaths(evidence.touches)
	}
	if got := touches(); len(got) != 0 {
		t.Fatalf("an effect launched on another branch leaked into this one: %v", got)
	}
	h.sess.branch(launchBranch)
	got := touches()
	if len(got) != 1 || got[0] != "late.md|observed-write" {
		t.Fatalf("the launch branch must own the late effect: %v", got)
	}
	_, err := h.sess.appendContextEdit(receipt, nil)
	forkMust(t, err)
	if got := touches(); len(got) != 0 {
		t.Fatalf("a deleted launch receipt must hide its effects: %v", got)
	}
}

func TestNoteEvidence_scopeRecoveryTouches(t *testing.T) {
	root := nsRealTemp(t)
	owner := noteOwner{id: "s1", timestamp: nsTimestamp}
	scope, _ := forkScope(owner)
	parentOwner := noteOwner{id: "parent", timestamp: nsTimestamp}
	binding := &noteBinding{
		kind: "fork", owner: owner, root: root, scope: scope, absent: []string{"gone.md"},
		source: &noteScope{root: root},
		seeds:  []noteSeed{{path: "seeded.md", source: noteAddress{noteScope{root: root}, "seeded.md"}, lineage: noteAddress{noteScope{root: root}, "seeded.md"}, anchors: []string{}}},
	}
	own := filepath.Join(root, filepath.FromSlash(scope))
	forkMust(t, os.MkdirAll(own, 0o755))
	entries := []rec{{"type": "custom", "customType": notesBindingType, "data": nsRawBinding(binding)}}
	recovery := &notesRecovery{binding: binding, entries: entries}

	touch := func(notePath, abs, kind string, owner *noteOwner) noteTouch {
		return noteTouch{notePath: notePath, absPath: abs, kind: kind, entryId: "e1", receiptId: "r1", inRetained: true, owner: owner}
	}
	t.Run("no recovery or a project binding changes nothing", func(t *testing.T) {
		in := []noteTouch{touch("a.md", filepath.Join(root, "a.md"), "full-write", nil)}
		for _, r := range []*notesRecovery{nil, {binding: &noteBinding{kind: "project", owner: owner, root: root}}} {
			got, err := scopeRecoveryTouches(in, r)
			if err != nil || len(got) != 1 || got[0] != in[0] {
				t.Fatalf("got %v, %v", got, err)
			}
		}
	})
	t.Run("a touch in the session's own scope is kept", func(t *testing.T) {
		in := touch("seeded.md", filepath.Join(own, "seeded.md"), "full-write", &owner)
		got, err := scopeRecoveryTouches([]noteTouch{in}, recovery)
		if err != nil || got[0] != in {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("inherited history of an owned path points at the private copy", func(t *testing.T) {
		for _, kind := range []string{"full-write", "partial-write", "observed-write", "read", "clear"} {
			in := touch("seeded.md", filepath.Join(root, "seeded.md"), kind, &parentOwner)
			got, err := scopeRecoveryTouches([]noteTouch{in}, recovery)
			if err != nil {
				t.Fatal(err)
			}
			wantKind := "observed-write"
			if kind == "read" || kind == "clear" {
				wantKind = kind
			}
			if got[0].absPath != filepath.Join(own, "seeded.md") || got[0].kind != wantKind || got[0].inRetained {
				t.Fatalf("%s: %+v", kind, got[0])
			}
		}
		// A path the session deleted (absent) is owned too.
		gone, err := scopeRecoveryTouches([]noteTouch{touch("gone.md", filepath.Join(root, "gone.md"), "full-write", nil)}, recovery)
		if err != nil || gone[0].absPath != filepath.Join(own, "gone.md") {
			t.Fatalf("absent path: %+v %v", gone, err)
		}
	})
	t.Run("an unowned path of the source scope becomes a labeled reference", func(t *testing.T) {
		in := touch("other.md", filepath.Join(root, "other.md"), "full-write", &parentOwner)
		got, err := scopeRecoveryTouches([]noteTouch{in}, recovery)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].kind != "read" || got[0].referenceScope != root || got[0].inRetained || got[0].absPath != in.absPath {
			t.Fatalf("%+v", got[0])
		}
	})
	t.Run("touches without an address or outside every scope are kept", func(t *testing.T) {
		a := touch("x.md", "", "full-write", nil)
		b := touch("y.md", filepath.Join(filepath.Dir(root), "elsewhere", "y.md"), "full-write", nil)
		got, err := scopeRecoveryTouches([]noteTouch{a, b}, recovery)
		if err != nil || got[0] != a || got[1] != b {
			t.Fatalf("%v %v", got, err)
		}
	})
}

// nsRawBinding is the binding as a session entry's data carries it (JSON decoded).
func nsRawBinding(b *noteBinding) any {
	var out any
	if err := jsonDecode(b.appendJSON(nil), &out); err != nil {
		panic(err)
	}
	return out
}

// note-provenance.ts:116 resolveReference: a note an ancestor owned and deleted is gone for its descendants; the
// walk up the provenance chain must not fall through to the grandparent's copy.
func TestFork_deletedOwnedNoteDoesNotFallThroughToAnAncestor(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	grandparent := newForkSide(t, cwd, w.session(cwd))
	grandparent.mustStart()
	grandparent.notes(rec{"op": "write", "path": "x.md", "content": "grandparent version"})
	parent := newForkSide(t, cwd, w.fork(grandparent.sess, cwd))
	parent.mustStart()
	privateCopy := forkFilePath(t, parent.notes(rec{"op": "read", "path": "x.md"}))
	forkMust(t, os.Remove(privateCopy))
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	failure := child.start()
	forkMatch(t, failure, `Incomplete notes snapshot: source note x\.md is unavailable`)
}

// note-provenance.ts:176 seedCandidates: a seed of an ancestor's manifest reaches a descendant only when one of its
// anchors is a visible receipt of the descendant's branch.
func TestFork_descendantSnapshotsOnlySeedsVisibleOnItsBranch(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	p := newForkSide(t, cwd, w.session(cwd))
	p.mustStart()
	p.notes(rec{"op": "write", "path": "a.md", "content": "first"})
	afterFirst := forkLeaf(p.sess)
	p.notes(rec{"op": "write", "path": "b.md", "content": "second"})
	c := newForkSide(t, cwd, w.fork(p.sess, cwd))
	c.mustStart()
	if !forkExists(filepath.Join(scopeDirectory(c.bindingOf(t)), "b.md")) {
		t.Fatal("the first fork must snapshot both notes")
	}
	gcSession := w.fork(c.sess, cwd)
	gcSession.branch(afterFirst)
	gc := newForkSide(t, cwd, gcSession)
	gc.mustStart()
	dir := scopeDirectory(gc.bindingOf(t))
	if !forkExists(filepath.Join(dir, "a.md")) {
		t.Fatal("the note touched on the branch was not snapshotted")
	}
	if forkExists(filepath.Join(dir, "b.md")) {
		t.Fatal("a seed whose anchors are not on the branch was snapshotted")
	}
}

// note-provenance.ts:55 sourceEntries: the journals of the whole parent chain, custom and session entries only,
// each ancestor read once, a missing file tolerated.
func TestNoteProvenance_sourceEntriesFollowsTheParentChain(t *testing.T) {
	dir := nsRealTemp(t)
	file := func(name string) string { return filepath.Join(dir, name+".jsonl") }
	custom := func(id string) rec { return fxCustom(id, "x", rec{"n": id}) }
	forkMust(t, fxWriteSession(file("gp"), fxHeader("gp", dir, ""), custom("g1"), fxMessageEntry("gm", fxUser("dropped"))))
	forkMust(t, fxWriteSession(file("parent"), fxHeader("parent", dir, file("gp")), custom("p1")))
	forkMust(t, fxWriteSession(file("child"), fxHeader("child", dir, file("parent")), custom("c1")))
	ids := func(entries []rec) string {
		var out []string
		for _, e := range entries {
			if id, ok := e["id"].(string); ok {
				out = append(out, id)
			}
		}
		return strings.Join(out, ",")
	}
	child := file("child")
	entries, err := sourceEntries(nil, &child)
	if err != nil || ids(entries) != "child,c1,parent,p1,gp,g1" {
		t.Fatalf("chain = %s, %v", ids(entries), err)
	}
	// A cycle ends at the first repeated file.
	forkMust(t, fxWriteSession(file("gp"), fxHeader("gp", dir, file("child")), custom("g1")))
	if entries, err = sourceEntries(nil, &child); err != nil || ids(entries) != "child,c1,parent,p1,gp,g1" {
		t.Fatalf("cycle = %s, %v", ids(entries), err)
	}
	// A missing ancestor ends the chain without an error; a missing file yields nothing.
	forkMust(t, os.Remove(file("parent")))
	if entries, err = sourceEntries(nil, &child); err != nil || ids(entries) != "child,c1" {
		t.Fatalf("missing ancestor = %s, %v", ids(entries), err)
	}
	if entries, err = sourceEntries(nil, nil); err != nil || len(entries) != 0 {
		t.Fatalf("undefined file = %v, %v", entries, err)
	}
}
