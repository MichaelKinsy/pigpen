package frontend

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// workingNode draws PiG's working indicator as a Tern spinner labeled with
// its message. Tern animates the spinner, so PiG's ticks send nothing. Tern
// spinners have fixed styles: PiG's default frames are Tern's braille, and an
// extension's own frames draw as braille too; an extension that hides the
// spinner (no frames) leaves its message as text. The retry countdown takes
// the warning tone that PiG draws it in.
func workingNode(id string, working fe.Working) map[string]any {
	message := stripANSI(working.Message)
	if working.Frames != nil && len(working.Frames) == 0 {
		return map[string]any{"id": id, "k": "text", "p": map[string]any{"text": message, "role": "pig.working"}}
	}
	props := map[string]any{"style": "braille", "label": message, "role": "pig.working"}
	if working.Kind == fe.WorkingRetry {
		props["tone"] = "warning"
	}
	return map[string]any{"id": id, "k": "spinner", "p": props}
}

// footerNode draws PiG's footer as a Tern status strip. The left group holds
// what Pi's first footer line shows (the working directory, git branch and
// session name) and the extension statuses of its third line; the right
// group holds its second line: usage, context, the experimental badge and
// the model. Tern drops the lowest-priority segments when the strip is
// narrow, the model and context last. Segments that show nothing are hidden
// rather than removed, so a change of the footer's data is a set of props.
func footerNode(id string, footer fe.Footer) map[string]any {
	segs := []any{
		seg(id+".cwd", map[string]any{"icon": "folder", "text": footer.Cwd, "priority": 7, "min": map[string]any{"w": "12ch"}}),
		seg(id+".branch", map[string]any{"icon": "branch", "text": footer.GitBranch, "priority": 5, "hidden": footer.GitBranch == ""}),
		seg(id+".session", map[string]any{"icon": "message", "text": footer.SessionName, "priority": 3, "hidden": footer.SessionName == ""}),
	}
	for i, status := range footer.ExtensionStatuses {
		segs = append(segs, seg(id+".status."+strconv.Itoa(i), map[string]any{"text": stripANSI(status), "priority": 2}))
	}
	usage := usageText(footer)
	segs = append(segs,
		seg(id+".usage", map[string]any{"side": "right", "text": usage, "priority": 6, "hidden": usage == ""}),
		contextSeg(id+".context", footer),
		seg(id+".xp", map[string]any{"side": "right", "text": "xp", "tone": "warning", "priority": 1, "hidden": !footer.Experimental}),
		modelSeg(id+".model", footer),
	)
	return map[string]any{"id": id, "k": "status", "p": map[string]any{"role": "pig.footer"}, "c": segs}
}

func seg(id string, props map[string]any, children ...any) map[string]any {
	node := map[string]any{"id": id, "k": "seg", "p": props}
	if len(children) > 0 {
		node["c"] = children
	}
	return node
}

// usageText is Pi's footer usage: the token totals, the latest cache-hit rate
// while the cache was used, and the cost, which a subscription shows even at
// zero.
func usageText(footer fe.Footer) string {
	usage := footer.UsageTotals
	var parts []string
	for _, part := range []struct {
		prefix string
		count  int
	}{{"↑", usage.Input}, {"↓", usage.Output}, {"R", usage.CacheRead}, {"W", usage.CacheWrite}} {
		if part.count != 0 {
			parts = append(parts, part.prefix+formatTokens(part.count))
		}
	}
	if (usage.CacheRead > 0 || usage.CacheWrite > 0) && footer.LatestCacheHitRate != nil {
		parts = append(parts, "CH"+toFixed(*footer.LatestCacheHitRate, 1)+"%")
	}
	if (usage.Cost != 0 && !math.IsNaN(usage.Cost)) || footer.UsingSubscription {
		cost := "$" + toFixed(usage.Cost, 3)
		if footer.UsingSubscription {
			cost += " (sub)"
		}
		parts = append(parts, cost)
	}
	return strings.Join(parts, " ")
}

