package powerline_footer

import (
	"os"
	"path/filepath"
	"sync"
)

// The stash that survives /reload. The original keeps it in a `globalThis` map (ACTIVE_STASH_RELOAD_STATE_KEY) that the next
// extension generation, loaded into the same Node process, reads. PiG stops a Go extension's process on reload and starts a
// new one, so there is no memory to share: the hand-off goes through a file in the agent directory instead, with the same rules
// (keyed by session id, taken once, dropped on quit, on disable and by a session start that is not a reload).
// upstream: index.ts 202-224 (activeStashReloadState, takeActiveStashReload, transferActiveStashReload, clearActiveStashReload).

var stashReloadMu sync.Mutex

func stashReloadPath() string { return getAgentPath("powerline-footer", "active-stash-reload.json") }

func readStashReload() *jsObject {
	data, err := os.ReadFile(stashReloadPath())
	if err != nil {
		return newObject()
	}
	v, err := parseJSON(data)
	if o := asObject(v); err == nil && o != nil {
		if by := asObject(o.vals["bySessionId"]); by != nil {
			return by
		}
	}
	return newObject()
}

func writeStashReload(by *jsObject) {
	path := stashReloadPath()
	if len(by.keys) == 0 {
		_ = os.Remove(path)
		return
	}
	root := newObject()
	root.set("version", float64(1))
	root.set("bySessionId", by)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(marshalJSON(root, "  ")+"\n"), 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (o *jsObject) remove(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// takeActiveStashReload returns the stash handed over for the session, once.
func takeActiveStashReload(sessionID string) *string {
	stashReloadMu.Lock()
	defer stashReloadMu.Unlock()
	by := readStashReload()
	text, ok := by.vals[sessionID].(string)
	if !ok {
		return nil
	}
	by.remove(sessionID)
	writeStashReload(by)
	return &text
}

// transferActiveStashReload hands text (nil: nothing) to the next generation of the session.
func transferActiveStashReload(sessionID string, text *string) {
	stashReloadMu.Lock()
	defer stashReloadMu.Unlock()
	by := readStashReload()
	if text == nil {
		by.remove(sessionID)
	} else {
		by.set(sessionID, *text)
	}
	writeStashReload(by)
}

func clearActiveStashReload(sessionID string) { transferActiveStashReload(sessionID, nil) }
