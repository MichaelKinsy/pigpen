package ast

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A version probe that times out is not remembered: the next probe of the same
// binary runs again and can succeed.
func TestVersionProbeTimeoutIsNotCached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a POSIX shell script")
	}
	resetProbeCacheForTests()
	t.Cleanup(resetProbeCacheForTests)
	old := versionProbeTimeout
	t.Cleanup(func() { versionProbeTimeout = old })

	dir := t.TempDir()
	slow := filepath.Join(dir, "slow")
	bin := filepath.Join(dir, "ast-grep")
	// exec replaces the shell so that the timeout kills the sleeping process
	// itself and no child keeps the output pipe open.
	script := "#!/bin/sh\nif [ -e '" + slow + "' ]; then exec sleep 60; fi\nprintf 'ast-grep 0.40.5\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slow, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	versionProbeTimeout = 300 * time.Millisecond
	if verifyAstGrepBinary(bin) {
		t.Fatal("a probe that timed out must not verify the binary")
	}

	// The binary itself is unchanged (same size and mtime); only its answer is.
	if err := os.Remove(slow); err != nil {
		t.Fatal(err)
	}
	versionProbeTimeout = time.Minute
	if !verifyAstGrepBinary(bin) {
		t.Fatal("the binary stayed rejected after a timed-out probe")
	}
}
