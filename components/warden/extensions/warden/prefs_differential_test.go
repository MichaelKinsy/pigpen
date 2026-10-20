package warden

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The preference detectors run through the ORIGINAL pi-warden (port/gen-prefs-corpus.ts against the pinned oracle)
// and through this port on the same messages. RE2 has no lookahead, lookbehind or backreference, so the rewritten
// patterns are exactly what this guards: isHumanTyped, the clauses and permissions of a message, the class of a
// clause and its tokens.

type prefsCorpusItem struct {
	Text        string     `json:"text"`
	Human       bool       `json:"human"`
	Clauses     []string   `json:"clauses"`
	Permissions []string   `json:"permissions"`
	Classes     []string   `json:"classes"`
	Tokens      [][]string `json:"tokens"`
}

type prefsCorpus struct {
	Items []prefsCorpusItem `json:"items"`
	Raws  []struct {
		Text   string   `json:"text"`
		Class  string   `json:"cls"`
		Tokens []string `json:"tokens"`
	} `json:"raws"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestPrefsDifferentialAgainstThePinnedOriginal(t *testing.T) {
	raw, err := os.ReadFile("testdata/prefs_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus prefsCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Items) < 2000 {
		t.Fatalf("corpus too small: %d", len(corpus.Items))
	}
	bad := 0
	report := func(kind, text string, got, want any) {
		bad++
		if bad <= 40 {
			t.Errorf("%s differs for %q\n  port:     %#v\n  original: %#v", kind, text, got, want)
		}
	}
	for _, it := range corpus.Items {
		if got := isHumanTyped(it.Text); got != it.Human {
			report("isHumanTyped", it.Text, got, it.Human)
		}
		clauses, permissions := nonNil(extractPreferences(it.Text)), nonNil(extractPermissions(it.Text))
		if !reflect.DeepEqual(clauses, nonNil(it.Clauses)) {
			report("extractPreferences", it.Text, clauses, it.Clauses)
		}
		if !reflect.DeepEqual(permissions, nonNil(it.Permissions)) {
			report("extractPermissions", it.Text, permissions, it.Permissions)
		}
		for i, clause := range append(append([]string(nil), it.Clauses...), it.Permissions...) {
			if i < len(it.Classes) && string(classifyClause(clause)) != it.Classes[i] {
				report("classifyClause", clause, string(classifyClause(clause)), it.Classes[i])
			}
			if i < len(it.Tokens) && !reflect.DeepEqual(nonNil(tokens(clause)), nonNil(it.Tokens[i])) {
				report("tokens", clause, tokens(clause), it.Tokens[i])
			}
		}
	}
	for _, r := range corpus.Raws {
		if got := string(classifyClause(r.Text)); got != r.Class {
			report("classifyClause (raw)", r.Text, got, r.Class)
		}
		if got := nonNil(tokens(r.Text)); !reflect.DeepEqual(got, nonNil(r.Tokens)) {
			report("tokens (raw)", r.Text, got, r.Tokens)
		}
	}
	t.Logf("%d messages and %d raw texts compared", len(corpus.Items), len(corpus.Raws))
	if bad > 0 {
		t.Fatalf("%d differences", bad)
	}
}

// redactFast skips Redact when no trigger of any rule is present: it must equal Redact on every text, the corpus of
// the original's own commands and messages included.
func TestRedactFastEqualsRedact(t *testing.T) {
	var texts []string
	for _, f := range []string{"testdata/diff_corpus.json", "testdata/prefs_corpus.json"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var items []struct {
			Command string `json:"command"`
			Text    string `json:"text"`
		}
		if f == "testdata/prefs_corpus.json" {
			var c prefsCorpus
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			for _, it := range c.Items {
				texts = append(texts, it.Text)
				texts = append(texts, it.Clauses...)
			}
			continue
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			texts = append(texts, it.Command)
		}
	}
	texts = append(texts,
		"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----", "Authorization: Bearer abcdef", "curl -H 'authorization=basic abc'",
		"export API_KEY=sk-abcdefgh12345678", "password=ab&cd1 next", "https://user:pass@example.com/x", "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_abcdefghijklmnopqrstuvwxyz", "AKIAABCDEFGHIJKLMNOP", "xoxb-1234567890-abcdef", "AIzaSyA-abcdefghijklmnopqrstuvwxyz012345",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop", "client_secret: 'a b c'", "PASSPHRASE=hunter2", "Token: abc", "nothing here")
	for _, s := range texts {
		if got, want := redactFast(s), Redact(s); got != want {
			t.Fatalf("redactFast(%q) = %q, Redact = %q", s, got, want)
		}
	}
	t.Logf("%d texts compared", len(texts))
}
