package langsmith

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func lockDirOf(configPath string) string { return configPath + ".oauth.lock.lock" }

// foreignLock creates a lock directory held by "someone else" with the given metadata (nil: no created_at file).
func foreignLock(t testing.TB, configPath string, createdAt *string) string {
	t.Helper()
	dir := lockDirOf(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if createdAt != nil {
		if err := os.WriteFile(filepath.Join(dir, lockMetadataFile), []byte(*createdAt), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func shortenLockTiming(t *testing.T, poll time.Duration, stale float64) {
	prevPoll, prevStale := lockPollInterval, lockStaleAfterMs
	lockPollInterval, lockStaleAfterMs = poll, stale
	t.Cleanup(func() { lockPollInterval, lockStaleAfterMs = prevPoll, prevStale })
}

// hostDates answers the SDK's Date.parse for the non-canonical lock timestamps the tests write.
func hostDates(t *testing.T, table map[string]float64) {
	sdk.SetHostHelpers(&sdk.HostHelpers{ParseDate: func(text string) (float64, bool) {
		v, ok := table[text]
		return v, ok
	}})
	t.Cleanup(func() { sdk.SetHostHelpers(nil) })
}

var (
	isoLine  = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
	uuidV4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func TestLockAcquireWritesMetadataAndReleases(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "nested", "deeper", "config.json") // the parent does not exist yet
	lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+5000)
	if err != nil {
		t.Fatal(err)
	}
	dir := lockDirOf(cfg)
	b, err := os.ReadFile(filepath.Join(dir, lockMetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) != 3 || lines[2] != "" || !isoLine.MatchString(lines[0]) || !uuidV4RE.MatchString(lines[1]) {
		t.Fatalf("metadata %q", b)
	}
	if lines[1] != lock.owner {
		t.Fatalf("owner on disk %q, handle %q", lines[1], lock.owner)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(dir)
		mi, _ := os.Stat(filepath.Join(dir, lockMetadataFile))
		if fi.Mode().Perm()&0o077 != 0 || mi.Mode().Perm() != 0o600 {
			t.Errorf("modes: dir %v file %v", fi.Mode().Perm(), mi.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, lockMetadataFile+".tmp")); err == nil {
		t.Error("temp file left behind")
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("lock directory survives release: %v", err)
	}
	if err := lock.release(); err != nil { // releasing a lock that is gone is not an error
		t.Fatal(err)
	}
	var none *oauthRefreshLock
	if err := none.release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockReleaseChecksOwner(t *testing.T) {
	for name, meta := range map[string]*string{
		"other owner":      new("2020-01-01T00:00:00.000Z\nsomeone-else\n"),
		"blank owner line": new("2020-01-01T00:00:00.000Z\n   \n"),
		"one line":         new("2020-01-01T00:00:00.000Z"),
		"empty file":       new(""),
		"no metadata file": nil,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.json")
			lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+5000)
			if err != nil {
				t.Fatal(err)
			}
			dir := lockDirOf(cfg)
			if meta == nil {
				os.Remove(filepath.Join(dir, lockMetadataFile))
			} else if err := os.WriteFile(filepath.Join(dir, lockMetadataFile), []byte(*meta), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := lock.release(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("a lock owned by someone else was removed: %v", err)
			}
		})
	}
	t.Run("padded owner matches", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "config.json")
		lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+5000)
		if err != nil {
			t.Fatal(err)
		}
		dir := lockDirOf(cfg)
		os.WriteFile(filepath.Join(dir, lockMetadataFile), []byte("2020\r\n  "+lock.owner+"  \r\n"), 0o600)
		if err := lock.release(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("owner with surrounding whitespace (CRLF file) not recognised")
		}
	})
}

func TestLockMutualExclusionGoroutines(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	const workers, rounds = 8, 20
	var inside atomic.Int32
	counter := 0 // protected by the file lock alone: -race flags a lock that lets two holders in
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+30_000)
				if err != nil {
					t.Error(err)
					return
				}
				if inside.Add(1) != 1 {
					t.Error("two holders")
				}
				counter++
				time.Sleep(200 * time.Microsecond)
				inside.Add(-1)
				if err := lock.release(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if counter != workers*rounds {
		t.Fatalf("counter %d", counter)
	}
}

func TestLockTimeout(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	foreignLock(t, cfg, new(jsISOString(nowMs())+"\nholder\n"))

	start := time.Now()
	_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+150)
	if err == nil || err.Error() != "timed out acquiring OAuth refresh lock" {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d < 120*time.Millisecond || d > 3*time.Second {
		t.Fatalf("waited %v", d)
	}
	// A deadline in the past gives up after the first attempt, without sleeping.
	start = time.Now()
	if _, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()-1); err == nil {
		t.Fatal("expected timeout")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("slept %v past the deadline", d)
	}
	// The foreign lock was left alone.
	if o, ok := lockOwner(lockDirOf(cfg)); !ok || o != "holder" {
		t.Fatalf("owner %q %v", o, ok)
	}
}

