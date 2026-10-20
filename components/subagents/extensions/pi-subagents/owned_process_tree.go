package pi_subagents

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// owned-process-tree.ts: one writer's process group, terminated exactly once (SIGTERM to the group, a grace, SIGKILL, then
// `ps` proves the group is gone and that no detached descendant of it is still alive).

const (
	defaultTermGrace    = 3000 * time.Millisecond
	defaultKillVerify   = 1000 * time.Millisecond
	ownedTreeVerifyStep = 25 * time.Millisecond
)

type ownedProcessTreeOptions struct {
	TermGrace  time.Duration // 0: 3 s
	KillVerify time.Duration // 0: 1 s
}

type ownedProcessTreeController struct {
	pid   int
	opts  ownedProcessTreeOptions
	posix bool
	once  sync.Once
	proof *ProcessTreeProof
}

func newOwnedProcessTreeController(pid int, opts ownedProcessTreeOptions) *ownedProcessTreeController {
	return &ownedProcessTreeController{pid: pid, opts: opts, posix: backgroundSupported}
}

// Terminate stops the whole group once and returns the proof; later calls return the same proof.
func (c *ownedProcessTreeController) Terminate() *ProcessTreeProof {
	c.once.Do(func() { c.proof = c.terminate() })
	return c.proof
}

// FinishAfterWriterClose is Terminate (the original names both the same).
func (c *ownedProcessTreeController) FinishAfterWriterClose() *ProcessTreeProof { return c.Terminate() }

// signalOutcome is "sent", "absent", or the diagnostic of a failed signal.
func ownedSignal(target, signal int) (outcome, diag string) {
	err := defaultKill(target, signal)
	switch {
	case err == nil:
		return "sent", ""
	case err == errNoSuchProcess:
		return "absent", ""
	}
	return "", err.Error()
}

var (
	psGroupRow    = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\S+)`)
	psParentedRow = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\d+)\s+(\S+)`)
)

// ownedGroupMembers lists the live (non-zombie) members of the process group, or a diagnostic.
func ownedGroupMembers(pgid int) ([]int, string) {
	out, err := exec.Command("ps", "-axo", "pid=,pgid=,stat=").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if d := strings.TrimSpace(string(ee.Stderr)); d != "" {
				return nil, d
			}
			return nil, fmt.Sprintf("ps exited with %d", ee.ExitCode())
		}
		return nil, err.Error()
	}
	members := []int{}
	for _, line := range strings.Split(string(out), "\n") {
		m := psGroupRow.FindStringSubmatch(line)
		if m == nil || m[2] != strconv.Itoa(pgid) || strings.HasPrefix(m[3], "Z") {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		members = append(members, pid)
	}
	return members, ""
}

// ownedDetachedDescendants lists the live processes outside the group whose parent is in it.
func ownedDetachedDescendants(pgid int) []int {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,stat=").Output()
	if err != nil {
		return nil
	}
	owned := map[int]bool{}
	type row struct{ pid, ppid int }
	var outside []row
	for _, line := range strings.Split(string(out), "\n") {
		m := psParentedRow.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[4], "Z") {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		ppid, _ := strconv.Atoi(m[2])
		g, _ := strconv.Atoi(m[3])
		if g == pgid {
			owned[pid] = true
		} else {
			outside = append(outside, row{pid, ppid})
		}
	}
	var res []int
	for _, r := range outside {
		if owned[r.ppid] {
			res = append(res, r.pid)
		}
	}
	return res
}

// ownedWaitGroupTerminal polls until the group is empty. It returns nil when it is, else the state and diagnostic.
func ownedWaitGroupTerminal(pgid int, timeout time.Duration) (state, diag string, gone bool) {
	deadline := time.Now().Add(timeout)
	for {
		members, d := ownedGroupMembers(pgid)
		if d == "" && len(members) == 0 {
			return "", "", true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if d != "" {
				return "enumeration-failed", d, false
			}
			ids := make([]string, len(members))
			for i, p := range members {
				ids[i] = strconv.Itoa(p)
			}
			return "still-active", fmt.Sprintf("Process group %d still has active members: %s.", pgid, strings.Join(ids, ", ")), false
		}
		time.Sleep(min(ownedTreeVerifyStep, remaining))
	}
}

func ownedObserved(pgid int) *ProcessTreeProof {
	return &ProcessTreeProof{State: "observed", Mechanism: "posix-process-group", ProcessGroupID: pgid, VerifiedAt: ms(nowMs())}
}

func ownedObservedUnlessLiveDetached(pgid int, detached []int) *ProcessTreeProof {
	var live []string
	for _, pid := range detached {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		s := strings.TrimSpace(string(out))
		if err == nil && s != "" && !strings.HasPrefix(s, "Z") {
			live = append(live, strconv.Itoa(pid))
		}
	}
	if len(live) == 0 {
		return ownedObserved(pgid)
	}
	return &ProcessTreeProof{State: "unknown", Reason: "verification-failed", Diagnostic: "Owned detached descendant(s) still active: " + strings.Join(live, ", ") + "."}
}

func (c *ownedProcessTreeController) terminate() *ProcessTreeProof {
	if !c.posix {
		_, _ = ownedSignal(c.pid, signalTerm)
		return &ProcessTreeProof{State: "unknown", Reason: "unsupported-platform"}
	}
	target := -c.pid
	termGrace, killVerify := c.opts.TermGrace, c.opts.KillVerify
	if termGrace == 0 {
		termGrace = defaultTermGrace
	}
	if killVerify == 0 {
		killVerify = defaultKillVerify
	}
	detached := ownedDetachedDescendants(c.pid)
	if out, diag := ownedSignal(target, signalTerm); out == "" {
		return &ProcessTreeProof{State: "unknown", Reason: "signal-failed", Diagnostic: diag}
	}
	if _, _, gone := ownedWaitGroupTerminal(c.pid, termGrace); gone {
		return ownedObservedUnlessLiveDetached(c.pid, detached)
	}
	if out, diag := ownedSignal(target, signalKill); out == "" {
		members, d := ownedGroupMembers(c.pid)
		if d != "" || len(members) > 0 {
			return &ProcessTreeProof{State: "unknown", Reason: "signal-failed", Diagnostic: diag}
		}
	}
	if _, d, gone := ownedWaitGroupTerminal(c.pid, killVerify); !gone {
		return &ProcessTreeProof{State: "unknown", Reason: "verification-failed", Diagnostic: d}
	}
	return ownedObservedUnlessLiveDetached(c.pid, detached)
}
