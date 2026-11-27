package modes

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

const (
	modelPresetsUsage = "Usage: /modelpreset [list | save <name> | switch <name> | delete <name>]"
	noPresetsMessage  = "No model presets saved. Use /modelpreset save <name> to create one."
)

var validPresetNameRE = regexp.MustCompile(`^[a-zA-Z][\w-]*$`)

// ModelPreset is a named snapshot of the session's model, thinking level,
// and optional model role assignments.
// upstream: packages/coding-agent/src/config/model-presets.ts
type ModelPreset struct {
	Model                string            `json:"model,omitempty"`
	DefaultThinkingLevel string            `json:"defaultThinkingLevel,omitempty"`
	ModelRoles           map[string]string `json:"modelRoles,omitempty"`
}

type modelPresetStore struct {
	Presets map[string]ModelPreset `json:"presets"`
}

func isValidModelPresetName(name string) bool {
	return validPresetNameRE.MatchString(name)
}

func modelPresetsPath(ctx sdk.Context) (string, error) {
	cfgHome, err := ctx.ConfigHome()
	if err != nil || strings.TrimSpace(cfgHome) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		cfgHome = filepath.Join(home, ".omp")
	}
	return filepath.Join(cfgHome, "agent", "model-presets.json"), nil
}

func loadModelPresets(path string) (*modelPresetStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &modelPresetStore{Presets: map[string]ModelPreset{}}, nil
		}
		return nil, err
	}
	var st modelPresetStore
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse model presets %s: %w", path, err)
	}
	if st.Presets == nil {
		st.Presets = map[string]ModelPreset{}
	}
	return &st, nil
}

func saveModelPresets(path string, st *modelPresetStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sortedPresetNames(st *modelPresetStore) []string {
	names := make([]string, 0, len(st.Presets))
	for k := range st.Presets {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// handleModelPresetCommand implements `/modelpreset [list|save|switch|delete] [name]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:801-968
func (m *manager) handleModelPresetCommand(ctx sdk.Context, args string) error {
	path, err := modelPresetsPath(ctx)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to resolve model presets path: %v", err))
		return nil
	}
	st, err := loadModelPresets(path)
	if err != nil {
		notifyError(ctx, err.Error())
		return nil
	}

	trimmed := strings.TrimSpace(args)
	if trimmed == "" && ctx.HasUI() {
		names := sortedPresetNames(st)
		if len(names) == 0 {
			notifyInfo(ctx, noPresetsMessage)
			return nil
		}
		pickedVal, ok, err := ctx.Select("Switch to model preset", names)
		if err != nil || !ok {
			return nil
		}
		if picked, ok := pickedVal.(string); ok && picked != "" {
			trimmed = "switch " + picked
		} else {
			return nil
		}
	}

	fields := strings.Fields(trimmed)
	sub := "list"
	var name string
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
		if len(fields) > 1 {
			name = strings.Join(fields[1:], " ")
		}
	}

	switch sub {
	case "list":
		names := sortedPresetNames(st)
		if len(names) == 0 {
			notifyInfo(ctx, noPresetsMessage)
		} else {
			notifyInfo(ctx, fmt.Sprintf("Model presets: %s", strings.Join(names, ", ")))
		}
		return nil

	case "save":
		if name == "" {
			notifyWarn(ctx, "Usage: /modelpreset save <name>")
			return nil
		}
		if !isValidModelPresetName(name) {
			notifyWarn(ctx, fmt.Sprintf("Invalid preset name %q: use a letter, then letters, digits, - or _", name))
			return nil
		}
		thinkingLevel, _ := ctx.GetThinkingLevel()
		preset := ModelPreset{
			Model:                ctx.ModelQualified(),
			DefaultThinkingLevel: thinkingLevel,
		}
		if Setting != nil {
			if rawRoles, ok := Setting(ctx, "modelRoles").(map[string]any); ok && len(rawRoles) > 0 {
				roles := make(map[string]string, len(rawRoles))
				for k, v := range rawRoles {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						roles[k] = s
					}
				}
				if len(roles) > 0 {
					preset.ModelRoles = roles
				}
			}
		}
		st.Presets[name] = preset
		if err := saveModelPresets(path, st); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to save preset %q: %v", name, err))
			return nil
		}
		notifyInfo(ctx, fmt.Sprintf("Saved model preset %q", name))
		return nil

	case "switch":
		if name == "" {
			notifyWarn(ctx, "Usage: /modelpreset switch <name>")
			return nil
		}
		preset, ok := st.Presets[name]
		if !ok {
			notifyWarn(ctx, fmt.Sprintf("Preset not found: %s", name))
			return nil
		}
		targetModel := preset.Model
		if targetModel == "" && preset.ModelRoles != nil {
			targetModel = preset.ModelRoles["default"]
		}
		if targetModel != "" {
			switched, err := ctx.SetModelNamed(targetModel)
			if err != nil {
				notifyWarn(ctx, fmt.Sprintf("Failed to switch to preset %q: %v", name, err))
				return nil
			}
			if !switched {
				notifyWarn(ctx, fmt.Sprintf("Preset %q model unavailable: %s", name, targetModel))
				return nil
			}
		}
		if preset.DefaultThinkingLevel != "" {
			ctx.SetThinkingLevel(preset.DefaultThinkingLevel)
		}
		if len(preset.ModelRoles) > 0 && SetSetting != nil {
			rolesAny := make(map[string]any, len(preset.ModelRoles))
			for k, v := range preset.ModelRoles {
				rolesAny[k] = v
			}
			_ = SetSetting(ctx, "modelRoles", rolesAny, false)
		}
		if targetModel != "" && preset.DefaultThinkingLevel != "" {
			notifyInfo(ctx, fmt.Sprintf("Switched to model preset %q (%s, thinking: %s)", name, targetModel, preset.DefaultThinkingLevel))
		} else if targetModel != "" {
			notifyInfo(ctx, fmt.Sprintf("Switched to model preset %q (%s)", name, targetModel))
		} else {
			notifyInfo(ctx, fmt.Sprintf("Switched to model preset %q", name))
		}
		return nil

	case "delete":
		if name == "" {
			notifyWarn(ctx, "Usage: /modelpreset delete <name>")
			return nil
		}
		if _, ok := st.Presets[name]; !ok {
			notifyWarn(ctx, fmt.Sprintf("Preset not found: %s", name))
			return nil
		}
		delete(st.Presets, name)
		if err := saveModelPresets(path, st); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to delete preset %q: %v", name, err))
			return nil
		}
		notifyInfo(ctx, fmt.Sprintf("Deleted model preset %q", name))
		return nil

	default:
		notifyWarn(ctx, modelPresetsUsage)
		return nil
	}
}
