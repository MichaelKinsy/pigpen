package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLogFilePermissionsTwins(t *testing.T) {
	const f = "logging/log-file-permissions"
	posix := func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes: on Windows chmod only toggles the read-only bit")
		}
	}
	tw(t, f, "tightens a world-readable file to owner-only", func(t *testing.T) {
		posix(t)
		p := filepath.Join(t.TempDir(), "review.jsonl")
		os.WriteFile(p, []byte("{}\n"), 0o644)
		os.Chmod(p, 0o644)
		RestrictExistingPathToOwner(p, OwnerOnlyFileMode)
		st, _ := os.Stat(p)
		eq(t, st.Mode().Perm(), os.FileMode(0o600), "mode")
	})
	tw(t, f, "tightens a world-readable directory to owner-only", func(t *testing.T) {
		posix(t)
		p := filepath.Join(t.TempDir(), "logs")
		os.Mkdir(p, 0o755)
		os.Chmod(p, 0o755)
		RestrictExistingPathToOwner(p, OwnerOnlyDirectoryMode)
		st, _ := os.Stat(p)
		eq(t, st.Mode().Perm(), os.FileMode(0o700), "mode")
	})
	tw(t, f, "does not throw for a path that does not exist", func(t *testing.T) {
		RestrictExistingPathToOwner(filepath.Join(t.TempDir(), "absent.jsonl"), OwnerOnlyFileMode)
	})
	tw(t, f, "exposes the POSIX owner-only modes", func(t *testing.T) {
		eq(t, OwnerOnlyFileMode, os.FileMode(0o600), "file")
		eq(t, OwnerOnlyDirectoryMode, os.FileMode(0o700), "dir")
	})
}
