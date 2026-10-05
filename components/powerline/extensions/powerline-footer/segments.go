package powerline_footer

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// The status line segments. upstream: segments.ts.

func segColor(ctx *segmentContext, semantic, text string) string {
	return fg(ctx.Theme, semantic, text, ctx.Colors)
}

func withIcon(icon, text string) string {
	if icon != "" {
		return icon + " " + text
	}
	return text
}

func formatTokens(n float64) string {
	switch {
	case n < 1000:
		return jsNumber(n)
	case n < 10000:
		return jsToFixed(n/1000, 1) + "k"
	case n < 1000000:
		return jsNumber(jsRound(n/1000)) + "k"
	case n < 10000000:
		return jsToFixed(n/1000000, 1) + "M"
	}
	return jsNumber(jsRound(n/1000000)) + "M"
}

func formatDuration(ms float64) string {
	seconds := math.Floor(ms / 1000)
	minutes := math.Floor(seconds / 60)
	hours := math.Floor(minutes / 60)
	switch {
	case hours > 0:
		return jsNumber(hours) + "h" + jsNumber(math.Mod(minutes, 60)) + "m"
	case minutes > 0:
		return jsNumber(minutes) + "m" + jsNumber(math.Mod(seconds, 60)) + "s"
	}
	return jsNumber(seconds) + "s"
}

// utf16Len and utf16Tail measure and cut strings in UTF-16 code units, as JavaScript's length and slice do.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func utf16Tail(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if n >= len(u) {
		return s
	}
	return string(utf16.Decode(u[len(u)-n:]))
}

func notVisible() renderedSegment { return renderedSegment{} }

func orStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func modelSegment(ctx *segmentContext) renderedSegment {
	icons := getIcons()
	var opts modelOptions
	if ctx.Options.Model != nil {
		opts = *ctx.Options.Model
	}
	name := "no-model"
	if ctx.Model != nil {
		name = orStr(ctx.Model.Name, ctx.Model.ID, "no-model")
	}
	if opts.Display != nil && *opts.Display == "qualified" && ctx.Model != nil && ctx.Model.ID != "" {
		provider := orStr(ctx.Model.Provider, ctx.Model.ProviderID, ctx.Model.ProviderName)
		if provider != "" && !strings.HasPrefix(ctx.Model.ID, provider+"/") {
			name = provider + "/" + ctx.Model.ID
		} else {
			name = ctx.Model.ID
		}
	} else if rest, ok := strings.CutPrefix(name, "Claude "); ok {
		name = rest
	}
	content := withIcon(icons.Model, name)
	if (opts.ShowThinkingLevel == nil || *opts.ShowThinkingLevel) && ctx.Model != nil && ctx.Model.Reasoning {
		level := orStr(ctx.ThinkingLevel, "off")
		if level != "off" {
			if t, ok := getThinkingText(level); ok && t != "" {
				content += sepDot + t
			}
		}
	}
	return renderedSegment{segColor(ctx, "model", content), true}
}

func pathSegment(ctx *segmentContext) renderedSegment {
	icons := getIcons()
	var opts pathOptions
	if ctx.Options.Path != nil {
		opts = *ctx.Options.Path
	}
	mode := "basename"
	if opts.Mode != nil {
		mode = *opts.Mode
	}
	pwd := ctx.CWD
	if pwd == "" {
		pwd, _ = os.Getwd()
	}
	home := orStr(os.Getenv("HOME"), os.Getenv("USERPROFILE"))
	if mode == "basename" {
		if b := filepath.Base(pwd); b != "" && b != "/" && b != "." {
			pwd = b
		}
	} else {
		if home != "" && strings.HasPrefix(pwd, home) {
			pwd = "~" + pwd[len(home):]
		}
		if strings.HasPrefix(pwd, "/work/") {
			pwd = pwd[6:]
		}
		if mode == "abbreviated" {
			maxLen := 40
			if opts.MaxLength != nil {
				maxLen = *opts.MaxLength
			}
			if utf16Len(pwd) > maxLen {
				pwd = "…" + utf16Tail(pwd, maxLen-1)
			}
		}
	}
	return renderedSegment{segColor(ctx, "path", withIcon(icons.Folder, pwd)), true}
}

