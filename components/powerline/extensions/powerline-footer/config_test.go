package powerline_footer

import "testing"

var cfgPresets = []string{"default", "compact"}

func cfg(t *testing.T, text string) powerlineConfig {
	return parsePowerlineConfig(js(t, text), cfgPresets)
}

func ci(id, statusKey, position string, hideWhenMissing, exclude bool) customItem {
	return customItem{ID: id, StatusKey: statusKey, Position: position, HideWhenMissing: hideWhenMissing, ExcludeFromStatuses: exclude}
}

func TestConfig(t *testing.T) {
	tw(t, "custom-items", "fixed custom preset is removed in favor of powerline.layout", func(t *testing.T) {
		_, has := presets["custom"]
		eq(t, has, false, "custom preset")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports object config with custom items", func(t *testing.T) {
		c := cfg(t, `{"preset":"compact","customItems":[{"id":"ci","statusKey":"ci-status","position":"right","prefix":"CI"},{"id":"review","position":"secondary","hideWhenMissing":false}]}`)
		eq(t, c.Preset, "compact", "preset")
		eq(t, len(c.CustomItems), 2, "items")
		eq(t, itemAt(c.CustomItems, 0).ID, "ci", "id")
		eq(t, itemAt(c.CustomItems, 0).StatusKey, "ci-status", "statusKey")
		eq(t, itemAt(c.CustomItems, 1).StatusKey, "review", "default statusKey")
		eq(t, itemAt(c.CustomItems, 1).HideWhenMissing, false, "hideWhenMissing")
		eq(t, itemAt(c.CustomItems, 0).SelfColorize, false, "selfColorize")
		eq(t, c.DisabledSegments, []string{}, "disabled")
		eq(t, c.InvalidDisabledSegments, []string{}, "invalid disabled")
		eq(t, c.Layout == nil, true, "layout")
		eq(t, c.InvalidLayoutSegments, []string{}, "invalid layout")
		eq(t, c.Separator, "", "separator")
		eq(t, c.Placement, "above", "placement")
		eq(t, c.InvalidPlacement == nil, true, "invalid placement")
		eq(t, c.Welcome, true, "welcome")
		eq(t, c.StashSharpSShortcut, false, "stashSharpSShortcut")
		eq(t, c.CompactPromptMode, "queue", "queue")
	})
	tw(t, "custom-items", "parsePowerlineConfig accepts self-colored custom items", func(t *testing.T) {
		c := parsePowerlineConfig(js(t, `{"customItems":[{"id":"usage","color":"warning","selfColorize":true}]}`), []string{"default"})
		eq(t, c.CustomItems, []customItem{{ID: "usage", StatusKey: "usage", Position: "right", Color: "warning", SelfColorize: true, HideWhenMissing: true, ExcludeFromStatuses: true}}, "items")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports disabled segments", func(t *testing.T) {
		c := cfg(t, `{"preset":"default","customItems":[{"id":"ci"}],"disabledSegments":["queue","cost"," extension_statuses ","custom:ci","cost","unknown","custom:missing",123]}`)
		eq(t, c.DisabledSegments, []string{"queue", "cost", "extension_statuses", "custom:ci"}, "disabled")
		eq(t, c.InvalidDisabledSegments, []string{"unknown", "custom:missing", "123"}, "invalid")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports partial explicit layout rows", func(t *testing.T) {
		c := cfg(t, `{"preset":"default","customItems":[{"id":"ci"}],"layout":{"left":["model","custom:ci","model","unknown",123],"right":["model","cost"],"secondary":[]}}`)
		eq(t, c.Layout, &statusLayout{Left: []string{"model", "custom:ci"}, Right: []string{"cost"}, Secondary: []string{}}, "layout")
		eq(t, c.InvalidLayoutSegments, []string{"left:unknown", "left:123", "right:model"}, "invalid")
	})
	tw(t, "custom-items", "parsePowerlineConfig preserves reporter layout groups", func(t *testing.T) {
		c := cfg(t, `{"preset":"default","layout":{"left":["model"],"right":["path"],"secondary":["thinking"]}}`)
		eq(t, c.Layout, &statusLayout{Left: []string{"model"}, Right: []string{"path"}, Secondary: []string{"thinking"}}, "layout")
		eq(t, c.InvalidLayoutSegments, []string{}, "invalid")
		l, r, sec := mergeSegmentsWithCustomItems(presets["default"], c.CustomItems, c.Layout, c.DisabledSegments)
		eq(t, [][]string{l, r, sec}, [][]string{{"model"}, {"path"}, {"thinking"}}, "merged")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports separator overrides", func(t *testing.T) {
		eq(t, cfg(t, `{"preset":"default","separator":" chevron "}`).Separator, "chevron", "valid")
		eq(t, cfg(t, `{"preset":"default","separator":"sparkle"}`).Separator, "", "invalid")
	})
	tw(t, "custom-items", "configured separators resolve independently of presets", func(t *testing.T) {
		preset := getSeparator(presets["default"].separator).Left
		configured := getSeparator("chevron").Left
		eq(t, configured != preset, true, "differs")
		eq(t, configured, "›", "chevron")
	})
	tw(t, "custom-items", "${style} separator leaves padding to the footer renderer", func(t *testing.T) {
		for _, c := range []struct{ style, glyph string }{{"pipe", "|"}, {"slash", "/"}} {
			sep := getSeparator(c.style)
			eq(t, sep.Left, c.glyph, c.style+" left")
			eq(t, sep.Right, c.glyph, c.style+" right")
			// The renderer adds one space on each side; layout budgets the same two columns.
			eq(t, "model "+sep.Left+" directory", "model "+c.glyph+" directory", "joined")
			eq(t, len(sep.Left)+2, 3, "width")
		}
	})
	tw(t, "custom-items", "parsePowerlineConfig validates primary powerline placement", func(t *testing.T) {
		below := cfg(t, `{"preset":"compact","placement":"below"}`)
		invalid := cfg(t, `{"preset":"compact","placement":"sideways"}`)
		eq(t, below.Placement, "below", "below")
		eq(t, below.InvalidPlacement == nil, true, "below valid")
		eq(t, invalid.Placement, "above", "invalid")
		eq(t, invalid.InvalidPlacement, s("sideways"), "invalid text")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports queue compact prompt mode", func(t *testing.T) {
		eq(t, cfg(t, `{}`).CompactPromptMode, "queue", "default")
		eq(t, cfg(t, `{"queue":{"compactPromptMode":"native"}}`).CompactPromptMode, "native", "native")
		eq(t, cfg(t, `{"queue":{"compactPromptMode":"passthrough"}}`).CompactPromptMode, "queue", "invalid")
		eq(t, parsePowerlineConfig(js(t, `"compact"`), cfgPresets).CompactPromptMode, "queue", "shorthand")
	})
	tw(t, "custom-items", "parsePowerlineConfig supports welcome and legacy sharp-S settings", func(t *testing.T) {
		c := cfg(t, `{"preset":"compact","welcome":false,"stashSharpSShortcut":true}`)
		short := parsePowerlineConfig(js(t, `"compact"`), cfgPresets)
		eq(t, c.Welcome, false, "welcome")
		eq(t, c.StashSharpSShortcut, true, "sharp-S")
		eq(t, short.Welcome, true, "shorthand welcome")
		eq(t, short.StashSharpSShortcut, false, "shorthand sharp-S")
	})
	tw(t, "custom-items", "parsePowerlineConfig extracts supported segment options", func(t *testing.T) {
		c := cfg(t, `{"preset":"default","model":{"showThinkingLevel":true,"display":"qualified"},"path":{"mode":"full","maxLength":120},"git":{"showBranch":false,"showStaged":false,"showUnstaged":true,"showUntracked":false,"polling":"branch","hostIcon":true},"time":{"format":"12h","showSeconds":true},"cost":{"subscriptionDisplay":"both","currency":"cny"},"workingVibes":{"color":"rainbow"}}`)
		eq(t, c.SegmentOptions, segmentOptions{
			Model: &modelOptions{ShowThinkingLevel: ptr(true), Display: s("qualified")},
			Path:  &pathOptions{Mode: s("full"), MaxLength: ptr(120)},
			Git:   &gitOptions{ShowBranch: ptr(false), ShowStaged: ptr(false), ShowUnstaged: ptr(true), ShowUntracked: ptr(false), Polling: s("branch"), HostIcon: ptr(true)},
			Time:  &timeOptions{Format: s("12h"), ShowSeconds: ptr(true)},
			Cost:  &costOptions{SubscriptionDisplay: s("both"), Currency: s("CNY")},
		}, "options")
		eq(t, c.WorkingVibesColor, "rainbow", "workingVibes")
		eq(t, cfg(t, `{"cost":{"currency":"BTC"}}`).SegmentOptions, segmentOptions{Cost: &costOptions{}}, "invalid currency")
	})
	tw(t, "custom-items", "parsePowerlineConfig accepts working-vibe theme colors and hex colors", func(t *testing.T) {
		eq(t, parsePowerlineConfig(js(t, `{"workingVibes":{"color":"warning"}}`), []string{"default"}).WorkingVibesColor, "warning", "semantic")
		eq(t, parsePowerlineConfig(js(t, `{"workingVibes":{"color":"#89d281"}}`), []string{"default"}).WorkingVibesColor, "#89d281", "hex")
	})
	tw(t, "custom-items", "mergeSegmentOptions lets user config override preset segment defaults", func(t *testing.T) {
		merged := mergeSegmentOptions(
			segmentOptions{Path: &pathOptions{Mode: s("basename"), MaxLength: ptr(20)}, Git: &gitOptions{ShowBranch: ptr(true), ShowUntracked: ptr(true)}},
			segmentOptions{Path: &pathOptions{Mode: s("full")}, Git: &gitOptions{ShowUntracked: ptr(false)}, Cost: &costOptions{SubscriptionDisplay: s("reported-cost")}})
		eq(t, merged, segmentOptions{
			Model: &modelOptions{}, Path: &pathOptions{Mode: s("full"), MaxLength: ptr(20)},
			Git: &gitOptions{ShowBranch: ptr(true), ShowUntracked: ptr(false)}, Time: &timeOptions{},
			Cost: &costOptions{SubscriptionDisplay: s("reported-cost")}, Context: &contextOptions{}, CacheRead: &cacheReadOptions{},
		}, "merged")
	})
	items3 := []customItem{ci("ci", "ci", "left", true, true), ci("timer", "timer", "right", true, true), ci("review", "review", "secondary", true, true)}
	tw(t, "custom-items", "mergeSegmentsWithCustomItems appends custom segment ids by position", func(t *testing.T) {
		l, r, sec := mergeSegmentsWithCustomItems(presetDef{left: []string{"path"}, right: []string{"git"}, secondary: []string{"extension_statuses"}, separator: "powerline"}, items3, nil, nil)
		eq(t, l, []string{"path", "custom:ci"}, "left")
		eq(t, r, []string{"git", "custom:timer"}, "right")
		eq(t, sec, []string{"extension_statuses", "custom:review"}, "secondary")
	})
	tw(t, "custom-items", "mergeSegmentsWithCustomItems filters disabled segment ids", func(t *testing.T) {
		l, r, sec := mergeSegmentsWithCustomItems(presetDef{left: []string{"path", "model"}, right: []string{"git", "cost"}, secondary: []string{"extension_statuses"}, separator: "powerline"}, items3, nil,
			[]string{"model", "cost", "custom:ci", "custom:review"})
		eq(t, l, []string{"path"}, "left")
		eq(t, r, []string{"git", "custom:timer"}, "right")
		eq(t, sec, []string{"extension_statuses"}, "secondary")
	})
	tw(t, "custom-items", "mergeSegmentsWithCustomItems applies partial layout rows before disabled filtering", func(t *testing.T) {
		l, r, sec := mergeSegmentsWithCustomItems(
			presetDef{left: []string{"path", "model"}, right: []string{"git", "cost", "extension_statuses"}, secondary: []string{"extension_statuses"}, separator: "powerline"},
			[]customItem{ci("ci", "ci", "right", true, true), ci("review", "review", "secondary", true, true)},
			&statusLayout{Left: []string{"model", "custom:ci", "extension_statuses"}, Secondary: []string{}}, []string{"model"})
		eq(t, l, []string{"custom:ci", "extension_statuses"}, "left")
		eq(t, r, []string{"git", "cost"}, "right")
		eq(t, sec, []string{}, "secondary")
	})
	tw(t, "custom-items", "nextPowerlineSettingWithPreset preserves object settings", func(t *testing.T) {
		updated := nextPowerlineSettingWithPreset(js(t, `{"preset":"default","customItems":[{"id":"ci"}]}`), "compact")
		eq(t, updated, js(t, `{"preset":"compact","customItems":[{"id":"ci"}]}`), "updated")
	})
	tw(t, "custom-items", "nextPowerlineSettingWithOptions preserves object settings", func(t *testing.T) {
		updated := nextPowerlineSettingWithOptions(js(t, `{"preset":"default","customItems":[{"id":"ci"}]}`), jo(t, `{"placement":"below"}`), "compact")
		eq(t, updated, js(t, `{"preset":"default","customItems":[{"id":"ci"}],"placement":"below"}`), "updated")
	})
	tw(t, "custom-items", "nextPowerlineSettingWithOptions converts string presets to object settings", func(t *testing.T) {
		eq(t, nextPowerlineSettingWithOptions("compact", jo(t, `{"placement":"below"}`), "compact"), js(t, `{"preset":"compact","placement":"below"}`), "converted")
	})
	tw(t, "custom-items", "collectHiddenExtensionStatusKeys includes default custom status keys", func(t *testing.T) {
		hidden := collectHiddenExtensionStatusKeys([]customItem{ci("ci", "ci-status", "right", true, true), ci("review", "review", "secondary", true, false)})
		eq(t, hidden["ci-status"], true, "promoted")
		eq(t, hidden["review"], false, "kept")
	})
	tw(t, "custom-items", "normalizeCompactExtensionStatus strips baked-in trailing separators", func(t *testing.T) {
		eq(t, normalizeCompactExtensionStatus("CI ok · "), s("CI ok"), "dot")
		eq(t, normalizeCompactExtensionStatus("CI ok |   "), s("CI ok"), "pipe")
		eq(t, normalizeCompactExtensionStatus("[notice] queued") == nil, true, "notification")
	})
	tw(t, "custom-items", "normalizeExtensionStatusValue keeps notification-style statuses renderable for custom items", func(t *testing.T) {
		eq(t, normalizeExtensionStatusValue("[review] queued · ", false), s("[review] queued"), "kept")
	})
	tw(t, "custom-items", "getNotificationExtensionStatuses skips promoted hidden status keys", func(t *testing.T) {
		statuses := []statusEntry{{"ci-status", "[ci] queued"}, {"review", "[review] running"}, {"plain", "plain status"}}
		eq(t, getNotificationExtensionStatuses(statuses, map[string]bool{"ci-status": true}), []string{"[review] running"}, "notifications")
	})
}
