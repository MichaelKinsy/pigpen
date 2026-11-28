package todo

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultRemindersMax matches omp's default `todo.remindersMax` setting.
// upstream: packages/coding-agent/src/tools/settings.ts:402-415
const defaultRemindersMax = 3

// BuildStopReminder constructs omp's completion reminder text when the agent stops
// with incomplete (pending or in_progress) todo items. Completed and abandoned
// tasks do not trigger the reminder.
// Returns ("", 0) when no open tasks remain.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:281-322
func BuildStopReminder(phases []TodoPhase, attempt, maxAttempts int) (string, int) {
	if len(phases) == 0 {
		return "", 0
	}
	type openPhase struct {
		name  string
		tasks []TodoItem
	}
	var incompleteByPhase []openPhase
	totalIncomplete := 0
	for _, phase := range phases {
		var open []TodoItem
		for _, task := range phase.Tasks {
			if task.Status == StatusPending || task.Status == StatusInProgress {
				open = append(open, task)
			}
		}
		if len(open) > 0 {
			incompleteByPhase = append(incompleteByPhase, openPhase{name: phase.Name, tasks: open})
			totalIncomplete += len(open)
		}
	}
	if totalIncomplete == 0 {
		return "", 0
	}

	var listLines []string
	for _, p := range incompleteByPhase {
		listLines = append(listLines, "- "+p.name)
		for _, t := range p.tasks {
			listLines = append(listLines, "  - "+t.Content)
		}
	}
	todoList := strings.Join(listLines, "\n")
	reminder := fmt.Sprintf(
		"<system-reminder>\n"+
			"You stopped with %d incomplete todo item(s):\n%s\n\n"+
			"Please continue working on these tasks or mark them complete if finished.\n"+
			"(Reminder %d/%d)\n"+
			"</system-reminder>",
		totalIncomplete,
		todoList,
		attempt,
		maxAttempts,
	)
	return reminder, totalIncomplete
}

// BuildManualEditReminder constructs omp's system reminder injected when the user
// modifies the todo list via /todo.
// upstream: packages/coding-agent/src/modes/controllers/todo-command-controller.ts:120-132
func BuildManualEditReminder(action string, phases []TodoPhase, removed bool) string {
	md := "(empty)"
	if len(phases) > 0 {
		md = strings.TrimRight(PhasesToMarkdown(phases), "\n")
	}
	lines := []string{
		"<system-reminder>",
		fmt.Sprintf("The user manually modified the todo list (%s).", action),
	}
	if removed {
		if len(phases) == 0 {
			lines = append(lines, "The user intentionally cleared the todo list. Do NOT recreate or re-populate it unless the user explicitly asks; continue the current request without a todo list.")
		} else {
			lines = append(lines, "The user intentionally removed the entries no longer shown below. Do NOT re-add them unless the user explicitly asks.")
		}
	}
	lines = append(lines, "Current todo list:", "", md, "</system-reminder>")
	return strings.Join(lines, "\n")
}

// =============================================================================
// isAwaitingUserAnswer — ported from packages/coding-agent/src/session/todo-tracker.ts:43-78,412-542
// =============================================================================

