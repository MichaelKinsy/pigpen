package eq

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Result is the verdict for one scenario.
type Result struct {
	Scenario string
	Pass     bool
	Detail   string // human-readable reason when Pass is false, or a note when true
	Traces   map[string]string
	// Fatal marks a host that never became ready or died early (for example an
	// extension that does not build). Such a run shows nothing about behavior.
	Fatal bool
}

// Config names the hosts and extensions of a comparison.
type Config struct {
	Pi, Pig string // host executables
	TS      string // the original TypeScript extension (oracle)
	Go      string // the Go port directory
	// GoOracle is an original that is already a Go extension (relocated code): it runs
	// under PiG as the oracle. Self means there is no original at all: the port is recorded
	// as its own golden trace, a regression baseline and not proof of equivalence.
	// Exactly one of TS, GoOracle and Self is set.
	GoOracle string
	// UnitOnly (mutate) judges mutants by the port's own Go tests alone: an original adapter has
	// no scenarios or golden traces. Jobs is how many mutants run at once (default 1).
	UnitOnly bool
	Jobs     int
	// Builtin means the port is compiled into the Pig executable (a built Piglet Binary): no
	// extension directory is loaded with -e. check and record use it; mutate cannot.
	Builtin bool
	Self    bool
	Out     string // directory for traces; a temp dir when empty
	Options Options
	// SkipHostCheck skips running the TypeScript original under PiG. That run
	// proves the trace projection is host-neutral: identical traces from Pi and
	// from PiG's Node runtime mean any Go-lane difference is the port's.
	SkipHostCheck bool
	// Surface is PiG's docs/extension-sdk-surface.md; empty uses the embedded
	// snapshot of its non-implemented rows.
	Surface string
	// AcceptedGaps maps a gap symbol to the owner approval that excuses it.
	AcceptedGaps map[string]string
}

// The three kinds of oracle a golden trace can come from (the trace header's lane).
const (
	OracleTS         = "pi-ts"           // the TypeScript original under Pi
	OracleGoUpstream = "pig-go-upstream" // an original Go extension under PiG (a Go-to-Go relocation)
	OracleSelf       = "pig-go-self"     // the port itself: a regression baseline
)

// oracle picks the lane that produces the reference trace and says which kind it is.
func (c Config) oracle() (Lane, string, error) {
	n := 0
	for _, set := range []bool{c.TS != "", c.GoOracle != "", c.Self} {
		if set {
			n++
		}
	}
	switch {
	case n > 1:
		return Lane{}, "", errors.New("give exactly one original: --ts, --go-oracle or --self")
	case n == 0:
		return Lane{}, "", errors.New("no oracle: give --ts (a TypeScript original, run under Pi), --go-oracle (a Go original, run under PiG) or --self (no original exists: the port is recorded as its own regression baseline)")
	case c.TS != "":
		if c.Pi == "" {
			return Lane{}, "", errors.New("the TypeScript original needs a pi executable (--pi or PIGEQ_PI)")
		}
		return Lane{Name: OracleTS, Host: "pi", Bin: c.Pi, Ext: c.TS}, OracleTS, nil
	}
	if c.Pig == "" {
		return Lane{}, "", errors.New("a pig executable is required (--pig or PIGEQ_PIG)")
	}
	if c.GoOracle != "" {
		return Lane{Name: OracleGoUpstream, Host: "pig", Bin: c.Pig, Ext: c.GoOracle}, OracleGoUpstream, nil
	}
	return Lane{Name: OracleSelf, Host: "pig", Bin: c.Pig, Ext: c.goExt()}, OracleSelf, nil
}

// goExt is the extension directory loaded into the port's lane: none for a built-in port.
func (c Config) goExt() string {
	if c.Builtin {
		return ""
	}
	return c.Go
}

// GoOracleLane names the oracle lane of a Go-original run.
const GoOracleLane = OracleGoUpstream

