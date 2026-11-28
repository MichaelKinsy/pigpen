package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// upstream: packages/coding-agent/test/commit-selective-flow.test.ts

const flowChangelog = "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- Fixed an old bug\n"

func testGit(t *testing.T, cwd string, extraEnv []string, args ...string) string {
	t.Helper()
	out, err := testGitErr(cwd, extraEnv, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func testGitErr(cwd string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	env := make([]string, 0, len(os.Environ())+len(extraEnv)+4)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") ||
			strings.HasPrefix(kv, "GIT_WORK_TREE=") ||
			strings.HasPrefix(kv, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") ||
			strings.HasPrefix(kv, "GIT_CONFIG_SYSTEM=") ||
			strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+cwd,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func testGitBlob(t *testing.T, cwd, spec string) []byte {
	t.Helper()
	repo, err := discoverGitRepo(context.Background(), cwd)
	if err != nil || repo == nil {
		t.Fatalf("discoverGitRepo(%s): %v", cwd, err)
	}
	b, err := repo.showBlob(context.Background(), spec)
	if err != nil {
		t.Fatalf("showBlob(%s): %v", spec, err)
	}
	return b
}

func writeFileMust(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileMust(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

type testWorkspace struct {
	root      string
	artifacts string
	journal   *ChangeJournal
}

func (w *testWorkspace) write(t *testing.T, agent, parent, relPath, after string) {
	t.Helper()
	abs := filepath.Join(w.root, filepath.FromSlash(relPath))
	var before *string
	if b, err := os.ReadFile(abs); err == nil {
		s := string(b)
		before = &s
	}
	writeFileMust(t, abs, []byte(after))
	afterCopy := after
	w.journal.RecordChange(ChangeInput{
		Agent:  agent,
		Parent: parent,
		Tool:   "edit",
		Path:   abs,
		Before: before,
		After:  &afterCopy,
	})
}

func newEmptyRepo(t *testing.T) *testWorkspace {
	t.Helper()
	root := t.TempDir()
	artifacts := t.TempDir()
	testGit(t, root, nil, "init", "-q", "-b", "main")
	testGit(t, root, nil, "config", "user.email", "test@example.com")
	testGit(t, root, nil, "config", "user.name", "Test")
	testGit(t, root, nil, "config", "commit.gpgSign", "false")
	testGit(t, root, nil, "config", "core.autocrlf", "false")
	return &testWorkspace{
		root:      root,
		artifacts: artifacts,
		journal:   NewChangeJournal(artifacts),
	}
}

type gitlinkWS struct {
	*testWorkspace
	module string
	before string
	after  string
	later  string
}

func newGitlinkWorkspace(t *testing.T) *gitlinkWS {
	t.Helper()
	ws := newEmptyRepo(t)
	mod := filepath.Join(ws.root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, mod, nil, "init", "-q", "-b", "main")
	testGit(t, mod, nil, "config", "user.email", "test@example.com")
	testGit(t, mod, nil, "config", "user.name", "Test")
	testGit(t, mod, nil, "config", "commit.gpgSign", "false")
	var commits []string
	for _, content := range []string{"one\n", "two\n", "three\n"} {
		writeFileMust(t, filepath.Join(mod, "content.txt"), []byte(content))
		testGit(t, mod, nil, "add", "content.txt")
		testGit(t, mod, nil, "commit", "-qm", strings.TrimSpace(content))
		commits = append(commits, strings.TrimSpace(testGit(t, mod, nil, "rev-parse", "HEAD")))
	}
	before, after, later := commits[0], commits[1], commits[2]
	testGit(t, mod, nil, "checkout", "-q", before)
	writeFileMust(t, filepath.Join(ws.root, "user.txt"), []byte("original\n"))
	testGit(t, ws.root, nil, "add", "user.txt")
	testGit(t, ws.root, nil, "update-index", "--add", "--cacheinfo", fmt.Sprintf("160000,%s,module", before))
	testGit(t, ws.root, nil, "commit", "-qm", "seed gitlink")
	testGit(t, mod, nil, "checkout", "-q", after)
	return &gitlinkWS{testWorkspace: ws, module: mod, before: before, after: after, later: later}
}

func newSharedWorkspace(t *testing.T) (*testWorkspace, string) {
	t.Helper()
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "CHANGELOG.md"), []byte(flowChangelog))
	writeFileMust(t, filepath.Join(ws.root, "a.ts"), []byte("export const a = 1;\n"))
	writeFileMust(t, filepath.Join(ws.root, "b.ts"), []byte("export const b = 1;\n"))
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
	head := strings.TrimSpace(testGit(t, ws.root, nil, "rev-parse", "HEAD"))

	writeFileMust(t, filepath.Join(ws.root, "b.ts"), []byte("export const b = 2;\n"))
	testGit(t, ws.root, nil, "add", "b.ts")
	userChangelog := strings.Replace(flowChangelog, "### Fixed\n\n", "### Fixed\n\n- Fixed the theme flicker\n", 1)
	writeFileMust(t, filepath.Join(ws.root, "CHANGELOG.md"), []byte(userChangelog))

	ws.write(t, "Main", "", "a.ts", "export const a = 2;\n")
	ws.write(t, "Main", "", "CHANGELOG.md", strings.Replace(userChangelog, "### Fixed\n\n", "### Fixed\n\n- Fixed /commit\n", 1))
	ws.write(t, "ParserFix", "Main", "parser.ts", "export function parse() {}\n")
	ws.journal.Flush()
	return ws, head
}

func installSubjectRewriteHook(t *testing.T, root string) {
	t.Helper()
	testGit(t, root, nil, "config", "core.hooksPath", ".git/hooks")
	hook := filepath.Join(root, ".git", "hooks", "commit-msg")
	script := "#!/bin/sh\n" +
		"cat > \"$1\" <<'EOF'\n" +
		"fix(notes): kept the rewritten subject\n" +
		"\n" +
		"- Hook rewrote the body.\n" +
		"EOF\n"
	writeFileMust(t, hook, []byte(script))
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
}

func commitIsRecorded(journalFile, sha string) bool {
	b, err := os.ReadFile(journalFile)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if line == "" {
			continue
		}
		var rec JournalRecord
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec.Type == "commit" && rec.SHA == sha {
			return true
		}
	}
	return false
}

func selectionPaths(sel []SelectedChange) []string {
	out := make([]string, len(sel))
	for i, s := range sel {
		out[i] = s.Path
	}
	return out
}

// upstream: commit-selective-flow.test.ts: "commits an inserted statement before a return changed only in the worktree"
func TestFlowInsertBeforeChangedReturn(t *testing.T) {
	ws := newEmptyRepo(t)
	file := "value.luau"
	head := "local function value()\n    return 1\nend\n"
	foreign := strings.Replace(head, "return 1", "return 2", 1)
	statement := "    assert(true)\n"
	insert := func(src string) string { return strings.Replace(src, "    return", statement+"    return", 1) }

	writeFileMust(t, filepath.Join(ws.root, file), []byte(head))
	testGit(t, ws.root, nil, "add", file)
	testGit(t, ws.root, nil, "commit", "-qm", "seed")
	ws.write(t, "Other", "", file, foreign)
	ws.write(t, "Main", "", file, insert(foreign))
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(draft, CommitRequest{Subject: "test: assert before return"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:"+file); got != insert(head) {
		t.Fatalf("HEAD:%s = %q, want %q", file, got, insert(head))
	}
	if got := readFileMust(t, filepath.Join(ws.root, file)); got != insert(foreign) {
		t.Fatalf("worktree = %q, want %q", got, insert(foreign))
	}

	replay, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Selection()) != 0 {
		t.Fatalf("expected empty replay selection, got %v", replay.Selection())
	}
	if strings.Contains(replay.Summary(), "Review:") {
		t.Fatalf("unexpected Review in replay summary:\n%s", replay.Summary())
	}
	remaining, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(remaining, CommitRequest{Subject: "test: update return"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Other"}}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:"+file); got != insert(foreign) {
		t.Fatalf("final HEAD:%s = %q, want %q", file, got, insert(foreign))
	}
	if status := testGit(t, ws.root, nil, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean status, got %q", status)
	}
}

// upstream: commit-selective-flow.test.ts: "never selects a second copy of a paragraph already committed during a reorder"
func TestFlowReorderDoesNotDuplicateParagraph(t *testing.T) {
	ws := newEmptyRepo(t)
	file := "notes.md"
	contents := func(order, mainVal, otherVal string) string {
		var b strings.Builder
		for _, ch := range order {
			fmt.Fprintf(&b, "## %c\n\nsame body\n\n\n", ch)
		}
		fmt.Fprintf(&b, "footer\nmain: %s\nseparator\nother: %s\n", mainVal, otherVal)
		return b.String()
	}
	writeFileMust(t, filepath.Join(ws.root, file), []byte(contents("AB", "old", "old")))
	testGit(t, ws.root, nil, "add", file)
	testGit(t, ws.root, nil, "commit", "-qm", "initial paragraphs")
	ws.write(t, "Main", "", file, contents("BAC", "old", "old"))
	ws.write(t, "Other", "", file, contents("BADC", "old", "pending"))

	snapPath := filepath.Join(ws.root, ".git", "snapshot")
	writeFileMust(t, snapPath, []byte(contents("ABD", "old", "old")))
	sha := strings.TrimSpace(testGit(t, ws.root, nil, "hash-object", "-w", ".git/snapshot"))
	testGit(t, ws.root, nil, "update-index", "--cacheinfo", fmt.Sprintf("100644,%s,%s", sha, file))
	testGit(t, ws.root, nil, "commit", "-qm", "take D")
	ws.write(t, "Main", "", file, contents("ABCD", "pending", "pending"))
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Summary(), "review the net change") {
		t.Fatalf("expected reorder review in summary:\n%s", draft.Summary())
	}
	if !strings.Contains(draft.Diff(), "+main: pending") || strings.Contains(draft.Diff(), "+## B") {
		t.Fatalf("unexpected diff:\n%s", draft.Diff())
	}
	if _, err := draft.Take(SelectionTarget{Path: &file, Units: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(draft, CommitRequest{Subject: "add C"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:"+file); got != contents("ABCD", "pending", "old") {
		t.Fatalf("HEAD:%s mismatch:\ngot:\n%s\nwant:\n%s", file, got, contents("ABCD", "pending", "old"))
	}
	if got := readFileMust(t, filepath.Join(ws.root, file)); got != contents("ABCD", "pending", "pending") {
		t.Fatalf("worktree mismatch")
	}
}

// upstream: commit-selective-flow.test.ts: "explicitly selects a gitlink SHA with ordinary edits without committing child dirt or other staged work"
func TestFlowGitlinkSelectAndCommit(t *testing.T) {
	ws := newGitlinkWorkspace(t)
	writeFileMust(t, filepath.Join(ws.root, "user.txt"), []byte("user staged\n"))
	testGit(t, ws.root, nil, "add", "user.txt")
	ws.write(t, "Main", "", "agent.txt", "agent\n")
	ws.journal.Flush()
	writeFileMust(t, filepath.Join(ws.module, "content.txt"), []byte("child dirty\n"))
	childStatus := testGit(t, ws.module, nil, "status", "--porcelain")

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := selectionPaths(draft.Selection()); !reflect.DeepEqual(got, []string{"agent.txt"}) {
		t.Fatalf("initial selection = %v, want [agent.txt]", got)
	}
	shown, err := draft.Show(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, ws.before+" → "+ws.after) {
		t.Fatalf("show missing gitlink transition:\n%s", shown)
	}
	if _, err := draft.Take(SelectionTarget{Path: new("module"), Units: []string{"2"}}); err == nil || !strings.Contains(err.Error(), "only unit 1") {
		t.Fatalf("expected only unit 1 error, got %v", err)
	}
	if _, err := draft.Take(SelectionTarget{Path: new("module"), Units: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Drop(SelectionTarget{Path: new("module")}); err != nil {
		t.Fatal(err)
	}
	if got := selectionPaths(draft.Selection()); !reflect.DeepEqual(got, []string{"agent.txt"}) {
		t.Fatalf("after drop = %v", got)
	}
	if _, err := draft.Take(SelectionTarget{Path: new("module")}); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Only([]string{"module", "agent.txt"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Diff(), fmt.Sprintf("-Subproject commit %s\n+Subproject commit %s\n", ws.before, ws.after)) {
		t.Fatalf("diff missing subproject commit lines:\n%s", draft.Diff())
	}
	outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix: update module and agent"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	if err != nil {
		t.Fatal(err)
	}
	sortedPaths := append([]string(nil), outcome.Paths...)
	sort.Strings(sortedPaths)
	if !reflect.DeepEqual(sortedPaths, []string{"agent.txt", "module"}) {
		t.Fatalf("paths = %v", sortedPaths)
	}
	if len(outcome.IndexNotes) != 0 {
		t.Fatalf("unexpected indexNotes: %v", outcome.IndexNotes)
	}
	if got := testGit(t, ws.root, nil, "ls-tree", "HEAD", "module"); got != fmt.Sprintf("160000 commit %s\tmodule\n", ws.after) {
		t.Fatalf("ls-tree module = %q", got)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:agent.txt"); got != "agent\n" {
		t.Fatalf("HEAD:agent.txt = %q", got)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:user.txt"); got != "original\n" {
		t.Fatalf("HEAD:user.txt = %q", got)
	}
	if got := testGit(t, ws.root, nil, "diff", "--cached", "--name-only"); got != "user.txt\n" {
		t.Fatalf("staged = %q", got)
	}
	if got := testGit(t, ws.module, nil, "status", "--porcelain"); got != childStatus {
		t.Fatalf("child status changed: %q vs %q", got, childStatus)
	}
}

// upstream: commit-selective-flow.test.ts: gitlink edge cases
func TestFlowGitlinkEdgeCases(t *testing.T) {
	t.Run("dirty child only is not offered", func(t *testing.T) {
		ws := newGitlinkWorkspace(t)
		testGit(t, ws.module, nil, "checkout", "-q", ws.before)
		writeFileMust(t, filepath.Join(ws.module, "content.txt"), []byte("dirty\n"))
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range draft.Paths() {
			if p == "module" {
				t.Fatalf("unexpected module in draft paths")
			}
		}
	})

	t.Run("rejects changed child HEAD after drafting", func(t *testing.T) {
		ws := newGitlinkWorkspace(t)
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := draft.Take(SelectionTarget{Path: new("module")}); err != nil {
			t.Fatal(err)
		}
		testGit(t, ws.module, nil, "checkout", "-q", ws.later)
		headBefore := testGit(t, ws.root, nil, "rev-parse", "HEAD")
		idxBefore := testGit(t, ws.root, nil, "ls-files", "--stage")
		_, err = CommitSelection(draft, CommitRequest{Subject: "fix: stale"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err == nil || !strings.Contains(err.Error(), "submodule HEAD changed") {
			t.Fatalf("expected submodule HEAD changed error, got %v", err)
		}
		if testGit(t, ws.root, nil, "rev-parse", "HEAD") != headBefore || testGit(t, ws.root, nil, "ls-files", "--stage") != idxBefore {
			t.Fatalf("HEAD or index changed after rejection")
		}
	})

	t.Run("preserves different staged gitlink while committing selected worktree SHA", func(t *testing.T) {
		ws := newGitlinkWorkspace(t)
		testGit(t, ws.root, nil, "update-index", "--cacheinfo", fmt.Sprintf("160000,%s,module", ws.later))
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := draft.Take(SelectionTarget{Path: new("module")}); err != nil {
			t.Fatal(err)
		}
		outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix: selected pointer"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(testGit(t, ws.root, nil, "rev-parse", "HEAD:module")); got != ws.after {
			t.Fatalf("HEAD:module = %q, want %q", got, ws.after)
		}
		if got := testGit(t, ws.root, nil, "ls-files", "--stage", "module"); got != fmt.Sprintf("160000 %s 0\tmodule\n", ws.later) {
			t.Fatalf("staged module = %q", got)
		}
		if len(outcome.IndexNotes) == 0 || !strings.Contains(outcome.IndexNotes[0], "someone else's staged changes") {
			t.Fatalf("expected staged changes note, got %v", outcome.IndexNotes)
		}
	})

	t.Run("rejects conflicting superproject commit", func(t *testing.T) {
		ws := newGitlinkWorkspace(t)
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := draft.Take(SelectionTarget{Path: new("module")}); err != nil {
			t.Fatal(err)
		}
		testGit(t, ws.root, nil, "update-index", "--cacheinfo", fmt.Sprintf("160000,%s,module", ws.later))
		testGit(t, ws.root, nil, "commit", "-qm", "other pointer")
		_, err = CommitSelection(draft, CommitRequest{Subject: "fix: conflict"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err == nil || !strings.Contains(err.Error(), "HEAD moved") || !strings.Contains(err.Error(), "module") {
			t.Fatalf("expected HEAD moved module error, got %v", err)
		}
	})

	for _, state := range []string{"uninitialized", "deleted", "file"} {
		t.Run("skips "+state+" gitlink checkout", func(t *testing.T) {
			ws := newGitlinkWorkspace(t)
			if err := os.RemoveAll(ws.module); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "uninitialized":
				if err := os.MkdirAll(ws.module, 0o755); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeFileMust(t, ws.module, []byte("not a checkout\n"))
			}
			ws.write(t, "Main", "", "agent.txt", "agent\n")
			ws.journal.Flush()
			draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
			if err != nil {
				t.Fatal(err)
			}
			if got := selectionPaths(draft.Selection()); !reflect.DeepEqual(got, []string{"agent.txt"}) {
				t.Fatalf("selection = %v", got)
			}
		})
	}
}

// upstream: commit-selective-flow.test.ts: "commits the agent tree's lines and leaves the user's staged and unstaged work alone"
func TestFlowSharedWorkspaceIsolation(t *testing.T) {
	ws, head := newSharedWorkspace(t)
	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := CommitSelection(draft, CommitRequest{
		Subject: "fix(parser): added parse",
		Body:    "- Added the parser.",
		Fixes:   []string{"7"},
	}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Parent == nil || *outcome.Parent != head {
		t.Fatalf("parent = %v, want %s", outcome.Parent, head)
	}
	sorted := append([]string(nil), outcome.Paths...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(sorted, []string{"CHANGELOG.md", "a.ts", "parser.ts"}) {
		t.Fatalf("paths = %v", sorted)
	}
	wantChangelog := strings.Replace(flowChangelog, "### Fixed\n\n", "### Fixed\n\n- Fixed /commit\n", 1)
	if got := testGit(t, ws.root, nil, "show", "HEAD:CHANGELOG.md"); got != wantChangelog {
		t.Fatalf("HEAD:CHANGELOG.md = %q, want %q", got, wantChangelog)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:b.ts"); got != "export const b = 1;\n" {
		t.Fatalf("HEAD:b.ts = %q", got)
	}
	if got := testGit(t, ws.root, nil, "log", "-1", "--format=%B"); got != "fix(parser): added parse\n\n- Added the parser.\n\nFixes #7\n\n" {
		t.Fatalf("commit message = %q", got)
	}
	if got := strings.TrimSpace(testGit(t, ws.root, nil, "diff", "--cached", "--name-only")); got != "b.ts" {
		t.Fatalf("staged = %q, want b.ts", got)
	}
	if got := testGit(t, ws.root, nil, "diff", "--name-only"); got != "CHANGELOG.md\n" {
		t.Fatalf("unstaged = %q, want CHANGELOG.md", got)
	}
	if !strings.Contains(readFileMust(t, filepath.Join(ws.root, "CHANGELOG.md")), "- Fixed the theme flicker") {
		t.Fatalf("user changelog line lost")
	}
}

// upstream: commit-selective-flow.test.ts: "syncs the shared index once another git process lets go of its lock"
func TestFlowSharedIndexLockRetry(t *testing.T) {
	ws, _ := newSharedWorkspace(t)
	lock := filepath.Join(ws.root, ".git", "index.lock")
	writeFileMust(t, lock, nil)
	done := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.Remove(lock)
		close(done)
	}()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix: locked"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.IndexNotes) != 0 {
		t.Fatalf("unexpected indexNotes: %v", outcome.IndexNotes)
	}
	if got := strings.TrimSpace(testGit(t, ws.root, nil, "diff", "--cached", "--name-only")); got != "b.ts" {
		t.Fatalf("staged = %q", got)
	}
}

// upstream: commit-selective-flow.test.ts: "runs hooks without reporting sparse-checkout paths as unstaged deletions"
func TestFlowSparseCheckoutHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell hook test")
	}
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "kept/inside"), []byte("before\n"))
	writeFileMust(t, filepath.Join(ws.root, "away/deep/outside"), []byte("outside\n"))
	writeFileMust(t, filepath.Join(ws.root, "away/other"), []byte("other\n"))
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
	testGit(t, ws.root, nil, "sparse-checkout", "init", "--cone", "--sparse-index")
	testGit(t, ws.root, nil, "sparse-checkout", "set", "kept")

	marker := filepath.Join(ws.root, ".git", "omp-hook-sparse.txt")
	hook := filepath.Join(ws.root, ".git", "hooks", "pre-commit")
	writeFileMust(t, hook, []byte(strings.Join([]string{
		"#!/bin/sh",
		"set -e",
		`[ -z "$(git diff --name-only)" ]`,
		"git add kept/inside",
		`git ls-files --sparse > "$GIT_DIR/omp-hook-sparse.txt"`,
	}, "\n")+"\n"))
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	ws.write(t, "Main", "", "kept/inside", "after\n")
	ws.journal.Flush()
	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(draft, CommitRequest{Subject: "fix: sparse commit"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
		t.Fatal(err)
	}
	gotMarker := strings.Split(strings.TrimSpace(readFileMust(t, marker)), "\n")
	sort.Strings(gotMarker)
	if !reflect.DeepEqual(gotMarker, []string{"away/", "kept/inside"}) {
		t.Fatalf("hook sparse listing = %v", gotMarker)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:kept/inside"); got != "after\n" {
		t.Fatalf("HEAD:kept/inside = %q", got)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:away/deep/outside"); got != "outside\n" {
		t.Fatalf("HEAD:away/deep/outside = %q", got)
	}
	testGit(t, ws.root, nil, "fsck", "--strict")
}

// upstream: commit-selective-flow.test.ts: "syncs the shared index to what a formatting pre-commit hook committed"
func TestFlowPreCommitFormatterHookSyncsSharedIndex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell hook test")
	}
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "fixture.txt"), []byte("let value = 0\n"))
	writeFileMust(t, filepath.Join(ws.root, "other.txt"), []byte("other\n"))
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
	writeFileMust(t, filepath.Join(ws.root, "other.txt"), []byte("staged by the user\n"))
	testGit(t, ws.root, nil, "add", "other.txt")
	testGit(t, ws.root, nil, "config", "core.hooksPath", ".git/hooks")
	hook := filepath.Join(ws.root, ".git", "hooks", "pre-commit")
	writeFileMust(t, hook, []byte(strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"sed 's/value=/value = /' fixture.txt > fixture.tmp",
		"mv fixture.tmp fixture.txt",
		"blob=$(git hash-object -w fixture.txt)",
		`git update-index --cacheinfo "100644,$blob,fixture.txt"`,
	}, "\n")+"\n"))
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, value := range []int{1, 2} {
		ws.write(t, "Main", "", "fixture.txt", fmt.Sprintf("let value=%d\n", value))
		ws.journal.Flush()
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := CommitSelection(draft, CommitRequest{Subject: fmt.Sprintf("fix: set value %d", value)}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		formatted := fmt.Sprintf("let value = %d\n", value)
		if len(outcome.IndexNotes) != 0 {
			t.Fatalf("value=%d: unexpected indexNotes: %v", value, outcome.IndexNotes)
		}
		if got := testGit(t, ws.root, nil, "show", "HEAD:fixture.txt"); got != formatted {
			t.Fatalf("HEAD:fixture.txt = %q, want %q", got, formatted)
		}
		if got := testGit(t, ws.root, nil, "show", ":fixture.txt"); got != formatted {
			t.Fatalf(":fixture.txt = %q, want %q", got, formatted)
		}
		if got := testGit(t, ws.root, nil, "status", "--porcelain"); got != "M  other.txt\n" {
			t.Fatalf("status = %q", got)
		}
	}
}

// upstream: commit-selective-flow.test.ts: "commits only one subagent's changes when scoped to it"
func TestFlowSingleSubagentScope(t *testing.T) {
	ws, _ := newSharedWorkspace(t)
	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main", Agents: ScopeString("ParserFix")})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := CommitSelection(draft, CommitRequest{Subject: "feat: added parser"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Paths, []string{"parser.ts"}) {
		t.Fatalf("paths = %v", outcome.Paths)
	}
	if got := strings.TrimSpace(testGit(t, ws.root, nil, "show", "--name-only", "--format=", "HEAD")); got != "parser.ts" {
		t.Fatalf("HEAD files = %q", got)
	}
}

// TestFlowDashPrefixedFilename verifies files whose names begin with `-` (e.g. `-n.txt`,
// plus a binary `-b.bin`) are drafted, diffed, and committed safely without option injection.
func TestFlowDashPrefixedFilename(t *testing.T) {
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "-n.txt"), []byte("alpha\n"))
	writeFileMust(t, filepath.Join(ws.root, "-b.bin"), []byte{0, 1, 2})
	testGit(t, ws.root, nil, "add", "--", "-n.txt", "-b.bin")
	testGit(t, ws.root, nil, "commit", "-qm", "seed")

	ws.write(t, "Main", "", "-n.txt", "alpha\nbeta\n")
	writeFileMust(t, filepath.Join(ws.root, "-b.bin"), []byte{0, 3, 4, 5})
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Take(SelectionTarget{Path: new("-b.bin")}); err != nil {
		t.Fatal(err)
	}
	outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix: dash filename"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	if err != nil {
		t.Fatal(err)
	}
	sorted := append([]string(nil), outcome.Paths...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(sorted, []string{"-b.bin", "-n.txt"}) {
		t.Fatalf("paths = %v", sorted)
	}
	if got := testGit(t, ws.root, nil, "show", "HEAD:-n.txt"); got != "alpha\nbeta\n" {
		t.Fatalf("HEAD:-n.txt = %q", got)
	}
	if !bytes.Equal(testGitBlob(t, ws.root, "HEAD:-b.bin"), []byte{0, 3, 4, 5}) {
		t.Fatalf("HEAD:-b.bin mismatch")
	}
	if status := testGit(t, ws.root, nil, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean status, got %q", status)
	}
}

