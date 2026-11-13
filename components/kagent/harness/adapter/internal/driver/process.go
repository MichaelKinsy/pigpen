// Package driver runs `pig --mode rpc` once per kagent turn and translates its
// events into kagent's runtime vocabulary. No process outlives a turn: an
// approval request parks the turn by closing pig's stdin, and the pending
// record in the durable directory carries the rest (see pending.go).
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/fsutil"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/session"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// ContinuationPrompt is the one line of non-native text the model sees after an
// approval or answer: PiG's RPC has no "continue from a tool result" command.
const ContinuationPrompt = "Continue."

// Environment names the pig process may inherit. Nothing else from the adapter's
// environment reaches pig, its extensions or the bash tool: in particular never
// the bootstrap descriptor.
var inheritedEnv = []string{
	"PATH", "HOME", "TZ", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "USER", "LOGNAME", "SHELL",
	// Egress trust bundle (set by the ActorTemplate; Harness env cannot override them).
	"SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "AWS_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy",
}

// Config holds the driver's inputs.
type Config struct {
	// Executable is the pig binary (a fused Piglet Binary carrying the policy extension).
	Executable string
	// Workspace is the fixed cwd of every pig process. PiG's session lookup is cwd-scoped.
	Workspace string
	// SessionDir is pig's --session-dir (durable).
	SessionDir string
	// PigHome is the ephemeral PIG_HOME: models.json, policy and prompt file. Never inside /data.
	PigHome string
	// AdapterDir is <DurableDir>/adapter, where the pending record lives.
	AdapterDir string
	Compiled   *config.Compiled
	// Environment is the adapter's own environment (os.Environ()).
	Environment []string
	// PassEnv lists extra variable names to inherit (KAGENT_PIG_PASS_ENV).
	PassEnv []string
	// CABundle, when set, replaces the CA file variables handed to pig and its
	// tools with a bundle of system roots plus the egress trust bundle. The
	// adapter's own MCP client uses it as its root set too.
	CABundle       string
	MaxStderrBytes int
	// Grace bounds each step of shutdown: stdin close, SIGTERM, SIGKILL.
	Grace time.Duration
	// ReapTimeout bounds waiting for a killed process to be reaped.
	ReapTimeout time.Duration
	Tools       ToolCaller
	// Tasks tells RestorePending whether kagent still holds a restored parked task open. Nil leaves every restored
	// record parked, which is what a Substrate restore of an undisturbed Actor needs.
	Tasks TaskStates
	// Logger receives the restore's explanations; nil means slog.Default().
	Logger *slog.Logger
	Now    func() time.Time
}

// Driver implements runtime.Runner and the executor's PendingRestorer.
type Driver struct {
	cfg     Config
	journal *journal.Store
	// crash is a test seam: it is called at each durable step boundary of
	// Resume so tests can simulate a process death there. It is nil in production.
	crash func(point string)
}

func (d *Driver) point(name string) {
	if d.crash != nil {
		d.crash(name)
	}
}

// New validates the configuration and returns a driver.
func New(cfg Config) (*Driver, error) {
	if cfg.Executable == "" || cfg.Compiled == nil || cfg.Tools == nil {
		return nil, errors.New("driver needs an executable, a compiled configuration and a tool caller")
	}
	for name, path := range map[string]string{"workspace": cfg.Workspace, "session dir": cfg.SessionDir, "PIG_HOME": cfg.PigHome, "adapter dir": cfg.AdapterDir} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s must be an absolute path", name)
		}
	}
	if cfg.MaxStderrBytes <= 0 {
		cfg.MaxStderrBytes = 16 << 10
	}
	if cfg.Grace <= 0 {
		cfg.Grace = 5 * time.Second
	}
	if cfg.ReapTimeout <= 0 {
		cfg.ReapTimeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Driver{cfg: cfg, journal: journal.Open(cfg.AdapterDir)}, nil
}

