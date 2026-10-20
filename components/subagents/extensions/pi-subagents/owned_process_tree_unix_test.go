//go:build unix

package pi_subagents

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func psActive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	s := strings.TrimSpace(string(out))
	return err == nil && s != "" && !strings.HasPrefix(s, "Z")
}

// startWriter runs script in its own process group and returns the pid it prints first.
func startWriter(t *testing.T, script string) (writer *exec.Cmd, printed string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	line, _ := bufio.NewReader(out).ReadString('\n')
	return cmd, strings.TrimSpace(line)
}

func waitReaped(cmd *exec.Cmd) { go func() { _ = cmd.Wait() }() }

func ownedTreeTermResistant(t *testing.T) {
	// a writer and a grandchild that both ignore SIGTERM
	writer, gc := startWriter(t, `trap '' TERM; (trap '' TERM; exec sleep 1000) & echo $!; wait`)
	grandchild, _ := strconv.Atoi(gc)
	waitReaped(writer)
	proof := newOwnedProcessTreeController(writer.Process.Pid, ownedProcessTreeOptions{TermGrace: 50 * time.Millisecond, KillVerify: time.Second}).Terminate()
	eq(t, proof.State, "observed", jsonOf(proof))
	if psActive(writer.Process.Pid) || psActive(grandchild) {
		t.Error("the group must be gone")
	}
	// ps fails once: the enumeration failure still waits through the TERM grace before SIGKILL
	dir := tmp(t)
	marker := filepath.Join(dir, "failed-once")
	realPs, _ := exec.LookPath("ps")
	writeFile(t, filepath.Join(dir, "ps"), "#!/bin/sh\nif [ ! -e \""+marker+"\" ]; then : > \""+marker+"\"; exit 1; fi\nexec "+realPs+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(dir, "ps"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	w2, _ := startWriter(t, `trap '' TERM; echo ready; while :; do sleep 1; done`)
	waitReaped(w2)
	grace := 50 * time.Millisecond
	start := time.Now()
	failed := newOwnedProcessTreeController(w2.Process.Pid, ownedProcessTreeOptions{TermGrace: grace, KillVerify: time.Second}).Terminate()
	if time.Since(start) < grace {
		t.Error("enumeration failure must still wait through the TERM grace")
	}
	eq(t, failed.State, "observed", jsonOf(failed))
	if psActive(w2.Process.Pid) {
		t.Error("the writer must be gone")
	}
}

func ownedTreeDetachedDescendant(t *testing.T) {
	// the writer starts a descendant in its own group (setsid) that outlives the writer's group
	writer, gc := startWriter(t, `setsid sleep 1000 & echo $!; wait`)
	grandchild, _ := strconv.Atoi(gc)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })
	waitReaped(writer)
	proof := newOwnedProcessTreeController(writer.Process.Pid, ownedProcessTreeOptions{TermGrace: 50 * time.Millisecond, KillVerify: time.Second}).Terminate()
	eq(t, proof.State, "unknown", jsonOf(proof))
	if psActive(writer.Process.Pid) {
		t.Error("the writer must be gone")
	}
	if !psActive(grandchild) {
		t.Error("the detached descendant must still be active")
	}
}
