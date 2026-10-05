package eq

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// NormalizerVersion names the reviewed rule set below and the trace
// projection. Change it whenever a rule or the projection changes so that
// recorded golden traces stay attributable.
//
//	v1  first version.
//	v2  model requests carry every tool's full definition (name, description,
//	    parameters) instead of its name, and the extension's part of the system
//	    text (difference from the host's baseline); a final quiet period
//	    records late effects before shutdown.
//	v3  N6 (Pi 1.0's bash tool reports its wall time in a tool result's
//	    structuredContent).
const NormalizerVersion = "v3"

// The normalizer removes only values that are different in every run by
// construction. It never removes an event, reorders events, or rewrites
// extension-authored text. Each rule is listed here so a reviewer can audit it:
//
//	N1  literal roots: the workspace, the run's temp root and HOME become
//	    <cwd>, <tmp> and <home> (longest first).
//	N2  UUIDs become <uuid>.
//	N3  ISO-8601 timestamps become <time>.
//	N4  in an RPC response's data only, keys that carry clocks or host
//	    identifiers are dropped: see volatileKeys. Extension-authored payloads
//	    (UI requests, tool results, messages) keep every key.
//	N5  the RPC correlation `id` of responses and UI requests is dropped
//	    (the harness assigns it).
//	N6  in a tool_execution_end result, structuredContent.wall_time_seconds
//	    becomes "<seconds>": Pi 1.0's bash tool measures the command's wall
//	    time (rounded to 0.1 s, pi-coding-agent 1.0.1 dist/core/tools/bash.js
//	    lines 284-294). Every other structured key is compared.
var volatileKeys = []string{
	"timestamp", "usage", "responseId", "sessionId", "sessionFile",
	"toolCallId", "api", "provider", "model", "durationMs", "sessionPath",
}

var (
	uuidRE = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	timeRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
)

// Replacement is one literal N1 rule.
type Replacement struct{ From, To string }

// Normalizer applies rules N1-N6.
type Normalizer struct{ Replace []Replacement }

// NewNormalizer builds rule N1 from the run's roots. Symlink-resolved forms are
// added because hosts report either.
func NewNormalizer(cwd, tmp, home string) *Normalizer {
	n := &Normalizer{}
	add := func(from, to string) {
		if from == "" {
			return
		}
		n.Replace = append(n.Replace, Replacement{from, to})
		if real, err := filepath.EvalSymlinks(from); err == nil && real != from {
			n.Replace = append(n.Replace, Replacement{real, to})
		}
	}
	add(cwd, "<cwd>")
	add(home, "<home>")
	add(tmp, "<tmp>")
	sort.SliceStable(n.Replace, func(i, j int) bool { return len(n.Replace[i].From) > len(n.Replace[j].From) })
	return n
}

// AddReplacement adds a literal N1 rule after construction (a fake upstream's host:port).
func (n *Normalizer) AddReplacement(from, to string) {
	if from == "" {
		return
	}
	n.Replace = append(n.Replace, Replacement{from, to})
	sort.SliceStable(n.Replace, func(i, j int) bool { return len(n.Replace[i].From) > len(n.Replace[j].From) })
}

func (n *Normalizer) str(s string) string {
	for _, r := range n.Replace {
		s = strings.ReplaceAll(s, r.From, r.To)
	}
	s = uuidRE.ReplaceAllString(s, "<uuid>")
	return timeRE.ReplaceAllString(s, "<time>")
}

// Value applies N1-N3 to a decoded JSON value.
func (n *Normalizer) Value(v any) any {
	switch x := v.(type) {
	case string:
		return n.str(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = n.Value(e)
		}
		return out
	case []map[string]any: // the model request's messages
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = n.Value(e)
		}
		return out
	case []string: // an exec event's args: the run's roots appear in them (git -C <cwd>)
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = n.str(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[n.str(k)] = n.Value(e)
		}
		return out
	}
	return v
}

// Data normalizes and canonically encodes an event payload.
func (n *Normalizer) Data(v any) json.RawMessage { return canonical(n.Value(v)) }

// ProjectHost maps one raw host RPC record to a trace event, or reports false
// when the record is not part of the captured set. The projection keeps what
// an extension can influence and drops host bookkeeping (N4, N5).
func (n *Normalizer) ProjectHost(rec map[string]any, capture []string) (ch string, data json.RawMessage, ok bool) {
	typ, _ := rec["type"].(string)
	if !slices.Contains(capture, typ) {
		return "", nil, false
	}
	switch typ {
	case "response":
		out := map[string]any{"command": rec["command"], "success": rec["success"]}
		if e, ok := rec["error"]; ok {
			out["error"] = e
		}
		if d, ok := rec["data"]; ok {
			out["data"] = dropVolatile(d)
		}
		return ChResponse, n.Data(out), true
	case "extension_ui_request":
		out := map[string]any{}
		for k, v := range rec {
			if k != "id" && k != "type" {
				out[k] = v
			}
		}
		return ChUI, n.Data(out), true
	case "extension_error":
		return ChHost, n.Data(map[string]any{"type": typ, "event": rec["event"], "error": rec["error"]}), true
	case "tool_execution_start":
		return ChHost, n.Data(map[string]any{"type": typ, "toolName": rec["toolName"], "args": rec["args"]}), true
	case "tool_execution_end":
		return ChHost, n.Data(map[string]any{"type": typ, "toolName": rec["toolName"], "isError": rec["isError"], "result": withoutWallTime(rec["result"])}), true
	case "message_end":
		msg, _ := rec["message"].(map[string]any)
		return ChHost, n.Data(map[string]any{"type": typ, "message": projectMessage(msg)}), true
	default: // agent_start, agent_end, turn_start, turn_end and any other lifecycle marker
		return ChHost, n.Data(map[string]any{"type": typ}), true
	}
}

// projectMessage keeps a message's role and content, the parts extensions read
// and write. Streaming bookkeeping such as usage and model ids is dropped.
func projectMessage(msg map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"role", "customType", "toolName", "isError", "display", "details", "stopReason"} {
		if v, ok := msg[k]; ok {
			out[k] = v
		}
	}
	switch c := msg["content"].(type) {
	case string:
		out["content"] = c
	case []any:
		blocks := make([]any, 0, len(c))
		for _, b := range c {
			m, _ := b.(map[string]any)
			switch m["type"] {
			case "text":
				blocks = append(blocks, map[string]any{"type": "text", "text": m["text"]})
			case "toolCall":
				blocks = append(blocks, map[string]any{"type": "toolCall", "name": m["name"], "arguments": m["arguments"]})
			default:
				blocks = append(blocks, map[string]any{"type": m["type"]})
			}
		}
		out["content"] = blocks
	}
	return out
}

// withoutWallTime applies N6 to a tool result.
func withoutWallTime(result any) any {
	r, ok := result.(map[string]any)
	if !ok {
		return result
	}
	structured, ok := r["structuredContent"].(map[string]any)
	if !ok {
		return result
	}
	if _, ok := structured["wall_time_seconds"]; !ok {
		return result
	}
	out, sc := maps.Clone(r), maps.Clone(structured)
	sc["wall_time_seconds"] = "<seconds>"
	out["structuredContent"] = sc
	return out
}

// dropVolatile applies N4 to a response's data, recursively.
func dropVolatile(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = dropVolatile(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if !slices.Contains(volatileKeys, k) {
				out[k] = dropVolatile(e)
			}
		}
		return out
	}
	return v
}
