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
