package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Twins of test/authority/forwarding-io.test.ts.

func fwSkipWithoutPosixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not reported by Windows")
	}
}

func TestForwardingIOFormatting(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "returns the error message for Error instances", func(t *testing.T) {
		if got := FormatUnknownErrorMessage(errors.New("oops")); got != "oops" {
			t.Fatal(got)
		}
	})
	tw(t, f, "converts non-Error values to string", func(t *testing.T) {
		if FormatUnknownErrorMessage("raw string") != "raw string" || FormatUnknownErrorMessage(42) != "42" {
			t.Fatal("not converted")
		}
	})
	tw(t, f, "falls back to String(error) for Error with empty message", func(t *testing.T) {
		// error.message is falsy (""), so the function falls through to String(error)
		if got := FormatUnknownErrorMessage(errors.New("")); got != "Error" {
			t.Fatal(got)
		}
	})
}

func TestForwardingIOIsErrnoCode(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "returns true when code matches", func(t *testing.T) {
		if !IsErrnoCode(&fwErrno{code: "ENOENT"}, "ENOENT") {
			t.Fatal("no match")
		}
	})
	tw(t, f, "returns false when code does not match", func(t *testing.T) {
		if IsErrnoCode(&fwErrno{code: "EACCES"}, "ENOENT") {
			t.Fatal("matched")
		}
	})
	tw(t, f, "returns false for null", func(t *testing.T) {
		if IsErrnoCode(nil, "ENOENT") {
			t.Fatal("matched")
		}
	})
	tw(t, f, "returns false when no code property", func(t *testing.T) {
		if IsErrnoCode(errors.New("{}"), "ENOENT") {
			t.Fatal("matched")
		}
	})
}

func TestForwardingIOLogWarning(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "calls logger.review with the warning event", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingWarning(l, "something went wrong", nil)
		r := l.reviews()
		if len(r) != 1 || r[0].event != "permission_forwarding.warning" || !fwEqual(r[0].details, fwObj("message", "something went wrong")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "calls logger.debug with the warning event", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingWarning(l, "something went wrong", nil)
		d := l.debugs()
		if len(d) != 1 || d[0].event != "permission_forwarding.warning" || !fwEqual(d[0].details, fwObj("message", "something went wrong")) {
			t.Fatalf("%+v", d)
		}
	})
	tw(t, f, "includes formatted error when an error is provided", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingWarning(l, "bad thing", errors.New("fs fail"))
		r := l.reviews()
		if len(r) != 1 || !fwEqual(r[0].details, fwObj("message", "bad thing", "error", "fs fail")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "does not throw when logger is null", func(t *testing.T) {
		LogPermissionForwardingWarning(nil, "ignored", nil)
	})
	tw(t, f, "does not call anything when logger is null", func(t *testing.T) {
		// Verify the null-logger path is a true no-op: the call succeeds silently.
		LogPermissionForwardingWarning(nil, "msg", errors.New("err"))
	})
}