// Validate checks that the pig binary starts and reports a version.
func (d *Driver) Validate(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, d.cfg.Executable, "version")
	command.Env = d.pigEnv()
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("run %q version: %w", d.cfg.Executable, err)
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(output)), "\n", "; ")), " "), nil
}

// pigArgs are the per-turn arguments. The policy extension comes from the
// Piglet Binary, so there is no -e.
func (d *Driver) pigArgs(sessionID string) []string {
	c := d.cfg.Compiled
	args := []string{
		"--mode", "rpc", "--offline", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--tools", c.ToolsArg(), "--provider", c.Provider, "--model", c.Model,
		"--session-dir", d.cfg.SessionDir, "--session-id", sessionID,
	}
	if c.Thinking != "" {
		args = append(args, "--thinking", c.Thinking)
	}
	if c.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", d.promptPath())
	}
	return args
}

func (d *Driver) promptPath() string { return filepath.Join(d.cfg.PigHome, "system-prompt.md") }
func (d *Driver) policyPath() string { return filepath.Join(d.cfg.PigHome, "kagent-policy.json") }

// pigEnv builds pig's environment from an allowlist plus generated values.
func (d *Driver) pigEnv() []string {
	have := map[string]string{}
	for _, entry := range d.cfg.Environment {
		if name, value, ok := strings.Cut(entry, "="); ok {
			have[name] = value
		}
	}
	var env []string
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, inheritedEnv...), d.cfg.PassEnv...) {
		if value, ok := have[name]; ok && !seen[name] {
			seen[name] = true
			env = append(env, name+"="+value)
		}
	}
	env = slices.DeleteFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "PATH=") })
	env = append(env, "PATH="+systemFirstPath(have["PATH"], filepath.Dir(d.cfg.AdapterDir)))
	if d.cfg.CABundle != "" {
		env = slices.DeleteFunc(env, func(entry string) bool {
			name, _, _ := strings.Cut(entry, "=")
			return slices.Contains(caEnvNames, name)
		})
		for _, name := range caEnvNames {
			env = append(env, name+"="+d.cfg.CABundle)
		}
	}
	env = append(env, "PIG_HOME="+d.cfg.PigHome, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "KAGENT_PIG_POLICY="+d.policyPath())
	if d.cfg.Compiled != nil {
		for _, v := range d.cfg.Compiled.Env {
			env = append(env, v.Name+"="+v.Value)
		}
	}
	return env
}

// systemDirs lead the PATH of pig and its tools.
var systemDirs = []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin"}

// systemFirstPath orders PATH explicitly: the system directories first, then the rest of the inherited entries,
// and the entries under the durable data directory last. The model's bash tool can write into the data directory,
// so a file it plants there must never shadow a system binary. Empty and relative entries (which mean the working
// directory, the workspace) are dropped.
func systemFirstPath(inherited, dataDir string) string {
	var system, data []string
	seen := map[string]bool{}
	add := func(dir string) {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		if dir == dataDir || strings.HasPrefix(dir, dataDir+string(filepath.Separator)) {
			data = append(data, dir)
		} else {
			system = append(system, dir)
		}
	}
	for _, dir := range systemDirs {
		add(dir)
	}
	for _, dir := range filepath.SplitList(inherited) {
		if filepath.IsAbs(dir) {
			add(dir)
		}
	}
	return strings.Join(append(system, data...), string(filepath.ListSeparator))
}

// headerValues returns the KAGENT_PIG_HDR_n values for the adapter's own MCP calls.
func HeaderValues(c *config.Compiled) map[string]string {
	values := map[string]string{}
	for _, v := range c.Env {
		if strings.HasPrefix(v.Name, config.HeaderEnvPrefix) {
			values[v.Name] = v.Value
		}
	}
	return values
}

