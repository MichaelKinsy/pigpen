package pi_background_tasks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resource control: running tasks at lower priority. upstream: extensions/resource-control.ts.

var (
	resourceControlModes   = []string{"auto", "systemd-run", "nice-ionice", "off"}
	resourceIoniceClasses  = []string{"realtime", "best-effort", "idle"}
	systemctlTimeout       = 2 * time.Second
	systemdUserManagerArgs = []string{"--user", "show-environment"}
)

const (
	defaultCPUWeight   = 100
	defaultIOWeight    = 100
	defaultNice        = 10
	defaultIoniceClass = "best-effort"
	defaultIoniceLevel = 7
)

type resourceControlSettings struct {
	enabled               bool
	mode                  string
	applyToBgTask         bool
	applyToAutoBackground bool
	cpuWeight             int
	ioWeight              int
	nice                  int
	ioniceClass           string
	ioniceLevel           int
	warnOnFallback        bool
}

// jsRound is Math.round: ties toward +Infinity.
func jsRound(f float64) float64 { return math.Floor(f + 0.5) }

func clampInt(value float64, lo, hi, fallback int) int {
	v := fallback
	if !math.IsNaN(value) && !math.IsInf(value, 0) {
		v = int(jsRound(value))
	}
	return min(hi, max(lo, v))
}

func readResourceControlSettings(cwd string) resourceControlSettings {
	return resourceControlSettings{
		enabled:               settingBoolean("resourceControlEnabled", true, cwd),
		mode:                  settingEnum("resourceControlMode", resourceControlModes, "nice-ionice", cwd),
		applyToBgTask:         settingBoolean("resourceControlApplyToBgTask", true, cwd),
		applyToAutoBackground: settingBoolean("resourceControlApplyToAutoBackground", true, cwd),
		cpuWeight:             clampInt(settingNumber("resourceControlCpuWeight", defaultCPUWeight, cwd), 1, 10_000, defaultCPUWeight),
		ioWeight:              clampInt(settingNumber("resourceControlIoWeight", defaultIOWeight, cwd), 1, 10_000, defaultIOWeight),
		nice:                  clampInt(settingNumber("resourceControlNice", defaultNice, cwd), -20, 19, defaultNice),
		ioniceClass:           settingEnum("resourceControlIoniceClass", resourceIoniceClasses, defaultIoniceClass, cwd),
		ioniceLevel:           clampInt(settingNumber("resourceControlIoniceLevel", defaultIoniceLevel, cwd), 0, 7, defaultIoniceLevel),
		warnOnFallback:        settingBoolean("resourceControlWarnOnFallback", true, cwd),
	}
}

func settledSuccess(r probeResult) tri {
	switch {
	case r.kind == "unsettled":
		return triNull
	case r.kind == "exited" && r.status == 0:
		return triTrue
	}
	return triFalse
}

// userManagerReachability is whether the user's systemd manager answers on its bus. Spawn planning asks synchronously and
// liveness probes asynchronously; both read one memo, so once either settles the answer the other never probes.
type userManagerReachability struct {
	run     probeRunner
	runSync probeRunner
	mu      sync.Mutex
	answer  tri
	flight  *future
}

func newUserManagerReachability(run, runSync probeRunner) *userManagerReachability {
	if run == nil {
		run = runProbe
	}
	if runSync == nil {
		runSync = runProbe
	}
	return &userManagerReachability{run: run, runSync: runSync}
}

// sync answers from the memo or one probe; triNull when this call's probe settled nothing.
func (u *userManagerReachability) sync() tri {
	u.mu.Lock()
	if u.answer != triNull {
		a := u.answer
		u.mu.Unlock()
		return a
	}
	u.mu.Unlock()
	result := settledSuccess(u.runSync("systemctl", systemdUserManagerArgs))
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.answer == triNull {
		u.answer = result
	}
	return u.answer
}

// check answers from the memo or a probe the concurrent callers share.
func (u *userManagerReachability) check() bool {
	u.mu.Lock()
	if u.answer != triNull {
		a := u.answer
		u.mu.Unlock()
		return a == triTrue
	}
	if u.flight != nil {
		flight := u.flight
		u.mu.Unlock()
		flight.wait()
		u.mu.Lock()
		defer u.mu.Unlock()
		return u.answer == triTrue
	}
	flight := newFuture()
	u.flight = flight
	u.mu.Unlock()
	result := settledSuccess(u.run("systemctl", systemdUserManagerArgs))
	u.mu.Lock()
	if u.answer == triNull {
		u.answer = result
	}
	u.flight = nil
	answer := u.answer
	u.mu.Unlock()
	flight.resolve()
	return answer == triTrue
}

