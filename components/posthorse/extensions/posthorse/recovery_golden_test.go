package posthorse

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Replays testdata/recovery_golden.json.gz: scenarios for buildAutoHandoff recorded from the unmodified
// original (port/tools/recovery-golden.mjs). Each scenario carries the output's UTF-16 length and SHA-256,
// and the text itself when it is short.

type rcGoldenScenario struct {
	Name      string   `json:"name"`
	Entries   []any    `json:"entries"`
	Projected any      `json:"projected"`
	MaxChars  int      `json:"maxChars"`
	OwnerQ    bool     `json:"ownerQ"`
	Retained  []string `json:"retained"`
	Length    int      `json:"length"`
	Sha256    string   `json:"sha256"`
	Expected  *string  `json:"expected"`
}

// rcExpand resolves the {"$rep":[unit,count]} and {"$cat":[parts]} markers of the generator.
func rcExpand(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = rcExpand(x[i])
		}
		return out
	case map[string]any:
		if rep, ok := x["$rep"].([]any); ok {
			return strings.Repeat(rep[0].(string), int(rep[1].(float64)))
		}
		if cat, ok := x["$cat"].([]any); ok {
			var b strings.Builder
			for _, part := range cat {
				b.WriteString(rcExpand(part).(string))
			}
			return b.String()
		}
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = rcExpand(val)
		}
		return out
	}
	return v
}

func rcLoadGolden(t testing.TB) []rcGoldenScenario {
	t.Helper()
	raw, err := os.ReadFile("testdata/recovery_golden.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scenarios []rcGoldenScenario `json:"scenarios"`
	}
	if err := json.NewDecoder(zr).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Scenarios
}

func (sc *rcGoldenScenario) run(t testing.TB) string {
	t.Helper()
	entries := make([]rec, len(sc.Entries))
	for i, e := range sc.Entries {
		entries[i] = rcExpand(e).(map[string]any)
	}
	var projected []projectedEntry
	if list, ok := sc.Projected.([]any); ok {
		for _, item := range list {
			p := item.(map[string]any)
			source := entries[int(p["source"].(float64))]
			pe := projectedEntry{sourceEntry: source, messages: []rec{}}
			if s, ok := p["messages"].(string); ok && s == "same" {
				pe.messages = []rec{source["message"].(map[string]any)}
			} else {
				for _, m := range rcExpand(p["messages"]).([]any) {
					pe.messages = append(pe.messages, m.(map[string]any))
				}
			}
			projected = append(projected, pe)
		}
	} else {
		projected = rcProjection(entries)
	}
	retained := make(map[string]struct{}, len(sc.Retained))
	for _, id := range sc.Retained {
		retained[id] = struct{}{}
	}
	out, err := buildAutoHandoff(entries, projected, sc.MaxChars, autoHandoffOptions{ownerQuestionRegistered: sc.OwnerQ, retainedEntryIds: retained})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecoveryGoldenMatchesOracle(t *testing.T) {
	scenarios := rcLoadGolden(t)
	if len(scenarios) == 0 {
		t.Fatal("no golden scenarios")
	}
	for i := range scenarios {
		sc := &scenarios[i]
		t.Run(sc.Name, func(t *testing.T) {
			out := sc.run(t)
			if dir := os.Getenv("RC_GOLDEN_DUMP"); dir != "" {
				if err := os.WriteFile(dir+"/"+sc.Name+".txt", []byte(out), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			sum := sha256.Sum256(jsUTF8Bytes(out))
			if u16len(out) == sc.Length && hex.EncodeToString(sum[:]) == sc.Sha256 {
				return
			}
			if sc.Expected != nil {
				t.Fatalf("output differs from the original (len %d, want %d)\n--- got\n%s\n--- want\n%s", u16len(out), sc.Length, out, *sc.Expected)
			}
			t.Fatalf("output differs from the original: length %d, want %d; sha256 %s, want %s", u16len(out), sc.Length, hex.EncodeToString(sum[:]), sc.Sha256)
		})
	}
}
