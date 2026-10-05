package eq

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed driver/eq-driver.ts
var driverSource []byte

// Lane is one (host, extension) pair. Host is "pi" or "pig".
type Lane struct {
	Name string // for example pi-ts, pig-ts, pig-go
	Host string
	Bin  string // path to the pi or pig executable
	Ext  string // a TypeScript file, or a Go extension directory
}

// Options tunes a run.
type Options struct {
	// StartupTimeout bounds host start, including a Go extension's first build.
	StartupTimeout time.Duration
	// StepTimeout bounds each scenario step.
	StepTimeout time.Duration
	// Keep leaves the run directory in place and reports it on Stderr.
	Keep bool
	// Stderr receives progress and, on failure, the host's stderr tail.
	Stderr io.Writer

	// baseline, when set, makes the lane a baseline run that captures the
	// host's system text instead of comparing to it.
	baseline *baselineCapture
}

func (o Options) startup() time.Duration {
	if o.StartupTimeout > 0 {
		return o.StartupTimeout
	}
	return 5 * time.Minute
}

func (o Options) step() time.Duration {
	if o.StepTimeout > 0 {
		return o.StepTimeout
	}
	return 60 * time.Second
}

// toolchain is the part of the caller's environment lanes need: the node and
// go executables. Everything else is dropped so a lane never sees the caller's
// HOME, credentials or configuration. The executables are linked into a
// directory of their own rather than adding their directories to PATH: node
// often lives in /usr/bin next to git, and adding that directory back would
// undo a scenario's "missing" command.
type toolchain struct {
	env   []string          // GO* variables
	tools map[string]string // executable name -> absolute path (node, go)
}

// toolchainVars are the Go variables a lane inherits from the caller (detectToolchain).
var toolchainVars = []string{"GOROOT", "GOCACHE", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOSUMDB", "GOTOOLCHAIN"}

// harnessEnv reports whether the harness sets name for every lane (or owns its prefix), so a
// scenario's env must not: exec keeps the last duplicate, and a scenario variable would silently
// replace the hermetic value. GOWORK and GOENV would change how PiG builds the port.
func harnessEnv(name string) bool {
	switch name {
	case "PATH", "HOME", "TMPDIR", "LANG", "TERM", "NO_COLOR", "GOWORK", "GOENV":
		return true
	}
	for _, prefix := range []string{"EQ_", "PIG_", "PI_", "GIT_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return slices.Contains(toolchainVars, name)
}

var (
	tcOnce sync.Once
	tcVal  toolchain
)

func detectToolchain() toolchain {
	tcOnce.Do(func() {
		tcVal.tools = map[string]string{}
		if out, err := exec.Command("node", "-p", "process.execPath").Output(); err == nil {
			tcVal.tools["node"] = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("go", append([]string{"env", "-json"}, toolchainVars...)...).Output(); err == nil {
			var vars map[string]string
			if json.Unmarshal(out, &vars) == nil {
				for k, v := range vars {
					if v != "" {
						tcVal.env = append(tcVal.env, k+"="+v)
					}
				}
				slices.Sort(tcVal.env)
				if root := vars["GOROOT"]; root != "" {
					tcVal.tools["go"] = filepath.Join(root, "bin", "go")
				}
			}
		}
	})
	return tcVal
}

type recorder struct {
	mu     sync.Mutex
	step   string
	events []Event
	norm   *Normalizer
}

func (r *recorder) setStep(s string) {
	r.mu.Lock()
	r.step = s
	r.mu.Unlock()
}

func (r *recorder) add(ch string, data any) {
	raw := r.norm.Data(data)
	r.mu.Lock()
	r.events = append(r.events, Event{Step: r.step, Ch: ch, Data: raw})
	r.mu.Unlock()
}

func (r *recorder) addRaw(ch string, raw json.RawMessage) {
	r.mu.Lock()
	r.events = append(r.events, Event{Step: r.step, Ch: ch, Data: raw})
	r.mu.Unlock()
}

func hashExtension(path string) (string, error) {
	h := sha256.New()
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		h.Write(b)
		return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
	}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(path, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
		return nil
	})
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), err
}