// CheckGoOracle validates a Go-original configuration: the original Go extension replaces the
// TypeScript original and Pi, and runs under PiG.
func (c Config) CheckGoOracle() error {
	switch {
	case c.GoOracle == "":
		return errors.New("no Go original named")
	case c.TS != "" || c.Pi != "":
		return errors.New("a Go original replaces the TypeScript original and Pi; do not also pass --ts or --pi")
	case c.Pig == "":
		return errors.New("a Go original runs under PiG; pass --pig")
	}
	return nil
}

// IsEquivalenceProof reports whether a golden trace of this kind can prove equivalence.
// A self-recorded trace only detects later change.
func IsEquivalenceProof(kind string) bool { return kind != OracleSelf }

func goldenNote(kind string) string {
	switch kind {
	case OracleSelf:
		return "golden is self-recorded (regression baseline, not equivalence: the port was recorded from itself and reviewed by hand)"
	case OracleGoUpstream:
		return "golden recorded from the original Go extension under PiG"
	default:
		return "golden recorded from the original under Pi"
	}
}

func (c *Config) outDir() (string, error) {
	if c.Out != "" {
		return c.Out, os.MkdirAll(c.Out, 0o755)
	}
	return os.MkdirTemp("", "eq-out")
}

// scratchOut is outDir for a caller that owns the result: with no --out the traces go to a private
// temporary directory, and release removes it once every result passed (nobody needs the traces then)
// or the run errored (no result points into it). A failing run keeps it, because its Result.Traces paths are what
// `pigeq diff` reads. A directory named with --out is never removed.
func (c *Config) scratchOut() (string, func([]Result, error), error) {
	dir, err := c.outDir()
	if err != nil {
		return "", nil, err
	}
	if c.Out != "" {
		return dir, func([]Result, error) {}, nil
	}
	return dir, func(results []Result, err error) {
		for _, r := range results {
			if !r.Pass {
				return
			}
		}
		_ = os.RemoveAll(dir)
	}, nil
}

// withScratchOut runs f with cfg.Out set to a scratch directory when none was named, and cleans it up as scratchOut says.
func withScratchOut(cfg Config, f func(Config) ([]Result, error)) ([]Result, error) {
	dir, release, err := cfg.scratchOut()
	if err != nil {
		return nil, err
	}
	cfg.Out = dir
	results, err := f(cfg)
	release(results, err)
	return results, err
}

func writeTrace(dir, scenario, lane string, t *Trace) (string, error) {
	p := filepath.Join(dir, scenario)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(p, lane+".jsonl")
	return path, t.WriteFile(path)
}

// verdict compares a candidate trace to the wanted one and describes the outcome.
func verdict(want, got *Trace) (bool, string) {
	if e, failed := got.Failed(); failed {
		return false, fmt.Sprintf("lane %s failed while running: %s", got.Header.Lane, e.Data)
	}
	if e, failed := want.Failed(); failed {
		return false, fmt.Sprintf("reference lane %s failed while running: %s", want.Header.Lane, e.Data)
	}
	if d := Diff(want, got); d != nil {
		return false, fmt.Sprintf("%s differs from %s: %s", got.Header.Lane, want.Header.Lane, d)
	}
	return true, fmt.Sprintf("%s == %s (%d events)", got.Header.Lane, want.Header.Lane, len(want.Events))
}

