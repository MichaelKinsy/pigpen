package pi_subagents

import (
	"regexp"
	"strings"
)

// Runtime acknowledged extensions (src/runs/shared/runtime-acknowledged-extensions.ts): the opaque ids of the extensions a
// child session reports it loaded, bounded for the result projection.
const (
	runtimeExtensionAckEvent             = "subagent:acknowledge-extension"
	maxRuntimeAcknowledgedExtensionIDs   = 32
	maxRuntimeAcknowledgedExtensionIDLen = 128
)

var runtimeAckIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@+-]+$`)

// runtimeAcknowledgedExtensions is RuntimeAcknowledgedChildExtensions.
type runtimeAcknowledgedExtensions struct {
	Version int      `json:"version"`
	Source  string   `json:"source"`
	IDs     []string `json:"ids"`
	Omitted int      `json:"omitted"`
}

func isRuntimeAcknowledgedExtensionID(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && jsLen(s) <= maxRuntimeAcknowledgedExtensionIDLen && runtimeAckIDRe.MatchString(s) &&
		!strings.Contains(s, "..")
}

// projectRuntimeAcknowledgedExtensions deduplicates, caps and counts the omitted valid ids; nil when none is valid.
func projectRuntimeAcknowledgedExtensions(ids []any) *runtimeAcknowledgedExtensions {
	var unique []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !isRuntimeAcknowledgedExtensionID(id) || seen[id.(string)] {
			continue
		}
		seen[id.(string)] = true
		unique = append(unique, id.(string))
	}
	if len(unique) == 0 {
		return nil
	}
	out := &runtimeAcknowledgedExtensions{Version: 1, Source: "child-runtime", IDs: unique, Omitted: 0}
	if len(unique) > maxRuntimeAcknowledgedExtensionIDs {
		out.IDs = unique[:maxRuntimeAcknowledgedExtensionIDs]
		out.Omitted = len(unique) - maxRuntimeAcknowledgedExtensionIDs
	}
	return out
}

// sanitizeRuntimeAcknowledgedExtensions validates a persisted projection (nil for an invalid shape).
func sanitizeRuntimeAcknowledgedExtensions(value any) *runtimeAcknowledgedExtensions {
	raw, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	if v, _ := raw.num("version"); v != 1 {
		return nil
	}
	if s, _ := raw.str("source"); s != "child-runtime" {
		return nil
	}
	ids, ok := raw.vals["ids"].([]any)
	if !ok {
		return nil
	}
	projected := projectRuntimeAcknowledgedExtensions(ids)
	if projected == nil {
		return nil
	}
	if n, ok := raw.num("omitted"); ok && n == n && n < 1e308 && n > -1e308 {
		if n > 0 {
			projected.Omitted += int(n)
		}
	}
	return projected
}
