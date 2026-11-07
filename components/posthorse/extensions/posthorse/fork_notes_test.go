package posthorse

// Layer-1 ports of the fork-notes cases of test/native-host.ts:203-835 (real Pi in the original). The extension
// runs on the fake host with the fixture's fake session manager; the Pi pieces that cannot be reproduced here are
// stood in for by the helpers below and listed per case in port/lane-NotesStoreLane.md:
//
//   - a "native call" is simulated as Pi orders it: the assistant message with the tool call is appended, the
//     `tool_call` handlers run (they may block), the tool runs (the notes tool through the real extension, a native
//     read/write/edit through an effect closure that does what the native tool does to the file), the `tool_result`
//     handlers run, `tool_execution_end` fires and the tool-result message entry is appended;
//   - the session manager is the fixture's fakeSession (fork-from, open, branch);
//   - `bindExtensions` is the `session_start` event.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type forkSide struct {
	t    *testing.T
	f    *fixture
	sess *fakeSession
	cwd  string
	mu   sync.Mutex
	seq  int
}

func newForkSide(t *testing.T, cwd string, sess *fakeSession) *forkSide {
	t.Helper()
	f := newFixture(t, withStartCwd(cwd), withSession(sess), withRealProjection(), withAppendToSession())
	return &forkSide{t: t, f: f, sess: sess, cwd: cwd}
}

// start is bindExtensions: the session_start handlers run; the failure of any is returned.
func (s *forkSide) start() string {
	s.t.Helper()
	host, leave := s.f.enter()
	defer leave()
	h := host.h
	h.mu.Lock()
	ids := append([]int(nil), h.allHandlers["session_start"]...)
	h.mu.Unlock()
	var failures []string
	for _, id := range ids {
		if _, failure := fxStartRequest(h, fxEventRequest("session_start", id, rec{"reason": "startup"})).wait(); failure != "" {
			failures = append(failures, failure)
		}
	}
	return strings.Join(failures, "\n")
}

func (s *forkSide) mustStart() {
	s.t.Helper()
	if failure := s.start(); failure != "" {
		s.t.Fatalf("session_start failed: %s", failure)
	}
}

func (s *forkSide) nextID(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s-%d", prefix, s.seq)
}

type forkResult struct {
	text    string
	isError bool
	details rec
}

// begin appends the assistant message carrying the call and fires the tool_call handlers; blocked is the reason
// a handler gave, "" when the call may run.
func (s *forkSide) begin(id, tool string, args, extra rec) (blocked string) {
	s.t.Helper()
	if extra == nil { // a nested call belongs to its parent's message
		s.sess.appendMessage(fxAssistant([]any{fxToolCall(id, tool, args)}))
	}
	event := rec{"toolName": tool, "toolCallId": id, "input": args}
	for k, v := range extra {
		event[k] = v
	}
	if r := s.f.Fire("tool_call", event); r != nil && r["block"] == true {
		reason, _ := r["reason"].(string)
		return reason
	}
	return ""
}

func (s *forkSide) finish(id, tool string, res forkResult, nested bool) {
	s.t.Helper()
	s.f.Fire("tool_execution_end", rec{"toolCallId": id, "toolName": tool})
	if nested {
		return
	}
	message := fxToolResult(id, tool, []any{fxText(res.text)}, res.isError)
	if res.details != nil {
		message["details"] = res.details
	}
	s.sess.appendMessage(message)
}

// notesCall runs the notes tool as a call of the model (or, with parent != "", as a call nested in another).
func (s *forkSide) notesCall(args rec, parent string) forkResult {
	s.t.Helper()
	id := s.nextID("notes")
	var extra rec
	if parent != "" {
		extra = rec{"parentToolCallId": parent}
	}
	if reason := s.begin(id, "notes", args, extra); reason != "" {
		res := forkResult{text: reason, isError: true}
		s.finish(id, "notes", res, parent != "")
		return res
	}
	s.f.checkTool("notes", args)
	host, leave := s.f.enter()
	raw, failure := fxStartRequest(host.h, fxToolRequest("notes", args, id)).wait()
	leave()
	var res forkResult
	if failure != "" {
		res = forkResult{text: failure, isError: true}
	} else {
		parsed, err := fxParseResult(raw)
		if err != nil {
			s.t.Errorf("notes result: %v", err)
		}
		res = forkResult{text: parsed.Text(), details: parsed.Details}
	}
	s.finish(id, "notes", res, parent != "")
	return res
}

