package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

type lgFixture struct {
	t                  *testing.T
	logsDir            string
	debugPath, revPath string
	config             LogConfig
}

func newLgFixture(t *testing.T) *lgFixture {
	base := t.TempDir()
	logs := filepath.Join(base, "logs")
	return &lgFixture{t: t, logsDir: logs, debugPath: filepath.Join(logs, "debug.jsonl"), revPath: filepath.Join(logs, "review.jsonl"),
		config: LogConfig{PermissionReviewLog: true}}
}

func (x *lgFixture) logger() PermissionSystemLogger {
	return CreatePermissionSystemLogger(PermissionSystemLoggerOptions{
		GetConfig:     func() LogConfig { return x.config },
		DebugLogPath:  x.debugPath,
		ReviewLogPath: x.revPath,
		EnsureLogsDirectory: func() string {
			os.MkdirAll(x.logsDir, 0o755)
			return ""
		},
	})
}

func (x *lgFixture) read(path string) string {
	x.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		x.t.Fatal(err)
	}
	return string(b)
}

func (x *lgFixture) entry(path string) *jsObject {
	x.t.Helper()
	return jo(x.t, strings.TrimSpace(x.read(path)))
}

func lgGet(o *jsObject, path ...string) any {
	var cur any = o
	for _, p := range path {
		m, ok := cur.(*jsObject)
		if !ok {
			return nil
		}
		cur, _ = m.get(p)
	}
	return cur
}

