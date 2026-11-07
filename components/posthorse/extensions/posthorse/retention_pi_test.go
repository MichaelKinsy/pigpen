package posthorse

// The SDK's EstimateTokens, FindCutPoint and DefaultCompactionSettings stand in for Pi's
// estimateTokens, findCutPoint and DEFAULT_COMPACTION_SETTINGS in retention.go. These tests compare
// them with Pi 1.5.0's own answers on generated inputs. testdata/retention_pi_cases.json was made by
// running Pi 1.5.0 (the oracle host's @earendil-works/pi-coding-agent) over seeded random messages and
// session entries of every kind the cut logic reads (user, assistant, toolResult, custom,
// bashExecution, branchSummary, compactionSummary, system, an unknown role, null content, astral and
// accented text, images, tool-call arguments with nested JSON, compaction entries with a
// systemMessage), and recording what it returned.

import (
	"encoding/json"
	"os"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type piCases struct {
	Defaults struct {
		Enabled          bool `json:"enabled"`
		ReserveTokens    int  `json:"reserveTokens"`
		KeepRecentTokens int  `json:"keepRecentTokens"`
	} `json:"defaults"`
	Estimate []struct {
		Message rec `json:"message"`
		Tokens  int `json:"tokens"`
	} `json:"estimate"`
	Cut []struct {
		Entries []rec `json:"entries"`
		Start   int   `json:"start"`
		End     int   `json:"end"`
		Keep    int   `json:"keep"`
		Result  struct {
			FirstKeptEntryIndex int  `json:"firstKeptEntryIndex"`
			TurnStartIndex      int  `json:"turnStartIndex"`
			IsSplitTurn         bool `json:"isSplitTurn"`
		} `json:"result"`
	} `json:"cut"`
}

func loadPiCases(t *testing.T) piCases {
	t.Helper()
	raw, err := os.ReadFile("testdata/retention_pi_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases piCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestRetentionDefaultCompactionSettingsMatchPi(t *testing.T) {
	cases := loadPiCases(t)
	got := sdk.DefaultCompactionSettings
	if got.Enabled != cases.Defaults.Enabled || got.ReserveTokens != cases.Defaults.ReserveTokens || got.KeepRecentTokens != cases.Defaults.KeepRecentTokens {
		t.Fatalf("DefaultCompactionSettings = %+v, Pi 1.5.0 = %+v", got, cases.Defaults)
	}
}

func TestRetentionEstimateTokensMatchesPi(t *testing.T) {
	for i, c := range loadPiCases(t).Estimate {
		if got := sdk.EstimateTokens(c.Message); got != c.Tokens {
			t.Errorf("case %d %v: EstimateTokens = %d, Pi 1.5.0 = %d", i, c.Message, got, c.Tokens)
		}
		// messageCost is the retention.go cost of a projected message.
		if got := messageCost(c.Message); got != c.Tokens {
			t.Errorf("case %d: messageCost = %d, Pi 1.5.0 = %d", i, got, c.Tokens)
		}
	}
}

func TestRetentionFindCutPointMatchesPi(t *testing.T) {
	for i, c := range loadPiCases(t).Cut {
		got := sdk.FindCutPoint(c.Entries, c.Start, c.End, c.Keep)
		if got.FirstKeptEntryIndex != c.Result.FirstKeptEntryIndex || got.TurnStartIndex != c.Result.TurnStartIndex || got.IsSplitTurn != c.Result.IsSplitTurn {
			t.Errorf("case %d (keep %d, start %d, %d entries): FindCutPoint = %+v, Pi 1.5.0 = %+v", i, c.Keep, c.Start, len(c.Entries), got, c.Result)
		}
	}
}
