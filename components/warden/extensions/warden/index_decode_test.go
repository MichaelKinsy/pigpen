package warden

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Go-only: the one-pass reader of an index file (index_decode.go) is held to the generic decoder it sits in front of.

var indexTextPieces = []string{"plain", "a \"quoted\" word", "back\\slash", "tab\there", "new\nline", "\r", "\x00", "\x1f", "\x7f", "é", "日本語", "😀", "\u2028", "</script>", "a & b", "\ufeff", "/", "\\u0041", "[]", "{}", "null", "true", "1", ""}

func randomIndexText(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.IntN(4) {
		b.WriteString(indexTextPieces[rng.IntN(len(indexTextPieces))])
	}
	return b.String()
}

func randomIndexList(rng *rand.Rand) []string {
	switch rng.IntN(4) {
	case 0:
		return nil
	case 1:
		return []string{}
	}
	out := make([]string, rng.IntN(4)+1)
	for i := range out {
		out[i] = randomIndexText(rng)
	}
	return out
}

func randomIndexFile(rng *rand.Rand) *IndexFile {
	f := &IndexFile{FormatVersion: 1, BuiltAt: randomIndexText(rng), Model: randomIndexText(rng)}
	for range rng.IntN(5) {
		f.Entries = append(f.Entries, IndexEntry{
			Kind: randomIndexText(rng), Name: randomIndexText(rng), Scope: randomIndexText(rng), SourceHash: randomIndexText(rng),
			Role: randomIndexText(rng), Lead: randomIndexText(rng), UseWhen: randomIndexList(rng), NotWhen: randomIndexList(rng),
			Inputs: randomIndexText(rng), Examples: randomIndexList(rng), Thin: rng.IntN(2) == 0, SourceQuality: randomIndexText(rng),
			Improve: randomIndexText(rng), Location: randomIndexText(rng), Truncated: rng.IntN(2) == 0,
		})
	}
	return f
}

// TestDecodeIndexFastEqualsTheGenericDecoder: for the files the writer produces, and for those same files with a byte
// changed, dropped, added or cut off, the fast reader either refuses or gives exactly what the generic route gives.
func TestDecodeIndexFastEqualsTheGenericDecoder(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 1))
	path := filepath.Join(t.TempDir(), "index.json")
	check := func(label, text string) (accepted bool) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		want := readIndexGeneric(path, []byte(text))
		got, ok := decodeIndexFast(nodeUTF8([]byte(text)))
		if !ok {
			return false
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s %q:\nfast    %+v\ngeneric %+v", label, text, got, want)
		}
		return true
	}
	accepted, refused := 0, 0
	for range 1500 {
		f := randomIndexFile(rng)
		for i := range f.Entries {
			f.Entries[i].UseWhen, f.Entries[i].NotWhen, f.Entries[i].Examples = orEmpty(f.Entries[i].UseWhen), orEmpty(f.Entries[i].NotWhen), orEmpty(f.Entries[i].Examples)
		}
		if f.Entries == nil {
			f.Entries = []IndexEntry{}
		}
		text := string(indexFileJSON(f))
		if !check("written", text) {
			t.Fatalf("a file the writer produced was refused: %q", text)
		}
		accepted++
		for range 12 {
			b := []byte(text)
			switch rng.IntN(5) {
			case 0:
				b[rng.IntN(len(b))] = byte(rng.IntN(256))
			case 1:
				i := rng.IntN(len(b))
				b = append(b[:i], b[i+1:]...)
			case 2:
				i := rng.IntN(len(b))
				b = append(b[:i], append([]byte{"\"\\{}[],:0 \n"[rng.IntN(11)]}, b[i:]...)...)
			case 3:
				b = b[:rng.IntN(len(b))]
			case 4:
				i := rng.IntN(len(b))
				b = append(b[:i], append([]byte(`\u00e9\ud83d\n`), b[i:]...)...)
			}
			if check("mutated", string(b)) {
				accepted++
			} else {
				refused++
			}
		}
	}
	// Spellings the reader leaves to the generic decoder, and shapes it must not mistake for the plain one.
	entry := `{"kind":"skill","name":"n","scope":"global","sourceHash":"h","role":"evidence","lead":"l","useWhen":[],"notWhen":[],"inputs":"i","examples":[],"thin":false,"sourceQuality":"strong"}`
	for _, text := range []string{
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":1.0,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":1e0,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":2,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":null}`,
		`{"formatVersion":1,"builtAt":"t","model":"m"}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[],"extra":1}`,
		`{"formatVersion":1,"formatVersion":1,"builtAt":"t","model":"m","entries":[]}`,
		`{"FormatVersion":1,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[]} x`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[]}  `,
		"\ufeff" + `{"formatVersion":1,"builtAt":"t","model":"m","entries":[]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[` + entry + `]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[` + entry + `,` + entry + `]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[` + entry + `,5,"x"]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"kind":"skill","kind":"tool"}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"Kind":"skill"}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"kind":null}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"useWhen":null}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"useWhen":["a",null]}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"thin":1}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"name":"\ud83d\ude00 \ud800"}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"name":"tab` + "\t" + `inside"}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"name":"bad \x escape"}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{"improve":"a","_location":"b","truncated":true}]}`,
		`{"formatVersion":1,"builtAt":"t","model":"m","entries":[{}]}`,
		`[]`, `{}`, ``, ` `, `null`, `{"formatVersion":1,"builtAt":"t","model":"m","entries":[`,
	} {
		check("listed", text)
	}
	if accepted < 3000 || refused < 1000 {
		t.Fatalf("accepted %d, refused %d: the test does not reach both routes", accepted, refused)
	}
}

// TestJSONStringWriterEqualsTheGenericEncoder: every byte that needs an escape, and the ones around it, at every
// offset of the eight-byte steps the scan takes and after strings of every length up to a few steps.
func TestJSONStringWriterEqualsTheGenericEncoder(t *testing.T) {
	specials := []string{"\"", "\\", "\n", "\t", "\r", "\b", "\f", "\x00", "\x01", "\x1f", "\x7f", "\x20", "é", "😀", "\u2028", "<", "&", "/"}
	for _, special := range specials {
		for before := range 26 {
			for _, after := range []int{0, 1, 7, 8, 9, 17} {
				s := strings.Repeat("a", before) + special + strings.Repeat("b", after) + special
				w := jsonOut{}
				w.str(s)
				want := strings.ReplaceAll(jsonCompact(s), `\u2028`, "\u2028")
				if string(w.b) != want {
					t.Fatalf("%q: %s, want %s", s, w.b, want)
				}
			}
		}
	}
	for _, s := range []string{"", "a", strings.Repeat("a", 4096), strings.Repeat("\"", 100), "\xff\xfe invalid"} {
		w := jsonOut{}
		w.str(s)
		if string(w.b) != strings.ReplaceAll(jsonCompact(s), `\u2028`, "\u2028") && utf8.ValidString(s) {
			t.Fatalf("%q: %s", s, w.b)
		}
	}
}
