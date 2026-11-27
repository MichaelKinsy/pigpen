package eval

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/eval/py/kernel.ts,
//           packages/coding-agent/src/eval/py/runtime.ts,
//           packages/coding-agent/src/eval/py/runner.py,
//           packages/coding-agent/src/eval/py/prelude.py,
//           packages/coding-agent/src/eval/kernel-base.ts,
//           packages/coding-agent/src/eval/runner-cache.ts,
//           packages/coding-agent/src/eval/probe.ts

//go:embed py_runner.py
var pyRunnerScript string

//go:embed py_prelude.py
var pyPreludeScript string

const (
	pyMinMinor            = 9
	pyTooOldExitBase      = 200
	pyStartupTimeout      = 10 * time.Second
	pyShutdownGrace       = 1 * time.Second
	pyInterruptEscalation = 5 * time.Second
	pyProbeTimeout        = 10 * time.Second
)

var pyReservedPreludeExports = map[string]bool{
	"__omp_tools__":    true,
	"_omp_prelude":     true,
	"__omp_prelude__":  true,
	"AgentHandle":      true,
	"CompletionHandle": true,
	"JudgmentBatch":    true,
	"JudgmentItem":     true,
	"WorkPool":         true,
	"agent":            true,
	"budget":           true,
	"completion":       true,
	"env":              true,
	"judge":            true,
	"judge_batch":      true,
	"log":              true,
	"output":           true,
	"phase":            true,
	"read":             true,
	"tool":             true,
	"wait":             true,
	"workpool":         true,
	"write":            true,
}

var pyEnvAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "LC_MESSAGES": true,
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true,
	"TMPDIR": true, "TEMP": true, "TMP": true,
	"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true,
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true,
	"CONDA_PREFIX": true, "CONDA_DEFAULT_ENV": true, "VIRTUAL_ENV": true,
	"PYTHONPATH": true, "LD_LIBRARY_PATH": true,
	// Windows essentials
	"APPDATA": true, "COMPUTERNAME": true, "COMSPEC": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"LOCALAPPDATA": true, "NUMBER_OF_PROCESSORS": true, "OS": true, "PATHEXT": true,
	"PROCESSOR_ARCHITECTURE": true, "PROCESSOR_IDENTIFIER": true, "PROGRAMDATA": true,
	"PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMW6432": true,
	"SESSIONNAME": true, "SYSTEMDRIVE": true, "SYSTEMROOT": true, "USERDOMAIN": true,
	"USERDOMAIN_ROAMINGPROFILE": true, "USERPROFILE": true, "USERNAME": true, "WINDIR": true,
}

var pyEnvDenylist = map[string]bool{
	"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true, "GOOGLE_API_KEY": true,
	"GEMINI_API_KEY": true, "OPENROUTER_API_KEY": true, "PERPLEXITY_API_KEY": true,
	"PERPLEXITY_COOKIES": true, "EXA_API_KEY": true, "AZURE_OPENAI_API_KEY": true,
	"MISTRAL_API_KEY": true,
}

type pythonRuntime struct {
	PythonPath string
	Env        map[string]string
	VenvPath   string
}

type pythonAvailability struct {
	OK         bool
	PythonPath string
	Reason     string
	Runtime    *pythonRuntime
	Transient  bool
}

var (
	pyAvailMu    sync.Mutex
	pyAvailCache = map[string]pythonAvailability{}
)

func filterPythonEnv(environ []string) map[string]string {
	caseInsensitive := runtime.GOOS == "windows"
	filtered := make(map[string]string)
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		norm := k
		if caseInsensitive {
			norm = strings.ToUpper(k)
		}
		if pyEnvDenylist[norm] {
			continue
		}
		if pyEnvAllowlist[norm] {
			if norm == "PATH" {
				filtered["PATH"] = v
			} else {
				filtered[k] = v
			}
			continue
		}
		if strings.HasPrefix(norm, "LC_") || strings.HasPrefix(norm, "XDG_") || strings.HasPrefix(norm, "PI_") {
			filtered[k] = v
		}
	}
	return filtered
}

func cloneEnvMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mapToEnviron(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func venvBinDir(venvPath string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPath, "Scripts")
	}
	return filepath.Join(venvPath, "bin")
}

