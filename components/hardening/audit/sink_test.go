package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func TestStderrIsTheDefaultSink(t *testing.T) {
	var buf bytes.Buffer
	l, err := Open("warden", &profile.Audit{Sink: profile.SinkStderr}, "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Emit(Event{Event: EventLoad, Package: "warden", Outcome: OutcomeOK, Reason: ReasonNone})
	if !strings.Contains(buf.String(), `"event":"load"`) {
		t.Fatalf("%q", buf.String())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionFileIsAnOptInOpenedOnFirstUse(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	l, err := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile}, dir, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SessionFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file was created before the first event")
	}
	ev := Event{Event: EventToolCall, Package: "warden", Tool: "bash", Outcome: OutcomeOK, Reason: ReasonNone}
	l.Emit(ev)
	l.Emit(ev)
	data, err := os.ReadFile(path)
	if err != nil || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("%q %v", data, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", info.Mode())
		}
	}
	_ = l.Close()
	// A second logger appends (the file survives a crash and a restart).
	l2, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile}, dir, &stderr)
	l2.Emit(ev)
	_ = l2.Close()
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "\n") != 3 {
		t.Fatalf("%q", data)
	}
}

func TestSessionFileNeedsAnAbsoluteDirectoryAndAKnownSink(t *testing.T) {
	var stderr bytes.Buffer
	for _, dir := range []string{"", "relative/dir"} {
		if _, err := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile}, dir, &stderr); !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("%q: %v", dir, err)
		}
	}
	if _, err := Open("warden", &profile.Audit{Sink: "/tmp/anything"}, t.TempDir(), &stderr); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Errorf("sink: %v", err)
	}
	l, err := Open("warden", nil, "", &stderr)
	if l != nil || err != nil {
		t.Fatalf("a nil configuration: %v %v", l, err)
	}
}

func TestAnUnwritableSessionDirectoryCostsOneNoticeAndFailsARequiredAudit(t *testing.T) {
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	l, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile, Required: true}, missing, &stderr)
	ev := Event{Event: EventToolCall, Package: "warden", Tool: "bash", Outcome: OutcomeOK, Reason: ReasonNone}
	if err := l.Record(ev); !profile.IsCode(err, profile.AuditUnavailable) {
		t.Fatalf("%v", err)
	}
	if strings.Count(stderr.String(), "\n") != 1 || strings.Contains(stderr.String(), "such") {
		t.Fatalf("%q", stderr.String())
	}
	// The failure is not remembered: once the directory exists the next event is written.
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(ev); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(missing, SessionFileName)); !strings.Contains(string(data), `"tool":"bash"`) {
		t.Fatalf("%q", data)
	}
}

func TestADirectoryAtTheAuditPathIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, SessionFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	l, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile, Required: true}, dir, &stderr)
	if err := l.Record(Event{Event: EventLoad, Package: "warden", Outcome: OutcomeOK, Reason: ReasonNone}); !profile.IsCode(err, profile.AuditUnavailable) {
		t.Fatal(err)
	}
}

func TestOpenPinsThePackage(t *testing.T) {
	var buf bytes.Buffer
	l, _ := Open("warden", &profile.Audit{Sink: profile.SinkStderr}, "", &buf)
	l.Emit(Event{Event: EventLoad, Package: "a2a", Outcome: OutcomeOK, Reason: ReasonNone})
	if !strings.Contains(buf.String(), `"package":"invalid"`) || l.Invalid() != 1 {
		t.Fatalf("%q", buf.String())
	}
}
