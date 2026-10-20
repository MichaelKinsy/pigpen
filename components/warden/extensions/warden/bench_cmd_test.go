package warden

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func benchCommandProject(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "pi-warden.md"), "# No console statements\nCode must not contain `console.log`.\n")
	o.cwd = cwd
	h := startHarness(t, o)
	h.start()
	return h
}

// twin(wiring): tests/extension.test.ts:2624 (/warden bench measures with a built-in sample and sends nothing without
// consent). The fake backend reports 10 input tokens per request, not the upstream fixture's 50.
func TestBenchMeasuresWithTheBuiltInSample(t *testing.T) {
	h := benchCommandProject(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.host.Command("warden", "bench --runs 3")
	if h.ts.count() != 3 {
		t.Fatalf("%d requests, want one per run", h.ts.count())
	}
	if path := h.ts.requests[0].Body["state"].(map[string]any)["path"].(string); !strings.HasPrefix(path, "src/") {
		t.Fatalf("state path %q: only the built-in sample travels", path)
	}
	text := h.notices()[len(h.notices())-1]
	for _, re := range []string{
		`^Bench: 3 checks of one built-in sample file against the active rules \(1 rule per check\)\.`,
		`The sample is built in and no project content is sent, so no confirmation was needed\.`,
		`Latency: p50 \d+ ms, p95 \d+ ms; requests 3\.`,
		`Input tokens per check: 10 \(mean\)\.`,
		`Estimated cost per check: \$0\.000000; per 100 edits: \$0\.000033\.`,
	} {
		if !regexp.MustCompile(`(?m)` + re).MatchString(text) {
			t.Errorf("missing %s in %q", re, text)
		}
	}
}

func TestBenchWithoutConsentSendsNothingAndBadArgsAreWarned(t *testing.T) {
	h := benchCommandProject(t, harnessOpts{})
	h.host.Command("warden", "bench")
	n := h.notices()
	if got := n[len(n)-1]; got != "Bench sent nothing: Jev judgments are off. warden: Jev judgments are off (no consent). Run /warden enable." {
		t.Fatal(got)
	}
	h.host.Command("warden", "bench --runs 0")
	n = h.notices()
	if got := n[len(n)-1]; got != "--runs needs a positive integer. Usage: /warden bench [--runs N], where N is a positive integer (default 10)." {
		t.Fatal(got)
	}
	if h.ts.count() != 0 {
		t.Fatal("sent a request")
	}
}

func TestBudgetUsageCountsRequestsAndInputCost(t *testing.T) {
	b := &Budget{}
	_ = b.take("x")
	b.recordSuccess(1000, 5)
	b.recordSuccess(-3, -1)
	u := b.Usage()
	if u.RequestsStarted != 1 || u.InputTokens != 1000 || u.OutputTokens != 5 || u.EstimatedUSD != 0.000042 {
		t.Fatalf("%+v", u)
	}
	b.Reset()
	if u := b.Usage(); u != (BenchUsage{}) {
		t.Fatalf("%+v", u)
	}
	var none *Budget
	none.recordSuccess(1, 1)
}