func gitSegment(ctx *segmentContext) renderedSegment {
	icons := getIcons()
	var opts gitOptions
	if ctx.Options.Git != nil {
		opts = *ctx.Options.Git
	}
	branch := ""
	if ctx.Git.Branch != nil {
		branch = *ctx.Git.Branch
	}
	g := ctx.Git
	dirty := g.Staged > 0 || g.Unstaged > 0 || g.Untracked > 0
	if branch == "" && !dirty {
		return notVisible()
	}
	showBranch := opts.ShowBranch == nil || *opts.ShowBranch
	branchColor := "gitClean"
	if dirty {
		branchColor = "gitDirty"
	}
	content := ""
	if showBranch && branch != "" {
		icon := icons.Branch
		if opts.HostIcon != nil && *opts.HostIcon {
			icon = resolveBranchIcon(icons, ctx.CWD)
		}
		content = segColor(ctx, branchColor, withIcon(icon, branch))
	}
	if dirty {
		var indicators []string
		if (opts.ShowUnstaged == nil || *opts.ShowUnstaged) && g.Unstaged > 0 {
			indicators = append(indicators, applyColor(ctx.Theme, "warning", "*"+jsNumber(float64(g.Unstaged))))
		}
		if (opts.ShowStaged == nil || *opts.ShowStaged) && g.Staged > 0 {
			indicators = append(indicators, applyColor(ctx.Theme, "success", "+"+jsNumber(float64(g.Staged))))
		}
		if (opts.ShowUntracked == nil || *opts.ShowUntracked) && g.Untracked > 0 {
			indicators = append(indicators, applyColor(ctx.Theme, "muted", "?"+jsNumber(float64(g.Untracked))))
		}
		if len(indicators) > 0 {
			text := strings.Join(indicators, " ")
			if content == "" && !showBranch {
				prefix := ""
				if icons.Git != "" {
					prefix = icons.Git + " "
				}
				content = segColor(ctx, branchColor, prefix) + text
			} else {
				if content != "" {
					content += " "
				}
				content += text
			}
		}
	}
	if content == "" {
		return notVisible()
	}
	return renderedSegment{content, true}
}

// resolveBranchIcon: the origin host's logo when known, the generic git logo for an unrecognised remote, else the branch icon.
func resolveBranchIcon(icons iconSet, cwd string) string {
	switch host := getGitRemoteHost(cwd); host {
	case "github":
		return icons.Github
	case "gitlab":
		return icons.Gitlab
	case "bitbucket":
		return icons.Bitbucket
	case "other":
		return icons.Git
	}
	return icons.Branch
}

func thinkingSegment(ctx *segmentContext) renderedSegment {
	level := orStr(ctx.ThinkingLevel, "off")
	labels := map[string]string{"off": "off", "minimal": "min", "low": "low", "medium": "med", "high": "high", "xhigh": "xhigh"}
	label, ok := labels[level]
	if !ok {
		label = level
	}
	content := "think:" + label
	semantic := "thinking"
	switch level {
	case "high":
		semantic = "thinkingHigh"
	case "xhigh":
		semantic = "thinkingXhigh"
	case "max":
		semantic = "thinkingMax"
	case "minimal":
		semantic = "thinkingMinimal"
	case "low":
		semantic = "thinkingLow"
	case "medium":
		semantic = "thinkingMedium"
	}
	return renderedSegment{segColor(ctx, semantic, content), true}
}

func queueSegment(ctx *segmentContext) renderedSegment {
	q := ctx.Queue
	var parts []string
	if q.Compacting && q.QueueCount > 0 {
		parts = append(parts, "compact q "+jsNumber(float64(q.QueueCount)))
	} else if q.QueueCount > 0 {
		parts = append(parts, "q "+jsNumber(float64(q.QueueCount)))
	}
	if q.BlockedCount > 0 {
		parts = append(parts, "blocked "+jsNumber(float64(q.BlockedCount)))
	}
	if len(parts) == 0 {
		return notVisible()
	}
	return renderedSegment{segColor(ctx, "queue", strings.Join(parts, sepDot)), true}
}

func tokenSegment(ctx *segmentContext, n float64, icon string) renderedSegment {
	if n == 0 {
		return notVisible()
	}
	return renderedSegment{segColor(ctx, "tokens", withIcon(icon, formatTokens(n))), true}
}

func costSegment(ctx *segmentContext) renderedSegment {
	cost := ctx.Usage.Cost + ctx.Usage.SubagentCost
	sub := ctx.UsingSubscription
	if cost == 0 && !sub {
		return notVisible()
	}
	currency := ""
	if ctx.Options.Cost != nil && ctx.Options.Cost.Currency != nil {
		currency = *ctx.Options.Cost.Currency
	}
	reported := ""
	if cost > 0 {
		reported = formatUsdCost(cost, currency)
	}
	if !sub {
		if reported != "" {
			return renderedSegment{segColor(ctx, "cost", reported), true}
		}
		return notVisible()
	}
	display := "subscription"
	if ctx.Options.Cost != nil && ctx.Options.Cost.SubscriptionDisplay != nil {
		display = *ctx.Options.Cost.SubscriptionDisplay
	}
	if display == "reported-cost" && reported != "" {
		return renderedSegment{segColor(ctx, "cost", reported), true}
	}
	if display == "both" && reported != "" {
		return renderedSegment{segColor(ctx, "cost", reported+" (sub)"), true}
	}
	return renderedSegment{segColor(ctx, "cost", "(sub)"), true}
}

