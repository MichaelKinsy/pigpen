package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// execCommand is the real ExecFunc: it runs a command with a 10 second limit and returns stdout.
func execCommand(env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	// A neutral directory: a go.mod where the doctor was started must not make
	// `go version` switch to (and download) another toolchain during a read-only check.
	cmd.Dir = string(filepath.Separator)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// defaultProcs picks the process source for this platform.
func defaultProcs(exec ExecFunc, env []string) ProcSource {
	if runtime.GOOS == "linux" {
		return &procFS{Root: "/proc"}
	}
	return &psProcs{Exec: exec, Env: env}
}

// procFS lists processes from a /proc-style tree (Linux).
type procFS struct{ Root string }

func (p *procFS) List() ([]Proc, error) {
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join(p.Root, e.Name())
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		proc := Proc{PID: pid, Args: splitNul(cmdline)}
		proc.Exe, _ = os.Readlink(filepath.Join(dir, "exe"))
		proc.Cwd, _ = os.Readlink(filepath.Join(dir, "cwd"))
		if env, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
			proc.Env = map[string]string{}
			for _, kv := range splitNul(env) {
				if k, v, ok := strings.Cut(kv, "="); ok && procEnvKeys[k] {
					proc.Env[k] = v
				}
			}
		}
		out = append(out, proc)
	}
	return out, nil
}

// procEnvKeys are the only variables kept from another process's environment:
// the rest (API keys among them) is dropped as soon as it is parsed.
var procEnvKeys = map[string]bool{"PIG_CODING_AGENT_DIR": true, "PIG_HOME": true, "HOME": true}

func splitNul(b []byte) []string {
	var out []string
	for _, s := range bytes.Split(b, []byte{0}) {
		if len(s) > 0 {
			out = append(out, string(s))
		}
	}
	return out
}

// psProcs lists processes with ps (macOS and other systems without /proc).
// The environment of a process comes from `ps eww`, only for pig-like processes.
type psProcs struct {
	Exec ExecFunc
	Env  []string
}

func (p *psProcs) List() ([]Proc, error) {
	out, err := p.Exec(p.Env, "ps", "-axo", "pid=,command=")
	if err != nil {
		return nil, err
	}
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidStr, rest, ok := strings.Cut(line, " ")
		pid, err := strconv.Atoi(pidStr)
		if !ok || err != nil {
			continue
		}
		args := strings.Fields(strings.TrimSpace(rest))
		if len(args) == 0 {
			continue
		}
		proc := Proc{PID: pid, Args: args}
		if pigLikeName(args[0]) {
			if eww, err := p.Exec(p.Env, "ps", "eww", "-p", strconv.Itoa(pid), "-o", "command="); err == nil {
				proc.Env = map[string]string{}
				for _, f := range strings.Fields(eww) {
					if k, v, ok := strings.Cut(f, "="); ok && isEnvName(k) && procEnvKeys[k] {
						proc.Env[k] = v
					}
				}
			}
		}
		procs = append(procs, proc)
	}
	return procs, nil
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func pigLikeName(arg0 string) bool {
	name := filepath.Base(arg0)
	return name == "pig" || strings.HasPrefix(name, "pig-") && name != "pig-doctor"
}

// isPigProcess reports whether p is a running PiG (or a Piglet Binary under home).
// The doctor itself never counts.
func isPigProcess(p Proc, home string, self int) bool {
	if p.PID == self || len(p.Args) == 0 {
		return false
	}
	if filepath.Base(p.Args[0]) == "pig-doctor" {
		return false
	}
	if pigLikeName(p.Args[0]) {
		return true
	}
	if p.Exe != "" && within(filepath.Join(home, "artifacts"), p.Exe) {
		return true
	}
	if v := p.Env["PIG_HOME"]; v != "" && filepath.Clean(v) == filepath.Clean(home) {
		return true
	}
	return false
}
