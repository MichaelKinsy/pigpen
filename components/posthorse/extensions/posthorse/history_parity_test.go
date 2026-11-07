package posthorse

// Parity tests of the history lane (history*.go, session_files.go, session_lines.go) against the TypeScript original. The differential tests read the oracle's answers from
// testdata/historyrev_*.json.gz, written by port/review/historyrev-probes/probe.mts (Node 24.22.1 on xd6702; regenerate with
// port/tools/gen-testdata.sh), and fail when those files are absent. See port/review/historyrev.md.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"unicode"
	"unicode/utf8"
)

func historyParityLoad(t *testing.T, name string, v any) {
	t.Helper()
	data := testdataGz(t, "historyrev_"+name+".gz")
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

// historyParityString builds the port's string form of UTF-16 units: pairs as UTF-8, lone surrogates as WTF-8.
func historyParityString(units []int) string {
	var b []byte
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		if u >= 0xD800 && u < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] < 0xE000 {
			b = utf8.AppendRune(b, 0x10000+(u-0xD800)<<10+rune(units[i+1])-0xDC00)
			i++
			continue
		}
		if u >= 0xD800 && u < 0xE000 {
			s := surrogateBytes(u)
			b = append(b, s[:]...)
			continue
		}
		b = utf8.AppendRune(b, u)
	}
	return string(b)
}

// history-search.ts:311 and history-entries.ts:156,169,183,250 (String.prototype.toLowerCase): V8 (ICU) treats U+0387,
// U+055F and U+05F4 as Case_Ignorable for Final_Sigma. Oracle (node): "ΑΣ\u0387Β".toLowerCase() = "ασ\u0387β",
// "Α\u0387Σ".toLowerCase() = "α\u0387ς"; "ΑΣ\u0387Β".toLowerCase().includes("ασ\u0387β") = true.
func TestHistoryParityFinalSigmaCaseIgnorable(t *testing.T) {
	for in, want := range map[string]string{
		"ΑΣ\u0387Β": "ασ\u0387β",
		"Α\u0387Σ":  "α\u0387ς",
		"ΑΣ\u055FΒ": "ασ\u055Fβ",
		"Α\u05F4Σ":  "α\u05F4ς",
	} {
		if got := jsToLowerCase(in); got != want {
			t.Errorf("jsToLowerCase(%q) = %q, oracle %q", in, got, want)
		}
	}
	if !historyIncludes("ΑΣ\u0387Β", "ασ\u0387β") {
		t.Errorf(`historyIncludes("ΑΣ·Β", "ασ·β") = false; oracle "ΑΣ·Β".toLowerCase().includes("ασ·β") = true`)
	}
}

// Differential: toLowerCase and toLowerCase().indexOf over 20,000 random strings (Greek, sigma contexts, İ, lone
// surrogates, Kelvin, long s, Cherokee).
func TestHistoryParityLowerDifferential(t *testing.T) {
	var cases []struct {
		S, Lower, Q []int
		Index       int
	}
	historyParityLoad(t, "lower.json", &cases)
	bad, badIndex := 0, 0
	for _, c := range cases {
		s, want := historyParityString(c.S), historyParityString(c.Lower)
		if got := jsToLowerCase(s); got != want {
			if bad < 5 {
				t.Errorf("jsToLowerCase(%q) = %q, oracle %q", s, got, want)
			}
			bad++
		}
		if got := historyIndex(s, jsToLowerCase(historyParityString(c.Q))); got != c.Index {
			if badIndex < 5 {
				t.Errorf("historyIndex(%q, lower(%q)) = %d, oracle %d", s, historyParityString(c.Q), got, c.Index)
			}
			badIndex++
		}
	}
	t.Logf("%d cases: jsToLowerCase mismatches %d, historyIndex mismatches %d", len(cases), bad, badIndex)
}

