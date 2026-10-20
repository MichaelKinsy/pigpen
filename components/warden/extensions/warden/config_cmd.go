package warden

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `/warden config` (extension.ts 2831-2859): `set <key> <value>`, `get <key>` and, with no argument, the editor overlay.
// Values are written to the user file only; the project file and the environment never change.

var errNoConfigPath = errors.New("the PiG config directory could not be found")

// defaultAt is JSON.stringify of the default config's value at a dotted path, in the order the original writes its
// defaults, or "undefined" when the path names nothing.
func defaultAt(path string) string {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		return "undefined"
	}
	current := json.RawMessage(data)
	for _, key := range strings.Split(path, ".") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return "undefined"
		}
		next, ok := obj[key]
		if !ok {
			return "undefined"
		}
		current = next
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, current); err != nil {
		return "undefined"
	}
	return buf.String()
}

func (w *warden) configCommand(ctx sdk.Context, tokens []string) error {
	tail := strings.Join(tokens, " ")
	// A bare "set" or "get" is an incomplete command, not a request for the panel; tell the user rather than opening it.
	if tail == "set" || tail == "get" {
		w.say(ctx, "Usage: /warden config set <key> <value> or /warden config get <key>", "warning")
		return nil
	}
	w.mu.Lock()
	path := w.cfgPath
	w.mu.Unlock()
	if path == "" {
		w.say(ctx, "warden: could not find the PiG config directory; nothing was changed.", "warning")
		return nil
	}
	switch {
	case strings.HasPrefix(tail, "set "):
		rest := jsTrim(tail[4:])
		space := strings.Index(rest, " ")
		if space == -1 {
			w.say(ctx, "Usage: /warden config set <key> <value>", "warning")
			return nil
		}
		keyPath, rawValue := jsTrim(rest[:space]), jsTrim(rest[space+1:])
		value := ParseConfigValue(rawValue)
		if _, err := Serialize("config:"+path, func() (struct{}, error) {
			return struct{}{}, WriteUserConfig(path, SetNestedValue(ReadUserConfig(path), keyPath, value))
		}); err != nil {
			return err
		}
		w.say(ctx, "Saved "+keyPath+" = "+jsStringify(value)+".", "info")
	case strings.HasPrefix(tail, "get "):
		keyPath := jsTrim(tail[4:])
		value := GetNestedValue(ReadUserConfig(path), keyPath)
		if value == nil && !hasNestedKey(ReadUserConfig(path), keyPath) {
			w.say(ctx, keyPath+" not set (default: "+defaultAt(keyPath)+")", "info")
		} else {
			w.say(ctx, keyPath+" = "+jsStringify(value), "info")
		}
	default:
		if !ctx.HasUI() {
			w.say(ctx, "Edit "+path+" directly. Use /warden config set <key> <value> for quick changes.", "info")
			return nil
		}
		w.toggleConfig(ctx, w.config(ctx))
	}
	return nil
}

// hasNestedKey tells a key set to null from a key that is absent (JavaScript: undefined versus null).
func hasNestedKey(obj map[string]any, path string) bool {
	keys := strings.Split(path, ".")
	var current any = obj
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return false
		}
		v, present := m[key]
		if !present {
			return false
		}
		current = v
	}
	return true
}
