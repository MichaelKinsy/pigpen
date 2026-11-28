package tern

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
)

// TitleIcons mirrors omp's `title.icons` setting (`nf+emoji`, `emoji`, `boring`).
// upstream: packages/coding-agent/src/utils/title-settings.ts:9
type TitleIcons string

const (
	TitleIconsNFEmoji TitleIcons = "nf+emoji"
	TitleIconsEmoji   TitleIcons = "emoji"
	TitleIconsBoring  TitleIcons = "boring"
)

// TitleCard is the card that heads a card-form session title (`<icon> <CODE>: <title>`).
// upstream: packages/coding-agent/src/utils/title-card.ts:15
type TitleCard struct {
	// Icon is an emoji or a Nerd Fonts glyph (1-8 non-ASCII runes).
	Icon string
	// Code is 1-6 ASCII uppercase letters or digits naming the subject (`FLAKY`, `Z3`).
	Code string
}

// CardTitleParts is the decomposed form of a card-form session title.
// upstream: packages/coding-agent/src/utils/title-card.ts:23
type CardTitleParts struct {
	TitleCard
	// Title is the title without its card prefix.
	Title string
}

// maxIconCodepoints is the maximum number of codepoints Tern reads as a card icon.
// upstream: packages/coding-agent/src/utils/title-card.ts:50
const maxIconCodepoints = 8

const (
	maxTitleChars = 80
	maxTitleWords = 12
)

var (
	titleCodeRe    = regexp.MustCompile(`^[A-Z0-9]{1,6}$`)
	cardLineRe     = regexp.MustCompile(`^(\S+) ([A-Z0-9]{1,6}): (\S.*)$`)
	leadingCodeRe  = regexp.MustCompile(`^([A-Z0-9]{2,6}):? (\S.*)$`)
	titleTagRe     = regexp.MustCompile(`(?is)<title(\s[^>]*?)?\s*(?:/>|>(.*?)</title>)`)
	titleAttrRe    = regexp.MustCompile(`(\w+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	titleWordRe    = regexp.MustCompile(`[\p{L}\p{N}]+`)
	spacesRe       = regexp.MustCompile(`\s+`)
	noTitleTagRe   = regexp.MustCompile(`(?i)^<title\s*/>$`)
	openTitleTagRe = regexp.MustCompile(`(?i)^<title>`)
	closeTitleRe   = regexp.MustCompile(`(?i)</title>$`)
)

// IsCardIcon reports whether icon reads as a card icon to Tern:
// 1-8 codepoints, none of them ASCII (< 0x80).
// upstream: packages/coding-agent/src/utils/title-card.ts:53
func IsCardIcon(icon string) bool {
	if icon == "" {
		return false
	}
	count := 0
	for _, r := range icon {
		if r < 0x80 {
			return false
		}
		count++
		if count > maxIconCodepoints {
			return false
		}
	}
	return count > 0
}

// FormatCardTitle heads title with card: `🧪 FLAKY: Fix flaky park tests`.
// upstream: packages/coding-agent/src/utils/title-card.ts:102
func FormatCardTitle(card TitleCard, title string) string {
	return card.Icon + " " + card.Code + ": " + title
}

// SplitCardTitle splits a card-form session title (`<icon> <CODE>: <title>`).
// Returns ok=false for a plain title (e.g. `Fix: the parser` has an ASCII prefix).
// upstream: packages/coding-agent/src/utils/title-card.ts:171
func SplitCardTitle(title string) (CardTitleParts, bool) {
	m := cardLineRe.FindStringSubmatch(title)
	if m == nil || !IsCardIcon(m[1]) {
		return CardTitleParts{}, false
	}
	return CardTitleParts{
		TitleCard: TitleCard{Icon: m[1], Code: m[2]},
		Title:     m[3],
	}, true
}

// KeepTitleCard heads title with the card of current, if current has one.
// If title brings a card of its own, it yields to current's card rather than nesting.
// upstream: packages/coding-agent/src/utils/title-card.ts:185
func KeepTitleCard(current, title string) string {
	card, ok := SplitCardTitle(current)
	if !ok {
		return title
	}
	if next, hasCard := SplitCardTitle(title); hasCard {
		return FormatCardTitle(card.TitleCard, next.Title)
	}
	return FormatCardTitle(card.TitleCard, title)
}

// cardEmoji validates a single emoji icon for a title card.
// Keycaps (`#️⃣`) are refused because they start with ASCII (`#`).
// Unqualified emoji that commonly need U+FE0F (e.g. `🗄` -> `🗄️`) are normalized.
// upstream: packages/coding-agent/src/utils/title-card.ts:66
func cardEmoji(value string) string {
	s := strings.TrimSpace(value)
	if s == "" || !IsCardIcon(s) {
		return ""
	}
	// Reject multiple distinct emoji (e.g. "🧪🧪"): a single emoji is either:
	// - one base rune + optional VS16/skin-tone modifier, or
	// - a regional-indicator flag pair (2 runes in U+1F1E6..U+1F1FF), or
	// - a ZWJ sequence joining runes with U+200D.
	runes := []rune(s)
	if !isSingleEmoji(runes) {
		return ""
	}
	// Restore missing variation selector-16 on unqualified BMP/SMP pictographs
	// (such as U+1F5C4 `🗄` -> `🗄️`).
	if len(runes) == 1 && needsVS16(runes[0]) {
		return s + "\uFE0F"
	}
	return s
}

func isSingleEmoji(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	// Regional indicator pair (flag).
	if len(runes) == 2 && isRegionalIndicator(runes[0]) && isRegionalIndicator(runes[1]) {
		return true
	}
	// Must start with an emoji-like symbol/pictograph (not plain letters/digits/punctuation).
	if !isEmojiBase(runes[0]) {
		return false
	}
	// Walk remaining runes: only variation selectors, skin-tone modifiers,
	// combining enclosing keycap, or ZWJ followed by another emoji base are allowed.
	i := 1
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == 0xFE0F || r == 0xFE0E:
			i++
		case r >= 0x1F3FB && r <= 0x1F3FF: // Fitzpatrick skin tone modifier
			i++
		case r == 0x200D: // ZWJ
			if i+1 >= len(runes) || !isEmojiBase(runes[i+1]) {
				return false
			}
			i += 2
		default:
			return false
		}
	}
	return true
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

func isEmojiBase(r rune) bool {
	// Exclude letters and numbers (e.g. plain words or non-ASCII letters).
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	// Exclude Nerd Fonts PUA ranges (handled separately via nf catalog).
	if (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0xFFFFD) {
		return false
	}
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // Mahjong, Domino, Playing Cards, Pictographs, Emoticons, Transport, Alchemical, Geometric, Supplemental
		return true
	case r >= 0x2600 && r <= 0x27BF: // Misc Symbols & Dingbats
		return true
	case r >= 0x2300 && r <= 0x23FF: // Misc Technical (⏳, ⏰, ⌨, etc.)
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // Misc Symbols and Arrows
		return true
	case r == 0x00A9 || r == 0x00AE || r == 0x203C || r == 0x2049 || r == 0x2122 || r == 0x2139 || (r >= 0x2194 && r <= 0x21AA) || (r >= 0x25AA && r <= 0x25FE) || r == 0x3030 || r == 0x303D || r == 0x3297 || r == 0x3299:
		return true
	default:
		return false
	}
}