func TestLockAcquiredWhenHolderReleases(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	dir := foreignLock(t, cfg, new(jsISOString(nowMs())+"\nholder\n"))
	go func() {
		time.Sleep(60 * time.Millisecond)
		os.RemoveAll(dir)
	}()
	start := time.Now()
	lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+5000)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	if d := time.Since(start); d < 50*time.Millisecond {
		t.Fatalf("acquired after %v while the holder still had it", d)
	}
}

func TestLockContextEndsPollSleep(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	foreignLock(t, cfg, new(jsISOString(nowMs())+"\nholder\n"))
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("shutdown")
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel(cause)
	}()
	_, err := acquireOAuthRefreshLock(ctx, cfg, nowMs()+60_000)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
}

func TestLockStaleBreak(t *testing.T) {
	// "garbage" is absent from the table: the SDK's Date.parse answers NaN for it.
	hostDates(t, map[string]float64{"Thu, 01 Jan 1970 00:00:00 GMT": 0})
	now := nowMs()
	old := func(ms int64) string { return jsISOString(now - ms) }
	cases := []struct {
		name   string
		meta   *string
		mtime  time.Duration // age of the directory when no timestamp can be read
		stale  bool
		expect string
	}{
		{"old timestamp", new(old(20_000) + "\nx\n"), 0, true, ""},
		{"timestamp just over the limit", new(old(10_001) + "\nx\n"), 0, true, ""},
		{"padded timestamp", new("  " + old(20_000) + "  \nx\n"), 0, true, ""},
		{"CRLF line ends", new(old(20_000) + "\r\nx\r\n"), 0, true, ""},
		{"timestamp only", new(old(20_000)), 0, true, ""},
		{"rfc 2822 date", new("Thu, 01 Jan 1970 00:00:00 GMT\nx\n"), 0, true, ""},
		{"fresh timestamp", new(old(1000) + "\nx\n"), 0, false, ""},
		{"timestamp in the future", new(jsISOString(now+60_000) + "\nx\n"), 0, false, ""},
		{"garbage falls back to a fresh mtime", new("garbage\nx\n"), 0, false, ""},
		{"garbage falls back to an old mtime", new("garbage\nx\n"), time.Hour, true, ""},
		{"empty file with an old mtime", new(""), time.Hour, true, ""},
		{"blank first line with an old mtime", new("  \nx\n"), time.Hour, true, ""},
		{"empty file with a fresh mtime", new(""), 0, false, ""},
		{"no metadata, old mtime", nil, time.Hour, true, ""},
		{"no metadata, fresh mtime", nil, 0, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.json")
			dir := foreignLock(t, cfg, c.meta)
			if c.mtime > 0 {
				past := time.Now().Add(-c.mtime)
				if err := os.Chtimes(dir, past, past); err != nil {
					t.Fatal(err)
				}
			}
			os.MkdirAll(filepath.Join(dir, "sub"), 0o700) // removal is recursive
			os.WriteFile(filepath.Join(dir, "sub", "f"), []byte("x"), 0o600)
			if c.mtime > 0 { // creating sub bumped the mtime
				past := time.Now().Add(-c.mtime)
				os.Chtimes(dir, past, past)
			}
			lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+200)
			if c.stale {
				if err != nil {
					t.Fatalf("stale lock not broken: %v", err)
				}
				defer lock.release()
				if o, _ := lockOwner(dir); o != lock.owner {
					t.Fatalf("owner %q", o)
				}
				if _, err := os.Stat(filepath.Join(dir, "sub")); err == nil {
					t.Fatal("old lock content survived")
				}
				return
			}
			if err == nil || err.Error() != "timed out acquiring OAuth refresh lock" {
				t.Fatalf("a live lock was broken or another error: %v", err)
			}
		})
	}
}

// An unparseable timestamp the SDK's date parser cannot even be asked about is the host's failure, not "NaN".
func TestLockDateParserFailureIsReturned(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	foreignLock(t, cfg, new("not-iso\nx\n"))
	sdk.SetHostHelpers(nil)
	_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+200)
	if err == nil || err.Error() == "timed out acquiring OAuth refresh lock" {
		t.Fatalf("err = %v", err)
	}
}

