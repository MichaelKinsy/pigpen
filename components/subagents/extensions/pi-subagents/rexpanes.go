package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// rexHost runs children as interactive terminals in a split beside the parent's Rex block, through the rex CLI.
type rexHost struct {
	bin, session, parent, sessionDir string
	notify                           func(msg, level string)
}

// rexCLI runs the rex CLI and returns its standard output; a failure carries its standard error.
var rexCLI = func(bin string, args ...string) ([]byte, error) {
	out, err := exec.Command(bin, args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = errors.New(strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

// rexPanesEnabled reports whether subagents run in Rex panes: the setting is on, the Rex terminal environment is
// present, and the platform is not Windows (the child starts through /bin/sh).
func rexPanesEnabled(s subagentSettings, getenv func(string) string, goos string) bool {
	return s.rexPanes != nil && *s.rexPanes && goos != "windows" &&
		getenv("REX_SESSION") != "" && getenv("REX_BLOCK") != ""
}

// rexBinary is the rex CLI of the server this terminal runs in (REX_BIN_DIR), else the one on PATH.
func rexBinary(getenv func(string) string) string {
	if d := getenv("REX_BIN_DIR"); d != "" {
		return filepath.Join(d, "rex")
	}
	return "rex"
}

var shellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// rexEnvScript is env as a shell script of exports, without the parent block's REX_ variables (Rex sets the child's own).
// The environment travels in a file the child's shell reads, not on the command line, so process listings do not show it.
func rexEnvScript(env []string) string {
	var b strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && shellName.MatchString(k) && !strings.HasPrefix(k, "REX_") {
			b.WriteString("export " + k + "='" + strings.ReplaceAll(v, "'", `'\''`) + "'\n")
		}
	}
	return b.String()
}

// call is one block method of Rex's terminal: its JSON result decoded into result.
func (p *rexHost) call(method, block string, result any) error {
	args, _ := json.Marshal(map[string]string{"block_id": block})
	out, err := rexCLI(p.bin, "api", "call", "-s", p.session, "com.superlogical.terminal."+method, string(args))
	if err != nil {
		return err
	}
	return json.Unmarshal(out, result)
}

// poll is the child's state: whether its program ended and with which code, else the status PiG reported (OSC 7501).
func (p *rexHost) poll(block string) (st string, finished bool, exit int, err error) {
	var proc struct {
		LastExit *struct {
			ExitCode int `json:"exit_code"`
		} `json:"last_exit"`
	}
	if err := p.call("process", block, &proc); err != nil {
		return "", false, 0, err
	}
	if proc.LastExit != nil {
		return "", true, proc.LastExit.ExitCode, nil
	}
	var status struct {
		Records []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"records"`
	}
	if err := p.call("program_status", block, &status); err != nil {
		return "", false, 0, err
	}
	for _, r := range status.Records {
		if r.ID == "" { // the root record: PiG reports no other
			st = r.State
		}
	}
	return st, false, 0, nil
}

// run runs req in a pane. Before the split, any failure falls back to the hidden runner for this one call; after it,
// a failure is the child's error and the child never runs twice.
func (p *rexHost) run(done <-chan struct{}, req childRequest, agent string) (childResult, error) {
	select {
	case <-done:
		return childResult{}, errCancelled
	default:
	}
	fallback := func(why string) (childResult, error) {
		p.notify(why, "warning")
		return runChild(done, req)
	}
	if len(req.Stdin) > paneTaskMax {
		return fallback(agent + "'s task is over 100 KiB; it runs without a pane.")
	}
	if _, _, _, err := p.poll(p.parent); err != nil {
		return fallback(fmt.Sprintf("Rex is not reachable (%v); %s runs without a pane.", err, agent))
	}

	dir := p.sessionDir
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "pig-subagent-"); err != nil {
			return childResult{}, err
		}
		defer os.RemoveAll(dir)
	}
	envFile, err := os.CreateTemp("", "pig-subagent-env-") // mode 0600
	if err != nil {
		return childResult{}, err
	}
	defer os.Remove(envFile.Name()) // the child's shell removes it too, once read
	_, err = envFile.WriteString(rexEnvScript(req.Env))
	if cerr := envFile.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return childResult{}, err
	}

	id := uuidV4()
	// --shell=none: no login shell between Rex and the child, so the environment is the parent's, as for the hidden child
	out, err := rexCLI(p.bin, append([]string{"split", "-s", p.session, "-b", p.parent, "--focus=false", "--shell=none", "--keep-open",
		"--cwd", req.Cwd, "--label", paneName(agent, randHex(3)), "--json", "--",
		"/bin/sh", "-c", `. "$0" && rm -f "$0" && exec "$@"`, envFile.Name(), req.Bin}, paneArgs(req.Args, dir, id, req.Stdin)...)...)
	if err != nil {
		return childResult{}, fmt.Errorf("rex: %w", err) // the child never started
	}
	var started struct {
		BlockIDs []string `json:"block_ids"`
	}
	if json.Unmarshal(out, &started) != nil || len(started.BlockIDs) == 0 || started.BlockIDs[0] == "" {
		return childResult{}, fmt.Errorf("rex started %s without a block; the child is unknown", agent)
	}
	block := started.BlockIDs[0]
	defer func() { _, _ = rexCLI(p.bin, "block", "close", "-s", p.session, block) }() // ends the child too

	// Rex counts no turns: the first turn has ended at done or error, or at idle once the child was seen working.
	var st string
	var finished bool
	var exit int
	turned, blocked := false, false
	for {
		select {
		case <-done:
			return childResult{}, errCancelled
		default:
		}
		if st, finished, exit, err = p.poll(block); err != nil {
			return childResult{Exit: 1, Stderr: fmt.Sprintf("Rex stopped answering while %s ran in %s: %v", agent, block, err)}, nil
		}
		if finished || st == "done" || st == "error" || st == "idle" && turned {
			break
		}
		turned = turned || st == "working" || st == "blocked"
		if st == "blocked" && !blocked {
			p.notify(fmt.Sprintf("%s needs you in its Rex pane (%s).", agent, block), "warning")
		}
		blocked = st == "blocked"
		select {
		case <-done:
			return childResult{}, errCancelled
		case <-time.After(panePoll):
		}
	}
	return paneAnswer(dir, id, agent, finished, exit, func() string {
		screen, _ := rexCLI(p.bin, "capture", "-s", p.session, "-b", block, "--trim")
		lines := strings.Split(strings.TrimRight(string(screen), "\n"), "\n")
		return strings.Join(lines[max(0, len(lines)-40):], "\n")
	}), nil
}
