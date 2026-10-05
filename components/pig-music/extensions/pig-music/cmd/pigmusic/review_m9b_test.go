package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Review of M9b: the native engine now needs the pigmusic program, and the doctor's fix (native.BuildPigmusicHint) is
// `go build` in this directory. That build does not use Pigpen's generated go.work, so this module must carry the go.sum
// entries of everything it links (WaxTap, WaxFlow, oto...). Checked offline: only the module cache is used, and a module
// missing from it skips the test rather than failing it.
func TestReviewTheDoctorsBuildCommandWorksWithoutAWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if strings.Contains(string(out), "missing go.sum entry") {
		t.Fatalf("`go build` in cmd/pigmusic (the doctor's fix) fails without a go.work:\n%s", out)
	}
	t.Skipf("cannot check offline: %s", out)
}
