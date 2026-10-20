package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Twins of test/authority/transient-fs-retry.test.ts and test/authority/forwarding-io-fs-retry.test.ts.

// fwFailingOperation fails its first `failures` attempts with code (negative: always), and counts attempts.
func fwFailingOperation(code string, failures int) (operation func() error, attempts func() int) {
	calls := 0
	return func() error {
			calls++
			if failures < 0 || calls <= failures {
				return &fwErrno{code: code, msg: code + ": operation not permitted"}
			}
			return nil
		}, func() int {
			return calls
		}
}

func fwSleepRecorder() (func(int), func() []int) {
	var calls []int
	return func(ms int) { calls = append(calls, ms) }, func() []int { return calls }
}

func TestTransientFsRetry(t *testing.T) {
	const f = "authority/transient-fs-retry"
	tw(t, f, "reports a single attempt and never sleeps when it succeeds outright", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		record, err := RetryOnTransientFsError(func() error { return nil }, &TransientFsRetryOptions{Sleep: sleep})
		if err != nil || record != (TransientFsRetryRecord{Attempts: 1}) || len(sleeps()) != 0 {
			t.Fatalf("record=%+v err=%v sleeps=%v", record, err, sleeps())
		}
	})
	tw(t, f, "retries a transient EPERM and reports the attempts it took", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		operation, attempts := fwFailingOperation("EPERM", 1)
		record, err := RetryOnTransientFsError(operation, &TransientFsRetryOptions{Sleep: sleep})
		if err != nil || record != (TransientFsRetryRecord{Attempts: 2, Code: "EPERM"}) || attempts() != 2 || !reflect.DeepEqual(sleeps(), []int{10}) {
			t.Fatalf("record=%+v err=%v attempts=%d sleeps=%v", record, err, attempts(), sleeps())
		}
	})
	// it.each(["EBUSY", "EACCES"]) is outside the ledger: its titles carry the parameter.
	for _, code := range []string{"EBUSY", "EACCES"} {
		t.Run("retries a transient "+code+" the same way", func(t *testing.T) {
			sleep, sleeps := fwSleepRecorder()
			operation, _ := fwFailingOperation(code, 2)
			record, err := RetryOnTransientFsError(operation, &TransientFsRetryOptions{Sleep: sleep})
			if err != nil || record != (TransientFsRetryRecord{Attempts: 3, Code: code}) || !reflect.DeepEqual(sleeps(), []int{10, 20}) {
				t.Fatalf("record=%+v err=%v sleeps=%v", record, err, sleeps())
			}
		})
	}
	tw(t, f, "exhausts the budget, rethrows the last error, and sleeps between attempts", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		operation, attempts := fwFailingOperation("EPERM", 10)
		_, err := RetryOnTransientFsError(operation, &TransientFsRetryOptions{Sleep: sleep})
		if err == nil || err.Error() != "EPERM: operation not permitted" {
			t.Fatalf("err=%v", err)
		}
		if attempts() != 4 || !reflect.DeepEqual(sleeps(), []int{10, 20, 30}) {
			t.Fatalf("attempts=%d sleeps=%v", attempts(), sleeps())
		}
	})
	tw(t, f, "rethrows a non-transient errno immediately", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		operation, attempts := fwFailingOperation("ENOENT", 10)
		_, err := RetryOnTransientFsError(operation, &TransientFsRetryOptions{Sleep: sleep})
		if err == nil || err.Error() != "ENOENT: operation not permitted" || attempts() != 1 || len(sleeps()) != 0 {
			t.Fatalf("err=%v attempts=%d sleeps=%v", err, attempts(), sleeps())
		}
	})
	tw(t, f, "rethrows a thrown value carrying no errno code", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		calls := 0
		_, err := RetryOnTransientFsError(func() error { calls++; return errors.New("something else") }, &TransientFsRetryOptions{Sleep: sleep})
		if err == nil || err.Error() != "something else" || calls != 1 || len(sleeps()) != 0 {
			t.Fatalf("err=%v calls=%d sleeps=%v", err, calls, sleeps())
		}
	})
	tw(t, f, "honors an injected delay sequence", func(t *testing.T) {
		sleep, sleeps := fwSleepRecorder()
		operation, _ := fwFailingOperation("EBUSY", 1)
		record, err := RetryOnTransientFsError(operation, &TransientFsRetryOptions{Sleep: sleep, DelaysMs: []int{5}})
		if err != nil || record != (TransientFsRetryRecord{Attempts: 2, Code: "EBUSY"}) || !reflect.DeepEqual(sleeps(), []int{5}) {
			t.Fatalf("record=%+v err=%v sleeps=%v", record, err, sleeps())
		}
	})
}

