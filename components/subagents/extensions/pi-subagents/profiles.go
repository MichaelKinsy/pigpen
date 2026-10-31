package pi_subagents

// Profiles (src/profiles/profiles.ts, application layer): profile files under
// <agent dir>/profiles/pi-subagents/*.json. A profile owns the complete
// agentOverrides mapping; applying it merges the mapping into the user's
// settings.json, keeping unrelated subagent settings (disableBuiltins,
// modelScope, ...) and existing machine pins. The provider catalog half
// (generate/refresh/check with live probes) is not ported and is refused.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// profileFile is the parsed SubagentProfileFile: only the fields the original's
// validation reads.
type profileFile struct {
	AgentOverrides  map[string]map[string]any
	DisableBuiltins *bool
	// Extra holds the rest of the subagents object so applying a profile keeps
	// the original's merge (`...profile.subagents` over the existing keys).
	Extra map[string]any
}

var safePathToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// normalizePathToken is the original's: a safe file name, no separators.
func normalizePathToken(value, label string) (string, string) {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return "", label + " is required."
	}
	if !safePathToken.MatchString(trimmed) || trimmed == "." || trimmed == ".." ||
		strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", label + " must be a safe file name using only letters, numbers, dots, underscores, and hyphens."
	}
	return trimmed, ""
}

// normalizeProfileName strips an optional .json and validates the token
// (normalizeProfileName in the original).
func normalizeProfileName(name string) (string, string) {
	trimmed := jsTrim(name)
	stem := trimmed
	if strings.HasSuffix(trimmed, ".json") {
		stem = strings.TrimSuffix(trimmed, ".json")
	}
	return normalizePathToken(stem, "Profile name")
}

// getSubagentProfilesDir is the original's getSubagentProfilesDir.
func getSubagentProfilesDir() string {
	return filepath.Join(agentDir(), "profiles", "pi-subagents")
}

// listSubagentProfiles is the original's: the dir exists afterwards (the
// original ensures it), every *.json stem, sorted.
func listSubagentProfiles() ([]string, string) {
	dir := getSubagentProfilesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err.Error()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, ""
		}
		return nil, err.Error()
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	sort.Strings(names)
	if names == nil {
		names = []string{}
	}
	return names, ""
}

// validateSubagentProfile validates a parsed profile the original's way
// (validateSubagentProfile), returning the port's view of it.
func validateSubagentProfile(filePath string, parsed map[string]any) (*profileFile, string) {
	subagents, ok := parsed["subagents"].(map[string]any)
	if !ok || subagents == nil {
		return nil, fmt.Sprintf("Profile '%s' must contain a 'subagents' object.", filePath)
	}
	agentOverrides, ok := subagents["agentOverrides"].(map[string]any)
	if !ok || agentOverrides == nil {
		return nil, fmt.Sprintf("Profile '%s' must contain 'subagents.agentOverrides' as an object.", filePath)
	}
	out := &profileFile{AgentOverrides: map[string]map[string]any{}}
	names := make([]string, 0, len(agentOverrides))
	for name := range agentOverrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, ok := agentOverrides[name].(map[string]any)
		if !ok || value == nil {
			return nil, fmt.Sprintf("Profile '%s' has invalid override '%s'; expected an object.", filePath, name)
		}
		if model, present := value["model"]; present {
			if s, ok := model.(string); !ok {
				return nil, fmt.Sprintf("Profile '%s' has invalid model for '%s'; expected a string.", filePath, name)
			} else {
				_ = s
			}
		}
		if thinking, present := value["thinking"]; present {
			if s, ok := thinking.(string); ok {
				_ = s
			} else if thinking != false {
				return nil, fmt.Sprintf("Profile '%s' has invalid thinking for '%s'; expected a string or false.", filePath, name)
			}
		}
		if _, present := value["fallbackModels"]; present {
			return nil, fmt.Sprintf("Profile '%s' uses removed field fallbackModels for '%s'; configure one model instead.", filePath, name)
		}
		out.AgentOverrides[name] = value
	}
	if v, present := subagents["disableBuiltins"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Sprintf("Profile '%s' has invalid subagents.disableBuiltins; expected a boolean.", filePath)
		}
		out.DisableBuiltins = &b
	}
	extra := map[string]any{}
	for k, v := range subagents {
		if k == "agentOverrides" {
			continue
		}
		extra[k] = v
	}
	out.Extra = extra
	return out, ""
}

// readSubagentProfile is the original's: resolve the path, read, validate
// (`Profile not found: <name>` when absent).
func readSubagentProfile(name string) (string, *profileFile, string) {
	stem, errText := normalizeProfileName(name)
	if errText != "" {
		return "", nil, errText
	}
	filePath := filepath.Join(getSubagentProfilesDir(), stem+".json")
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Sprintf("Profile not found: %s", name)
		}
		return "", nil, fmt.Sprintf("Failed to read profile '%s': %s", filePath, err.Error())
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", nil, fmt.Sprintf("Failed to parse profile '%s': %s", filePath, err.Error())
	}
	profile, errText := validateSubagentProfile(filePath, parsed)
	if errText != "" {
		return "", nil, errText
	}
	return filePath, profile, ""
}

