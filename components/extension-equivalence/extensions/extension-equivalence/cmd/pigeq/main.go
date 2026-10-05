// Command pigeq is the extension equivalence harness.
//
//	pigeq run    --scenarios DIR --ts FILE --go DIR --pi PI --pig PIG   oracle and port, live
//	pigeq record --scenarios DIR --golden DIR --ts FILE --pi PI [--pig PIG]
//	pigeq check  --scenarios DIR --golden DIR --go DIR --pig PIG        port against golden traces
//	pigeq mutate --scenarios DIR --golden DIR --go DIR --pig PIG --mutations FILE [--unit --sdk-dir DIR]
//	pigeq lane   --scenarios DIR --host pi|pig --bin EXE --ext PATH --out DIR   one lane, traces only
//	pigeq gaps   --ts FILE|DIR | --go DIR [--surface FILE] [--accept-gaps FILE]
//	                                                                    Pi APIs the Go SDK lacks (TS), or PiG-internal
//	                                                                    imports, fuse hazards and SDK stand-ins (Go)
//
// The oracle is one of --ts (TypeScript original under Pi), --go-oracle (a Go original under
// PiG) or --self (no original: record the port as its own regression baseline, never proof).
//
//	pigeq diff   WANT.jsonl GOT.jsonl
//	pigeq env    --root DIR [--pig PIG] [--pi PI] [--source DIR] [--module DIR]...
//	                                                                    print an isolated shell environment (source it);
//	                                                                    with --pig and --module also writes DIR/go.work
//	pigeq twins  list --tests DIR > ledger.json                         upstream test titles per file
//	pigeq twins  check --ledger ledger.json --go DIR [--files a,b]       every title has a tw() twin or a named tskip()
//	pigeq llm    --script turns.json [--log requests.jsonl]              serve the scripted model (prints its base URL)
//	pigeq source --from PIG-REPO --rev REV --out DIR                    export a PiG revision as a one-commit git checkout
//	                                                                    (PIG_SOURCE_ROOT for `pig piglet build`)
//
// PIGEQ_PI and PIGEQ_PIG supply the host executables. Exit status is 0 only when
// every scenario passes (or, for mutate, every mutation is caught).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MichaelKinsy/pigpen/extension-equivalence/eq"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pigeq run|record|check|mutate|lane|gaps|diff|env|source|twins|llm [flags]")
		return 2
	}
	cmd, rest := args[0], args[1:]
	if cmd == "env" || cmd == "source" {
		return session(cmd, rest, stdout, stderr)
	}
	if cmd == "twins" {
		return twins(rest, stdout, stderr)
	}
	if cmd == "llm" {
		return llm(rest, stdout, stderr)
	}
	fs := flag.NewFlagSet("pigeq "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		scenarios = fs.String("scenarios", "", "scenario file or directory")
		golden    = fs.String("golden", "", "directory of golden traces")
		ts        = fs.String("ts", "", "the original TypeScript extension (oracle)")
		goDir     = fs.String("go", "", "the Go port directory (gaps: the Go source to scan)")
		goOracle  = fs.String("go-oracle", "", "an original that is already a Go extension (oracle, run under PiG)")
		self      = fs.Bool("self", false, "no original exists: record the port as its own regression baseline (not equivalence)")
		pi        = fs.String("pi", os.Getenv("PIGEQ_PI"), "pi executable (default $PIGEQ_PI)")
		pig       = fs.String("pig", os.Getenv("PIGEQ_PIG"), "pig executable (default $PIGEQ_PIG)")
		out       = fs.String("out", "", "directory for recorded traces")
		mutations = fs.String("mutations", "", "mutation list (JSON)")
		skipHost  = fs.Bool("skip-host-check", false, "do not run the original under PiG")
		host      = fs.String("host", "", "lane host: pi or pig")
		bin       = fs.String("bin", "", "lane host executable")
		ext       = fs.String("ext", "", "lane extension (TypeScript file or Go directory)")
		surface   = fs.String("surface", os.Getenv("PIGEQ_SURFACE"), "PiG docs/extension-sdk-surface.md (default: embedded snapshot)")
		accept    = fs.String("accept-gaps", "", "JSON object mapping an accepted gap symbol to its owner approval")
		sdkDir    = fs.String("sdk-dir", os.Getenv("PIG_SDK_DIR"), "PiG's staged Go SDK (mutate: enables the unit-test layer)")
		unitTests = fs.Bool("unit", false, "mutate: also run the port's go tests on each mutant first (needs --sdk-dir)")
		builtin   = fs.Bool("builtin", false, "check, record --self: the port is compiled into --pig (a built Piglet Binary); load no extension directory")
		unitOnly  = fs.Bool("unit-only", false, "mutate: judge mutants by the port's own go tests alone; no scenarios, golden traces or pig (for an original adapter)")
		jobs      = fs.Int("jobs", 1, "mutate: how many mutants to run at once (each has its own copy)")
		keep      = fs.Bool("keep", false, "keep each lane's run directory")
		stepSecs  = fs.Int("step-timeout", 0, "seconds to wait for each scenario step (default 60; 20 for mutants)")
	)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	var accepted map[string]string
	if *accept != "" {
		b, err := os.ReadFile(*accept)
		if err == nil {
			err = json.Unmarshal(b, &accepted)
		}
		if err != nil {
			fmt.Fprintln(stderr, "pigeq: --accept-gaps:", err)
			return 2
		}
	}
	cfg := eq.Config{Surface: *surface, AcceptedGaps: accepted, Pi: *pi, Pig: *pig, TS: *ts, Go: *goDir, GoOracle: *goOracle, Self: *self, Builtin: *builtin, Out: *out, SkipHostCheck: *skipHost,
		Options: eq.Options{Keep: *keep, Stderr: stderr, StepTimeout: time.Duration(*stepSecs) * time.Second}}
	need := func(pairs ...string) bool {
		for i := 0; i < len(pairs); i += 2 {
			if pairs[i+1] == "" {
				fmt.Fprintf(stderr, "pigeq %s: %s is required\n", cmd, pairs[i])
				return false
			}
		}
		return true
	}
	if cmd == "gaps" {
		if (*ts == "") == (*goDir == "") {
			fmt.Fprintln(stderr, "pigeq gaps: give --ts or --go (a TypeScript original or Go source), not both and not neither")
			return 2
		}
		surf := ""
		if *surface != "" {
			b, err := os.ReadFile(*surface)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			surf = string(b)
		}
		var gaps []eq.Gap
		var err error
		if *ts != "" {
			if ok, oerr := eq.PathLooksLikeExtension(*ts); oerr == nil && !ok {
				fmt.Fprint(stdout, eq.NotAnExtensionNote())
				return 1
			}
			gaps, err = eq.ScanGapsPath(*ts, surf)
		} else {
			gaps, err = eq.ScanGoPath(*goDir, surf)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprint(stdout, eq.FormatGaps(gaps))
		// An owner-approved exclusion (--accept-gaps) does not block; it is still listed.
		if open := eq.Unaccepted(gaps, accepted); len(open) > 0 {
			return 1
		}
		for _, g := range gaps {
			if reason, ok := accepted[g.Symbol]; ok {
				fmt.Fprintf(stdout, "ACCEPTED %s: %s\n", g.Symbol, reason)
			}
		}
		return 0
	}
	if cmd == "diff" {
		if fs.NArg() != 2 {
			fmt.Fprintln(stderr, "usage: pigeq diff WANT.jsonl GOT.jsonl")
			return 2
		}
		want, err := eq.ReadTrace(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		got, err := eq.ReadTrace(fs.Arg(1))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if d := eq.Diff(want, got); d != nil {
			fmt.Fprintln(stdout, "DIFFERENT:", d)
			return 1
		}
		fmt.Fprintf(stdout, "IDENTICAL (%d events)\n", len(want.Events))
		return 0
	}
	if cmd == "mutate" && *builtin {
		fmt.Fprintln(stderr, "pigeq mutate: cannot mutate a built binary; mutate the port's source directory (--go)")
		return 2
	}
	if cmd == "mutate" && *unitOnly {
		return mutateUnitOnly(cfg, *mutations, *sdkDir, *jobs, stdout, stderr, need)
	}
	if !need("--scenarios", *scenarios) {
		return 2
	}
	scs, err := eq.LoadScenarios(*scenarios)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var results []eq.Result
	switch cmd {
	case "run":
		if !need("--go", *goDir, "--pig", *pig) {
			return 2
		}
		results, err = eq.Run(scs, cfg)
	case "lane":
		if !need("--host", *host, "--bin", *bin, "--ext", *ext) {
			return 2
		}
		results, err = eq.LaneTraces(scs, cfg, eq.Lane{Name: *host + "-lane", Host: *host, Bin: *bin, Ext: *ext})
	case "record":
		if !need("--golden", *golden) {
			return 2
		}
		if *ts == "" && *goOracle == "" && !*self {
			fmt.Fprintln(stderr, "pigeq record: give the oracle: --ts, --go-oracle or --self")
			return 2
		}
		results, err = eq.Record(scs, cfg, *golden)
	case "check":
		if *builtin {
			cfg.Builtin = true
			if !need("--golden", *golden, "--pig", *pig) {
				return 2
			}
		} else if !need("--golden", *golden, "--go", *goDir, "--pig", *pig) {
			return 2
		}
		results, err = eq.Check(scs, cfg, *golden)
	case "mutate":
		if !need("--golden", *golden, "--go", *goDir, "--pig", *pig, "--mutations", *mutations) {
			return 2
		}
		list, lerr := eq.LoadMutations(*mutations)
		if lerr != nil {
			fmt.Fprintln(stderr, lerr)
			return 2
		}
		var unit *eq.UnitTest
		if *unitTests {
			if *sdkDir == "" {
				fmt.Fprintln(stderr, "pigeq mutate: --unit needs --sdk-dir (or $PIG_SDK_DIR)")
				return 2
			}
			unit = eq.NewUnitTest(*sdkDir)
		}
		cfg.Jobs = *jobs
		mres, merr := eq.Mutate(scs, cfg, *golden, list, unit)
		if merr != nil {
			fmt.Fprintln(stderr, merr)
			return 2
		}
		return reportMutations(stdout, mres)
	default:
		fmt.Fprintf(stderr, "pigeq: unknown command %q\n", cmd)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !eq.WriteReport(stdout, results) {
		return 1
	}
	return 0
}

// session implements the two commands that prepare a porting session.
func session(cmd string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pigeq "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "env: scratch root; home, pighome and agent live under it (default: a new private directory)")
	pig := fs.String("pig", os.Getenv("PIGEQ_PIG"), "env: pig executable")
	pi := fs.String("pi", os.Getenv("PIGEQ_PI"), "env: pi executable")
	source := fs.String("source", os.Getenv("PIG_SOURCE_ROOT"), "env: git checkout of the PiG source the pig was built from")
	var modules []string
	fs.Func("module", "env: a Go module directory of the port (repeatable); needs --pig", func(v string) error {
		abs, err := filepath.Abs(v)
		modules = append(modules, abs)
		return err
	})
	from := fs.String("from", "", "source: a PiG git repository")
	rev := fs.String("rev", "", "source: the revision to export (a full commit)")
	out := fs.String("out", "", "source: the new directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if cmd == "source" {
		if *from == "" || *rev == "" || *out == "" {
			fmt.Fprintln(stderr, "pigeq source: --from, --rev and --out are required")
			return 2
		}
		abs, err := filepath.Abs(*out)
		if err == nil {
			err = eq.SnapshotSource(*from, *rev, abs)
		}
		if err != nil {
			fmt.Fprintln(stderr, "pigeq source:", err)
			return 2
		}
		fmt.Fprintf(stdout, "export PIG_SOURCE_ROOT=%s\n", abs)
		return 0
	}
	if *root == "" {
		// A private directory per call: lanes that each picked /tmp/<name> overwrote one another's env.sh.
		dir, err := os.MkdirTemp("", "pigeq-")
		if err != nil {
			fmt.Fprintln(stderr, "pigeq env:", err)
			return 2
		}
		*root = dir
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "pigeq env:", err)
		return 2
	}
	if *pi != "" && *pig != "" {
		if err := eq.CheckPiVersion(*pi, *pig); err != nil {
			fmt.Fprintln(stderr, "pigeq env:", err)
			return 2
		}
	}
	spec, err := eq.DiscoverToolchain()
	if err != nil {
		fmt.Fprintln(stderr, "pigeq env:", err)
		return 2
	}
	spec.Root, spec.Pig, spec.Pi, spec.Source = abs, *pig, *pi, *source
	if *pig != "" && len(modules) > 0 {
		sdk, err := eq.SDKPath(*pig, abs)
		if err == nil {
			spec.SDKDir, spec.GoWork = sdk, filepath.Join(abs, "go.work")
			err = eq.WriteGoWork(spec.GoWork, sdk, modules)
		}
		if err != nil {
			fmt.Fprintln(stderr, "pigeq env:", err)
			return 2
		}
	}
	fmt.Fprint(stdout, eq.EnvScript(spec))
	return 0
}

// twins implements `pigeq twins list|check`.
func twins(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "check") {
		fmt.Fprintln(stderr, "usage: pigeq twins list --tests DIR | pigeq twins check --ledger FILE --go DIR [--files a,b] [--deferred slices.json] [--max-same-reason N]")
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("pigeq twins "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	tests := fs.String("tests", "", "list: the upstream test directory")
	ledgerPath := fs.String("ledger", "", "check: the ledger written by list")
	goDir := fs.String("go", "", "check: the port directory with the Go twins")
	files := fs.String("files", "", "check: only these ledger files (the slice shipped so far), comma separated")
	deferred := fs.String("deferred", "", "check: JSON map of ledger file -> reason; the file's cases without a twin or named skip are deferred to a later slice (stated once)")
	same := fs.Int("max-same-reason", 0, "check: most skips that may share one reason (default 3)")
	twinFn := fs.String("twin", "tw", "check: name of the twin helper")
	skipFn := fs.String("skip", "tskip", "check: name of the named-skip helper")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if sub == "list" {
		if *tests == "" {
			fmt.Fprintln(stderr, "pigeq twins list: --tests is required")
			return 2
		}
		ledger, err := eq.ListUpstreamTitles(*tests)
		if err != nil {
			fmt.Fprintln(stderr, "pigeq twins list:", err)
			return 2
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", " ")
		if err := enc.Encode(ledger); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		return 0
	}
	if *ledgerPath == "" || *goDir == "" {
		fmt.Fprintln(stderr, "pigeq twins check: --ledger and --go are required")
		return 2
	}
	var ledger map[string][]string
	b, err := os.ReadFile(*ledgerPath)
	if err == nil {
		err = json.Unmarshal(b, &ledger)
	}
	if err != nil {
		fmt.Fprintln(stderr, "pigeq twins check:", err)
		return 2
	}
	opts := eq.TwinOptions{MaxSameReason: *same, TwinFunc: *twinFn, SkipFunc: *skipFn}
	if *deferred != "" {
		db, derr := os.ReadFile(*deferred)
		if derr == nil {
			derr = json.Unmarshal(db, &opts.Deferred)
		}
		if derr != nil {
			fmt.Fprintln(stderr, "pigeq twins check:", derr)
			return 2
		}
	}
	if *files != "" {
		opts.Files = strings.Split(*files, ",")
	}
	rep, err := eq.CheckTwins(ledger, *goDir, opts)
	if err != nil {
		fmt.Fprintln(stderr, "pigeq twins check:", err)
		return 2
	}
	fmt.Fprint(stdout, rep.Summary())
	if !rep.OK() {
		return 1
	}
	return 0
}

// llm serves the scripted OpenAI-compatible model until stdin closes or a signal arrives.
func llm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pigeq llm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	script := fs.String("script", "", "JSON array of turns: [{\"text\":\"...\"},{\"toolCalls\":[{\"name\":\"bash\",\"arguments\":{}}]}]")
	logPath := fs.String("log", "", "write each request as a JSON line to this file (default: none)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *script == "" {
		fmt.Fprintln(stderr, "pigeq llm: --script is required")
		return 2
	}
	var turns []eq.Turn
	b, err := os.ReadFile(*script)
	if err == nil {
		err = json.Unmarshal(b, &turns)
	}
	if err != nil {
		fmt.Fprintln(stderr, "pigeq llm:", err)
		return 2
	}
	var log io.Writer
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fmt.Fprintln(stderr, "pigeq llm:", err)
			return 2
		}
		defer f.Close()
		log = f
	}
	url, stop, err := eq.ServeLLM(turns, log)
	if err != nil {
		fmt.Fprintln(stderr, "pigeq llm:", err)
		return 2
	}
	defer stop()
	fmt.Fprintln(stdout, url)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(done) }()
	select {
	case <-sig:
	case <-done:
	}
	return 0
}

// reportMutations prints one status per mutant and the summary line; it returns the exit status.
func reportMutations(stdout io.Writer, mres []eq.MutationResult) int {
	survived := 0
	for _, m := range mres {
		status := "KILLED  "
		if m.Invalid {
			status = "INVALID "
			survived++
		} else if !m.Killed {
			status = "SURVIVED"
			survived++
		}
		fmt.Fprintf(stdout, "%s %s\n         %s\n", status, m.Mutation.Name, m.Detail)
	}
	fmt.Fprintf(stdout, "%d mutation(s), %d not killed\n", len(mres), survived)
	if survived > 0 {
		return 1
	}
	return 0
}

// mutateUnitOnly runs mutants against the port's own tests only.
func mutateUnitOnly(cfg eq.Config, mutations, sdkDir string, jobs int, stdout, stderr io.Writer, need func(pairs ...string) bool) int {
	if !need("--go", cfg.Go, "--mutations", mutations, "--sdk-dir", sdkDir) {
		return 2
	}
	list, err := eq.LoadMutations(mutations)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg.UnitOnly, cfg.Jobs = true, jobs
	mres, err := eq.Mutate(nil, cfg, "", list, eq.NewUnitTest(sdkDir))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return reportMutations(stdout, mres)
}
