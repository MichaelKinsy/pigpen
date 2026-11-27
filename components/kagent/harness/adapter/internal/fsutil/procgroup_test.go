package fsutil

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The helper modes run this test binary again: "tree" starts a "sleep" grandchild, prints its pid and waits.
const helperEnv = "FSUTIL_PROCGROUP_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "tree":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		os.Stdout.WriteString(strconv.Itoa(child.Process.Pid) + "\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startTree starts the helper in its own process group and returns it with the pid of the grandchild it started.
func startTree(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), helperEnv+"=tree")
	ConfigureProcessGroup(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := AttachProcessGroup(command.Process); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = KillProcessGroup(command.Process); _ = command.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read the grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("grandchild pid %q: %v", line, err)
	}
	if !alive(pid) {
		t.Fatalf("grandchild %d is not running before the kill", pid)
	}
	return command, pid
}

func TestKillProcessGroupEndsTheChildAndItsDescendants(t *testing.T) {
	command, grandchild := startTree(t)
	if err := KillProcessGroup(command.Process); err != nil {
		t.Fatalf("KillProcessGroup: %v", err)
	}
	_ = command.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for alive(grandchild) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d outlived KillProcessGroup", grandchild)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKillProcessGroupAfterTheChildExitedIsNotAnError(t *testing.T) {
	command, _ := startTree(t)
	if err := KillProcessGroup(command.Process); err != nil {
		t.Fatalf("first KillProcessGroup: %v", err)
	}
	_ = command.Wait()
	// Stop calls it twice: once on escalation and once to reap a tool that outlived pig. Unix reports a group that is gone as ESRCH.
	if err := KillProcessGroup(command.Process); err != nil && !groupGone(err) {
		t.Fatalf("second KillProcessGroup: %v", err)
	}
}
