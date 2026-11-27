package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

// profileSubdirs matches Hermes's _PROFILE_DIRS bootstrapped inside every new profile
// (hermes_cli/profiles.py:45).
var profileSubdirs = []string{
	"memories",
	"sessions",
	"skills",
	"skins",
	"logs",
	"plans",
	"workspace",
	"cron",
	"home",
}

// reservedProfileNames mirrors Hermes's reserved profile names that must not be created.
var reservedProfileNames = map[string]bool{
	"default":  true,
	"profiles": true,
	"active":   true,
}

// NormalizeProfileName normalizes a profile name to its canonical on-disk form.
func NormalizeProfileName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.EqualFold(trimmed, "default") {
		return "default"
	}
	return strings.ToLower(trimmed)
}

// ListProfiles returns the sorted list of available profile names, always starting
// with "default" followed by any valid profile directories under p.ProfilesDir.
func ListProfiles(p paths.ProfilePaths) ([]string, error) {
	out := []string{"default"}
	if strings.TrimSpace(p.ProfilesDir) == "" {
		return out, nil
	}
	entries, err := os.ReadDir(p.ProfilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read profiles dir: %w", err)
	}
	var named []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "default" || !paths.ValidateProfileName(name) {
			continue
		}
		named = append(named, name)
	}
	sort.Strings(named)
	return append(out, named...), nil
}

// CreateProfile creates a new named profile directory under p.ProfilesDir and
// bootstraps Hermes's standard subdirectories. Returns the created profile path.
func CreateProfile(p paths.ProfilePaths, name string) (string, error) {
	canon := NormalizeProfileName(name)
	if canon == "" || !paths.ValidateProfileName(canon) {
		return "", fmt.Errorf("invalid profile name %q: must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}", name)
	}
	if reservedProfileNames[canon] {
		return "", fmt.Errorf("profile name %q is reserved", canon)
	}
	if strings.TrimSpace(p.ProfilesDir) == "" {
		return "", errors.New("profiles directory is not configured")
	}

	dir := filepath.Join(p.ProfilesDir, canon)
	if info, err := os.Stat(dir); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("profile %q already exists", canon)
		}
		return "", fmt.Errorf("path %q already exists and is not a directory", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat profile dir: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create profile dir: %w", err)
	}
	for _, sub := range profileSubdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return "", fmt.Errorf("create profile subdir %q: %w", sub, err)
		}
	}
	return dir, nil
}

// DeleteProfile deletes an existing named profile under p.ProfilesDir.
// The "default" profile cannot be deleted. If the deleted profile is currently
// active in p.ActiveProfile, the active profile reverts to "default".
func DeleteProfile(p paths.ProfilePaths, name string) error {
	canon := NormalizeProfileName(name)
	if canon == "default" {
		return errors.New("cannot delete the default profile")
	}
	if canon == "" || !paths.ValidateProfileName(canon) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	if strings.TrimSpace(p.ProfilesDir) == "" {
		return errors.New("profiles directory is not configured")
	}

	dir := filepath.Join(p.ProfilesDir, canon)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %q does not exist", canon)
		}
		return fmt.Errorf("stat profile dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile path %q is not a directory", dir)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove profile dir: %w", err)
	}

	// If this profile was sticky-active, reset active_profile back to default.
	if active, err := GetActiveProfile(p); err == nil && active == canon {
		_ = SetActiveProfile(p, "default")
	}
	return nil
}

// GetActiveProfile returns the currently active sticky profile name from
// p.ActiveProfile, or "default" if unset, missing, or invalid.
func GetActiveProfile(p paths.ProfilePaths) (string, error) {
	if strings.TrimSpace(p.ActiveProfile) == "" {
		if p.ProfileName != "" && paths.ValidateProfileName(p.ProfileName) {
			return p.ProfileName, nil
		}
		return "default", nil
	}
	raw, err := os.ReadFile(p.ActiveProfile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "default", nil
		}
		return "", fmt.Errorf("read active_profile: %w", err)
	}
	name := NormalizeProfileName(string(raw))
	if name == "" || name == "default" || !paths.ValidateProfileName(name) {
		return "default", nil
	}
	return name, nil
}

// SetActiveProfile sets the sticky active profile in p.ActiveProfile.
// Setting "default" removes the active_profile file (matching Hermes behavior).
// Setting a named profile verifies that the profile exists under p.ProfilesDir
// and writes the name atomically.
func SetActiveProfile(p paths.ProfilePaths, name string) error {
	canon := NormalizeProfileName(name)
	if canon == "" {
		return errors.New("profile name cannot be empty")
	}
	if strings.TrimSpace(p.ActiveProfile) == "" {
		return errors.New("active_profile path is not configured")
	}
	if canon == "default" {
		err := os.Remove(p.ActiveProfile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove active_profile: %w", err)
		}
		return nil
	}
	if !paths.ValidateProfileName(canon) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	if strings.TrimSpace(p.ProfilesDir) == "" {
		return errors.New("profiles directory is not configured")
	}
	dir := filepath.Join(p.ProfilesDir, canon)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %q does not exist", canon)
		}
		return fmt.Errorf("stat profile dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile path %q is not a directory", dir)
	}

	parent := filepath.Dir(p.ActiveProfile)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create active_profile parent dir: %w", err)
	}
	tmp, err := os.CreateTemp(parent, "active_profile.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp active_profile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(canon + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp active_profile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp active_profile: %w", err)
	}
	if err := os.Rename(tmpName, p.ActiveProfile); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename active_profile: %w", err)
	}
	return nil
}