// applySubagentProfile merges the profile into the user's settings
// (applySubagentProfile in the original): the profile owns the complete
// agentOverrides map, existing machine pins survive a switch, and unrelated
// subagent settings (disableBuiltins, modelScope, ...) survive too.
func applySubagentProfile(name string) (filePath string, settingsPath string, errText string) {
	filePath, profile, errText := readSubagentProfile(name)
	if errText != "" {
		return "", "", errText
	}
	settingsPath = userAgentSettingsPath()
	settings, errText := readSettingsStrict(settingsPath)
	if errText != "" {
		return "", "", errText
	}
	existing, _ := settings["subagents"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	existingOverrides, _ := existing["agentOverrides"].(map[string]any)
	overrides := map[string]any{}
	for agentName, entry := range profile.AgentOverrides {
		overrides[agentName] = entry
	}
	for agentName, raw := range existingOverrides {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		machine, isString := entry["machine"].(string)
		if !isString || jsTrim(machine) == "" {
			continue
		}
		current, present := overrides[agentName].(map[string]any)
		if present {
			if _, profileHasMachine := current["machine"]; profileHasMachine {
				continue
			}
		}
		merged := map[string]any{}
		for k, v := range current {
			merged[k] = v
		}
		merged["machine"] = machine
		overrides[agentName] = merged
	}
	merged := map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range profile.Extra {
		merged[k] = v
	}
	merged["agentOverrides"] = overrides
	settings["subagents"] = merged
	if msg := writeSettingsFile(settingsPath, settings); msg != "" {
		return "", "", msg
	}
	return filePath, settingsPath, ""
}

// getProfileWorkerModel is the original's: the worker override's model.
func getProfileWorkerModel(profile *profileFile) string {
	entry, ok := profile.AgentOverrides["worker"]
	if !ok {
		return ""
	}
	model, ok := entry["model"].(string)
	if !ok || jsTrim(model) == "" {
		return ""
	}
	return jsTrim(model)
}

// profileSlashCommand is /subagents-profiles: list saved profiles.
func profileSlashCommand(ctx sdk.Context, args string) error {
	names, errText := listSubagentProfiles()
	if errText != "" {
		ctx.Notify(errText, "error")
		return nil
	}
	if len(names) == 0 {
		sendSlashText(ctx, "Subagent profiles\n\nNo subagent profiles found in ~/.pi/agent/profiles/pi-subagents/")
		return nil
	}
	sendSlashText(ctx, "Subagent profiles\n\n"+strings.Join(names, "\n"))
	return nil
}

// loadProfileSlashCommand is /subagents-load-profile <name>: apply the profile
// into settings.json and optionally switch this session to its worker model.
func loadProfileSlashCommand(ctx sdk.Context, args string) error {
	parts := strings.Fields(args)
	if len(parts) != 1 {
		ctx.Notify("Usage: /subagents-load-profile <name>", "error")
		return nil
	}
	workerModel := ""
	if _, profile, errText := readSubagentProfile(parts[0]); errText == "" {
		workerModel = getProfileWorkerModel(profile)
	}
	filePath, settingsPath, errText := applySubagentProfile(parts[0])
	if errText != "" {
		ctx.Notify(errText, "error")
		return nil
	}
	lines := []string{
		"Loaded subagent profile: " + parts[0],
		"Profile: " + filePath,
		"Updated: " + settingsPath,
	}
	if workerModel != "" {
		found := false
		for _, m := range registryModels(ctx) {
			if strings.EqualFold(m.FullID, workerModel) || strings.EqualFold(m.ID, workerModel) {
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, fmt.Sprintf("Could not switch current session model: '%s' is not available in the current model registry.", workerModel))
		} else {
			answer, err := ctx.Confirm("", fmt.Sprintf("Profile loaded. Also switch this session to the profile worker model?\n\n%s", workerModel))
			if err == nil && isConfirmYes(answer) {
				ok, setErr := ctx.SetModel(workerModel)
				switch {
				case setErr != nil || !ok:
					lines = append(lines, fmt.Sprintf("Could not switch current session model to '%s': no API key or provider access is available.", workerModel))
				default:
					lines = append(lines, "Current session model switched to: "+workerModel)
				}
			}
		}
	}
	sendSlashText(ctx, strings.Join(lines, "\n"))
	return nil
}

// isConfirmYes interprets the Confirm dialog's answer (the original checks the
// boolean its ui.confirm resolves).
func isConfirmYes(answer any) bool {
	switch v := answer.(type) {
	case bool:
		return v
	case string:
		return v == "yes" || v == "true"
	default:
		return false
	}
}
