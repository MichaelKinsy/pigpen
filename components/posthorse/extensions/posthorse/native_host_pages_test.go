package posthorse

// Layer-1 twin of the native-host.ts:994 case "native page reads estimate current active schemas only when usage is
// unknown" (real Pi in the original; the recorded scenarios cannot count host calls, the fake host can).

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestNativeHost_pageReadsEstimateCurrentActiveSchemasOnlyWhenUsageIsUnknown(t *testing.T) {
	dir := coreReal(t)
	file := filepath.Join(dir, ".pi", "notes", "state.md")
	coreWrite(t, file, "saved state")
	var policyReads atomic.Int32
	source := rec{"path": "<fixture>", "source": "extension", "scope": "temporary", "origin": "top-level"}
	tool := func(name, description string) rec {
		return rec{"name": name, "description": description, "parameters": rec{}, "exposure": "direct", "sourceInfo": source}
	}
	session := fxIndexedSession(fxMessageEntry("saved-id", fxUser("saved state")))
	f := newFixture(t, withCwd(dir), withSession(session), withSystemPrompt("Offline page regression."), withActiveTools("notes", "history"),
		withPolicyReader(func(sdk.Context) (compactionPolicy, error) {
			policyReads.Add(1)
			return compactionPolicy{enabled: true, reserveTokens: 16_384}, nil
		}),
		// the original's `catalog` tool: a 280,000-character description
		withHostAnswer("getAllTools", func(map[string]any) (any, string) {
			return rec{"tools": []any{tool("new_context", ""), tool("get_context_remaining", ""), tool("notes", ""), tool("history", ""),
				tool("catalog", strings.Repeat("d", 280_000))}}, ""
		}))
	type counts struct{ active, all, usage, policy int }
	var got, want []counts
	// call runs a notes read and a history read under one usage state; every evaluation owns its counters.
	call := func(usage fixOpt, unknown, defined bool) (notes, history fixResult) {
		t.Helper()
		f.Set(usage)
		for _, run := range []func() fixResult{
			func() fixResult { return f.Run("notes", rec{"op": "read", "path": "state.md"}) },
			func() fixResult { return f.Run("history", rec{"op": "read", "id": "saved-id"}) },
		} {
			// the original emits turn_start before each call: a new turn releases the page reservations
			f.Fire("turn_start", rec{"type": "turn_start", "turnIndex": 0, "timestamp": float64(1)})
			f.ResetReads()
			policyReads.Store(0)
			res := run()
			if notes.Content == nil {
				notes = res
			} else {
				history = res
			}
			got = append(got, counts{f.Reads("getActiveTools"), f.Reads("getAllTools"), f.Reads("getContextUsage"), int(policyReads.Load())})
			active, policy := 0, 0
			if unknown {
				active = 1
			}
			if defined {
				policy = 1
			}
			want = append(want, counts{active, active, 1, policy})
		}
		return notes, history
	}
	baseNotes, baseHistory := call(withUsage(100_000, 1000), false, true)
	if baseNotes.Text() != "File: "+file+"\nsaved state" {
		t.Fatalf("notes read %q", baseNotes.Text())
	}
	if !strings.HasSuffix(baseHistory.Text(), " [window initial] [saved-id] [chars 0-18 of 18] [user] saved state") {
		t.Fatalf("history read %q", baseHistory.Text())
	}
	for _, c := range []struct {
		name             string
		usage            fixOpt
		unknown, defined bool
	}{
		{"zero tokens", withUsage(100_000, 0), false, true},
		{"null tokens", withUsageUnknown(100_000), true, true},
		{"no usage", withNoUsage(), true, false},
	} {
		n, h := call(c.usage, c.unknown, c.defined)
		if n.Text() != baseNotes.Text() || h.Text() != baseHistory.Text() {
			t.Fatalf("%s: results differ from the baseline:\n%q\n%q", c.name, n.Text(), h.Text())
		}
	}
	// a 50,000-character body: more active schemas reduce the unknown-usage capacity
	coreWrite(t, file, strings.Repeat("n", 50_000))
	f.Set(withActiveTools("notes", "history"))

	small, _ := call(withUsageUnknown(100_000), true, true)
	f.Set(withActiveTools("notes", "history", "catalog"))
	large, largeHistory := call(withUsageUnknown(100_000), true, true)
	smallEnd, _ := num(small.Details, "end")
	largeEnd, _ := num(large.Details, "end")
	if largeEnd >= smallEnd {
		t.Fatalf("new active schemas must reduce the unknown-usage capacity: end %v vs %v", largeEnd, smallEnd)
	}
	end := strconv.Itoa(int(largeEnd))
	if text, want := large.Text(), "File: "+file+"\n"+strings.Repeat("n", int(largeEnd))+"\n[chars 0-"+end+" of 50000; continue with offset "+end+"]"; text != want {
		t.Fatalf("large read text differs (%d chars, want %d)", len(text), len(want))
	}
	if largeHistory.Text() != baseHistory.Text() {
		t.Fatalf("history read changed with the active schemas: %q", largeHistory.Text())
	}
	f.Set(withActiveTools("notes", "history"))
	again, _ := call(withUsageUnknown(100_000), true, true)
	if e, _ := num(again.Details, "end"); e != smallEnd {
		t.Fatalf("later loadouts are estimated afresh, not cached: end %v want %v", e, smallEnd)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("evaluation %d: active/all/usage/policy reads %v, want %v (only unknown usage estimates the active tools)", i, got[i], want[i])
		}
	}
}
