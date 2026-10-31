package pi_subagents

// Cancellation as every platform sees it: the unix suite builds only on unix,
// so these twins keep the cancel path covered on Windows too (the mutation
// lane runs there). A child that runs long is stopped promptly with
// errCancelled; a run whose slot is taken still stops on a closed done.

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sleepCommand is a child that runs for about seconds on either platform. On
// Windows it is ping itself, not `cmd /c ping`: killing cmd.exe would leave
// the grandchild holding the pipes and cmd.Wait would block until it exits.
func sleepCommand(seconds int) (string, []string) {
	if runtime.GOOS == "windows" {
		return "ping", []string{"-n", strconv.Itoa(seconds + 1), "127.0.0.1"}
	}
	return "sleep", []string{strconv.Itoa(seconds)}
}

func TestExecChildCancellationStopsTheChildPromptly(t *testing.T) {
	isolateTemp(t)
	bin, args := sleepCommand(30)
	done := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(done)
	}()
	start := time.Now()
	_, err := execChild(done, childRequest{Bin: bin, Args: args, Cwd: t.TempDir(), Env: os.Environ()}, 10*time.Second)
	if !errors.Is(err, errCancelled) {
		t.Fatalf("err = %v, want errCancelled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %v; the child was not stopped promptly", elapsed)
	}
}

// A run whose slots are all taken must still stop when its done channel
// closes, instead of waiting for a slot that will never free.
func TestRunStopsWhenCancelledWhileWaitingForASlot(t *testing.T) {
	withTempHome(t)
	var held int
	for held < cap(childSlots) {
		childSlots <- struct{}{}
		held++
	}
	defer func() {
		for i := 0; i < held; i++ {
			<-childSlots
		}
	}()
	for _, tc := range []struct {
		name string
		run  func(done <-chan struct{}) error
	}{
		{"channelled", func(done <-chan struct{}) error {
			_, err := runAgentChannelled(done, t.TempDir(), &AgentConfig{Name: "late"}, "t", "", "")
			return err
		}},
		{"external-cli", func(done <-chan struct{}) error {
			_, err := runAgentExternalCLI(done, t.TempDir(), &AgentConfig{Name: "late", Runner: &Runner{Type: "external-cli", Command: "git"}}, "t")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			close(done)
			result := make(chan error, 1)
			go func() { result <- tc.run(done) }()
			select {
			case err := <-result:
				if err == nil || !strings.Contains(err.Error(), "was cancelled") {
					t.Fatalf("err = %v, want the cancellation message", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the run did not stop when cancelled")
			}
		})
	}
}

// A failing external CLI is reported as a child exit with its stderr as the
// detail (the external runner's copy of the exit handling). The child's usage
// must go to stderr, so an empty-detail mutant cannot pass: findstr on
// Windows writes its usage to stderr, POSIX git prints usage to stderr.
func TestExternalCLIFailureCarriesTheExitDetail(t *testing.T) {
	isolateTemp(t)
	command := "git"
	if runtime.GOOS == "windows" {
		command = "findstr" // no strings: usage on stderr, exit 2, stdout empty
	}
	agent := &AgentConfig{Name: "gitless", Runner: &Runner{Type: "external-cli", Command: command}}
	_, err := runAgentExternalCLI(nil, t.TempDir(), agent, "do the thing")
	var exit *childExit
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want a child exit", err)
	}
	if exit.code == 0 {
		t.Errorf("exit code 0: %+v", exit)
	}
	if exit.detail == "" {
		t.Errorf("no detail from stderr: %+v", exit)
	}
}