// upstream: commit-selective-flow.test.ts: "commits modes, executable deletions, symlinks, BOMs, and non-UTF-8 files exactly as in the worktree"
func TestFlowModesSymlinksBOMsNonUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/chmod test")
	}
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "run.sh"), []byte("#!/bin/sh\necho run\n"))
	writeFileMust(t, filepath.Join(ws.root, "tool.sh"), []byte("#!/bin/sh\necho tool\n"))
	writeFileMust(t, filepath.Join(ws.root, "plain.txt"), []byte("plain\n"))
	writeFileMust(t, filepath.Join(ws.root, "latin1.txt"), []byte{'c', 'a', 'f', 0xe9, '\n'})
	writeFileMust(t, filepath.Join(ws.root, "bom.ass"), []byte("placeholder\n"))
	_ = os.Chmod(filepath.Join(ws.root, "run.sh"), 0o755)
	_ = os.Chmod(filepath.Join(ws.root, "tool.sh"), 0o755)
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")

	_ = os.Chmod(filepath.Join(ws.root, "run.sh"), 0o644)
	_ = os.Remove(filepath.Join(ws.root, "tool.sh"))
	ws.write(t, "Main", "", "plain.txt", "plain\nmore\n")
	_ = os.Chmod(filepath.Join(ws.root, "plain.txt"), 0o755)
	writeFileMust(t, filepath.Join(ws.root, "latin1.txt"), []byte{'c', 'a', 'f', 0xe9, '\n', 'n', 'a', 0xef, 'v', 'e', '\n'})
	writeFileMust(t, filepath.Join(ws.root, "bom.ass"), []byte("\uFEFF[Script Info]\r\nTitle: d\r\n"))
	if err := os.Symlink("plain.txt", filepath.Join(ws.root, "link")); err != nil {
		t.Fatal(err)
	}
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Take(SelectionTarget{Path: new("**")}); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(draft, CommitRequest{Subject: "chore: committed everything"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
		t.Fatal(err)
	}

	oracleIdx := filepath.Join(ws.root, ".git", "oracle-index")
	oracleEnv := []string{"GIT_INDEX_FILE=" + oracleIdx}
	testGit(t, ws.root, oracleEnv, "read-tree", "HEAD~1")
	testGit(t, ws.root, oracleEnv, "add", "-A")
	wantTree := testGit(t, ws.root, oracleEnv, "write-tree")
	gotTree := testGit(t, ws.root, nil, "rev-parse", "HEAD^{tree}")
	if gotTree != wantTree {
		t.Fatalf("HEAD tree %s != oracle tree %s", strings.TrimSpace(gotTree), strings.TrimSpace(wantTree))
	}
	if status := testGit(t, ws.root, nil, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean status, got %q", status)
	}
}

