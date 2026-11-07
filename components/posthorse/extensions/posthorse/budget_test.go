package posthorse

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func budgetPolicy(window, tokens, reserve int, enabled bool) fakePolicy {
	t := tokens
	return fakePolicy{
		Settings: compactionPolicy{enabled: enabled, reserveTokens: reserve},
		Usage:    &contextUsage{tokens: &t, contextWindow: window},
		Prompt:   "You are a test assistant.",
	}
}

func TestBudgetFor(t *testing.T) {
	intp := func(n int) *int { return &n }
	for _, tc := range []struct {
		name    string
		policy  fakePolicy
		window  *int
		want    *budget
		wantNil bool
	}{
		{"usage window", budgetPolicy(100_000, 1000, 16_384, true), nil,
			&budget{contextWindow: 100_000, reserveTokens: 16_384, enabled: true, usable: 83_616, rolloverAt: 83_617, supported: true}, false},
		{"explicit window overrides usage", budgetPolicy(100_000, 1000, 16_384, true), intp(32_768),
			&budget{contextWindow: 32_768, reserveTokens: 16_384, enabled: true, usable: 16_384, rolloverAt: 16_385, supported: true}, false},
		{"boundary: exactly the minimum is supported", budgetPolicy(26_384, 0, 16_384, true), nil,
			&budget{contextWindow: 26_384, reserveTokens: 16_384, enabled: true, usable: 10_000, rolloverAt: 10_001, supported: true}, false},
		{"boundary: one below is unsupported", budgetPolicy(26_383, 0, 16_384, true), nil,
			&budget{contextWindow: 26_383, reserveTokens: 16_384, enabled: true, usable: 9_999, rolloverAt: 10_000, supported: false}, false},
		{"disabled compaction is always supported", budgetPolicy(4096, 0, 16_384, false), nil,
			&budget{contextWindow: 4096, reserveTokens: 16_384, enabled: false, usable: -12_288, rolloverAt: -12_287, supported: true}, false},
		{"zero window", budgetPolicy(0, 0, 16_384, true), nil, nil, true},
		{"negative explicit window", budgetPolicy(100_000, 0, 16_384, true), intp(-1), nil, true},
		{"no usage", fakePolicy{Settings: compactionPolicy{enabled: true, reserveTokens: 1}}, nil, nil, true},
	} {
		got, err := budgetFor(tc.policy, tc.window)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.wantNil {
			if got != nil {
				t.Errorf("%s: got %+v, want nil", tc.name, got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestBudgetForPropagatesHostErrors(t *testing.T) {
	boom := errors.New("host down")
	if _, err := budgetFor(fakePolicy{Err: boom}, nil); !errors.Is(err, boom) {
		t.Errorf("usage error: %v", err)
	}
	intp := 5000
	if _, err := budgetFor(fakePolicy{Err: boom}, &intp); !errors.Is(err, boom) {
		t.Errorf("settings error: %v", err)
	}
}

func TestUnsupportedMessage(t *testing.T) {
	got := unsupportedMessage(&budget{contextWindow: 4096, reserveTokens: 16_384, usable: -12_288})
	want := "Posthorse: unsupported configuration. The model's context window (4,096 tokens) minus Pi's compaction.reserveTokens (16,384) leaves -12,288 usable tokens; Posthorse needs at least 10,000. Automatic Posthorse rollover and checkpoint reminders are off for this model, so Pi's own compaction applies. Lower compaction.reserveTokens in Pi settings or use a larger-context model. new_context remains available with a model-aware handoff limit."
	if got != want {
		t.Errorf("unsupportedMessage:\n got %q\nwant %q", got, want)
	}
}

func TestBuildGuidanceVariants(t *testing.T) {
	enabled, err := buildGuidance(budgetPolicy(100_000, 1000, 16_384, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"## Automatic context management (Posthorse)\nKeep working on the assigned task;",
		"The rollover line is 83,617 tokens used under the available Pi settings.",
		"Configured context capacity: 100,000 tokens.",
		"Write handoffs and notes as normal readable prose; notes and history search match literal text.",
	} {
		if !strings.Contains(enabled, part) {
			t.Errorf("enabled guidance lacks %q", part)
		}
	}
	disabled, _ := buildGuidance(budgetPolicy(100_000, 1000, 16_384, false))
	if !strings.Contains(disabled, "Pi compaction is disabled in the available settings, so Posthorse sends no checkpoint reminder. new_context remains available.\nConfigured context capacity: 100,000 tokens.") || strings.Contains(disabled, "rollover line") {
		t.Errorf("disabled guidance: %q", disabled)
	}
	unsupported, _ := buildGuidance(budgetPolicy(4096, 1000, 16_384, true))
	if !strings.Contains(unsupported, "unsupported configuration") || strings.Contains(unsupported, "Keep working on the assigned task") {
		t.Errorf("unsupported guidance: %q", unsupported)
	}
	unknown, _ := buildGuidance(fakePolicy{Settings: compactionPolicy{enabled: true, reserveTokens: 1}})
	if !strings.Contains(unknown, "Configured context capacity is unknown.") || strings.Contains(unknown, "The rollover line is") {
		t.Errorf("unknown-capacity guidance: %q", unknown)
	}
	unknownDisabled, _ := buildGuidance(fakePolicy{Settings: compactionPolicy{enabled: false}})
	if !strings.Contains(unknownDisabled, "Pi compaction is disabled") {
		t.Errorf("a missing budget still reports the settings' enabled flag: %q", unknownDisabled)
	}
}

// guidanceCost is the token estimate of the request-local system message.
func guidanceCost(t testing.TB, p policyContext) int {
	t.Helper()
	guidance, err := buildGuidance(p)
	if err != nil {
		t.Fatal(err)
	}
	return (u16len("<posthorse>\n"+guidance+"\n</posthorse>") + 3) / 4
}

func TestGuidanceTokensIsPiEstimate(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	got, err := guidanceTokens(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := guidanceCost(t, p); got != want {
		t.Errorf("guidanceTokens = %d, want ceil(length/4) = %d", got, want)
	}
}

func TestFreshPayloadChars(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	p.Prompt = strings.Repeat("p", 401)
	g := guidanceCost(t, p)
	pending := []rec{
		{"role": "user", "content": strings.Repeat("x", 40)},
		{"role": "user", "content": []any{rec{"type": "text", "text": "yyyyy"}, rec{"type": "image", "data": "d", "mimeType": "image/png"}}},
	}
	// line 83,617; prompt ceil(401/4) = 101; pending 10 + (2 + 1 image * 1200).
	want := (83_617 - 1_000 - (101 + g) - 300 - (10 + 2 + 1200)) / 2 * 4
	got, err := freshPayloadChars(p, 300, pending, maxHandoffChars)
	if err != nil {
		t.Fatal(err)
	}
	if got != min(want, maxHandoffChars) {
		t.Errorf("freshPayloadChars = %d, want %d", got, min(want, maxHandoffChars))
	}
	if got, _ := freshPayloadChars(p, 300, nil, 100); got != 100 {
		t.Errorf("the cap bounds the result: %d", got)
	}
	// Disabled compaction measures against the configured limit rather than the rollover line.
	off := budgetPolicy(100_000, 1000, 16_384, false)
	got, _ = freshPayloadChars(off, 0, nil, maxPageChars)
	if want := min(maxPageChars, (100_000-1_000-(7+guidanceCost(t, off)))/2*4); got != want {
		t.Errorf("disabled: %d, want %d", got, want)
	}
	// No window: the cap itself.
	if got, _ := freshPayloadChars(fakePolicy{Settings: compactionPolicy{enabled: true}}, 0, nil, 1234); got != 1234 {
		t.Errorf("unknown window: %d", got)
	}
	// Exhausted budget: zero.
	tight := budgetPolicy(32_768, 1000, 16_384, true)
	tight.Prompt = strings.Repeat("s", 60_000)
	if got, _ := freshPayloadChars(tight, 0, nil, maxHandoffChars); got != 0 {
		t.Errorf("a prompt larger than the window leaves nothing: %d", got)
	}
}

func TestPageCapacityKnownUsage(t *testing.T) {
	p := budgetPolicy(100_000, 12_000, 16_384, true)
	fail := func(sdk.Context) (int, error) { return 0, errors.New("tool tokens must not be read") }
	got, err := pageCapacity(p, fail)
	if err != nil {
		t.Fatal(err)
	}
	if want := 83_617 - 12_000 - guidanceCost(t, p); got != want {
		t.Errorf("pageCapacity = %d, want %d", got, want)
	}
	disabled := budgetPolicy(100_000, 12_000, 16_384, false)
	got, _ = pageCapacity(disabled, fail)
	if want := 100_000 - 12_000 - guidanceCost(t, disabled); got != want {
		t.Errorf("disabled pageCapacity = %d, want %d", got, want)
	}
}

func TestPageCapacityUnknownUsageNeedsTheHost(t *testing.T) {
	p := fakePolicy{Settings: compactionPolicy{enabled: true, reserveTokens: 16_384}}
	if _, err := pageCapacity(p, func(sdk.Context) (int, error) { return 0, nil }); err == nil {
		t.Error("a policy context that has no host cannot read the active tools")
	}
}

func TestPolicySnapshotReadsSettingsOnce(t *testing.T) {
	reads := 0
	reader := func(sdk.Context) (compactionPolicy, error) {
		reads++
		return compactionPolicy{enabled: true, reserveTokens: 7}, nil
	}
	snap := policySnapshot(sdk.Context{}, reader, nil)
	for range 3 {
		got, err := snap.getCompactionSettings()
		if err != nil || got.reserveTokens != 7 {
			t.Fatalf("settings: %+v, %v", got, err)
		}
	}
	if reads != 1 {
		t.Errorf("reader called %d times, want 1", reads)
	}
	live := compactionPolicy{enabled: false, reserveTokens: 99}
	got, _ := policySnapshot(sdk.Context{}, reader, &live).getCompactionSettings()
	if got != live || reads != 1 {
		t.Errorf("a live policy replaces the reader: %+v after %d reads", got, reads)
	}
}

func TestPolicySnapshotDoesNotCacheFailures(t *testing.T) {
	calls := 0
	reader := func(sdk.Context) (compactionPolicy, error) {
		calls++
		if calls == 1 {
			return compactionPolicy{}, errors.New("first read fails")
		}
		return compactionPolicy{reserveTokens: 3}, nil
	}
	snap := policySnapshot(sdk.Context{}, reader, nil)
	if _, err := snap.getCompactionSettings(); err == nil {
		t.Fatal("expected the reader's error")
	}
	got, err := snap.getCompactionSettings()
	if err != nil || got.reserveTokens != 3 {
		t.Fatalf("second read: %+v, %v", got, err)
	}
}

func TestHostOfNeedsASnapshot(t *testing.T) {
	if _, err := hostOf(fakePolicy{}); err == nil {
		t.Error("a bare policy context has no host")
	}
	if _, err := hostOf(policySnapshot(sdk.Context{}, nil, nil)); err != nil {
		t.Errorf("a snapshot carries its host: %v", err)
	}
}

func BenchmarkBudgetFor(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	b.ReportAllocs()
	for range b.N {
		_, _ = budgetFor(p, nil)
	}
}

func BenchmarkBuildGuidance(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	b.ReportAllocs()
	for range b.N {
		_, _ = buildGuidance(p)
	}
}

func BenchmarkGuidanceTokens(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	b.ReportAllocs()
	for range b.N {
		_, _ = guidanceTokens(p)
	}
}

func BenchmarkFreshPayloadChars(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	pending := []rec{{"role": "user", "content": strings.Repeat("x", 400)}}
	b.ReportAllocs()
	for range b.N {
		_, _ = freshPayloadChars(p, 300, pending, maxHandoffChars)
	}
}

func BenchmarkPageCapacity(b *testing.B) {
	p := budgetPolicy(100_000, 12_000, 16_384, true)
	b.ReportAllocs()
	for range b.N {
		_, _ = pageCapacity(p, nil)
	}
}
