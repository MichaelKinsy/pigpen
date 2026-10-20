package pi_subagents

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Twins of session-lease. A resumed child revives a session file; the lease keeps one writer on it. Session files are
// ordinary files for pig (`pig --session <file>`), so the lease is ported as the original has it.

type leaseFixture struct{ root, leases, sessionFile string }

func newLeaseFixture(t *testing.T) leaseFixture {
	t.Helper()
	root := tmp(t)
	f := leaseFixture{root: root, leases: filepath.Join(root, "leases"), sessionFile: filepath.Join(root, "session.jsonl")}
	writeFile(t, f.sessionFile, "")
	return f
}

func leaseRequest(sessionFile, runID string) sessionLeaseRequest {
	return sessionLeaseRequest{SessionFile: sessionFile, RunID: runID, SourceRunID: "source-run", ParentSessionID: "parent-session"}
}

func aliveAlways(int) (bool, bool) { return true, true }

func wantConflict(t *testing.T, err error, pattern string) *sessionLeaseConflictError {
	t.Helper()
	var ce *sessionLeaseConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want a session lease conflict, got %v", err)
	}
	hasRe(t, err.Error(), pattern)
	return ce
}

func TestSessionLeaseTwins(t *testing.T) {
	const f = "session-lease"
	tw(t, f, "allows one owner and reports its run metadata on contention", func(t *testing.T) {
		fx := newLeaseFixture(t)
		first, err := acquireSessionLease(leaseRequest(fx.sessionFile, "revive-a"), sessionLeaseOptions{RootDir: fx.leases, PID: 101, Hostname: "host-a",
			ProcessStartIdentity: "start-a", IsProcessAlive: aliveAlways, GetProcessStartIdentity: func(int) string { return "start-a" },
			Token: func() string { return "token-a" }, Now: func() int64 { return 1000 }})
		if err != nil {
			t.Fatal(err)
		}
		_, err = acquireSessionLease(leaseRequest(fx.sessionFile, "revive-b"), sessionLeaseOptions{RootDir: fx.leases, PID: 202, Hostname: "host-a",
			ProcessStartIdentity: "start-b", IsProcessAlive: aliveAlways, GetProcessStartIdentity: func(int) string { return "start-a" },
			Token: func() string { return "token-b" }})
		ce := wantConflict(t, err, "already owned by run 'revive-a'")
		eq(t, ce.Owner.RunID, "revive-a", "owner")
		eq(t, first.Release(), true, "release")
		second, err := acquireSessionLease(leaseRequest(fx.sessionFile, "revive-b"), sessionLeaseOptions{RootDir: fx.leases, Token: func() string { return "token-b" }})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, second.Release(), true, "second release")
	})
	tw(t, f, "acquires a lease without a ps start probe when the platform has none", func(t *testing.T) {
		if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			t.Skip("platform has a native start probe")
		}
		fx := newLeaseFixture(t)
		lease, err := acquireSessionLease(leaseRequest(fx.sessionFile, "no-ps"), sessionLeaseOptions{RootDir: fx.leases})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(lease.Owner.ProcessStartIdentity, "runtime:") {
			t.Errorf("identity %q", lease.Owner.ProcessStartIdentity)
		}
		lease.Release()
	})
	tw(t, f, "keys aliases by the canonical session path", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation is not portable on Windows CI")
		}
		fx := newLeaseFixture(t)
		alias := filepath.Join(fx.root, "alias.jsonl")
		if err := os.Symlink(fx.sessionFile, alias); err != nil {
			t.Fatal(err)
		}
		a, _ := canonicalSessionFilePath(alias)
		b, _ := canonicalSessionFilePath(fx.sessionFile)
		eq(t, a, b, "canonical path")
		da, _ := sessionLeaseDir(alias, fx.leases)
		db, _ := sessionLeaseDir(fx.sessionFile, fx.leases)
		eq(t, da, db, "lease dir")
		lease, err := acquireSessionLease(leaseRequest(alias, "alias-owner"), sessionLeaseOptions{RootDir: fx.leases})
		if err != nil {
			t.Fatal(err)
		}
		_, err = acquireSessionLease(leaseRequest(fx.sessionFile, "direct-owner"), sessionLeaseOptions{RootDir: fx.leases})
		wantConflict(t, err, "already owned by run 'alias-owner'")
		lease.Release()
	})
	tw(t, f, "allows different canonical session files concurrently", func(t *testing.T) {
		fx := newLeaseFixture(t)
		other := filepath.Join(fx.root, "other.jsonl")
		writeFile(t, other, "")
		first, err1 := acquireSessionLease(leaseRequest(fx.sessionFile, "revive-a"), sessionLeaseOptions{RootDir: fx.leases})
		second, err2 := acquireSessionLease(leaseRequest(other, "revive-b"), sessionLeaseOptions{RootDir: fx.leases})
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		first.Release()
		second.Release()
	})
	tw(t, f, "reclaims only demonstrably dead or PID-reused owners", func(t *testing.T) {
		fx := newLeaseFixture(t)
		dead, err := acquireSessionLease(leaseRequest(fx.sessionFile, "dead-owner"), sessionLeaseOptions{RootDir: fx.leases, PID: 101, Hostname: "host-a",
			ProcessStartIdentity: "start-dead", Token: func() string { return "dead-token" }})
		if err != nil {
			t.Fatal(err)
		}
		recovered, err := acquireSessionLease(leaseRequest(fx.sessionFile, "recovered"), sessionLeaseOptions{RootDir: fx.leases, PID: 202, Hostname: "host-a",
			ProcessStartIdentity: "start-recovered", IsProcessAlive: func(pid int) (bool, bool) { return pid != 101, true }, Token: func() string { return "recovered-token" }})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, recovered.Owner.RunID, "recovered", "owner")
		if _, err := os.Stat(dead.LeaseDir + ".stale-dead-token"); err != nil {
			t.Errorf("tombstone: %v", err)
		}
		recovered.Release()

		reused, err := acquireSessionLease(leaseRequest(fx.sessionFile, "reused-owner"), sessionLeaseOptions{RootDir: fx.leases, PID: 303, Hostname: "host-a",
			ProcessStartIdentity: "old-start", Token: func() string { return "reused-token" }})
		if err != nil {
			t.Fatal(err)
		}
		after, err := acquireSessionLease(leaseRequest(fx.sessionFile, "after-reuse"), sessionLeaseOptions{RootDir: fx.leases, PID: 404, Hostname: "host-a",
			ProcessStartIdentity: "new-owner-start", IsProcessAlive: aliveAlways, Token: func() string { return "after-reuse-token" },
			GetProcessStartIdentity: func(pid int) string {
				if pid == 303 {
					return "new-start"
				}
				return "new-owner-start"
			}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, after.Owner.RunID, "after-reuse", "owner after pid reuse")
		if _, err := os.Stat(reused.LeaseDir + ".stale-reused-token"); err != nil {
			t.Errorf("tombstone: %v", err)
		}
		after.Release()
	})
	tw(t, f, "does not reclaim a dead runner while its writer is live or still spawning", func(t *testing.T) {
		fx := newLeaseFixture(t)
		lease, err := acquireSessionLease(leaseRequest(fx.sessionFile, "runner-owner"), sessionLeaseOptions{RootDir: fx.leases, PID: 101, Hostname: "host-a",
			ProcessStartIdentity: "runner-start", GetProcessStartIdentity: func(pid int) string {
				if pid == 202 {
					return "writer-start"
				}
				return ""
			}})
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.UpdateWriter(sessionLeaseWriter{State: "spawning"}); err != nil {
			t.Fatal(err)
		}
		_, err = acquireSessionLease(leaseRequest(fx.sessionFile, "spawning-contender"), sessionLeaseOptions{RootDir: fx.leases, PID: 303, Hostname: "host-a",
			ProcessStartIdentity: "contender-start", IsProcessAlive: func(int) (bool, bool) { return false, true }})
		wantConflict(t, err, "already owned by run 'runner-owner'")
		if err := lease.UpdateWriter(sessionLeaseWriter{State: "running", PID: 202}); err != nil {
			t.Fatal(err)
		}
		_, err = acquireSessionLease(leaseRequest(fx.sessionFile, "writer-contender"), sessionLeaseOptions{RootDir: fx.leases, PID: 303, Hostname: "host-a",
			ProcessStartIdentity: "contender-start", IsProcessAlive: func(pid int) (bool, bool) { return pid == 202, true },
			GetProcessStartIdentity: func(pid int) string {
				if pid == 202 {
					return "writer-start"
				}
				return ""
			}})
		wantConflict(t, err, "already owned by run 'runner-owner'")
		lease.Release()
	})
	tw(t, f, "does not reclaim unverifiable owners on another host", func(t *testing.T) {
		fx := newLeaseFixture(t)
		lease, err := acquireSessionLease(leaseRequest(fx.sessionFile, "remote-owner"), sessionLeaseOptions{RootDir: fx.leases, PID: 101, Hostname: "host-a", ProcessStartIdentity: "start-a"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = acquireSessionLease(leaseRequest(fx.sessionFile, "local-owner"), sessionLeaseOptions{RootDir: fx.leases, PID: 202, Hostname: "host-b",
			ProcessStartIdentity: "start-b", IsProcessAlive: func(int) (bool, bool) { return false, true }})
		wantConflict(t, err, "already owned by run 'remote-owner'")
		lease.Release()
	})
	tw(t, f, "keeps an orphaned writer exclusive after its runner exits", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("process groups and SIGTERM are POSIX")
		}
		fx := newLeaseFixture(t)
		owner := leaseHelper(t, fx, "", "abandon-with-writer")
		var started struct {
			Acquired  bool `json:"acquired"`
			WriterPID int  `json:"writerPid"`
		}
		if err := json.Unmarshal([]byte(owner.line), &started); err != nil || !started.Acquired || started.WriterPID == 0 {
			t.Fatalf("helper said %q (%v)", owner.line, err)
		}
		t.Cleanup(func() { leaseKillPid(started.WriterPID, true) })
		owner.wait(t)
		if !leasePidAlive(started.WriterPID) {
			t.Fatal("the writer must outlive the runner")
		}
		_, err := acquireSessionLease(leaseRequest(fx.sessionFile, "blocked-by-writer"), sessionLeaseOptions{RootDir: fx.leases})
		wantConflict(t, err, "already owned by run 'child-")
		leaseKillPid(started.WriterPID, false)
		waitPidGone(t, started.WriterPID)
		recovered, err := acquireSessionLease(leaseRequest(fx.sessionFile, "after-writer-exit"), sessionLeaseOptions{RootDir: fx.leases})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, recovered.Owner.RunID, "after-writer-exit", "owner")
		recovered.Release()
	})
	tw(t, f, "enforces ownership across independent Node processes and elects one stale successor", func(t *testing.T) {
		fx := newLeaseFixture(t)
		release := filepath.Join(fx.root, "release")
		child := leaseHelper(t, fx, release, "hold")
		var started struct {
			Owner struct {
				PID int `json:"pid"`
			} `json:"owner"`
		}
		_ = json.Unmarshal([]byte(child.line), &started)
		eq(t, started.Owner.PID, child.cmd.Process.Pid, "the owner is the helper process")
		_, err := acquireSessionLease(leaseRequest(fx.sessionFile, "parent-contender"), sessionLeaseOptions{RootDir: fx.leases})
		wantConflict(t, err, "already owned by run 'child-")
		writeFile(t, release, "release")
		child.wait(t)
		after, err := acquireSessionLease(leaseRequest(fx.sessionFile, "parent-after-release"), sessionLeaseOptions{RootDir: fx.leases})
		if err != nil {
			t.Fatal(err)
		}
		after.Release()

		_ = os.Remove(release)
		abandoned := leaseHelper(t, fx, release, "abandon")
		abandoned.wait(t)

		releaseA, releaseB := filepath.Join(fx.root, "release-a"), filepath.Join(fx.root, "release-b")
		a := leaseHelperAsync(t, fx, releaseA, "hold")
		b := leaseHelperAsync(t, fx, releaseB, "hold")
		var sa, sb struct {
			Acquired bool `json:"acquired"`
		}
		_ = json.Unmarshal([]byte(a.readLine(t)), &sa)
		_ = json.Unmarshal([]byte(b.readLine(t)), &sb)
		won := 0
		if sa.Acquired {
			won++
			writeFile(t, releaseA, "release")
		}
		if sb.Acquired {
			won++
			writeFile(t, releaseB, "release")
		}
		eq(t, won, 1, "exactly one contender takes the stale lease")
		a.wait(t)
		b.wait(t)
		recovered, err := acquireSessionLease(leaseRequest(fx.sessionFile, "parent-after-contenders"), sessionLeaseOptions{RootDir: fx.leases})
		if err != nil {
			t.Fatal(err)
		}
		recovered.Release()
	})
}