// Go-specific: the errno code classification the retry and the cleanup rely on, for the errors the os package really produces.
func TestFsErrnoCodeOfOSErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := os.ReadFile(filepath.Join(dir, "absent"))
	if got := FsErrnoCode(err); got != "ENOENT" {
		t.Fatalf("ENOENT file: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FsErrnoCode(os.Remove(filepath.Join(dir, "d"))); got != "ENOTEMPTY" {
		t.Fatalf("non-empty dir: %q", got)
	}
	if FsErrnoCode(errors.New("plain")) != "" || FsErrnoCode(nil) != "" {
		t.Fatal("an error without an errno must carry no code")
	}
}

// ── forwarding-io-fs-retry ─────────────────────────────────────────────────

// fwFailRenames fails the next `failures` renames with code (negative: always), then lets the real one run.
func fwFailRenames(t *testing.T, failures int, code string) (calls func() int) {
	t.Helper()
	real := renameFile
	n := 0
	renameFile = func(from, to string) error {
		n++
		if failures < 0 || n <= failures {
			return fwErrnoError(code, "rename")
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })
	return func() int { return n }
}

func fwFailMkdirs(t *testing.T, failures int, code string) {
	t.Helper()
	real := mkdirAll
	n := 0
	mkdirAll = func(path string, perm os.FileMode) error {
		n++
		if failures < 0 || n <= failures {
			return fwErrnoError(code, "mkdir")
		}
		return real(path, perm)
	}
	t.Cleanup(func() { mkdirAll = real })
}

func fwDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteJsonFileAtomicUnderATransientFileLock(t *testing.T) {
	const f = "authority/forwarding-io-fs-retry"
	tw(t, f, "leaves an unrelated errno alone", func(t *testing.T) {
		root := t.TempDir()
		calls := fwFailRenames(t, 1, "ENOSPC")
		err := WriteJSONFileAtomic(&fwLogger{}, filepath.Join(root, "req.json"), fwObj("id", "req-1"))
		if err == nil || err.Error() != "ENOSPC: operation not permitted, rename" || calls() != 1 {
			t.Fatalf("err=%v calls=%d", err, calls())
		}
	})
	tw(t, f, "lands the file when the rename fails EPERM twice", func(t *testing.T) {
		root := t.TempDir()
		filePath := filepath.Join(root, "req.json")
		fwFailRenames(t, 2, "EPERM")
		if err := WriteJSONFileAtomic(&fwLogger{}, filePath, fwObj("id", "req-1")); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filePath)
		if !fwEqual(fwParse(t, string(raw)), fwObj("id", "req-1")) {
			t.Fatalf("content %s", raw)
		}
		if st, _ := os.Stat(filePath); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", st.Mode().Perm())
		}
		if !reflect.DeepEqual(fwDirNames(t, root), []string{"req.json"}) {
			t.Fatalf("dir %v", fwDirNames(t, root))
		}
	})
	tw(t, f, "records the recovery on the debug stream alone", func(t *testing.T) {
		root := t.TempDir()
		filePath := filepath.Join(root, "req.json")
		logger := &fwLogger{}
		fwFailRenames(t, 2, "EPERM")
		if err := WriteJSONFileAtomic(logger, filePath, fwObj("id", "req-1")); err != nil {
			t.Fatal(err)
		}
		d := logger.debugs()
		if len(d) != 1 || d[0].event != "permission_forwarding.fs_retried" ||
			!fwEqual(d[0].details, fwObj("operation", "rename", "path", filePath, "attempts", 3, "code", "EPERM")) {
			t.Fatalf("debug %+v", d)
		}
		if len(logger.reviews()) != 0 {
			t.Fatalf("review %+v", logger.reviews())
		}
	})
	tw(t, f, "records nothing when the rename succeeds outright", func(t *testing.T) {
		root := t.TempDir()
		logger := &fwLogger{}
		if err := WriteJSONFileAtomic(logger, filepath.Join(root, "req.json"), fwObj("id", "req-1")); err != nil {
			t.Fatal(err)
		}
		if len(logger.debugs()) != 0 || len(logger.reviews()) != 0 {
			t.Fatalf("logged %+v %+v", logger.debugs(), logger.reviews())
		}
	})
	tw(t, f, "rethrows a lock that outlasts the budget, leaving no temp file and no partial destination", func(t *testing.T) {
		root := t.TempDir()
		filePath := filepath.Join(root, "req.json")
		fwFailRenames(t, -1, "EPERM")
		err := WriteJSONFileAtomic(&fwLogger{}, filePath, fwObj("id", "req-1"))
		if err == nil || err.Error() != "EPERM: operation not permitted, rename" {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Stat(filePath); statErr == nil {
			t.Fatal("destination exists")
		}
		if len(fwDirNames(t, root)) != 0 {
			t.Fatalf("leftovers %v", fwDirNames(t, root))
		}
	})
}

