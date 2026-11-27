// Package cli owns the single clihook.Register registration for pig-hermes and any
// derived Piglet (such as oh-my-hermes-pig). Because PiG v0.8.0's clihook.Register
// stores one global Hook pointer, all Hermes/OMH subcommands and pre-parse flags
// register into this dispatcher instead of calling clihook.Register directly.
package cli

import (
	"sort"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
)

// SubcommandFunc executes a headless CLI subcommand and returns an exit code.
type SubcommandFunc func(env clihook.Env, args []string) int

// FlagPreprocessor inspects or transforms raw CLI args before PiG parses them.
// Returning (newArgs, handled, exitCode): if handled is true, the CLI exits with exitCode.
type FlagPreprocessor func(env clihook.Env, args []string) ([]string, bool, int)

var (
	mu           sync.RWMutex
	subcommands  = make(map[string]SubcommandFunc)
	descriptions = make(map[string]string)
	preprocs     []FlagPreprocessor
	installOnce  sync.Once
)

// Install registers this dispatcher once with PiG's clihook.
func Install() {
	installOnce.Do(func() {
		clihook.Register(dispatch)
	})
}

// RegisterSubcommand registers a top-level subcommand (e.g. "profile", "cron", "skills", "hud").
func RegisterSubcommand(name, description string, fn SubcommandFunc) {
	Install()
	mu.Lock()
	defer mu.Unlock()
	subcommands[name] = fn
	descriptions[name] = description
}

// RegisterFlagPreprocessor registers an argument preprocessor (e.g. -p/--profile or --omh-home).
func RegisterFlagPreprocessor(fn FlagPreprocessor) {
	Install()
	mu.Lock()
	defer mu.Unlock()
	preprocs = append(preprocs, fn)
}

// ListSubcommands returns sorted registered subcommand names and descriptions.
func ListSubcommands() []struct{ Name, Description string } {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(subcommands))
	for k := range subcommands {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]struct{ Name, Description string }, len(names))
	for i, n := range names {
		out[i] = struct{ Name, Description string }{Name: n, Description: descriptions[n]}
	}
	return out
}

// Dispatch runs registered preprocessors and subcommands against env.
func Dispatch(env clihook.Env) clihook.Result {
	return dispatch(env)
}

func dispatch(env clihook.Env) clihook.Result {
	mu.RLock()
	ps := make([]FlagPreprocessor, len(preprocs))
	copy(ps, preprocs)
	mu.RUnlock()

	cur := env.Args
	for _, p := range ps {
		next, done, code := p(env, cur)
		if done {
			return clihook.Result{Handled: true, Exit: code}
		}
		cur = next
	}
	if len(cur) == 0 {
		return clihook.Result{Args: cur}
	}
	mu.RLock()
	fn := subcommands[cur[0]]
	mu.RUnlock()
	if fn == nil {
		return clihook.Result{Args: cur}
	}
	return clihook.Result{Handled: true, Exit: fn(env, cur[1:])}
}