// upstream: commit-selective-flow.test.ts: "makes the first commit of a repository without commits" & only/unmatched errors
func TestFlowFirstCommitAndSelectionErrors(t *testing.T) {
	ws := newEmptyRepo(t)
	ws.write(t, "Main", "", "first.txt", "first\n")
	ws.write(t, "Main", "", "second.txt", "second\n")
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Summary(), "✓ first.txt  +1  [Main]") {
		t.Fatalf("summary:\n%s", draft.Summary())
	}
	report, err := draft.Only([]string{"first.txt", "docs/**", "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "Deselected 1 path(s)") || !strings.Contains(report, "Unmatched: docs/**, missing.txt") {
		t.Fatalf("report = %q", report)
	}
	if _, err := draft.Only([]string{"missing.txt", "docs/**"}); err == nil || !strings.Contains(err.Error(), "No changed path matches") {
		t.Fatalf("expected all-unmatched error, got %v", err)
	}
	for _, bad := range []string{"missing.txt", "docs/**"} {
		if _, err := draft.Take(SelectionTarget{Path: &bad}); err == nil || !strings.Contains(err.Error(), "No changed path matches") {
			t.Fatalf("Take(%q) err = %v", bad, err)
		}
		if _, err := draft.Drop(SelectionTarget{Path: &bad}); err == nil || !strings.Contains(err.Error(), "No changed path matches") {
			t.Fatalf("Drop(%q) err = %v", bad, err)
		}
		if _, err := draft.Show(&bad); err == nil || !strings.Contains(err.Error(), "No changed path matches") {
			t.Fatalf("Show(%q) err = %v", bad, err)
		}
	}
	outcome, err := CommitSelection(draft, CommitRequest{Subject: "feat: first"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Parent != nil {
		t.Fatalf("expected nil parent on root commit, got %v", *outcome.Parent)
	}
	if got := testGit(t, ws.root, nil, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("rev-list count = %q", got)
	}
	if got := testGit(t, ws.root, nil, "ls-tree", "--name-only", "HEAD"); got != "first.txt\n" {
		t.Fatalf("ls-tree = %q", got)
	}
	if got := testGit(t, ws.root, nil, "status", "--porcelain"); got != "?? second.txt\n" {
		t.Fatalf("status = %q", got)
	}
}

// upstream: commit-selective-flow.test.ts: "attributes edits journaled through a symlinked directory"
func TestFlowSymlinkedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test")
	}
	ws := newEmptyRepo(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(ws.root, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(alias, "notes.md")
	writeFileMust(t, file, []byte("hello\n"))
	ws.journal.RecordChange(ChangeInput{Agent: "Main", Tool: "write", Path: file, Before: nil, After: new("hello\n")})
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Summary(), "✓ notes.md  +1  [Main]") {
		t.Fatalf("summary:\n%s", draft.Summary())
	}
}

// upstream: commit-selective-flow.test.ts: "lists what no edit tool changed as unrecorded and commits it once taken"
func TestFlowUnrecordedChanges(t *testing.T) {
	ws := newEmptyRepo(t)
	for _, file := range []string{"gone.ts", "old.ts", "generated.ts", "user.ts"} {
		writeFileMust(t, filepath.Join(ws.root, file), []byte(fmt.Sprintf("export const file = %q;\n", file)))
	}
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
	ws.write(t, "Main", "", "own.ts", "export const own = true;\n")
	_ = os.Remove(filepath.Join(ws.root, "gone.ts"))
	_ = os.Rename(filepath.Join(ws.root, "old.ts"), filepath.Join(ws.root, "new.ts"))
	writeFileMust(t, filepath.Join(ws.root, "generated.ts"), []byte("export const generated = true;\n"))
	writeFileMust(t, filepath.Join(ws.root, "user.ts"), []byte("export const user = true;\n"))
	ws.journal.Flush()

	draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	summary := draft.Summary()
	if !reflect.DeepEqual(selectionPaths(draft.Selection()), []string{"own.ts"}) {
		t.Fatalf("selection = %v", selectionPaths(draft.Selection()))
	}
	if strings.Contains(summary, "Review:") || !strings.Contains(summary, "Unrecorded: 5 path(s)") {
		t.Fatalf("summary:\n%s", summary)
	}
	for _, f := range []string{"generated.ts", "gone.ts", "new.ts", "old.ts"} {
		if _, err := draft.Take(SelectionTarget{Path: &f}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CommitSelection(draft, CommitRequest{Subject: "refactor: moved files"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, ws.root, nil, "ls-tree", "-r", "--name-only", "HEAD"); got != "generated.ts\nnew.ts\nown.ts\nuser.ts\n" {
		t.Fatalf("ls-tree = %q", got)
	}
	if got := testGit(t, ws.root, nil, "status", "--porcelain"); got != " M user.ts\n" {
		t.Fatalf("status = %q", got)
	}
}

// upstream: commit-selective-flow.test.ts: formatter rewrite review & unreplayable record review
func TestFlowFormatterAndUnreplayableReview(t *testing.T) {
	t.Run("formatter rewrite review", func(t *testing.T) {
		ws := newEmptyRepo(t)
		head := "fn a() {}\n\nfn keep() {}\n\nfn user() {}\n"
		writeFileMust(t, filepath.Join(ws.root, "lib.rs"), []byte(head))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
		edited := strings.Replace(head, "fn a() {}", "fn a() { let  x=1; }", 1)
		ws.write(t, "Main", "", "lib.rs", edited)
		formatted := strings.Replace(edited, "fn a() { let  x=1; }", "fn a() {\n    let x = 1;\n}", 1)
		work := strings.Replace(formatted, "fn user() {}", "fn user() { todo!() }", 1)
		writeFileMust(t, filepath.Join(ws.root, "lib.rs"), []byte(work))
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if len(draft.Selection()) != 0 {
			t.Fatalf("expected empty preselection")
		}
		if !strings.Contains(draft.Summary(), "? lib.rs  hunk 1: in-scope lines rewritten afterwards by an unrecorded change") {
			t.Fatalf("summary:\n%s", draft.Summary())
		}
		if _, err := draft.Take(SelectionTarget{Path: new("lib.rs"), Units: []string{"1"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := CommitSelection(draft, CommitRequest{Subject: "feat: a"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
			t.Fatal(err)
		}
		if got := testGit(t, ws.root, nil, "show", "HEAD:lib.rs"); got != formatted {
			t.Fatalf("HEAD:lib.rs = %q, want %q", got, formatted)
		}
	})

	t.Run("unreplayable edit record", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "data.txt"), []byte("one\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
		ws.write(t, "Main", "", "data.txt", "one\x00two\n")
		writeFileMust(t, filepath.Join(ws.root, "data.txt"), []byte("one\ntwo\n"))
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(draft.Summary(), "? data.txt  +1; edited by Main, but the edit record cannot be replayed") {
			t.Fatalf("summary:\n%s", draft.Summary())
		}
	})
}

// upstream: commit-selective-flow.test.ts: HEAD moved conflict & hook race retry
func TestFlowHeadMovedAndRetry(t *testing.T) {
	t.Run("refuses conflicting HEAD move", func(t *testing.T) {
		ws, _ := newSharedWorkspace(t)
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main", Agents: ScopeString("self")})
		if err != nil {
			t.Fatal(err)
		}
		testGit(t, ws.root, nil, "commit", "-q", "-m", "other", "--only", "b.ts")
		writeFileMust(t, filepath.Join(ws.root, "a.ts"), []byte("export const a = 3;\n"))
		testGit(t, ws.root, nil, "commit", "-q", "-m", "conflict", "--only", "a.ts")

		_, err = CommitSelection(draft, CommitRequest{Subject: "fix: a"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err == nil || !strings.Contains(err.Error(), "HEAD moved") || !strings.Contains(err.Error(), "a.ts") {
			t.Fatalf("expected HEAD moved a.ts error, got %v", err)
		}
	})

	t.Run("retries commit when commit-msg hook moves HEAD", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell hook test")
		}
		ws, head := newSharedWorkspace(t)
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		testGit(t, ws.root, nil, "config", "core.hooksPath", ".git/hooks")
		moved := strings.TrimSpace(testGit(t, ws.root, nil, "commit-tree", head+"^{tree}", "-p", head, "-m", "concurrent"))
		hook := filepath.Join(ws.root, ".git", "hooks", "commit-msg")
		writeFileMust(t, hook, []byte(fmt.Sprintf("#!/bin/sh\ngit update-ref refs/heads/main %s\n", moved)))
		_ = os.Chmod(hook, 0o755)

		outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix(parser): added parse"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Parent == nil || *outcome.Parent != moved {
			t.Fatalf("parent = %v, want %s", outcome.Parent, moved)
		}
		if got := strings.TrimSpace(testGit(t, ws.root, nil, "rev-parse", "HEAD~1")); got != moved {
			t.Fatalf("HEAD~1 = %q, want %q", got, moved)
		}
	})
}

// upstream: commit-selective-flow.test.ts: refuses during merge / cherry-pick / revert / rebase / git am / unmerged index
func TestFlowRefusesInProgressOperations(t *testing.T) {
	t.Run("merge in linked worktree", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "base.txt"), []byte("base\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-qm", "base")
		testGit(t, ws.root, nil, "checkout", "-qb", "side")
		writeFileMust(t, filepath.Join(ws.root, "side.txt"), []byte("side\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-qm", "side")
		testGit(t, ws.root, nil, "checkout", "-q", "main")
		writeFileMust(t, filepath.Join(ws.root, "main.txt"), []byte("main\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-qm", "main")

		linked := filepath.Join(t.TempDir(), "linked")
		testGit(t, ws.root, nil, "worktree", "add", "-q", "-b", "topic", linked, "main")
		testGit(t, linked, nil, "merge", "--no-commit", "--no-ff", "side")
		file := filepath.Join(linked, "agent.txt")
		writeFileMust(t, file, []byte("agent\n"))
		ws.journal.RecordChange(ChangeInput{Agent: "Main", Tool: "write", Path: file, Before: nil, After: new("agent\n")})
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: linked, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = CommitSelection(draft, CommitRequest{Subject: "fix: agent"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err == nil || !regexp.MustCompile(`merge is in progress; finish or abort.*git commit`).MatchString(err.Error()) {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	opCases := []struct {
		op   string
		args []string
	}{
		{"cherry-pick", []string{"cherry-pick", "--no-edit", "side"}},
		{"revert", []string{"revert", "--no-edit", "side"}},
		{"rebase", []string{"rebase", "--merge", "side"}},
		{"rebase", []string{"rebase", "--apply", "side"}},
		{"git am", []string{"am", "--3way", ".git/side.patch"}},
	}
	for _, tc := range opCases {
		t.Run(tc.op+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			ws := newEmptyRepo(t)
			writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("base\n"))
			testGit(t, ws.root, nil, "add", ".")
			testGit(t, ws.root, nil, "commit", "-qm", "base")
			testGit(t, ws.root, nil, "checkout", "-qb", "side")
			writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("side\n"))
			testGit(t, ws.root, nil, "add", ".")
			testGit(t, ws.root, nil, "commit", "-qm", "side")
			if tc.op == "git am" {
				patch := testGit(t, ws.root, nil, "format-patch", "--stdout", "-1")
				writeFileMust(t, filepath.Join(ws.root, ".git", "side.patch"), []byte(patch))
			}
			testGit(t, ws.root, nil, "checkout", "-q", "main")
			writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("main\n"))
			testGit(t, ws.root, nil, "add", ".")
			testGit(t, ws.root, nil, "commit", "-qm", "main")
			if _, err := testGitErr(ws.root, nil, tc.args...); err == nil {
				t.Fatalf("expected %s conflict", tc.op)
			}
			ws.write(t, "Main", "", "agent.txt", "agent\n")
			ws.journal.Flush()
			draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = CommitSelection(draft, CommitRequest{Subject: "fix: agent"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
			wantRE := regexp.MustCompile(tc.op + ` is in progress; finish or abort.*git commit`)
			if err == nil || !wantRE.MatchString(err.Error()) {
				t.Fatalf("got err=%v, want match %s", err, wantRE)
			}
		})
	}

	t.Run("unmerged index from stash pop conflict", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("base\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-qm", "base")
		writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("stashed\n"))
		testGit(t, ws.root, nil, "stash", "push", "-m", "stashed")
		writeFileMust(t, filepath.Join(ws.root, "conflict.txt"), []byte("main\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-qm", "main")
		ws.write(t, "Main", "", "agent.txt", "agent\n")
		ws.journal.Flush()
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testGitErr(ws.root, nil, "stash", "pop"); err == nil {
			t.Fatalf("expected stash pop conflict")
		}
		_, err = CommitSelection(draft, CommitRequest{Subject: "fix: agent"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err == nil || !strings.Contains(err.Error(), "index has unresolved conflicts; resolve them before using commit") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// upstream: commit-selective-flow.test.ts: empty repo vs skipped-only vs empty selection
func TestFlowEmptyRepoVsEmptySelection(t *testing.T) {
	ws := newEmptyRepo(t)
	writeFileMust(t, filepath.Join(ws.root, "a.ts"), []byte("export const a = 1;\n"))
	testGit(t, ws.root, nil, "add", ".")
	testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
	cctx := CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}

	clean, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(clean, CommitRequest{Subject: "fix: a"}, cctx); err == nil || !strings.Contains(err.Error(), "has no changes against HEAD.") {
		t.Fatalf("unexpected err: %v", err)
	}
	if home, herr := os.UserHomeDir(); herr == nil && home != "" {
		clean.Root = filepath.Join(home, "workspace")
		_, err := CommitSelection(clean, CommitRequest{Subject: "fix: a"}, cctx)
		if err == nil || err.Error() != "Nothing to commit: ~/workspace has no changes against HEAD." {
			t.Fatalf("home shorten err = %v", err)
		}
	}

	nested := filepath.Join(ws.root, "vendor")
	_ = os.MkdirAll(nested, 0o755)
	testGit(t, nested, nil, "init", "-q")
	skippedOnly, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(skippedOnly.Skipped, []string{"vendor"}) {
		t.Fatalf("skipped = %v", skippedOnly.Skipped)
	}
	if _, err := CommitSelection(skippedOnly, CommitRequest{Subject: "fix: a"}, cctx); err == nil ||
		err.Error() != "Nothing to commit: only vendor changed (submodule or nested repository), which selective commits skip." {
		t.Fatalf("skipped err = %v", err)
	}
	_ = os.RemoveAll(nested)

	writeFileMust(t, filepath.Join(ws.root, "a.ts"), []byte("export const a = 2;\n"))
	unselected, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitSelection(unselected, CommitRequest{Subject: "fix: a"}, cctx); err == nil || err.Error() != "Nothing selected; use take() first." {
		t.Fatalf("unselected err = %v", err)
	}
}

// upstream: commit-selective-flow.test.ts: unknown unit id, quoted filename, whole-file frozen blobs, clean filter, autocrlf, no journal, amend
func TestFlowUnitsQuotedWholeFilesHooksAndAmend(t *testing.T) {
	t.Run("unknown hunk unit id leaves selection intact", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "a.ts"), []byte("export const a = 1;\n"))
		writeFileMust(t, filepath.Join(ws.root, "b.ts"), []byte("export const b = 1;\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
		ws.write(t, "Main", "", "a.ts", "export const a = 2;\nexport const extra = 3;\n")
		ws.write(t, "Other", "", "a.ts", "export const a = 2;\nexport const extra = 3;\nexport const foreign = 4;\n")
		ws.write(t, "Main", "", "b.ts", "export const b = 2;\n")
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		beforeDiff := draft.Diff()
		if _, err := draft.Take(SelectionTarget{Path: new("a.ts"), Units: []string{"1z"}}); err == nil || !strings.Contains(err.Error(), `a.ts has no unit "1z"`) {
			t.Fatalf("expected 1z error, got %v", err)
		}
		if _, err := draft.Take(SelectionTarget{Path: new("*.ts"), Units: []string{"1b"}}); err == nil || !strings.Contains(err.Error(), `has no unit "1b"`) {
			t.Fatalf("expected 1b error on b.ts, got %v", err)
		}
		if draft.Diff() != beforeDiff {
			t.Fatalf("selection mutated after error")
		}
		msg, err := draft.Take(SelectionTarget{Path: new("a.ts"), Units: []string{"1b"}})
		if err != nil || msg != "Selected 1 change line(s)." {
			t.Fatalf("take 1b = (%q, %v)", msg, err)
		}
		msg, err = draft.Drop(SelectionTarget{Path: new("a.ts"), Units: []string{"1a"}})
		if err != nil || msg != "Deselected 3 change line(s)." {
			t.Fatalf("drop 1a = (%q, %v)", msg, err)
		}
	})

	t.Run("quoted diff filename", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("quotes in filename unsupported on Windows")
		}
		ws := newEmptyRepo(t)
		filename := `has"quote.txt`
		file := filepath.Join(ws.root, filename)
		writeFileMust(t, file, []byte("before\n"))
		testGit(t, ws.root, nil, "add", "--", filename)
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")
		ws.write(t, "Main", "", filename, "after\n")
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := CommitSelection(draft, CommitRequest{Subject: "fix: quoted filename"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(outcome.Paths, []string{filename}) {
			t.Fatalf("paths = %v", outcome.Paths)
		}
		if got := testGit(t, ws.root, nil, "show", "HEAD:"+filename); got != "after\n" {
			t.Fatalf("HEAD:%s = %q", filename, got)
		}
	})

	t.Run("frozen whole-file blobs ignore later worktree edits", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), []byte{0, 1, 2})
		writeFileMust(t, filepath.Join(ws.root, "gone.bin"), []byte{0, 9})
		writeFileMust(t, filepath.Join(ws.root, "big.txt"), []byte(strings.Repeat("a", 4*1024*1024)+"\n"))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")

		draftedLogo := []byte{0, 3, 3, 3}
		draftedBig := strings.Repeat("b", 4*1024*1024) + "\n"
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), draftedLogo)
		writeFileMust(t, filepath.Join(ws.root, "big.txt"), []byte(draftedBig))
		_ = os.Remove(filepath.Join(ws.root, "gone.bin"))
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := draft.Take(SelectionTarget{Path: new("**")}); err != nil {
			t.Fatal(err)
		}
		// Mutate worktree after draft captured blobs.
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), []byte{0, 7, 7, 7, 7, 7})
		writeFileMust(t, filepath.Join(ws.root, "big.txt"), []byte(strings.Repeat("c", 4*1024*1024)+"\n"))
		writeFileMust(t, filepath.Join(ws.root, "gone.bin"), []byte{0, 5, 5})

		outcome, err := CommitSelection(draft, CommitRequest{Subject: "chore: assets"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(outcome.Paths, []string{"big.txt", "gone.bin", "logo.bin"}) {
			t.Fatalf("paths = %v", outcome.Paths)
		}
		if !bytes.Equal(testGitBlob(t, ws.root, "HEAD:logo.bin"), draftedLogo) {
			t.Fatalf("HEAD:logo.bin mismatch")
		}
		if string(testGitBlob(t, ws.root, "HEAD:big.txt")) != draftedBig {
			t.Fatalf("HEAD:big.txt mismatch")
		}
	})

	t.Run("clean-filtered whole files use drafted bytes", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX clean filter script")
		}
		ws := newEmptyRepo(t)
		script := filepath.Join(t.TempDir(), "clean.sh")
		writeFileMust(t, script, []byte("#!/bin/sh\nprintf 'pointer:'\ncat\n"))
		testGit(t, ws.root, nil, "config", "filter.asset.clean", "sh "+script)
		testGit(t, ws.root, nil, "config", "filter.asset.required", "true")
		writeFileMust(t, filepath.Join(ws.root, ".gitattributes"), []byte("*.bin filter=asset\nwide.dat text eol=lf\n"))
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), []byte{0, 1})
		writeFileMust(t, filepath.Join(ws.root, "wide.dat"), append([]byte("a\r\nb\r\n"), 0xff))
		testGit(t, ws.root, nil, "add", ".")
		testGit(t, ws.root, nil, "commit", "-q", "-m", "seed")

		draftedLogo := []byte{0, 2, 2, 2}
		draftedWide := append([]byte("c\r\nd\r\n"), 0xff)
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), draftedLogo)
		writeFileMust(t, filepath.Join(ws.root, "wide.dat"), draftedWide)
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = draft.Take(SelectionTarget{Path: new("logo.bin")})
		_, _ = draft.Take(SelectionTarget{Path: new("wide.dat")})
		writeFileMust(t, filepath.Join(ws.root, "logo.bin"), []byte{0, 9, 9, 9, 9})
		writeFileMust(t, filepath.Join(ws.root, "wide.dat"), append([]byte("z\r\nz\r\n"), 0xff))

		outcome, err := CommitSelection(draft, CommitRequest{Subject: "chore: assets"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(outcome.Paths, []string{"logo.bin", "wide.dat"}) {
			t.Fatalf("paths = %v", outcome.Paths)
		}
		if !bytes.Equal(testGitBlob(t, ws.root, "HEAD:logo.bin"), append([]byte("pointer:"), draftedLogo...)) {
			t.Fatalf("HEAD:logo.bin mismatch")
		}
		if !bytes.Equal(testGitBlob(t, ws.root, "HEAD:wide.dat"), append([]byte("c\nd\n"), 0xff)) {
			t.Fatalf("HEAD:wide.dat mismatch")
		}
	})

	t.Run("autocrlf worktree to git conversion", func(t *testing.T) {
		ws := newEmptyRepo(t)
		testGit(t, ws.root, nil, "config", "core.autocrlf", "true")
		writeFileMust(t, filepath.Join(ws.root, "notes.txt"), []byte("one\r\ntwo\r\nthree\r\n"))
		testGit(t, ws.root, nil, "add", "notes.txt")
		testGit(t, ws.root, nil, "commit", "-qm", "seed")
		ws.write(t, "Main", "", "notes.txt", "one\r\n2\r\nthree\r\n")
		ws.journal.Flush()
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(draft.Summary(), "✓ notes.txt  +1 −1") {
			t.Fatalf("summary:\n%s", draft.Summary())
		}
		if _, err := CommitSelection(draft, CommitRequest{Subject: "fix: two"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}}); err != nil {
			t.Fatal(err)
		}
		if got := testGit(t, ws.root, nil, "show", "HEAD:notes.txt"); got != "one\n2\nthree\n" {
			t.Fatalf("HEAD:notes.txt = %q", got)
		}
	})

	t.Run("commit-msg rewritten subject, cancel during read, and amend", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell hook test")
		}
		ws := newEmptyRepo(t)
		installSubjectRewriteHook(t, ws.root)
		ws.write(t, "Main", "", "notes.txt", "notes\n")
		ws.journal.Flush()

		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		journalPath := filepath.Join(ws.journal.Dir, journalFile)
		sawRead := false
		recordedBeforeRead := false
		outcome, err := CommitSelection(draft, CommitRequest{Subject: "chore: original subject", Body: "- Original body."}, CommitContext{
			Journal: ws.journal,
			Agent:   JournalAgent{Agent: "Main"},
			Ctx:     ctx,
			testOnCommitDetails: func(sha string, readCtx context.Context) {
				sawRead = true
				recordedBeforeRead = commitIsRecorded(journalPath, sha)
				if readCtx != nil {
					t.Errorf("expected nil (uncancelled) readCtx")
				}
				cancel()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sawRead || !recordedBeforeRead {
			t.Fatalf("sawRead=%v recordedBeforeRead=%v", sawRead, recordedBeforeRead)
		}
		if outcome.Subject != "fix(notes): kept the rewritten subject" {
			t.Fatalf("subject = %q", outcome.Subject)
		}

		// Follow-up amend replaces root commit.
		ws.write(t, "Main", "", "notes.txt", "notes\nrevised\n")
		ws.journal.Flush()
		followUp, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		amended, err := CommitSelection(followUp, CommitRequest{Subject: "fix(notes): amended", Amend: true, NoVerify: true}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if amended.Amended != outcome.SHA || amended.Parent != nil || !reflect.DeepEqual(amended.Paths, []string{"notes.txt"}) {
			t.Fatalf("amended = %+v", amended)
		}
		if got := strings.TrimSpace(testGit(t, ws.root, nil, "rev-list", "--count", "HEAD")); got != "1" {
			t.Fatalf("rev-list count = %q", got)
		}
	})

	t.Run("no journal warning and review of binary edit", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "a.txt"), []byte("a\n"))
		draft, err := CreateDraft(DraftSource{Cwd: ws.root, Journal: nil, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(draft.Summary(), "No change journal: this session keeps no artifacts") ||
			!strings.Contains(draft.Summary(), "Unrecorded: 1 path(s)") {
			t.Fatalf("summary:\n%s", draft.Summary())
		}

		ws2 := newEmptyRepo(t)
		ws2.write(t, "Main", "", "blob.bin", "binary\x00content\n")
		ws2.journal.Flush()
		d2, err := CreateDraft(DraftSource{Cwd: ws2.root, Journal: ws2.journal, Caller: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(d2.Summary(), "? blob.bin  whole binary, non-UTF-8, or large file; edited by Main") {
			t.Fatalf("summary:\n%s", d2.Summary())
		}
		shown, err := d2.Show(nil)
		if err != nil || shown != "blob.bin  whole-file change (binary, non-UTF-8, or large file); edited by Main\n  1 ? whole file" {
			t.Fatalf("show = (%q, %v)", shown, err)
		}
	})

	t.Run("amend against non-root commit reports parent and all paths", func(t *testing.T) {
		ws := newEmptyRepo(t)
		writeFileMust(t, filepath.Join(ws.root, "seed.txt"), []byte("seed\n"))
		testGit(t, ws.root, nil, "add", "seed.txt")
		testGit(t, ws.root, nil, "commit", "-qm", "seed")
		seed := strings.TrimSpace(testGit(t, ws.root, nil, "rev-parse", "HEAD"))
		ws.write(t, "Main", "", "a.txt", "a\n")
		ws.write(t, "Main", "", "b.txt", "b\n")
		ws.journal.Flush()
		d1, _ := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		first, err := CommitSelection(d1, CommitRequest{Subject: "feat: a and b"}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		ws.write(t, "Main", "", "a.txt", "a\nmore\n")
		ws.journal.Flush()
		d2, _ := CreateDraft(DraftSource{Cwd: ws.root, Journal: ws.journal, Caller: "Main"})
		amended, err := CommitSelection(d2, CommitRequest{Subject: "feat: a and b", Amend: true}, CommitContext{Journal: ws.journal, Agent: JournalAgent{Agent: "Main"}})
		if err != nil {
			t.Fatal(err)
		}
		if amended.Amended != first.SHA || amended.Parent == nil || *amended.Parent != seed || !reflect.DeepEqual(amended.Paths, []string{"a.txt", "b.txt"}) {
			t.Fatalf("amended = %+v", amended)
		}
	})
}
