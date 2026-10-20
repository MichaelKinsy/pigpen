package pi_permission_system

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The settings modal's component, driven directly: the fake host cannot send the input a focused component receives.

func TestSettingsModalComponent(t *testing.T) {
	store := &fileBackedStore{config: defaultExtensionConfig(), t: t, path: t.TempDir() + "/config.json"}
	controller := PermissionSystemConfigController{Config: store, ConfigPath: store.path, GetActiveAgentConfigRules: func() Ruleset { return nil }}
	ctx := &stubCommandContext{hasUI: true}
	if err := runPermissionSystemCommand(ctx, "", controller); err != nil {
		t.Fatal(err)
	}
	modal, ok := ctx.lastFactory.(*settingsModal)
	if !ok {
		t.Fatalf("ui.custom got %T", ctx.lastFactory)
	}
	opts := ctx.lastOptions.(sdk.RemoteOverlayOptions)
	eq(t, opts.Overlay, true, "overlay")
	eq(t, opts.OverlayOptions.Anchor, "center", "anchor")
	eq(t, opts.OverlayOptions.Width.Value, 82.0, "width")
	eq(t, opts.OverlayOptions.MaxHeight.Percent && opts.OverlayOptions.MaxHeight.Value == 85, true, "maxHeight")
	eq(t, *opts.OverlayOptions.Margin.All, 1, "margin")

	list := modal.View(80).Root.(*kit.SettingsList)
	var ids []string
	for _, it := range list.Items {
		ids = append(ids, it.ID+"="+it.CurrentValue)
	}
	eq(t, ids, []string{"yoloMode=off", "permissionReviewLog=on", "debugLog=off", "doublePressToConfirm=on"}, "items")
	eq(t, list.MaxVisible, 10, "maxVisible")
	eq(t, modal.View(80).Focus, list.ID, "focus")

	res, err := modal.HandleViewEvent(kit.Event{Node: list.ID, Type: kit.EventChange, ID: "yoloMode", Value: "on"})
	eq(t, err, nil, "err")
	eq(t, res.Done, false, "stays open")
	eq(t, store.config.YoloMode, true, "saved")
	eq(t, modal.View(80).Root.(*kit.SettingsList).Items[0].CurrentValue, "on", "the list shows the saved value")

	res, _ = modal.HandleViewEvent(kit.Event{Node: list.ID, Type: kit.EventCancel})
	eq(t, res.Done, true, "cancel closes")
	res, _ = modal.HandleInput("x")
	eq(t, res, sdk.RemoteComponentResult{}, "no other keys are bound")
}
