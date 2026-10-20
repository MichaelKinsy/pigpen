package pi_subagents

import (
	"fmt"
	"strings"
)

// The workflow-lane part of src/runs/shared/lane-metadata.ts: launch-declared lane metadata as ordered JSON objects.

const (
	workflowLaneKeyMaxBytes        = 128
	workflowLaneSourceRefMaxBytes  = 128
	workflowLaneClaimMaxBytes      = 160
	workflowLaneClaimsMax          = 20
	workflowLaneOutputPathMaxBytes = 256
	workflowLaneOutputPathsMax     = 10
)

// laneBoundedString is boundedNonEmptyString (lane-metadata.ts:17): the trimmed string, at most maxBytes UTF-8 bytes, without
// newlines or NUL.
func laneBoundedString(value any, label string, maxBytes int) (string, error) {
	s, ok := value.(string)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("%s must be a non-empty string.", label)
	}
	n := jsTrim(s)
	if len(n) > maxBytes {
		return "", fmt.Errorf("%s exceeds %d UTF-8 bytes.", label, maxBytes)
	}
	if strings.ContainsAny(n, "\n\r\x00") {
		return "", fmt.Errorf("%s must not contain newlines or NUL bytes.", label)
	}
	return n, nil
}

func laneStringArray(value any, label string, maxItems, maxBytes int) ([]any, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array.", label)
	}
	if len(arr) > maxItems {
		return nil, fmt.Errorf("%s supports at most %d entries.", label, maxItems)
	}
	out := make([]any, len(arr))
	for i, e := range arr {
		s, err := laneBoundedString(e, fmt.Sprintf("%s[%d]", label, i), maxBytes)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// NormalizeWorkflowLaneMetadata is normalizeWorkflowLaneMetadata: it validates launch-declared lane metadata and returns the
// normalized copy; undef{} (undefined) gives nil.
func NormalizeWorkflowLaneMetadata(value any, label string) (*jsObject, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be a plain JSON object.", label)
	}
	var unknown []string
	for _, k := range o.order() {
		switch k {
		case "version", "key", "mode", "sourceRef", "claims", "outputPaths":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%s has unsupported fields: %s.", label, strings.Join(unknown, ", "))
	}
	if v, _ := o.vals["version"].(float64); v != 1 {
		return nil, fmt.Errorf("%s.version must be 1.", label)
	}
	key, err := laneBoundedString(o.vals["key"], label+".key", workflowLaneKeyMaxBytes)
	if err != nil {
		return nil, err
	}
	if !workflowKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%s.key is invalid.", label)
	}
	out := jo("version", float64(1), "key", key)
	if o.has("mode") {
		m, _ := o.vals["mode"].(string)
		if m != "mutation" && m != "review" && m != "scout" && m != "gate" {
			return nil, fmt.Errorf("%s.mode is invalid.", label)
		}
		out.set("mode", m)
	}
	if o.has("sourceRef") {
		s, err := laneBoundedString(o.vals["sourceRef"], label+".sourceRef", workflowLaneSourceRefMaxBytes)
		if err != nil {
			return nil, err
		}
		out.set("sourceRef", s)
	}
	if o.has("claims") {
		a, err := laneStringArray(o.vals["claims"], label+".claims", workflowLaneClaimsMax, workflowLaneClaimMaxBytes)
		if err != nil {
			return nil, err
		}
		out.set("claims", a)
	}
	if o.has("outputPaths") {
		a, err := laneStringArray(o.vals["outputPaths"], label+".outputPaths", workflowLaneOutputPathsMax, workflowLaneOutputPathMaxBytes)
		if err != nil {
			return nil, err
		}
		out.set("outputPaths", a)
	}
	return out, nil
}

// AssertWorkflowLaneKey is assertWorkflowLaneKey: a lane must carry the key of the workflow child it describes.
func AssertWorkflowLaneKey(lane *jsObject, workflowKey, label string) error {
	if lane == nil {
		return nil
	}
	if k, _ := lane.vals["key"].(string); k != workflowKey {
		return fmt.Errorf("%s.key '%s' does not match workflow key '%s'.", label, k, workflowKey)
	}
	return nil
}