// userManager is the answer this extension load reads.
var userManager = newUserManagerReachability(nil, nil)

// commandLookup is whether a helper command is on PATH, memoized per platform and command.
type commandLookup struct {
	runSync probeRunner
	mu      sync.Mutex
	settled map[string]bool
}

func newCommandLookup(runSync probeRunner) *commandLookup {
	if runSync == nil {
		runSync = runProbe
	}
	return &commandLookup{runSync: runSync, settled: map[string]bool{}}
}

func (c *commandLookup) exists(command, platform string) bool {
	key := platform + ":" + command
	c.mu.Lock()
	known, ok := c.settled[key]
	c.mu.Unlock()
	if ok {
		return known
	}
	var result tri
	if platform == "windows" {
		result = settledSuccess(c.runSync("where", []string{command}))
	} else {
		result = settledSuccess(c.runSync("sh", []string{"-c", `command -v "$1" >/dev/null 2>&1`, "sh", command}))
	}
	// A timed-out or killed lookup reads as absent for this spawn only; the next spawn asks again.
	if result == triNull {
		return false
	}
	c.mu.Lock()
	c.settled[key] = result == triTrue
	c.mu.Unlock()
	return result == triTrue
}

var helperLookups = newCommandLookup(nil)

var (
	systemdCacheMu     sync.Mutex
	systemdRunnableFor = map[string]bool{}
)

func systemdProbeCacheKey(s resourceControlSettings) string {
	return fmt.Sprintf("%d:%d:%d:%s:%d", s.cpuWeight, s.ioWeight, s.nice, s.ioniceClass, s.ioniceLevel)
}

func systemdResourcePropertyArgs(s resourceControlSettings) []string {
	args := []string{
		"--property=CPUWeight=" + strconv.Itoa(s.cpuWeight),
		"--property=IOWeight=" + strconv.Itoa(s.ioWeight),
		"--property=Nice=" + strconv.Itoa(s.nice),
		"--property=IOSchedulingClass=" + s.ioniceClass,
	}
	if s.ioniceClass != "idle" {
		args = append(args, "--property=IOSchedulingPriority="+strconv.Itoa(s.ioniceLevel))
	}
	return args
}

// userSystemdAvailable probes the exact transient-service shape the plan uses, memoized per settings. A manager that did not
// answer is cached as unavailable for the settings, so spawn planning blocks on it once; the liveness check keeps asking.
func userSystemdAvailable(commandProbe func(string) bool, platform string, s resourceControlSettings, manager *userManagerReachability) bool {
	if platform != "linux" {
		return false
	}
	if !commandProbe("systemd-run") || !commandProbe("systemctl") {
		return false
	}
	key := systemdProbeCacheKey(s)
	systemdCacheMu.Lock()
	cached, ok := systemdRunnableFor[key]
	systemdCacheMu.Unlock()
	if ok {
		return cached
	}
	set := func(v bool) bool {
		systemdCacheMu.Lock()
		systemdRunnableFor[key] = v
		systemdCacheMu.Unlock()
		return v
	}
	if manager.sync() != triTrue {
		return set(false)
	}
	cwd, _ := os.Getwd()
	unitName := fmt.Sprintf("kendex-pi-bg-probe-%d-%d.service", os.Getpid(), clockNow())
	args := append([]string{"--user", "--wait", "--pipe", "--quiet", "--collect", "--unit=" + unitName, "--working-directory=" + cwd}, systemdResourcePropertyArgs(s)...)
	args = append(args, "--", "/usr/bin/true")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-run", args...)
	hideWindow(cmd)
	return set(cmd.Run() == nil)
}

// resourceControlProbes replaces the platform and the helper and systemd probes in a test.
type resourceControlProbes struct {
	platform             string
	commandExists        func(string) bool
	commandLookup        *commandLookup
	userSystemdAvailable func() bool
	userManager          *userManagerReachability
}

// resourceControlSpawnInput is ResourceControlSpawnInput.
type resourceControlSpawnInput struct {
	command   string
	cwd       string
	shell     string
	shellArgs []string
	taskID    string
	now       int64
	origin    string
	settings  *resourceControlSettings
	probes    *resourceControlProbes
}

// resourceControlSpawnPlan is the program and arguments to start, the metadata to persist and the warnings to show.
type resourceControlSpawnPlan struct {
	file     string
	args     []string
	metadata *resourceControlMeta
	warnings []string
}

func platformFor(p *resourceControlProbes) string {
	if p != nil && p.platform != "" {
		return p.platform
	}
	return runtime.GOOS
}

