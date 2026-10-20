package warden

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf16"
)

// The plain regexp implementations of Redact and FindSecrets, as written before the candidate scanning of
// redact_fast.go. They are the reference the fast ones must equal.

type refSet struct {
	privateKey, authorization, bearer, quotedDouble, quotedSingle, assignHead, urlPass *lazyRegexp
	tokenRules, tokenShapes                                                            []*lazyRegexp
	authorizationValue, bearerValue, urlPasswordValue                                  *lazyRegexp
}

// refs builds the reference expressions on first use: the test binary's package init is bounded
// (TestPackageInitDoesNotCompileRegexps), and these are only needed by the differential tests.
var refs = sync.OnceValue(func() *refSet {
	return &refSet{
		privateKey:    lazyRE(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		authorization: lazyRE(`(?i)(authorization\s*[:=]\s*)(?:basic|bearer|token)?\s*\S+`),
		bearer:        lazyRE(`(?i)\b(bearer\s+)\S+`),
		quotedDouble:  lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*)"[^"'\n]*"`),
		quotedSingle:  lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*)'[^"'\n]*'`),
		assignHead:    lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*["']?)`),
		urlPass:       lazyRE(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`),
		tokenRules: []*lazyRegexp{
			lazyRE(`\bsk-[A-Za-z0-9_-]{8,}`),
			lazyRE(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
			lazyRE(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
			lazyRE(`\bAKIA[0-9A-Z]{16}\b`),
			lazyRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
			lazyRE(`\bAIza[0-9A-Za-z_-]{30,}`),
			lazyRE(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
		},
		tokenShapes: []*lazyRegexp{
			lazyRE(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
			lazyRE(`\bsk-[A-Za-z0-9_-]{16,}`),
			lazyRE(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
			lazyRE(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
			lazyRE(`\bAKIA[0-9A-Z]{16}\b`),
			lazyRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
			lazyRE(`\bAIza[0-9A-Za-z_-]{30,}`),
			lazyRE(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
		},
		authorizationValue: lazyRE(`(?i)authorization\s*[:=]\s*(?:basic|bearer|token)?\s*([^\s"']+)`),
		bearerValue:        lazyRE(`(?i)\bbearer\s+([^\s"']+)`),
		urlPasswordValue:   lazyRE(`(?i)[a-z][a-z0-9+.-]*://[^\s/@:]+:([^\s/@]+)@`),
	}
})

func redactRef(text string) string {
	out := refs().privateKey.ReplaceAllString(text, redacted)
	out = refs().authorization.ReplaceAllString(out, "${1}"+redacted)
	out = refs().bearer.ReplaceAllString(out, "${1}"+redacted)
	out = refs().quotedDouble.ReplaceAllString(out, `${1}"`+redacted+`"`)
	out = refs().quotedSingle.ReplaceAllString(out, `${1}'`+redacted+`'`)
	out = redactUnquotedRef(out)
	out = refs().urlPass.ReplaceAllString(out, "${1}"+redacted+"@")
	for _, re := range refs().tokenRules {
		out = re.ReplaceAllString(out, redacted)
	}
	return out
}

func redactUnquotedRef(text string) string {
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		loc := refs().assignHead.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}
		headStart, headEnd := pos+loc[0], pos+loc[3]
		end := valueEnd(text, headEnd)
		if end == headEnd {
			out.WriteString(text[pos : headStart+1])
			pos = headStart + 1
			continue
		}
		out.WriteString(text[pos:headEnd])
		out.WriteString(redacted)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

func findSecretsRef(text string) []string {
	var found []string
	seen := map[string]bool{}
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	for _, shape := range refs().tokenShapes {
		for _, m := range shape.FindAllString(text, -1) {
			add(m)
		}
	}
	pos := 0
	for pos < len(text) {
		loc := refs().assignHead.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}
		headStart, headEnd := pos+loc[0], pos+loc[3]
		end := valueEndFind(text, headEnd)
		if end == headEnd {
			pos = headStart + 1
			continue
		}
		if v := text[headEnd:end]; v != "" && LooksLikeSecretValue(v) {
			add(v)
		}
		pos = end
	}
	for _, re := range []*lazyRegexp{refs().authorizationValue, refs().bearerValue, refs().urlPasswordValue} {
		for _, m := range re.get().FindAllStringSubmatch(text, -1) {
			if m[1] != "" && LooksLikeSecretValue(m[1]) {
				add(m[1])
			}
		}
	}
	return found
}

// fragments are the pieces generated text is made of: keys, separators, quotes, shapes and noise around them.
var redactFragments = []string{
	"api_key", "API_KEY", "ApiKey", "apikey", "access-key", "secret_key", "client_secret", "private_key", "password", "passwd", "passphrase", "PASSWORD", "token", "TOKEN", "secret", "credentials", "credential",
	"=", ":", " = ", ": ", "=\"", "='", "\"", "'", "&", "&&", ";", ",", ")", "(", " ", "\n", "\t", "  ", "://", "http", "postgres", "x", "ftp+ssh", "1", "-", ".", "/", "@", "user", "pw", "p4ss&w0rd",
	"k7Rm2Qx9Lp4Ns8Vt1Wd6" + "Yc3Zb5Hf7Jg0Pk2Mn5Qz", "abc123def456", "<redacted>", "${HOME}", "process.env.X", "Authorization", "authorization", "Authorization: Bearer ", "bearer ", "Bearer", "basic ", "token ",
	"sk-", "sk-live-0123456789abcdefABCDEF", "xsk-aaaaaaaaaaaaaaaaaaaa", "ghp_Qk7mZ2pR9vT4xL8nW3sY6bD1cF5hJ0aM", "github_pat_", "AKIAABCDEF" + "GHIJKLMNOP", "AKIA", "xoxb-1234567890-abcdef", "AIza", "AIzaSy7Lm2Rt9Vk4Np8Wd3Hz6Jc1Qx7Lm2Rabcdef",
	"eyJhbGciOiJIUzI1NiJ9", ".", "eyJzdWIiOiIxMjM0In0", "dGVzdHNpZ25hdHVyZTEyMw", "-----BEGIN PRIVATE KEY-----", "-----END PRIVATE KEY-----", "-----BEGIN RSA PRIVATE KEY-----", "MIIE", "é", "日本語", "\u00a0", "_", "a", "Z",
}

func generateRedactText(r *rand.Rand) string {
	var b strings.Builder
	for n := 1 + r.Intn(40); n > 0; n-- {
		b.WriteString(redactFragments[r.Intn(len(redactFragments))])
	}
	return b.String()
}

func TestFastRedactEqualsThePlainRegexpImplementation(t *testing.T) {
	var inputs []string
	for _, c := range loadDiffCorpusCommands(t) {
		inputs = append(inputs, c)
	}
	r := rand.New(rand.NewSource(42))
	for range 12000 {
		inputs = append(inputs, generateRedactText(r))
	}
	inputs = append(inputs, benchOutput(), strings.Repeat("token=abc ", 500), strings.Repeat("sk-", 300)+"aaaaaaaaaaaaaaaaaaaaaaaa", "xsk-aaaaaaaaaaaaaaaaaaaa-sk-bbbbbbbbbbbbbbbbbbbbbbbb", "http://a:b@c http://d:e@f")
	changed, found := 0, 0
	for _, in := range inputs {
		if got, want := Redact(in), redactRef(in); got != want {
			t.Fatalf("Redact differs on %q\n got %q\nwant %q", in, got, want)
		}
		if got, want := FindSecrets(in), findSecretsRef(in); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Fatalf("FindSecrets differs on %q\n got %q\nwant %q", in, got, want)
		}
		if Redact(in) != in {
			changed++
		}
		if len(FindSecrets(in)) > 0 {
			found++
		}
	}
	if changed < 3000 || found < 1000 {
		t.Fatalf("the generated text exercises too little: %d redacted, %d with secrets", changed, found)
	}
}