// needsVS16 reports whether a single unqualified emoji codepoint takes U+FE0F
// in its RGI qualified form (such as U+1F5C4 file cabinet `🗄` -> `🗄️`).
func needsVS16(r rune) bool {
	switch {
	case r >= 0x1F500 && r <= 0x1F5FF:
		// Subset of Miscellaneous Symbols and Pictographs with text-default presentation
		// (e.g. U+1F5C4 file cabinet, U+1F5A5 desktop, U+1F5B1 mouse, U+1F5D1 wastebasket).
		return (r >= 0x1F5A5 && r <= 0x1F5FA) || r == 0x1F573 || r == 0x1F574 || r == 0x1F575 || r == 0x1F576 || r == 0x1F577 || r == 0x1F578 || r == 0x1F579 || r == 0x1F587 || (r >= 0x1F58A && r <= 0x1F58D) || r == 0x1F590
	case r >= 0x2600 && r <= 0x27BF:
		return r == 0x2600 || r == 0x2601 || r == 0x260E || r == 0x2611 || r == 0x261D || r == 0x2639 || r == 0x263A || r == 0x2699 || r == 0x26A0 || r == 0x2702 || r == 0x2708 || r == 0x2709 || r == 0x270F || r == 0x2712 || r == 0x2714 || r == 0x2716 || r == 0x2744 || r == 0x2764
	default:
		return false
	}
}

type replyCard struct {
	code  string
	emoji string
	nf    string
}

func buildReplyCard(code, emoji, nf string, nfResolver func(string) string) *replyCard {
	cardCode := strings.ToUpper(strings.TrimSpace(code))
	if !titleCodeRe.MatchString(cardCode) {
		return nil
	}
	e := cardEmoji(emoji)
	var nfGlyph string
	if nf != "" && nfResolver != nil {
		nfGlyph = nfResolver(nf)
	}
	if e == "" && nfGlyph == "" {
		return nil
	}
	return &replyCard{code: cardCode, emoji: e, nf: nfGlyph}
}