var (
	markdownPromptPrefixRe = regexp.MustCompile(`^(?:>\s*)?(?:(?:[-*+]|\d+[.)])\s+)*`)
	promptLabelRe          = regexp.MustCompile(`(?i)^(?:q(?:uestion)?|ask)\s*\d*\s*[:.)-]\s*`)
	inlineBoldStarRe       = regexp.MustCompile(`\*\*(.*?)\*\*`)
	inlineBoldUnderRe      = regexp.MustCompile(`__(.*?)__`)
	optionLineRe           = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+\S`)
	recommendationRe       = regexp.MustCompile(`(?i)^(?:i(?:'d| would)?\s+recommend|(?:my\s+)?recommendation)\b`)
	choiceConfirmationRe   = regexp.MustCompile(`(?i)^(?:go|proceed|continue|stick)\s+with\b`)
	questionPromptRe       = regexp.MustCompile(`(?i)^(?:what|which|when|where|why|how|who|whom|whose|do|does|did|can|could|would|will|should|is|are|am|may|shall)\b`)
	userDirectedPromptRe   = regexp.MustCompile(`(?i)\b(?:you|your|we|our)\b`)
	addresseeRe            = regexp.MustCompile(`(?i)\b(?:you|your)\b`)
	userResponseCueRe      = regexp.MustCompile(`(?i)^(?:please\s+)?(?:confirm|reply|choose|pick|decide|advise|answer|approve)\b|^(?:please\s+)?(?:let\s+me\s+know|(?:tell|message|ping)\s+me)\b|^(?:if|once|when)\s+you\s+(?:confirm|approve|agree|allow|okay|ok|give)\b`)
	leadingConnectiveRe    = regexp.MustCompile(`(?i)^(?:and|or|so|then|also|otherwise|alternatively|separately|finally)\b,?\s*`)
	leadingCueHeadRe       = regexp.MustCompile(`(?i)^(?:please\b|let\s+me\s+know\b|(?:tell|message|ping)\s+me\b|(?:if|once|when)\s+you\b)`)
	trailingQuestionMarkRe = regexp.MustCompile(`[?？]\s*$`)
	trailingPunctRe        = regexp.MustCompile(`[.!?。！？]+$`)
	paragraphSplitRe       = regexp.MustCompile(`\r?\n\s*\r?\n`)
)

func hasNonASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func unwrapEmphasis(s string) string {
	for _, delim := range []string{"***", "**", "*", "___", "__", "_"} {
		if len(s) > 2*len(delim) && strings.HasPrefix(s, delim) && strings.HasSuffix(s, delim) {
			return strings.TrimSpace(s[len(delim) : len(s)-len(delim)])
		}
	}
	return s
}

type promptLineResult struct {
	text           string
	hadPromptLabel bool
}

func parsePromptLine(line string) promptLineResult {
	s := strings.TrimSpace(line)
	s = inlineBoldStarRe.ReplaceAllString(s, "$1")
	s = inlineBoldUnderRe.ReplaceAllString(s, "$1")
	withoutMarkdownPrefix := strings.TrimSpace(markdownPromptPrefixRe.ReplaceAllString(s, ""))
	withoutPromptLabel := strings.TrimSpace(promptLabelRe.ReplaceAllString(withoutMarkdownPrefix, ""))
	withoutEmphasis := strings.TrimSpace(unwrapEmphasis(withoutPromptLabel))
	return promptLineResult{
		text:           withoutEmphasis,
		hadPromptLabel: withoutPromptLabel != withoutMarkdownPrefix,
	}
}

func isQuestionPromptLine(line string) bool {
	c := parsePromptLine(line)
	if !trailingQuestionMarkRe.MatchString(c.text) {
		return false
	}
	return c.hadPromptLabel ||
		questionPromptRe.MatchString(c.text) ||
		choiceConfirmationRe.MatchString(c.text) ||
		userDirectedPromptRe.MatchString(c.text) ||
		hasNonASCII(c.text)
}

func isResponseCueLine(line string) bool {
	c := parsePromptLine(line).text
	c = trailingPunctRe.ReplaceAllString(c, "")
	if loc := leadingConnectiveRe.FindStringIndex(c); loc != nil {
		rest := c[loc[1]:]
		if leadingCueHeadRe.MatchString(rest) {
			c = rest
		}
	}
	c = strings.TrimSpace(c)
	return userResponseCueRe.MatchString(c)
}

type closingAskKind int

const (
	closingAskNone closingAskKind = iota
	closingAskQuestion
	closingAskRequest
	closingAskCue
)

func askOf(span string) closingAskKind {
	if isResponseCueLine(span) {
		return closingAskCue
	}
	if !isQuestionPromptLine(span) {
		return closingAskNone
	}
	c := parsePromptLine(span)
	if c.hadPromptLabel || addresseeRe.MatchString(c.text) || hasNonASCII(c.text) {
		return closingAskRequest
	}
	return closingAskQuestion
}

func splitSentences(line string) []string {
	var out []string
	start := 0
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '。' || r == '！' || r == '？' {
			seg := strings.TrimSpace(string(runes[start : i+1]))
			if seg != "" {
				out = append(out, seg)
			}
			start = i + 1
			continue
		}
		if (r == '.' || r == '!' || r == '?' || r == '…') && i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\t') {
			seg := strings.TrimSpace(string(runes[start : i+1]))
			if seg != "" {
				out = append(out, seg)
			}
			j := i + 1
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			start = j
			i = j - 1
		}
	}
	if start < len(runes) {
		if seg := strings.TrimSpace(string(runes[start:])); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func closingSentences(paragraph string) []string {
	var sentences []string
	lines := strings.Split(strings.ReplaceAll(paragraph, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && len(sentences) < 2; i-- {
		line := strings.TrimSpace(lines[i])
		if optionLineRe.MatchString(line) {
			break
		}
		ls := splitSentences(line)
		if len(ls) == 0 {
			continue
		}
		need := 2 - len(sentences)
		if len(ls) > need {
			ls = ls[len(ls)-need:]
		}
		sentences = append(ls, sentences...)
	}
	return sentences
}

func closingAsk(paragraph string) closingAskKind {
	lastLine := paragraph
	if idx := strings.LastIndexByte(paragraph, '\n'); idx >= 0 {
		lastLine = paragraph[idx+1:]
	}
	lastLine = strings.TrimSpace(lastLine)
	sentences := closingSentences(paragraph)
	a1 := askOf(lastLine)
	a2 := closingAskNone
	if len(sentences) > 0 {
		a2 = askOf(sentences[len(sentences)-1])
	}
	foreignQuestion := len(sentences) == 2 && hasNonASCII(sentences[0]) && isQuestionPromptLine(sentences[0])
	if a1 == closingAskCue || a2 == closingAskCue {
		return closingAskCue
	}
	if foreignQuestion || a1 == closingAskRequest || a2 == closingAskRequest {
		return closingAskRequest
	}
	if a1 == closingAskQuestion || a2 == closingAskQuestion {
		return closingAskQuestion
	}
	return closingAskNone
}

// IsAwaitingUserAnswer reports whether the assistant's closing text asks the user a question
// or gives a response cue so the stop reminder stays silent.
// upstream: packages/coding-agent/src/session/todo-tracker.ts:500-542
func IsAwaitingUserAnswer(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	paragraphs := paragraphSplitRe.Split(trimmed, -1)
	lastParagraph := strings.TrimSpace(paragraphs[len(paragraphs)-1])
	if closingAsk(lastParagraph) != closingAskNone {
		return true
	}
	if len(paragraphs) >= 2 {
		prevParagraph := strings.TrimSpace(paragraphs[len(paragraphs)-2])
		if prevParagraph != "" && !strings.Contains(lastParagraph, "\n") && !optionLineRe.MatchString(lastParagraph) && closingAsk(prevParagraph) == closingAskCue {
			return true
		}
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	optionCount := 0
	hasRecommendation := false
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if optionLineRe.MatchString(line) {
			optionCount++
			continue
		}
		c := parsePromptLine(line)
		if optionCount > 0 {
			return optionCount >= 2 &&
				isQuestionPromptLine(line) &&
				(hasRecommendation ||
					c.hadPromptLabel ||
					userDirectedPromptRe.MatchString(c.text) ||
					choiceConfirmationRe.MatchString(c.text))
		}
		if recommendationRe.MatchString(c.text) {
			hasRecommendation = true
			continue
		}
		return false
	}
	return false
}
