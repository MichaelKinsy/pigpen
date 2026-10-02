package pitypesafe

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultUSDPerMTok mirrors the $42-per-billion-input-token rate the original's README quotes, so a spend cap
// means something before anyone configures a price. TypeSafe bills input tokens only; output is free.
const DefaultUSDPerMTok = 0.042

const (
	keepDays     = 31
	usageVersion = 1
)

// UsageTotals are counters for one window (a session, or one local day).
type UsageTotals struct {
	RequestsStarted   int `json:"requestsStarted"`
	RequestsSucceeded int `json:"requestsSucceeded"`
	RequestsFailed    int `json:"requestsFailed"`
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
}

// UsageReport is one day of persisted totals plus the cost estimate for that day.
type UsageReport struct {
	UsageTotals
	Day          string
	EstimatedUSD float64
}

// SpendCaps are client-side spend caps. A zero field is unlimited. A cap that is reached raises a budget
// error before the next request leaves the process.
type SpendCaps struct {
	MaxRequests          int
	MaxRequestsPerDay    int
	MaxInputTokensPerDay int
	MaxUSDPerDay         float64
}

// BlockedCapName names a reached cap.
type BlockedCapName string

// The day caps.
const (
	CapRequestsPerDay    BlockedCapName = "requestsPerDay"
	CapInputTokensPerDay BlockedCapName = "inputTokensPerDay"
	CapUSDPerDay         BlockedCapName = "usdPerDay"
)

// BlockedCap is the cap that stops the next request, with what it allows and what has been used today.
type BlockedCap struct {
	Cap   BlockedCapName
	Limit float64
	Used  float64
	Day   string
}

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// LocalDay is the local calendar day of t as YYYY-MM-DD.
func LocalDay(t time.Time) string { return t.Format("2006-01-02") }

// UsagePath is the stored ledger, alongside the key store so one directory holds every file of this package.
func UsagePath() string { return filepath.Join(TypeSafeDir(), "usage.json") }

// EstimateUSD is the input-token cost, rounded to a micro-dollar so the number stays readable.
func EstimateUSD(inputTokens int, usdPerMTok float64) float64 {
	return math.Floor(float64(inputTokens)*usdPerMTok/1e6*1e6+0.5) / 1e6
}

// EmptyTotals returns zeroed counters.
func EmptyTotals() UsageTotals { return UsageTotals{} }

// CapsFromEnvironment reads the caps a headless run may set without code: PI_TYPESAFE_MAX_USD_PER_DAY and its
// siblings. getenv nil uses the process environment. Values that are not positive numbers are ignored.
func CapsFromEnvironment(getenv func(string) string) SpendCaps {
	if getenv == nil {
		getenv = os.Getenv
	}
	number := func(name string) float64 {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return 0
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return 0
		}
		return v
	}
	return SpendCaps{
		MaxRequestsPerDay:    int(math.Floor(number("PI_TYPESAFE_MAX_REQUESTS_PER_DAY"))),
		MaxInputTokensPerDay: int(math.Floor(number("PI_TYPESAFE_MAX_INPUT_TOKENS_PER_DAY"))),
		MaxUSDPerDay:         number("PI_TYPESAFE_MAX_USD_PER_DAY"),
	}
}

// MergeCaps combines an explicit cap with the environment's; the lowest set value wins, so the environment may
// lower an explicit cap and never raise it. The session cap comes from the explicit caps only.
func MergeCaps(explicit, environment SpendCaps) SpendCaps {
	lowestInt := func(a, b int) int {
		switch {
		case a == 0:
			return b
		case b == 0:
			return a
		}
		return min(a, b)
	}
	lowestFloat := func(a, b float64) float64 {
		switch {
		case a == 0:
			return b
		case b == 0:
			return a
		}
		return math.Min(a, b)
	}
	return SpendCaps{
		MaxRequests:          explicit.MaxRequests,
		MaxRequestsPerDay:    lowestInt(explicit.MaxRequestsPerDay, environment.MaxRequestsPerDay),
		MaxInputTokensPerDay: lowestInt(explicit.MaxInputTokensPerDay, environment.MaxInputTokensPerDay),
		MaxUSDPerDay:         lowestFloat(explicit.MaxUSDPerDay, environment.MaxUSDPerDay),
	}
}

