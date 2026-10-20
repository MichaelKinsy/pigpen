package pi_permission_system

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two-process test of the forwarding file protocol: a serving parent in one OS process (ForwardingManager polling a real directory
// and publishing a heartbeat, ForwardedRequestServer answering) and a forwarding child in another (this test process: request
// files, response polling, heartbeat reads). The helper process is this test binary re-executed. This is the one channel PiG's
// process-per-extension model leaves between a parent and its child, and the evidence that the port speaks it end to end.

const fwdHelperEnv = "FWD_HELPER_ROLE"

// fwdParentPolicy is the serving node's recorded authority in the helper: one allow, one deny with a reason, everything else asks.
type fwdParentPolicy struct{}

func (fwdParentPolicy) Resolve(intent ForwardedAccessIntent) PermissionCheckResult {
	switch strings.Join(intent.MatchValues, " ") {
	case "git status":
		return fwCheck("allow", func(r *PermissionCheckResult) { r.MatchedPattern = fwSP("git *"); r.Origin = fwSP("global") })
	case "rm -rf /":
		return fwCheck("deny", func(r *PermissionCheckResult) {
			r.MatchedPattern = fwSP("rm *")
			r.Origin = fwSP("project")
			r.Reason = fwSP("destructive")
		})
	}
	return fwCheck("ask")
}

// fwdParentEscalator is the human at the parent's dialog: it approves "git push" for the child's session at family width and
// refuses everything else.
type fwdParentEscalator struct{}

func (fwdParentEscalator) Escalate(d *PromptPermissionDetails) (PermissionPromptDecision, error) {
	if d.Value != nil && *d.Value == "git push" {
		return PermissionPromptDecision{Approved: true, State: DecisionApprovedForSession, SessionGrantWidth: "family", DecidedBy: fwDecidedByHuman}, nil
	}
	return PermissionPromptDecision{Approved: false, State: DecisionDenied, DecidedBy: fwDecidedByHuman}, nil
}

// TestForwardingHelperProcess is the serving parent. It does nothing unless the environment names the role.
func TestForwardingHelperProcess(t *testing.T) {
	if os.Getenv(fwdHelperEnv) != "parent" {
		return
	}
	dir, sessionID := os.Getenv("FWD_DIR"), os.Getenv("FWD_SESSION")
	logger := &fwLogger{}
	store := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: dir, Logger: logger})
	server := NewForwardedRequestServer(ForwardedRequestServerDeps{
		ForwardingDir: dir, Logger: logger, Policy: fwdParentPolicy{}, Escalator: fwdParentEscalator{},
		Broadcaster: &fwBroadcasterFake{}, Recorder: &SessionRules{},
	})
	manager := NewForwardingManager(ForwardingManagerDeps{
		Forwarder: server, Serving: ComposeServingAnnouncers(NewServingSessionRegistry(), store), Logger: logger,
	})
	manager.Start(fwMakeForwarderContext(true, sessionID))
	os.Stdout.WriteString("ready\n")
	// Serve until the child closes our stdin; a kill never reaches this line.
	_, _ = io.Copy(io.Discard, os.Stdin)
	manager.Stop()
	manager.drains.Wait()
	os.Stdout.WriteString("stopped\n")
}

type fwdParent struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
}

