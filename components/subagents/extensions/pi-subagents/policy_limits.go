package pi_subagents

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Port of the limits and paths of src/shared/types.ts: the recursion guard, the spawn caps, the top-level parallel
// settings and the temp directories.

const (
	defaultSubagentMaxDepth     = 2
	subagentMaxDepthEnv         = "PI_SUBAGENT_MAX_DEPTH"
	defaultMaxSubagentSpawnsRun = 64
	maxParallel                 = 8
	maxConcurrency              = 4 // the original's MAX_CONCURRENCY (maxChildren in run.go)
)

// envLookup reads an environment variable (os.LookupEnv in production; tests pass a map).
type envLookup func(string) (string, bool)

// osEnv is the process environment.
var osEnv envLookup = os.LookupEnv

// normalizeNonNegativeInteger is the original's helper: a number, or a string Number() reads, that is an integer >= 0.
func normalizeNonNegativeInteger(value any) (int, bool) {
	var f float64
	if str, ok := value.(string); ok {
		f = jsStringToNumber(str)
	} else if n, ok := asNumber(value); ok {
		f = n
	} else {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

func normalizeMaxSubagentDepth(value any) (int, bool) { return normalizeNonNegativeInteger(value) }

// subagentDepthContext is the original's SubagentDepthContext (the executor's own child runtime).
type subagentDepthContext struct {
	Depth       float64
	MaxDepth    any // nil: none
	HasMaxDepth bool
}

func resolveCurrentMaxSubagentDepth(configMaxDepth any, runtime *subagentDepthContext, getenv envLookup) int {
	var source any
	if runtime != nil {
		if runtime.HasMaxDepth {
			source = runtime.MaxDepth
		}
	} else if v, ok := getenv(subagentMaxDepthEnv); ok {
		source = v
	}
	if n, ok := normalizeMaxSubagentDepth(source); ok {
		return n
	}
	if n, ok := normalizeMaxSubagentDepth(configMaxDepth); ok {
		return n
	}
	return defaultSubagentMaxDepth
}

func resolveChildMaxSubagentDepth(parentMaxDepth int, agentMaxDepth any) int {
	parent := defaultSubagentMaxDepth
	if n, ok := normalizeMaxSubagentDepth(parentMaxDepth); ok {
		parent = n
	}
	if a, ok := normalizeMaxSubagentDepth(agentMaxDepth); ok && a < parent {
		return a
	}
	return parent
}

func resolveCurrentSubagentDepth(runtime *subagentDepthContext) float64 {
	if runtime == nil || math.IsNaN(runtime.Depth) || math.IsInf(runtime.Depth, 0) {
		return 0
	}
	return runtime.Depth
}

type depthCheck struct {
	Blocked  bool
	Depth    float64
	MaxDepth int
}

// checkSubagentDepth is the original's checkSubagentDepth. getenv reads PI_SUBAGENT_MAX_DEPTH (os.Getenv in production).
func checkSubagentDepth(configMaxDepth any, runtime *subagentDepthContext, getenv envLookup) depthCheck {
	depth := resolveCurrentSubagentDepth(runtime)
	max := resolveCurrentMaxSubagentDepth(configMaxDepth, runtime, getenv)
	return depthCheck{Blocked: depth >= float64(max), Depth: depth, MaxDepth: max}
}

// resolveChildDepth is the depth context of a child launched by an executor at runtime.
func resolveChildDepth(maxDepth any, runtime *subagentDepthContext, getenv envLookup) (depth float64, max int) {
	depth = resolveCurrentSubagentDepth(runtime) + 1
	if n, ok := normalizeMaxSubagentDepth(maxDepth); ok {
		return depth, n
	}
	return depth, resolveCurrentMaxSubagentDepth(nil, runtime, getenv)
}

func normalizeMaxSubagentSpawnsPerSession(value any) (int, bool) {
	return normalizeNonNegativeInteger(value)
}

// resolveMaxSubagentSpawnsPerSession is the cap on children per session: 0 and unset are unlimited (ok false).
func resolveMaxSubagentSpawnsPerSession(configMax any, getenv envLookup) (int, bool) {
	if v, set := getenv("PI_SUBAGENT_MAX_SPAWNS_PER_SESSION"); set {
		if env, ok := normalizeMaxSubagentSpawnsPerSession(v); ok {
			return env, env != 0
		}
	}
	n, ok := normalizeMaxSubagentSpawnsPerSession(configMax)
	return n, ok && n != 0
}

func normalizeMaxSubagentSpawnsPerRun(value any) (int, bool) {
	n, ok := normalizeNonNegativeInteger(value)
	return n, ok && n > 0
}

func resolveMaxSubagentSpawnsPerRun(configMax any, getenv envLookup) int {
	if v, set := getenv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN"); set {
		if n, ok := normalizeMaxSubagentSpawnsPerRun(v); ok {
			return n
		}
	}
	if n, ok := normalizeMaxSubagentSpawnsPerRun(configMax); ok {
		return n
	}
	return defaultMaxSubagentSpawnsRun
}

func normalizeTopLevelParallelValue(value any) (int, bool) {
	var f float64
	if str, ok := value.(string); ok {
		f = jsStringToNumber(str)
	} else if n, ok := asNumber(value); ok {
		f = n
	} else {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

func resolveTopLevelParallelMaxTasks(value any) int {
	if n, ok := normalizeTopLevelParallelValue(value); ok {
		return n
	}
	return maxParallel
}

func resolveTopLevelParallelConcurrency(override, config any) int {
	if n, ok := normalizeTopLevelParallelValue(override); ok {
		return n
	}
	if n, ok := normalizeTopLevelParallelValue(config); ok {
		return n
	}
	return maxConcurrency
}

// ---- temp paths -----------------------------------------------------------------------------------------------------

var tempScopeUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeTempScopeSegment(value string) string {
	s := tempScopeUnsafe.ReplaceAllString(jsTrim(value), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "unknown"
	}
	return s
}

// tempScopeOptions are the seams of resolveTempScopeId (the original's options): nil functions are unavailable.
type tempScopeOptions struct {
	Env      map[string]string
	Getuid   func() int
	UserInfo func() (string, error)
	Homedir  func() (string, error)
}

// resolveTempScopeID is the original's resolveTempScopeId: "uid-<uid>" where the OS has uids, else a user or home name.
func resolveTempScopeID(o *tempScopeOptions) string {
	if o == nil {
		env := map[string]string{}
		for _, k := range []string{"USERNAME", "USER", "LOGNAME", "USERPROFILE", "HOME"} {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		o = &tempScopeOptions{Env: env, UserInfo: osUserName, Homedir: os.UserHomeDir}
		if uid := os.Getuid(); uid >= 0 {
			o.Getuid = func() int { return uid }
		}
	}
	if o.Getuid != nil {
		return "uid-" + strconv.Itoa(o.Getuid())
	}
	for _, key := range []string{"USERNAME", "USER", "LOGNAME"} {
		if v := o.Env[key]; v != "" {
			return "user-" + sanitizeTempScopeSegment(v)
		}
	}
	if o.UserInfo != nil {
		if name, err := o.UserInfo(); err == nil && name != "" {
			return "user-" + sanitizeTempScopeSegment(name)
		}
	}
	home := o.Env["USERPROFILE"]
	if home == "" {
		home = o.Env["HOME"]
	}
	if home != "" {
		return "home-" + sanitizeTempScopeSegment(home)
	}
	if o.Homedir != nil {
		if h, err := o.Homedir(); err == nil && h != "" {
			return "home-" + sanitizeTempScopeSegment(h)
		}
	}
	return "shared"
}

// tempRootFor is TEMP_ROOT_DIR for an environment value of PI_SUBAGENTS_TEMP_ROOT.
func tempRootFor(configured, tmpdir, scope string) string {
	if c := jsTrim(configured); c != "" {
		abs, err := filepath.Abs(c)
		if err != nil {
			return filepath.Clean(c)
		}
		return abs
	}
	return filepath.Join(tmpdir, "pi-subagents-"+scope)
}

// tempRootDir is TEMP_ROOT_DIR of this process.
func tempRootDir() string {
	return tempRootFor(os.Getenv("PI_SUBAGENTS_TEMP_ROOT"), os.TempDir(), resolveTempScopeID(nil))
}

// The original's scoped temp directories, all directly under TEMP_ROOT_DIR.
func resultsDir() string       { return filepath.Join(tempRootDir(), "async-subagent-results") }
func asyncDir() string         { return filepath.Join(tempRootDir(), "async-subagent-runs") }
func chainRunsDir() string     { return filepath.Join(tempRootDir(), "chain-runs") }
func tempArtifactsDir() string { return filepath.Join(tempRootDir(), "artifacts") }

// asyncConfigPath is getAsyncConfigPath.
func asyncRunConfigPath(suffix string) string {
	return filepath.Join(tempRootDir(), "async-cfg-"+suffix+".json")
}
