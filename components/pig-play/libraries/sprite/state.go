package sprite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultID is the sprite used until one is selected.
const DefaultID = "pig-default"

type state struct {
	Variant string `json:"variant"`
}

func loadVariant(configHome string) Variant {
	data, err := os.ReadFile(StatePath(configHome))
	if err != nil {
		return FindVariant(DefaultID)
	}
	var saved state
	if json.Unmarshal(data, &saved) != nil {
		return FindVariant(DefaultID)
	}
	return FindVariant(saved.Variant)
}

// SaveVariant persists the selected sprite under configHome (owner-only file).
func SaveVariant(configHome, id string) error {
	dir := filepath.Dir(StatePath(configHome))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create login state directory: %w", err)
	}
	data, err := json.Marshal(state{Variant: id})
	if err != nil {
		return fmt.Errorf("encode login state: %w", err)
	}
	if err := os.WriteFile(StatePath(configHome), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write login state: %w", err)
	}
	return nil
}

// StatePath is where the sprite selection lives. The directory name is kept from
// PiG Standard so a selection made there carries over.
func StatePath(configHome string) string {
	return filepath.Join(configHome, "state", "pig-standard", "login.json")
}