// prepare writes the ephemeral files pig reads. They are regenerated each turn
// and never enter /data.
func (d *Driver) prepare() error {
	if err := os.MkdirAll(d.cfg.Workspace, 0o755); err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}
	if err := fsutil.EnsurePrivateDir(d.cfg.SessionDir); err != nil {
		return err
	}
	// The agent directory is PiG's user configuration root. The model can write to it with its own tools
	// (mcp.json would add servers or widen an allowlist, settings.json and trust decisions change what PiG loads),
	// so nothing a previous turn left there is kept: it is rebuilt from the compiled configuration every turn.
	agentDir := filepath.Join(d.cfg.PigHome, "agent")
	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("reset PIG_HOME/agent: %w", err)
	}
	if err := fsutil.EnsurePrivateDir(agentDir); err != nil {
		return err
	}
	c := d.cfg.Compiled
	files := map[string][]byte{
		filepath.Join(d.cfg.PigHome, "agent", "models.json"): c.ModelsJSON,
		d.policyPath(): c.PolicyJSON,
	}
	if c.SystemPrompt != "" {
		files[d.promptPath()] = []byte(c.SystemPrompt)
	}
	for path, contents := range files {
		if err := fsutil.ReplacePrivateFile(path, contents); err != nil {
			return err
		}
	}
	return nil
}

// proc is one running pig process.
type proc struct {
	cmd    *exec.Cmd
	rpc    *rpcClient
	exited chan error
	stderr *fsutil.TailBuffer
	d      *Driver

	stopOnce sync.Once
	stopErr  error

	// drainMu serialises the frame callback of stop with the caller: once stop returns, the callback never runs again.
	drainMu sync.Mutex
	drained bool
}

func (d *Driver) start(sessionID string) (*proc, error) {
	command := exec.Command(d.cfg.Executable, d.pigArgs(sessionID)...)
	command.Dir, command.Env = d.cfg.Workspace, d.pigEnv()
	fsutil.ConfigureProcessGroup(command)
	stderr := fsutil.NewTailBuffer(d.cfg.MaxStderrBytes)
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", d.cfg.Executable, err)
	}
	if err := fsutil.AttachProcessGroup(command.Process); err != nil {
		_ = command.Wait()
		return nil, fmt.Errorf("start %q: %w", d.cfg.Executable, err)
	}
	p := &proc{cmd: command, exited: make(chan error, 1), stderr: stderr, d: d}
	p.rpc = newRPCClient(stdin, stdout, MaxFrameBytes)
	go func() {
		<-p.rpc.done // Wait closes the stdout pipe; let the reader consume the final records first.
		p.exited <- command.Wait()
		close(p.exited)
	}()
	return p, nil
}

// stop closes stdin and waits for exit, escalating to SIGTERM and SIGKILL of the
// process group. It returns an error only when the process could not be reaped,
// which is the one case where cancellation cleanup is unsafe. Frames pig writes
// while it shuts down are handed to onFrame (when set), which is how usage that
// arrives after the cancel grace still reaches the sink; stop waits for them
// (bounded by ReapTimeout once the process is gone), and onFrame never runs after
// stop returns. Only the first call's onFrame is used.
func (p *proc) stop(onFrame func(frame)) error {
	p.stopOnce.Do(func() {
		p.rpc.closeStdin()
		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
			for f := range p.rpc.frames { // keep the reader unblocked so Wait can finish
				p.drainMu.Lock()
				if onFrame != nil && !p.drained && f.err == nil {
					onFrame(f)
				}
				p.drainMu.Unlock()
			}
		}()
		grace := p.d.cfg.Grace
		select {
		case <-p.exited:
		case <-time.After(grace):
			_ = fsutil.TerminateProcessGroup(p.cmd.Process)
			select {
			case <-p.exited:
			case <-time.After(grace):
				if err := fsutil.KillProcessGroup(p.cmd.Process); err != nil && !errors.Is(err, syscall.ESRCH) {
					p.stopErr = fmt.Errorf("kill pig process group: %w", err)
				}
				select {
				case <-p.exited:
				case <-time.After(p.d.cfg.ReapTimeout):
					p.stopErr = errors.Join(p.stopErr, errors.New("pig did not exit after SIGKILL"))
				}
			}
		}
		// A tool pig started can outlive it.
		_ = fsutil.KillProcessGroup(p.cmd.Process)
		select {
		case <-drainDone:
		case <-time.After(p.d.cfg.ReapTimeout):
		}
		p.drainMu.Lock()
		p.drained = true
		p.drainMu.Unlock()
	})
	return p.stopErr
}