func loadDiffCorpusCommands(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("testdata/diff_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []corpusItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Command
	}
	return out
}

// The regexp forms of the text splitting the runaway and prose checks do by hand.
var (
	refSpaceRun       = lazyRE(`[` + jsSpaceClass + `]+`)
	refParagraphBreak = lazyRE(`\n[` + jsSpaceClass + `]*\n`)
	refSentenceBreak  = lazyRE(`(?:[.!?]|\n)+[` + jsSpaceClass + `]*`)
)

var textPieces = []string{"a", "word", "Hello", "x y", ".", "!", "?", "...", "\n", "\n\n", " ", "  ", "\t", "\r\n", "\u00a0", "\u2028", "\u3000", "\ufeff", "é", "日本", "\xff", "- item", "```", "1.5", "e.g."}

func generateText(r *rand.Rand) string {
	var b strings.Builder
	for n := r.Intn(30); n > 0; n-- {
		b.WriteString(textPieces[r.Intn(len(textPieces))])
	}
	return b.String()
}

func TestHandWrittenTextSplittingEqualsTheRegexps(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for range 20000 {
		in := generateText(r)
		if got, want := collapseSpaceBytes(in), refSpaceRun.ReplaceAllLiteralString(in, " "); got != want {
			t.Fatalf("collapse %q: got %q want %q", in, got, want)
		}
		if got, want := splitParagraphs(in), refParagraphBreak.Split(in, -1); !reflect.DeepEqual(got, want) {
			t.Fatalf("paragraphs %q: got %q want %q", in, got, want)
		}
		var want []string
		for _, piece := range refSentenceBreak.Split(in, -1) {
			if piece != "" {
				want = append(want, piece)
			}
		}
		if got := splitSentences(in); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Fatalf("sentences %q: got %q want %q", in, got, want)
		}
	}
}

