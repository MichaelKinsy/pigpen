package jev

import (
	"fmt"
	"strings"
)

// Two display styles. "plain" is the original's wording, character for character
// (the equivalence scenarios run in it); "rich" is the default: one glyph per state,
// a verdict card with a bar per question, and the destination named wherever content
// is about to leave. Only wording and layout differ, never behavior.

const statusKey = "jev"

func (x *ext) plain() bool { return x.cfgDisplay() == "plain" }

func (x *ext) cfgDisplay() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.cfg.Display
}

// statusText renders a footer status. rest is the plain text after "jev: ".
func statusText(plain bool, glyph, rest string) string {
	if plain {
		return "jev: " + rest
	}
	if glyph == "" {
		return "⚖ jev: " + rest
	}
	return "⚖ jev " + glyph + " " + rest
}

func bar(p float64) string {
	const width = 10
	n := int(p*width + 0.5)
	n = max(0, min(width, n))
	return strings.Repeat("█", n) + strings.Repeat("·", width-n)
}

// card is the verdict card of the gate: one line per question with a bar, the
// value, its threshold and a mark on the ones that crossed it.
func card(v gateVerdict, c config) string {
	t := c.Gate.BlockOn
	line := func(label string, p, limit float64) string {
		mark := "  "
		cmp := "<"
		if p >= limit {
			mark, cmp = "⚠ ", "≥"
		}
		return fmt.Sprintf("  %s%-13s %s  %s  %s %s", mark, label, bar(p), toFixed2(p), cmp, toFixed2(limit))
	}
	lines := []string{
		line("destructive", v.Destructive, t.Destructive),
		line("exfiltration", v.Exfiltration, t.Exfiltration),
		line("beyond scope", v.BeyondScope, t.BeyondScope),
	}
	if v.HasImpact {
		mark, cmp := "  ", "<"
		if v.Impact >= t.Impact && v.ImpactConfidence >= c.Gate.MinConfidence {
			mark, cmp = "⚠ ", "≥"
		}
		lines = append(lines, fmt.Sprintf("  %s%-13s %s  %s/3  %s %s  (confidence %s)", mark, "impact", bar(v.Impact/3), toFixed2(v.Impact), cmp, toFixed2(t.Impact), toFixed2(v.ImpactConfidence)))
	}
	return strings.Join(lines, "\n")
}

// preview names what a tool call touches, for the confirmation dialog.
func preview(tool string, input any) string {
	m, _ := input.(map[string]any)
	for _, k := range []string{"command", "path", "file_path", "filePath"} {
		if s, ok := m[k].(string); ok && s != "" {
			return cut(collapse(s), 120)
		}
	}
	return ""
}

func (x *ext) shadowNote(tool string, v gateVerdict, c config) string {
	if c.Display == "plain" {
		return fmt.Sprintf("jev shadow: %s - %s", tool, v.summary())
	}
	return fmt.Sprintf("⚠ Jev flagged %s (shadow mode: reported, not blocked)\n%s", tool, card(v, c))
}

func (x *ext) headlessNote(tool string, v gateVerdict, c config) string {
	tail := "(headless: not blocking; set gate.blockWithoutUI to block)"
	if c.Display == "plain" {
		return fmt.Sprintf("jev: %s - %s %s", tool, v.summary(), tail)
	}
	return fmt.Sprintf("⚠ Jev flagged %s: %s %s", tool, v.summary(), tail)
}

func (x *ext) confirmMessage(tool string, input any, v gateVerdict, c config, dest string) string {
	if c.Display == "plain" {
		return fmt.Sprintf("%s\n%s\n\nRun it anyway?", tool, v.summary())
	}
	head := tool
	if p := preview(tool, input); p != "" {
		head += "  " + p
	}
	return fmt.Sprintf("%s\n\n%s\n\nJudged by %s\nRun it anyway?", head, card(v, c), dest)
}

func (x *ext) leakNote(tool string, leak float64, c config) string {
	if c.Display == "plain" {
		return fmt.Sprintf("jev: %s output may carry a secret (%s)", tool, toFixed2(leak))
	}
	return fmt.Sprintf("⚠ Jev: %s output may carry a secret (%s). The model was told not to repeat it.", tool, toFixed2(leak))
}

func (x *ext) failureNote(msg string, c config) string {
	if c.Display == "plain" {
		return fmt.Sprintf("pi-jev: %s (failing open)", msg)
	}
	return fmt.Sprintf("✗ Jev could not judge: %s\nThe tool call ran unjudged (failing open).", msg)
}

// disclosure says what leaves the machine and where it goes. It is shown until the
// user sets "acknowledged", and by /jev on before consent.
func disclosure(c config, dest string) string {
	var b strings.Builder
	b.WriteString("What leaves this machine for each judgment:\n")
	b.WriteString("  • the working directory and the tool name\n")
	fmt.Fprintf(&b, "  • your last message (first %d characters)\n", userRequestChars)
	fmt.Fprintf(&b, "  • the tool arguments (%s; long fields are cut at %d characters, and write/edit arguments are file content)\n", strings.Join(c.Gate.Tools, "/"), c.Gate.ArgumentChars)
	fmt.Fprintf(&b, "  • %s output (first %d characters)\n", strings.Join(c.Output.Tools, "/"), c.Output.OutputChars)
	fmt.Fprintf(&b, "Also sent: text the model passes to jev_ask (up to %d characters) and text you pass to /jev check.\n", c.MaxStateChars)
	fmt.Fprintf(&b, "Destination: %s\n", dest)
	b.WriteString("If the judge is unavailable, tool calls run unjudged (it fails open). A judgment is probabilistic advice, not a sandbox.")
	return b.String()
}

func startupDisclosure(c config, dest string) string {
	return fmt.Sprintf("Jev is ON (%s mode). %s\nSet \"acknowledged\": true in pi-jev.json to hide this notice.", c.Gate.Mode, disclosure(c, dest))
}