// notStartedError proves the prompt never reached pig, so a retry is safe.
type notStartedError struct{ err error }

func (e *notStartedError) Error() string { return e.err.Error() }
func (e *notStartedError) Unwrap() error { return e.err }

// spec is one pig invocation.
type spec struct {
	taskID, contextID string
	origin            runtime.TraceOrigin
	sessionID         string
	prompt            string
	// afterAccept runs once pig has accepted the prompt (a retry is no longer safe).
	afterAccept func() error
}

// Run implements runtime.Runner.
func (d *Driver) Run(ctx context.Context, turn runtime.Turn, sink runtime.EventSink) (runtime.Outcome, error) {
	if strings.TrimSpace(turn.Prompt) == "" {
		return runtime.Outcome{}, errors.New("a prompt is required")
	}
	id := turn.ContinuationID
	if id == "" {
		id = uuid.NewString()
	}
	return d.run(ctx, spec{taskID: turn.TaskID, contextID: turn.ContextID, origin: turn.Origin, sessionID: id, prompt: turn.Prompt}, sink)
}

type stateData struct {
	Model *struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"model"`
	SessionID string `json:"sessionId"`
}

func (d *Driver) run(ctx context.Context, sp spec, sink runtime.EventSink) (outcome runtime.Outcome, returnErr error) {
	dispatched := false
	defer func() {
		if !dispatched && returnErr != nil {
			returnErr = &notStartedError{err: returnErr}
		}
	}()
	if _, found, err := session.Find(d.cfg.SessionDir, sp.sessionID); err != nil {
		return runtime.Outcome{}, err
	} else if !found && sp.afterAccept != nil {
		return runtime.Outcome{}, errors.New("the parked PiG session file is missing")
	}
	if err := d.prepare(); err != nil {
		return runtime.Outcome{}, err
	}
	p, err := d.start(sp.sessionID)
	if err != nil {
		return runtime.Outcome{}, err
	}
	defer func() {
		if err := p.stop(nil); err != nil && returnErr == nil {
			returnErr = &runtime.CancellationError{Err: err}
		}
	}()
	if err := d.verifyState(ctx, p, sp.sessionID); err != nil {
		return runtime.Outcome{}, err
	}
	if err := sink.SessionStarted(runtime.SessionStarted{ContinuationID: sp.sessionID}); err != nil {
		return runtime.Outcome{}, err
	}
	dispatched = true
	if err := p.rpc.send(map[string]any{"id": "prompt", "type": "prompt", "message": sp.prompt}); err != nil {
		dispatched = false
		return runtime.Outcome{}, err
	}
	tr := newTranslator(sink, d.cfg.Compiled.Policy.MCPServers, d.cfg.Now)
	defer func() {
		if outcome.Pending == nil {
			// Whatever ended the turn (cancel, a crash, a protocol error), a call announced without a result is
			// closed so the A2A tool artifact does not show it running forever.
			tr.abandonOpen()
		}
	}()
	return d.consume(ctx, p, sp, tr, sink)
}

// verifyState confirms pig resumed the intended session on the intended model.
// A different session id means PiG's cwd-scoped lookup forked the history; a
// different model means it silently fell back.
func (d *Driver) verifyState(ctx context.Context, p *proc, sessionID string) error {
	if err := p.rpc.send(map[string]any{"id": "state", "type": "get_state"}); err != nil {
		return d.protocolError(p, err)
	}
	timeout := time.After(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return errors.New("PiG did not answer get_state")
		case f, ok := <-p.rpc.frames:
			if !ok {
				return d.protocolError(p, errors.New("PiG exited before answering get_state"))
			}
			if f.err != nil {
				return d.protocolError(p, f.err)
			}
			if f.kind != "response" {
				continue
			}
			var rec responseRecord
			if err := json.Unmarshal(f.raw, &rec); err != nil || rec.ID != "state" {
				continue
			}
			var data stateData
			if !rec.Success || json.Unmarshal(rec.Data, &data) != nil || data.Model == nil {
				return errors.New("PiG get_state failed")
			}
			c := d.cfg.Compiled
			if data.Model.ID != c.Model || data.Model.Provider != c.Provider {
				return fmt.Errorf("PiG selected model %s/%s instead of %s/%s", data.Model.Provider, data.Model.ID, c.Provider, c.Model)
			}
			if data.SessionID != sessionID {
				return fmt.Errorf("PiG opened session %q instead of %q; the session lookup did not match", data.SessionID, sessionID)
			}
			return nil
		}
	}
}

func (d *Driver) protocolError(p *proc, err error) error {
	if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
		return fmt.Errorf("%w: %s", err, tail)
	}
	return err
}

func (d *Driver) consume(ctx context.Context, p *proc, sp spec, tr *translator, sink runtime.EventSink) (runtime.Outcome, error) {
	accepted := false
	for {
		select {
		case <-ctx.Done():
			return d.cancel(ctx, p, tr)
		case f, ok := <-p.rpc.frames:
			if !ok {
				return runtime.Outcome{}, d.protocolError(p, errors.New("PiG exited before the turn settled"))
			}
			if f.err != nil {
				return runtime.Outcome{}, d.protocolError(p, f.err)
			}
			switch f.kind {
			case "response":
				var rec responseRecord
				if json.Unmarshal(f.raw, &rec) != nil || rec.ID != "prompt" {
					continue
				}
				if !rec.Success {
					return runtime.Outcome{Failure: &runtime.Failure{Message: failRejected}}, nil
				}
				accepted = true
				if sp.afterAccept != nil {
					if err := sp.afterAccept(); err != nil {
						return runtime.Outcome{}, err
					}
				}
			case "extension_ui_request":
				outcome, handled, err := d.handleUI(ctx, p, sp, tr, f.raw)
				if err != nil || handled {
					return outcome, err
				}
			default:
				done, err := tr.handle(f.kind, f.raw)
				if err != nil {
					return runtime.Outcome{}, err
				}
				if done {
					if !accepted {
						return runtime.Outcome{}, errors.New("PiG settled before accepting the prompt")
					}
					if err := p.stop(nil); err != nil {
						return runtime.Outcome{}, &runtime.CancellationError{Err: err}
					}
					return tr.outcome(), nil
				}
			}
		}
	}
}

// cancel aborts the turn over RPC, lets pig flush its final usage, then reaps it.
func (d *Driver) cancel(ctx context.Context, p *proc, tr *translator) (runtime.Outcome, error) {
	tr.canceling = true
	_ = p.rpc.send(map[string]any{"id": "abort", "type": "abort"})
	timer := time.NewTimer(d.cfg.Grace)
	defer timer.Stop()
wait:
	for {
		select {
		case <-timer.C:
			break wait
		case f, ok := <-p.rpc.frames:
			if !ok || f.err != nil {
				break wait
			}
			if f.kind == "agent_settled" {
				break wait
			}
			if f.kind == "extension_ui_request" {
				continue
			}
			if _, err := tr.handle(f.kind, f.raw); err != nil {
				break wait
			}
		}
	}
	// Usage pig writes after the grace (while stdin closes, or from the kill's last breath) is still reported: the
	// contract wants every ModelCall the harness emitted. Text and tool events stay suppressed while canceling.
	if err := p.stop(func(f frame) { _, _ = tr.handle(f.kind, f.raw) }); err != nil {
		return runtime.Outcome{}, &runtime.CancellationError{Err: err}
	}
	return runtime.Outcome{}, ctx.Err()
}

// respondCancelled answers a blocking dialog the adapter cannot satisfy.
func respondCancelled(p *proc, id string) {
	_ = p.rpc.send(map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
}