func contextPctSegment(ctx *segmentContext) renderedSegment {
	if ctx.CustomCompactionEnabled {
		return notVisible()
	}
	icons := getIcons()
	autoIcon := ""
	if ctx.AutoCompactEnabled && icons.Auto != "" {
		autoIcon = " " + icons.Auto
	}
	percentOnly := ctx.Options.Context != nil && ctx.Options.Context.Format != nil && *ctx.Options.Context.Format == "percent"
	known := ctx.ContextTokens != nil && ctx.ContextPercent != nil
	approx := ""
	if ctx.ContextApproximate {
		approx = "~"
	}
	var text string
	switch {
	case percentOnly && known:
		text = approx + jsNumber(jsRound(*ctx.ContextPercent)) + "%"
	case percentOnly:
		text = "?"
	case known:
		text = approx + formatTokens(*ctx.ContextTokens) + "/" + formatTokens(ctx.ContextWindow) + " (" + jsToFixed(*ctx.ContextPercent, 1) + "%)" + autoIcon
	default:
		text = "?/" + formatTokens(ctx.ContextWindow) + autoIcon
	}
	colored := func(semantic string) string {
		if percentOnly {
			return segColor(ctx, semantic, text)
		}
		return withIcon(icons.Context, segColor(ctx, semantic, text))
	}
	switch {
	case known && *ctx.ContextPercent > 90:
		return renderedSegment{colored("contextError"), true}
	case known && *ctx.ContextPercent > 70:
		return renderedSegment{colored("contextWarn"), true}
	}
	return renderedSegment{colored("context"), true}
}

func contextTotalSegment(ctx *segmentContext) renderedSegment {
	if ctx.CustomCompactionEnabled || ctx.ContextWindow == 0 {
		return notVisible()
	}
	return renderedSegment{segColor(ctx, "context", withIcon(getIcons().Context, formatTokens(ctx.ContextWindow))), true}
}

func timeSpentSegment(ctx *segmentContext) renderedSegment {
	elapsed := float64(clock() - ctx.SessionStart)
	if elapsed < 1000 {
		return notVisible()
	}
	return renderedSegment{withIcon(getIcons().Time, formatDuration(elapsed)), true}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + jsNumber(float64(n))
	}
	return jsNumber(float64(n))
}

func timeSegment(ctx *segmentContext) renderedSegment {
	var opts timeOptions
	if ctx.Options.Time != nil {
		opts = *ctx.Options.Time
	}
	now := time.UnixMilli(clock()).In(time.Local)
	hours := now.Hour()
	suffix := ""
	if opts.Format != nil && *opts.Format == "12h" {
		suffix = "am"
		if hours >= 12 {
			suffix = "pm"
		}
		hours %= 12
		if hours == 0 {
			hours = 12
		}
	}
	str := jsNumber(float64(hours)) + ":" + pad2(now.Minute())
	if opts.ShowSeconds != nil && *opts.ShowSeconds {
		str += ":" + pad2(now.Second())
	}
	return renderedSegment{withIcon(getIcons().Time, str+suffix), true}
}

func sessionSegment(ctx *segmentContext) renderedSegment {
	display := ctx.SessionName
	if jsTrim(display) == "" {
		display = ""
		if ctx.SessionID != "" {
			u := utf16.Encode([]rune(ctx.SessionID))
			if len(u) > 8 {
				u = u[:8]
			}
			display = string(utf16.Decode(u))
		}
		if display == "" {
			display = "new"
		}
	}
	return renderedSegment{withIcon(getIcons().Session, display), true}
}

func hostnameSegment() renderedSegment {
	name, _, _ := strings.Cut(hostnameFn(), ".")
	return renderedSegment{withIcon(getIcons().Host, name), true}
}

