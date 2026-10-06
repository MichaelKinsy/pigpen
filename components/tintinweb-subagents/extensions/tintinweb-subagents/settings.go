package tintinweb_subagents

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// The subagents.json settings this port honors: maxConcurrent, defaultMaxTurns, graceTurns, fallbackSubagent and
// disableDefaultAgents. The global file (<agent dir>/subagents.json) is read first, the project file
// (<cwd>/.pi/subagents.json) overrides it. A field of the wrong shape is dropped, silently, as in the original.
// upstream: src/settings.ts (sanitize, loadSettings).
type settings struct {
	MaxConcurrent        int // 0: unset
	DefaultMaxTurns      *int
	GraceTurns           int // 0: unset
	FallbackSubagent     *string
	DisableDefaultAgents *bool
}

const (
	maxConcurrentCeiling = 1024
	maxTurnsCeiling      = 10_000
	graceTurnsCeiling    = 1_000
)

func integer(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int(f), true
}

func sanitizeSettings(raw any) settings {
	var out settings
	r, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	if n, ok := integer(r["maxConcurrent"]); ok && n >= 1 && n <= maxConcurrentCeiling {
		out.MaxConcurrent = n
	}
	if n, ok := integer(r["defaultMaxTurns"]); ok && n >= 0 && n <= maxTurnsCeiling {
		out.DefaultMaxTurns = &n
	}
	if n, ok := integer(r["graceTurns"]); ok && n >= 1 && n <= graceTurnsCeiling {
		out.GraceTurns = n
	}
	if b, ok := r["disableDefaultAgents"].(bool); ok {
		out.DisableDefaultAgents = &b
	}
	switch v := r["fallbackSubagent"].(type) {
	case bool:
		if !v { // `false` is a spelling of "none"
			n := noFallback
			out.FallbackSubagent = &n
		}
	case string:
		if s := strings.TrimSpace(v); s != "" {
			out.FallbackSubagent = &s
		}
	}
	return out
}

func readSettingsFile(path string) settings {
	data, err := os.ReadFile(path)
	if err != nil {
		return settings{}
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return settings{}
	}
	return sanitizeSettings(raw)
}

// loadSettings merges the global and the project file, the project one winning field by field.
func loadSettings(cwd string) settings {
	s := readSettingsFile(filepath.Join(agentDir(), "subagents.json"))
	p := readSettingsFile(filepath.Join(cwd, ".pi", "subagents.json"))
	if p.MaxConcurrent != 0 {
		s.MaxConcurrent = p.MaxConcurrent
	}
	if p.DefaultMaxTurns != nil {
		s.DefaultMaxTurns = p.DefaultMaxTurns
	}
	if p.GraceTurns != 0 {
		s.GraceTurns = p.GraceTurns
	}
	if p.FallbackSubagent != nil {
		s.FallbackSubagent = p.FallbackSubagent
	}
	if p.DisableDefaultAgents != nil {
		s.DisableDefaultAgents = p.DisableDefaultAgents
	}
	return s
}
