package pi_permission_system

import "os"

// Port of src/logging/log-file-permissions.ts: owner-only POSIX modes for the extension's on-disk artifacts.

const (
	OwnerOnlyFileMode      os.FileMode = 0o600
	OwnerOnlyDirectoryMode os.FileMode = 0o700
)

// RestrictExistingPathToOwner is a best-effort tightening of an existing path's mode. It never reports: on Windows chmod only
// toggles the read-only bit, and a hardening failure must never break the gate.
func RestrictExistingPathToOwner(path string, mode os.FileMode) {
	_ = os.Chmod(path, mode)
}