func TestForwardingIOLogError(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "calls logger.review with the error event", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingError(l, "critical failure", nil)
		r := l.reviews()
		if len(r) != 1 || r[0].event != "permission_forwarding.error" || !fwEqual(r[0].details, fwObj("message", "critical failure")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "calls logger.debug with the error event", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingError(l, "critical failure", nil)
		d := l.debugs()
		if len(d) != 1 || d[0].event != "permission_forwarding.error" || !fwEqual(d[0].details, fwObj("message", "critical failure")) {
			t.Fatalf("%+v", d)
		}
	})
	tw(t, f, "includes formatted error when an error is provided", func(t *testing.T) {
		l := &fwLogger{}
		LogPermissionForwardingError(l, "io error", errors.New("ENOENT"))
		r := l.reviews()
		if len(r) != 1 || !fwEqual(r[0].details, fwObj("message", "io error", "error", "ENOENT")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "does not throw when logger is null", func(t *testing.T) {
		LogPermissionForwardingError(nil, "ignored", nil)
	})
}

func TestForwardingArtifactPermissions(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "writes a forwarded request owner-only", func(t *testing.T) {
		root := t.TempDir()
		fwSkipWithoutPosixModes(t)
		filePath := filepath.Join(root, "req.json")
		if err := WriteJSONFileAtomic(nil, filePath, fwObj("id", "req-1")); err != nil {
			t.Fatal(err)
		}
		if st, _ := os.Stat(filePath); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", st.Mode().Perm())
		}
	})
	tw(t, f, "creates a forwarding directory owner-only", func(t *testing.T) {
		root := t.TempDir()
		fwSkipWithoutPosixModes(t)
		dirPath := filepath.Join(root, "sessions", "parent", "requests")
		if !EnsureDirectoryExists(nil, dirPath, "requests") {
			t.Fatal("not created")
		}
		if st, _ := os.Stat(dirPath); st.Mode().Perm() != 0o700 {
			t.Fatalf("mode %o", st.Mode().Perm())
		}
	})
}

// fwBaseRequest is the request the access-intent, session-approval and payload groups start from.
func fwBaseRequest(extra ...any) *jsObject {
	o := fwObj("id", "req-1", "createdAt", 1000, "requesterSessionId", "child-session", "targetSessionId", "parent-session", "requesterAgentName", "researcher")
	for i := 0; i < len(extra); i += 2 {
		o.set(extra[i].(string), fwNorm(extra[i+1]))
	}
	return o
}

func fwWriteAndReadRequest(t *testing.T, raw *jsObject) *ForwardedPermissionRequest {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "req.json")
	if err := WriteJSONFileAtomic(nil, filePath, raw); err != nil {
		t.Fatal(err)
	}
	return ReadForwardedPermissionRequest(nil, filePath)
}

func TestReadForwardedPermissionRequestAccessIntent(t *testing.T) {
	const f = "authority/forwarding-io"
	roundTrip := func(t *testing.T, intent ForwardedAccessIntent) {
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "read", "accessIntent", intent.JS()))
		if parsed == nil || parsed.AccessIntent == nil || !reflect.DeepEqual(*parsed.AccessIntent, intent) {
			t.Fatalf("got %+v want %+v", parsed, intent)
		}
	}
	tw(t, f, "round-trips a well-formed access intent (path surface)", func(t *testing.T) {
		roundTrip(t, ForwardedAccessIntent{
			ForwardedAccessFacts: ForwardedAccessFacts{Surface: "path", MatchValues: []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts"}, BoundaryValue: fwSP("/worktree/issue-42/src/foo.ts")},
			RequesterCwd:         "/worktree/issue-42",
			Principal:            ForwardedPrincipal{SessionID: "child-session", AgentName: "researcher"},
		})
	})
	tw(t, f, "round-trips a non-path access intent (skill surface, null boundary)", func(t *testing.T) {
		roundTrip(t, ForwardedAccessIntent{
			ForwardedAccessFacts: ForwardedAccessFacts{Surface: "skill", MatchValues: []string{"deep-research"}},
			RequesterCwd:         "/repo",
			Principal:            ForwardedPrincipal{SessionID: "child-session", AgentName: "unknown"},
		})
	})
	tw(t, f, "carries only strings on matchValues (the ADR-0002 wire boundary)", func(t *testing.T) {
		intent := ForwardedAccessIntent{
			ForwardedAccessFacts: ForwardedAccessFacts{Surface: "external_directory", MatchValues: []string{"/etc/hosts", "/private/etc/hosts"}, BoundaryValue: fwSP("/private/etc/hosts")},
			RequesterCwd:         "/repo",
			Principal:            ForwardedPrincipal{SessionID: "child-session", AgentName: "researcher"},
		}
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "read", "accessIntent", intent.JS()))
		if parsed.AccessIntent == nil || len(parsed.AccessIntent.MatchValues) != 2 {
			t.Fatalf("%+v", parsed.AccessIntent)
		}
		// The wire form itself holds strings only: the matchValues array of the written file decodes to strings.
		for _, v := range intent.JS().vals["matchValues"].([]any) {
			if _, ok := v.(string); !ok {
				t.Fatalf("non-string %v", v)
			}
		}
	})
	tw(t, f, "reads a request with no access intent as undefined (version skew)", func(t *testing.T) {
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "read"))
		if parsed.AccessIntent != nil {
			t.Fatal("intent read")
		}
		// Display/routing fields still reconstruct.
		if !fwDisplayIs(parsed.Surface, "read") || parsed.RequesterAgentName != "researcher" {
			t.Fatalf("%+v", parsed)
		}
	})
	tw(t, f, "drops a malformed access intent to undefined (non-string match value)", func(t *testing.T) {
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "read", "accessIntent", fwObj(
			"surface", "path", "matchValues", []any{"/ok", 42}, "boundaryValue", nil, "requesterCwd", "/repo",
			"principal", fwObj("sessionId", "child-session", "agentName", "researcher"))))
		if parsed.AccessIntent != nil {
			t.Fatal("intent read")
		}
	})
	tw(t, f, "drops a malformed access intent to undefined (missing principal)", func(t *testing.T) {
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "read", "accessIntent", fwObj(
			"surface", "path", "matchValues", []any{"/ok"}, "boundaryValue", nil, "requesterCwd", "/repo")))
		if parsed.AccessIntent != nil {
			t.Fatal("intent read")
		}
	})
}

