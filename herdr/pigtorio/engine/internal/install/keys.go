package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeySection is the keys.json section Pigtorio adds: it applies only while the
// pigtorio layout shows. Herdr keeps the keyboard on the focused terminal
// pane even while the farm covers it, so the farm's own letter keys rarely
// arrive; these cmd-shift chords reach the farm from anywhere in the layout
// and take nothing from typing. None of them is a Herdr 0.1.13 default.
var KeySection = map[string]string{
	"cmd-shift-b": "pigtorio.bell",
	"cmd-shift-m": "pigtorio.map",
	"cmd-shift-g": "pigtorio.walk",
	"cmd-shift-k": "pigtorio.workshop",
	"cmd-shift-j": "pigtorio.board",
	"cmd-shift-a": "pigtorio.adopt",
	"cmd-shift-r": "pigtorio.report",
	"cmd-shift-t": "pigtorio.terminals",
	"cmd-shift-f": "pigtorio.find",
	"cmd-shift-n": "pigtorio.crate",
	"cmd-shift-h": "pigtorio.help",
}

const keySectionName = "layout:pigtorio"

// ErrNoKeys means there is no keys.json to add the section to.
var ErrNoKeys = errors.New("there is no keys.json in the Herdr home: Herdr has not written one yet, and a file holding only Pigtorio's section would hide Herdr's defaults. Create it from Herdr's keys settings first, then run install --keys again")

// CheckKeys refuses, before anything is installed, when keys.json is missing.
func CheckKeys(home string) error {
	if _, err := os.Stat(filepath.Join(home, "keys.json")); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: %w", filepath.Join(home, "keys.json"), ErrNoKeys)
	}
	return nil
}

// Keys adds the layout:pigtorio section to home/keys.json. It changes nothing
// when the section exists, and otherwise inserts it as the file's first
// section, leaving every other byte as it was. Herdr treats any edit of
// keys.json as the person's own file, so this runs only when asked.
func Keys(home string) (bool, error) {
	path := filepath.Join(home, "keys.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("%s: %w", path, ErrNoKeys)
	} else if err != nil {
		return false, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if _, ok := top[keySectionName]; ok {
		return false, nil
	}
	for chord := range KeySection {
		for name, raw := range top {
			var sec map[string]json.RawMessage
			if json.Unmarshal(raw, &sec) == nil {
				if _, taken := sec[chord]; taken && (name == "global" || name == keySectionName) {
					return false, fmt.Errorf("%s already binds %s in %q; not adding Pigtorio's keys", path, chord, name)
				}
			}
		}
	}
	section, err := json.MarshalIndent(map[string]any{keySectionName: KeySection}, "", "  ")
	if err != nil {
		return false, err
	}
	inner := bytes.TrimSpace(section)
	inner = bytes.TrimSpace(inner[1 : len(inner)-1]) // "layout:pigtorio": {...}
	open := bytes.IndexByte(b, '{')
	rest := bytes.TrimSpace(b[open+1:])
	var out bytes.Buffer
	out.Write(b[:open+1])
	out.WriteString("\n  ")
	out.Write(inner)
	if len(rest) > 0 && rest[0] != '}' {
		out.WriteString(",")
	}
	out.WriteString("\n  ")
	out.Write(bytes.TrimLeft(b[open+1:], " \t\r\n"))
	var check map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &check); err != nil {
		return false, fmt.Errorf("adding the section would break %s: %w", path, err)
	}
	return true, os.WriteFile(path, out.Bytes(), 0o644)
}
