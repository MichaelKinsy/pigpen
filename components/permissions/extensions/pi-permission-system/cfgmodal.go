package pi_permission_system

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Port of src/config/config-modal.ts: the `/permission-system` command and its settings modal.

// CommandConfigStore is the subset of ConfigStore the `/permission-system` command uses.
type CommandConfigStore interface {
	ConfigReader
	Save(next PermissionSystemExtensionConfig, ui ConfigUI)
}

// PermissionSystemConfigController is what the command needs: the store, the precomputed global config file path, and the composed
// config-layer ruleset of the active agent scope.
type PermissionSystemConfigController struct {
	Config                    CommandConfigStore
	ConfigPath                string
	GetActiveAgentConfigRules func() Ruleset
}

// CommandContext is the part of the command context the handler uses; sdk.Context implements it.
type CommandContext interface {
	ConfigUI
	HasUI() bool
	Custom(factory any, options any) (any, error)
}

var commandArguments = []sdk.AutocompleteItem{
	{Value: "show", Label: "Show active settings", Description: "Display the current permission-system config summary"},
	{Value: "path", Label: "Show config path", Description: "Display the config.json path used by pi-permission-system"},
	{Value: "reset", Label: "Reset defaults", Description: "Restore default yolo/logging settings and persist them"},
	{Value: "help", Label: "Show help", Description: "Display command usage"},
}

const usageText = "Usage: /permission-system [show|path|reset|help] (or run /permission-system with no args to open settings modal)"

func cloneDefaultConfig() PermissionSystemExtensionConfig {
	d := defaultExtensionConfig()
	return PermissionSystemExtensionConfig{DebugLog: d.DebugLog, PermissionReviewLog: d.PermissionReviewLog, YoloMode: d.YoloMode,
		DoublePressToConfirm: d.DoublePressToConfirm}
}

func toOnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func formatRulesSummary(rules Ruleset) string {
	var formatted []string
	for _, r := range rules {
		if r.Layer != "config" || r.Origin == "" {
			continue
		}
		key := r.Surface
		if r.Pattern != "*" {
			key = r.Surface + `["` + r.Pattern + `"]`
		}
		formatted = append(formatted, key+"="+r.Action+" ("+r.Origin+")")
	}
	if len(formatted) == 0 {
		return ""
	}
	return "\n  rules: " + strings.Join(formatted, ", ")
}

// summarizeConfig is the `show` text. rules nil leaves the rules out.
func summarizeConfig(c PermissionSystemExtensionConfig, rules Ruleset) string {
	knobs := "yoloMode=" + toOnOff(c.YoloMode) + ", permissionReviewLog=" + toOnOff(c.PermissionReviewLog) + ", debugLog=" + toOnOff(c.DebugLog)
	suffix := ""
	if rules != nil {
		suffix = formatRulesSummary(rules)
	}
	return knobs + suffix
}

var onOff = []string{"on", "off"}

func buildSettingItems(c PermissionSystemExtensionConfig) []kit.SettingItem {
	return []kit.SettingItem{
		{ID: "yoloMode", Label: "YOLO mode", Description: "Auto-approve ask-state permission checks, including subagent approval forwarding",
			CurrentValue: toOnOff(c.YoloMode), Values: onOff},
		{ID: "permissionReviewLog", Label: "Permission review log", Description: "Write permission request and decision audit events to the extension logs directory",
			CurrentValue: toOnOff(c.PermissionReviewLog), Values: onOff},
		{ID: "debugLog", Label: "Debug logging", Description: "Write verbose permission-system diagnostics to the extension logs directory",
			CurrentValue: toOnOff(c.DebugLog), Values: onOff},
		{ID: "doublePressToConfirm", Label: "Double-press to confirm", Description: "Require a confirming second press of a decision hotkey in the inline TUI permission dialog",
			CurrentValue: toOnOff(c.DoublePressToConfirm), Values: onOff},
	}
}

