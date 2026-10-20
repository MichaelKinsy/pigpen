package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// Costs of the paths the forwarding layer runs on a timer or per ask: the poll tick of an idle session (four a second for the life of
// every UI session), the throttled heartbeat announcement, a heartbeat read by a polling child, and one served request.

func BenchmarkProcessInboxIdle(b *testing.B) {
	forwardingDir := filepath.Join(b.TempDir(), "forwarding")
	server := NewForwardedRequestServer(ForwardedRequestServerDeps{
		ForwardingDir: forwardingDir, Logger: &fwLogger{}, Policy: fwPolicyReturning(fwCheck("ask")),
		Escalator: fwEscalatorAnswering(fwApprove(DecisionApproved)), Broadcaster: &fwBroadcasterFake{}, Recorder: &fwRecorderFake{},
	})
	ctx := fwMakeForwarderContext(true, "parent-session")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.ProcessInbox(ctx)
	}
}

func BenchmarkMarkServingThrottled(b *testing.B) {
	now := int64(1_700_000_000_000)
	store := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{
		ForwardingDir: filepath.Join(b.TempDir(), "forwarding"), Logger: &fwLogger{}, Now: func() int64 { return now }, PID: 4242,
		IsProcessAlive: onlyOwnPidAlive,
	})
	store.MarkServing("sess-1")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.MarkServing("sess-1")
	}
}

func BenchmarkHeartbeatRead(b *testing.B) {
	now := int64(1_700_000_000_000)
	dir := filepath.Join(b.TempDir(), "forwarding")
	store := NewServingHeartbeatStore(ServingHeartbeatStoreDeps{
		ForwardingDir: dir, Logger: &fwLogger{}, Now: func() int64 { return now }, PID: 4242, IsProcessAlive: onlyOwnPidAlive,
	})
	store.MarkServing("sess-1")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if store.Read("sess-1") != HeartbeatAlive {
			b.Fatal("not alive")
		}
	}
}

func BenchmarkServeOneRequest(b *testing.B) {
	root := b.TempDir()
	forwardingDir := filepath.Join(root, "forwarding")
	location, err := CreatePermissionForwardingLocation(forwardingDir, "parent-session")
	if err != nil {
		b.Fatal(err)
	}
	for _, d := range []string{location.RequestsDir, location.ResponsesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	intent := fwBashIntent("git status")
	payload := fwMakePromptPayload()
	request := ForwardedPermissionRequest{
		ID: "req-1", CreatedAt: 1000, RequesterSessionID: "child-session", TargetSessionID: "parent-session", RequesterAgentName: "Explore",
		Payload: &payload, Source: "tool_call", Surface: DisplayStr("bash"), Value: DisplayStr("git status"), AccessIntent: &intent,
	}
	raw := []byte(marshalJSON(request.JS(), ""))
	server := NewForwardedRequestServer(ForwardedRequestServerDeps{
		ForwardingDir: forwardingDir, Logger: &fwLogger{}, Policy: fwPolicyReturning(fwCheck("allow")),
		Escalator: fwEscalatorAnswering(fwApprove(DecisionApproved)), Broadcaster: &fwBroadcasterFake{}, Recorder: &fwRecorderFake{},
	})
	ctx := fwMakeForwarderContext(true, "parent-session")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := os.MkdirAll(location.RequestsDir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(location.RequestsDir, "req-1.json"), raw, 0o600); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		server.ProcessInbox(ctx)
	}
}
