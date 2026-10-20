//go:build windows

package powerline_footer

import (
	"os/exec"
	"testing"
)

// The shell and git child processes must not flash a console window (upstream: windowsHide: true in completion.ts runGit).
func TestBashChildProcessesHideTheirConsole(t *testing.T) {
	for name, configure := range map[string]func(*exec.Cmd){"git": hideConsoleWindow, "shell": configureShellProcess} {
		cmd := exec.Command("cmd")
		configure(cmd)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
			t.Errorf("%s: SysProcAttr.HideWindow is not set", name)
		}
	}
}