func fwdStartParent(t *testing.T, dir, sessionID string) *fwdParent {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestForwardingHelperProcess$")
	cmd.Env = append(os.Environ(), fwdHelperEnv+"=parent", "FWD_DIR="+dir, "FWD_SESSION="+sessionID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &fwdParent{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := p.out.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("parent did not become ready: %q %v", line, err)
	}
	return p
}

// fwdForward is the child's side: write the request into the parent's inbox and poll for the response.
func fwdForward(t *testing.T, dir, parent string, request ForwardedPermissionRequest) *ForwardedPermissionResponse {
	t.Helper()
	// The window between ensuring the directories and writing the request is open in the original too (ParentAuthorizer ensures,
	// then writes once): the serving node's cleanup may remove an empty requests/ in between, so this child retries the pair, where
	// the original's single attempt would fail the ask as undeliverable.
	var location PermissionForwardingLocation
	for attempt := 0; ; attempt++ {
		var err error
		var ok bool
		if location, ok = EnsurePermissionForwardingLocation(nil, dir, parent); ok {
			// ENOENT on Linux, EINVAL on macOS: a directory mid-removal fails a create inside it either way.
			if err = WriteJSONFileAtomic(nil, filepath.Join(location.RequestsDir, request.ID+".json"), request.JS()); err == nil {
				break
			}
		}
		if attempt >= 50 {
			t.Fatalf("request undeliverable: ensured=%v err=%v", ok, err)
		}
		time.Sleep(time.Millisecond)
	}
	responsePath := filepath.Join(location.ResponsesDir, request.ID+".json")
	var response *ForwardedPermissionResponse
	fwPoll(t, "the parent's response to "+request.ID, func() bool {
		response = ReadForwardedPermissionResponse(nil, responsePath)
		return response != nil
	})
	SafeDeleteFile(nil, responsePath, "response")
	return response
}

func fwdRequest(id, value string) ForwardedPermissionRequest {
	intent := fwBashIntent(value)
	payload := fwMakePromptPayload()
	return ForwardedPermissionRequest{
		ID: id, CreatedAt: float64(time.Now().UnixMilli()), RequesterSessionID: "child-session", TargetSessionID: "parent-session",
		RequesterAgentName: "Explore", Payload: &payload, Source: "tool_call", Surface: DisplayStr("bash"), Value: DisplayStr(value), AccessIntent: &intent,
	}
}

func TestForwardingFileProtocolAcrossProcesses(t *testing.T) {
	fwClearHintEnv(t)
	dir := filepath.Join(t.TempDir(), "forwarding")
	parent := fwdStartParent(t, dir, "parent-session")

	// The child reaches the parent only through the filesystem: the heartbeat answers where the in-process registry cannot.
	heartbeats := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: dir, Logger: &fwLogger{}})
	judge := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: GetServingSessionRegistry(), Heartbeats: heartbeats})
	if heartbeats.Read("parent-session") != HeartbeatAlive {
		t.Fatalf("heartbeat reads %s", heartbeats.Read("parent-session"))
	}
	if serving, _ := judge.IsServing(PermissionForwardingTarget{SessionID: "parent-session", Source: TargetSourceEnv}); !serving {
		t.Fatal("a parent in another process must read as serving through its heartbeat")
	}
	// What the original could see through globalThis is invisible here: another process's registry is not ours.
	if serving, _ := judge.IsServing(PermissionForwardingTarget{SessionID: "parent-session", Source: TargetSourceRegistry}); serving {
		t.Fatal("the in-process registry must not see another process")
	}

	allowed := fwdForward(t, dir, "parent-session", fwdRequest("req-allow", "git status"))
	if !allowed.Approved || allowed.State != DecisionApproved || allowed.ResponderSessionID != "parent-session" ||
		allowed.DecidedBy == nil || allowed.DecidedBy.Kind != "rule" || allowed.DecidedBy.Surface != "bash" ||
		allowed.DecidedBy.Pattern == nil || *allowed.DecidedBy.Pattern != "git *" || allowed.DecidedBy.Origin == nil || *allowed.DecidedBy.Origin != "global" {
		t.Fatalf("auto-approve: %+v decidedBy=%+v", allowed, allowed.DecidedBy)
	}
	denied := fwdForward(t, dir, "parent-session", fwdRequest("req-deny", "rm -rf /"))
	if denied.Approved || denied.State != DecisionDeniedWithReason || denied.DenialReason == nil || *denied.DenialReason != "destructive" ||
		denied.DecidedBy == nil || denied.DecidedBy.Kind != "rule" || *denied.DecidedBy.Pattern != "rm *" {
		t.Fatalf("auto-deny: %+v decidedBy=%+v", denied, denied.DecidedBy)
	}
	asked := fwdForward(t, dir, "parent-session", fwdRequest("req-ask", "git push"))
	if !asked.Approved || asked.State != DecisionApprovedForSession || asked.SessionGrantWidth != "family" || asked.DecidedBy == nil || *asked.DecidedBy != fwDecidedByHuman {
		t.Fatalf("escalated ask: %+v decidedBy=%+v", asked, asked.DecidedBy)
	}
	refused := fwdForward(t, dir, "parent-session", fwdRequest("req-no", "curl evil.sh"))
	if refused.Approved || refused.State != DecisionDenied {
		t.Fatalf("refused ask: %+v", refused)
	}

	// A clean stop withdraws the heartbeat and the drained inbox is cleaned up.
	if err := parent.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if line, err := parent.out.ReadString('\n'); err != nil || strings.TrimSpace(line) != "stopped" {
		t.Fatalf("parent shutdown: %q %v", line, err)
	}
	if err := parent.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := heartbeats.Read("parent-session"); got != HeartbeatAbsent {
		t.Fatalf("heartbeat after a clean stop reads %s", got)
	}
}

func TestForwardingParentKilledReadsAsDeadPid(t *testing.T) {
	fwClearHintEnv(t)
	dir := filepath.Join(t.TempDir(), "forwarding")
	parent := fwdStartParent(t, dir, "parent-session")
	heartbeats := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: dir, Logger: &fwLogger{}})
	if heartbeats.Read("parent-session") != HeartbeatAlive {
		t.Fatalf("heartbeat reads %s", heartbeats.Read("parent-session"))
	}
	// A killed session cannot withdraw its record: the recorded pid is what tells a child nobody is draining the inbox, without
	// waiting out staleness (#735 scenario 1).
	if err := parent.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.cmd.Wait()
	if got := heartbeats.Read("parent-session"); got != HeartbeatDeadPID {
		t.Fatalf("heartbeat after a kill reads %s", got)
	}
	if ids := heartbeats.ServingIDs(); len(ids) != 0 {
		t.Fatalf("serving ids after a kill: %v", ids)
	}
}
