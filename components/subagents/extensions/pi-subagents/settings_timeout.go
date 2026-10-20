package pi_subagents

import (
	"fmt"
	"math"
)

// Launch timeout defaults (src/runs/foreground/subagent-executor.ts:2943-3025) and the config helpers of
// src/extension/config.ts and src/shared/utils.ts that the settings port shares.

const (
	defaultForegroundTimeoutMs = 30 * 60 * 1000
	defaultAsyncTimeoutMs      = 30 * 60 * 1000
	// maxTimerDelayMs is the largest delay a timer honors: above the 32-bit signed ceiling the delay overflows.
	maxTimerDelayMs = 2147483647
)

func timerDelayOverflowError(name string, v any) string {
	if f, ok := v.(float64); ok && f > maxTimerDelayMs {
		return fmt.Sprintf("%s must be a positive integer no larger than %d.", name, maxTimerDelayMs)
	}
	return ""
}

// resolveConfigDefaultTimeoutMs is subagent-executor.ts:2977: the config default deadline, 0 when unset or invalid.
func resolveConfigDefaultTimeoutMs(raw any) int {
	f, ok := raw.(float64)
	if !ok || !isIntegerNum(f) || f <= 0 || f > maxTimerDelayMs {
		return 0
	}
	return int(f)
}

// launchTimeout is the resolved launch timeout: set false for none, or an error message.
type launchTimeout struct {
	timeoutMs int
	set       bool
	err       string
}

func presentParam(params map[string]any, k string) (any, bool) {
	v, ok := params[k]
	if !ok || isUndefined(v) {
		return nil, false
	}
	return v, true
}

// resolveForegroundTimeout is subagent-executor.ts:2990. defaultTimeoutMs 0 is no default.
func resolveForegroundTimeout(params map[string]any, defaultTimeoutMs int) launchTimeout {
	rawTimeout, hasTimeout := presentParam(params, "timeoutMs")
	rawMax, hasMax := presentParam(params, "maxRuntimeMs")
	if !hasTimeout && !hasMax {
		if defaultTimeoutMs == 0 {
			return launchTimeout{}
		}
		return launchTimeout{timeoutMs: defaultTimeoutMs, set: true}
	}
	for _, c := range []struct {
		name string
		v    any
		has  bool
	}{{"timeoutMs", rawTimeout, hasTimeout}, {"maxRuntimeMs", rawMax, hasMax}} {
		if !c.has {
			continue
		}
		f, ok := c.v.(float64)
		if !ok || !isIntegerNum(f) || f <= 0 {
			return launchTimeout{err: c.name + " must be a positive integer."}
		}
		if msg := timerDelayOverflowError(c.name, c.v); msg != "" {
			return launchTimeout{err: msg}
		}
	}
	if hasTimeout && hasMax && rawTimeout.(float64) != rawMax.(float64) {
		return launchTimeout{err: "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both."}
	}
	v := rawTimeout
	if !hasTimeout {
		v = rawMax
	}
	return launchTimeout{timeoutMs: int(math.Round(v.(float64))), set: true}
}

// resolveSingleAgentLaunchTimeout is subagent-executor.ts:3014. configDefaultTimeoutMs 0 is unset.
func resolveSingleAgentLaunchTimeout(params map[string]any, async bool, configDefaultTimeoutMs int) launchTimeout {
	composite := false
	for _, k := range []string{"chain", "tasks"} {
		if a, ok := params[k].([]any); ok && len(a) > 0 {
			composite = true
		}
	}
	if _, ok := presentParam(params, "workflowScript"); ok {
		composite = true
	}
	foreground, asyncSingle := defaultForegroundTimeoutMs, defaultAsyncTimeoutMs
	if configDefaultTimeoutMs != 0 {
		foreground, asyncSingle = configDefaultTimeoutMs, configDefaultTimeoutMs
	}
	def := 0
	switch {
	case !async:
		def = foreground
	case composite:
		def = 0
	default:
		def = asyncSingle
	}
	return resolveForegroundTimeout(params, def)
}

// resolveAsyncByDefault is config.ts:208: async launches are the default unless explicitly disabled.
func resolveAsyncByDefault(asyncByDefault *bool) bool {
	return asyncByDefault == nil || *asyncByDefault
}

// resolveWatchPath is utils.ts:resolveWatchPath: the canonical form of a watcher path, the path itself when it cannot be resolved.
func resolveWatchPath(watchPath string, realpath func(string) (string, error)) string {
	if p, err := realpath(watchPath); err == nil {
		return p
	}
	return watchPath
}