// restatedShareRef is the restatement measure as first written: regexp sentence splitting and map word sets.
func restatedShareRef(reply string, earlier []string) float64 {
	type words struct {
		set   map[string]bool
		order []string
	}
	parse := func(text string) []words {
		var out []words
		for _, raw := range refSentenceBreak.Split(text, -1) {
			w := words{set: map[string]bool{}}
			for _, word := range strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
				if utf16Len(word) > 2 && !w.set[word] {
					w.set[word] = true
					w.order = append(w.order, word)
				}
			}
			if utf16Len(strings.Join(w.order, " ")) >= SentenceMinChars {
				out = append(out, w)
			}
		}
		return out
	}
	mine := parse(reply)
	var pool []words
	for _, text := range earlier {
		pool = append(pool, parse(text)...)
	}
	if len(mine) == 0 || len(pool) == 0 {
		return 0
	}
	restated := 0
sentence:
	for _, s := range mine {
		for _, old := range pool {
			shared := 0
			for _, w := range s.order {
				if old.set[w] {
					shared++
				}
			}
			if float64(shared)/float64(len(s.order)) >= SentenceOverlap {
				restated++
				continue sentence
			}
		}
	}
	return float64(restated) / float64(len(mine))
}

var sentenceVocabulary = [...]string{"the", "draft", "pull", "request", "is", "pushed", "with", "code", "tests", "and", "screenshots", "Linear", "sits", "In", "Review", "worktree", "awaits", "review", "done", "complete", "status", "CON-375", "2688", "café", "naïve", "日本語", "über", "x", "y", "of", "to", "a", "we"}

func generateSentences(r *rand.Rand) string {
	var b strings.Builder
	for s := 1 + r.Intn(5); s > 0; s-- {
		for w := 3 + r.Intn(9); w > 0; w-- {
			b.WriteString(sentenceVocabulary[r.Intn(len(sentenceVocabulary))])
			b.WriteString([]string{" ", " ", " ", ", ", "-"}[r.Intn(5)])
		}
		b.WriteString([]string{". ", "! ", "?\n", ".\n\n", "... "}[r.Intn(5)])
	}
	return b.String()
}