func hostVersion(bin string) string {
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

func hermeticGit() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=eq", "GIT_AUTHOR_EMAIL=eq@example.invalid", "GIT_COMMITTER_NAME=eq", "GIT_COMMITTER_EMAIL=eq@example.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	}
}

// RunLane executes the scenario against one lane and returns its trace. A
// failure of the harness or the host is returned as an error; failures the run
// itself observed (for example an unanswered dialog) are ChError events.
func RunLane(sc *Scenario, lane Lane, opt Options) (*Trace, error) {
	if lane.Host != "pi" && lane.Host != "pig" {
		return nil, fmt.Errorf("lane %s: host must be pi or pig", lane.Name)
	}
	extHash, extAbs := "none", ""
	if lane.Ext != "" {
		h, err := hashExtension(lane.Ext)
		if err != nil {
			return nil, fmt.Errorf("lane %s: extension: %w", lane.Name, err)
		}
		abs, err := filepath.Abs(lane.Ext)
		if err != nil {
			return nil, err
		}
		extHash, extAbs = h, abs
	}
	// The host's own system text, to which the model-visible system text of
	// this lane is compared (systemDelta). A baseline lane captures it instead.
	var baseSystem string
	if len(sc.LLM) > 0 && opt.baseline == nil {
		b, err := baselineSystem(lane, sc.Args, opt)
		if err != nil {
			return nil, fmt.Errorf("lane %s: baseline system prompt: %w", lane.Name, err)
		}
		baseSystem = b
	}
	// The run root is deliberately short: Unix socket paths are limited.
	root, err := os.MkdirTemp("", "eq")
	if err != nil {
		return nil, err
	}
	if opt.Keep {
		if opt.Stderr != nil {
			fmt.Fprintf(opt.Stderr, "eq: keeping %s\n", root)
		}
	} else {
		defer os.RemoveAll(root)
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	cwd := filepath.Join(root, "work")
	home := filepath.Join(root, "home")
	agent := filepath.Join(root, "agent")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{cwd, home, agent, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	rec := &recorder{step: "00-startup", norm: NewNormalizer(cwd, root, home)}
	tc := detectToolchain()
	origPath := os.Getenv("PATH")

	// Fake upstream servers start before the workspace is written: files and env refer to them.
	ups, err := startUpstreams(sc.Servers, rec.add)
	if err != nil {
		return nil, err
	}
	defer ups.close()
	ups.normalize(rec.norm)

	if err := writeAgentFiles(agent, sc.AgentFiles, ups); err != nil {
		return nil, err
	}
	for rel, content := range sc.Files {
		content = ups.expand(content)
		p := filepath.Join(cwd, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	baseEnv := append([]string{"PATH=" + origPath, "HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8"}, hermeticGit()...)
	for _, argv := range sc.Setup {
		if len(argv) == 0 {
			return nil, errors.New("empty setup command")
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = cwd, baseEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("setup %v: %v\n%s", argv, err, out)
		}
	}

	var missing []string
	for name, spec := range sc.Commands {
		if spec.Mode == "missing" {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	shims, err := newShimSet(root, sc.Commands, origPath, rec.add)
	if err != nil {
		return nil, err
	}
	defer shims.close()

	args := []string{"--mode", "rpc", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--offline"}
	if len(sc.LLM) > 0 {
		llm, err := newFakeLLM(sc.LLM, rec.add)
		if err != nil {
			return nil, err
		}
		defer llm.close()
		if capture := opt.baseline; capture != nil {
			llm.system = func(text string) (any, bool) {
				capture.once.Do(func() { capture.text = rec.norm.str(text) })
				return nil, false
			}
		} else {
			llm.system = func(text string) (any, bool) { return systemDelta(baseSystem, rec.norm.str(text)) }
		}
		models := map[string]any{"providers": map[string]any{"eq-llm": map[string]any{
			"baseUrl": llm.baseURL(), "api": "openai-completions", "apiKey": "eq-key",
			"models": []any{map[string]any{
				"id": "eq-1", "name": "eq-1", "reasoning": false, "input": []string{"text"},
				"contextWindow": 100000, "maxTokens": 4096,
				"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			}},
		}}}
		b, _ := json.Marshal(models)
		if err := os.WriteFile(filepath.Join(agent, "models.json"), b, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--provider", "eq-llm", "--model", "eq-1")
	}
	if extAbs != "" {
		args = append(args, "-e", extAbs)
	}
	if sc.usesDriver() {
		driverPath := filepath.Join(root, "eq-driver.ts")
		if err := os.WriteFile(driverPath, driverSource, 0o644); err != nil {
			return nil, err
		}
		args = append(args, "-e", driverPath)
	}
	for _, a := range sc.Args {
		args = append(args, ups.expand(a))
	}

	toolbin := filepath.Join(root, "toolbin")
	if err := os.MkdirAll(toolbin, 0o755); err != nil {
		return nil, err
	}
	for name, target := range tc.tools {
		if err := os.Symlink(target, filepath.Join(toolbin, name)); err != nil {
			return nil, err
		}
	}
	pathParts := []string{shims.dir}
	if p := pathWithout(origPath, missing); p != "" {
		pathParts = append(pathParts, p)
	}
	pathParts = append(pathParts, toolbin)
	lanePath := strings.Join(pathParts, string(filepath.ListSeparator))
	// A "missing" command must really be missing, or the scenario tests nothing.
	for _, name := range missing {
		if found, err := lookPath(name, lanePath); err == nil {
			return nil, fmt.Errorf("scenario %s: command %q is mode missing but the lane's PATH still finds %s (it is also the host's node or go?)", sc.Name, name, found)
		}
	}
	env := append([]string{
		"PATH=" + lanePath,
		"HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8", "TERM=dumb", "NO_COLOR=1",
		"PIG_HOME=" + filepath.Join(root, "pighome"), "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent,
		"PI_SKIP_VERSION_CHECK=1", "PI_OFFLINE=1", "PI_TELEMETRY=0",
		"EQ_SHIM_SOCK=" + shims.sockPath,
	}, hermeticGit()...)
	env = append(env, tc.env...)
	envNames := make([]string, 0, len(sc.Env))
	for k := range sc.Env {
		envNames = append(envNames, k)
	}
	slices.Sort(envNames)
	for _, k := range envNames {
		env = append(env, k+"="+ups.expand(sc.Env[k]))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, lane.Bin, args...)
	cmd.Dir, cmd.Env = cwd, env
	var stderrBuf tailBuffer
	cmd.Stderr = &stderrBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", lane.Bin, err)
	}
	d := &driver{sc: sc, rec: rec, stdin: stdin, events: make(chan map[string]any, 1<<16), silent: map[string]bool{}, capture: sc.capture()}
	readerDone := make(chan struct{})
	go d.read(stdout, readerDone)
	// The host's exit is read by the driver's waits and by the shutdown below, so it is a closed channel
	// plus the stored error, not a value one reader can take from the other (a lane used to hang forever).
	exited := &hostExit{done: make(chan struct{})}
	go func() { exited.err = cmd.Wait(); close(exited.done) }()

	fail := func(msg string) {
		rec.add(ChError, map[string]any{"message": msg})
	}
	runErr := d.run(opt, exited, &stderrBuf)
	if runErr != nil {
		fail(runErr.Error())
	}
	// Shut the host down: closing stdin ends an RPC session, and shutdown handlers run.
	rec.setStep("99-shutdown")
	_ = stdin.Close()
	select {
	case <-exited.done:
		err := exited.err
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		<-readerDone
		rec.add(ChExit, map[string]any{"code": code})
	case <-time.After(30 * time.Second):
		cancel()
		<-exited.done
		<-readerDone
		fail("host did not exit within 30s of stdin close")
	}
	if runErr != nil && opt.Stderr != nil {
		fmt.Fprintf(opt.Stderr, "eq: lane %s host stderr tail:\n%s\n", lane.Name, stderrBuf.String())
	}
	return &Trace{
		Header: Header{Kind: "header", Scenario: sc.Name, Lane: lane.Name, Host: lane.Host + " " + hostVersion(lane.Bin), Extension: extHash, Normalizer: NormalizerVersion},
		Events: rec.events,
	}, nil
}

// baselineCapture receives the first system text a baseline lane sends to the model.
type baselineCapture struct {
	once sync.Once
	text string
}

var (
	baselineMu    sync.Mutex
	baselineCache = map[string]string{}
)

// baselineSystem returns the normalized system text the lane's host sends with
// the scenario's arguments and no extension under test. It is computed once per
// host executable and argument list. When the arguments need the extension (a
// flag it registers), the host refuses them without it, and the baseline is
// taken without the scenario's arguments.
func baselineSystem(lane Lane, args []string, opt Options) (string, error) {
	key := lane.Host + "\x00" + lane.Bin + "\x00" + strings.Join(args, "\x00")
	baselineMu.Lock()
	defer baselineMu.Unlock()
	if text, ok := baselineCache[key]; ok {
		return text, nil
	}
	attempt := func(args []string) (string, error) {
		sc := &Scenario{
			Name: "baseline", LLM: []Turn{{Text: "ok"}}, Args: args, TailMs: -1,
			Steps: []Step{{Name: "baseline", RPC: map[string]any{"type": "prompt", "message": "baseline"}}},
		}
		capture := &baselineCapture{}
		o := opt
		o.baseline, o.Stderr, o.Keep = capture, nil, false
		t, err := RunLane(sc, Lane{Name: lane.Host + "-baseline", Host: lane.Host, Bin: lane.Bin}, o)
		if err != nil {
			return "", err
		}
		if e, failed := t.Failed(); failed {
			return "", fmt.Errorf("%s", e.Data)
		}
		captured := false
		for _, e := range t.Events {
			if e.Ch == ChLLM {
				captured = true
			}
		}
		if !captured {
			return "", errors.New("the host never called the model")
		}
		return capture.text, nil
	}
	text, err := attempt(args)
	if err != nil && len(args) > 0 {
		text, err = attempt(nil)
	}
	if err != nil {
		return "", err
	}
	baselineCache[key] = text
	return text, nil
}

type driver struct {
	sc      *Scenario
	rec     *recorder
	stdin   io.Writer
	events  chan map[string]any
	capture []string

	mu     sync.Mutex
	silent map[string]bool // barrier response ids, kept out of the trace
	nextID int
}

func (d *driver) send(m map[string]any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err = d.stdin.Write(append(b, '\n'))
	return err
}

func (d *driver) newID(silent bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	id := fmt.Sprintf("eq-%d", d.nextID)
	if silent {
		d.silent[id] = true
	}
	return id
}

func (d *driver) isSilent(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.silent[id]
}

// read records host output in arrival order and forwards it to the driver.
func (d *driver) read(r io.Reader, done chan<- struct{}) {
	defer close(done)
	defer close(d.events)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		v, err := decodeAny(line)
		rec, ok := v.(map[string]any)
		if err != nil || !ok {
			continue // not an RPC record (for example a stray log line)
		}
		id, _ := rec["id"].(string)
		if typ, _ := rec["type"].(string); typ == "response" && d.isSilent(id) {
			// barrier response: forwarded, not recorded
		} else if ch, data, ok := d.rec.norm.ProjectHost(rec, d.capture); ok {
			d.rec.addRaw(ch, data)
		}
		d.events <- rec
	}
}

var dialogMethods = []string{"select", "confirm", "input", "editor"}

// run executes startup, every step, and returns the first failure.
func (d *driver) run(opt Options, exited *hostExit, stderr *tailBuffer) error {
	if err := d.barrier(opt.startup(), exited, stderr, nil); err != nil {
		return fmt.Errorf("host did not become ready: %w", err)
	}
	time.Sleep(time.Duration(d.sc.settle()) * time.Millisecond)
	for i, st := range d.sc.Steps {
		d.rec.setStep(st.label(i))
		answers := slices.Clone(st.UI)
		id := d.newID(false)
		cmd := map[string]any{"id": id}
		for k, v := range st.RPC {
			cmd[k] = v
		}
		if err := d.send(cmd); err != nil {
			return fmt.Errorf("step %s: send: %w", st.label(i), err)
		}
		if err := d.await(id, st.wait() == "agent_end", st.wait() == "ui", opt.step(), exited, stderr, &answers); err != nil {
			return fmt.Errorf("step %s: %w", st.label(i), err)
		}
		if err := d.barrier(opt.step(), exited, stderr, &answers); err != nil {
			return fmt.Errorf("step %s: barrier: %w", st.label(i), err)
		}
		time.Sleep(time.Duration(d.sc.stepSettle(st)) * time.Millisecond)
		if len(answers) > 0 {
			return fmt.Errorf("step %s: %d scripted dialog answer(s) were never requested", st.label(i), len(answers))
		}
	}
	// Final quiet period: effects the extension started without awaiting and
	// that land after the last step's settle time are still recorded (under
	// the last step) instead of being lost when the host shuts down. A dialog
	// raised now has no scripted answer and is recorded as an error.
	if tail := d.sc.tail(); tail > 0 {
		time.Sleep(time.Duration(tail) * time.Millisecond)
		if err := d.barrier(opt.step(), exited, stderr, nil); err != nil {
			return fmt.Errorf("final quiet period: %w", err)
		}
	}
	return nil
}

// barrier round-trips get_state so every effect the host emitted before it has
// been read, then returns.
func (d *driver) barrier(timeout time.Duration, exited *hostExit, stderr *tailBuffer, answers *[]UIAnswer) error {
	id := d.newID(true)
	if err := d.send(map[string]any{"id": id, "type": "get_state"}); err != nil {
		return err
	}
	return d.await(id, false, false, timeout, exited, stderr, answers)
}

// await reads host records until the response to id (and, when wantEnd, an
// agent_end) arrives, answering dialogs from the scripted queue.
func (d *driver) await(id string, wantEnd, wantUI bool, timeout time.Duration, exited *hostExit, stderr *tailBuffer, answers *[]UIAnswer) error {
	deadline := time.After(timeout)
	gotResponse, gotEnd := false, false
	for {
		select {
		case rec, ok := <-d.events:
			if !ok {
				return fmt.Errorf("host closed its output early (stderr tail: %s)", stderr.String())
			}
			typ, _ := rec["type"].(string)
			switch typ {
			case "response":
				if rid, _ := rec["id"].(string); rid == id {
					gotResponse = true
					if success, _ := rec["success"].(bool); !success && !d.isSilent(id) {
						wantEnd = false // a rejected command starts no run
					}
				}
			case "agent_end":
				gotEnd = true
			case "extension_ui_request":
				if m, _ := rec["method"].(string); slices.Contains(dialogMethods, m) {
					if err := d.answer(rec, answers); err != nil {
						return err
					}
				}
			}
			if gotResponse && (!wantEnd || gotEnd) && (!wantUI || answers == nil || len(*answers) == 0) {
				return nil
			}
		case <-exited.done:
			return fmt.Errorf("host exited early: %v (stderr tail: %s)", exited.err, stderr.String())
		case <-deadline:
			return fmt.Errorf("timed out after %s waiting for %s (stderr tail: %s)", timeout, id, stderr.String())
		}
	}
}

func (d *driver) answer(req map[string]any, answers *[]UIAnswer) error {
	reply := map[string]any{"type": "extension_ui_response", "id": req["id"]}
	if answers == nil || len(*answers) == 0 {
		d.rec.add(ChError, map[string]any{"message": fmt.Sprintf("unanswered %v dialog: %v", req["method"], req["title"])})
		reply["cancelled"] = true
		return d.send(reply)
	}
	a := (*answers)[0]
	*answers = (*answers)[1:]
	switch {
	case a.Value != nil:
		reply["value"] = *a.Value
	case a.Confirmed != nil:
		reply["confirmed"] = *a.Confirmed
	default:
		reply["cancelled"] = true
	}
	return d.send(reply)
}

// hostExit is the host process's exit: done is closed once err holds cmd.Wait's result.
type hostExit struct {
	done chan struct{}
	err  error
}

// tailBuffer keeps the last 4 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
