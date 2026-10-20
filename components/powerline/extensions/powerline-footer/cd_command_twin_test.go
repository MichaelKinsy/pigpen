package powerline_footer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of port/oracle/tests/cd-command.test.ts.

// sessionLines is the content of a Pi session file (version 3) with the two messages the original's tests append.
func sessionLines(cwd string) string {
	return strings.Join([]string{
		`{"type":"session","version":3,"id":"0192f5a0-0000-7000-8000-000000000001","timestamp":"2026-10-20T10:00:00.000Z","cwd":` + marshalJSON(cwd, "") + `}`,
		`{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"user","content":"hello","timestamp":1792490401000}}`,
		`{"type":"message","id":"b2c3d4e5","parentId":"a1b2c3d4","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"assistant","content":"hi","timestamp":1792490402000,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}`,
	}, "\n") + "\n"
}

// writeSession writes a persisted session of cwd into dir and returns its path (SessionManager.create + two appendMessage calls).
func writeSession(t *testing.T, dir, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-20T10-00-00-000Z_0192f5a0-0000-7000-8000-000000000001.jsonl")
	if err := os.WriteFile(path, []byte(sessionLines(cwd)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type cdNotification struct{ Message, Level string }

// fakeCdHost is the ctx the original's tests build by hand: cwd, getSessionFile, notify, waitForIdle and switchSession.
type fakeCdHost struct {
	cwd, sessionFile, sessionsRoot, home string
	waitErr                              error
	notifications                        []cdNotification
	switchedPath                         string
	switchSession                        func(path string, withSession func(next cdNextContext) error) (bool, error)
}

func (f *fakeCdHost) Cwd() string                  { return f.cwd }
func (f *fakeCdHost) Home() string                 { return f.home }
func (f *fakeCdHost) Now() time.Time               { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
func (f *fakeCdHost) SessionFile() (string, error) { return f.sessionFile, nil }
func (f *fakeCdHost) SessionsRoot() string         { return f.sessionsRoot }
func (f *fakeCdHost) WaitForIdle() error           { return f.waitErr }
func (f *fakeCdHost) Notify(message, level string) {
	f.notifications = append(f.notifications, cdNotification{message, level})
}
func (f *fakeCdHost) SwitchSession(path string, withSession func(next cdNextContext) error) (bool, error) {
	f.switchedPath = path
	return f.switchSession(path, withSession)
}

type fakeNext struct {
	cwd           string
	notifications []cdNotification
	titles        []string
}

func (n *fakeNext) Cwd() string { return n.cwd }
func (n *fakeNext) Notify(message, level string) {
	n.notifications = append(n.notifications, cdNotification{message, level})
}
func (n *fakeNext) SetTitle(title string) { n.titles = append(n.titles, title) }

func TestCdCommandTwins(t *testing.T) {
	tw(t, "cd-command", "resolveCdTarget supports current, relative, home, and invalid directories", func(t *testing.T) {
		root, home := t.TempDir(), t.TempDir()
		child, homeProject := filepath.Join(root, "child"), filepath.Join(home, "project")
		if err := os.Mkdir(child, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(homeProject, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := resolveCdTarget("", root, home); got != nil {
			t.Errorf("empty argument = %+v, want nil", got)
		}
		if got := resolveCdTarget("child", root, home); got == nil || *got != (cdTarget{Path: child}) {
			t.Errorf("child = %+v, want %q", got, child)
		}
		if got := resolveCdTarget("~/project", root, home); got == nil || *got != (cdTarget{Path: homeProject}) {
			t.Errorf("~/project = %+v, want %q", got, homeProject)
		}
		want := cdTarget{Error: "Directory does not exist: " + filepath.Join(root, "My Folder")}
		if got := resolveCdTarget("My\\ Folder", root, home); got == nil || *got != want {
			t.Errorf("My\\ Folder = %+v, want %+v", got, want)
		}
		if got := resolveCdTarget("missing", root, home); got == nil || !strings.Contains(got.Error, "Directory does not exist:") {
			t.Errorf("missing = %+v", got)
		}
		if got := resolveCdTarget("file.txt", root, home); got == nil || !strings.Contains(got.Error, "Not a directory:") {
			t.Errorf("file.txt = %+v", got)
		}
	})

	tw(t, "cd-command", "cd argument completions suggest directories and quick picks", func(t *testing.T) {
		root, home := t.TempDir(), t.TempDir()
		for _, dir := range []string{filepath.Join(root, "child"), filepath.Join(root, "My Folder"), filepath.Join(home, "project")} {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}

		rootCompletions := getCdArgumentCompletions("", root, home)
		has := func(value, label string) bool {
			for _, item := range rootCompletions {
				if item.Value == value && item.Label == label {
					return true
				}
			}
			return false
		}
		if !has("../", "../") {
			t.Error("no ../ quick pick")
		}
		if !has("~/", "~/") {
			t.Error("no ~/ quick pick")
		}
		if !has("child/", "child/") {
			t.Error("no child/")
		}
		if !has("My\\ Folder/", "My Folder/") {
			t.Error("no escaped My Folder/")
		}
		for _, item := range rootCompletions {
			if item.Label == "file.txt" {
				t.Error("a file is suggested")
			}
		}

		var values []string
		for _, item := range getCdArgumentCompletions("~/p", root, home) {
			values = append(values, item.Value)
		}
		if len(values) != 1 || values[0] != "~/project/" {
			t.Errorf("~/p = %q, want [~/project/]", values)
		}
	})

	tw(t, "cd-command", "cd command cleans up the forked session when switching is cancelled", func(t *testing.T) {
		originalCwd, _ := os.Getwd()
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		host := &fakeCdHost{
			cwd: root, sessionsRoot: filepath.Join(root, "agent", "sessions"),
			sessionFile: writeSession(t, filepath.Join(root, "sessions"), root),
			switchSession: func(string, func(cdNextContext) error) (bool, error) {
				return true, nil
			},
		}

		if err := runCdCommand("target", host); err != nil {
			t.Fatal(err)
		}

		if wd, _ := os.Getwd(); wd != originalCwd {
			t.Errorf("process cwd changed to %q", wd)
		}
		if host.switchedPath == "" {
			t.Fatal("switchSession should receive the forked session path")
		}
		if _, err := os.Stat(host.switchedPath); !os.IsNotExist(err) {
			t.Errorf("the forked session %s still exists", host.switchedPath)
		}
		if last := host.notifications[len(host.notifications)-1]; last != (cdNotification{"Directory change cancelled", "warning"}) {
			t.Errorf("last notification = %+v", last)
		}
	})

	tskip(t, "cd-command", "cd command preserves the target session after a post-apply switch error",
		"the original asserts process.chdir took effect (realpathSync(process.cwd()) equals the target); a Go extension has no host process cwd to change (SDK capability: host cwd change), pinned by TestCdDoesNotChangeTheProcessCwd; the rest of the case runs as TestCdKeepsTheTargetSessionAfterAPostApplyError")

	tskip(t, "cd-command", "cd command uses session replacement instead of private cwd mutation",
		"the case reads the TypeScript sources of cd-command.ts and index.ts with regular expressions (including /process\\.chdir/, which has no Go counterpart); the Go equivalents are TestCdUsesSessionReplacementNotProcessCwd and the registration test in cdquote_ext_test.go")
}

func TestCdKeepsTheTargetSessionAfterAPostApplyError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	next := &fakeNext{cwd: target}
	host := &fakeCdHost{
		cwd: root, sessionsRoot: filepath.Join(root, "agent", "sessions"),
		sessionFile: writeSession(t, filepath.Join(root, "sessions"), root),
		switchSession: func(_ string, withSession func(cdNextContext) error) (bool, error) {
			if err := withSession(next); err != nil {
				return false, err
			}
			return false, errors.New("post-apply failure")
		},
	}

	if err := runCdCommand("target", host); err != nil {
		t.Fatal(err)
	}

	if host.switchedPath == "" {
		t.Fatal("switchSession should receive the forked session path")
	}
	if _, err := os.Stat(host.switchedPath); err != nil {
		t.Errorf("the forked session must survive a post-apply error: %v", err)
	}
	if last := host.notifications[len(host.notifications)-1]; last != (cdNotification{"Failed to change directory: post-apply failure", "error"}) {
		t.Errorf("last notification = %+v", last)
	}
	if len(next.notifications) != 1 || next.notifications[0] != (cdNotification{"Changed directory to " + target, "info"}) {
		t.Errorf("withSession notifications = %+v", next.notifications)
	}
	if len(next.titles) != 1 || next.titles[0] != "pi - target" {
		t.Errorf("titles = %q", next.titles)
	}
}

// The difference from the original, pinned: it calls process.chdir(nextCtx.cwd) inside withSession; the port never changes the
// extension process's cwd (os.Chdir is a fuse hazard and the host keeps its cwd per session).
func TestCdDoesNotChangeTheProcessCwd(t *testing.T) {
	originalCwd, _ := os.Getwd()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	host := &fakeCdHost{
		cwd: root, sessionsRoot: filepath.Join(root, "agent", "sessions"),
		sessionFile: writeSession(t, filepath.Join(root, "sessions"), root),
		switchSession: func(_ string, withSession func(cdNextContext) error) (bool, error) {
			return false, withSession(&fakeNext{cwd: target})
		},
	}
	if err := runCdCommand("target", host); err != nil {
		t.Fatal(err)
	}
	if wd, _ := os.Getwd(); wd != originalCwd {
		t.Errorf("process cwd = %q, want it unchanged (%q): the difference to process.chdir is documented in cdquote_ext.go", wd, originalCwd)
	}
}

func TestCdUsesSessionReplacementNotProcessCwd(t *testing.T) {
	src, err := os.ReadFile("cd_command.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{"forkSessionFrom(sessionFile, resolved.Path", "ctx.SwitchSession(nextSessionFile"} {
		if !strings.Contains(text, want) {
			t.Errorf("cd_command.go lacks %q", want)
		}
	}
	for _, banned := range []string{"os.Chdir", "ctx.cwd ="} {
		if strings.Contains(text, banned) {
			t.Errorf("cd_command.go contains %q", banned)
		}
	}
}