func lgDetails(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestLoggingTwins(t *testing.T) {
	const f = "logging/logging"
	mode := func(t *testing.T, p string) os.FileMode {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes")
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode().Perm()
	}
	tw(t, f, "creates the review log owner-only", func(t *testing.T) {
		x := newLgFixture(t)
		x.logger().Review("permission_request.waiting", lgDetails("toolName", "write"))
		eq(t, mode(t, x.revPath), os.FileMode(0o600), "mode")
	})
	tw(t, f, "creates the debug log owner-only", func(t *testing.T) {
		x := newLgFixture(t)
		x.config.DebugLog = true
		x.logger().Debug("permission.decision", lgDetails("toolName", "write"))
		eq(t, mode(t, x.debugPath), os.FileMode(0o600), "mode")
	})
	tw(t, f, "tightens a log inherited from an earlier version on next write", func(t *testing.T) {
		x := newLgFixture(t)
		os.MkdirAll(x.logsDir, 0o755)
		os.WriteFile(x.revPath, []byte("{}\n"), 0o644)
		os.Chmod(x.revPath, 0o644)
		x.logger().Review("permission_request.waiting", lgDetails("toolName", "write"))
		eq(t, mode(t, x.revPath), os.FileMode(0o600), "mode")
	})
	tw(t, f, "masks sensitive-keyed values before they reach the review log", func(t *testing.T) {
		x := newLgFixture(t)
		x.logger().Review("permission_request.waiting", lgDetails("toolName", "http", "headers", lgDetails("authorization", "Bearer TEST_VALUE")))
		eq(t, strings.Contains(x.read(x.revPath), "TEST_VALUE"), false, "leak")
		e := x.entry(x.revPath)
		eq(t, lgGet(e, "toolName"), "http", "tool")
		eq(t, lgGet(e, "headers", "authorization"), "[redacted]", "masked")
	})
	tw(t, f, "masks sensitive-keyed values in the debug log too", func(t *testing.T) {
		x := newLgFixture(t)
		x.config.DebugLog = true
		x.logger().Debug("permission.decision", lgDetails("toolName", "http", "apiKey", "sk-real-value"))
		eq(t, strings.Contains(x.read(x.debugPath), "sk-real-value"), false, "leak")
		eq(t, lgGet(x.entry(x.debugPath), "apiKey"), "[redacted]", "masked")
	})

	t.Run("masking a secret inside a command string", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
		review := func(x *lgFixture, kv ...any) *jsObject {
			x.logger().Review("permission_request.waiting", lgDetails(kv...))
			return x.entry(x.revPath)
		}
		tw(t, f, "masks a sensitively-named assignment in the review log", func(t *testing.T) {
			x := newLgFixture(t)
			e := review(x, "toolName", "bash", "command", `KEY="sk-secret-value" curl https://x`)
			eq(t, strings.Contains(x.read(x.revPath), "sk-secret-value"), false, "leak")
			eq(t, lgGet(e, "command"), "KEY=[redacted] curl https://x", "command")
		})
		tw(t, f, "masks the executed unit as well as the command", func(t *testing.T) {
			x := newLgFixture(t)
			e := review(x, "toolName", "bash", "command", "sudo TOKEN=sk-outer-value deploy", "executedUnit", "TOKEN=sk-inner-value deploy")
			w := x.read(x.revPath)
			eq(t, strings.Contains(w, "sk-outer-value") || strings.Contains(w, "sk-inner-value"), false, "leak")
			eq(t, lgGet(e, "command"), "sudo TOKEN=[redacted] deploy", "command")
			eq(t, lgGet(e, "executedUnit"), "TOKEN=[redacted] deploy", "unit")
		})
		tw(t, f, "masks a secret inside an inline-shell payload", func(t *testing.T) {
			x := newLgFixture(t)
			e := review(x, "toolName", "bash", "command", "bash -c 'TOKEN=sk-secret-value deploy'")
			eq(t, strings.Contains(x.read(x.revPath), "sk-secret-value"), false, "leak")
			eq(t, lgGet(e, "command"), "bash -c 'TOKEN=[redacted] deploy'", "command")
		})
		tw(t, f, "reports the same secret consistently under both keys of one record", func(t *testing.T) {
			x := newLgFixture(t)
			e := review(x, "toolName", "bash", "command", "bash -c 'TOKEN=sk-secret-value deploy'", "executedUnit", "TOKEN=sk-secret-value deploy")
			eq(t, lgGet(e, "command"), "bash -c 'TOKEN=[redacted] deploy'", "command")
			eq(t, lgGet(e, "executedUnit"), "TOKEN=[redacted] deploy", "unit")
		})
		tw(t, f, "masks a command in the debug log too, which the width bound does not touch", func(t *testing.T) {
			x := newLgFixture(t)
			x.config.DebugLog = true
			x.logger().Debug("permission.decision", lgDetails("toolName", "bash", "command", `curl -H "Authorization: Bearer sk-secret-value" https://x`))
			eq(t, strings.Contains(x.read(x.debugPath), "sk-secret-value"), false, "leak")
			eq(t, lgGet(x.entry(x.debugPath), "command"), `curl -H "Authorization:[redacted]" https://x`, "command")
		})
		tw(t, f, "masks before bounding the width, so the cap never shortens a secret", func(t *testing.T) {
			x := newLgFixture(t)
			w := 40.0
			x.config.ReviewLogFieldMaxWidth = &w
			e := review(x, "toolName", "bash", "command", `echo 0123456789 && KEY="sk-secret-value" deploy`)
			eq(t, lgGet(e, "command"), "echo 0123456789 && KEY=[redacted] deploy", "command")
		})
		tw(t, f, "leaves a flag-separated value, which binds no name any rule recognizes", func(t *testing.T) {
			x := newLgFixture(t)
			review(x, "toolName", "bash", "command", "deploy --token abc123")
			eq(t, strings.Contains(x.read(x.revPath), "deploy --token abc123"), true, "kept")
		})
	})

	t.Run("the review log's field-width bound", func(t *testing.T) {
		width := func(x *lgFixture, w float64) { x.config.ReviewLogFieldMaxWidth = &w }
		tw(t, f, "shortens an oversized value at the configured width", func(t *testing.T) {
			x := newLgFixture(t)
			width(x, 20)
			x.logger().Review("permission_request.waiting", lgDetails("toolName", "bash", "command", strings.Repeat("a", 500)))
			eq(t, lgGet(x.entry(x.revPath), "command"), strings.Repeat("a", 20)+"\u2026", "cut")
		})
		tw(t, f, "bounds every value the entry carries, not one chosen field", func(t *testing.T) {
			x := newLgFixture(t)
			width(x, 5)
			x.logger().Review("permission_request.waiting", lgDetails("command", strings.Repeat("b", 50), "path", strings.Repeat("c", 50), "toolInputPreview", strings.Repeat("d", 50)))
			e := x.entry(x.revPath)
			eq(t, lgGet(e, "command"), "bbbbb\u2026", "command")
			eq(t, lgGet(e, "path"), "ccccc\u2026", "path")
			eq(t, lgGet(e, "toolInputPreview"), "ddddd\u2026", "preview")
		})
		tw(t, f, "defaults to a width that leaves ordinary commands whole", func(t *testing.T) {
			x := newLgFixture(t)
			c := "pnpm run test --filter @gotgenes/pi-permission-system"
			x.logger().Review("permission_request.waiting", lgDetails("toolName", "bash", "command", c))
			eq(t, lgGet(x.entry(x.revPath), "command"), c, "whole")
		})
		tw(t, f, "masks a sensitive-keyed value whole, however long it was", func(t *testing.T) {
			x := newLgFixture(t)
			width(x, 10)
			x.logger().Review("permission_request.waiting", lgDetails("headers", lgDetails("authorization", "Bearer "+strings.Repeat("TEST_VALUE", 20))))
			eq(t, strings.Contains(x.read(x.revPath), "TEST_VALUE"), false, "leak")
			eq(t, lgGet(x.entry(x.revPath), "headers", "authorization"), "[redacted]", "masked")
		})
		tw(t, f, "bounds a string nested inside the decision provenance record", func(t *testing.T) {
			x := newLgFixture(t)
			width(x, 20)
			x.logger().Review("permission_request.denied", lgDetails("decidedBy", lgDetails("kind", "authorizer", "name", "model-judge", "verdict", "deny", "reason", strings.Repeat("f", 500))))
			eq(t, lgJSON(lgGet(x.entry(x.revPath), "decidedBy")),
				`{"kind":"authorizer","name":"model-judge","verdict":"deny","reason":"`+strings.Repeat("f", 20)+"\u2026"+`"}`, "decidedBy")
		})
		tw(t, f, "bounds a string nested two frames deep in a relayed provenance record", func(t *testing.T) {
			x := newLgFixture(t)
			width(x, 10)
			x.logger().Review("permission_request.approved", lgDetails("decidedBy", lgDetails("kind", "forwarded", "responderSessionId", "parent-session",
				"decision", lgDetails("kind", "unavailable", "reason", strings.Repeat("g", 500)))))
			eq(t, lgGet(x.entry(x.revPath), "decidedBy", "decision", "reason"), strings.Repeat("g", 10)+"\u2026", "cut")
		})
		tw(t, f, "masks a sensitive-keyed value nested in a provenance record", func(t *testing.T) {
			x := newLgFixture(t)
			x.logger().Review("permission_request.denied", lgDetails("decidedBy", lgDetails("kind", "authorizer", "name", "model-judge", "verdict", "deny", "apiKey", "TEST_VALUE_SECRET")))
			eq(t, strings.Contains(x.read(x.revPath), "TEST_VALUE_SECRET"), false, "leak")
			eq(t, lgGet(x.entry(x.revPath), "decidedBy", "apiKey"), "[redacted]", "masked")
		})
		tw(t, f, "leaves the debug log unbounded, since it exists to be read in full", func(t *testing.T) {
			x := newLgFixture(t)
			x.config.DebugLog = true
			width(x, 10)
			x.logger().Debug("permission.decision", lgDetails("command", strings.Repeat("e", 500)))
			eq(t, strings.Contains(x.read(x.debugPath), strings.Repeat("e", 500)), true, "full")
		})
	})

	tw(t, f, "respects debug toggle and keeps review log enabled by default", func(t *testing.T) {
		x := newLgFixture(t)
		l := x.logger()
		eq(t, l.Debug("debug.disabled", lgDetails("sample", true)), "", "debug warning")
		eq(t, l.Review("permission_request.waiting", lgDetails("toolName", "write")), "", "review warning")
		_, derr := os.Stat(x.debugPath)
		_, rerr := os.Stat(x.revPath)
		eq(t, os.IsNotExist(derr), true, "no debug log")
		eq(t, rerr == nil, true, "review log")
		x.config.DebugLog = true
		eq(t, l.Debug("debug.enabled", lgDetails("sample", true)), "", "enabled warning")
		eq(t, strings.Contains(x.read(x.debugPath), "debug.enabled"), true, "written")
	})

	t.Run("the line carries the fixed fields first, in order, and the details spread over them", func(t *testing.T) {
		x := newLgFixture(t)
		x.logger().Review("e.v", lgDetails("a", 1.0, "stream", "mine"))
		line := strings.TrimSpace(x.read(x.revPath))
		eq(t, strings.HasPrefix(line, `{"timestamp":"`), true, "timestamp first")
		eq(t, strings.Contains(line, `"extension":"pi-permission-system","stream":"mine","event":"e.v","a":1}`), true, "line "+line)
	})
	t.Run("a write failure is reported, not raised", func(t *testing.T) {
		x := newLgFixture(t)
		x.revPath = x.logsDir // a directory
		w := x.logger().Review("e", nil)
		eq(t, strings.HasPrefix(w, "Failed to write permission-system review log '"), true, w)
	})
	t.Run("a value that cannot serialize is reported", func(t *testing.T) {
		x := newLgFixture(t)
		w := x.logger().Review("e", lgDetails("fn", struct{ C chan int }{}))
		eq(t, strings.HasPrefix(w, "Failed to write permission-system review log '"), true, w)
	})
}

func TestLoggingCyclicRecord(t *testing.T) {
	// A record that contains itself overflows the original's recursive walks (a RangeError the writer reports as a warning); the
	// port stops at a fixed depth and reports the same text rather than crash on an unrecoverable Go stack overflow.
	x := newLgFixture(t)
	d := newObject()
	d.set("self", d)
	w := x.logger().Review("e", d)
	eq(t, w, "Failed to write permission-system review log '"+x.revPath+"': Maximum call stack size exceeded", "warning")
	_, err := os.Stat(x.revPath)
	eq(t, os.IsNotExist(err), true, "nothing written")
}