// LedgerOptions configure OpenUsageLedger.
type LedgerOptions struct {
	// Path defaults to UsagePath(); tests point it at a temporary file.
	Path string
	// Now is the clock for the local day and for rollover; injectable for tests.
	Now        func() time.Time
	UsdPerMTok float64
}

// UsageLedger is one day of persisted request, token, and cost totals, plus the caps that stop the next
// request. The in-memory copy is authoritative for this process; the file is the cross-process,
// across-restart record. Reads are defensive, writes are atomic and best-effort, and a day rolls over on the
// local date, so a long eval cannot accumulate forever unnoticed. It is safe for concurrent use.
type UsageLedger interface {
	Path() string
	UsdPerMTok() float64
	// Today returns today's totals, after any rollover.
	Today() UsageReport
	// RecordStart counts the attempt before it is submitted; a request that never returns still counts.
	RecordStart()
	RecordSuccess(inputTokens, outputTokens int)
	RecordFailure()
	// Blocked returns the reached day cap that blocks the next request, or nil. The caller owns the caps.
	Blocked(caps SpendCaps) *BlockedCap
	// Describe is one line for status output: today's requests, tokens, and cost, with the caps that apply.
	Describe(caps SpendCaps) string
}

type fileLedger struct {
	mu         sync.Mutex
	path       string
	now        func() time.Time
	usdPerMTok float64
	day        string
	days       map[string]UsageTotals
	totals     UsageTotals
}

// OpenUsageLedger opens (or starts) the ledger file.
func OpenUsageLedger(opts LedgerOptions) UsageLedger {
	l := &fileLedger{path: opts.Path, now: opts.Now, usdPerMTok: opts.UsdPerMTok}
	if l.path == "" {
		l.path = UsagePath()
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.usdPerMTok <= 0 {
		l.usdPerMTok = DefaultUSDPerMTok
	}
	l.day = LocalDay(l.now())
	l.days = keepRecent(readDays(l.path), l.day)
	l.totals = l.days[l.day]
	return l
}

func count(v any) int {
	f, ok := v.(float64)
	if !ok || f < 0 || f != math.Floor(f) || f > 1<<53-1 {
		return 0
	}
	return int(f)
}

func totalsOf(v any) UsageTotals {
	raw, _ := v.(map[string]any)
	return UsageTotals{
		RequestsStarted:   count(raw["requestsStarted"]),
		RequestsSucceeded: count(raw["requestsSucceeded"]),
		RequestsFailed:    count(raw["requestsFailed"]),
		InputTokens:       count(raw["inputTokens"]),
		OutputTokens:      count(raw["outputTokens"]),
	}
}

// readDays is defensive: a missing, unreadable, or corrupt ledger restarts today's count; it never blocks a request.
func readDays(path string) map[string]UsageTotals {
	out := map[string]UsageTotals{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) != nil {
		return out
	}
	days, ok := parsed["days"].(map[string]any)
	if !ok {
		return out
	}
	for day, totals := range days {
		if dayPattern.MatchString(day) {
			out[day] = totalsOf(totals)
		}
	}
	return out
}

func keepRecent(days map[string]UsageTotals, today string) map[string]UsageTotals {
	names := make([]string, 0, len(days))
	for n := range days {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > keepDays {
		names = names[len(names)-keepDays:]
	}
	out := map[string]UsageTotals{}
	for _, n := range names {
		out[n] = days[n]
	}
	if _, ok := days[today]; ok {
		out[today] = days[today]
	} else {
		out[today] = UsageTotals{}
	}
	return out
}

// writeDays is owner-only, atomic, and best-effort: a ledger this process cannot write never fails a request.
func writeDays(path string, days map[string]UsageTotals) {
	body, err := json.MarshalIndent(map[string]any{"version": usageVersion, "days": days}, "", "  ")
	if err != nil {
		return
	}
	_ = writeOwnerOnly(path, append(body, '\n'))
}

func (l *fileLedger) Path() string        { return l.path }
func (l *fileLedger) UsdPerMTok() float64 { return l.usdPerMTok }

func (l *fileLedger) report(t UsageTotals, day string) UsageReport {
	return UsageReport{UsageTotals: t, Day: day, EstimatedUSD: EstimateUSD(t.InputTokens, l.usdPerMTok)}
}

func (l *fileLedger) save() {
	l.days = keepRecent(mergeDay(l.days, l.day, l.totals), l.day)
	writeDays(l.path, l.days)
}

func mergeDay(days map[string]UsageTotals, day string, totals UsageTotals) map[string]UsageTotals {
	out := make(map[string]UsageTotals, len(days)+1)
	for k, v := range days {
		out[k] = v
	}
	out[day] = totals
	return out
}

func (l *fileLedger) roll() {
	current := LocalDay(l.now())
	if current == l.day {
		return
	}
	l.day = current
	l.totals = l.days[l.day]
	l.days = keepRecent(l.days, l.day)
}

func (l *fileLedger) add(d UsageTotals) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	l.totals = UsageTotals{
		RequestsStarted:   l.totals.RequestsStarted + d.RequestsStarted,
		RequestsSucceeded: l.totals.RequestsSucceeded + d.RequestsSucceeded,
		RequestsFailed:    l.totals.RequestsFailed + d.RequestsFailed,
		InputTokens:       l.totals.InputTokens + d.InputTokens,
		OutputTokens:      l.totals.OutputTokens + d.OutputTokens,
	}
	l.save()
}

func (l *fileLedger) Today() UsageReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	return l.report(l.totals, l.day)
}

