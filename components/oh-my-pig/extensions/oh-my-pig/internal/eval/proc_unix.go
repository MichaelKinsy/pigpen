//go:build !windows

package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// upstream: packages/coding-agent/src/eval/kernel-base.ts (killProcessGroup),
//           packages/coding-agent/src/eval/py/spawn-options.ts,
//           packages/coding-agent/src/eval/runner-cache.ts,
//           packages/coding-agent/src/utils/owner-private-dir.ts

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func sendInterrupt(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	_ = syscall.Kill(proc.Pid, syscall.SIGINT)
}

func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if grace > 0 {
		select {
		case <-done:
			return
		case <-time.After(grace):
		}
	}
	killProcessTreeImmediate(proc)
}

func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func defaultShellCommand(command string) (string, []string) {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", []string{"-c", command}
	}
	if p, err := exec.LookPath("bash"); err == nil && p != "" {
		return p, []string{"-c", command}
	}
	return "sh", []string{"-c", command}
}

// resolveStagingDir returns a per-uid private (0700) directory under os.TempDir(),
// refusing symlinks, non-directories, foreign owners, and group/other permissions,
// falling back to os.MkdirTemp when the predictable per-uid path is unusable.
// upstream: packages/coding-agent/src/eval/runner-cache.ts:76, utils/owner-private-dir.ts:42
func resolveStagingDir(dirName string) (string, error) {
	uid := os.Getuid()
	base := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", dirName, uid))
	if err := os.MkdirAll(base, 0o700); err == nil {
		if err := assertOwnerPrivateDir(base, "Runner staging directory"); err == nil {
			return base, nil
		}
	}
	dir, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("%s-%d-", dirName, uid))
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// assertOwnerPrivateDir opens dir with O_NOFOLLOW|O_DIRECTORY and checks fstat owner/mode
// on the pinned fd so another local user cannot swap a symlink between stat and use.
// upstream: packages/coding-agent/src/utils/owner-private-dir.ts:42
func assertOwnerPrivateDir(dir, label string) error {
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		if err == syscall.ELOOP || err == syscall.ENOTDIR {
			if info, lerr := os.Lstat(dir); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s %s is a symlink", label, dir)
			}
			return fmt.Errorf("%s %s is not a directory", label, dir)
		}
		return err
	}
	defer syscall.Close(fd)

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	uid := uint32(os.Getuid())
	if st.Uid != uid {
		return fmt.Errorf("%s %s is owned by uid %d, not %d", label, dir, st.Uid, uid)
	}
	mode := st.Mode & 0o777
	if mode != 0o700 {
		if err := syscall.Fchmod(fd, 0o700); err == nil {
			if err := syscall.Fstat(fd, &st); err == nil {
				mode = st.Mode & 0o777
			}
		}
	}
	if mode != 0o700 {
		return fmt.Errorf("%s %s must be mode 0700, got %o", label, dir, mode)
	}
	return nil
}

// configureJSExtraFiles attaches dedicated control-in and frame-out pipes for js_runner.js
// so fd 0/1/2 remain isolated from the NDJSON protocol channel.
func configureJSExtraFiles(cmd *exec.Cmd, ctrlRead, frameWrite *os.File) {
	cmd.ExtraFiles = []*os.File{ctrlRead, frameWrite}
}
