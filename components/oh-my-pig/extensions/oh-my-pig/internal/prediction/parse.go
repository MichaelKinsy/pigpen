// Package prediction ports omp v19.1.1's full-prompt composer prediction
// (`composer.predictions` and `composer.predictionAlternatives`).
//
// After an interactive turn settles cleanly, it launches a cancellable,
// prompt-cache-friendly side turn over the session's active model, system
// prompt, and branch messages, then publishes the parsed prediction choices as
// a hidden structured `kit.View` dock widget (`omp.composer.prediction`) for
// Tern's frontend to display as ghost text.
package prediction

import (
	_ "embed"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// SkipReply is the sentinel reply the prediction prompt asks for when the
	// model has no confident guess.
	// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:19
	SkipReply = "NO_PREDICTION"

	// MaxPredictionLength drops rambling replies longer than this many runes
	// (UTF-16 code units in upstream JS; runes in Go for normal text).
	// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:21
	MaxPredictionLength = 500

	// MaxPredictionChoices is the maximum number of alternatives requested and
	// offered when `composer.predictionAlternatives` is enabled.
	// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:23
	MaxPredictionChoices = 4

	// PredictionMaxTokens is the output cap for the side turn.
	// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:30
	PredictionMaxTokens = 1024
)

//go:embed prompts/composer-prediction-user.md
var composerPredictionPromptTemplate string

var (
	// ansiEscapeRe matches CSI, OSC, and 2-byte ESC sequences stripped by sanitizeText.
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

	// collapseWSRe collapses runs of whitespace to a single ASCII space.
	collapseWSRe = regexp.MustCompile(`\s+`)

	// listMarkerRe strips a leading list marker (- , * , • , 1. , 1) ) some
	// models emit despite the prompt.
	// upstream: composer-prediction-controller.ts:38
	listMarkerRe = regexp.MustCompile(`^\s*(?:[-*•]|\d{1,2}[.)])\s+`)

	// nonWordRe folds everything except Unicode letters and digits into a space
	// so alternatives differing only in case or punctuation deduplicate.
	// upstream: composer-prediction-controller.ts:40
	nonWordRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

	// crlfSplitRe splits on \r\n or \n.
	crlfSplitRe = regexp.MustCompile(`\r?\n`)
)

// RenderPrompt renders the embedded `composer-prediction-user.md` template for
// either a single prediction (`alternatives == false`) or up to
// `MaxPredictionChoices` alternatives (`alternatives == true`).
// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:243-246
func RenderPrompt(alternatives bool) string {
	choicesStr := ""
	if alternatives {
		choicesStr = strconv.Itoa(MaxPredictionChoices)
	}
	return renderPredictionTemplate(composerPredictionPromptTemplate, alternatives, choicesStr, SkipReply)
}

// renderPredictionTemplate evaluates the `{{#if choices}}...{{else}}...{{/if}}`,
// `{{choices}}`, and `{{skip}}` tags in `composer-prediction-user.md`, stripping
// Handlebars standalone block-tag lines the same way Handlebars + `prompt.format`
// do in upstream omp.
func renderPredictionTemplate(tpl string, hasChoices bool, choicesVal, skipVal string) string {
	// Normalize CRLF if present.
	tpl = strings.ReplaceAll(tpl, "\r\n", "\n")

	// Strip standalone lines containing only a block tag (`{{#if ...}}`,
	// `{{else}}`, `{{/if}}`) by absorbing their trailing newline before eval.
	lines := strings.Split(tpl, "\n")
	var out strings.Builder
	inIf := false
	inElse := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "{{#if choices}}":
			inIf = true
			inElse = false
			continue
		case "{{else}}":
			if inIf {
				inElse = true
				continue
			}
		case "{{/if}}":
			if inIf {
				inIf = false
				inElse = false
				continue
			}
		}
		if inIf {
			if (!inElse && !hasChoices) || (inElse && hasChoices) {
				continue
			}
		}
		expanded := evalInlineChoices(line, hasChoices, choicesVal, skipVal)
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(expanded)
	}
	return strings.TrimSpace(out.String())
}

func evalInlineChoices(line string, hasChoices bool, choicesVal, skipVal string) string {
	const (
		openTag = "{{#if choices}}"
		elseTag = "{{else}}"
		endTag  = "{{/if}}"
	)
	for {
		start := strings.Index(line, openTag)
		if start < 0 {
			break
		}
		rest := line[start+len(openTag):]
		endRel := strings.Index(rest, endTag)
		if endRel < 0 {
			break
		}
		body := rest[:endRel]
		var chosen string
		if elseRel := strings.Index(body, elseTag); elseRel >= 0 {
			if hasChoices {
				chosen = body[:elseRel]
			} else {
				chosen = body[elseRel+len(elseTag):]
			}
		} else if hasChoices {
			chosen = body
		}
		line = line[:start] + chosen + rest[endRel+len(endTag):]
	}
	line = strings.ReplaceAll(line, "{{choices}}", choicesVal)
	line = strings.ReplaceAll(line, "{{skip}}", skipVal)
	return line
}

