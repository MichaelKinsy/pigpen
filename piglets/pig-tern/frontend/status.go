package frontend

import (
	"math"
	"math/bits"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// workingElement draws PiG's working indicator as a Tern spinner labeled with
// its message. Tern animates the spinner, so PiG's ticks send nothing. Tern
// spinners have fixed styles: PiG's default frames are Tern's braille, and an
// extension's own frames draw as braille too; an extension that hides the
// spinner (no frames) leaves its message as text. The retry countdown takes
// the warning tone that PiG draws it in. Later changes of the label (or
// text) and tone are raw props (see queueWorking).
func workingElement(id string, working fe.Working) tern.Element {
	h := workingHot(working)
	common := ui.Common{Key: id, Role: "pig.working"}
	if spinnerless(working) {
		return ui.Text{Common: common, Text: h.text}
	}
	common.Tone = h.tone
	return ui.Spinner{Common: common, Style: ui.SpinnerBraille, Label: ui.T(h.text)}
}

// spinnerless reports an indicator whose extension hid the spinner.
func spinnerless(working fe.Working) bool {
	return working.Frames != nil && len(working.Frames) == 0
}

// workingHot is the indicator's message and tone.
func workingHot(working fe.Working) hot {
	h := hot{text: stripANSI(working.Message)}
	if working.Kind == fe.WorkingRetry && !spinnerless(working) {
		h.tone = ui.ToneWarning
	}
	return h
}

// queueWorking queues a change of the indicator's message, as the spinner's
// label or the text, and of its tone. A fresh indicator's view holds them.
func (s *session) queueWorking(e *entry, working fe.Working, fresh bool) {
	h := workingHot(working)
	switch {
	case fresh:
		e.raw.text, e.raw.hot = h.text, h
	case spinnerless(working):
		s.patchText(e.tid, e.raw.text, h.text)
		e.raw.text = h.text
	default:
		s.setProps(e.tid, "label", &e.raw.hot, h)
	}
}

// The footer's segments and glyphs with raw props, and their keys under
// the footer's id.
const (
	segCwd = iota
	segBranch
	segSession
	segUsage
	segContext
	segXP
	segModel
	segMeter
	segEffort
	footerSlots
)

var footerKeys = [footerSlots]string{".cwd", ".branch", ".session", ".usage", ".context", ".xp", ".model", ".context.meter", ".model.effort"}

// footerProps are the footer's raw props as sent, with the TSP ids they
// go to; an extension status at index i is the segment "status.i".
type footerProps struct {
	ids       [footerSlots]string
	slots     [footerSlots]hot
	statusIDs []string
	statuses  []hot
}

// footerHot is the footer's props by slot, and its extension statuses'.
func footerHot(footer fe.Footer) (slots [footerSlots]hot, statuses []hot) {
	slots[segCwd] = hot{text: footer.Cwd}
	slots[segBranch] = hot{text: footer.GitBranch, hidden: footer.GitBranch == ""}
	slots[segSession] = hot{text: footer.SessionName, hidden: footer.SessionName == ""}
	usage := usageText(footer)
	slots[segUsage] = hot{text: usage, hidden: usage == ""}
	slots[segContext], slots[segMeter] = contextProps(footer)
	slots[segXP] = hot{hidden: !footer.Experimental}
	slots[segModel], slots[segEffort] = modelProps(footer)
	statuses = make([]hot, len(footer.ExtensionStatuses))
	for i, status := range footer.ExtensionStatuses {
		statuses[i] = hot{text: stripANSI(status)}
	}
	return slots, statuses
}

// footerElement draws PiG's footer as a Tern status strip. The left group holds
// what Pi's first footer line shows (the working directory, git branch and
// session name) and the extension statuses of its third line; the right
// group holds its second line: usage, context, the experimental badge and
// the model. Tern drops the lowest-priority segments when the strip is
// narrow, the model and context last. Segments that show nothing are
// hidden rather than removed. Later changes of the texts, tones, the
// meter's value, the effort level and whether a segment hides are raw
// props (see queueFooter), so only a change in the number of extension
// statuses renders.
func footerElement(id string, footer fe.Footer) tern.Element {
	slots, statuses := footerHot(footer)
	wide := &ui.Bounds{W: ui.Ch(12)}
	common := func(key string, h hot) ui.Common {
		return ui.Common{Key: key, Tone: h.tone, Hidden: h.hidden}
	}
	cwd := ui.Seg{Common: common("cwd", slots[segCwd]), Icon: ui.IconFolder, Text: slots[segCwd].text, Priority: 7}
	cwd.Min = wide
	segs := make([]tern.Element, 0, 7+len(statuses))
	segs = append(segs,
		cwd,
		ui.Seg{Common: common("branch", slots[segBranch]), Icon: ui.IconBranch, Text: slots[segBranch].text, Priority: 5},
		ui.Seg{Common: common("session", slots[segSession]), Icon: ui.IconMessage, Text: slots[segSession].text, Priority: 3},
	)
	for i, status := range statuses {
		segs = append(segs, ui.Seg{Common: common("status."+strconv.Itoa(i), status), Text: status.text, Priority: 2})
	}
	meter := ui.Meter{Common: common("meter", slots[segMeter])}
	if slots[segMeter].hasValue {
		meter.Value = new(math.Float64frombits(slots[segMeter].value))
	}
	xp := ui.Seg{Common: common("xp", slots[segXP]), Side: ui.SideRight, Text: "xp", Priority: 1}
	xp.Tone = ui.ToneWarning
	model := ui.Seg{Common: common("model", slots[segModel]), Side: ui.SideRight, Icon: ui.IconName("model"), Text: slots[segModel].text, Priority: 9,
		Children: []tern.Element{ui.Effort{Common: common("effort", slots[segEffort]), Level: ui.EffortLevel(slots[segEffort].text)}}}
	model.Min = wide
	segs = append(segs,
		ui.Seg{Common: common("usage", slots[segUsage]), Side: ui.SideRight, Text: slots[segUsage].text, Priority: 6},
		ui.Seg{Common: common("context", slots[segContext]), Side: ui.SideRight, Text: slots[segContext].text, Priority: 8, Children: []tern.Element{meter}},
		xp,
		model,
	)
	return ui.Status{Common: ui.Common{Key: id, Role: "pig.footer"}, Children: segs}
}

// queueFooter queues the raw props of the footer's segments that changed
// from prev; each segment is derived only when the fields it shows changed.
// A fresh footer's view, and a new status segment's, holds their props.
func (s *session) queueFooter(e *entry, prev, next fe.Footer, fresh bool) {
	f := &e.raw.footer
	if fresh {
		for i, key := range footerKeys {
			f.ids[i] = e.tid + key
		}
		f.slots, f.statuses = footerHot(next)
	} else {
		if prev.Cwd != next.Cwd {
			s.setSlot(f, segCwd, "text", hot{text: next.Cwd})
		}
		if prev.GitBranch != next.GitBranch {
			s.setSlot(f, segBranch, "text", hot{text: next.GitBranch, hidden: next.GitBranch == ""})
		}
		if prev.SessionName != next.SessionName {
			s.setSlot(f, segSession, "text", hot{text: next.SessionName, hidden: next.SessionName == ""})
		}
		if prev.UsageTotals != next.UsageTotals || prev.UsingSubscription != next.UsingSubscription ||
			!sameRate(prev.LatestCacheHitRate, next.LatestCacheHitRate) {
			usage := usageText(next)
			s.setSlot(f, segUsage, "text", hot{text: usage, hidden: usage == ""})
		}
		if prev.ContextUsage != next.ContextUsage || prev.AutoCompact != next.AutoCompact {
			seg, meter := contextProps(next)
			s.setSlot(f, segContext, "text", seg)
			s.setSlot(f, segMeter, "text", meter)
		}
		if prev.Experimental != next.Experimental {
			s.setSlot(f, segXP, "text", hot{hidden: !next.Experimental})
		}
		if prev.Model != next.Model || prev.Provider != next.Provider || prev.ThinkingLevel != next.ThinkingLevel || prev.Routed != next.Routed {
			model, effort := modelProps(next)
			s.setSlot(f, segModel, "text", model)
			s.setSlot(f, segEffort, "level", effort)
		}
	}
	n := len(next.ExtensionStatuses)
	for i := len(f.statusIDs); i < n; i++ {
		f.statusIDs = append(f.statusIDs, e.tid+".status."+strconv.Itoa(i))
	}
	for i, status := range next.ExtensionStatuses {
		h := hot{text: stripANSI(status)}
		if i >= len(f.statuses) {
			// A segment the render adds now, with its text.
			f.statuses = append(f.statuses, h)
			continue
		}
		s.setProps(f.statusIDs[i], "text", &f.statuses[i], h)
	}
	for i := n; i < len(f.statuses); i++ {
		s.dropPending(f.statusIDs[i])
	}
	f.statuses = f.statuses[:n]
}

func (s *session) setSlot(f *footerProps, slot int, textKey string, next hot) {
	s.setProps(f.ids[slot], textKey, &f.slots[slot], next)
}

// sameRate reports equal cache-hit rates, both absent or both the same.
func sameRate(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// usageText is Pi's footer usage: the token totals, the latest cache-hit rate
// while the cache was used, and the cost, which a subscription shows even at
// zero.
func usageText(footer fe.Footer) string {
	usage := footer.UsageTotals
	b := make([]byte, 0, 64)
	part := func(prefix string) {
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = append(b, prefix...)
	}
	for _, p := range [...]struct {
		prefix string
		count  int
	}{{"↑", usage.Input}, {"↓", usage.Output}, {"R", usage.CacheRead}, {"W", usage.CacheWrite}} {
		if p.count != 0 {
			part(p.prefix)
			b = appendTokens(b, p.count)
		}
	}
	if (usage.CacheRead > 0 || usage.CacheWrite > 0) && footer.LatestCacheHitRate != nil {
		part("CH")
		b = append(appendFixed(b, *footer.LatestCacheHitRate, 1), '%')
	}
	if (usage.Cost != 0 && !math.IsNaN(usage.Cost)) || footer.UsingSubscription {
		part("$")
		b = appendFixed(b, usage.Cost, 3)
		if footer.UsingSubscription {
			b = append(b, " (sub)"...)
		}
	}
	return string(b)
}

// contextProps are the context segment's and meter's props: the context
// usage as Pi words it, and the window's fill. Pi colors it warning above
// 70% and error above 90%; an unknown size leaves the meter without a
// value.
func contextProps(footer fe.Footer) (seg, meter hot) {
	usage := footer.ContextUsage
	b := make([]byte, 0, 32)
	if usage.Unknown {
		b = append(b, '?')
	} else {
		b = append(appendFixed(b, usage.Percent, 1), '%')
		meter = hot{hasValue: true, value: math.Float64bits(usage.Percent / 100)}
		switch {
		case usage.Percent > 90:
			seg.tone, meter.tone = ui.ToneError, ui.ToneError
		case usage.Percent > 70:
			seg.tone, meter.tone = ui.ToneWarning, ui.ToneWarning
		}
	}
	b = appendTokens(append(b, '/'), usage.ContextWindow)
	if footer.AutoCompact {
		b = append(b, " (auto)"...)
	}
	seg.text = string(b)
	return seg, meter
}

// modelProps are the model segment's and effort glyph's props: Pi's model
// text (the provider while several are available, the model, its thinking
// level and the model a virtual selection routed to), and Tern's effort
// glyph for the thinking level of a model that reasons.
func modelProps(footer fe.Footer) (seg, effort hot) {
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
	return hot{text: text}, hot{text: footer.ThinkingLevel, hidden: footer.ThinkingLevel == ""}
}

// formatTokens is Pi's footer formatTokens: one decimal below ten thousand
// and ten million, whole thousands and millions rounded half up elsewhere.
func formatTokens(count int) string { return string(appendTokens(nil, count)) }

func appendTokens(b []byte, count int) []byte {
	switch {
	case count < 1000:
		return strconv.AppendInt(b, int64(count), 10)
	case count < 10_000:
		return append(appendFixed(b, float64(count)/1000, 1), 'k')
	case count < 1_000_000:
		return append(strconv.AppendInt(b, int64(math.Floor(float64(count)/1000+0.5)), 10), 'k')
	case count < 10_000_000:
		return append(appendFixed(b, float64(count)/1_000_000, 1), 'M')
	default:
		return append(strconv.AppendInt(b, int64(math.Floor(float64(count)/1_000_000+0.5)), 10), 'M')
	}
}

// toFixed is JavaScript's Number.prototype.toFixed: the exact value of x
// rounded to digits decimals, a tie going away from zero.
func toFixed(x float64, digits int) string { return string(appendFixed(nil, x, digits)) }

// appendFixed appends toFixed(x, digits). strconv rounds the exact value
// too, but a tie to even, so an exact tie is written with its last digit,
// the 5, which is then rounded up.
func appendFixed(b []byte, x float64, digits int) []byte {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return strconv.AppendFloat(b, x, 'f', -1, 64)
	}
	start := len(b)
	abs := math.Abs(x)
	if !exactTie(abs, digits) {
		b = strconv.AppendFloat(b, abs, 'f', digits, 64)
	} else {
		b = strconv.AppendFloat(b, abs, 'f', digits+1, 64)
		b = b[:len(b)-1]
		if digits == 0 {
			b = b[:len(b)-1]
		}
		b = roundUp(b, start)
	}
	if x < 0 && strings.Trim(string(b[start:]), "0.") != "" {
		b = append(b[:start+1], b[start:]...)
		b[start] = '-'
	}
	return b
}

// exactTie reports whether x, which is not negative, lies exactly halfway
// between two multiples of 10^-digits: x = m·2^e with m odd, and x·10^digits
// = m·5^digits·2^(e+digits) has the fraction one half when e = -(digits+1).
func exactTie(x float64, digits int) bool {
	if x == 0 {
		return false
	}
	raw := math.Float64bits(x)
	exp := int(raw >> 52 & 0x7ff)
	mant := raw & (1<<52 - 1)
	if exp == 0 {
		exp = 1
	} else {
		mant |= 1 << 52
	}
	return exp-1075+bits.TrailingZeros64(mant) == -(digits + 1)
}

// roundUp adds one in the last place of the decimal number in b[start:].
func roundUp(b []byte, start int) []byte {
	for i := len(b) - 1; i >= start; i-- {
		switch b[i] {
		case '.':
		case '9':
			b[i] = '0'
		default:
			b[i]++
			return b
		}
	}
	b = append(b[:start+1], b[start:]...)
	b[start] = '1'
	return b
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