func venvPythonExe(binDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(binDir, "python.exe")
	}
	return filepath.Join(binDir, "python")
}

func applyVenvEnv(baseEnv map[string]string, venvPath, binDir string) map[string]string {
	env := cloneEnvMap(baseEnv)
	env["VIRTUAL_ENV"] = venvPath
	if cur := env["PATH"]; cur != "" {
		env["PATH"] = binDir + string(os.PathListSeparator) + cur
	} else {
		env["PATH"] = binDir
	}
	return env
}

func resolveExplicitPythonRuntime(interpreter, cwd string, baseEnv map[string]string) pythonRuntime {
	expanded := expandHome(strings.TrimSpace(interpreter))
	pythonPath := expanded
	if !filepath.IsAbs(expanded) {
		if strings.ContainsAny(expanded, `/\`) {
			pythonPath = filepath.Join(cwd, expanded)
		} else if p, err := exec.LookPath(expanded); err == nil {
			pythonPath = p
		} else {
			pythonPath = filepath.Join(cwd, expanded)
		}
	}
	binDir := filepath.Dir(pythonPath)
	venvPath := filepath.Dir(binDir)
	if _, err := os.Stat(filepath.Join(venvPath, "pyvenv.cfg")); err == nil {
		return pythonRuntime{
			PythonPath: pythonPath,
			Env:        applyVenvEnv(baseEnv, venvPath, binDir),
			VenvPath:   venvPath,
		}
	}
	return pythonRuntime{PythonPath: pythonPath, Env: cloneEnvMap(baseEnv)}
}

func enumeratePythonRuntimes(cwd string, baseEnv map[string]string) []pythonRuntime {
	var out []pythonRuntime
	seen := map[string]bool{}
	push := func(rt pythonRuntime) {
		if rt.PythonPath == "" || seen[rt.PythonPath] {
			return
		}
		seen[rt.PythonPath] = true
		out = append(out, rt)
	}

	venvPath := baseEnv["VIRTUAL_ENV"]
	if venvPath == "" {
		venvPath = baseEnv["CONDA_PREFIX"]
	}
	if venvPath == "" {
		for _, cand := range []string{filepath.Join(cwd, ".venv"), filepath.Join(cwd, "venv")} {
			if st, err := os.Stat(cand); err == nil && st.IsDir() {
				venvPath = cand
				break
			}
		}
	}
	if venvPath != "" {
		binDir := venvBinDir(venvPath)
		cand := venvPythonExe(binDir)
		if _, err := os.Stat(cand); err == nil {
			push(pythonRuntime{
				PythonPath: cand,
				Env:        applyVenvEnv(baseEnv, venvPath, binDir),
				VenvPath:   venvPath,
			})
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		managedVenv := filepath.Join(home, ".omp", "python-env")
		managedBin := venvBinDir(managedVenv)
		managedPy := venvPythonExe(managedBin)
		if _, err := os.Stat(managedPy); err == nil {
			push(pythonRuntime{
				PythonPath: managedPy,
				Env:        applyVenvEnv(baseEnv, managedVenv, managedBin),
				VenvPath:   managedVenv,
			})
		}
	}

	for _, name := range []string{"python3", "python"} {
		for _, p := range lookPathAll(name, baseEnv["PATH"]) {
			push(pythonRuntime{PythonPath: p, Env: cloneEnvMap(baseEnv)})
		}
	}
	return out
}

func lookPathAll(name, pathEnv string) []string {
	var found []string
	seen := map[string]bool{}
	var exts []string
	if runtime.GOOS == "windows" {
		pathext := os.Getenv("PATHEXT")
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, e := range strings.Split(pathext, ";") {
			if e != "" {
				exts = append(exts, strings.ToLower(e))
			}
		}
	} else {
		exts = []string{""}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		for _, ext := range exts {
			cand := filepath.Join(dir, name+ext)
			if seen[cand] {
				continue
			}
			st, err := os.Stat(cand)
			if err != nil || st.IsDir() {
				continue
			}
			if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
				continue
			}
			seen[cand] = true
			found = append(found, cand)
		}
	}
	return found
}

func pyAvailabilityKey(cwd, interpreter string) string {
	absCwd, _ := filepath.Abs(cwd)
	return strings.Join([]string{
		absCwd,
		interpreter,
		os.Getenv("PATH"),
		os.Getenv("VIRTUAL_ENV"),
		os.Getenv("CONDA_PREFIX"),
		os.Getenv("HOME"),
		os.Getenv("PYTHONPATH"),
		os.Getenv("LD_LIBRARY_PATH"),
	}, "\x00")
}

func checkPythonAvailability(ctx context.Context, cwd, interpreter string) pythonAvailability {
	key := pyAvailabilityKey(cwd, interpreter)
	pyAvailMu.Lock()
	if cached, ok := pyAvailCache[key]; ok && cached.OK {
		pyAvailMu.Unlock()
		return cached
	}
	pyAvailMu.Unlock()

	baseEnv := filterPythonEnv(os.Environ())
	var runtimes []pythonRuntime
	if strings.TrimSpace(interpreter) != "" {
		runtimes = []pythonRuntime{resolveExplicitPythonRuntime(interpreter, cwd, baseEnv)}
	} else {
		runtimes = enumeratePythonRuntimes(cwd, baseEnv)
	}
	if len(runtimes) == 0 {
		res := pythonAvailability{OK: false, Reason: "Python executable not found on PATH"}
		pyAvailMu.Lock()
		pyAvailCache[key] = res
		pyAvailMu.Unlock()
		return res
	}

	probeScript := fmt.Sprintf("import sys;v=sys.version_info;sys.exit(0 if v>=(3,%d) else %d+min(v[0],3)*10+min(v[1],9))", pyMinMinor, pyTooOldExitBase)
	var failures []string
	var timedOut bool
	for i := range runtimes {
		rt := runtimes[i]
		if ctx.Err() != nil {
			return pythonAvailability{OK: false, PythonPath: runtimes[0].PythonPath, Reason: "Python availability probe was cancelled.", Transient: true}
		}
		probeCtx, cancel := context.WithTimeout(ctx, pyProbeTimeout)
		cmd := exec.CommandContext(probeCtx, rt.PythonPath, "-c", probeScript)
		cmd.Dir = cwd
		cmd.Env = mapToEnviron(rt.Env)
		err := cmd.Run()
		cancel()
		if err == nil {
			rtCopy := rt
			res := pythonAvailability{OK: true, PythonPath: rt.PythonPath, Runtime: &rtCopy}
			pyAvailMu.Lock()
			pyAvailCache[key] = res
			pyAvailMu.Unlock()
			return res
		}
		if ctx.Err() != nil {
			return pythonAvailability{OK: false, PythonPath: runtimes[0].PythonPath, Reason: "Python availability probe was cancelled.", Transient: true}
		}
		if probeCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			failures = append(failures, fmt.Sprintf("%s (probe timed out)", rt.PythonPath))
			continue
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			encoded := code - pyTooOldExitBase
			if encoded >= 20 && encoded <= 39 {
				failures = append(failures, fmt.Sprintf("%s (Python %d.%d is too old; 3.%d+ required)", rt.PythonPath, encoded/10, encoded%10, pyMinMinor))
			} else {
				failures = append(failures, fmt.Sprintf("%s (exit code %d)", rt.PythonPath, code))
			}
		} else {
			failures = append(failures, fmt.Sprintf("%s (%v)", rt.PythonPath, err))
		}
	}
	res := pythonAvailability{
		OK:         false,
		PythonPath: runtimes[0].PythonPath,
		Reason:     "No working Python interpreter found. Tried: " + strings.Join(failures, "; "),
		Transient:  timedOut,
	}
	if !res.Transient {
		pyAvailMu.Lock()
		pyAvailCache[key] = res
		pyAvailMu.Unlock()
	}
	return res
}

var (
	stagedMu    sync.Mutex
	stagedPaths = map[string]string{}
)

// stageRunnerScript writes content to a per-uid private (0700) staging directory
// under os.TempDir(), verifying ownership and permissions on every reuse.
// upstream: packages/coding-agent/src/eval/runner-cache.ts:39-93
func stageRunnerScript(dirName, ext, content string) (string, error) {
	sum := sha256.Sum256([]byte(content))
	key := dirName + "\x00" + hex.EncodeToString(sum[:8]) + "." + ext

	stagedMu.Lock()
	memo := stagedPaths[key]
	stagedMu.Unlock()
	if memo != "" {
		if err := assertOwnerPrivateDir(filepath.Dir(memo), "Runner staging directory"); err == nil {
			if existing, rerr := os.ReadFile(memo); rerr == nil && string(existing) == content {
				return memo, nil
			}
		}
		stagedMu.Lock()
		delete(stagedPaths, key)
		stagedMu.Unlock()
	}

	dir, err := resolveStagingDir(dirName)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, fmt.Sprintf("runner-%s.%s", hex.EncodeToString(sum[:8]), ext))
	if existing, err := os.ReadFile(dst); err == nil && string(existing) == content {
		stagedMu.Lock()
		stagedPaths[key] = dst
		stagedMu.Unlock()
		return dst, nil
	}
	tmp := dst + "." + newRandomID()
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		if existing, rerr := os.ReadFile(dst); rerr == nil && string(existing) == content {
			stagedMu.Lock()
			stagedPaths[key] = dst
			stagedMu.Unlock()
			return dst, nil
		}
		return "", err
	}
	stagedMu.Lock()
	stagedPaths[key] = dst
	stagedMu.Unlock()
	return dst, nil
}

// preludeSnippet is one installed prelude's snapshot for change detection.
type preludeSnippet struct {
	Name    string
	Exports []string
	Source  string
}

func samePreludeSnippet(a, b preludeSnippet) bool {
	if a.Name != b.Name || a.Source != b.Source || len(a.Exports) != len(b.Exports) {
		return false
	}
	for i := range a.Exports {
		if a.Exports[i] != b.Exports[i] {
			return false
		}
	}
	return true
}

// kernelResult is the outcome of one cell executed against a Python or JS kernel.
type kernelResult struct {
	Output         string
	FullOutput     string
	Truncation     *TruncationMeta
	ExitCode       *int
	Cancelled      bool
	TimedOut       bool
	KernelKilled   bool
	DiedMidExec    bool
	DisplayOutputs []DisplayOutput
}

// pyFrame mirrors omp's runner.py NDJSON frame shape.
type pyFrame struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	Data           string          `json:"data"`
	Text           string          `json:"text"`
	View           any             `json:"view"`
	Image          map[string]any  `json:"image"`
	Event          EvalStatusEvent `json:"event"`
	Ename          string          `json:"ename"`
	Evalue         string          `json:"evalue"`
	Traceback      []string        `json:"traceback"`
	Status         string          `json:"status"`
	ExecutionCount int             `json:"executionCount"`
	Cancelled      bool            `json:"cancelled"`
}

type pyPending struct {
	onChunk    func(string)
	onDisplay  func(DisplayOutput)
	status     string
	cancelled  bool
	diedMidRun bool
	done       chan struct{}
	once       sync.Once
}

func (p *pyPending) finish() {
	p.once.Do(func() { close(p.done) })
}

// pythonKernel manages one long-lived Python subprocess running py_runner.py.
type pythonKernel struct {
	id         string
	pythonPath string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdinMu    sync.Mutex

	mu               sync.Mutex
	alive            bool
	disposed         bool
	exitReason       string
	stderrTail       strings.Builder
	pending          map[string]*pyPending
	installedPrelude map[string]preludeSnippet
	exited           chan struct{}
	interruptWait    time.Duration
}

func startPythonKernel(ctx context.Context, cwd, interpreter string, env map[string]string) (*pythonKernel, error) {
	avail := checkPythonAvailability(ctx, cwd, interpreter)
	if !avail.OK {
		return nil, errors.New(avail.Reason)
	}
	rt := avail.Runtime
	if rt == nil {
		baseEnv := filterPythonEnv(os.Environ())
		if interpreter != "" {
			r := resolveExplicitPythonRuntime(interpreter, cwd, baseEnv)
			rt = &r
		} else {
			rts := enumeratePythonRuntimes(cwd, baseEnv)
			if len(rts) == 0 {
				return nil, errors.New("Python executable not found on PATH")
			}
			rt = &rts[0]
		}
	}

	scriptPath, err := stageRunnerScript("omp-python-runner", "py", pyRunnerScript)
	if err != nil {
		return nil, fmt.Errorf("stage python runner: %w", err)
	}

	spawnEnv := cloneEnvMap(rt.Env)
	for k, v := range env {
		spawnEnv[k] = v
	}
	spawnEnv["PYTHONUNBUFFERED"] = "1"
	spawnEnv["PYTHONIOENCODING"] = "utf-8"
	spawnEnv["PYTHONUTF8"] = "1"

	cmd := exec.Command(rt.PythonPath, "-u", scriptPath)
	cmd.Dir = cwd
	cmd.Env = mapToEnviron(spawnEnv)
	configureProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("python stdin pipe: %w", err)
	}
	// Parent-owned os.Pipe for stdout/stderr so cmd.Wait() never closes the read
	// end while readStdout/readStderr are still draining the last frames.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("python stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("python stderr pipe: %w", err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return nil, fmt.Errorf("start python kernel: %w", err)
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()

	k := &pythonKernel{
		id:               "py-" + newRandomID(),
		pythonPath:       rt.PythonPath,
		cmd:              cmd,
		stdin:            stdin,
		alive:            true,
		pending:          make(map[string]*pyPending),
		installedPrelude: make(map[string]preludeSnippet),
		exited:           make(chan struct{}),
		interruptWait:    pyInterruptEscalation,
	}

	var readersWG sync.WaitGroup
	readersWG.Add(2)
	go func() {
		defer readersWG.Done()
		defer stdoutR.Close()
		k.readStdout(stdoutR)
	}()
	go func() {
		defer readersWG.Done()
		defer stderrR.Close()
		k.readStderr(stderrR)
	}()
	go k.waitExit(&readersWG)
	initCtx, cancel := context.WithTimeout(ctx, pyStartupTimeout)
	defer cancel()

	initCode := buildPythonInitScript(cwd, env)
	if _, err := k.execRaw(initCtx, "init-"+newRandomID(), initCode, cwd, "", nil, true, nil, nil); err != nil {
		k.shutdown()
		return nil, fmt.Errorf("Python kernel init failed: %w", err)
	}
	// Run omp's prelude.py and alias __omp_prelude__ = _omp_prelude so both omp's
	// built-in Python prelude snippets (_omp_prelude) and internal/eval Prelude docs
	// (__omp_prelude__) work identically.
	preludeCode := pyPreludeScript + "\n__omp_prelude__ = _omp_prelude\n"
	if _, err := k.execRaw(initCtx, "prelude-"+newRandomID(), preludeCode, cwd, "", nil, true, nil, nil); err != nil {
		k.shutdown()
		return nil, fmt.Errorf("Python kernel prelude failed: %w", err)
	}
	return k, nil
}

func buildPythonInitScript(cwd string, env map[string]string) string {
	cwdJSON, _ := json.Marshal(cwd)
	envJSON, _ := json.Marshal(env)
	if env == nil {
		envJSON = []byte("{}")
	}
	lines := []string{
		"import os, signal, sys",
		"__omp_cwd = " + string(cwdJSON),
		"os.chdir(__omp_cwd)",
		"__omp_env = " + string(envJSON),
		"for __omp_key, __omp_val in __omp_env.items():\n    os.environ[__omp_key] = __omp_val",
		"if __omp_cwd not in sys.path:\n    sys.path.insert(0, __omp_cwd)",
	}
	if runtime.GOOS == "windows" {
		// GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid) arrives as SIGBREAK in CPython;
		// forward it to SIGINT so runner.py's own SIGINT handler interrupts the running cell
		// cleanly instead of terminating the kernel process.
		lines = append(lines, `if hasattr(signal, "SIGBREAK"):
    signal.signal(signal.SIGBREAK, lambda _s, _f: signal.raise_signal(signal.SIGINT))`)
	}
	return strings.Join(lines, "\n")
}

func (k *pythonKernel) pid() int {
	if k == nil || k.cmd == nil || k.cmd.Process == nil {
		return 0
	}
	return k.cmd.Process.Pid
}

func (k *pythonKernel) isAlive() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.alive && !k.disposed
}

func (k *pythonKernel) abortPending(reason string, diedMidRun bool) {
	k.mu.Lock()
	pendings := make([]*pyPending, 0, len(k.pending))
	for _, p := range k.pending {
		pendings = append(pendings, p)
	}
	k.pending = make(map[string]*pyPending)
	k.mu.Unlock()

	// upstream: packages/coding-agent/src/eval/kernel-base.ts:556-578
	for _, p := range pendings {
		p.status = "error"
		p.cancelled = true
		p.diedMidRun = diedMidRun
		if p.onChunk != nil && reason != "" {
			p.onChunk("[kernel] " + reason + "\n")
		}
		p.finish()
	}
}

func (k *pythonKernel) waitExit(readersWG *sync.WaitGroup) {
	// Wait for stdout/stderr readers to reach EOF before reaping the process so
	// final frames are never dropped and cmd.Wait() never races the scanner.
	if readersWG != nil {
		readersWG.Wait()
	}
	err := k.cmd.Wait()
	k.mu.Lock()
	k.alive = false
	disposed := k.disposed
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			k.exitReason = fmt.Sprintf("Python kernel exited with code %d", exitErr.ExitCode())
		} else {
			k.exitReason = fmt.Sprintf("Python kernel exited: %v", err)
		}
	} else {
		k.exitReason = "Python kernel exited with code 0"
	}
	reason := k.exitReason
	k.mu.Unlock()

	k.abortPending(reason, !disposed)
	close(k.exited)
}

func (k *pythonKernel) readStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			k.mu.Lock()
			if k.stderrTail.Len() < 4096 {
				k.stderrTail.Write(buf[:n])
			}
			k.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (k *pythonKernel) readStdout(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var frame pyFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		k.handleFrame(frame)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		// Scanner failed (e.g., frame exceeded 64 MiB): fail pending cells and shut down
		// so stdout is never left unread and cells never hang.
		k.abortPending(fmt.Sprintf("Python kernel protocol reader failed: %v", err), true)
		go k.shutdown()
	}
}

func (k *pythonKernel) handleFrame(frame pyFrame) {
	k.mu.Lock()
	p := k.pending[frame.ID]
	k.mu.Unlock()
	if p == nil {
		return
	}
	switch frame.Type {
	case "stdout", "stderr":
		if frame.Data != "" && p.onChunk != nil {
			p.onChunk(frame.Data)
		}
	case "view":
		if p.onDisplay != nil && frame.View != nil {
			p.onDisplay(DisplayOutput{Type: "view", Text: frame.Text, View: frame.View})
		}
	case "image":
		if p.onDisplay != nil && frame.Image != nil {
			if img := parseImageMap(frame.Image); img != nil {
				p.onDisplay(DisplayOutput{Type: "image", Image: img})
			}
		}
	case "status":
		if p.onDisplay != nil && frame.Event != nil {
			p.onDisplay(DisplayOutput{Type: "status", Status: frame.Event})
		}
	case "error":
		// upstream: packages/coding-agent/src/eval/kernel-base.ts:714-728
		// Stream error trace immediately into the output stream even when cancelled
		// (so KeyboardInterrupt tracebacks are preserved).
		p.status = "error"
		var msg string
		if len(frame.Traceback) > 0 {
			msg = strings.Join(frame.Traceback, "\n") + "\n"
		} else if frame.Ename != "" || frame.Evalue != "" {
			if frame.Evalue != "" {
				msg = frame.Ename + ": " + frame.Evalue + "\n"
			} else {
				msg = frame.Ename + "\n"
			}
		}
		if msg != "" && p.onChunk != nil {
			p.onChunk(msg)
		}
	case "done":
		if frame.Status != "" {
			p.status = frame.Status
		}
		if frame.Cancelled {
			p.cancelled = true
		}
		k.mu.Lock()
		delete(k.pending, frame.ID)
		k.mu.Unlock()
		p.finish()
	}
}

func parseImageMap(m map[string]any) *EvalImageDetail {
	data, _ := m["data"].(string)
	mime, _ := m["mimeType"].(string)
	if data == "" || mime == "" {
		return nil
	}
	img := &EvalImageDetail{
		Type:     "image",
		Data:     data,
		MimeType: mime,
	}
	if detail, ok := m["detail"].(string); ok {
		img.Detail = detail
	}
	if u, ok := m["url"].(string); ok {
		img.URL = u
	}
	return img
}

func (k *pythonKernel) writeLine(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	k.stdinMu.Lock()
	defer k.stdinMu.Unlock()
	if k.stdin == nil {
		return errors.New("Python kernel stdin closed")
	}
	_, err = k.stdin.Write(raw)
	return err
}

func (k *pythonKernel) syncPreludes(ctx context.Context, list []Prelude) error {
	desired := make(map[string]preludeSnippet, len(list))
	exportOwners := make(map[string]string)
	for _, p := range list {
		// Built-in host-only bridge preludes (`agent`, `workpool`) already have their
		// Python globals in omp's embedded prelude.py; skip snippet install and export conflict.
		if isBuiltinBridgePrelude(p.Name) {
			continue
		}
		if strings.TrimSpace(p.Python) == "" {
			continue
		}
		if _, dup := desired[p.Name]; dup {
			return fmt.Errorf("Duplicate eval prelude name: %s", p.Name)
		}
		for _, exp := range p.Exports {
			if pyReservedPreludeExports[exp] {
				return fmt.Errorf("Eval prelude %s cannot replace reserved global %s", p.Name, exp)
			}
			if owner, exists := exportOwners[exp]; exists {
				return fmt.Errorf("Eval preludes %s and %s both export %s", owner, p.Name, exp)
			}
			exportOwners[exp] = p.Name
		}
		desired[p.Name] = preludeSnippet{
			Name:    p.Name,
			Exports: append([]string(nil), p.Exports...),
			Source:  p.Python,
		}
	}

	k.mu.Lock()
	var removedExports []string
	var changed []preludeSnippet
	for name, installed := range k.installedPrelude {
		next, ok := desired[name]
		if ok && samePreludeSnippet(next, installed) {
			continue
		}
		removedExports = append(removedExports, installed.Exports...)
	}
	for name, next := range desired {
		installed, ok := k.installedPrelude[name]
		if ok && samePreludeSnippet(next, installed) {
			continue
		}
		changed = append(changed, next)
	}
	k.mu.Unlock()

	if len(removedExports) == 0 && len(changed) == 0 {
		return nil
	}

	var src []string
	if len(removedExports) > 0 {
		rawExports, _ := json.Marshal(removedExports)
		src = append(src,
			fmt.Sprintf("for __omp_export in %s:\n    globals().pop(__omp_export, None)", string(rawExports)),
			`globals().pop("__omp_export", None)`,
		)
	}
	for _, p := range changed {
		src = append(src, p.Source)
	}
	syncCtx, cancel := context.WithTimeout(ctx, pyStartupTimeout)
	defer cancel()
	if _, err := k.execRaw(syncCtx, "prelude-sync-"+newRandomID(), strings.Join(src, "\n"), "", "", nil, true, nil, nil); err != nil {
		return fmt.Errorf("Python eval prelude sync failed: %w", err)
	}

	k.mu.Lock()
	k.installedPrelude = desired
	k.mu.Unlock()
	return nil
}

func (k *pythonKernel) execRaw(
	ctx context.Context,
	runID, code, cwd, filename string,
	envPatch map[string]any,
	silent bool,
	onChunk func(string),
	onDisplay func(DisplayOutput),
) (kernelResult, error) {
	// Never submit a request whose context is already cancelled (upstream: kernel-base.ts:362-365).
	if ctx.Err() != nil {
		return kernelResult{Cancelled: true}, nil
	}
	if !k.isAlive() {
		return kernelResult{}, errors.New("Python kernel is not running")
	}

	outBuf := newSpillableOutputBuffer(DefaultOutputMaxBytes, WriteSpill != nil)
	var displays []DisplayOutput
	var dispMu sync.Mutex

	p := &pyPending{
		status: "ok",
		done:   make(chan struct{}),
		onChunk: func(chunk string) {
			outBuf.Append(chunk)
			if onChunk != nil {
				onChunk(chunk)
			}
		},
		onDisplay: func(d DisplayOutput) {
			dispMu.Lock()
			displays = append(displays, d)
			dispMu.Unlock()
			if onDisplay != nil {
				onDisplay(d)
			}
		},
	}

	k.mu.Lock()
	if !k.alive || k.disposed {
		k.mu.Unlock()
		return kernelResult{}, errors.New("Python kernel is not running")
	}
	k.pending[runID] = p
	k.mu.Unlock()

	req := map[string]any{
		"id":           runID,
		"code":         code,
		"silent":       silent,
		"storeHistory": !silent,
	}
	if cwd != "" {
		req["cwd"] = cwd
	}
	if filename != "" {
		req["filename"] = filename
	}
	if envPatch != nil {
		req["env"] = envPatch
	}
	if err := k.writeLine(req); err != nil {
		k.mu.Lock()
		delete(k.pending, runID)
		k.mu.Unlock()
		return kernelResult{}, err
	}

	var killed bool
	var cancelled bool
	select {
	case <-p.done:
		cancelled = p.cancelled
	case <-ctx.Done():
		cancelled = true
		_ = k.writeLine(map[string]any{"type": "interrupt", "id": runID})
		sendInterrupt(k.cmd.Process)
		timer := time.NewTimer(k.interruptWait)
		select {
		case <-p.done:
			timer.Stop()
		case <-timer.C:
			killed = true
			k.shutdown()
			<-p.done
		}
	}

	outStr, truncMeta := outBuf.Finalize()
	if silent && p.status == "error" {
		return kernelResult{}, errors.New(strings.TrimSpace(outStr))
	}

	var exitCode *int
	if !cancelled {
		codeVal := 0
		if p.status == "error" {
			codeVal = 1
		}
		exitCode = &codeVal
	}

	dispMu.Lock()
	dispCopy := append([]DisplayOutput(nil), displays...)
	dispMu.Unlock()

	fullOut := ""
	if truncMeta != nil && WriteSpill != nil {
		fullOut = outBuf.FullText()
	}
	return kernelResult{
		Output:         outStr,
		FullOutput:     fullOut,
		Truncation:     truncMeta,
		ExitCode:       exitCode,
		Cancelled:      cancelled,
		KernelKilled:   killed || !k.isAlive(),
		DiedMidExec:    p.diedMidRun && ctx.Err() == nil,
		DisplayOutputs: dispCopy,
	}, nil
}

func (k *pythonKernel) shutdown() {
	if k == nil {
		return
	}
	k.mu.Lock()
	if k.disposed {
		k.mu.Unlock()
		<-k.exited
		return
	}
	k.disposed = true
	k.alive = false
	k.mu.Unlock()

	k.abortPending("Python kernel shutdown", false)

	_ = k.writeLine(map[string]any{"type": "exit"})
	k.stdinMu.Lock()
	if k.stdin != nil {
		_ = k.stdin.Close()
		k.stdin = nil
	}
	k.stdinMu.Unlock()

	select {
	case <-k.exited:
		return
	case <-time.After(pyShutdownGrace):
	}
	killProcessTreeGraceful(k.cmd.Process, k.exited, pyShutdownGrace)
	<-k.exited
}

func formatPyTimeoutAnnotation(timeout time.Duration, kernelKilled bool) string {
	// upstream: packages/coding-agent/src/eval/kernel-session-registry.ts:126
	if kernelKilled {
		return "eval cell timed out and the kernel was unresponsive to interrupt; the kernel has been killed and will be recreated on the next call."
	}
	secs := max(int(timeout.Round(time.Second)/time.Second), 1)
	return fmt.Sprintf("eval cell timed out after %ds; kernel interrupted but remains running. Reset the kernel via { reset: true } if state appears corrupted.", secs)
}

func formatKernelDiedAnnotation() string {
	// upstream: packages/coding-agent/src/eval/executor-base.ts:581
	return "Kernel died during execution; completion is uncertain. The cell was not replayed; check for partial side effects before retrying."
}

func formatKernelRestartNotice(runtimeLabel string) string {
	// upstream: packages/coding-agent/src/eval/executor-base.ts:112
	return fmt.Sprintf("Kernel restarted: the previous %s is dead; state is unavailable. Re-run needed setup: variables, imports and definitions from earlier cells are gone.", runtimeLabel)
}

// parseIntOrZero helps parse version strings safely.
func parseIntOrZero(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