// Differential: prefilterNeedles over 20,000 random lowercased queries.
func TestHistoryParityPrefilterDifferential(t *testing.T) {
	var cases []struct {
		Q       []int
		Needles [][]int
	}
	historyParityLoad(t, "prefilter.json", &cases)
	bad := 0
	for _, c := range cases {
		q := historyParityString(c.Q)
		got, ok := prefilterNeedles(q)
		want := []string(nil)
		for _, n := range c.Needles {
			want = append(want, historyParityString(n))
		}
		if ok != (c.Needles != nil) || fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
			if bad < 5 {
				t.Errorf("prefilterNeedles(%q) = %q %v, oracle %q", q, got, ok, want)
			}
			bad++
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

// Differential: needleLines(...).test over Latin-1 byte windows (ASCII needles: k/s folds, their Latin-1 case
// variants, dotted I) and UTF-8 text windows (non-ASCII needles: the (?i) regexp and its anchors).
func TestHistoryParityMatcherDifferential(t *testing.T) {
	var cases []struct {
		Needles [][]int
		Window  []int
		Latin1  bool
		Test    bool
	}
	historyParityLoad(t, "matchers.json", &cases)
	bad := 0
	for _, c := range cases {
		needles := make([]string, len(c.Needles))
		for i, n := range c.Needles {
			needles[i] = historyParityString(n)
		}
		m := needleLines(needles)
		var window string
		if c.Latin1 {
			b := make([]byte, len(c.Window))
			for i, v := range c.Window {
				b[i] = byte(v)
			}
			window = string(b)
		} else {
			window = historyParityString(c.Window)
		}
		if (m.encoding == "latin1") != c.Latin1 || m.test(window) != c.Test {
			if bad < 5 {
				t.Errorf("needleLines(%q) %s test(%q) = %v, oracle %v (latin1 %v)", needles, m.encoding, window, m.test(window), c.Test, c.Latin1)
			}
			bad++
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

// Differential: Buffer.from(s, "base64url") over 20,000 strings mixing both alphabets, padding and junk.
func TestHistoryParityBase64Differential(t *testing.T) {
	var cases []struct {
		S     string
		Bytes []int
	}
	historyParityLoad(t, "base64.json", &cases)
	bad := 0
	for _, c := range cases {
		got := decodeBase64URL(c.S)
		want := make([]byte, len(c.Bytes))
		for i, v := range c.Bytes {
			want[i] = byte(v)
		}
		if string(got) != string(want) {
			if bad < 5 {
				t.Errorf("decodeBase64URL(%q) = %v, oracle %v", c.S, got, want)
			}
			bad++
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

// Differential: decodeCursor (JSON.parse of the decoded payload) over crafted tuples in three encodings.
func TestHistoryParityCursorDifferential(t *testing.T) {
	var cases []struct {
		Encoded  string
		OK       bool
		ID       []int
		Priority int
		Offset   float64
	}
	historyParityLoad(t, "cursors.json", &cases)
	bad := 0
	for _, c := range cases {
		got, ok := decodeCursor(c.Encoded, "KEY")
		same := ok == c.OK
		if same && ok && got != nil {
			same = got.id == historyParityString(c.ID) && got.priority == c.Priority && float64(got.offset) == c.Offset
		}
		if !same {
			t.Errorf("decodeCursor(%q) = %+v %v, oracle ok=%v id=%q priority=%d offset=%v", c.Encoded, got, ok, c.OK, historyParityString(c.ID), c.Priority, c.Offset)
			bad++
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

// Differential: parseSessionLine (JSON.parse) on lines a Go decoder may read differently.
func TestHistoryParityParseSessionLineDifferential(t *testing.T) {
	var cases []struct {
		Line []int
		OK   bool
		ID   []int
		Role *string
	}
	historyParityLoad(t, "lines.json", &cases)
	for _, c := range cases {
		line := historyParityString(c.Line)
		entry, ok := parseSessionLine(line)
		show := line
		if len(show) > 80 {
			show = show[:40] + "…" + show[len(show)-30:]
		}
		if ok != c.OK {
			t.Errorf("parseSessionLine(%q) ok = %v, oracle %v", show, ok, c.OK)
			continue
		}
		if !ok {
			continue
		}
		id, hasID := str(entry, "id")
		if hasID != (c.ID != nil) || id != historyParityString(c.ID) {
			t.Errorf("parseSessionLine(%q) id = %q, oracle %q", show, id, historyParityString(c.ID))
		}
		message, _ := recOf(entry["message"])
		role, hasRole := str(message, "role")
		if hasRole != (c.Role != nil) || (hasRole && role != *c.Role) {
			t.Errorf("parseSessionLine(%q) role = %q, oracle %v", show, role, c.Role)
		}
	}
}

// Differential: historyHit (lowercasing, excerpt index, header, file-qualified id) over random user messages.
func TestHistoryParityHitDifferential(t *testing.T) {
	var cases []struct {
		Text, Q []int
		Source  string
		Hit     *struct {
			ID           string
			Priority     int
			Text         []int
			HeaderLength int
		}
	}
	historyParityLoad(t, "hits.json", &cases)
	bad := 0
	for _, c := range cases {
		entry := rec{"type": "message", "id": "e1", "timestamp": "t", "message": rec{"role": "user", "content": historyParityString(c.Text)}}
		item, ok := toWindowedEntry(entry, "initial")
		if !ok {
			t.Fatalf("toWindowedEntry(%q) undefined", historyParityString(c.Text))
		}
		q := historyParityString(c.Q)
		source := c.Source
		if c.Hit == nil {
			source = ""
		}
		hit, found := newHistoryHit(item, q, source)
		same := found == (c.Hit != nil)
		if same && found {
			same = hit.id == c.Hit.ID && hit.priority == c.Hit.Priority && hit.text == historyParityString(c.Hit.Text) && hit.headerLength == c.Hit.HeaderLength
		}
		if !same {
			if bad < 5 {
				t.Errorf("historyHit(%q, %q) = %+v %v, oracle %+v", item.text, q, hit, found, c.Hit)
			}
			bad++
		}
	}
	t.Logf("%d cases, %d mismatches", len(cases), bad)
}

// DIVERGENCE: session-files.ts:279 (readdir) and 301 (stat) reject with Node fs errors, which reach the model as
// the history tool's error text. Oracle (node, probe.mts G): "EACCES: permission denied, scandir '<dir>/locked'" and
// "ENOENT: no such file or directory, stat '<path>'". sessionFiles and newestFirst return the raw Go errors.
func TestHistoryParitySessionFilesErrorText(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	if _, err := sessionFiles(dir); err == nil || err.Error() != "EACCES: permission denied, scandir '"+locked+"'" {
		t.Errorf("sessionFiles error = %v, oracle %q", err, "EACCES: permission denied, scandir '"+locked+"'")
	}
	missing := filepath.Join(dir, "missing.jsonl")
	if _, err := newestFirst([]string{missing}); err == nil || err.Error() != "ENOENT: no such file or directory, stat '"+missing+"'" {
		t.Errorf("newestFirst error = %v, oracle %q", err, "ENOENT: no such file or directory, stat '"+missing+"'")
	}
}

// DIVERGENCE: history-pages.ts:124,132 print the JS numbers of params.offset/params.imageOffset (`${offset}`). TypeBox
// Integer accepts 1e20 and 2^60; historyParamsOf truncates them to int (jsToInt clamps 1e20 to MaxInt) and readOffsets
// prints %d. Oracle (node): `${1e20}` = "100000000000000000000", `${2 ** 60}` = "1152921504606847000",
// `${1e21}` = "1e+21". Same class as notestools N5 (fixed there with jsNumberString).
func TestHistoryParityReadOffsetNumberText(t *testing.T) {
	item, ok := toWindowedEntry(rec{"type": "message", "id": "e1", "message": rec{"role": "user", "content": "hi"}}, "initial")
	if !ok {
		t.Fatal("no item")
	}
	total := u16len(item.text)
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"op": "read", "id": "e1", "offset": 1e20}, fmt.Sprintf(`Offset 100000000000000000000 is past the end of history entry "e1" (%d chars).`, total)},
		{map[string]any{"op": "read", "id": "e1", "offset": float64(1 << 60)}, fmt.Sprintf(`Offset 1152921504606847000 is past the end of history entry "e1" (%d chars).`, total)},
		{map[string]any{"op": "read", "id": "e1", "imageOffset": 1e21}, `Image offset 1e+21 is past the end of history entry "e1" (0 images).`},
	} {
		_, _, err := readOffsets(item, historyParamsOf(c.args), total)
		if err == nil || err.Error() != c.want {
			t.Errorf("readOffsets(%v) = %v, oracle %q", c.args, err, c.want)
		}
	}
}

// Differential: the (?i) regexp of needleLines' non-ASCII path against JavaScript's /iu over every code point with a
// case mapping (the equivalence classes the prefilter relies on, including stableAnchor's fold-stable characters).
func TestHistoryParityFoldClassDifferential(t *testing.T) {
	var cases []struct {
		C       rune
		Matched []rune
	}
	historyParityLoad(t, "folds.json", &cases)
	inK := make(map[rune]bool, len(cases))
	for _, c := range cases {
		inK[c.C] = true
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if f := unicode.SimpleFold(r); f != r && !inK[r] {
			t.Errorf("U+%04X folds (to U+%04X) but has no JavaScript case mapping: not covered", r, f)
		}
	}
	bad := 0
	for _, c := range cases {
		re := regexp.MustCompile("(?i)^" + regexp.QuoteMeta(string(c.C)) + "$")
		var got []rune
		for _, d := range cases {
			if re.MatchString(string(d.C)) {
				got = append(got, d.C)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(c.Matched) {
			if bad < 10 {
				t.Errorf("(?i)U+%04X matches %U, /iu matches %U", c.C, got, c.Matched)
			}
			bad++
		}
		stable := foldStable(c.C)
		jsStable := true
		for _, d := range c.Matched {
			if d != c.C && (d >= utf8.RuneSelf || c.C >= utf8.RuneSelf) {
				jsStable = false
			}
		}
		if stable && !jsStable {
			t.Errorf("foldStable(U+%04X) but /iu matches %U", c.C, c.Matched)
		}
	}
	t.Logf("%d cased code points, %d class mismatches", len(cases), bad)
}

// Equal (oracle values from node): the search scope key and an emitted cursor for queries with lone surrogates,
// astral characters and U+2028 / HTML characters (JSON.stringify escapes, base64url without padding).
func TestHistoryParityCursorKeys(t *testing.T) {
	for _, c := range []struct{ q, key, cursor string }{
		{"\xed\xa0\xbd", "wRh3qGx9nK", "WyJcdWQ4M2RAeCIsMCwzLCJ3UmgzcUd4OW5LIl0"},
		{"a\xed\xa0\xbdx", "eK7tktNHc1", "WyJhXHVkODNkeEB4IiwwLDMsImVLN3RrdE5IYzEiXQ"},
		{"é😀", "2NCGk_2eqa", "WyLDqfCfmIBAeCIsMCwzLCIyTkNHa18yZXFhIl0"},
		{"\u2028<>&", "bFIBsN7Btz", "WyLigKg8PiZAeCIsMCwzLCJiRklCc043QnR6Il0"},
	} {
		encoded, err := jsStringify([]any{c.q, true})
		if err != nil {
			t.Fatal(err)
		}
		key := shortKey(encoded)
		cursor, err := cursorFor(historyMatchSet{key: key}, historyHit{id: c.q + "@x", priority: 0}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if key != c.key || cursor != c.cursor {
			t.Errorf("query %q: key %s cursor %s, oracle %s %s", c.q, key, cursor, c.key, c.cursor)
		}
	}
}