// Run executes every scenario live: the original under Pi (the oracle), the
// original under PiG (host check), and the Go port under PiG. A scenario passes
// when the port's trace equals the oracle's.
func Run(scs []*Scenario, cfg Config) ([]Result, error) {
	if cfg.Out == "" {
		return withScratchOut(cfg, func(c Config) ([]Result, error) { return Run(scs, c) })
	}
	oracleLane, kind, err := cfg.oracle()
	if err != nil {
		return nil, err
	}
	if kind == OracleSelf {
		return nil, errors.New("run compares the port with an original; with no original use record --self, then check")
	}
	if cfg.Pig == "" || (cfg.Go == "" && !cfg.Builtin) {
		return nil, errors.New("run needs --pig and --go")
	}
	out, err := cfg.outDir()
	if err != nil {
		return nil, err
	}
	results, err := cfg.staticResults(scs)
	if err != nil {
		return nil, err
	}
	for _, sc := range scs {
		r := Result{Scenario: sc.Name, Traces: map[string]string{}}
		lane := func(l Lane) (*Trace, error) {
			t, err := RunLane(sc, l, cfg.Options)
			if err != nil {
				return nil, err
			}
			path, err := writeTrace(out, sc.Name, l.Name, t)
			r.Traces[l.Name] = path
			return t, err
		}
		oracle, err := lane(oracleLane)
		if err != nil {
			return nil, err
		}
		if e, failed := oracle.Failed(); failed {
			r.Pass, r.Detail = false, fmt.Sprintf("oracle failed while running: %s", e.Data)
			results = append(results, r)
			continue
		}
		notes := []string{}
		pass := true
		if kind == OracleTS && !cfg.SkipHostCheck {
			host, err := lane(Lane{Name: "pig-ts", Host: "pig", Bin: cfg.Pig, Ext: cfg.TS})
			if err != nil {
				return nil, err
			}
			ok, detail := verdict(oracle, host)
			if !ok {
				pass = false
			}
			notes = append(notes, "host check: "+detail)
		}
		port, err := lane(Lane{Name: "pig-go", Host: "pig", Bin: cfg.Pig, Ext: cfg.goExt()})
		if err != nil {
			return nil, err
		}
		ok, detail := verdict(oracle, port)
		if !ok {
			pass = false
		}
		notes = append(notes, "port: "+detail)
		r.Pass, r.Detail = pass, strings.Join(notes, "\n")
		results = append(results, r)
	}
	return results, nil
}

