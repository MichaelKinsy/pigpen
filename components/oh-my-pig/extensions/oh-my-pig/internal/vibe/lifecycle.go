package vibe

import (
	"encoding/json"
	"math"
	"regexp"
)

// upstream: packages/coding-agent/src/vibe/lifecycle.ts:1-106

// LifecycleCustomType is the custom-entry type tag for persisted Vibe lifecycle events.
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:14
const LifecycleCustomType = "vibe-session-lifecycle"

// LifecycleVersion is the schema version for persisted Vibe lifecycle events.
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:16
const LifecycleVersion = 1

// TombstoneReason explains why a Vibe worker session was terminated.
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:18
type TombstoneReason string

const (
	TombstoneExplicitKill  TombstoneReason = "explicit-kill"
	TombstoneModeExit      TombstoneReason = "mode-exit"
	TombstoneSpawnFailed   TombstoneReason = "spawn-failed"
	TombstoneUnrecoverable TombstoneReason = "unrecoverable"
)

// LifecycleAction discriminates the four Vibe lifecycle event shapes.
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:27-53
type LifecycleAction string

const (
	ActionSpawn            LifecycleAction = "spawn"
	ActionTurnStarted      LifecycleAction = "turn-started"
	ActionTurnSettled      LifecycleAction = "turn-settled"
	ActionTombstone        LifecycleAction = "tombstone"
	ActionTombstoneRevoked LifecycleAction = "tombstone-revoked"
)

// LifecycleEvent is one persisted `vibe-session-lifecycle` custom-entry payload.
// Fields unused by a given Action are omitted when marshaled.
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:20-53
type LifecycleEvent struct {
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	OwnerID          string          `json:"ownerId"`
	ParentSessionID  string          `json:"parentSessionId"`
	Action           LifecycleAction `json:"action"`
	CLI              CLI             `json:"cli,omitempty"`
	Agent            string          `json:"agent,omitempty"`
	ChildSessionFile string          `json:"childSessionFile,omitempty"`
	CreatedAt        int64           `json:"createdAt,omitempty"`
	Turn             int             `json:"turn,omitempty"`
	Reason           TombstoneReason `json:"reason,omitempty"`
}

// ToMap converts ev into the exact flat JSON object matching upstream's union
// variants (no extraneous keys for unrelated variants).
func (ev LifecycleEvent) ToMap() map[string]any {
	m := map[string]any{
		"version":         ev.Version,
		"id":              ev.ID,
		"ownerId":         ev.OwnerID,
		"parentSessionId": ev.ParentSessionID,
		"action":          string(ev.Action),
	}
	switch ev.Action {
	case ActionSpawn:
		m["cli"] = string(ev.CLI)
		m["agent"] = ev.Agent
		m["childSessionFile"] = ev.ChildSessionFile
		m["createdAt"] = ev.CreatedAt
	case ActionTurnStarted, ActionTurnSettled:
		m["turn"] = ev.Turn
	case ActionTombstone, ActionTombstoneRevoked:
		m["reason"] = string(ev.Reason)
	}
	return m
}

var validChildIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ParseLifecycleEvent validates and decodes one persisted lifecycle payload.
// Returns (zero, false) for foreign or malformed data, matching upstream's
// arktype `LifecycleEvent` schema (unknown extra keys are ignored via `"+": "delete"`).
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:62-89
func ParseLifecycleEvent(value any) (LifecycleEvent, bool) {
	m, ok := toObjectMap(value)
	if !ok {
		return LifecycleEvent{}, false
	}
	ver, ok := strictInt(m["version"])
	if !ok || ver != LifecycleVersion {
		return LifecycleEvent{}, false
	}
	id, ok := nonEmptyString(m["id"])
	if !ok {
		return LifecycleEvent{}, false
	}
	ownerID, ok := nonEmptyString(m["ownerId"])
	if !ok {
		return LifecycleEvent{}, false
	}
	parentSessionID, ok := nonEmptyString(m["parentSessionId"])
	if !ok {
		return LifecycleEvent{}, false
	}
	rawAction, ok := m["action"].(string)
	if !ok {
		return LifecycleEvent{}, false
	}
	ev := LifecycleEvent{
		Version:         LifecycleVersion,
		ID:              id,
		OwnerID:         ownerID,
		ParentSessionID: parentSessionID,
		Action:          LifecycleAction(rawAction),
	}
	switch ev.Action {
	case ActionSpawn:
		rawCLI, ok := m["cli"].(string)
		if !ok || !CLI(rawCLI).Valid() {
			return LifecycleEvent{}, false
		}
		agent, ok := m["agent"].(string)
		if !ok {
			return LifecycleEvent{}, false
		}
		childFile, ok := m["childSessionFile"].(string)
		if !ok {
			return LifecycleEvent{}, false
		}
		createdAt, ok := finiteNumberInt64(m["createdAt"])
		if !ok {
			return LifecycleEvent{}, false
		}
		ev.CLI = CLI(rawCLI)
		ev.Agent = agent
		ev.ChildSessionFile = childFile
		ev.CreatedAt = createdAt
		return ev, true

	case ActionTurnStarted, ActionTurnSettled:
		turn, ok := strictInt(m["turn"])
		if !ok || turn < 1 {
			return LifecycleEvent{}, false
		}
		ev.Turn = turn
		return ev, true

	case ActionTombstone:
		rawReason, ok := m["reason"].(string)
		if !ok {
			return LifecycleEvent{}, false
		}
		reason := TombstoneReason(rawReason)
		switch reason {
		case TombstoneExplicitKill, TombstoneModeExit, TombstoneSpawnFailed, TombstoneUnrecoverable:
			ev.Reason = reason
			return ev, true
		default:
			return LifecycleEvent{}, false
		}

	case ActionTombstoneRevoked:
		rawReason, ok := m["reason"].(string)
		if !ok || TombstoneReason(rawReason) != TombstoneModeExit {
			return LifecycleEvent{}, false
		}
		ev.Reason = TombstoneModeExit
		return ev, true

	default:
		return LifecycleEvent{}, false
	}
}

// PersistedVibeChildIDs returns the set of child IDs claimed by valid Vibe spawn
// records from untrusted persisted JSON entries (either decoded `map[string]any`
// or `json.RawMessage`).
// upstream: packages/coding-agent/src/vibe/lifecycle.ts:92-105
func PersistedVibeChildIDs(entries []any) map[string]bool {
	ids := make(map[string]bool)
	for _, value := range entries {
		entry, ok := toObjectMap(value)
		if !ok {
			continue
		}
		if t, _ := entry["type"].(string); t != "custom" {
			continue
		}
		if ct, _ := entry["customType"].(string); ct != LifecycleCustomType {
			continue
		}
		ev, ok := ParseLifecycleEvent(entry["data"])
		if !ok {
			continue
		}
		if ev.Action == ActionSpawn &&
			validChildIDRE.MatchString(ev.ID) &&
			ev.ChildSessionFile == ev.ID+".jsonl" {
			ids[ev.ID] = true
		}
	}
	return ids
}

func toObjectMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		if v == nil {
			return nil, false
		}
		return v, true
	case json.RawMessage:
		if len(v) == 0 {
			return nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil || m == nil {
			return nil, false
		}
		return m, true
	case []byte:
		if len(v) == 0 {
			return nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil || m == nil {
			return nil, false
		}
		return m, true
	case LifecycleEvent:
		return v.ToMap(), true
	case *LifecycleEvent:
		if v == nil {
			return nil, false
		}
		return v.ToMap(), true
	default:
		return nil, false
	}
}

func nonEmptyString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) == 0 {
		return "", false
	}
	return s, true
}

func strictInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, false
		}
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		if f, err := n.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f {
			return int(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func finiteNumberInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}
