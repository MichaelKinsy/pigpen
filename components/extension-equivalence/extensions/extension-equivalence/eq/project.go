package eq

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// SetProjection installs the scenario's opt-in projection (nil: the original one).
func (n *Normalizer) SetProjection(p *Projection) {
	n.proj = p
	n.dropPats = nil
	if p != nil {
		n.dropPats = splitDrop(p.Drop)
	}
}

// recordsWhole reports whether a captured host event of this type is recorded with all its keys.
func (n *Normalizer) recordsWhole(typ string) bool {
	if slices.Contains(DefaultFullEvents, typ) {
		return true
	}
	return n.proj != nil && slices.Contains(n.proj.Full, typ)
}

// wholeClockKeys are the keys a whole event drops: the clocks and host identifiers N4 names for a
// response, without N4's usage/model/provider keys, which a whole event's messages need.
var wholeClockKeys = []string{"timestamp", "sessionId", "sessionFile", "sessionPath"}

// wholeEvent is the record with its clocks dropped and its entry ids relabeled.
func (n *Normalizer) wholeEvent(rec map[string]any) any {
	v := dropKeys(rec, wholeClockKeys)
	if len(n.dropPats) > 0 {
		v = dropPaths(v, n.dropPats, nil)
	}
	return n.relabelIDs(v)
}

func dropKeys(v any, keys []string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = dropKeys(e, keys)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if !slices.Contains(keys, k) {
				out[k] = dropKeys(e, keys)
			}
		}
		return out
	}
	return v
}

// entryIDRE is the shape of a session entry id (Pi: eight lowercase hex digits).
var entryIDRE = regexp.MustCompile(`^[0-9a-f]{8}$`)

// idKeys are the keys whose string value is a session entry id.
var idKeys = []string{"id", "parentId", "firstKeptEntryId", "targetId", "leafId", "entryId", "fromId", "since"}

// idResponses are the commands whose response data carries entry ids.
var idResponses = []string{"get_entries", "get_tree", "get_fork_messages"}

func (n *Normalizer) relabelResponse(command, data any) any {
	if c, _ := command.(string); slices.Contains(idResponses, c) {
		return n.relabelIDs(data)
	}
	return data
}

// relabelIDs replaces every session entry id under an id key by <id:N>, N counting the distinct ids in order
// of first appearance in the trace. Entry ids are random per run; their structure (which entry points at which)
// is what a trace compares. Other strings are untouched: a model-visible text that quotes an id needs the
// scenario's own normalize rule.
func (n *Normalizer) relabelIDs(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = n.relabelIDs(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		// Sorted keys: the label an id gets must not depend on map iteration order.
		for _, k := range slices.Sorted(maps.Keys(x)) {
			e := x[k]
			if s, ok := e.(string); ok && entryIDRE.MatchString(s) && slices.Contains(idKeys, k) {
				out[k] = n.idLabel(s)
				continue
			}
			// Posthorse's fork manifests list the entry ids a seed is anchored to (`anchors`: an array of ids).
			if list, ok := e.([]any); ok && k == "anchors" {
				labeled := make([]any, len(list))
				for i, item := range list {
					labeled[i] = item
					if s, ok := item.(string); ok && entryIDRE.MatchString(s) {
						labeled[i] = n.idLabel(s)
					}
				}
				out[k] = labeled
				continue
			}
			out[k] = n.relabelIDs(e)
		}
		return out
	}
	return v
}

func (n *Normalizer) idLabel(id string) string {
	n.idMu.Lock()
	defer n.idMu.Unlock()
	if l, ok := n.ids[id]; ok {
		return l
	}
	if n.ids == nil {
		n.ids = map[string]string{}
	}
	l := fmt.Sprintf("<id:%d>", len(n.ids)+1)
	n.ids[id] = l
	return l
}

// imageBlock describes an image content block: media type, decoded byte count and the first 16 hex digits of
// the decoded bytes' SHA-256 (the model server's llm projection uses the same digest).
func imageBlock(m map[string]any) map[string]any {
	out := map[string]any{"type": "image", "mimeType": m["mimeType"]}
	if data, ok := m["data"].(string); ok {
		if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
			sum := sha256.Sum256(raw)
			out["bytes"] = len(raw)
			out["sha256"] = hex.EncodeToString(sum[:])[:16]
		} else {
			out["dataChars"] = len(data)
		}
	}
	return out
}