func (l *fileLedger) RecordStart() { l.add(UsageTotals{RequestsStarted: 1}) }
func (l *fileLedger) RecordSuccess(in, out int) {
	l.add(UsageTotals{RequestsSucceeded: 1, InputTokens: max(in, 0), OutputTokens: max(out, 0)})
}
func (l *fileLedger) RecordFailure() { l.add(UsageTotals{RequestsFailed: 1}) }

func (l *fileLedger) Blocked(caps SpendCaps) *BlockedCap {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	checks := []struct {
		cap   BlockedCapName
		limit float64
		used  float64
	}{
		{CapRequestsPerDay, float64(caps.MaxRequestsPerDay), float64(l.totals.RequestsStarted)},
		{CapInputTokensPerDay, float64(caps.MaxInputTokensPerDay), float64(l.totals.InputTokens)},
		{CapUSDPerDay, caps.MaxUSDPerDay, EstimateUSD(l.totals.InputTokens, l.usdPerMTok)},
	}
	for _, c := range checks {
		if c.limit > 0 && c.used >= c.limit {
			return &BlockedCap{Cap: c.cap, Limit: c.limit, Used: c.used, Day: l.day}
		}
	}
	return nil
}

func (l *fileLedger) Describe(caps SpendCaps) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	cur := l.report(l.totals, l.day)
	var limits []string
	if caps.MaxRequestsPerDay > 0 {
		limits = append(limits, fmt.Sprintf("%d/%d requests", cur.RequestsStarted, caps.MaxRequestsPerDay))
	}
	if caps.MaxInputTokensPerDay > 0 {
		limits = append(limits, fmt.Sprintf("%d/%d input tokens", cur.InputTokens, caps.MaxInputTokensPerDay))
	}
	if caps.MaxUSDPerDay > 0 {
		limits = append(limits, fmt.Sprintf("$%.4f/$%.2f", cur.EstimatedUSD, caps.MaxUSDPerDay))
	}
	tail := "; no daily cap"
	if len(limits) > 0 {
		tail = "; caps " + strings.Join(limits, ", ")
	}
	return fmt.Sprintf("%d requests today (%d ok, %d failed), %d input / %d output tokens, ~$%.4f%s",
		cur.RequestsStarted, cur.RequestsSucceeded, cur.RequestsFailed, cur.InputTokens, cur.OutputTokens, cur.EstimatedUSD, tail)
}
