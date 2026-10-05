package warden

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The user's configuration file: <PIG_HOME>/pigpen-warden/config.json. It holds no secret (the TypeSafe key
// is read from the environment and never stored here), and is written owner-only.

// ConfigPath is where the configuration lives under a PiG config home.
func ConfigPath(configHome string) string {
	return filepath.Join(configHome, "pigpen-warden", "config.json")
}

// LoadConfig reads the file over the defaults. A missing file is the defaults; a file that does not parse is an
// error the caller shows (the defaults still apply).
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultConfig(), err
	}
	return cfg, nil
}

// SaveConfig writes the configuration owner-only.
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
