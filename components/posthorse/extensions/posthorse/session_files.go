package posthorse

// Port of src/session-files.ts: session JSONL discovery, scope membership, the raw-text
// prefilter (ripgrep and the in-process line matcher) and window-aware entry streaming.
//
// Streaming: session archives can hold gigabytes, so nothing here keeps a file in memory.
// sessionLines (session_lines.go) hands over each line's first 512 bytes and, only for lines a
// caller selects, its text; a line the prefilter rejects is never decoded.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// firstSearchBytes: newest archive bytes searched before the rest; common queries finish within
// them (session-files.ts:17).
const firstSearchBytes = 256 * 1024 * 1024

// argumentChars: file-list characters per ripgrep run, within Windows' 32,767 and POSIX ARG_MAX
// limits (session-files.ts:19).
var argumentChars = func() int {
	if runtime.GOOS == "windows" {
		return 30_000
	}
	return 256 * 1024
}()

// rgMaxBuffer is execFile's `maxBuffer: 64 * 1024 * 1024` (session-files.ts:28).
const rgMaxBuffer = 64 * 1024 * 1024

// labelSource is one LABEL_SOURCES row (session-files.ts:42-50): label text normalization adds,
// and the raw JSONL every entry showing it contains.
type labelSource struct {
	label   string
	needles []string
}

var labelSources = [...]labelSource{
	{"call id", []string{`"type":"toolCall"`, `"toolCallId"`}},
	{"execution arguments", []string{`"executionArguments"`}},
	{"no handoff", []string{`"type":"context_window"`}},
	{"excluded from model context by pi", []string{`"excludeFromContext":true`}},
	{"images", []string{`"type":"image"`}},
	{"unknown type", []string{`"type":"image"`}},
	{"unknown", []string{`"type":"custom_message"`}},
}

var imageFragment = regexp.MustCompile(`^[0-9]+ ?i?m?a?g?e?s?$`)

