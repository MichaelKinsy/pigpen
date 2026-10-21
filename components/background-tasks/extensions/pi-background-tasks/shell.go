package pi_background_tasks

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// shellConfig is Pi's ShellConfig: the shell a command runs under and its arguments.
type shellConfig struct {
	shell string
	args  []string
}

// getShellConfig resolves the shell as Pi's getShellConfig() does without a custom path: on Windows Git Bash in its known
// locations, then bash.exe on PATH; elsewhere /bin/bash, then bash on PATH, then sh. A Windows host with no bash is an error
// that names the options. upstream: pi-coding-agent utils/shell.ts getShellConfig.
func getShellConfig() (shellConfig, error) {
	if runtime.GOOS == "windows" {
		var paths []string
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			paths = append(paths, pf+`\Git\bin\bash.exe`)
		}
		if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
			paths = append(paths, pf+`\Git\bin\bash.exe`)
		}
		for _, p := range paths {
			if exists(p) {
				return bashShellConfig(p), nil
			}
		}
		if found, err := exec.LookPath("bash.exe"); err == nil && found != "" {
			return bashShellConfig(found), nil
		}
		var b strings.Builder
		b.WriteString("No bash shell found. Options:\n  1. Install Git for Windows: https://git-scm.com/download/win\n  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n  3. Set shellPath in settings.json\n\nSearched Git Bash in:\n")
		for i, p := range paths {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("  " + p)
		}
		return shellConfig{}, errors.New(b.String())
	}
	if exists("/bin/bash") {
		return shellConfig{shell: "/bin/bash", args: []string{"-c"}}, nil
	}
	if found, err := exec.LookPath("bash"); err == nil && found != "" {
		abs, _ := filepath.Abs(found)
		return shellConfig{shell: abs, args: []string{"-c"}}, nil
	}
	return shellConfig{shell: "sh", args: []string{"-c"}}, nil
}

func bashShellConfig(shell string) shellConfig {
	normalized := strings.ToLower(strings.ReplaceAll(shell, "/", `\`))
	if normalized == `c:\windows\system32\bash.exe` || normalized == `c:\windows\sysnative\bash.exe` {
		return shellConfig{shell: shell, args: []string{"-s"}}
	}
	return shellConfig{shell: shell, args: []string{"-c"}}
}
