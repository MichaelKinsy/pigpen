package pitypesafe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(day, hour int) time.Time {
	return time.Date(2026, time.January, day, hour, 0, 0, 0, time.Local)
}

func TestUsage(t *testing.T) {
	tw(t, "usage", "a ledger counts requests, tokens, and failures, and prices input tokens only", func(t *testing.T) {
		l := OpenUsageLedger(LedgerOptions{Path: filepath.Join(t.TempDir(), "counts.json"), Now: func() time.Time { return at(1, 12) }})
		if got := l.Today(); got != (UsageReport{Day: "2026-01-01"}) {
			t.Fatalf("empty ledger = %+v", got)
		}
		l.RecordStart()
		l.RecordSuccess(42, 7)
		l.RecordStart()
		l.RecordFailure()
		today := l.Today()
		if today.RequestsStarted != 2 || today.RequestsSucceeded != 1 || today.RequestsFailed != 1 || today.InputTokens != 42 || today.OutputTokens != 7 || today.Day != "2026-01-01" {
			t.Fatalf("totals = %+v", today)
		}
		// Output tokens are free; only input tokens carry a price.
		if today.EstimatedUSD != EstimateUSD(42, DefaultUSDPerMTok) {
			t.Errorf("estimate = %v", today.EstimatedUSD)
		}
		d := l.Describe(SpendCaps{})
		if !contains(d, "2 requests today (1 ok, 1 failed)") || !contains(d, "~$0.0000") {
			t.Errorf("describe = %q", d)
		}
	})
	tw(t, "usage", "totals survive a new ledger instance, so a restart does not reset the day", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "persist.json")
		now := func() time.Time { return at(2, 12) }
		first := OpenUsageLedger(LedgerOptions{Path: path, Now: now})
		first.RecordStart()
		first.RecordSuccess(1000, 0)
		second := OpenUsageLedger(LedgerOptions{Path: path, Now: now})
		if got := second.Today(); got.RequestsStarted != 1 || got.InputTokens != 1000 || got.EstimatedUSD != EstimateUSD(1000, DefaultUSDPerMTok) {
			t.Fatalf("reopened = %+v", got)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", info, err)
		}
	})
	tw(t, "usage", "the day rolls over on the local date and old days are kept", func(t *testing.T) {
		day := 3
		path := filepath.Join(t.TempDir(), "rollover.json")
		l := OpenUsageLedger(LedgerOptions{Path: path, Now: func() time.Time { return at(day, 12) }})
		l.RecordStart()
		l.RecordSuccess(500, 0)
		day = 4
		if got := l.Today(); got.Day != "2026-01-04" || got.RequestsStarted != 0 || got.InputTokens != 0 {
			t.Fatalf("after rollover = %+v", got)
		}
		l.RecordStart()
		var file struct {
			Days map[string]UsageTotals `json:"days"`
		}
		data, _ := os.ReadFile(path)
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatal(err)
		}
		if file.Days["2026-01-03"].InputTokens != 500 || file.Days["2026-01-04"].RequestsStarted != 1 {
			t.Fatalf("file = %s", data)
		}
	})
	tw(t, "usage", "each cap stops the next request and names itself", func(t *testing.T) {
		dir := t.TempDir()
		now := func() time.Time { return at(5, 12) }
		counting := OpenUsageLedger(LedgerOptions{Path: filepath.Join(dir, "r.json"), Now: now})
		requests := SpendCaps{MaxRequestsPerDay: 1}
		if counting.Blocked(requests) != nil {
			t.Fatal("blocked before any request")
		}
		counting.RecordStart()
		if got := counting.Blocked(requests); got == nil || *got != (BlockedCap{Cap: CapRequestsPerDay, Limit: 1, Used: 1, Day: "2026-01-05"}) {
			t.Fatalf("blocked = %+v", got)
		}
		tokens := OpenUsageLedger(LedgerOptions{Path: filepath.Join(dir, "t.json"), Now: now})
		tokens.RecordSuccess(100, 0)
		if got := tokens.Blocked(SpendCaps{MaxInputTokensPerDay: 100}); got == nil || got.Cap != CapInputTokensPerDay {
			t.Fatalf("blocked = %+v", got)
		}
		usd := OpenUsageLedger(LedgerOptions{Path: filepath.Join(dir, "u.json"), Now: now, UsdPerMTok: 1_000_000})
		usd.RecordSuccess(1, 0)
		if got := usd.Blocked(SpendCaps{MaxUSDPerDay: 0.5}); got == nil || got.Cap != CapUSDPerDay || got.Used != 1 {
			t.Fatalf("blocked = %+v", got)
		}
		// The caps belong to the caller: the same totals block under one cap and pass under another.
		if usd.Blocked(SpendCaps{MaxUSDPerDay: 10}) != nil {
			t.Fatal("a higher cap must not block")
		}
	})
	tw(t, "usage", "caps come from the environment without ever raising the explicit cap", func(t *testing.T) {
		env := map[string]string{"PI_TYPESAFE_MAX_REQUESTS_PER_DAY": "500", "PI_TYPESAFE_MAX_USD_PER_DAY": "2.5", "PI_TYPESAFE_MAX_INPUT_TOKENS_PER_DAY": "nonsense"}
		get := func(k string) string { return env[k] }
		environment := CapsFromEnvironment(get)
		if environment != (SpendCaps{MaxRequestsPerDay: 500, MaxUSDPerDay: 2.5}) {
			t.Fatalf("environment = %+v", environment)
		}
		if got := MergeCaps(SpendCaps{MaxRequests: 20, MaxUSDPerDay: 1}, environment); got != (SpendCaps{MaxRequests: 20, MaxRequestsPerDay: 500, MaxUSDPerDay: 1}) {
			t.Fatalf("merged = %+v", got)
		}
		if MergeCaps(SpendCaps{}, SpendCaps{}) != (SpendCaps{}) || CapsFromEnvironment(func(string) string { return "" }) != (SpendCaps{}) {
			t.Fatal("empty caps must stay empty")
		}
	})
	tw(t, "usage", "a corrupt, unreadable, or foreign ledger never blocks a request", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.json")
		now := func() time.Time { return at(6, 12) }
		writeFile(t, path, "{ not json", 0o600)
		l := OpenUsageLedger(LedgerOptions{Path: path, Now: now})
		if l.Today().RequestsStarted != 0 {
			t.Fatal("corrupt ledger must restart the count")
		}
		l.RecordStart()
		if OpenUsageLedger(LedgerOptions{Path: path, Now: now}).Today().RequestsStarted != 1 {
			t.Fatal("the rewritten ledger must persist")
		}
		writeFile(t, path, `{"version":1,"days":{"2026-01-06":{"requestsStarted":"many"},"notADay":{}}}`, 0o600)
		if OpenUsageLedger(LedgerOptions{Path: path, Now: now}).Today().RequestsStarted != 0 {
			t.Fatal("a foreign ledger must read as zero")
		}
	})
	tw(t, "usage", "the default usage path sits with the key store", func(t *testing.T) {
		dir := isolate(t)
		if UsagePath() != filepath.Join(dir, "pi-typesafe", "usage.json") || LocalDay(at(7, 12)) != "2026-01-07" {
			t.Fatalf("path = %s", UsagePath())
		}
	})
}