func TestRestatementMeasureEqualsTheMapImplementation(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	nonZero := 0
	for range 4000 {
		reply := generateSentences(r)
		var earlier []string
		for n := r.Intn(4); n > 0; n-- {
			earlier = append(earlier, generateSentences(r))
		}
		got, want := RestatedShare(reply, earlier), restatedShareRef(reply, earlier)
		if got != want {
			t.Fatalf("share %v want %v for %q against %q", got, want, reply, earlier)
		}
		if want > 0 {
			nonZero++
		}
		w := NewRestatementWindow(4)
		for _, e := range earlier {
			w.Record(e)
		}
		if got := w.Share(reply); got != want {
			t.Fatalf("window share %v want %v", got, want)
		}
	}
	if nonZero < 150 {
		t.Fatalf("the generated replies restate too rarely: %d", nonZero)
	}
}

// repeatedTailRef is RepeatedTail as first written, on UTF-16 units with the regexp normalisation.
func repeatedTailRef(text string, chars, minPeriod int) RepeatSignal {
	normal := utf16.Encode([]rune(strings.ToLower(jsTrim(refSpaceRun.ReplaceAllLiteralString(text, " ")))))
	if len(normal) < chars*2 {
		return RepeatSignal{}
	}
	start := len(normal) - chars
	phrase := normal[start:]
	previous := lastIndexUnits(normal, phrase, start-1)
	if previous == -1 {
		return RepeatSignal{Count: 1, Block: string(utf16.Decode(phrase))}
	}
	period := start - previous
	if period < minPeriod {
		return RepeatSignal{Count: 1, Block: string(utf16.Decode(phrase))}
	}
	unit := normal[len(normal)-period:]
	count := 1
	for end := len(normal) - period; end >= period && equalUnits(normal[end-period:end], unit); end -= period {
		count++
	}
	return RepeatSignal{Count: count, Block: string(utf16.Decode(unit))}
}

func repeatedBlockRef(text string, minChars int) RepeatSignal {
	counts := map[string]int{}
	best := RepeatSignal{}
	for _, raw := range refParagraphBreak.Split(text, -1) {
		block := strings.ToLower(jsTrim(refSpaceRun.ReplaceAllLiteralString(raw, " ")))
		if utf16Len(block) < minChars {
			continue
		}
		counts[block]++
		if counts[block] > best.Count {
			best = RepeatSignal{Count: counts[block], Block: block}
		}
	}
	return best
}

func generateRunOn(r *rand.Rand) string {
	units := []string{"All checks passed on the PR — merging and watching the deploy. ", "Stop. PR green. Merge.\n\n```bash\ngh pr merge 1234\n```\n\n", "same thing again ", "ÄÖÜ unit ünït ", "|---", "Done.\n\n", "x"}
	var b strings.Builder
	unit := units[r.Intn(len(units))]
	for n := r.Intn(16); n > 0; n-- {
		b.WriteString(unit)
	}
	for n := r.Intn(4); n > 0; n-- {
		b.WriteString(textPieces[r.Intn(len(textPieces))])
		u := units[r.Intn(len(units))]
		b.WriteString(u[:min(len(u), r.Intn(10))])
	}
	return b.String()
}

func TestFusedRunawayTextHandlingEqualsTheRegexpAndUnitVersions(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	repeated := 0
	for range 20000 {
		in := generateRunOn(r)
		if got, want := RepeatedTail(in, PhraseChars, BlockMinChars), repeatedTailRef(in, PhraseChars, BlockMinChars); got.Count != want.Count || got.Block != want.Block {
			t.Fatalf("tail %q: got %+v want %+v", in, got, want)
		}
		if got, want := RepeatedBlock(in, BlockMinChars), repeatedBlockRef(in, BlockMinChars); got.Count != want.Count || got.Block != want.Block {
			t.Fatalf("block %q: got %+v want %+v", in, got, want)
		}
		if repeatedTailRef(in, PhraseChars, BlockMinChars).Count > 2 {
			repeated++
		}
	}
	if repeated < 500 {
		t.Fatalf("the generated text repeats too rarely: %d", repeated)
	}
}
