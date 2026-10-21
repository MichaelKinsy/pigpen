package warden

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The user's configuration file: <PIG_HOME>/pigpen-warden/config.json. It holds no secret (the TypeSafe key
// is read from the environment and never stored here), and is written owner-only. Upstream's user file is
// <agentDir>/pi-warden/config.json (D5: the port keeps its own, with its own `consent` and `backend` keys).

// ConfigPath is where the configuration lives under a PiG config home.
func ConfigPath(configHome string) string {
	return filepath.Join(configHome, "pigpen-warden", "config.json")
}

// ProjectConfigFile is the project file's name: the same as upstream's, so a repository's tuning reads the same for the
// original and for the port. A project file never carries consent or a backend (it cannot set them).
const ProjectConfigFile = "pi-warden.json"

// readJSONObject reads a JSON file that holds one object. A missing file is (nil, nil); a file that is not an object
// (an array, a string, ...) is (nil, nil) too, as upstream's readJson does; a file that does not parse is an error.
func readJSONObject(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	m, _ := parsed.(map[string]any)
	return m, nil
}

// LoadConfig reads the user file over the defaults. A missing file is the defaults; a file that does not parse is an
// error the caller shows (the defaults still apply, and never turn warden on).
func LoadConfig(path string) (Config, error) {
	return LoadConfigWithProject(path, "", false)
}

// LoadConfigWithProject is upstream's loadConfig: the user file over the defaults, then the project file when the
// project is trusted. A broken project file is an error the caller shows; the user's configuration still applies.
func LoadConfigWithProject(userPath, projectPath string, projectTrusted bool) (Config, error) {
	user, err := readJSONObject(userPath)
	if err != nil {
		return DefaultConfig(), err
	}
	cfg := ApplyUserOverrides(DefaultConfig(), mapOrNil(user))
	if projectPath == "" || !projectTrusted {
		return cfg, nil
	}
	project, err := readJSONObject(projectPath)
	if err != nil {
		return cfg, err
	}
	return ApplyProjectOverrides(cfg, mapOrNil(project)), nil
}

// mapOrNil keeps a nil map from becoming a non-nil `any` that asObj would accept as an empty object (no difference to
// the loader, but a missing file stays "no file").
func mapOrNil(m map[string]any) any {
	if m == nil {
		return nil
	}
	return m
}

// ReadUserConfig reads only the user file as raw JSON, for editing and persisting consent. A missing or unreadable
// file is empty.
func ReadUserConfig(path string) map[string]any {
	m, err := readJSONObject(path)
	if err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func marshalConfig(raw any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteUserConfig writes raw JSON to the user file, owner-only, atomically. Keys are written sorted (encoding/json
// orders map keys; upstream keeps insertion order).
func WriteUserConfig(path string, raw map[string]any) error {
	data, err := marshalConfig(raw)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

// SetUserSetting persists one top-level user setting without disturbing the rest of the file.
func SetUserSetting(path, key string, value any) error {
	_, err := Serialize("config:"+path, func() (struct{}, error) {
		raw := ReadUserConfig(path)
		raw[key] = value
		return struct{}{}, WriteUserConfig(path, raw)
	})
	return err
}

// SaveConfig writes the whole configuration owner-only.
func SaveConfig(path string, cfg Config) error {
	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

// SaveConfigChange persists what a command changed: only the keys whose value differs between before and after are
// merged into the user file, so every other key (including ones this build does not know) stays as the user wrote it.
func SaveConfigChange(path string, before, after Config) error {
	b, err := toJSONMap(before)
	if err != nil {
		return err
	}
	a, err := toJSONMap(after)
	if err != nil {
		return err
	}
	_, err = Serialize("config:"+path, func() (struct{}, error) {
		raw := ReadUserConfig(path)
		mergeChanged(raw, b, a)
		return struct{}{}, WriteUserConfig(path, raw)
	})
	return err
}

func toJSONMap(cfg Config) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(data, &m)
}

// mergeChanged writes into dst every leaf where after differs from before. Objects merge key by key; anything else
// (a list, a scalar) is replaced as a whole.
func mergeChanged(dst, before, after map[string]any) {
	for key, a := range after {
		b, had := before[key]
		am, aIsObj := a.(map[string]any)
		bm, bIsObj := b.(map[string]any)
		if aIsObj && bIsObj {
			child, _ := dst[key].(map[string]any)
			if child == nil {
				child = map[string]any{}
			}
			mergeChanged(child, bm, am)
			if len(child) > 0 {
				dst[key] = child
			}
			continue
		}
		if !had || !configJSONEqual(a, b) {
			dst[key] = a
		}
	}
}

func configJSONEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
