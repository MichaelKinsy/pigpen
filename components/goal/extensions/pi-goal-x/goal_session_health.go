package pi_goal_x

import (
	"os"
	"regexp"
	"strings"
)

// ported from goal-session-health.ts: read-only checkpoint health diagnostics of a session JSONL (issue #30).

// v2MarkerPattern is the exact shape written by checkpointTriggerPrompt (v2 markers only).
var v2MarkerPattern = regexp.MustCompile(`^<pi_goal_continuation` + jsS + `+goal_id="[^"]+"` + jsS + `+kind="checkpoint"` + jsS + `+v="2"` + jsS + `*/>$`)

// repairedMarkerEstimateChars is the approximate persisted size of one repaired marker, used for projections.
var repairedMarkerEstimateChars = utf16Len(`<pi_goal_continuation goal_id=xxxxxxxxxxxxxxxxxxxx kind="checkpoint" v="2"/>`)

type checkpointHealth struct {
	total, legacyFull, v2Minimal, totalContentChars, recoverableChars, largestCheckpointChars int
}

// sessionRecord is asRecord: a JSON object, else nil.
func sessionRecord(v any) *jsObject {
	o, _ := v.(*jsObject)
	return o
}

// checkpointEntry returns the content and details.version of a pi-goal-event custom message.
func checkpointEntry(entry any) (content string, version float64, hasVersion bool, ok bool) {
	raw := sessionRecord(entry)
	if raw == nil || raw.vals["type"] != any("custom_message") || raw.vals["customType"] != any(goalEventEntry) {
		return "", 0, false, false
	}
	content, isStr := raw.vals["content"].(string)
	if !isStr {
		return "", 0, false, false
	}
	if d := sessionRecord(raw.vals["details"]); d != nil {
		version, hasVersion = d.vals["version"].(float64)
	}
	return content, version, hasVersion, true
}

// inspectCheckpointHealth classifies every pi-goal-event custom message: a v2 entry matches the bounded marker shape and
// carries details.version 2 (or 3); everything else is a legacy full prompt that offline recovery can shrink.
func inspectCheckpointHealth(entries []any) *checkpointHealth {
	h := &checkpointHealth{}
	for _, e := range entries {
		accumulateCheckpointHealth(h, e)
	}
	return h
}

func accumulateCheckpointHealth(h *checkpointHealth, entry any) {
	content, version, hasVersion, ok := checkpointEntry(entry)
	if !ok {
		return
	}
	n := utf16Len(content)
	h.total++
	h.totalContentChars += n
	if n > h.largestCheckpointChars {
		h.largestCheckpointChars = n
	}
	if hasVersion && (version == 2 || version == 3) && v2MarkerPattern.MatchString(content) {
		h.v2Minimal++
	} else {
		h.legacyFull++
		h.recoverableChars += n
	}
}

// readSessionCheckpointHealth reads a session JSONL (header included); nil when the file cannot be read. Malformed lines are
// skipped.
func readSessionCheckpointHealth(sessionFile string) *checkpointHealth {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil
	}
	h := &checkpointHealth{}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := jsTrim(rawLine)
		if line == "" {
			continue
		}
		if v, err := parseJSON([]byte(line)); err == nil {
			accumulateCheckpointHealth(h, v)
		}
	}
	return h
}

func projectedContentCharsAfterRecovery(h *checkpointHealth) int {
	return h.v2Minimal * repairedMarkerEstimateChars
}

func formatChars(chars int) string {
	switch {
	case chars >= 1_000_000:
		return jsToFixed(float64(chars)/1_000_000, 1) + " MB"
	case chars >= 1_000:
		return jsToFixed(float64(chars)/1_000, 1) + " KB"
	}
	return jsNumber(float64(chars)) + " chars"
}

// formatCheckpointHealthReport renders the read-only report section; sessionFile nil = undefined.
func formatCheckpointHealthReport(h *checkpointHealth, sessionFile *string) string {
	lines := []string{
		"Session checkpoints:",
		"  total: " + jsNumber(float64(h.total)),
		"  legacy full checkpoints: " + jsNumber(float64(h.legacyFull)),
		"  v2 minimal markers: " + jsNumber(float64(h.v2Minimal)),
		"  checkpoint content: " + formatChars(h.totalContentChars),
		"  projected content after recovery: " + formatChars(projectedContentCharsAfterRecovery(h)),
	}
	if h.legacyFull > 0 && sessionFile != nil && *sessionFile != "" {
		lines = append(lines, "  close Pi, then run:", "    pi-goal-x-recover --session <path> --dry-run")
	}
	return strings.Join(lines, "\n")
}
