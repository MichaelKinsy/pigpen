package posthorse

// Differential test of automaticRecovery (retention.go) against the oracle's src/retention.ts: the
// cases in testdata/retention_recovery_cases.json are seeded random sessions (an optional Posthorse or
// native boundary, user, assistant, tool-call and tool-result messages, custom messages, bash runs,
// entries that project nothing) with the answer the unmodified TypeScript gave, run under Node 24
// against Pi 1.5.0. They cover a prepared firstKeptEntryId, the native cut finder, the paired-tail
// check, the half-budget split, and the "no room" answer.

import (
	"encoding/json"
	"os"
	"testing"
)

type recoveryCase struct {
	Branch    []rec `json:"branch"`
	Projected []struct {
		SourceEntry rec   `json:"sourceEntry"`
		Messages    []rec `json:"messages"`
	} `json:"projected"`
	Owner    bool `json:"owner"`
	Settings struct {
		Enabled          bool `json:"enabled"`
		ReserveTokens    int  `json:"reserveTokens"`
		KeepRecentTokens *int `json:"keepRecentTokens"`
	} `json:"settings"`
	ContextWindow    int     `json:"contextWindow"`
	SystemPrompt     string  `json:"systemPrompt"`
	ToolTokens       int     `json:"toolTokens"`
	PendingMessages  []rec   `json:"pendingMessages"`
	FirstKeptEntryID *string `json:"firstKeptEntryId"`
	Result           *struct {
		Handoff          string  `json:"handoff"`
		FirstKeptEntryID *string `json:"firstKeptEntryId"`
	} `json:"result"`
}

func TestAutomaticRecoveryMatchesOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/retention_recovery_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []recoveryCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	var nilResults, withFirstKept int
	for i, c := range cases {
		projected := make([]projectedEntry, len(c.Projected))
		for j, p := range c.Projected {
			projected[j] = projectedEntry{sourceEntry: p.SourceEntry, messages: p.Messages}
		}
		capacity := recoveryCapacity{
			host: roPolicy{
				settings: compactionPolicy{enabled: c.Settings.Enabled, reserveTokens: c.Settings.ReserveTokens, keepRecentTokens: c.Settings.KeepRecentTokens},
				usage:    &contextUsage{contextWindow: c.ContextWindow},
				prompt:   c.SystemPrompt,
			},
			toolTokens:      c.ToolTokens,
			pendingMessages: c.PendingMessages,
		}
		if c.FirstKeptEntryID != nil {
			capacity.firstKeptEntryID, capacity.hasFirstKept = *c.FirstKeptEntryID, true
		}
		got, err := automaticRecovery(c.Branch, projected, capacity, c.Owner)
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		switch {
		case c.Result == nil:
			nilResults++
			if got != nil {
				t.Errorf("case %d: the oracle found no room for a record, got a %d-character handoff", i, len(got.handoff))
			}
		case got == nil:
			t.Errorf("case %d: the oracle recovered %d characters, got none", i, len(c.Result.Handoff))
		default:
			if got.handoff != c.Result.Handoff {
				t.Errorf("case %d: handoff differs:\n got %q\nwant %q", i, got.handoff, c.Result.Handoff)
			}
			switch {
			case c.Result.FirstKeptEntryID == nil && got.hasFirstKept:
				t.Errorf("case %d: firstKeptEntryId = %q, oracle has none", i, got.firstKeptEntryID)
			case c.Result.FirstKeptEntryID != nil && (!got.hasFirstKept || got.firstKeptEntryID != *c.Result.FirstKeptEntryID):
				t.Errorf("case %d: firstKeptEntryId = %q (%v), oracle %q", i, got.firstKeptEntryID, got.hasFirstKept, *c.Result.FirstKeptEntryID)
			}
			if c.Result.FirstKeptEntryID != nil {
				withFirstKept++
			}
		}
	}
	if nilResults == 0 || withFirstKept == 0 {
		t.Errorf("the cases must cover both a missing record (%d) and a retained tail (%d)", nilResults, withFirstKept)
	}
}