// staticResults are the pseudo-scenarios that need no host: the SDK gap check of the
// TypeScript original, the gap check of the Go port (PiG-internal imports, fuse
// hazards) and exec coverage of every source.
func (c Config) staticResults(scs []*Scenario) ([]Result, error) {
	var results []Result
	if c.TS != "" {
		r, err := c.gapResult()
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if c.Go != "" {
		r, err := c.portGapResult()
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	coverage, err := execCoverageAccepting(scs, c.AcceptedGaps, c.TS, c.GoOracle, c.Go)
	if err != nil {
		return nil, err
	}
	return append(results, coverage), nil
}

// Record runs the oracle lane and writes one golden trace per scenario. With
// a PiG binary set it also runs the original under PiG and fails on any host
// difference, because a golden trace must not depend on the host that made it.
func Record(scs []*Scenario, cfg Config, goldenDir string) ([]Result, error) {
	oracleLane, kind, err := cfg.oracle()
	if err != nil {
		return nil, err
	}
	if kind == OracleTS && cfg.TS == "" || kind == OracleSelf && cfg.Go == "" && !cfg.Builtin {
		return nil, errors.New("record needs the original (--ts or --go-oracle) or, with --self, the port (--go)")
	}
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		return nil, err
	}
	results, err := cfg.staticResults(scs)
	if err != nil {
		return nil, err
	}
	for _, sc := range scs {
		r := Result{Scenario: sc.Name, Pass: true, Traces: map[string]string{}}
		oracle, err := RunLane(sc, oracleLane, cfg.Options)
		if err != nil {
			return nil, err
		}
		if e, failed := oracle.Failed(); failed {
			r.Pass, r.Detail = false, fmt.Sprintf("oracle failed while running: %s", e.Data)
			results = append(results, r)
			continue
		}
		path := filepath.Join(goldenDir, sc.Name+".jsonl")
		if err := oracle.WriteFile(path); err != nil {
			return nil, err
		}
		r.Traces["golden"] = path
		r.Detail = fmt.Sprintf("recorded %d events from %s (%s)", len(oracle.Events), oracle.Header.Host, kind)
		if kind == OracleSelf {
			r.Detail += ": " + goldenNote(kind)
		}
		if kind == OracleTS && cfg.Pig != "" {
			host, err := RunLane(sc, Lane{Name: "pig-ts", Host: "pig", Bin: cfg.Pig, Ext: cfg.TS}, cfg.Options)
			if err != nil {
				return nil, err
			}
			ok, detail := verdict(oracle, host)
			if !ok {
				r.Pass = false
			}
			r.Detail += "\nhost check: " + detail
		}
		results = append(results, r)
	}
	return results, nil
}

// Check runs only the Go port under PiG and compares it to golden traces
// recorded from the oracle. It needs no Pi installation.
func Check(scs []*Scenario, cfg Config, goldenDir string) ([]Result, error) {
	return check(scs, cfg, goldenDir, false)
}

// check stops at the first failing result when failFast is set (a mutant is
// killed by one divergence; the remaining scenarios would only cost time).
func check(scs []*Scenario, cfg Config, goldenDir string, failFast bool) ([]Result, error) {
	if cfg.Out == "" {
		return withScratchOut(cfg, func(c Config) ([]Result, error) { return check(scs, c, goldenDir, failFast) })
	}
	out, err := cfg.outDir()
	if err != nil {
		return nil, err
	}
	results, err := cfg.staticResults(scs)
	if err != nil {
		return nil, err
	}
	if failFast {
		for _, r := range results {
			if !r.Pass {
				return results, nil
			}
		}
	}
	for _, sc := range scs {
		r := Result{Scenario: sc.Name, Traces: map[string]string{}}
		golden, err := ReadTrace(filepath.Join(goldenDir, sc.Name+".jsonl"))
		if err != nil {
			return nil, fmt.Errorf("scenario %s has no golden trace: %w", sc.Name, err)
		}
		if golden.Header.Normalizer != NormalizerVersion {
			return nil, fmt.Errorf("golden %s was recorded with normalizer %s, this harness is %s: record it again", sc.Name, golden.Header.Normalizer, NormalizerVersion)
		}
		if err := goldenModeMatches(sc.Name, golden.Header, cfg.Builtin); err != nil {
			return nil, err
		}
		port, err := RunLane(sc, Lane{Name: "pig-go", Host: "pig", Bin: cfg.Pig, Ext: cfg.goExt()}, cfg.Options)
		if err != nil {
			return nil, err
		}
		if r.Traces["pig-go"], err = writeTrace(out, sc.Name, "pig-go", port); err != nil {
			return nil, err
		}
		r.Pass, r.Detail = verdict(golden, port)
		if !IsEquivalenceProof(golden.Header.Lane) {
			r.Detail += " [" + goldenNote(golden.Header.Lane) + "]"
		}
		r.Fatal = hostFailed(port)
		results = append(results, r)
		if failFast && !r.Pass {
			break
		}
	}
	return results, nil
}

// WriteReport prints one line per scenario plus the details of failures.
func WriteReport(w io.Writer, results []Result) (allPass bool) {
	allPass = true
	for _, r := range results {
		status := "PASS"
		if !r.Pass {
			status, allPass = "FAIL", false
		}
		fmt.Fprintf(w, "%s %s\n", status, r.Scenario)
		for _, line := range strings.Split(r.Detail, "\n") {
			if line != "" {
				fmt.Fprintf(w, "     %s\n", line)
			}
		}
	}
	return allPass
}

// hostFailed reports whether the lane's host failed to start or died mid-run.
func hostFailed(t *Trace) bool {
	for _, e := range t.Events {
		if e.Ch == ChError && (strings.Contains(string(e.Data), "host did not become ready") || strings.Contains(string(e.Data), "host exited early") || strings.Contains(string(e.Data), "host closed its output early")) {
			return true
		}
	}
	return false
}

// Lane runs one lane per scenario and writes the traces under cfg.Out, for
// debugging a divergence. It compares nothing.
func LaneTraces(scs []*Scenario, cfg Config, lane Lane) ([]Result, error) {
	out, err := cfg.outDir()
	if err != nil {
		return nil, err
	}
	var results []Result
	for _, sc := range scs {
		t, err := RunLane(sc, lane, cfg.Options)
		if err != nil {
			return nil, err
		}
		path, err := writeTrace(out, sc.Name, lane.Name, t)
		if err != nil {
			return nil, err
		}
		r := Result{Scenario: sc.Name, Pass: true, Traces: map[string]string{lane.Name: path}, Detail: fmt.Sprintf("%d events: %s", len(t.Events), path)}
		if e, failed := t.Failed(); failed {
			r.Pass, r.Detail = false, fmt.Sprintf("lane failed while running: %s", e.Data)
		}
		results = append(results, r)
	}
	return results, nil
}

// GapScenario is the pseudo-scenario name of the static SDK gap check.
const GapScenario = "sdk-gaps"

// gapResult scans the original TypeScript for Pi APIs the Go SDK lacks. Only a
// static check can see them: a scenario cannot fire an event nobody in the
// Go lane can subscribe to. A blocking gap fails unless the owner accepted it.
func (c Config) gapResult() (Result, error) {
	r := Result{Scenario: GapScenario, Pass: true}
	surface := ""
	if c.Surface != "" {
		b, err := os.ReadFile(c.Surface)
		if err != nil {
			return r, err
		}
		surface = string(b)
	}
	gaps, err := ScanGapsPath(c.TS, surface)
	if err != nil {
		return r, err
	}
	if ok, oerr := PathLooksLikeExtension(c.TS); oerr == nil && !ok {
		r.Pass, r.Detail = false, NotAnExtensionNote()
		return r, nil
	}
	var lines []string
	for _, g := range gaps {
		where := "lines"
		if g.File != "" {
			where = g.File + " lines"
		}
		note := fmt.Sprintf("%s %s (%s %v): %s", strings.ToUpper(g.Severity), g.Symbol, where, g.Lines, g.Note)
		if g.Related > 0 {
			note += fmt.Sprintf(" [+%d related surface rows]", g.Related)
		}
		if reason, ok := c.AcceptedGaps[g.Symbol]; ok {
			note = "ACCEPTED " + note + " [" + reason + "]"
		} else if g.Severity == "missing" {
			r.Pass = false
		}
		lines = append(lines, note)
	}
	if len(lines) == 0 {
		lines = []string{"no Go SDK gaps found for the APIs the source uses"}
	}
	r.Detail = strings.Join(lines, "\n")
	return r, nil
}

// PortGapScenario is the pseudo-scenario name of the static check of the Go port.
const PortGapScenario = "port-gaps"

// portGapResult scans the Go port for what stops it being a public-SDK-only, fusable
// Package (PiG-internal imports, process-global calls). SDK stand-ins it uses are
// listed for review and do not fail. An accepted symbol is listed as ACCEPTED.
func (c Config) portGapResult() (Result, error) {
	r := Result{Scenario: PortGapScenario, Pass: true}
	surface := ""
	if c.Surface != "" {
		b, err := os.ReadFile(c.Surface)
		if err != nil {
			return r, err
		}
		surface = string(b)
	}
	gaps, err := ScanGoPath(c.Go, surface)
	if err != nil {
		return r, err
	}
	var lines []string
	for _, g := range gaps {
		note := fmt.Sprintf("%s %s (%s lines %v): %s", strings.ToUpper(g.Severity), g.Symbol, g.File, g.Lines, g.Note)
		if reason, ok := c.AcceptedGaps[g.Symbol]; ok {
			note = "ACCEPTED " + note + " [" + reason + "]"
		} else if g.Severity == "missing" {
			r.Pass = false
		}
		lines = append(lines, note)
	}
	if len(lines) == 0 {
		lines = []string{"the port imports only the public SDK and uses no process-global call or SDK stand-in"}
	}
	r.Detail = strings.Join(lines, "\n")
	return r, nil
}

// goldenModeMatches refuses to check a self-recorded golden in a different mode from the one it
// was recorded in: with the extension loaded (-e) the host's model request is diffed against a host
// without the port; a built Binary has no such host, so its `llm` events differ. The header's
// extension is "none" when nothing was loaded.
func goldenModeMatches(scenario string, h Header, builtin bool) error {
	if h.Lane != OracleSelf {
		return nil
	}
	recordedBuiltin := h.Extension == "none"
	switch {
	case builtin && !recordedBuiltin:
		return fmt.Errorf("golden %s was recorded with the extension loaded (--go); check it the same way, or record it again with `record --self --builtin --pig <Binary>` to check the Binary", scenario)
	case !builtin && recordedBuiltin:
		return fmt.Errorf("golden %s was recorded from a built Binary (--builtin); check it with --builtin --pig <Binary>, or record it again with --go", scenario)
	}
	return nil
}