func cacheReadSegment(ctx *segmentContext) renderedSegment {
	icons := getIcons()
	cr, in := ctx.Usage.CacheRead, ctx.Usage.Input
	if cr == 0 {
		return notVisible()
	}
	format := "tokens"
	if ctx.Options.CacheRead != nil && ctx.Options.CacheRead.Format != nil {
		format = *ctx.Options.CacheRead.Format
	}
	hit := "0"
	if in+cr > 0 {
		hit = jsToFixed(cr/(in+cr)*100, 0)
	}
	join := func(parts ...string) string {
		var keep []string
		for _, p := range parts {
			if p != "" {
				keep = append(keep, p)
			}
		}
		return strings.Join(keep, " ")
	}
	var content string
	if format == "percent" {
		content = join(icons.Cache, hit+"%")
	} else {
		content = join(icons.Cache, icons.Input, formatTokens(cr))
		if format == "both" {
			content += " (" + hit + "%)"
		}
	}
	return renderedSegment{segColor(ctx, "tokens", content), true}
}

func cacheWriteSegment(ctx *segmentContext) renderedSegment {
	icons := getIcons()
	if ctx.Usage.CacheWrite == 0 {
		return notVisible()
	}
	var parts []string
	for _, p := range []string{icons.Cache, icons.Output, formatTokens(ctx.Usage.CacheWrite)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return renderedSegment{segColor(ctx, "tokens", strings.Join(parts, " ")), true}
}

func extensionStatusesSegment(ctx *segmentContext) renderedSegment {
	if len(ctx.ExtensionStatuses) == 0 {
		return notVisible()
	}
	var parts []string
	for _, e := range ctx.ExtensionStatuses {
		if ctx.HiddenStatusKeys[e.Key] {
			continue
		}
		if e.Value == "" {
			continue
		}
		if n := normalizeCompactExtensionStatus(e.Value); n != nil {
			parts = append(parts, *n)
		}
	}
	if len(parts) == 0 {
		return notVisible()
	}
	// Normalization strips trailing SGR resets; isolate each status's styling.
	for i, p := range parts {
		parts[i] = "\x1b[0m" + p + "\x1b[0m"
	}
	return renderedSegment{strings.Join(parts, sepDot), true}
}

func renderCustomSegment(id string, ctx *segmentContext) renderedSegment {
	custom, ok := ctx.CustomItems[strings.TrimPrefix(id, "custom:")]
	if !ok {
		return notVisible()
	}
	raw := ""
	for _, e := range ctx.ExtensionStatuses {
		if e.Key == custom.StatusKey {
			raw = e.Value
			break
		}
	}
	var normalized *string
	if raw != "" {
		normalized = normalizeExtensionStatusValue(raw, custom.SelfColorize)
	}
	if normalized == nil {
		if custom.HideWhenMissing {
			return notVisible()
		}
		return renderedSegment{orStr(custom.Prefix, custom.ID), true}
	}
	content := *normalized
	if custom.Prefix != "" {
		content = custom.Prefix + sepDot + content
	}
	if custom.Color != "" && !custom.SelfColorize {
		content = applyColor(ctx.Theme, custom.Color, content)
	}
	return renderedSegment{content, true}
}

// renderSegment renders one segment by id; an unknown id is not visible.
func renderSegment(id string, c segmentContext) renderedSegment {
	ctx := &c
	if strings.HasPrefix(id, "custom:") {
		return renderCustomSegment(id, ctx)
	}
	icons := getIcons
	switch id {
	case "model":
		return modelSegment(ctx)
	case "shell_mode":
		return notVisible() // the bash-mode editor is not ported, so shell mode is never active
	case "path":
		return pathSegment(ctx)
	case "git":
		return gitSegment(ctx)
	case "thinking":
		return thinkingSegment(ctx)
	case "subagents":
		return notVisible()
	case "queue":
		return queueSegment(ctx)
	case "token_in":
		return tokenSegment(ctx, ctx.Usage.Input, icons().Input)
	case "token_out":
		return tokenSegment(ctx, ctx.Usage.Output, icons().Output)
	case "token_total":
		return tokenSegment(ctx, ctx.Usage.Input+ctx.Usage.Output+ctx.Usage.CacheRead+ctx.Usage.CacheWrite, icons().Tokens)
	case "cost":
		return costSegment(ctx)
	case "context_pct":
		return contextPctSegment(ctx)
	case "context_total":
		return contextTotalSegment(ctx)
	case "time_spent":
		return timeSpentSegment(ctx)
	case "time":
		return timeSegment(ctx)
	case "session":
		return sessionSegment(ctx)
	case "hostname":
		return hostnameSegment()
	case "cache_read":
		return cacheReadSegment(ctx)
	case "cache_write":
		return cacheWriteSegment(ctx)
	case "extension_statuses":
		return extensionStatusesSegment(ctx)
	}
	return notVisible()
}
