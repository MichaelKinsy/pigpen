package pi_subagents

import (
	"math"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Port of src/runs/shared/nested-path.ts.

const (
	maxNestedIDLength    = 128
	maxNestedPathEntries = 4
)

// nestedPathEntry is NestedPathEntry: one run on the path from the root to a nested run.
type nestedPathEntry struct {
	RunID     string `json:"runId"`
	StepIndex *int   `json:"stepIndex,omitempty"`
	Agent     string `json:"agent,omitempty"`
}

// jsSlice is s.slice(0, max) on UTF-16 units.
func jsSlice(s string, max int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= max {
		return s
	}
	return string(utf16.Decode(u[:max]))
}

func isSafeNestedPathID(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && jsLength(s) <= maxNestedIDLength && !filepath.IsAbs(s) && !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}

// sanitizeNestedPath keeps the well-formed entries of a decoded path (at most four); anything else is an empty path.
func sanitizeNestedPath(v any) []nestedPathEntry {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []nestedPathEntry
	for _, part := range arr {
		rec, ok := part.(map[string]any)
		if !ok || !isSafeNestedPathID(rec["runId"]) {
			continue
		}
		e := nestedPathEntry{RunID: rec["runId"].(string)}
		if f, ok := rec["stepIndex"].(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
			i := int(f)
			e.StepIndex = &i
		}
		if a, ok := rec["agent"].(string); ok && a != "" {
			e.Agent = jsSlice(a, 128)
		}
		out = append(out, e)
	}
	if len(out) > maxNestedPathEntries {
		out = out[:maxNestedPathEntries]
	}
	return out
}
