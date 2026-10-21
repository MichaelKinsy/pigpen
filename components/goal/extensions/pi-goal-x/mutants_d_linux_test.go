//go:build linux

package pi_goal_x

import (
	"os/exec"
	"syscall"
	"testing"
)

// The child leads its own group and gets SIGTERM when its parent thread ends.
func TestMutantsDLinuxChildAttributes(t *testing.T) {
	cmd := exec.Command("true")
	configureAuditChild(cmd)
	eq(t, cmd.SysProcAttr.Setpgid, true, "Setpgid")
	eq(t, cmd.SysProcAttr.Pdeathsig, syscall.SIGTERM, "Pdeathsig")
}
