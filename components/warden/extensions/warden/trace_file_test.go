package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of tests/trace-file.test.ts. The environment variable is PIGPEN_WARDEN_TRACE_DIR (the port's naming, D4).

func testTraceEntry(line string, details ...string) *TraceEntry {
	if details == nil {
		details = []string{}
	}
	return &TraceEntry{At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Guard: "action", Line: line, Details: details, Tokens: map[string]string{"level": "allow"}}
}

func traceRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestPackageVersionMatchesPackageJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Version string }
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != PackageVersion {
		t.Errorf("package.json %s, PackageVersion %s", manifest.Version, PackageVersion)
	}
}

// twin: tests/trace-file.test.ts:17
func TestTraceDirIsOnlyForANonEmptyAbsolutePath(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, v := range []string{"", "traces", "./traces"} {
		eq(t, "dir "+v, TraceDir(env(v)), "")
	}
	abs, _ := filepath.Abs(filepath.Join(string(filepath.Separator), "var", "tmp", "traces"))
	eq(t, "absolute", TraceDir(env(abs)), abs)
	root := filepath.Join(string(filepath.Separator), "t")
	eq(t, "path", TraceFilePath(root, "0199-abc"), filepath.Join(root, "0199-abc.jsonl"))
	eq(t, "the session id cannot leave the directory", TraceFilePath(root, "../../etc/x"), filepath.Join(root, "etcx.jsonl"))
	eq(t, "empty id falls to the pid", TraceFilePath(root, "..."), filepath.Join(root, traceFileID("")+".jsonl"))
}

// twin: tests/trace-file.test.ts:27
func TestTraceFileIsOwnerOnlyAndHoldsSessionEntriesAndAmends(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "traces")
	path := TraceFilePath(dir, "session-1")
	var failures []string
	trace := newTrace(2)
	file := NewTraceFile(path, dir, TraceFileSession{SessionID: "session-1", Cwd: "/work/project", Mode: "steer", Judgments: "on"}, func(s string) { failures = append(failures, s) })
	trace.Subscribe(file.Listener)
	first := testTraceEntry("first", "ran: npm test")
	trace.Push(first)
	trace.Push(testTraceEntry("second"))
	if !trace.Amend(first, "outcome: approved") {
		t.Fatal("amend of a live entry")
	}
	trace.Push(testTraceEntry("third"))
	if trace.Amend(first, "evicted") {
		t.Fatal("the memory limit dropped the first entry")
	}
	trace.Clear()
	file.Flush()

	eq(t, "failures", len(failures), 0)
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %v", info.Mode().Perm())
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v", info.Mode().Perm())
		}
	}
	records := traceRecords(t, path)
	var kinds []any
	for _, r := range records {
		if r["kind"] == "entry" {
			kinds = append(kinds, r["id"])
		} else {
			kinds = append(kinds, r["kind"])
		}
		eq(t, "v", r["v"], 1.0)
	}
	eq(t, "order", kinds, []any{"session", 1.0, 2.0, "amend", 3.0})
	session := records[0]
	eq(t, "sessionId", session["sessionId"], "session-1")
	eq(t, "cwd", session["cwd"], "/work/project")
	eq(t, "mode", session["mode"], "steer")
	eq(t, "judgments", session["judgments"], "on")
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(session["wardenVersion"].(string)) {
		t.Errorf("version %v", session["wardenVersion"])
	}
	if _, err := time.Parse(time.RFC3339Nano, session["at"].(string)); err != nil {
		t.Error(err)
	}
	eq(t, "the entry line is the entry as pushed, not as amended later", records[1], map[string]any{
		"v": 1.0, "kind": "entry", "id": 1.0, "at": "2026-01-02T03:04:05.000Z", "guard": "action", "line": "first",
		"details": []any{"ran: npm test"}, "tokens": map[string]any{"level": "allow"}})
	eq(t, "amend", records[3]["line"], "outcome: approved")
	if _, err := time.Parse(time.RFC3339Nano, records[3]["at"].(string)); err != nil {
		t.Error(err)
	}
}

