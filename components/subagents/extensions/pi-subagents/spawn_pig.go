package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The pig a child is (pi-spawn.ts, binary-pi-spawn.ts). The original spawns Pi through Node (cli.js of the Pi package) or as
// a compiled Bun image; a child here is the native pig binary, so the plan is the program, never `node <script>`.

const (
	pigBinaryEnv        = "PIG_SUBAGENT_PIG_BINARY"
	pigHarnessBinaryEnv = "PIG_HARNESS_BINARY"
)

type pigSpawnCommand struct {
	Command string
	Args    []string
}

// pigSpawnDeps are the system facts the resolution reads; tests supply their own.
type pigSpawnDeps struct {
	GOOS       string
	Getenv     func(string) string
	Executable func() (string, error)
	Exists     func(string) bool
	LookPath   func(string) (string, error)
}

func osPigSpawnDeps() pigSpawnDeps {
	return pigSpawnDeps{GOOS: runtime.GOOS, Getenv: os.Getenv, Executable: os.Executable, Exists: fileExists, LookPath: exec.LookPath}
}

// getPigSpawnCommand picks the pig to start: PIG_SUBAGENT_PIG_BINARY (an override is the program itself on every platform),
// the pig that runs this extension (PIG_HARNESS_BINARY, the host's own image, renamed or not), a pig next to this program,
// then PATH; plain `pig` when none exists.
func getPigSpawnCommand(args []string, deps pigSpawnDeps) pigSpawnCommand {
	for _, k := range []string{pigBinaryEnv, pigHarnessBinaryEnv} {
		if b := strings.TrimSpace(deps.Getenv(k)); b != "" {
			return pigSpawnCommand{Command: b, Args: args}
		}
	}
	name := "pig"
	if deps.GOOS == "windows" {
		name = "pig.exe"
	}
	if self, err := deps.Executable(); err == nil {
		dir := filepath.Dir(self)
		if deps.GOOS == "windows" {
			// filepath on a non-Windows build does not split a drive path; the program's directory ends at its last separator
			if i := strings.LastIndexAny(self, `\/`); i >= 0 {
				dir = self[:i]
			}
			if c := dir + `\` + name; deps.Exists(c) {
				return pigSpawnCommand{Command: c, Args: args}
			}
		} else if c := filepath.Join(dir, name); deps.Exists(c) {
			return pigSpawnCommand{Command: c, Args: args}
		}
	}
	if p, err := deps.LookPath("pig"); err == nil {
		return pigSpawnCommand{Command: p, Args: args}
	}
	return pigSpawnCommand{Command: "pig", Args: args}
}

// pigBinary is the pig to start for a child of this process.
func pigBinary() string { return getPigSpawnCommand(nil, osPigSpawnDeps()).Command }