func commandProbeFor(p *resourceControlProbes, platform string) func(string) bool {
	if p != nil && p.commandExists != nil {
		return p.commandExists
	}
	lookup := helperLookups
	if p != nil && p.commandLookup != nil {
		lookup = p.commandLookup
	}
	return func(command string) bool { return lookup.exists(command, platform) }
}

func ioniceClassNumber(class string) string {
	switch class {
	case "realtime":
		return "1"
	case "idle":
		return "3"
	}
	return "2"
}

var unsafeUnit = regexp.MustCompile(`[^A-Za-z0-9_.:-]`)

func makeSystemdUnitName(taskID string, now int64) string {
	safe := jsHead(unsafeUnit.ReplaceAllString(taskID+"-"+strconv.FormatInt(now, 10), "-"), 96)
	if safe == "" {
		safe = "task"
	}
	return "kendex-pi-bg-" + safe + ".service"
}

type niceIoniceHelpers struct{ nice, ionice bool }

func helpersFor(commandProbe func(string) bool, platform string) *niceIoniceHelpers {
	if platform == "windows" {
		return nil
	}
	h := &niceIoniceHelpers{nice: commandProbe("nice"), ionice: commandProbe("ionice")}
	if h.nice || h.ionice {
		return h
	}
	return nil
}

// modeResolution is the mode the settings resolve to: "none", "systemd-run" or "nice-ionice" (with its helpers).
type modeResolution struct {
	mode    string
	helpers *niceIoniceHelpers
	warning string
}

func resolveMode(s resourceControlSettings, origin string, probes *resourceControlProbes) modeResolution {
	applies := s.applyToBgTask
	if origin == originAutoBackground {
		applies = s.applyToAutoBackground
	}
	if !s.enabled || s.mode == "off" || !applies {
		return modeResolution{mode: "none"}
	}
	platform := platformFor(probes)
	hasCommand := commandProbeFor(probes, platform)
	// The systemd probe can block on a transient unit, so only the modes that may pick systemd-run ask it.
	hasSystemd := func() bool {
		if probes != nil && probes.userSystemdAvailable != nil {
			return probes.userSystemdAvailable()
		}
		manager := userManager
		if probes != nil && probes.userManager != nil {
			manager = probes.userManager
		}
		return userSystemdAvailable(hasCommand, platform, s, manager)
	}
	switch s.mode {
	case "systemd-run":
		if hasSystemd() {
			return modeResolution{mode: "systemd-run"}
		}
		return modeResolution{mode: "none", warning: "resourceControlMode=systemd-run requested, but usable user systemd-run support was not detected; spawning without resource controls."}
	case "nice-ionice":
		if h := helpersFor(hasCommand, platform); h != nil {
			return modeResolution{mode: "nice-ionice", helpers: h}
		}
		return modeResolution{mode: "none", warning: "resourceControlMode=nice-ionice requested, but nice/ionice helpers were not detected; spawning without resource controls."}
	}
	if hasSystemd() {
		return modeResolution{mode: "systemd-run"}
	}
	if h := helpersFor(hasCommand, platform); h != nil {
		return modeResolution{mode: "nice-ionice", helpers: h, warning: "resourceControlMode=auto could not use user systemd-run; using nice/ionice fallback."}
	}
	return modeResolution{mode: "none", warning: "resource controls are enabled, but no supported helper was detected; spawning without resource controls."}
}

func basePlan(in resourceControlSpawnInput) resourceControlSpawnPlan {
	return resourceControlSpawnPlan{file: in.shell, args: append(append([]string{}, in.shellArgs...), in.command), warnings: []string{}}
}

func systemdPlan(in resourceControlSpawnInput, s resourceControlSettings, warning string) resourceControlSpawnPlan {
	now := in.now
	if now == 0 {
		now = clockNow()
	}
	unit := makeSystemdUnitName(in.taskID, now)
	args := []string{"--user", "--quiet", "--wait", "--pipe", "--collect", "--unit=" + unit, "--working-directory=" + in.cwd}
	args = append(args, systemdResourcePropertyArgs(s)...)
	args = append(args, "--", in.shell)
	args = append(args, in.shellArgs...)
	args = append(args, in.command)
	plan := resourceControlSpawnPlan{file: "systemd-run", args: args, metadata: &resourceControlMeta{Mode: "systemd-run", RequestedMode: s.mode, UnitName: unit, Warning: warning}, warnings: []string{}}
	if warning != "" {
		plan.warnings = []string{warning}
	}
	return plan
}