func shownCard(card *replyCard, icons TitleIcons) (TitleCard, bool) {
	if card == nil || icons == TitleIconsBoring {
		return TitleCard{}, false
	}
	var icon string
	if icons == TitleIconsNFEmoji && card.nf != "" {
		icon = card.nf
	} else {
		icon = card.emoji
	}
	if icon == "" {
		return TitleCard{}, false
	}
	return TitleCard{Icon: icon, Code: card.code}, true
}

// ParseCardTitleReply parses a title model reply in `<title ...>text</title>`
// card form, matching omp's `parseCardTitleReply`.
// nfResolver optionally maps a Nerd Fonts name (e.g. "nf-md-flask") to its
// glyph; when nil, Nerd Fonts names degrade to the emoji.
// Returns ("", false) when the reply names no valid title.
// upstream: packages/coding-agent/src/utils/title-card.ts:123
func ParseCardTitleReply(reply string, icons TitleIcons, sourceText string, nfResolver func(string) string) (string, bool) {
	tag := titleTagRe.FindStringSubmatch(reply)
	if tag == nil || !strings.Contains(strings.ToLower(tag[0]), "</title>") {
		return "", false
	}
	attrs := map[string]string{}
	for _, m := range titleAttrRe.FindAllStringSubmatch(tag[1], -1) {
		val := m[2]
		if val == "" {
			val = m[3]
		}
		attrs[strings.ToLower(m[1])] = val
	}
	text := strings.TrimSpace(spacesRe.ReplaceAllString(tag[2], " "))
	_, hasCodeAttr := attrs["code"]
	card := buildReplyCard(attrs["code"], attrs["emoji"], attrs["nf"], nfResolver)
	if card == nil && !hasCodeAttr {
		if line := cardLineRe.FindStringSubmatch(text); line != nil {
			if lc := buildReplyCard(line[2], line[1], attrs["nf"], nfResolver); lc != nil {
				card = lc
				text = line[3]
			}
		}
		if card == nil {
			if lead := leadingCodeRe.FindStringSubmatch(text); lead != nil {
				if lc := buildReplyCard(lead[1], attrs["emoji"], attrs["nf"], nfResolver); lc != nil {
					card = lc
					text = lead[2]
				}
			}
		}
	}
	title, ok := normalizeGeneratedTitle(text, sourceText)
	if !ok {
		return "", false
	}
	if shown, ok := shownCard(card, icons); ok {
		return FormatCardTitle(shown, title), true
	}
	return title, true
}

// ParseCardReply parses a card-only reply (`[nf] [emoji] CODE ...`).
// upstream: packages/coding-agent/src/utils/title-card.ts:160
func ParseCardReply(reply string, icons TitleIcons, nfResolver func(string) string) (TitleCard, bool) {
	tokens := strings.Fields(reply)
	if len(tokens) == 0 {
		return TitleCard{}, false
	}
	var nf, emoji string
	if strings.HasPrefix(tokens[0], "nf-") {
		nf = tokens[0]
		tokens = tokens[1:]
	}
	if len(tokens) > 0 && cardEmoji(tokens[0]) != "" {
		emoji = tokens[0]
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return TitleCard{}, false
	}
	code := strings.TrimSuffix(tokens[0], ":")
	return shownCard(buildReplyCard(code, emoji, nf, nfResolver), icons)
}

var commonTitleAcronyms = map[string]bool{
	"API": true, "CLI": true, "CPU": true, "CRUD": true, "CSS": true,
	"DNS": true, "ETL": true, "GPU": true, "HTML": true, "HTTP": true,
	"HTTPS": true, "ID": true, "JSON": true, "LLM": true, "REST": true,
	"SDK": true, "SSH": true, "TCP": true, "TLS": true, "TUI": true,
	"UI": true, "URI": true, "URL": true, "UX": true, "XML": true,
	"YAML": true,
}

// isNoTitleSentinel mirrors omp's `isNoTitleSentinel` (`NO_TITLE_SENTINEL = "none"`).
// upstream: packages/coding-agent/src/tiny/text.ts:201-211
func isNoTitleSentinel(s string) bool {
	bare := strings.TrimSpace(s)
	bare = stripOuterQuotes(bare)
	if n := len(bare); n > 0 && (bare[n-1] == '.' || bare[n-1] == '!' || bare[n-1] == '?') {
		bare = bare[:n-1]
	}
	return strings.ToLower(strings.TrimSpace(bare)) == "none"
}