func TestReadForwardedPermissionRequestSessionApproval(t *testing.T) {
	const f = "authority/forwarding-io"
	read := func(t *testing.T, sessionApproval any) *ForwardedSessionApproval {
		return fwWriteAndReadRequest(t, fwBaseRequest("surface", "bash", "sessionApproval", sessionApproval)).SessionApproval
	}
	grantObj := func(surface, pattern any) *jsObject { return fwObj("surface", surface, "pattern", pattern) }
	tw(t, f, "round-trips a single grant", func(t *testing.T) {
		got := read(t, fwObj("grants", []any{grantObj("bash", "git *")}))
		if !reflect.DeepEqual(got, &ForwardedSessionApproval{Grants: []ApprovalGrant{{"bash", "git *"}}}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "round-trips several grants naming different surfaces", func(t *testing.T) {
		got := read(t, fwObj("grants", []any{grantObj("external_directory_read", "/outside/*"), grantObj("external_directory_write", "/elsewhere/*")}))
		want := &ForwardedSessionApproval{Grants: []ApprovalGrant{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/elsewhere/*"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "ignores unknown sibling keys on a grant", func(t *testing.T) {
		got := read(t, fwObj("grants", []any{fwObj("surface", "bash", "pattern", "git *", "effect", "read")}))
		if !reflect.DeepEqual(got, &ForwardedSessionApproval{Grants: []ApprovalGrant{{"bash", "git *"}}}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "reads a request with no session approval as undefined (version skew)", func(t *testing.T) {
		parsed := fwWriteAndReadRequest(t, fwBaseRequest("surface", "bash"))
		if parsed.SessionApproval != nil {
			t.Fatal("approval read")
		}
		// The rest of the request still reconstructs.
		if parsed.RequesterAgentName != "researcher" {
			t.Fatal("request lost")
		}
	})
	tw(t, f, "rejects the pre-#810 shape rather than normalizing it", func(t *testing.T) {
		if got := read(t, fwObj("surface", "bash", "patterns", []any{"git *"})); got != nil {
			t.Fatalf("%+v", got)
		}
	})
	// it.each: outside the ledger (its title carries the case label).
	for _, c := range []struct {
		label string
		raw   any
	}{
		{"a non-object", "bash"},
		{"null", nil},
		{"an object with no grants", fwObj()},
		{"an empty grant list", fwObj("grants", []any{})},
		{"a non-array grants", fwObj("grants", "bash")},
		{"a null grant", fwObj("grants", []any{nil})},
		{"an empty surface", fwObj("grants", []any{grantObj("", "git *")})},
		{"a missing pattern", fwObj("grants", []any{fwObj("surface", "bash")})},
		{"a missing surface", fwObj("grants", []any{fwObj("pattern", "git *")})},
		{"a non-string pattern", fwObj("grants", []any{grantObj("bash", 42)})},
		{"one bad grant among good ones", fwObj("grants", []any{grantObj("bash", "git *"), grantObj("bash", 42)})},
	} {
		t.Run("drops a malformed session approval to undefined ("+c.label+")", func(t *testing.T) {
			if got := read(t, c.raw); got != nil {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func fwBashPayload() PromptPayload {
	tool, matched := "bash", "git *"
	agent := "Explore"
	return PromptPayload{
		Kind: "bash",
		Request: PromptRequestFacts{
			Requester: PromptRequester{AgentName: &agent}, Surface: "bash", ToolName: &tool, Value: "git push", MatchedPattern: &matched,
		},
		Evidence:    []PromptEvidence{{Label: "command", Text: "git push"}},
		Annotations: []PromptAnnotation{},
	}
}

func TestReadForwardedPermissionRequestPayload(t *testing.T) {
	const f = "authority/forwarding-io"
	base := func() *jsObject {
		return fwObj("id", "req-1", "createdAt", 1000, "requesterSessionId", "child-session", "targetSessionId", "parent-session", "requesterAgentName", "researcher")
	}
	tw(t, f, "round-trips the child's complete prompt payload", func(t *testing.T) {
		payload := fwBashPayload()
		o := base()
		o.set("payload", payload.JS())
		parsed := fwWriteAndReadRequest(t, o)
		if parsed.Payload == nil || !reflect.DeepEqual(*parsed.Payload, payload) {
			t.Fatalf("got %+v want %+v", parsed.Payload, payload)
		}
	})
	tw(t, f, "reads a request with no payload as undefined (version skew)", func(t *testing.T) {
		parsed := fwWriteAndReadRequest(t, base())
		if parsed.Payload != nil || parsed.RequesterAgentName != "researcher" {
			t.Fatalf("%+v", parsed)
		}
	})
	dropped := func(t *testing.T, edit func(p *jsObject)) {
		t.Helper()
		p := fwMakePromptPayload().JS()
		edit(p)
		o := base()
		o.set("payload", p)
		if parsed := fwWriteAndReadRequest(t, o); parsed.Payload != nil {
			t.Fatalf("payload kept: %+v", parsed.Payload)
		}
	}
	tw(t, f, "drops a payload with an unrecognized kind", func(t *testing.T) {
		dropped(t, func(p *jsObject) { p.set("kind", "telepathy") })
	})
	tw(t, f, "drops a payload whose request facts are malformed", func(t *testing.T) {
		dropped(t, func(p *jsObject) { p.obj("request").set("value", 42.0) })
	})
	tw(t, f, "drops a payload whose requester is malformed", func(t *testing.T) {
		dropped(t, func(p *jsObject) { p.obj("request").set("requester", fwObj("forwarded", true)) })
	})
	tw(t, f, "drops a payload whose evidence entries are malformed", func(t *testing.T) {
		dropped(t, func(p *jsObject) { p.set("evidence", []any{fwObj("label", "command", "text", nil, "detail", nil)}) })
	})
	tw(t, f, "drops a payload whose annotations are malformed", func(t *testing.T) {
		dropped(t, func(p *jsObject) { p.set("annotations", []any{fwObj("source", "judge")}) })
	})
	tw(t, f, "accepts a legacy message-only request and reconstructs no message", func(t *testing.T) {
		// An older child writes `message` and no `payload`. The required-core gate no longer demands the field, so the request is
		// served (from its display fields) rather than rejected outright, and the sentence is not salvaged.
		o := base()
		o.set("message", "Allow this path access?")
		o.set("surface", "read")
		o.set("value", "/tmp/x")
		parsed := fwWriteAndReadRequest(t, o)
		if parsed == nil || !fwDisplayIs(parsed.Surface, "read") || !fwDisplayIs(parsed.Value, "/tmp/x") || parsed.Payload != nil {
			t.Fatalf("%+v", parsed)
		}
	})
}

func fwWriteAndReadResponse(t *testing.T, raw *jsObject) *ForwardedPermissionResponse {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "res.json")
	if err := WriteJSONFileAtomic(nil, filePath, raw); err != nil {
		t.Fatal(err)
	}
	return ReadForwardedPermissionResponse(nil, filePath)
}

func fwBaseResponse(state string, extra ...any) *jsObject {
	o := fwObj("approved", true, "state", state, "responderSessionId", "parent-session", "respondedAt", 1000)
	for i := 0; i < len(extra); i += 2 {
		o.set(extra[i].(string), fwNorm(extra[i+1]))
	}
	return o
}

func TestReadForwardedPermissionResponseDecidedBy(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "round-trips the responder's decider", func(t *testing.T) {
		// The reader rebuilds an allowlist of known fields, so an added one is silently dropped until it is listed.
		got := fwWriteAndReadResponse(t, fwBaseResponse("approved", "decidedBy", fwObj("kind", "user", "via", "dialog")))
		if !reflect.DeepEqual(got.DecidedBy, &DecisionSource{Kind: "user", Via: "dialog"}) {
			t.Fatalf("%+v", got.DecidedBy)
		}
	})
	tw(t, f, "round-trips a nested forwarded decider from a relay hop", func(t *testing.T) {
		decidedBy := fwObj("kind", "forwarded", "responderSessionId", "root-session",
			"decision", fwObj("kind", "rule", "surface", "bash", "pattern", "*", "origin", "global"))
		got := fwWriteAndReadResponse(t, fwBaseResponse("approved", "decidedBy", decidedBy))
		want := &DecisionSource{Kind: "forwarded", ResponderSessionID: fwSP("root-session"),
			Decision: &DecisionSource{Kind: "rule", Surface: "bash", Pattern: fwSP("*"), Origin: fwSP("global")}}
		if !reflect.DeepEqual(got.DecidedBy, want) {
			t.Fatalf("%+v", got.DecidedBy)
		}
	})
	tw(t, f, "drops a malformed decider without rejecting the response", func(t *testing.T) {
		got := fwWriteAndReadResponse(t, fwBaseResponse("approved", "decidedBy", fwObj("kind", "user", "via", "smoke-signal")))
		// The decision itself still has to reach the child; only its unusable provenance is discarded.
		if got == nil || !got.Approved || got.DecidedBy != nil {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "leaves decidedBy absent for an older responder", func(t *testing.T) {
		if got := fwWriteAndReadResponse(t, fwBaseResponse("approved")); got.DecidedBy != nil {
			t.Fatalf("%+v", got.DecidedBy)
		}
	})
}

func TestReadForwardedPermissionResponseSessionGrantWidth(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "round-trips the width the responder's human chose", func(t *testing.T) {
		if got := fwWriteAndReadResponse(t, fwBaseResponse("approved_for_session", "sessionGrantWidth", "family")); got.SessionGrantWidth != "family" {
			t.Fatalf("%q", got.SessionGrantWidth)
		}
	})
	tw(t, f, "round-trips the proven width", func(t *testing.T) {
		if got := fwWriteAndReadResponse(t, fwBaseResponse("approved_for_session", "sessionGrantWidth", "proven")); got.SessionGrantWidth != "proven" {
			t.Fatalf("%q", got.SessionGrantWidth)
		}
	})
	tw(t, f, "drops an unrecognized width without rejecting the response", func(t *testing.T) {
		got := fwWriteAndReadResponse(t, fwBaseResponse("approved_for_session", "sessionGrantWidth", "everything"))
		// Least privilege in the skew direction: the grant still reaches the child, recorded at the width the gate proved.
		if got == nil || !got.Approved || got.State != DecisionApprovedForSession || got.SessionGrantWidth != "" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "leaves the width absent for an older responder", func(t *testing.T) {
		if got := fwWriteAndReadResponse(t, fwBaseResponse("approved_for_session")); got.SessionGrantWidth != "" {
			t.Fatalf("%q", got.SessionGrantWidth)
		}
	})
}

func TestTryRemoveDirectoryIfEmpty(t *testing.T) {
	const f = "authority/forwarding-io"
	tw(t, f, "returns true when the directory does not exist", func(t *testing.T) {
		root := t.TempDir()
		if !TryRemoveDirectoryIfEmpty(nil, filepath.Join(root, "nonexistent"), "test") {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true and removes an empty directory", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "empty")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if !TryRemoveDirectoryIfEmpty(nil, dir, "test") || pathExists(dir) {
			t.Fatal("not removed")
		}
	})
	tw(t, f, "returns false and leaves a non-empty directory in place", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "nonempty")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if TryRemoveDirectoryIfEmpty(nil, dir, "test") || !pathExists(dir) {
			t.Fatal("removed")
		}
	})
}

func TestCleanupPermissionForwardingLocationIfEmpty(t *testing.T) {
	const f = "authority/forwarding-io"
	setup := func(t *testing.T) PermissionForwardingLocation {
		forwardingDir := filepath.Join(t.TempDir(), "forwarding")
		location, err := CreatePermissionForwardingLocation(forwardingDir, "parent-session")
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{location.RequestsDir, location.ResponsesDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return location
	}
	tw(t, f, "preserves responses/ when requests/ is non-empty (the concurrent-request race)", func(t *testing.T) {
		location := setup(t)
		// requests/ has a pending file, responses/ is momentarily empty (sibling subagent A already cleaned up its response)
		if err := os.WriteFile(filepath.Join(location.RequestsDir, "req-b.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		CleanupPermissionForwardingLocationIfEmpty(nil, location)
		// requests/ is non-empty, so it stays; responses/ must survive (removing it causes the ENOENT write loop); so must the root.
		if !pathExists(location.RequestsDir) || !pathExists(location.ResponsesDir) || !pathExists(location.SessionRootDir) {
			t.Fatal("a directory was removed")
		}
	})
	tw(t, f, "removes both subdirs and sessionRoot when both are empty (normal serial cleanup)", func(t *testing.T) {
		location := setup(t)
		CleanupPermissionForwardingLocationIfEmpty(nil, location)
		if pathExists(location.RequestsDir) || pathExists(location.ResponsesDir) || pathExists(location.SessionRootDir) {
			t.Fatal("a directory survived")
		}
	})
	tw(t, f, "leaves responses/ in place when it is non-empty even if requests/ is empty", func(t *testing.T) {
		location := setup(t)
		if err := os.WriteFile(filepath.Join(location.ResponsesDir, "resp.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		CleanupPermissionForwardingLocationIfEmpty(nil, location)
		// requests/ is empty so it gets removed; responses/ is non-empty so it survives, and so does the session root.
		if pathExists(location.RequestsDir) || !pathExists(location.ResponsesDir) || !pathExists(location.SessionRootDir) {
			t.Fatal("wrong directories remain")
		}
	})
}

// fwDisplayIs reports whether a display field holds the string s (DisplayString carries a pointer, so == would compare addresses).
func fwDisplayIs(d DisplayString, s string) bool { return d.Set && d.Value != nil && *d.Value == s }