func applySetting(c PermissionSystemExtensionConfig, id, value string) PermissionSystemExtensionConfig {
	switch id {
	case "yoloMode":
		c.YoloMode = value == "on"
	case "permissionReviewLog":
		c.PermissionReviewLog = value == "on"
	case "debugLog":
		c.DebugLog = value == "on"
	case "doublePressToConfirm":
		c.DoublePressToConfirm = value == "on"
	}
	return c
}

// getArgumentCompletions answers the command's argument completions: nil for none.
func getArgumentCompletions(argumentPrefix string) []sdk.AutocompleteItem {
	normalized := strings.ToLower(jsTrim(argumentPrefix))
	if strings.Contains(normalized, " ") {
		return nil
	}
	var filtered []sdk.AutocompleteItem
	for _, item := range commandArguments {
		if strings.HasPrefix(item.Value, normalized) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// settingsModal is the settings list of the modal: a kit view whose events (the host owns the list's state) save each change.
type settingsModal struct {
	ctx        ConfigUI
	controller PermissionSystemConfigController
	current    PermissionSystemExtensionConfig
}

const settingsListID = "permission-system-settings"

func (m *settingsModal) View(int) kit.View {
	return kit.View{Root: kit.NewSettingsList(settingsListID, buildSettingItems(m.current), 10), Focus: settingsListID}
}

// HandleInput: every key the settings list binds stays on the host; nothing else is bound.
func (m *settingsModal) HandleInput(string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

func (m *settingsModal) HandleViewEvent(ev kit.Event) (sdk.RemoteComponentResult, error) {
	switch ev.Type {
	case kit.EventChange:
		m.current = applySetting(m.current, ev.ID, ev.Value)
		m.controller.Config.Save(m.current, m.ctx)
		m.current = m.controller.Config.Current()
	case kit.EventCancel:
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func openSettingsModal(ctx CommandContext, controller PermissionSystemConfigController) error {
	one := 1
	_, err := ctx.Custom(&settingsModal{ctx: ctx, controller: controller, current: controller.Config.Current()}, sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Anchor: "center", Width: sdk.OverlayCells(82), MaxHeight: sdk.OverlayPercent(85), Margin: &sdk.OverlayMargin{All: &one},
		},
	})
	return err
}

// handleArgs runs a `show|path|reset|help` argument; false when there is none (open the modal).
func handleArgs(args string, ui ConfigUI, controller PermissionSystemConfigController) bool {
	normalized := strings.ToLower(jsTrim(args))
	switch normalized {
	case "":
		return false
	case "show":
		ui.Notify("permission-system: "+summarizeConfig(controller.Config.Current(), controller.GetActiveAgentConfigRules()), "info")
	case "path":
		ui.Notify("permission-system config: "+controller.ConfigPath, "info")
	case "reset":
		controller.Config.Save(cloneDefaultConfig(), ui)
		ui.Notify("Permission system settings reset to defaults.", "info")
	case "help":
		ui.Notify(usageText, "info")
	default:
		ui.Notify(usageText, "warning")
	}
	return true
}

func runPermissionSystemCommand(ctx CommandContext, args string, controller PermissionSystemConfigController) error {
	if handleArgs(args, ctx, controller) {
		return nil
	}
	if !ctx.HasUI() {
		ctx.Notify("/permission-system requires interactive TUI mode.", "warning")
		return nil
	}
	return openSettingsModal(ctx, controller)
}

// registerPermissionSystemCommand registers `/permission-system`.
func registerPermissionSystemCommand(e *sdk.Extension, controller PermissionSystemConfigController) {
	e.RegisterCommand("permission-system", sdk.CommandOptions{
		Description:            "Configure pi-permission-system logging and yolo-mode behavior",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) { return getArgumentCompletions(prefix), nil },
		Handler: func(ctx sdk.Context, args string) error {
			return runPermissionSystemCommand(ctx, args, controller)
		},
	})
}