// utf16Len counts UTF-16 code units, matching JavaScript's `string.length`.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		r1, r2 := utf16.EncodeRune(r)
		if r1 == 0xFFFD && r2 == 0xFFFD {
			n++
		} else {
			n += 2
		}
	}
	return n
}

// normalizeGeneratedTitle mirrors omp's `normalizeGeneratedTitle` from `src/tiny/text.ts`.
// upstream: packages/coding-agent/src/tiny/text.ts:224
func normalizeGeneratedTitle(value, sourceText string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	firstLine := trimmed
	if idx := strings.IndexAny(trimmed, "\r\n"); idx >= 0 {
		firstLine = strings.TrimSpace(trimmed[:idx])
	}
	if firstLine == "" {
		return "", false
	}
	unquoted := strings.TrimSpace(stripOuterQuotes(firstLine))
	if noTitleTagRe.MatchString(unquoted) {
		return "", false
	}
	title := openTitleTagRe.ReplaceAllString(unquoted, "")
	title = closeTitleRe.ReplaceAllString(title, "")
	title = stripOuterQuotes(title)
	if n := len(title); n > 0 && (title[n-1] == '.' || title[n-1] == '!' || title[n-1] == '?') {
		title = title[:n-1]
	}
	title = strings.TrimSpace(title)
	if title == "" || isNoTitleSentinel(title) {
		return "", false
	}
	words := titleWordRe.FindAllString(title, -1)
	if len(words) == 0 || utf16Len(title) > maxTitleChars || len(words) > maxTitleWords {
		return "", false
	}
	if sourceText != "" {
		title = reconcileTitleCasing(title, sourceText)
	}
	return title, true
}

func stripOuterQuotes(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	if len(s) >= 1 && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	if len(s) >= 1 && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

func reconcileTitleCasing(title, sourceText string) string {
	verbatim := map[string]bool{}
	distinctive := map[string]string{}
	acronyms := map[string]string{}
	shouty := isShoutySource(sourceText)
	for _, tok := range titleWordRe.FindAllString(sourceText, -1) {
		verbatim[tok] = true
		if isDistinctiveCasing(tok) {
			lower := strings.ToLower(tok)
			if _, ok := distinctive[lower]; !ok {
				distinctive[lower] = tok
			}
		} else if !shouty && isAllCapsAcronym(tok) {
			lower := strings.ToLower(tok)
			if _, ok := acronyms[lower]; !ok {
				acronyms[lower] = tok
			}
		}
	}
	return titleWordRe.ReplaceAllStringFunc(title, func(tok string) string {
		if verbatim[tok] {
			return tok
		}
		lower := strings.ToLower(tok)
		if restored, ok := distinctive[lower]; ok {
			return restored
		}
		if isTitleCasedArtifact(tok) {
			if acronym, ok := acronyms[lower]; ok {
				return acronym
			}
		}
		if isCamelArtifact(tok) {
			return lower
		}
		return tok
	})
}

func isDistinctiveCasing(tok string) bool {
	hasLower := false
	prevLetter := false
	hasInteriorUpper := false
	for _, r := range tok {
		if unicode.IsLower(r) {
			hasLower = true
		}
		if prevLetter && unicode.IsUpper(r) {
			hasInteriorUpper = true
		}
		prevLetter = unicode.IsLetter(r)
	}
	return hasLower && hasInteriorUpper
}

func isAllCapsWord(tok string) bool {
	letters := 0
	hasUpper := false
	for _, r := range tok {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				hasUpper = true
			}
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return letters >= 2 && hasUpper
}

func isAllCapsAcronym(tok string) bool {
	if !isAllCapsWord(tok) {
		return false
	}
	upper := strings.ToUpper(tok)
	if commonTitleAcronyms[upper] {
		return true
	}
	for _, r := range tok {
		if unicode.IsNumber(r) {
			return true
		}
	}
	return !strings.ContainsAny(upper, "AEIOU")
}

func isTitleCasedArtifact(tok string) bool {
	runes := []rune(tok)
	if len(runes) < 2 || !unicode.IsUpper(runes[0]) {
		return false
	}
	hasLower := false
	for _, r := range runes[1:] {
		if unicode.IsUpper(r) {
			return false
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	return hasLower
}

func isShoutySource(sourceText string) bool {
	run := 0
	for _, tok := range titleWordRe.FindAllString(sourceText, -1) {
		if isAllCapsWord(tok) {
			run++
			if run >= 2 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

func isCamelArtifact(tok string) bool {
	runes := []rune(tok)
	if len(runes) < 2 || !unicode.IsLower(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}
