//go:build windows

package doctor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// These run on Windows only (CI's windows runner, or a Windows machine): lock probing there is LockFileEx, the same byte-range
// lock PiG takes, so what is probed is what PiG holds.

func writeLockFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json.lock")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAMissingLockIsNotHeld(t *testing.T) {
	held, err := lockHeld(filepath.Join(t.TempDir(), "none.lock"), time.Now())
	if held || err != nil {
		t.Fatalf("held = %v, err = %v", held, err)
	}
}

func TestAFreeLockFileIsNotHeldAndIsLeftAsItWas(t *testing.T) {
	path := writeLockFile(t, "pid 1")
	before, _ := os.Stat(path)
	for i := 0; i < 2; i++ { // probing twice: the first probe must release what it took
		if held, err := lockHeld(path, time.Now()); held || err != nil {
			t.Fatalf("probe %d: held = %v, err = %v", i, held, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the probe changed the lock file: %v %v", after, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "pid 1" {
		t.Fatalf("the probe wrote to the lock file: %q", data)
	}
}

func TestALockAnotherHandleHoldsIsSeenAsHeldUntilItIsReleased(t *testing.T) {
	path := writeLockFile(t, "")
	release, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := lockHeld(path, time.Now()); !held || err != nil {
		t.Fatalf("while held: held = %v, err = %v", held, err)
	}
	release()
	if held, err := lockHeld(path, time.Now()); held || err != nil {
		t.Fatalf("after release: held = %v, err = %v", held, err)
	}
}

func TestALockFileOpenedWithoutSharingIsHeld(t *testing.T) {
	path := writeLockFile(t, "")
	name, _ := syscall.UTF16PtrFromString(path)
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	if held, _ := lockHeld(path, time.Now()); !held {
		t.Fatal("a file another process has open for exclusive use was probed as free")
	}
}

func TestAcquireLockIsExclusive(t *testing.T) {
	path := writeLockFile(t, "")
	release, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := acquireLock(path); err == nil {
		second()
		t.Fatal("a second acquire of a held lock succeeded")
	}
}

func TestAcquireLockRemovesAFileItCreatedAndKeepsOneThatWasThere(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, ".gc.lock")
	release, err := acquireLock(created)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("the lock file is not there while held: %v", err)
	}
	release()
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("a lock file that did not exist before is left behind: %v", err)
	}
	existing := writeLockFile(t, "mine")
	release, err = acquireLock(existing)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if data, err := os.ReadFile(existing); err != nil || string(data) != "mine" {
		t.Fatalf("a lock file that was there is gone or changed: %q %v", data, err)
	}
}

func TestALockDirectoryIsHeldWhileFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x.lock")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if held, err := lockHeld(dir, now); !held || err != nil {
		t.Fatalf("fresh: held = %v, err = %v", held, err)
	}
	old := now.Add(-2 * dirLockFresh)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if held, err := lockHeld(dir, now); held || err != nil {
		t.Fatalf("old: held = %v, err = %v", held, err)
	}
}

func TestALockThatIsASymlinkIsNotFollowed(t *testing.T) {
	target := writeLockFile(t, "")
	link := filepath.Join(t.TempDir(), "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no permission to create a symlink here: %v", err)
	}
	if held, _ := lockHeld(link, time.Now()); !held {
		t.Fatal("a symlinked lock was probed through the link instead of being refused")
	}
	if release, err := acquireLock(link); err == nil {
		release()
		t.Fatal("acquireLock followed a symlink")
	}
}
