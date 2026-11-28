package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// SetSetting is a nil-safe hook wired by the integrator to update omp's
// extension settings (e.g. `browser.headless`) via internal/settings.
var SetSetting func(ctx sdk.Context, key string, value any) error

// applyBrowserModeChangeFn is an injectable hook for tests. Defaults to
// dropping owned headless tabs for the calling session so the next browser
// open launches in the requested headless/visible mode.
var applyBrowserModeChangeFn = defaultApplyBrowserModeChange

func defaultApplyBrowserModeChange(ctx sdk.Context, headless bool) error {
	_ = headless
	sessionID := sessionIDFromSDK(ctx)
	if sessionID == "" {
		return nil
	}
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defaultManager.DropHeadlessTabs(c, sessionID)
	return nil
}

func registerBrowserCommand(e *sdk.Extension) {
	if e == nil {
		return
	}
	e.RegisterCommand("browser", sdk.CommandOptions{
		Description: "Toggle browser eval-prelude headless vs visible mode",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			if strings.Contains(prefix, " ") {
				return nil, nil
			}
			items := []sdk.AutocompleteItem{
				{Value: "headless ", Label: "headless", Description: "Switch to headless mode"},
				{Value: "visible ", Label: "visible", Description: "Switch to visible mode"},
			}
			lower := strings.ToLower(prefix)
			var out []sdk.AutocompleteItem
			for _, item := range items {
				if strings.HasPrefix(item.Label, lower) {
					out = append(out, item)
				}
			}
			return out, nil
		},
		Handler: handleBrowserCommand,
	})
}

// handleBrowserCommand implements `/browser [headless|visible]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:506-574
func handleBrowserCommand(ctx sdk.Context, args string) error {
	s := effectiveSettingsForSDK(ctx, defaultManager)
	if !s.Enabled {
		ctx.Notify("Browser capability is disabled (enable in settings)", "warning")
		return nil
	}

	arg := strings.ToLower(strings.TrimSpace(args))
	next := s.Headless
	switch arg {
	case "":
		next = !s.Headless
	case "headless", "hidden":
		next = true
	case "visible", "show", "headful":
		next = false
	default:
		ctx.Notify("Usage: /browser [headless|visible]", "info")
		return nil
	}

	if SetSetting != nil {
		if err := SetSetting(ctx, "browser.headless", next); err != nil {
			ctx.Notify(fmt.Sprintf("Failed to update browser.headless: %v", err), "warning")
			return nil
		}
	}

	modeLabel := "visible"
	if next {
		modeLabel = "headless"
	}
	if applyBrowserModeChangeFn != nil {
		if err := applyBrowserModeChangeFn(ctx, next); err != nil {
			if ctx.HasUI() {
				ctx.Notify(fmt.Sprintf("Failed to apply the browser mode: %v", err), "warning")
			} else {
				ctx.Notify(fmt.Sprintf("Browser mode set to %s, but applying it failed: %v", modeLabel, err), "warning")
			}
			return nil
		}
	}

	ctx.Notify(fmt.Sprintf("Browser mode: %s", modeLabel), "info")
	return nil
}