func (s *forkSide) notes(args rec) string {
	s.t.Helper()
	res := s.notesCall(args, "")
	if res.isError {
		s.t.Fatalf("notes %v failed: %s", args, res.text)
	}
	return res.text
}

func (s *forkSide) notesErr(args rec) forkResult {
	s.t.Helper()
	res := s.notesCall(args, "")
	if !res.isError {
		s.t.Fatalf("notes %v succeeded: %s", args, res.text)
	}
	return res
}

// native is a call of Pi's own read/write/edit tool: effect does what the tool does to the file system and
// returns the result text and error.
func (s *forkSide) native(tool string, args rec, effect func() (string, error)) forkResult {
	s.t.Helper()
	id := s.nextID(tool)
	if reason := s.begin(id, tool, args, nil); reason != "" {
		res := forkResult{text: reason, isError: true}
		s.finish(id, tool, res, false)
		return res
	}
	text, err := effect()
	res := forkResult{text: text}
	if err != nil {
		res = forkResult{text: err.Error(), isError: true}
	}
	s.f.Fire("tool_result", rec{"toolName": tool, "toolCallId": id, "input": args, "isError": res.isError, "details": rec{}})
	s.finish(id, tool, res, false)
	return res
}

func (s *forkSide) nativeRead(path string) forkResult {
	return s.native("read", rec{"path": path}, func() (string, error) {
		data, err := os.ReadFile(path)
		return string(data), err
	})
}

func (s *forkSide) nativeWrite(path, content string) forkResult {
	return s.native("write", rec{"path": path, "content": content}, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		return "wrote", os.WriteFile(path, []byte(content), 0o644)
	})
}

func (s *forkSide) nativeEdit(path, oldText, newText string) forkResult {
	return s.native("edit", rec{"path": path, "oldText": oldText, "newText": newText}, func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !strings.Contains(string(data), oldText) {
			return "", fmt.Errorf("Could not find the text to replace in %s", path)
		}
		return "edited", os.WriteFile(path, []byte(strings.Replace(string(data), oldText, newText, 1)), 0o644)
	})
}

// ---- fixtures ---------------------------------------------------------------------------------------------------

type forkWorld struct {
	t    *testing.T
	root string
	dir  string // session directory
}

func newForkWorld(t *testing.T) *forkWorld {
	root := nsRealTemp(t)
	return &forkWorld{t: t, root: root, dir: filepath.Join(root, "sessions")}
}

