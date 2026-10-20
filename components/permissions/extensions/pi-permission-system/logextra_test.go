package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Cases beyond the original's tests, each there because a mutant of the port survived without it.

func TestLogExtras(t *testing.T) {
	t.Run("a Go error reads as an Error with no stack", func(t *testing.T) {
		eq(t, lgSafe(t, errors.New("x")), `{"name":"Error","message":"x"}`, "json")
	})
	t.Run("an invalid UTF-8 byte reads as U+FFFD", func(t *testing.T) {
		eq(t, lgSafe(t, "a\xffb"), `"a\ufffdb"`, "json")
	})
	t.Run("a replacer sees the index of an array element as its key", func(t *testing.T) {
		var keys []string
		_, _ = jsonStringifyWith([]any{"a", "b"}, func(k string, v any) any { keys = append(keys, k); return v })
		eq(t, keys, []string{"", "0", "1"}, "keys")
	})
	t.Run("a sensitive name whose only capital is Z", func(t *testing.T) {
		eq(t, IsSensitiveName("authoriZation"), true, "authoriZation")
	})
	t.Run("a Go map is capped like an object, keys sorted", func(t *testing.T) {
		got := CapLogFieldWidths(map[string]any{"b": "xxxxxx", "a": "yyyyyy"}, 2)
		eq(t, lgJSON(got), `{"a":"yy…","b":"xx…"}`, "capped")
	})
	t.Run("a Go map is masked like an object", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
		eq(t, lgJSON(maskValue(map[string]any{"command": "KEY=a x"}, false, 0)), `{"command":"KEY=[redacted] x"}`, "masked")
	})
	t.Run("the timestamp is UTC with milliseconds", func(t *testing.T) {
		x := newLgFixture(t)
		zone := time.FixedZone("x", 5*3600)
		l := CreatePermissionSystemLogger(PermissionSystemLoggerOptions{
			GetConfig: func() LogConfig { return x.config }, DebugLogPath: x.debugPath, ReviewLogPath: x.revPath,
			EnsureLogsDirectory: func() string { os.MkdirAll(x.logsDir, 0o755); return "" },
			Now:                 func() time.Time { return time.Date(2020, 1, 2, 8, 4, 5, 678_000_000, zone) },
		})
		l.Review("e", nil)
		eq(t, strings.HasPrefix(x.read(x.revPath), `{"timestamp":"2020-01-02T03:04:05.678Z",`), true, x.read(x.revPath))
	})
	t.Run("a second write appends a line", func(t *testing.T) {
		x := newLgFixture(t)
		l := x.logger()
		l.Review("one", nil)
		l.Review("two", nil)
		eq(t, strings.Count(x.read(x.revPath), "\n"), 2, "two lines")
		eq(t, strings.Contains(x.read(x.revPath), `"event":"one"`), true, "first kept")
	})
	t.Run("a log is tightened once per logger, not on every write", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes")
		}
		x := newLgFixture(t)
		l := x.logger()
		l.Review("one", nil)
		os.Chmod(x.revPath, 0o644)
		l.Review("two", nil)
		st, _ := os.Stat(x.revPath)
		eq(t, st.Mode().Perm(), os.FileMode(0o644), "left as the operator set it")
	})
	t.Run("a failed write is reported with Node's message and no path", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("/dev/full")
		}
		x := newLgFixture(t)
		x.revPath = "/dev/full"
		w := x.logger().Review("e", nil)
		eq(t, w, "Failed to write permission-system review log '/dev/full': ENOSPC: no space left on device, write", "warning")
	})
	t.Run("a failed open names the path", func(t *testing.T) {
		x := newLgFixture(t)
		x.revPath = x.logsDir
		w := x.logger().Review("e", nil)
		eq(t, strings.Contains(w, "EISDIR: illegal operation on a directory, open '"+x.logsDir+"'") || strings.Contains(w, "open '"+x.logsDir+"'"), true, w)
	})
	t.Run("a review warning is notified once, on its own", func(t *testing.T) {
		d := t.TempDir()
		barrier := filepath.Join(d, "barrier")
		os.WriteFile(barrier, nil, 0o644)
		n := &lgNotes{}
		l := NewPermissionSessionLogger(SessionLoggerDeps{GlobalLogsDir: filepath.Join(barrier, "logs"), GetConfig: func() LogConfig { return LogConfig{PermissionReviewLog: true} }, Notify: n.notify})
		l.Review("e", nil)
		l.Review("e", nil)
		eq(t, len(n.msgs), 1, "once")
	})
	t.Run("the trace of a block carries the reason", func(t *testing.T) {
		tr := &lgTracer{}
		var ctx sdk.Context
		CreateFailClosedToolCall(lgGate(GateOutcome{Action: "block", Reason: "r"}, nil), &lgReporter{}, &lgAudit{}, tr)(lgToolEvent("read", nil), ctx)
		eq(t, lgJSON(tr.details[0]), `{"toolName":"read","action":"block","reason":"r"}`, "details")
	})
	t.Run("the command is read from arguments when the event has no input", func(t *testing.T) {
		in := newObject()
		in.set("command", "ls")
		e := newObject()
		e.set("name", "bash")
		e.set("arguments", in)
		c, ok := bestEffortCommand(e)
		eq(t, ok, true, "ok")
		eq(t, c, "ls", "command")
	})
	t.Run("a masker that panics leaves the command as it was", func(t *testing.T) {
		eq(t, redactWithParser(lgPanicParser{}, "KEY=a x"), "KEY=a x", "unchanged")
	})
	t.Run("a payload layer past the depth bound is not descended", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
		layer := func(inner string) string { return "bash -c '" + strings.ReplaceAll(inner, "'", `'"'"'`) + "'" }
		cmd := "TOKEN=sk-x d"
		var masked []bool
		for i := 0; i < 7; i++ {
			cmd = layer(cmd)
			masked = append(masked, !strings.Contains(RedactCommandSecrets(cmd), "sk-x"))
		}
		eq(t, masked, []bool{true, true, true, true, false, false, false}, "layers masked")
	})
	t.Run("an environment word named with a leading underscore is masked", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
		eq(t, RedactCommandSecrets("env _TOKEN=abc deploy"), "env _TOKEN=[redacted] deploy", "masked")
	})
	t.Run("a header field must open with a letter", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
		for _, c := range []string{`curl -H "9token: abc" x`, `curl -H "-token: abc" x`, `curl -H "_token: abc" x`} {
			eq(t, RedactCommandSecrets(c), c, c)
		}
	})
}

type lgPanicParser struct{}

func (lgPanicParser) Parse(string) *bashparse.Tree { panic("boom") }