func TestEnsureDirectoryExistsUnderATransientFileLock(t *testing.T) {
	const f = "authority/forwarding-io-fs-retry"
	tw(t, f, "creates the directory when mkdir fails EPERM once", func(t *testing.T) {
		root := t.TempDir()
		dirPath := filepath.Join(root, "sessions", "parent", "requests")
		logger := &fwLogger{}
		fwFailMkdirs(t, 1, "EPERM")
		if !EnsureDirectoryExists(logger, dirPath, "requests") {
			t.Fatal("not created")
		}
		if st, err := os.Stat(dirPath); err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("stat %v %v", st, err)
		}
		d := logger.debugs()
		if len(d) != 1 || d[0].event != "permission_forwarding.fs_retried" ||
			!fwEqual(d[0].details, fwObj("operation", "mkdir", "path", dirPath, "attempts", 2, "code", "EPERM")) {
			t.Fatalf("debug %+v", d)
		}
	})
	tw(t, f, "reports failure without throwing when the lock outlasts the budget", func(t *testing.T) {
		root := t.TempDir()
		dirPath := filepath.Join(root, "sessions", "parent", "requests")
		logger := &fwLogger{}
		fwFailMkdirs(t, -1, "EPERM")
		if EnsureDirectoryExists(logger, dirPath, "requests") {
			t.Fatal("reported success")
		}
		if _, err := os.Stat(dirPath); err == nil {
			t.Fatal("directory exists")
		}
		r := logger.reviews()
		if len(r) != 1 || r[0].event != "permission_forwarding.error" ||
			!fwEqual(r[0].details, fwObj("message", "Failed to create requests directory '"+dirPath+"'", "error", "EPERM: operation not permitted, mkdir")) {
			t.Fatalf("review %+v", r)
		}
	})
}

// Go-specific: the log's `error` text of a real filesystem failure is worded as Node words it, as the original's logs are.
func TestNodeFsErrorMessages(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "absent.json")
	logger := &fwLogger{}
	if ReadForwardedPermissionRequest(logger, missing) != nil {
		t.Fatal("read a missing file")
	}
	want := "ENOENT: no such file or directory, open '" + missing + "'"
	r := logger.reviews()
	if len(r) != 1 || !fwEqual(r[0].details, fwObj("message", "Failed to read forwarded permission request '"+missing+"'", "error", want)) {
		t.Fatalf("%+v", r)
	}

	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logger = &fwLogger{}
	dir := filepath.Join(blocker, "sub")
	if EnsureDirectoryExists(logger, dir, "requests") {
		t.Fatal("created a directory under a file")
	}
	got, _ := logger.reviews()[0].details.str("error")
	if got != "ENOTDIR: not a directory, mkdir '"+dir+"'" {
		t.Fatal(got)
	}

	logger = &fwLogger{}
	if got := ListRequestFiles(logger, missing); len(got) != 0 {
		t.Fatal(got)
	}
	if msg, _ := logger.reviews()[0].details.str("error"); msg != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatal(msg)
	}
	// A rename names both ends, as Node does.
	err := wrapFsError2(&os.LinkError{Op: "rename", Old: "a", New: "b", Err: os.ErrNotExist}, "rename", "a", "b")
	if _, isWrapped := err.(*NodeFsError); isWrapped {
		t.Fatal("an error without an errno must be left alone")
	}
	if os.PathSeparator == '/' {
		_, err := os.Open(filepath.Join(root, "x", "y"))
		if msg := wrapFsError2(err, "rename", "a", "b").Error(); msg != "ENOENT: no such file or directory, rename 'a' -> 'b'" {
			t.Fatal(msg)
		}
	}
}