func (w *forkWorld) project(name string) string {
	dir := filepath.Join(w.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return dir
}

func (w *forkWorld) session(cwd string) *fakeSession {
	s, err := fxPersistedSession(cwd, w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w *forkWorld) fork(source *fakeSession, cwd string) *fakeSession {
	s, err := fxForkSession(source.getSessionFile(), cwd, w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w *forkWorld) open(source *fakeSession) *fakeSession {
	s, err := fxOpenSession(source.getSessionFile(), w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func forkMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func forkRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func forkExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func forkMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("%q does not match /%s/", text, pattern)
	}
}

func forkNoMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("%q matches /%s/", text, pattern)
	}
}

// filePath is `/^File: ([^\n]+)/u.exec(text)?.[1]`.
func forkFilePath(t *testing.T, text string) string {
	t.Helper()
	m := regexp.MustCompile(`^File: ([^\n]+)`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no File: line in %q", text)
	}
	return m[1]
}

func forkLeaf(s *fakeSession) string {
	id := s.getLeafID()
	if id == nil {
		return ""
	}
	return *id
}

func forkLines(text, prefix string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// forkMarker is a receipt a native read left in a parent session that never ran Posthorse.
func forkNativeReceipt(s *fakeSession, id, tool string, args rec, text string) {
	s.appendMessage(fxAssistant([]any{fxToolCall(id, tool, args)}))
	s.appendMessage(fxToolResult(id, tool, []any{fxText(text)}, false))
}

func forkGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Posthorse fixture", "-c", "user.email=posthorse@example.invalid"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

var forkEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// ---- the cases (test/native-host.ts) ----------------------------------------------------------------------------

// native-host.ts:203 "fork notes canonicalize legacy receipt aliases before resolving their source"
func TestFork_legacyReceiptAliasesCanonicalizeBeforeResolvingTheirSource(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	directory := filepath.Join(cwd, ".pi", "notes")
	forkMust(t, os.MkdirAll(directory, 0o755))
	forkMust(t, os.WriteFile(filepath.Join(directory, "legacy.md"), []byte("legacy checkpoint"), 0o644))
	alias := filepath.Join(filepath.Dir(cwd), "alias", ".pi", "notes")
	forkMust(t, os.MkdirAll(filepath.Dir(alias), 0o755))
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// The parent never ran Posthorse: only native read receipts describe its notes.
	parent := w.session(cwd)
	for i, path := range []string{filepath.Join(directory, "legacy.md"), filepath.Join(alias, "legacy.md")} {
		forkNativeReceipt(parent, fmt.Sprintf("read-%d", i), "read", rec{"path": path}, "legacy checkpoint")
	}
	child := newForkSide(t, cwd, w.fork(parent, cwd))
	child.mustStart()
	forkMatch(t, child.notes(rec{"op": "read", "path": "legacy.md"}), `/\.forks/[^/]+/notes/legacy\.md\nlegacy checkpoint$`)
	if got := forkRead(t, filepath.Join(directory, "legacy.md")); got != "legacy checkpoint" {
		t.Fatalf("source changed: %q", got)
	}
	// A receipt for a second store makes the legacy root ambiguous.
	other := filepath.Join(filepath.Dir(cwd), "other", ".pi", "notes")
	forkMust(t, os.MkdirAll(other, 0o755))
	forkMust(t, os.WriteFile(filepath.Join(other, "legacy.md"), []byte("different store"), 0o644))
	forkNativeReceipt(parent, "read-other", "read", rec{"path": filepath.Join(other, "legacy.md")}, "different store")
	rejected := newForkSide(t, cwd, w.fork(parent, cwd))
	failure := rejected.start()
	forkMatch(t, failure, `Incomplete notes snapshot: the source notes root is missing or ambiguous`)
}

// native-host.ts:250 "fork notes retry initialization after a missing source is restored"
func TestFork_retryInitializationAfterAMissingSourceIsRestored(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent checkpoint"})
	source := filepath.Join(cwd, ".pi", "notes", "task.md")
	forkMust(t, os.Remove(source))
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	failure := child.start()
	forkMatch(t, failure, `Incomplete notes snapshot: source note task\.md is unavailable`)
	if n := strings.Count(failure, "Incomplete notes snapshot"); n != 1 {
		t.Fatalf("one initialization error expected, got %d: %s", n, failure)
	}
	forkMust(t, os.WriteFile(source, []byte("restored checkpoint"), 0o644))
	forkMatch(t, child.notes(rec{"op": "read", "path": "task.md"}), `/\.forks/[^/]+/notes/task\.md\nrestored checkpoint$`)
	child.notes(rec{"op": "append", "path": "task.md", "content": "child progress"})
	if got := forkRead(t, source); got != "restored checkpoint" {
		t.Fatalf("a child append reached the source: %q", got)
	}
}

// native-host.ts:325 "fork notes retain deleted-worktree source roots across projects and interactive descendants"
func TestFork_retainDeletedWorktreeSourceRootsAcrossProjectsAndInteractiveDescendants(t *testing.T) {
	w := newForkWorld(t)
	main := w.project("main")
	worktree := filepath.Join(w.root, "task")
	forkGit(t, main, "init", "-b", "main")
	forkGit(t, main, "commit", "--allow-empty", "-m", "fixture")
	forkGit(t, main, "worktree", "add", "-b", "task", worktree)
	parent := newForkSide(t, worktree, w.session(worktree))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "shared checkpoint"})
	forkGit(t, main, "worktree", "remove", worktree)
	if forkExists(worktree) {
		t.Fatal("the fixture worktree is still there")
	}
	other := w.project("other-project")
	child := newForkSide(t, other, w.fork(parent.sess, other))
	child.mustStart()
	inherited := child.notes(rec{"op": "read", "path": "task.md"})
	childPath := forkFilePath(t, inherited)
	if !strings.HasPrefix(childPath, filepath.Join(other, ".pi", "notes", ".forks")+string(filepath.Separator)) {
		t.Fatalf("private copy at %s", childPath)
	}
	if !strings.HasSuffix(inherited, "shared checkpoint") {
		t.Fatalf("inherited %q", inherited)
	}
	if forkExists(worktree) {
		t.Fatal("source lookup recreated the deleted cwd")
	}
	child.notes(rec{"op": "write", "path": "task.md", "content": "private checkpoint"})
	childID := child.sess.sessionID()
	// The interactive fork of the child at its leaf starts (session_start) before anything else happens.
	grandchild := newForkSide(t, other, w.fork(child.sess, other))
	grandchild.mustStart()
	forkMust(t, os.WriteFile(childPath, []byte("parent advanced after the fork"), 0o644))
	privateRead := grandchild.notes(rec{"op": "read", "path": "task.md"})
	if !strings.HasSuffix(privateRead, "private checkpoint") || !strings.Contains(privateRead, grandchild.sess.sessionID()) {
		t.Fatalf("private read %q", privateRead)
	}
	if grandchild.sess.sessionID() == childID {
		t.Fatal("the descendant kept its parent's id")
	}
	if got := forkRead(t, filepath.Join(main, ".pi", "notes", "task.md")); got != "shared checkpoint" {
		t.Fatalf("the shared note changed: %q", got)
	}
	grandchild.notes(rec{"op": "write", "path": "descendant-only.md", "content": "cross-project private result"})
	original := newForkSide(t, main, fxMemorySession(main))
	original.mustStart()
	forkNoMatch(t, original.notes(rec{"op": "list"}), `descendant-only|other-project`)
	forkMatch(t, original.notes(rec{"op": "search", "query": "cross-project private result"}), `No notes match`)
	forkMatch(t, grandchild.notes(rec{"op": "search", "query": "cross-project private result"}), `descendant-only`)
}

