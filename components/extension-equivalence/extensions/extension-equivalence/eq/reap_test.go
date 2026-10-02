//go:build linux

package eq

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A mutant that removes a port's TERM-to-KILL escalation leaves the port's worker process alive:
// the worker ignores TERM by design and sits in a process group and session of its own, so neither
// `go test` exiting nor a group kill reaches it. Every mutation run of such a port leaked it. The
// unit runner must kill everything the run started, wherever it went.
func TestUnitRunKillsWorkersThatLeftTheProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("needs setsid to start a worker in a session of its own")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "worker.pid")
	// The fake go command plays a killed mutant: it starts a TERM-ignoring worker in a new
	// session, reports a failing test and exits, leaving the worker behind.
	fakeGo(t, "setsid sh -c 'trap \"\" TERM; echo $$ > "+pidFile+"; exec sleep 600' >/dev/null 2>&1 &\n"+
		"i=0; while [ ! -s "+pidFile+" ] && [ $i -lt 100 ]; do i=$((i+1)); sleep 0.05; done\n"+
		"echo '--- FAIL: TestWorkerEscalatesToKill (0.01s)'; exit 1")
	u := UnitTest{SDKDir: t.TempDir(), Args: []string{"test", "./..."}}
	if failed, _, err := u.run(dir, nil); !failed || err != nil {
		t.Fatalf("run: failed=%v err=%v, want a killed mutant", failed, err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the worker never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("worker %d is still running after the unit run", pid)
}

// The marker is per run: a run must not kill a sibling run's processes (mutants run side by side).
func TestReapKillsOnlyTheMarkedRun(t *testing.T) {
	start := func(marker string) *exec.Cmd {
		c := exec.Command("sleep", "600")
		c.Env = append(os.Environ(), runMarkerVar+"="+marker)
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
		return c
	}
	mine, other := start("run-a"), start("run-b")
	killRun("run-a")
	done := make(chan error, 1)
	go func() { done <- mine.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the marked process survived")
	}
	if syscall.Kill(other.Process.Pid, 0) != nil {
		t.Error("a process of another run was killed")
	}
}