// sanitizeText strips invalid UTF-8, ANSI escape sequences, and C0/C1 control
// characters (preserving \t and \n), matching `@oh-my-pi/pi-utils` sanitizeText.
// upstream: packages/utils/src/sanitize-text.ts:25-40
func sanitizeText(text string) string {
	if !utf8.ValidString(text) {
		var clean strings.Builder
		clean.Grow(len(text))
		for i := 0; i < len(text); {
			r, size := utf8.DecodeRuneInString(text[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			clean.WriteRune(r)
			i += size
		}
		text = clean.String()
	}
	if strings.IndexByte(text, 0x1b) >= 0 {
		text = ansiEscapeRe.ReplaceAllString(text, "")
	}
	var b strings.Builder
	b.Grow(len(text))
	changed := false
	for _, r := range text {
		if isControlRune(r) {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return text
	}
	return b.String()
}

func isControlRune(r rune) bool {
	if r == '\t' || r == '\n' {
		return false
	}
	return (r >= 0x00 && r <= 0x1f) || (r >= 0x7f && r <= 0x9f)
}

// stripWrappingQuotes removes a single matching pair of double quotes ("..."),
// curly double quotes (“...”), or backticks (`...`) wrapping the entire string,
// provided the interior does not contain the same quote characters.
// upstream: composer-prediction-controller.ts:36,88-89
func stripWrappingQuotes(text string) string {
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		inner := text[1 : len(text)-1]
		if !strings.ContainsRune(inner, '"') {
			return strings.TrimSpace(inner)
		}
	}
	if len(text) >= 2 && text[0] == '`' && text[len(text)-1] == '`' {
		inner := text[1 : len(text)-1]
		if !strings.ContainsRune(inner, '`') {
			return strings.TrimSpace(inner)
		}
	}
	const leftCurly, rightCurly = "“", "”"
	if strings.HasPrefix(text, leftCurly) && strings.HasSuffix(text, rightCurly) && len(text) >= len(leftCurly)+len(rightCurly) {
		inner := text[len(leftCurly) : len(text)-len(rightCurly)]
		if !strings.ContainsAny(inner, "“”") {
			return strings.TrimSpace(inner)
		}
	}
	return text
}

// ParseComposerPrediction normalizes a single prediction reply to the one-line
// string shown in the composer, or ("", false) when the model skipped, rambled,
// or replied with nothing usable.
// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:86-92
func ParseComposerPrediction(reply string) (string, bool) {
	text := strings.TrimSpace(collapseWSRe.ReplaceAllString(sanitizeText(reply), " "))
	text = stripWrappingQuotes(text)
	if text == "" || strings.Contains(text, SkipReply) || utf8.RuneCountInString(text) > MaxPredictionLength {
		return nilString()
	}
	return text, true
}

func nilString() (string, bool) { return "", false }

// ParseComposerPredictions normalizes an alternatives reply (one message per
// line, most likely first) to at most `max` distinct lines, or nil when the
// model skipped or nothing usable remains.
// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:100-122
func ParseComposerPredictions(reply string, max int) []string {
	if max <= 0 {
		return nil
	}
	rawLines := crlfSplitRe.Split(reply, -1)
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		cleaned := listMarkerRe.ReplaceAllString(raw, "")
		if strings.TrimSpace(cleaned) != "" {
			lines = append(lines, cleaned)
		}
	}
	choices := make([]string, 0, min(max, len(lines)))
	seen := make(map[string]struct{}, len(lines))
	for i, line := range lines {
		if len(choices) == max {
			break
		}
		// A skip in place of the most likely message declines the whole prediction.
		if len(choices) == 0 && strings.Contains(line, SkipReply) {
			return nil
		}
		text, ok := ParseComposerPrediction(line)
		if !ok {
			continue
		}
		// "Here are some options:" introducing the alternatives below it, despite the prompt.
		if i == 0 && i < len(lines)-1 && strings.HasSuffix(text, ":") {
			continue
		}
		// Lines with no letters or digits (an emoji, "?") fold to nothing; compare those verbatim.
		key := dedupeKey(text)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		choices = append(choices, text)
	}
	if len(choices) == 0 {
		return nil
	}
	return choices
}

// dedupeKey folds case and strips non-letter/non-digit characters, falling back
// to the verbatim text when no letters or digits remain.
func dedupeKey(text string) string {
	lower := strings.Map(unicode.ToLower, text)
	folded := strings.TrimSpace(nonWordRe.ReplaceAllString(lower, " "))
	if folded == "" {
		return text
	}
	return folded
}

// ParseReply parses either a single prediction or up to MaxPredictionChoices
// alternatives depending on `alternatives`.
// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:125-129
func ParseReply(reply string, alternatives bool) []string {
	if alternatives {
		return ParseComposerPredictions(reply, MaxPredictionChoices)
	}
	text, ok := ParseComposerPrediction(reply)
	if !ok {
		return nil
	}
	return []string{text}
}