// native-host.ts:405 "fork notes snapshot the selected branch without sharing its writable checkpoint"
func TestFork_snapshotTheSelectedBranchWithoutSharingItsWritableCheckpoint(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent checkpoint"})
	parentNote := filepath.Join(cwd, ".pi", "notes", "task.md")
	forkMust(t, os.Chtimes(parentNote, forkEpoch, forkEpoch))
	forkMust(t, os.WriteFile(filepath.Join(cwd, ".pi", "notes", "untouched.md"), []byte("not part of the branch"), 0o644))
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	child.mustStart()
	initial := child.notes(rec{"op": "read", "path": "task.md"})
	childPath := forkFilePath(t, initial)
	childInfo, _ := os.Stat(childPath)
	parentInfo, _ := os.Stat(parentNote)
	if !childInfo.ModTime().Equal(parentInfo.ModTime()) {
		t.Fatalf("the private copy lost the source mtime: %v vs %v", childInfo.ModTime(), parentInfo.ModTime())
	}
	if forkExists(filepath.Join(filepath.Dir(childPath), "untouched.md")) {
		t.Fatal("a note outside the branch was copied")
	}
	listing := parent.notes(rec{"op": "list"})
	if n := forkLines(listing, "task.md "); n != 1 {
		t.Fatalf("task.md listed %d times:\n%s", n, listing)
	}
	forkMatch(t, listing, `identical copies`)
	forkNoMatch(t, listing, `manifest\.json|staging`)
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent advanced"})
	inheritedAgain := child.notes(rec{"op": "read", "path": "task.md"})
	forkMatch(t, inheritedAgain, `/\.forks/[^/]+/notes/task\.md\nparent checkpoint$`)
	if !strings.Contains(inheritedAgain, child.sess.sessionID()) {
		t.Fatalf("the private path does not carry the session id: %s", inheritedAgain)
	}
	comparison := child.notes(rec{"op": "read", "path": "task.md", "scope": filepath.Dir(parentNote)})
	forkMatch(t, comparison, `^Read-only live reference:.*\nparent advanced$`)
	diverged := parent.notes(rec{"op": "list"})
	if n := forkLines(diverged, "task.md "); n != 2 {
		t.Fatalf("diverged copies must list separately, got %d:\n%s", n, diverged)
	}
	child.notes(rec{"op": "append", "path": "task.md", "content": "child progress"})
	forkMatch(t, child.notes(rec{"op": "read", "path": "task.md"}), `parent checkpoint\nchild progress\n$`)
	forkMatch(t, parent.notes(rec{"op": "read", "path": "task.md"}), `parent advanced$`)
	resumed := newForkSide(t, cwd, w.open(child.sess))
	resumed.mustStart()
	forkMatch(t, resumed.notes(rec{"op": "read", "path": "task.md"}), `parent checkpoint\nchild progress\n$`)
}