// ── the helper process (support/session-lease-child.mjs): this test binary run again ──

const leaseHelperEnv = "PISUB_SESSION_LEASE_HELPER"

func TestSessionLeaseHelperProcess(t *testing.T) {
	spec := os.Getenv(leaseHelperEnv)
	if spec == "" {
		t.Skip("helper process of the session-lease twins")
	}
	parts := strings.Split(spec, "\n")
	sessionFile, rootDir, releaseSignal, mode := parts[0], parts[1], parts[2], parts[3]
	lease, err := acquireSessionLease(sessionLeaseRequest{SessionFile: sessionFile, RunID: "child-" + strconv.Itoa(os.Getpid()), SourceRunID: "source-child", ParentSessionID: "parent-child"}, sessionLeaseOptions{RootDir: rootDir})
	out := json.NewEncoder(os.Stdout)
	if err != nil {
		_ = out.Encode(map[string]any{"acquired": false, "error": err.Error()})
		os.Exit(2)
	}
	switch mode {
	case "abandon-with-writer":
		_ = lease.UpdateWriter(sessionLeaseWriter{State: "spawning"})
		pid := leaseSpawnWriter(t)
		_ = lease.UpdateWriter(sessionLeaseWriter{State: "running", PID: pid})
		_ = out.Encode(map[string]any{"acquired": true, "owner": lease.Owner, "writerPid": pid})
		os.Exit(0)
	case "abandon":
		_ = out.Encode(map[string]any{"acquired": true, "owner": lease.Owner})
		os.Exit(0)
	}
	_ = out.Encode(map[string]any{"acquired": true, "owner": lease.Owner})
	for {
		if _, err := os.Stat(releaseSignal); err == nil {
			lease.Release()
			os.Exit(0)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type leaseProc struct {
	cmd  *exec.Cmd
	r    *bufio.Reader
	line string
}

func leaseHelperAsync(t *testing.T, fx leaseFixture, releaseSignal, mode string) *leaseProc {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLeaseHelperProcess$")
	cmd.Env = append(os.Environ(), leaseHelperEnv+"="+strings.Join([]string{fx.sessionFile, fx.leases, releaseSignal, mode}, "\n"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return &leaseProc{cmd: cmd, r: bufio.NewReader(stdout)}
}

func (p *leaseProc) readLine(t *testing.T) string {
	t.Helper()
	line, err := p.r.ReadString('\n')
	if err != nil && line == "" {
		t.Fatalf("the lease helper reported nothing: %v", err)
	}
	return strings.TrimSpace(line)
}

func (p *leaseProc) wait(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the lease helper to exit")
	}
}

func leaseHelper(t *testing.T, fx leaseFixture, releaseSignal, mode string) *leaseProc {
	p := leaseHelperAsync(t, fx, releaseSignal, mode)
	p.line = p.readLine(t)
	return p
}

func waitPidGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for leasePidAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pid %d to exit", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