// splitsFragment reports the characters session-files.ts:62 splits a query on:
// ["\\[\](){}:$\p{Cc}\p{Cs}]. A lone surrogate is the three-byte form at s[i:].
func splitsFragment(s string, i int) (size int, split bool) {
	if isWTF8Surrogate(s, i) {
		return 3, true
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	switch r {
	case '"', '\\', '[', ']', '(', ')', '{', '}', ':', '$':
		return size, true
	}
	return size, unicode.Is(unicode.Cc, r)
}

// prefilterNeedles is `prefilterNeedles` (session-files.ts:57-79): raw JSONL needles, one of
// which every line rendering the lowercase query contains; false means every line must be
// scanned.
func prefilterNeedles(query string) ([]string, bool) {
	// A match contains every fragment between characters that JSON escapes or labels may add.
	// The longest trimmed part (by UTF-16 length, the first of equals) wins.
	fragment, fragmentLen := "", -1
	start := 0
	consider := func(part string) {
		part = jsTrim(part)
		if n := u16len(part); n > fragmentLen {
			fragment, fragmentLen = part, n
		}
	}
	for i := 0; i < len(query); {
		size, split := splitsFragment(query, i)
		if split {
			consider(query[start:i])
			start = i + size
		}
		i += size
	}
	consider(query[start:])
	if fragment == "" {
		return nil, false
	}
	image := strings.Contains(fragment, ",") || imageFragment.MatchString(fragment)
	needles := []string{fragment}
	for _, source := range labelSources {
		if strings.Contains(source.label, fragment) {
			needles = append(needles, source.needles...)
		}
	}
	if image {
		needles = append(needles, `"type":"image"`)
	}
	// İ lowercases to i + U+0307, which case-insensitive raw matching cannot see.
	if strings.ContainsAny(fragment, "i\u0307") {
		needles = append(needles, "\u0130")
	}
	return needles, true
}

// ASCII_FOLDS (session-files.ts:81-85): UTF-8 of the only non-ASCII characters that case-fold to
// ASCII letters, read as Latin-1. The matcher also accepts the Latin-1 case variants of their
// first byte, which the case-insensitive JS regexp over those characters accepts.
const (
	kelvinUTF8 = "\xe2\x84\xaa" // U+212A KELVIN SIGN
	longSUTF8  = "\xc5\xbf"     // U+017F LATIN SMALL LETTER LONG S
	dottedI    = "\xc4\xb0"     // DOTTED_I: U+0130 as Latin-1 text (session-files.ts:86)
)

var lowerASCII = func() (t [256]byte) {
	for i := range t {
		t[i] = byte(i)
		if i >= 'A' && i <= 'Z' {
			t[i] = byte(i) + 'a' - 'A'
		}
	}
	return t
}()

// byteRarity ranks how common a byte is in session JSONL text (lower is rarer), for choosing the
// anchor byte of a foldPattern. Letters follow English frequencies in tenths of a percent.
var byteRarity = func() (t [256]uint16) {
	for i := range t {
		t[i] = 5 // other punctuation and control bytes
	}
	for c, f := range map[byte]uint16{
		'e': 127, 't': 91, 'a': 82, 'o': 75, 'i': 70, 'n': 67, 's': 63, 'h': 61, 'r': 60, 'd': 43, 'l': 40,
		'c': 28, 'u': 28, 'm': 24, 'w': 24, 'f': 22, 'g': 20, 'y': 20, 'p': 19, 'b': 15, 'v': 10, 'k': 8,
		'j': 2, 'x': 2, 'q': 1, 'z': 1, ' ': 150, '"': 100, ':': 60, ',': 60, '{': 50, '}': 50, '\n': 20,
	} {
		t[c] = f
	}
	for c := byte('0'); c <= '9'; c++ {
		t[c] = 30
	}
	for c := byte('A'); c <= 'Z'; c++ {
		t[c] = t[c+'a'-'A']
	}
	return t
}()

// foldPattern searches a lowercase ASCII pattern in text ignoring ASCII case. It looks for the
// rarest byte of the pattern with the vectorized IndexByte (both cases) and checks the pattern
// around each hit, so a window that lacks that byte is skipped at memory speed whatever else it
// holds.
type foldPattern struct {
	p string
	// exact is true when p holds no ASCII letter: case does not matter and strings.Index is exact.
	exact  bool
	anchor int  // index in p of the anchor byte
	lo, hi byte // the anchor in each case (equal for a caseless byte)
}

func newFoldPattern(p string) *foldPattern {
	f := &foldPattern{p: p, exact: !strings.ContainsFunc(p, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' })}
	for j := 1; j < len(p); j++ {
		if byteRarity[p[j]] < byteRarity[p[f.anchor]] {
			f.anchor = j
		}
	}
	if len(p) > 0 {
		f.lo, f.hi = p[f.anchor], p[f.anchor]
		if f.lo >= 'a' && f.lo <= 'z' {
			f.hi = f.lo - 'a' + 'A'
		}
	}
	return f
}

// index is the byte offset of the first match of the pattern in text, or -1.
func (f *foldPattern) index(text string) int {
	if f.exact {
		return strings.Index(text, f.p)
	}
	p := f.p
	m, n := len(p), len(text)
	if m == 0 {
		return 0
	}
	// The anchor's next occurrence in each case; n means none (later than every candidate).
	nextLo, nextHi := -1, -1
	for pos := f.anchor; pos < n-(m-1-f.anchor); {
		if nextLo < pos {
			nextLo = n
			if r := strings.IndexByte(text[pos:], f.lo); r >= 0 {
				nextLo = pos + r
			}
		}
		if nextHi < pos {
			nextHi = n
			if f.hi != f.lo {
				if r := strings.IndexByte(text[pos:], f.hi); r >= 0 {
					nextHi = pos + r
				}
			}
		}
		at := min(nextLo, nextHi)
		start := at - f.anchor
		if start+m > n {
			return -1
		}
		j := 0
		for j < m && lowerASCII[text[start+j]] == p[j] {
			j++
		}
		if j == m {
			return start
		}
		pos = at + 1
	}
	return -1
}

// foldIndexString is foldPattern.index for a one-off search: short texts are scanned directly.
func foldIndexString(text, p string) int {
	if len(p) == 0 {
		return 0
	}
	if len(text) >= 256 {
		return newFoldPattern(p).index(text)
	}
	m := len(p)
	for i := 0; i+m <= len(text); i++ {
		j := 0
		for j < m && lowerASCII[text[i+j]] == p[j] {
			j++
		}
		if j == m {
			return i
		}
	}
	return -1
}

// asciiLines is the Latin-1 matcher of needleLines (session-files.ts:94-110) for ASCII needles.
type asciiLines struct {
	patterns []*foldPattern
	dotted   bool
	// alternates is true when a needle holds k or s, whose case folds are two- and three-byte
	// sequences in the raw text.
	alternates bool
	needles    []string
}

// altSequences are the byte sequences that make a window need the exact matcher: the folds of
// k and s, and their Latin-1 case variants (â/Â and Å/å are the same letter to /i).
var altSequences = [...]string{kelvinUTF8, "\xc2\x84\xaa", longSUTF8, "\xe5\xbf"}

func (a *asciiLines) test(text string) bool {
	if a.dotted && strings.Contains(text, dottedI) {
		return true
	}
	if a.alternates {
		for _, seq := range altSequences {
			if strings.Contains(text, seq) {
				return a.exact(text)
			}
		}
	}
	for _, p := range a.patterns {
		if p.index(text) >= 0 {
			return true
		}
	}
	return false
}

// exact is the JS regexp `(?:k|\xe2\x84\xaa)...` with the i flag over Latin-1 text, written out.
func (a *asciiLines) exact(text string) bool {
	for _, needle := range a.needles {
		for i := 0; i <= len(text); i++ {
			if matchFolded(needle, 0, text, i) {
				return true
			}
		}
	}
	return false
}

func matchFolded(needle string, j int, text string, i int) bool {
	if j == len(needle) {
		return true
	}
	c := lowerASCII[needle[j]]
	if i < len(text) && lowerASCII[text[i]] == c && matchFolded(needle, j+1, text, i+1) {
		return true
	}
	switch c {
	case 'k':
		if i+2 < len(text) && (text[i] == 0xe2 || text[i] == 0xc2) && text[i+1] == 0x84 && text[i+2] == 0xaa {
			return matchFolded(needle, j+1, text, i+3)
		}
	case 's':
		if i+1 < len(text) && (text[i] == 0xc5 || text[i] == 0xe5) && text[i+1] == 0xbf {
			return matchFolded(needle, j+1, text, i+2)
		}
	}
	return false
}

// needleLines is `needleLines` (session-files.ts:87-118): the raw-line matcher for
// prefilterNeedles, ignoring case like the rendered text would. For encoding "latin1" the
// matcher reads its window as raw bytes (byte == character).
func needleLines(needles []string) *lineMatcher {
	longest := 0
	for _, needle := range needles {
		longest = max(longest, u16len(needle))
	}
	overlap := 4*longest + 4
	others := make([]string, 0, len(needles))
	for _, needle := range needles {
		if needle != "\u0130" {
			others = append(others, needle)
		}
	}
	dotted := len(others) < len(needles)
	ascii := true
	for _, needle := range others {
		if !isASCII(needle) {
			ascii = false
			break
		}
	}
	if ascii {
		// ASCII text matches its Latin-1 byte view, so most lines are never decoded as UTF-8.
		if len(others) == 0 {
			// new RegExp("", "i") matches every line.
			return &lineMatcher{test: func(string) bool { return true }, encoding: "latin1", overlap: overlap}
		}
		m := &asciiLines{dotted: dotted}
		for _, needle := range others {
			lowered := strings.ToLower(needle)
			m.needles = append(m.needles, lowered)
			if strings.ContainsAny(lowered, "ks") {
				m.alternates = true
			}
			m.patterns = append(m.patterns, newFoldPattern(lowered))
		}
		return &lineMatcher{test: m.test, encoding: "latin1", overlap: overlap}
	}
	quoted := make([]string, len(needles))
	for i, needle := range needles {
		quoted[i] = regexp.QuoteMeta(needle)
	}
	pattern, err := regexp.Compile("(?i)" + strings.Join(quoted, "|"))
	if err != nil {
		// An oversized needle exceeds the regexp program limit: every line stays a candidate,
		// which the entry match then filters (the prefilter is an optimization only).
		return &lineMatcher{test: func(string) bool { return true }, encoding: "utf8", overlap: overlap}
	}
	// The regexp is exact but slow (no literal prefix under case folding). Every match contains
	// each needle's longest run of characters that fold only among ASCII letters, so a cheap
	// search for those anchors decides most windows; the regexp confirms a window that has one.
	anchors := make([]*foldPattern, 0, len(needles))
	for _, needle := range needles {
		anchor := stableAnchor(needle)
		if anchor == "" {
			return &lineMatcher{test: pattern.MatchString, encoding: "utf8", overlap: overlap}
		}
		anchors = append(anchors, newFoldPattern(anchor))
	}
	return &lineMatcher{encoding: "utf8", overlap: overlap, test: func(text string) bool {
		for _, anchor := range anchors {
			if anchor.index(text) >= 0 {
				return pattern.MatchString(text)
			}
		}
		return false
	}}
}

// stableAnchor is the longest run of s whose characters match only themselves, up to ASCII case,
// under simple case folding: caseless characters and ASCII letters other than k and s (K, ſ and
// the Kelvin sign fold together). The run is lowercased for foldPattern; "" if s has none.
func stableAnchor(s string) string {
	best, bestLen := "", 0
	var b strings.Builder
	flush := func() {
		if b.Len() > bestLen {
			best, bestLen = b.String(), b.Len()
		}
		b.Reset()
	}
	for _, r := range s {
		if !foldStable(r) {
			flush()
			continue
		}
		if r < utf8.RuneSelf && r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	flush()
	return best
}

// foldStable: the simple-fold orbit of r holds only r, or only ASCII letters.
func foldStable(r rune) bool {
	if r == utf8.RuneError {
		return false
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f >= utf8.RuneSelf || r >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// commandGroups is `commandGroups` (session-files.ts:120-135): file lists that each fit one
// ripgrep command line.
func commandGroups(paths []string) [][]string {
	var groups [][]string
	var group []string
	chars := 0
	for _, path := range paths {
		n := u16len(path)
		if len(group) > 0 && chars+n > argumentChars {
			groups = append(groups, group)
			group = nil
			chars = 0
		}
		group = append(group, path)
		chars += n + 1
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups
}

// capBuffer is a bounded output buffer: a write past the limit cancels the command, as
// execFile's maxBuffer kills the child.
type capBuffer struct {
	buf    bytes.Buffer
	limit  int
	cancel context.CancelCauseFunc
}

var errRipgrepOutput = errors.New("ripgrep output exceeded maxBuffer")

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		c.cancel(errRipgrepOutput)
		return 0, errRipgrepOutput
	}
	return c.buf.Write(p)
}

// execFileText is `execFileText` (session-files.ts:21-37): stdout of a command, or its error.
func execFileText(ctx context.Context, file string, args []string, dir string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	cmd := exec.CommandContext(ctx, file, args...)
	cmd.Dir = dir
	out := &capBuffer{limit: rgMaxBuffer, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.buf.String(), nil
}

// isNoEntry is `error.code === "ENOENT"`: the binary (or the working directory) does not exist.
func isNoEntry(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

// ripgrepFilter is `ripgrepFilter` (session-files.ts:136-186): files containing a needle, per
// ripgrep; files of a failed run stay candidates. The binaries are tried in order and one that
// does not exist is dropped for the rest of the search.
func ripgrepFilter(ctx context.Context, dir string, needles []string) func(files []string) ([]string, error) {
	// getAgentDir() is the host's directory; a host that does not provide it has no managed
	// binary, which leaves PATH (the original's own fallback).
	var binaries []string
	if agentDir, err := sdk.AgentDir(); err == nil {
		binaries = append(binaries, filepath.Join(agentDir, "bin", "rg"))
	}
	binaries = append(binaries, "rg")
	var mu sync.Mutex
	flags := make([]string, 0, 6+2*len(needles))
	flags = append(flags, "--files-with-matches", "--null", "--fixed-strings", "--ignore-case", "--no-messages")
	for _, needle := range needles {
		flags = append(flags, "--regexp", needle)
	}
	flags = append(flags, "--")
	matching := func(paths []string) ([]string, error) {
		mu.Lock()
		candidates := append([]string(nil), binaries...)
		mu.Unlock()
		for _, binary := range candidates {
			args := append(append(make([]string, 0, len(flags)+len(paths)), flags...), paths...)
			// Try the host-managed binary first; PATH is a fallback only when it is unavailable.
			stdout, err := execFileText(ctx, binary, args, dir)
			if err == nil {
				return strings.Split(stdout, "\x00"), nil
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return nil, nil
			}
			if !isNoEntry(err) {
				if aborted := throwIfAborted(ctx); aborted != nil {
					return nil, aborted
				}
				return paths, nil
			}
			mu.Lock()
			kept := make([]string, 0, len(binaries))
			for _, candidate := range binaries {
				if candidate != binary {
					kept = append(kept, candidate)
				}
			}
			binaries = kept
			mu.Unlock()
		}
		return paths, nil
	}
	return func(files []string) ([]string, error) {
		paths := make([]string, len(files))
		for i, file := range files {
			paths[i], _ = filepath.Rel(dir, file)
		}
		groups := commandGroups(paths)
		results := make([][]string, len(groups))
		errs := make([]error, len(groups))
		var wg sync.WaitGroup
		for i, group := range groups {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = matching(group)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
		found := make(map[string]struct{})
		for _, result := range results {
			for _, path := range result {
				found[path] = struct{}{}
			}
		}
		kept := make([]string, 0, len(files))
		for i, file := range files {
			if _, ok := found[paths[i]]; ok {
				kept = append(kept, file)
			}
		}
		return kept, nil
	}
}

// entryHead is the metadata of the leading keys of a Pi-ordered line.
type entryHead struct {
	id       string
	parentID string // "" for a null parent
}

// cutPlain splits s at the first double quote when no backslash precedes it: the
// `([^"\\]*)"` of the head pattern.
func cutPlain(s string) (value, rest string, ok bool) {
	i := strings.IndexAny(s, "\"\\")
	if i < 0 || s[i] == '\\' {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// parseEntryHead is `entryHead` (session-files.ts:243-254): window metadata of a Pi-ordered
// line that cannot open a window, without parsing it. The pattern is
// ^\{"type":"([^"\\]*)","id":"([^"\\]*)","parentId":(null|"[^"\\]*")[,}] ; compaction and
// context_window lines are not heads.
func parseEntryHead(prefix string) (entryHead, bool) {
	rest, ok := strings.CutPrefix(prefix, `{"type":"`)
	if !ok {
		return entryHead{}, false
	}
	typ, rest, ok := cutPlain(rest)
	if !ok {
		return entryHead{}, false
	}
	if rest, ok = strings.CutPrefix(rest, `,"id":"`); !ok {
		return entryHead{}, false
	}
	id, rest, ok := cutPlain(rest)
	if !ok {
		return entryHead{}, false
	}
	if rest, ok = strings.CutPrefix(rest, `,"parentId":`); !ok {
		return entryHead{}, false
	}
	parent := ""
	if after, isNull := strings.CutPrefix(rest, "null"); isNull {
		rest = after
	} else if quoted, isQuoted := strings.CutPrefix(rest, `"`); isQuoted {
		if parent, rest, ok = cutPlain(quoted); !ok {
			return entryHead{}, false
		}
	} else {
		return entryHead{}, false
	}
	if rest == "" || (rest[0] != ',' && rest[0] != '}') {
		return entryHead{}, false
	}
	if typ == "compaction" || typ == "context_window" {
		return entryHead{}, false
	}
	return entryHead{id: id, parentID: parent}, true
}

// lineFilter is the TS LineFilter reduced to the one fact its callers read: whether a caller
// may need the line with this entry id; other lines only advance window ancestry.
type lineFilter func(id string) bool

// sessionWindowEntries is `sessionWindowEntries` (session-files.ts:274-295): the entries of one
// session file, oldest first, with their window ids. A nil wanted accepts every id. A search
// (forRead false) gets entries that are valid only until the loop advances and carry no images; a
// read gets entries that own their strings, images included.
func sessionWindowEntries(ctx context.Context, file string, wanted lineFilter, matcher *lineMatcher, forRead bool) iter.Seq2[windowedEntry, error] {
	return func(yield func(windowedEntry, error) bool) {
		if !existsSync(file) {
			return
		}
		project := windowProjection()
		for line, err := range sessionLines(ctx, file, matcher) {
			if err != nil {
				yield(windowedEntry{}, err)
				return
			}
			head, isHead := parseEntryHead(line.head)
			if isHead && (!line.matches || (wanted != nil && !wanted(head.id))) {
				project.track(head.id, head.parentID, false)
				continue
			}
			text, err := line.text()
			if err != nil {
				yield(windowedEntry{}, err)
				return
			}
			// A search looks at the entry within this step, so it reads the line where it lies (no
			// copy); a read returns the entry and needs one that owns its strings.
			parse := parseSessionLineView
			if forRead {
				parse = parseSessionLine
			}
			entry, ok := parse(text)
			if !ok {
				continue
			}
			windowID := project.project(entry)
			selected := isHead
			if !selected && line.matches {
				id, _ := str(entry, "id")
				selected = wanted == nil || wanted(id)
			}
			if !selected {
				continue
			}
			build := windowedEntryOf
			if forRead {
				build = toWindowedEntry
			}
			if item, ok := build(entry, windowID); ok && !yield(item, nil) {
				return
			}
		}
	}
}

// sessionFiles is `sessionFiles` (session-files.ts:296-322): breadth-first like readdirSync's
// recursive order, reading each level concurrently. Names within a directory are sorted, as
// libuv's scandir sorts them.
func sessionFiles(dir string) ([]string, error) {
	var files []string
	var level []string
	if _, err := os.Stat(dir); err == nil {
		level = []string{dir}
	}
	for len(level) > 0 {
		// Each level lists the directories the previous level found.
		listings := make([][]os.DirEntry, len(level))
		errs := make([]error, len(level))
		if len(level) == 1 {
			listings[0], errs[0] = readDirNode(level[0])
		} else {
			var wg sync.WaitGroup
			sem := make(chan struct{}, 16)
			for i, path := range level {
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					listings[i], errs[i] = readDirNode(path)
				}()
			}
			wg.Wait()
		}
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
		next := make([]string, 0)
		for i, entries := range listings {
			for _, entry := range entries {
				path := filepath.Join(level[i], entry.Name())
				switch {
				case entry.IsDir():
					next = append(next, path)
				case entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") && !strings.Contains(entry.Name(), ".intent."):
					files = append(files, path)
				}
			}
		}
		level = next
	}
	return files, nil
}

// sessionFile is the TS SessionFile (session-files.ts:323).
type sessionFile struct {
	file  string
	size  int64
	mtime float64
}

// newestFirst is `newestFirst` (session-files.ts:324-333): modification time, newest first,
// stable for ties. The time is Node's `mtimeMs` = sec*1e3 + nsec/1e6 as a double.
func newestFirst(files []string) ([]sessionFile, error) {
	stats := make([]sessionFile, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, file := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			info, err := os.Stat(file)
			if err != nil {
				errs[i] = nodeFsError(err, "stat", file)
				return
			}
			mod := info.ModTime()
			stats[i] = sessionFile{file: file, size: info.Size(), mtime: float64(mod.Unix())*1e3 + float64(mod.Nanosecond())/1e6}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(stats, func(a, b int) bool { return stats[a].mtime > stats[b].mtime })
	return stats, nil
}

// searchPhases is `searchPhases` (session-files.ts:334-348): the newest files up to
// firstSearchBytes, then the rest.
func searchPhases(files []sessionFile) [2][]string {
	count := 0
	var total int64
	for _, file := range files {
		if total >= firstSearchBytes {
			break
		}
		total += file.size
		count++
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.file
	}
	return [2][]string{paths[:count], paths[count:]}
}

// sessionHeader is `sessionHeader` (session-files.ts:349-364): the first parsed line of a
// session file when it is a session header; ok is false for anything else.
func sessionHeader(ctx context.Context, file string) (cwd string, hasCwd, ok bool, err error) {
	for line, lineErr := range lfLines(ctx, file, 64*1024) {
		if lineErr != nil {
			return "", false, false, lineErr
		}
		value, parsed := jsonParse(jsFromBytes(line))
		if !parsed {
			continue // Pi skips malformed lines before the first parsed entry.
		}
		if value == nil {
			continue
		}
		header, isRec := value.(rec)
		if !isRec {
			return "", false, false, nil
		}
		if typ, _ := str(header, "type"); typ != "session" {
			return "", false, false, nil
		}
		if _, hasID := str(header, "id"); !hasID {
			return "", false, false, nil
		}
		cwd, hasCwd = str(header, "cwd")
		return cwd, hasCwd, true, nil
	}
	return "", false, false, nil
}

// parentSession is `parentSession` (session-files.ts:365-377): a nested file belongs to the
// session file named like its directory.
func parentSession(file, dir string, hasFile func(string) bool) (string, bool) {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i := len(parts) - 1; i > 0; i-- {
		candidate := filepath.Join(append([]string{dir}, parts[:i]...)...) + ".jsonl"
		if hasFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// sessionScope is the TS SessionScope (session-files.ts:378-386). currentFile "" and fileKey nil
// are undefined; nil needles mean no prefilter.
type sessionScope struct {
	dir         string
	cwd         string
	currentFile string
	fileKey     *string
	needles     []string
}

// membership is `membership` (session-files.ts:387-409): scope memoization is owned by one
// enumeration; nested runs inherit their parent's project.
type membership struct {
	ctx     context.Context
	scope   sessionScope
	hasFile func(string) bool
	visible map[string]bool
}

func (m *membership) belongs(file string) (bool, error) {
	if known, ok := m.visible[file]; ok {
		return known, nil
	}
	cwd, hasCwd, ok, err := sessionHeader(m.ctx, file)
	if err != nil {
		return false, err
	}
	if !ok {
		m.visible[file] = false
		return false, nil
	}
	parent, hasParent := parentSession(file, m.scope.dir, m.hasFile)
	allowed := m.scope.currentFile != "" && file == m.scope.currentFile
	if !allowed {
		if !hasParent {
			if hasCwd && cwd != "" {
				abs, absErr := filepath.Abs(cwd)
				allowed = absErr == nil && abs == m.scope.cwd
			}
		} else if allowed, err = m.belongs(parent); err != nil {
			return false, err
		}
	}
	m.visible[file] = allowed
	return allowed, nil
}

// scopedSessionFiles is `scopedSessionFiles` (session-files.ts:410-): newest modified project
// session files, optionally limited by file key or needles.
func scopedSessionFiles(ctx context.Context, scope sessionScope) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		files, err := sessionFiles(scope.dir)
		if err != nil {
			yield("", err)
			return
		}
		fileSet := make(map[string]struct{}, len(files))
		for _, file := range files {
			fileSet[file] = struct{}{}
		}
		belongs := &membership{
			ctx: ctx, scope: scope, visible: make(map[string]bool),
			hasFile: func(file string) bool { _, ok := fileSet[file]; return ok },
		}
		candidates := files
		if scope.fileKey != nil {
			candidates = make([]string, 0, len(files))
			for _, file := range files {
				rel, _ := filepath.Rel(scope.dir, file)
				if strings.HasPrefix(*scope.fileKey, shortKey(rel)) {
					candidates = append(candidates, file)
				}
			}
		}
		selected, err := newestFirst(candidates)
		if err != nil {
			yield("", err)
			return
		}
		var prefilter func([]string) ([]string, error)
		if scope.needles != nil {
			prefilter = ripgrepFilter(ctx, scope.dir, scope.needles)
		}
		for _, phase := range searchPhases(selected) {
			// Older files are searched only when the newest ones did not satisfy the caller.
			if prefilter != nil {
				if phase, err = prefilter(phase); err != nil {
					yield("", err)
					return
				}
			}
			for _, file := range phase {
				// Preserve newest-first traversal while closing each header stream before opening another.
				ok, err := belongs.belongs(file)
				if err != nil {
					yield("", err)
					return
				}
				if ok && !yield(file, nil) {
					return
				}
			}
		}
	}
}
