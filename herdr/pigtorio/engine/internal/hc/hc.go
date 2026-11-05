// Package hc wraps the `hc` command line client of herdr-core.
package hc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Term is one terminal as `hc ls --json` / `hc get --json` describe it.
type Term struct {
	Address string
	Name    string
	Cwd     string
	Agent   string
	State   string // live | finished
	Status  string // working, idle, done, blocked, "" (no status reported)
	Turn    int
	Argv    []string // only filled by Get
	// StatusMs is when the status last changed; only `hc get` reports it.
	StatusMs int64
}

// ErrBlocked is returned by Send when the terminal is blocked on a dialog.
var ErrBlocked = errors.New("terminal is blocked")

// ErrNotReady is returned by Send when its ifStatus condition did not hold: nothing was typed.
var ErrNotReady = errors.New("terminal is not in the wanted status")

// Client is what the engine needs from herdr-core.
type Client interface {
	List() ([]Term, error)
	Get(addr string) (Term, error)
	// Send pastes text and presses Enter. With ifStatus "idle" hc types only if the status is
	// exactly that and returns ErrNotReady otherwise.
	Send(addr, text, ifStatus string) error
	Read(addr string, lines int) (string, error)
	// Follow blocks while `hc events --json` runs and calls wake for every event line.
	Follow(ctx context.Context, wake func()) error
}

// CLI runs the hc binary.
type CLI struct {
	Bin string
	Env []string
}

// Find locates hc on PATH or in ~/.local/bin.
func Find() (string, error) {
	if p, err := exec.LookPath("hc"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "hc")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("hc not found on PATH or in ~/.local/bin")
}

// Environ returns the environment for hc: the inherited one, plus HC_SOCKET
// taken from HC_TERMINAL_SOCKET when only the latter is set (a terminal started
// by the core carries the core's socket there).
func Environ(base []string) []string {
	env := append([]string(nil), base...)
	get := func(k string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], k+"="); ok {
				return v
			}
		}
		return ""
	}
	if get("HC_SOCKET") == "" {
		if s := get("HC_TERMINAL_SOCKET"); s != "" {
			env = append(env, "HC_SOCKET="+s)
		}
	}
	return env
}

// NewCLI finds hc and captures the inherited environment.
func NewCLI() (*CLI, error) {
	bin, err := Find()
	if err != nil {
		return nil, err
	}
	return &CLI{Bin: bin, Env: Environ(os.Environ())}, nil
}

func (c *CLI) cmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Env = c.Env
	return cmd
}

func (c *CLI) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := c.cmd(ctx, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// hc reports API errors as JSON on stdout with a non-zero exit.
		var e struct {
			Error struct {
				Kind    string `json:"kind"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error.Message != "" {
			switch e.Error.Kind {
			case "blocked":
				return out, fmt.Errorf("%w: %s", ErrBlocked, e.Error.Message)
			case "status_mismatch":
				return out, fmt.Errorf("%w: %s", ErrNotReady, e.Error.Message)
			}
			return out, fmt.Errorf("hc %s: %s", args[0], e.Error.Message)
		}
		return out, fmt.Errorf("hc %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type wireTerm struct {
	Address  string          `json:"address"`
	Name     string          `json:"name"`
	Cwd      string          `json:"cwd"`
	Agent    string          `json:"agent"`
	State    string          `json:"state"`
	Status   json.RawMessage `json:"status"`
	Turn     int             `json:"turn"`
	Argv     []string        `json:"argv"`
	StatusMs *int64          `json:"status_ms"`
}

// status is a string in `ls` and an object {app,state,...} in `get`.
func statusOf(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.State
	}
	return ""
}

func (w wireTerm) term() Term {
	t := Term{Address: w.Address, Name: w.Name, Cwd: w.Cwd, Agent: w.Agent, State: w.State, Status: statusOf(w.Status), Turn: w.Turn, Argv: w.Argv}
	if w.StatusMs != nil {
		t.StatusMs = *w.StatusMs
	}
	return t
}

// ParseList parses `hc ls --json` output, keeping live terminals only.
func ParseList(out []byte) ([]Term, error) {
	var v struct {
		Terminals []wireTerm `json:"terminals"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("parse hc ls: %w", err)
	}
	terms := make([]Term, 0, len(v.Terminals))
	for _, w := range v.Terminals {
		t := w.term()
		if t.State != "" && t.State != "live" {
			continue
		}
		terms = append(terms, t)
	}
	return terms, nil
}

func (c *CLI) List() ([]Term, error) {
	out, err := c.run("ls", "--json")
	if err != nil {
		return nil, err
	}
	return ParseList(out)
}

func (c *CLI) Get(addr string) (Term, error) {
	out, err := c.run("get", addr, "--json")
	if err != nil {
		return Term{}, err
	}
	var w wireTerm
	if err := json.Unmarshal(out, &w); err != nil {
		return Term{}, fmt.Errorf("parse hc get: %w", err)
	}
	return w.term(), nil
}

func (c *CLI) Send(addr, text, ifStatus string) error {
	args := []string{"send", "--json"}
	if ifStatus != "" {
		args = append(args, "--if", ifStatus)
	}
	_, err := c.run(append(args, addr, "--", text)...)
	return err
}

func (c *CLI) Read(addr string, lines int) (string, error) {
	out, err := c.run("read", addr, "--lines", fmt.Sprint(lines))
	return string(out), err
}

func (c *CLI) Follow(ctx context.Context, wake func()) error {
	cmd := c.cmd(ctx, "events", "--json")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		_, err := r.ReadSlice('\n')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				_ = cmd.Wait()
				return err
			}
			break
		}
		wake()
	}
	return cmd.Wait()
}
