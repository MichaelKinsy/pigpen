package websearch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
)

// Curator settings of web-search.json: where the curator server binds (utils.ts
// resolveCuratorNetworkConfig), the idle timeout and browser auto-open (index.ts), and the
// order-preserving write of one setting (saveConfig).

// CuratorNetwork is resolveCuratorNetworkConfig's result.
type CuratorNetwork struct {
	// Enabled reports that remote access was opted into with curatorRemote.
	Enabled bool
	// Host is what the curator URL names.
	Host string
	// Bind is the address the server listens on.
	Bind string
}

var localCuratorNetwork = CuratorNetwork{Enabled: false, Host: "localhost", Bind: "127.0.0.1"}

// hostnameFunc is os.Hostname (a test seam).
var hostnameFunc = os.Hostname

func trimmedStringOf(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return jsTrim(s)
}

func osHostname() string {
	h, _ := hostnameFunc()
	return h
}

// ResolveCuratorNetworkConfig reads curatorRemote: true binds 0.0.0.0 and names this machine; an
// object overrides host and bind. A missing, unreadable or non-object file means local only.
func ResolveCuratorNetworkConfig() CuratorNetwork {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return localCuratorNetwork
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return localCuratorNetwork
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return localCuratorNetwork
	}
	switch remote := root["curatorRemote"].(type) {
	case bool:
		if remote {
			return CuratorNetwork{Enabled: true, Host: osHostname(), Bind: "0.0.0.0"}
		}
	case map[string]any:
		n := CuratorNetwork{Enabled: true, Host: trimmedStringOf(remote["host"]), Bind: trimmedStringOf(remote["bind"])}
		if n.Host == "" {
			n.Host = osHostname()
		}
		if n.Bind == "" {
			n.Bind = "0.0.0.0"
		}
		return n
	}
	return localCuratorNetwork
}

const (
	defaultCuratorTimeoutSeconds       = 20
	defaultRemoteCuratorTimeoutSeconds = 60
	maxCuratorTimeoutSeconds           = 600
)

// normalizeCuratorTimeoutSeconds: a finite number floored, at least 1, at most 600.
func normalizeCuratorTimeoutSeconds(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	n := math.Floor(f)
	if n < 1 {
		return 0, false
	}
	return int(math.Min(n, maxCuratorTimeoutSeconds)), true
}

// CuratorTimeoutSeconds is getCuratorTimeoutSeconds: the configured idle timeout, else 60 seconds
// for a remote curator (the user must notice and click a printed link) and 20 for a local one.
func CuratorTimeoutSeconds() (int, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		return 0, err
	}
	if n, ok := normalizeCuratorTimeoutSeconds(root["curatorTimeoutSeconds"]); ok {
		return n, nil
	}
	if ResolveCuratorNetworkConfig().Enabled {
		return defaultRemoteCuratorTimeoutSeconds, nil
	}
	return defaultCuratorTimeoutSeconds, nil
}

// ShouldAutoOpenCuratorBrowser is shouldAutoOpenCuratorBrowser: autoOpenBrowser false never opens;
// a remote curator opens only when autoOpenBrowser is true.
func ShouldAutoOpenCuratorBrowser(root map[string]any) bool {
	if root["autoOpenBrowser"] == false {
		return false
	}
	if ResolveCuratorNetworkConfig().Enabled && root["autoOpenBrowser"] != true {
		return false
	}
	return true
}

// SaveConfigValue is saveConfig({[key]: value}): the file's top-level keys keep their order, the
// key is replaced in place or appended, and the file is written with two-space indentation and a
// final newline. An existing file must parse as an object.
func SaveConfigValue(key string, value any) error {
	path := ConfigPath()
	var keys []string
	values := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		var err error
		keys, values, err = parseOrderedObject(path, data)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := marshalNoEscape(value)
	if err != nil {
		return err
	}
	if _, present := values[key]; !present {
		keys = append(keys, key)
	}
	values[key] = raw
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			compact.WriteByte(',')
		}
		name, _ := marshalNoEscape(k)
		compact.Write(name)
		compact.WriteByte(':')
		compact.Write(values[k])
	}
	compact.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// parseOrderedObject splits a JSON object into its keys (in file order, the last duplicate
// winning like JSON.parse) and raw values, with parseConfigRoot's error messages.
func parseOrderedObject(path string, data []byte) ([]string, map[string]json.RawMessage, error) {
	var probe any
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, nil, fmt.Errorf("Invalid config in %s: expected a JSON object", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	var keys []string
	values := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		if _, present := values[key]; !present {
			keys = append(keys, key)
		}
		values[key] = raw
	}
	return keys, values, nil
}