// contextSeg shows the context usage as Pi words it, with a meter of the
// window's fill. Pi colors it warning above 70% and error above 90%; an
// unknown size leaves the meter without a value.
func contextSeg(id string, footer fe.Footer) map[string]any {
	usage := footer.ContextUsage
	auto := ""
	if footer.AutoCompact {
		auto = " (auto)"
	}
	meterProps := map[string]any{}
	segProps := map[string]any{"side": "right", "priority": 8}
	segProps["text"] = "?/" + formatTokens(usage.ContextWindow) + auto
	if !usage.Unknown {
		segProps["text"] = toFixed(usage.Percent, 1) + "%/" + formatTokens(usage.ContextWindow) + auto
		meterProps["value"] = usage.Percent / 100
		tone := ""
		switch {
		case usage.Percent > 90:
			tone = "error"
		case usage.Percent > 70:
			tone = "warning"
		}
		if tone != "" {
			segProps["tone"], meterProps["tone"] = tone, tone
		}
	}
	return seg(id, segProps, map[string]any{"id": id + ".meter", "k": "meter", "p": meterProps})
}

// modelSeg shows Pi's model text: the provider while several are available,
// the model, its thinking level and the model a virtual selection routed to,
// with Tern's effort glyph for the thinking level of a model that reasons.
func modelSeg(id string, footer fe.Footer) map[string]any {
	text := footer.Model
	switch footer.ThinkingLevel {
	case "":
	case "off":
		text += " • thinking off"
	default:
		text += " • " + footer.ThinkingLevel
	}
	if routed := footer.Routed; routed.Model != "" {
		text += " → " + routed.Model
		if routed.ThinkingLevel != "" {
			text += " • " + routed.ThinkingLevel
		}
	}
	if footer.Provider != "" {
		text = "(" + footer.Provider + ") " + text
	}
	effortProps := map[string]any{"hidden": footer.ThinkingLevel == ""}
	if footer.ThinkingLevel != "" {
		effortProps["level"] = footer.ThinkingLevel
	}
	effort := map[string]any{"id": id + ".effort", "k": "effort", "p": effortProps}
	return seg(id, map[string]any{"side": "right", "icon": "model", "text": text, "priority": 9, "min": map[string]any{"w": "12ch"}}, effort)
}

// formatTokens is Pi's footer formatTokens: one decimal below ten thousand
// and ten million, whole thousands and millions rounded half up elsewhere.
func formatTokens(count int) string {
	switch {
	case count < 1000:
		return strconv.Itoa(count)
	case count < 10_000:
		return toFixed(float64(count)/1000, 1) + "k"
	case count < 1_000_000:
		return strconv.Itoa(int(math.Floor(float64(count)/1000+0.5))) + "k"
	case count < 10_000_000:
		return toFixed(float64(count)/1_000_000, 1) + "M"
	default:
		return strconv.Itoa(int(math.Floor(float64(count)/1_000_000+0.5))) + "M"
	}
}

// toFixed is JavaScript's Number.prototype.toFixed: the exact value of x
// rounded to digits decimals, a tie going away from zero.
func toFixed(x float64, digits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	exact := new(big.Rat).SetFloat64(x)
	negative := exact.Sign() < 0
	exact.Abs(exact)
	exact.Mul(exact, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)))
	quotient, remainder := new(big.Int).QuoRem(exact.Num(), exact.Denom(), new(big.Int))
	if remainder.Lsh(remainder, 1).Cmp(exact.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	text := quotient.String()
	if digits > 0 {
		if len(text) <= digits {
			text = strings.Repeat("0", digits-len(text)+1) + text
		}
		text = text[:len(text)-digits] + "." + text[len(text)-digits:]
	}
	if negative && strings.Trim(text, "0.") != "" {
		text = "-" + text
	}
	return text
}

// stripANSI removes escape sequences, which Tern's plain text props would
// show literally.
func stripANSI(text string) string {
	if !strings.Contains(text, "\x1b") {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] != 0x1b {
			out.WriteByte(text[i])
			continue
		}
		if i+1 >= len(text) {
			break
		}
		switch text[i+1] {
		case '[':
			i += 2
			for i < len(text) && (text[i] < 0x40 || text[i] > 0x7e) {
				i++
			}
		case ']', '_', 'P':
			i += 2
			for i < len(text) && text[i] != 0x07 && !(text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\') {
				i++
			}
			if i < len(text) && text[i] == 0x1b {
				i++
			}
		default:
			i++
		}
	}
	return out.String()
}
