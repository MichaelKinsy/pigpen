package warden

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf16"
)

// Secret detection (src/redact.ts, the half after `redact`): what earns the "possible credentials" notice and
// what the tool-result masking replaces. Redaction (redact.go) stays broad on purpose; detection must not fire
// on a name. `secret: boolean`, `TYPESAFE_API_KEY`, `savedKey`, `token: string` and `password=<your password>`
// are code and prose about credentials, not credentials.

var (
	// Token shapes that are a credential by construction (redact.ts TOKEN_SHAPES).
	reTokenShapes = []*lazyRegexp{
		lazyRE(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		lazyRE(`\bsk-[A-Za-z0-9_-]{16,}`),
		lazyRE(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
		lazyRE(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
		lazyRE(`\bAKIA[0-9A-Z]{16}\b`),
		lazyRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
		lazyRE(`\bAIza[0-9A-Za-z_-]{30,}`),
		lazyRE(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	}
	// Headers and URL passwords whose value must still look like a secret; group 1 is the value.
	reAuthorizationValue = lazyRE(`(?i)authorization\s*[:=]\s*(?:basic|bearer|token)?\s*([^\s"']+)`)
	reBearerValue        = lazyRE(`(?i)\bbearer\s+([^\s"']+)`)
	reURLPasswordValue   = lazyRE(`(?i)[a-z][a-z0-9+.-]*://[^\s/@:]+:([^\s/@]+)@`)

	reAngleOnly       = lazyRE(`^<[^<>]*>$`)
	reTrimEnds        = lazyRE("^[`\"'(\\[{<]+|[`\"')\\]}>.,;]+$")
	reTrimEndsSynth   = lazyRE("^[`\"'(\\[{<]+|[`\"')\\]\\}>.,;\\]+$")
	reNotValueShape   = lazyRE(`(?i)^[$%<\[{*]|^(?:process|os|env|settings|config|secrets?|vault|keychain|import\.meta)\.|^\$?\{|^\*+$|^x+$|^(?:your|my|the|a|an)[-_ ]`)
	rePlainNumber     = lazyRE(`^\d+(?:[,_]\d+)*$`)
	reCallExpression  = lazyRE(`^(?:[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)+|[a-z]+(?:[A-Z][\w$]*)+|[A-Z][a-z]+(?:[A-Z][\w$]*)*)\s*[(\[]`)
	rePropertyAccess  = lazyRE(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)+$`)
	reEnvName         = lazyRE(`^[A-Z][A-Z0-9_]{6,}$`)
	reTypeName        = lazyRE(`^[A-Z][a-z]+(?:[A-Z][a-z0-9]*)*$`)
	reCamelCase       = lazyRE(`^[a-z]+(?:[A-Z][a-z0-9]*)+$`)
	reSnakeKebabWords = lazyRE(`^[a-z]+(?:[_-][a-z]+)+$`)
	rePathLike        = lazyRE(`^/|^\.\.?/`)
	reThreeDigits     = lazyRE(`\d{3,}`)
	reDigit           = lazyRE(`\d`)
	reLower           = lazyRE(`[a-z]`)
	reUpper           = lazyRE(`[A-Z]`)
	reSymbol          = lazyRE(`[^A-Za-z0-9]`)

	reNonAlnumRun  = lazyRE(`[^a-z0-9]+`)
	reSynthPrefix  = lazyRE(`^[a-z0-9]{2,6}[^a-z0-9]+`)
	reHTTPSURL     = lazyRE("https://[^\\s\"'<>`]+")
	reSignedExpiry = lazyRE(`(?i)[?&](?:X-Amz-Expires|X-Goog-Expires|Expires|se)=`)
	reSignedParam  = lazyRE(`(?i)[?&](?:X-Amz-Signature|X-Amz-Credential|X-Amz-Security-Token|X-Goog-Signature|X-Goog-Credential|Signature|token)=([^&#]*)`)
)

// notValues are words that sit after `secret:` in code and docs (redact.ts NOT_VALUES).
var notValues = map[string]bool{}

func init() {
	for _, w := range strings.Fields("boolean string number object any unknown null undefined true false none nil void never required optional redacted hidden masked omitted unset missing empty changeme example placeholder password secret token value text str int bytes yes no on off auto default bearer basic env process os environ config settings input output prompt") {
		notValues[w] = true
	}
}

// LooksLikeSecretValue is a value shape, not a name: at least 8 characters, not a type word or placeholder, not a
// plain number, not an identifier or expression in code, not a reference to where the value lives, not an
// all-caps identifier, and with the mixture of character classes that keys have.
func LooksLikeSecretValue(value string) bool {
	raw := jsTrim(value)
	if reAngleOnly.MatchString(raw) {
		return false
	}
	text := reTrimEnds.get().ReplaceAllString(raw, "")
	if n := utf16Len(text); n < 8 || n > 512 {
		return false
	}
	if notValues[strings.ToLower(text)] {
		return false
	}
	if reNotValueShape.MatchString(text) || rePlainNumber.MatchString(text) || reCallExpression.MatchString(text) || rePropertyAccess.MatchString(text) || reEnvName.MatchString(text) {
		return false
	}
	if reTypeName.MatchString(text) && !reThreeDigits.MatchString(text) {
		return false
	}
	if reCamelCase.MatchString(text) && !reThreeDigits.MatchString(text) {
		return false
	}
	if reSnakeKebabWords.MatchString(text) || rePathLike.MatchString(text) {
		return false
	}
	classes := 0
	digits, lower, upper, symbol := reDigit.MatchString(text), reLower.MatchString(text), reUpper.MatchString(text), reSymbol.MatchString(text)
	for _, c := range []bool{digits, lower, upper, symbol} {
		if c {
			classes++
		}
	}
	return classes >= 2 && (digits || (lower && upper))
}

// Words that name a value as a stand-in (redact.ts SYNTHETIC_MARKERS / SYNTHETIC_SEGMENTS).
var (
	syntheticMarkers  = strings.Fields("synthetic devtok example sample dummy fake placeholder changeme hunter2 deadbeef cafebabe notreal foobar lorem acme")
	syntheticSegments = func() map[string]bool {
		m := map[string]bool{}
		for _, w := range strings.Fields("test tests demo sample fake dummy staging sandbox localdev example examples placeholder notreal redacted masked changeme hunter2") {
			m[w] = true
		}
		return m
	}()
)

const sequenceRun = 6

// hasSequenceRun: an ascending or descending run of consecutive UTF-16 code units, such as `0123456789`.
func hasSequenceRun(text string, min int) bool {
	units := utf16.Encode([]rune(text))
	ascending, descending := 1, 1
	for i := 1; i < len(units); i++ {
		step := int(units[i]) - int(units[i-1])
		if step == 1 {
			ascending++
		} else {
			ascending = 1
		}
		if step == -1 {
			descending++
		} else {
			descending = 1
		}
		if ascending >= min || descending >= min {
			return true
		}
	}
	return false
}

// isRepetitive: one unit repeated to fill the value, or three or fewer distinct characters.
func isRepetitive(text string) bool {
	lower := strings.ToLower(text)
	distinct := map[uint16]bool{}
	units := utf16.Encode([]rune(lower))
	for _, u := range units {
		distinct[u] = true
	}
	if len(distinct) <= 3 {
		return true
	}
	// /^(.{1,4})\1+$/: at least two repeats of a unit of one to four characters.
	runes := []rune(lower)
	for unit := 1; unit <= 4 && unit*2 <= len(runes); unit++ {
		if len(runes)%unit != 0 {
			continue
		}
		repeated := true
		for i := unit; i < len(runes); i++ {
			if runes[i] != runes[i-unit] {
				repeated = false
				break
			}
		}
		if repeated {
			return true
		}
	}
	return false
}

// Syntheticish is true for a credential-shaped value that is a stand-in rather than a credential: a name that
// says so, a sequence or repeat used as an example body, or a documented dummy. Judged from the value alone.
func Syntheticish(value string) bool {
	text := reTrimEndsSynth.get().ReplaceAllString(jsTrim(value), "")
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range syntheticMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, segment := range reNonAlnumRun.Split(lower, -1) {
		if syntheticSegments[segment] {
			return true
		}
	}
	body := reSynthPrefix.get().ReplaceAllString(lower, "")
	body = reNonAlnumRun.get().ReplaceAllString(body, "")
	return hasSequenceRun(text, sequenceRun) || hasSequenceRun(body, sequenceRun) || isRepetitive(body)
}

// signedURLSpans: start and end byte offsets of every signature parameter value in a signed https URL.
func signedURLSpans(text string) [][2]int {
	var spans [][2]int
	for _, loc := range reHTTPSURL.get().FindAllStringIndex(text, -1) {
		url := text[loc[0]:loc[1]]
		if !reSignedExpiry.MatchString(url) {
			continue
		}
		for _, p := range reSignedParam.get().FindAllStringSubmatchIndex(url, -1) {
			spans = append(spans, [2]int{loc[0] + p[2], loc[0] + p[3]})
		}
	}
	return spans
}

// signedURLOnly: every occurrence of value sits inside the query signature of a signed URL.
func signedURLOnly(value string, spans [][2]int, text string) bool {
	if len(spans) == 0 {
		return false
	}
	found := false
	for from := 0; from <= len(text); {
		i := strings.Index(text[from:], value)
		if i < 0 {
			break
		}
		i += from
		inside := false
		for _, s := range spans {
			if i >= s[0] && i+len(value) <= s[1] {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
		found = true
		from = i + 1
	}
	return found
}

// PartitionSecrets splits credentials into the ones worth announcing and the stand-ins that are only traced.
func PartitionSecrets(secrets []string) (real, synthetic []string) {
	for _, v := range secrets {
		if Syntheticish(v) {
			synthetic = append(synthetic, v)
		} else {
			real = append(real, v)
		}
	}
	return real, synthetic
}

// PartitionSecretsIn is PartitionSecrets with the text: a value found only in the query signature of a signed URL
// is a stand-in too.
func PartitionSecretsIn(secrets []string, text string) (real, synthetic []string) {
	spans := signedURLSpans(text)
	for _, v := range secrets {
		if Syntheticish(v) || signedURLOnly(v, spans, text) {
			synthetic = append(synthetic, v)
		} else {
			real = append(real, v)
		}
	}
	return real, synthetic
}

// assignmentValues returns the value of every `credential-key = value` assignment (redact.ts
// CREDENTIAL_ASSIGNMENT). A value stops at whitespace, quotes, `;`, `,`, `)` and a separator `&`.
func assignmentValues(text string) []string {
	var out []string
	pos := 0
	for pos < len(text) {
		loc := reAssignHead.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}
		headStart, headEnd := pos+loc[0], pos+loc[3]
		end := valueEndFind(text, headEnd)
		if end == headEnd {
			pos = headStart + 1
			continue
		}
		out = append(out, text[headEnd:end])
		pos = end
	}
	return out
}

func valueEndFind(text string, from int) int {
	end := from
	for end < len(text) {
		c := text[end]
		if isSpaceByte(c) || c == '"' || c == '\'' || c == ';' || c == ',' || c == ')' {
			break
		}
		if c == '&' {
			rest := text[end+1:]
			if rest == "" || rest[0] == '&' || isSpaceByte(rest[0]) || reAmpSeparator.MatchString(rest) {
				break
			}
		}
		end++
	}
	return end
}

// FindSecrets returns the credential-shaped values in text, deduplicated, in discovery order. Empty when the text
// only talks about credentials.
func FindSecrets(text string) []string {
	var found []string
	seen := map[string]bool{}
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	for _, shape := range reTokenShapes {
		for _, m := range shape.FindAllString(text, -1) {
			add(m)
		}
	}
	for _, v := range assignmentValues(text) {
		if v != "" && LooksLikeSecretValue(v) {
			add(v)
		}
	}
	for _, re := range []*lazyRegexp{reAuthorizationValue, reBearerValue, reURLPasswordValue} {
		for _, m := range re.get().FindAllStringSubmatch(text, -1) {
			if m[1] != "" && LooksLikeSecretValue(m[1]) {
				add(m[1])
			}
		}
	}
	return found
}

// MaskSecrets replaces every credential value FindSecrets detects with [redacted]; stand-ins and signed-URL
// signatures stay readable. It returns the text and how many distinct values were masked.
func MaskSecrets(text string) (string, int) {
	real, _ := PartitionSecretsIn(FindSecrets(text), text)
	sort.SliceStable(real, func(i, j int) bool { return utf16Len(real[i]) > utf16Len(real[j]) })
	out, masked := text, 0
	for _, v := range real {
		if !strings.Contains(out, v) {
			continue
		}
		out = strings.ReplaceAll(out, v, redacted)
		masked++
	}
	return out, masked
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// SecretFingerprint is a short, stable id for a set of secrets; the values themselves never leave FindSecrets.
func SecretFingerprint(secrets []string) string {
	sorted := append([]string(nil), secrets...)
	sort.Strings(sorted)
	return shortHash(strings.Join(sorted, "\n"))
}

// SecretIDs is one fingerprint per distinct value, so repeats are detectable even when the surrounding set changes.
func SecretIDs(secrets []string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, s := range secrets {
		if seen[s] {
			continue
		}
		seen[s] = true
		ids = append(ids, shortHash(s))
	}
	sort.Strings(ids)
	return ids
}