// native-host.ts:469 "fork notes keep ownership across tree navigation, adoption, deletion, and resume"
// Not reproduced here: the codemode tool (the two parallel appends are issued as two calls nested in one codemode
// receipt), Pi's own edit/read tools (effect closures stand in) and navigateTree's session events.
func TestFork_keepOwnershipAcrossTreeNavigationAdoptionDeletionAndResume(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected checkpoint"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "reference.md", "content": "original reference"})
	parent.notes(rec{"op": "write", "path": "append.md", "content": "inherited prefix"})
	otherBranch := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	selected := child.notes(rec{"op": "read", "path": "selected.md"})
	childDirectory := filepath.Dir(forkFilePath(t, selected))
	if forkExists(filepath.Join(childDirectory, "reference.md")) || forkExists(filepath.Join(childDirectory, "append.md")) {
		t.Fatal("notes written after the fork point were snapshotted")
	}
	childSession.branch(otherBranch) // navigateTree(otherBranch, { summarize: false })
	if got := child.notes(rec{"op": "read", "path": "selected.md"}); got != selected {
		t.Fatalf("navigating the tree changed the owned note:\n%q\n%q", got, selected)
	}
	forkMatch(t, child.notes(rec{"op": "read", "path": "reference.md"}), `^Read-only live reference:.*\noriginal reference$`)
	parent.notes(rec{"op": "write", "path": "reference.md", "content": "current reference"})
	shared := filepath.Join(cwd, ".pi", "notes", "reference.md")
	blockedRead := child.nativeRead(shared)
	if !blockedRead.isError {
		t.Fatal("a native read of a foreign note was allowed")
	}
	forkMatch(t, blockedRead.text, `labeled read-only comparison`)
	blockedEdit := child.nativeEdit(shared, "original reference", "private edit")
	if !blockedEdit.isError {
		t.Fatal("a native edit of a foreign note was allowed")
	}
	forkMatch(t, blockedEdit.text, `Private copy created.*source was not edited`)
	if got := forkRead(t, shared); got != "current reference" {
		t.Fatalf("the source was edited: %q", got)
	}
	forkMatch(t, child.notes(rec{"op": "read", "path": "reference.md"}), `\ncurrent reference$`)
	edited := child.nativeEdit(filepath.Join(childDirectory, "reference.md"), "current reference", "private edit")
	if edited.isError {
		t.Fatalf("editing the private copy failed: %s", edited.text)
	}
	if got := forkRead(t, shared); got != "current reference" {
		t.Fatalf("the source changed: %q", got)
	}
	forkMatch(t, child.notes(rec{"op": "read", "path": "reference.md"}), `\nprivate edit$`)

	// Two parallel appends nested in one codemode call keep the inherited prefix and lose neither line.
	codemode := child.nextID("codemode")
	child.sess.appendMessage(fxAssistant([]any{fxToolCall(codemode, "codemode", rec{"code": "…"})}))
	child.f.Fire("tool_call", rec{"toolName": "codemode", "toolCallId": codemode, "input": rec{"code": "…"}})
	var wg sync.WaitGroup
	for _, content := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := child.notesCall(rec{"op": "append", "path": "append.md", "content": content}, codemode); res.isError {
				t.Errorf("nested append: %s", res.text)
			}
		}()
	}
	wg.Wait()
	child.sess.appendMessage(fxToolResult(codemode, "codemode", []any{fxText("appended")}, false))
	child.f.Fire("tool_execution_end", rec{"toolCallId": codemode, "toolName": "codemode"})
	appended := forkRead(t, filepath.Join(childDirectory, "append.md"))
	if !strings.HasPrefix(appended, "inherited prefix\n") {
		t.Fatalf("append.md = %q", appended)
	}
	got := strings.Split(strings.TrimRight(appended, "\n"), "\n")
	if len(got) != 3 || !(contains(got, "first") && contains(got, "second") && contains(got, "inherited prefix")) {
		t.Fatalf("append.md lines %q", got)
	}
	forkMatch(t, parent.notes(rec{"op": "read", "path": "append.md"}), `\ninherited prefix$`)

	// The parent cannot overwrite the child's private note, nor any managed metadata.
	if res := parent.nativeEdit(filepath.Join(childDirectory, "reference.md"), "private edit", "parent overwrite"); !res.isError {
		t.Fatal("the parent edited the child's private note")
	}
	if got := forkRead(t, filepath.Join(childDirectory, "reference.md")); got != "private edit" {
		t.Fatalf("private note = %q", got)
	}
	manifest := filepath.Join(filepath.Dir(childDirectory), "manifest.json")
	before := forkRead(t, manifest)
	for _, path := range []string{manifest, filepath.Join(cwd, ".pi", "notes", ".forks")} {
		res := parent.nativeWrite(path, "corrupt")
		if !res.isError {
			t.Fatalf("a native write to %s was allowed", path)
		}
		forkMatch(t, res.text, `Managed notes metadata`)
	}
	if forkRead(t, manifest) != before {
		t.Fatal("the manifest changed")
	}

	// A deleted private copy stays deleted: its source reference is not restored, also after a resume.
	forkMust(t, os.Remove(filepath.Join(childDirectory, "reference.md")))
	deleted := child.notesErr(rec{"op": "read", "path": "reference.md"})
	forkMatch(t, deleted.text, `No note`)
	resumed := newForkSide(t, cwd, w.open(childSession))
	resumed.mustStart()
	stillDeleted := resumed.notesErr(rec{"op": "read", "path": "reference.md"})
	forkMatch(t, stillDeleted.text, `No note`)
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// native-host.ts:591 "fork adoption reports a private copy left by failed source verification"
func TestFork_adoptionReportsAPrivateCopyLeftByFailedSourceVerification(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "changing.md", "content": "original reference"})
	otherBranch := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	directory := filepath.Dir(forkFilePath(t, child.notes(rec{"op": "read", "path": "selected.md"})))
	childSession.branch(otherBranch)
	source := filepath.Join(cwd, ".pi", "notes", "changing.md")
	destination := filepath.Join(directory, "changing.md")
	// The original replaces fs.promises.copyFile to change the source right after the copy.
	noteAfterCopy = func(src, _ string) {
		if src == source {
			_ = os.WriteFile(source, []byte("source changed after the copy"), 0o644)
		}
	}
	result := child.notesErr(rec{"op": "append", "path": "changing.md", "content": "not appended"})
	noteAfterCopy = nil
	forkMatch(t, result.text, `Private copy created.*verification failed.*Read the private copy`)
	if got := forkRead(t, destination); got != "original reference" {
		t.Fatalf("private copy = %q", got)
	}
	if got := forkRead(t, source); got != "source changed after the copy" {
		t.Fatalf("source = %q", got)
	}
	forkMatch(t, child.notes(rec{"op": "read", "path": "changing.md"}), `\noriginal reference$`)
	forkMust(t, os.Remove(destination))
	deleted := child.notesErr(rec{"op": "read", "path": "changing.md"})
	forkMatch(t, deleted.text, `No note`) // deleting a private copy must not restore its source reference
	child.notes(rec{"op": "append", "path": "changing.md", "content": "private update"})
	if got := forkRead(t, destination); got != "private update\n" {
		t.Fatalf("private copy = %q", got)
	}
	if got := forkRead(t, source); got != "source changed after the copy" {
		t.Fatalf("source = %q", got)
	}
}
