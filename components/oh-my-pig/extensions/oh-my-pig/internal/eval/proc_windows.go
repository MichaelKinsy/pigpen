//go:build windows

package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// upstream: packages/coding-agent/src/eval/kernel-base.ts, py/spawn-options.ts, runner-cache.ts

const (
	winCreateNewProcessGroup = 0x00000200
	winCtrlBreakEvent        = 1
)

var (
	kernel32DLL                  = syscall.NewLazyDLL("kernel32.dll")
	procGenerateConsoleCtrlEvent = kernel32DLL.NewProc("GenerateConsoleCtrlEvent")
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Place the child in its own console process group so CTRL_BREAK_EVENT
	// targets the child group without interrupting the PiG host process.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNewProcessGroup,
	}
}

func sendInterrupt(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	_, _, _ = procGenerateConsoleCtrlEvent.Call(uintptr(winCtrlBreakEvent), uintptr(proc.Pid))
}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	killProcessTreeImmediate(proc)
	if grace > 0 {
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
}

func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	if err := cmd.Run(); err != nil {
		_ = proc.Kill()
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}

func defaultShellCommand(command string) (string, []string) {
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		p := filepath.Join(base, "Git", "bin", "bash.exe")
		if _, err := os.Stat(p); err == nil {
			return p, []string{"-c", command}
		}
	}
	if p, err := exec.LookPath("bash.exe"); err == nil && p != "" {
		return p, []string{"-c", command}
	}
	if comspec := os.Getenv("ComSpec"); comspec != "" {
		return comspec, []string{"/C", command}
	}
	return "cmd.exe", []string{"/C", command}
}

// resolveStagingDir returns a staging directory under the per-user temp directory on Windows.
// upstream: packages/coding-agent/src/eval/runner-cache.ts:78-83
func resolveStagingDir(dirName string) (string, error) {
	dir := filepath.Join(os.TempDir(), dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func assertOwnerPrivateDir(dir, label string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrInvalid
	}
	return nil
}

// configureJSExtraFiles is a no-op on Windows (ExtraFiles is unsupported);
// js_runner.js falls back to stdio pipes when extra FDs are not configured.
func configureJSExtraFiles(_ *exec.Cmd, _, _ *os.File) {}
