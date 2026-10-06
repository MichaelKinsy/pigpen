package ponytail

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
)

// The shared configuration resolver. upstream: hooks/ponytail-config.js.
//
// Default mode: PONYTAIL_DEFAULT_MODE, else defaultMode in the config file ($XDG_CONFIG_HOME/ponytail/config.json,
// ~/.config/ponytail/config.json, %APPDATA%\ponytail\config.json on Windows), else full.

const defaultMode = "full"

var (
	validModes   = []string{"off", "lite", "full", "ultra", "review"}
	runtimeModes = []string{"off", "lite", "full", "ultra"}
)

// isJSSpace is JavaScript's whitespace (String.prototype.trim and \s): Unicode spaces and the byte-order mark.
func isJSSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\ufeff'
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// normalizeMode is a runtime level (off, lite, full, ultra), or "".
func normalizeMode(mode string) string {
	n := strings.ToLower(jsTrim(mode))
	if slices.Contains(runtimeModes, n) {
		return n
	}
	return ""
}

// normalizeConfigMode is any valid mode (review included), or "".
func normalizeConfigMode(mode string) string {
	n := strings.ToLower(jsTrim(mode))
	if slices.Contains(validModes, n) {
		return n
	}
	return ""
}

func normalizePersistedMode(mode string) string {
	if m := normalizeMode(mode); m != "" {
		return m
	}
	return normalizeConfigMode(mode)
}

// isDeactivationCommand: "stop ponytail" and "normal mode" turn ponytail off, but only as the whole message
// (case and trailing punctuation aside), so "add a normal mode toggle" does not.
func isDeactivationCommand(text string) bool {
	t := strings.ToLower(jsTrim(text))
	t = strings.TrimRightFunc(t, func(r rune) bool { return r == '.' || r == '!' || r == '?' || isJSSpace(r) })
	return t == "stop ponytail" || t == "normal mode"
}

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ponytail")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "ponytail")
		}
		return filepath.Join(home, "AppData", "Roaming", "ponytail")
	}
	return filepath.Join(home, ".config", "ponytail")
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

// readConfig is the config file's object; ok is false when it is missing or is not valid JSON. A UTF-8 byte-order
// mark (common on Windows-saved files) is stripped.
func readConfig() (cfg map[string]any, ok bool) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, false
	}
	if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &cfg) != nil {
		return nil, false
	}
	return cfg, true // a JSON value that is not an object (null, a list) has no fields, as in the original
}

func getDefaultMode() string {
	if env := os.Getenv("PONYTAIL_DEFAULT_MODE"); env != "" && slices.Contains(runtimeModes, strings.ToLower(env)) {
		return strings.ToLower(env)
	}
	if cfg, ok := readConfig(); ok {
		if s, isStr := cfg["defaultMode"].(string); isStr && slices.Contains(runtimeModes, strings.ToLower(s)) {
			return strings.ToLower(s)
		}
	}
	return defaultMode
}

// flag reads a boolean setting: the variable when it is set (anything but "", "0", "false", "no" is on), else the
// config field when it is exactly true.
func flag(env, field string) bool {
	if v, set := os.LookupEnv(env); set {
		switch strings.ToLower(jsTrim(v)) {
		case "", "0", "false", "no":
			return false
		}
		return true
	}
	cfg, _ := readConfig()
	return cfg[field] == true
}

// getQuietStartup silences the "Ponytail loaded" notice while keeping ponytail active.
func getQuietStartup() bool { return flag("PONYTAIL_QUIET_STARTUP", "quietStartup") }

// getHideStatus hides the status-bar indicator while keeping ponytail active (#324).
func getHideStatus() bool { return flag("PONYTAIL_HIDE_STATUS", "hideStatus") }

// writeDefaultMode saves a runtime level as the default, keeping the file's other fields in their order. review is
// session-only and is refused (#377): the result is "" with no error.
func writeDefaultMode(mode string) (string, error) {
	n := normalizeMode(mode)
	if n == "" {
		return "", nil
	}
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	var keys []string
	vals := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		keys, vals = orderedObject(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	}
	if _, has := vals["defaultMode"]; !has {
		keys = append(keys, "defaultMode")
	}
	vals["defaultMode"], _ = json.Marshal(n)
	var b bytes.Buffer
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		b.WriteString("\n" + string(kb) + ": ")
		b.Write(vals[k])
	}
	if len(keys) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}")
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		return "", errors.New("cannot format the config: " + err.Error())
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return "", err
	}
	return n, nil
}

// orderedObject reads a JSON object keeping the order of its keys; anything else reads as an empty object.
func orderedObject(data []byte) ([]string, map[string]json.RawMessage) {
	vals := map[string]json.RawMessage{}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, vals
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, map[string]json.RawMessage{}
		}
		k, _ := kt.(string)
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, map[string]json.RawMessage{}
		}
		if _, dup := vals[k]; !dup {
			keys = append(keys, k)
		}
		vals[k] = raw // a duplicate key: the last value wins, as in JavaScript
	}
	return keys, vals
}
