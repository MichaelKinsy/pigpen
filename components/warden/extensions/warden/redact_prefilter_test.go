package warden

import (
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// TestRedactPrefilterNeverSkipsARedaction: a text the prefilter passes over is one no rule changes. The texts are the
// differential corpus' commands, every literal a rule needs (in both cases), and random mixes of the two.
func TestRedactPrefilterNeverSkipsARedaction(t *testing.T) {
	var texts []string
	raw, err := os.ReadFile("testdata/diff_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus any
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			texts = append(texts, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(corpus)
	fragments := append(append([]string{}, credentialLiterals...), credentialLiteralsExact...)
	fragments = append(fragments, "://", "@", "=", ":", " ", "\n", "\"", "'", "AUTHORIZATION", "Bearer", "API_KEY", "PassWord", "TOKEN", "Secret", "x", "abcdefghij0123456789", "user:pw")
	rng := rand.New(rand.NewSource(7))
	for range 4000 {
		var b strings.Builder
		for range rng.Intn(10) + 1 {
			b.WriteString(fragments[rng.Intn(len(fragments))])
		}
		texts = append(texts, b.String())
	}
	skipped := 0
	for _, text := range texts {
		if mayHaveCredential(text) {
			continue
		}
		skipped++
		if got := redactRules(text); got != text {
			t.Fatalf("the prefilter skipped a text a rule changes: %q -> %q", text, got)
		}
	}
	if skipped == 0 {
		t.Fatal("the prefilter never skipped: the test proves nothing")
	}
}