func TestLockFilesystemErrors(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "file")
		os.WriteFile(file, []byte("x"), 0o600)
		_, err := acquireOAuthRefreshLock(context.Background(), filepath.Join(file, "config.json"), nowMs()+200)
		if err == nil || strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("lock path is a file", func(t *testing.T) {
		// mkdir reports EEXIST, the file has no metadata inside it and a fresh mtime: wait, then time out.
		cfg := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(lockDirOf(cfg), []byte("x"), 0o600)
		_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+100)
		if err == nil || err.Error() != "timed out acquiring OAuth refresh lock" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("mkdir permission error is not a wait", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a read-only directory the user cannot write")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0o700)
		start := time.Now()
		_, err := acquireOAuthRefreshLock(context.Background(), filepath.Join(dir, "config.json"), nowMs()+5000)
		if err == nil || !errors.Is(err, os.ErrPermission) || time.Since(start) > time.Second {
			t.Fatalf("err = %v after %v", err, time.Since(start))
		}
	})
	t.Run("metadata write failure removes the directory", func(t *testing.T) {
		prev := lockWriteMetadata
		boom := errors.New("disk full")
		lockWriteMetadata = func(string, []byte) error { return boom }
		defer func() { lockWriteMetadata = prev }()
		cfg := filepath.Join(t.TempDir(), "config.json")
		_, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+5000)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(lockDirOf(cfg)); !os.IsNotExist(err) {
			t.Fatalf("lock directory left behind: %v", err)
		}
	})
}

func TestRandomUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		u, err := randomUUIDv4()
		if err != nil || !uuidV4RE.MatchString(u) || seen[u] {
			t.Fatalf("uuid %q err %v", u, err)
		}
		seen[u] = true
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	if err := writeFileAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	// A temp path that cannot be written fails before the destination is touched.
	os.Mkdir(p+".tmp", 0o700)
	if err := writeFileAtomic(p, []byte("three")); err == nil {
		t.Fatal("expected an error")
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("destination changed: %q", b)
	}
}

// ---------------------------------------------------------------- cross-process

const lockHelperEnv = "LSPROFILE_LOCK_HELPER"

// TestProfileLockHelper is the child of the cross-process tests, not a test of its own.
func TestProfileLockHelper(t *testing.T) {
	spec := os.Getenv(lockHelperEnv)
	if spec == "" {
		t.Skip("child process of TestLockMutualExclusionProcesses")
	}
	parts := strings.Split(spec, "|") // mode|dir|rounds
	mode, dir := parts[0], parts[1]
	cfg := filepath.Join(dir, "config.json")
	if mode == "hold" {
		lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+10_000)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hold:", err)
			os.Exit(2)
		}
		_ = lock
		os.WriteFile(filepath.Join(dir, "held"), nil, 0o600)
		time.Sleep(time.Minute) // killed by the parent while holding the lock
		os.Exit(3)
	}
	rounds, _ := strconv.Atoi(parts[2])
	counter := filepath.Join(dir, "counter")
	marker := filepath.Join(dir, "marker")
	for range rounds {
		lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+60_000)
		if err != nil {
			fmt.Fprintln(os.Stderr, "acquire:", err)
			os.Exit(2)
		}
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "two holders at once:", err)
			os.Exit(4)
		}
		f.Close()
		b, _ := os.ReadFile(counter)
		n, _ := strconv.Atoi(string(b))
		time.Sleep(300 * time.Microsecond)
		os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o600)
		os.Remove(marker)
		if err := lock.release(); err != nil {
			fmt.Fprintln(os.Stderr, "release:", err)
			os.Exit(5)
		}
	}
	os.Exit(0)
}

func startLockHelper(t *testing.T, spec string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileLockHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+spec)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestLockMutualExclusionProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	const procs, rounds = 3, 25
	cmds := make([]*exec.Cmd, procs)
	for i := range cmds {
		cmds[i] = startLockHelper(t, fmt.Sprintf("run|%s|%d", dir, rounds))
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("child %d: %v", i, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "counter"))
	if n, _ := strconv.Atoi(string(b)); n != procs*rounds {
		t.Fatalf("counter %q, want %d: the lock let two processes interleave", b, procs*rounds)
	}
	if _, err := os.Stat(lockDirOf(filepath.Join(dir, "config.json"))); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

// A process killed while holding the lock leaves it behind; the next one breaks it once it is stale.
func TestLockOfKilledProcessIsBroken(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	cmd := startLockHelper(t, "hold|"+dir)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "held")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("helper never took the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	cfg := filepath.Join(dir, "config.json")
	if _, err := os.Stat(lockDirOf(cfg)); err != nil {
		t.Fatal("the killed holder left no lock")
	}
	shortenLockTiming(t, 5*time.Millisecond, 400)
	start := time.Now()
	lock, err := acquireOAuthRefreshLock(context.Background(), cfg, nowMs()+10_000)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("broke the lock after %v, before it was stale", d)
	}
}
