//go:build unix

package pi_goal_x

// Mutation-survivor tests, lane D: the audit child's process handling (process groups, SIGTERM, the wait delay, the group sweep).

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mdDead reports whether pid is gone (a zombie counts: it ran no more code).
func mdDead(pid int) bool {
	for range 200 {
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			s := string(b)
			if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) && s[i+2] == 'Z' {
				return true
			}
		} else if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return false
}

func mdKillAtEnd(t *testing.T, pidfile string) {
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}

// mdScriptPig is a pig stand-in written as a shell script; MD_* variables come from the factory's environment.
func mdScriptPig(t *testing.T, body string, env ...string) (*pigAuditorFactory, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "pig.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &pigAuditorFactory{
		bin:     func() string { return script },
		environ: func() []string { return append(append([]string{}, os.Environ()...), env...) },
		grace:   time.Minute,
	}, dir
}

func mdReadPid(t *testing.T, file string) int {
	t.Helper()
	return waitPid(t, file)
}

func TestMutantsDChildProcessGroup(t *testing.T) {
	unlock := lockAuditThread()
	defer unlock()

	// The child leads its own process group.
	cmd := exec.Command("sleep", "30")
	configureAuditChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid != cmd.Process.Pid {
		t.Errorf("child pgid %d (err %v), want its own pid %d", pgid, err, cmd.Process.Pid)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// Killing the group takes the grandchildren with it.
	pidfile := filepath.Join(t.TempDir(), "grandchild")
	mdKillAtEnd(t, pidfile)
	g := exec.Command("sh", "-c", `sleep 30 & echo $! > "$0"; wait`, pidfile)
	configureAuditChild(g)
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	grand := mdReadPid(t, pidfile)
	killAuditChildGroup(g.Process)
	_ = g.Wait()
	if !mdDead(grand) {
		t.Error("the grandchild survived the group kill")
	}

	// A child that is not a group leader is killed itself.
	lone := exec.Command("sleep", "30")
	if err := lone.Start(); err != nil {
		t.Fatal(err)
	}
	killAuditChildGroup(lone.Process)
	done := make(chan error, 1)
	go func() { done <- lone.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = lone.Process.Kill()
		t.Error("a child outside its own group was not killed")
	}
}

func TestMutantsDAbortSweepsTheGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	mdKillAtEnd(t, pidfile)
	// The shell ends on SIGTERM but its background child keeps the output pipes open; only the group sweep ends it.
	f, _ := mdScriptPig(t, `sleep 30 &
echo $! > "$MD_PID"
wait`, "MD_PID="+pidfile)
	s, _ := f.createSession(auditorSessionOptions{})
	errc := make(chan error, 1)
	go func() { errc <- s.prompt("x") }()
	grand := mdReadPid(t, pidfile)
	s.abort()
	select {
	case err := <-errc:
		eq(t, err, error(nil), "an aborted prompt returns nil")
	case <-time.After(15 * time.Second):
		t.Fatal("the aborted prompt did not return")
	}
	if !mdDead(grand) {
		t.Error("what the stopped pig left behind was not swept")
	}
}

func TestMutantsDWaitDelay(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	mdKillAtEnd(t, pidfile)
	// pig ends at once, a grandchild keeps the output pipes open: the prompt returns after the wait delay, without an error.
	f, _ := mdScriptPig(t, `sleep 12 &
echo $! > "$MD_PID"
exit 0`, "MD_PID="+pidfile)
	s, _ := f.createSession(auditorSessionOptions{})
	start := time.Now()
	err := s.prompt("x")
	eq(t, err, error(nil), "a lingering pipe holder is not an error")
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("the prompt waited %v for the pipe holder", d)
	}
}

func TestMutantsDChildRunsInItsDirectory(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "cwd")
	f, _ := mdScriptPig(t, `pwd -P > "$MD_OUT"`, "MD_OUT="+out)
	s, _ := f.createSession(auditorSessionOptions{cwd: dir})
	if err := s.prompt("x"); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	b, _ := os.ReadFile(out)
	eq(t, strings.TrimSpace(string(b)), want, "child working directory")
}

func TestMutantsDAbortTermsBeforeTheGrace(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	p := newFakePig(t, auditStream(evAgent), "GOALAUDIT_SLEEP=60", "GOALAUDIT_PIDFILE="+pidfile)
	p.factory.grace = 30 * time.Second
	s, _ := p.factory.createSession(auditorSessionOptions{})
	errc := make(chan error, 1)
	go func() { errc <- s.prompt("x") }()
	pid := waitPid(t, pidfile)
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	s.abort()
	select {
	case <-errc:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("SIGTERM was not sent: the child lived until the grace period")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("slow stop")
	}
}

func TestMutantsDDisposeWaitsForTheChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	p := newFakePig(t, "", "GOALAUDIT_SLEEP=60", "GOALAUDIT_PIDFILE="+pidfile)
	s, _ := p.factory.createSession(auditorSessionOptions{})
	errc := make(chan error, 1)
	go func() { errc <- s.prompt("x") }()
	waitPid(t, pidfile)
	s.dispose()
	select {
	case <-s.(*pigAuditorSession).exited:
	default:
		t.Error("dispose returned before the child was waited for")
	}
	<-errc
}

func TestMutantsDDisposeUnstartedSession(t *testing.T) {
	p := newFakePig(t, approvedScript())
	s, _ := p.factory.createSession(auditorSessionOptions{})
	s.dispose() // no child: nothing to stop or wait for
}

func TestMutantsDUnsubscribeKeepsTheOthers(t *testing.T) {
	p := newFakePig(t, approvedScript())
	s, _ := p.factory.createSession(auditorSessionOptions{})
	var a, b, c []string
	offA := s.subscribe(func(e auditorSessionEvent) { a = append(a, e.typ) })
	s.subscribe(func(e auditorSessionEvent) { b = append(b, e.typ) })
	s.subscribe(func(e auditorSessionEvent) { c = append(c, e.typ) })
	offA()
	s.(*pigAuditorSession).emit(auditorSessionEvent{typ: "agent_start"})
	if len(a) != 0 || !reflect.DeepEqual(b, []string{"agent_start"}) || !reflect.DeepEqual(c, []string{"agent_start"}) {
		t.Errorf("listeners after unsubscribing the first: a=%v b=%v c=%v", a, b, c)
	}
}

// A pig that could not start leaves no child to wait for: the exit channel is closed at once.
func TestMutantsDFailedStartClosesExited(t *testing.T) {
	f := &pigAuditorFactory{bin: func() string { return filepath.Join(t.TempDir(), "no-such-pig") }}
	s, _ := f.createSession(auditorSessionOptions{})
	if err := s.prompt("x"); err == nil {
		t.Fatal("no error for a missing pig")
	}
	select {
	case <-s.(*pigAuditorSession).exited:
	default:
		t.Error("exited is still open after a failed start")
	}
}