// twin: tests/trace-file.test.ts:61
func TestTraceFileSessionLineCarriesJudgmentStateAndAChangeAddsOneLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "judgments")
	path := TraceFilePath(dir, "j")
	trace := newTrace(100)
	file := NewTraceFile(path, dir, TraceFileSession{SessionID: "j", Cwd: "/w", Mode: "steer", Judgments: JudgmentsStateOf(JudgmentsKeyRejected)}, func(s string) { t.Error("no failure:", s) })
	trace.Subscribe(file.Listener)
	file.Judgments(JudgmentsStateOf(JudgmentsKeyRejected))
	trace.Push(testTraceEntry("first"))
	file.Judgments(JudgmentsStateOf(""))
	file.Judgments(JudgmentsStateOf(""))
	file.Judgments(JudgmentsStateOf(JudgmentsBudget))
	file.Flush()
	var got [][]any
	records := traceRecords(t, path)
	for _, r := range records {
		got = append(got, []any{r["kind"], r["judgments"]})
	}
	eq(t, "lines", got, [][]any{{"session", "off:key_rejected"}, {"entry", nil}, {"judgments", "on"}, {"judgments", "off:budget"}})
	if _, err := time.Parse(time.RFC3339Nano, records[2]["at"].(string)); err != nil {
		t.Error(err)
	}
}

// twin: tests/trace-file.test.ts:78
func TestTraceFileSecondSessionContinuesEntryIDs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reload")
	path := TraceFilePath(dir, "same")
	for round := 0; round < 2; round++ {
		trace := newTrace(100)
		file := NewTraceFile(path, dir, TraceFileSession{SessionID: "same", Cwd: "/w", Mode: "steer", Judgments: "on"}, func(s string) { t.Error("no failure:", s) })
		trace.Subscribe(file.Listener)
		trace.Push(testTraceEntry("round a"))
		trace.Push(testTraceEntry("round b"))
		file.Flush()
	}
	var got []any
	for _, r := range traceRecords(t, path) {
		if r["kind"] == "entry" {
			got = append(got, r["id"])
		} else {
			got = append(got, r["kind"])
		}
	}
	eq(t, "ids", got, []any{"session", 1.0, 2.0, "session", 3.0, 4.0})
}

// twin: tests/trace-file.test.ts:93
func TestTraceFileWriteFailureIsReportedOnceAndStopsTheFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "traces")
	var failures []string
	trace := newTrace(100)
	file := NewTraceFile(TraceFilePath(dir, "s"), dir, TraceFileSession{SessionID: "s", Cwd: "/w", Mode: "steer", Judgments: "on"}, func(s string) { failures = append(failures, s) })
	trace.Subscribe(file.Listener)
	trace.Push(testTraceEntry("a"))
	trace.Push(testTraceEntry("b"))
	file.Flush()
	trace.Push(testTraceEntry("c"))
	file.Flush()
	eq(t, "failures", len(failures), 1)
	if len(failures) == 1 && !regexp.MustCompile(`trace file .* could not be written`).MatchString(failures[0]) {
		t.Errorf("message %q", failures[0])
	}
}

// twin: tests/trace-file.test.ts:109
func TestTraceFileFailureAfterOpenIsAlsoReportedOnce(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("a read-only file still opens for writing as root and on Windows")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	path := TraceFilePath(dir, "s")
	var failures []string
	trace := newTrace(100)
	file := NewTraceFile(path, dir, TraceFileSession{SessionID: "s", Cwd: "/w", Mode: "steer", Judgments: "on"}, func(s string) {
		failures = append(failures, s)
		panic("the reporter itself fails")
	})
	trace.Subscribe(file.Listener)
	file.Flush()
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	trace.Push(testTraceEntry("a"))
	trace.Push(testTraceEntry("b"))
	file.Flush()
	eq(t, "failures", len(failures), 1)
	var kinds []any
	for _, r := range traceRecords(t, path) {
		kinds = append(kinds, r["kind"])
	}
	eq(t, "records", kinds, []any{"session"})
}