func niceIonicePlan(in resourceControlSpawnInput, s resourceControlSettings, h *niceIoniceHelpers, warning string) resourceControlSpawnPlan {
	file := in.shell
	args := append(append([]string{}, in.shellArgs...), in.command)
	if h.ionice {
		file = "ionice"
		pre := []string{"-c", ioniceClassNumber(s.ioniceClass)}
		if s.ioniceClass != "idle" {
			pre = append(pre, "-n", strconv.Itoa(s.ioniceLevel))
		}
		args = append(append(pre, in.shell), args...)
	}
	if h.nice {
		args = append([]string{"-n", strconv.Itoa(s.nice), file}, args...)
		file = "nice"
	}
	plan := resourceControlSpawnPlan{file: file, args: args, metadata: &resourceControlMeta{Mode: "nice-ionice", RequestedMode: s.mode, Warning: warning}, warnings: []string{}}
	if warning != "" {
		plan.warnings = []string{warning}
	}
	return plan
}

// planResourceControlledSpawn is the plan for one spawn.
func planResourceControlledSpawn(in resourceControlSpawnInput) resourceControlSpawnPlan {
	s := resourceControlSettings{}
	if in.settings != nil {
		s = *in.settings
	} else {
		s = readResourceControlSettings(in.cwd)
	}
	origin := in.origin
	if origin == "" {
		origin = originBgTask
	}
	res := resolveMode(s, origin, in.probes)
	switch res.mode {
	case "systemd-run":
		return systemdPlan(in, s, res.warning)
	case "nice-ionice":
		return niceIonicePlan(in, s, res.helpers, res.warning)
	}
	plan := basePlan(in)
	if res.warning != "" {
		plan.warnings = append(plan.warnings, res.warning)
	}
	return plan
}

// stopRunResult is what a stop command reports.
type stopRunResult struct {
	status    *int
	errText   string
	stderr    string
	hasStderr bool
}

type stopRunner func(command string, args []string) stopRunResult

// systemctlRunner runs the stop and kill of a unit; the tests replace it.
var systemctlRunner stopRunner = defaultStopRunner

func defaultStopRunner(command string, args []string) stopRunResult {
	ctx, cancel := context.WithTimeout(context.Background(), systemctlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	hideWindow(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := stopRunResult{stderr: stderr.String(), hasStderr: true}
	if err == nil {
		zero := 0
		res.status = &zero
		return res
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() >= 0 {
		code := ee.ExitCode()
		res.status = &code
		return res
	}
	res.errText = err.Error()
	return res
}

// resourceStopResult is ResourceControlStopResult.
type resourceStopResult struct {
	attempted bool
	ok        bool
	command   string
	args      []string
	err       string
}

// stopResourceControlledTask stops the transient unit of a systemd-run task: SIGKILL kills it, anything else stops it
// without blocking. A task that is not a systemd-run one is not attempted.
func stopResourceControlledTask(meta *resourceControlMeta, signal string, runner stopRunner) resourceStopResult {
	if meta == nil || meta.Mode != "systemd-run" || meta.UnitName == "" {
		return resourceStopResult{}
	}
	var args []string
	if signal == "SIGKILL" {
		args = []string{"--user", "kill", "--signal=" + signal, meta.UnitName}
	} else {
		args = []string{"--user", "stop", "--no-block", meta.UnitName}
	}
	if runner == nil {
		runner = systemctlRunner
	}
	result := runner("systemctl", args)
	ok := result.status != nil && *result.status == 0
	out := resourceStopResult{attempted: true, ok: ok, command: "systemctl", args: args}
	if !ok {
		switch {
		case result.errText != "":
			out.err = result.errText
		case result.hasStderr:
			out.err = result.stderr
		default:
			out.err = "systemctl failed"
		}
	}
	return out
}

// createSystemdUnitActiveProbe builds the unit liveness probe: true while the unit is active, false when `systemctl is-active`
// reports it inactive, null when it cannot be queried. An unreachable user manager answers null without a unit query.
func createSystemdUnitActiveProbe(platform func() string, run probeRunner, manager *userManagerReachability) unitActiveProbe {
	if platform == nil {
		platform = func() string { return runtime.GOOS }
	}
	if run == nil {
		run = runProbe
	}
	if manager == nil {
		manager = userManager
	}
	return func(unit string) tri {
		if unit == "" || platform() != "linux" {
			return triNull
		}
		if !manager.check() {
			return triNull
		}
		result := run("systemctl", []string{"--user", "is-active", "--quiet", unit})
		if result.kind != "exited" {
			return triNull
		}
		switch result.status {
		case 0:
			return triTrue
		case 3:
			return triFalse
		}
		return triNull
	}
}

var defaultSystemdUnitActive = createSystemdUnitActiveProbe(nil, nil, nil)
