// Package extension_equivalence exposes the equivalence harness to a PiG agent
// as three tools: equivalence_gaps (static check for Pi APIs the Go SDK lacks),
// equivalence_run (the differential run and the golden-trace check) and
// equivalence_diff (compare two recorded traces). The same code is the pigeq
// command line, so a person and an agent get identical verdicts.
package extension_equivalence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/extension-equivalence/eq"
)

// Extension returns the equivalence tools.
func Extension() *sdk.Extension {
	e := sdk.New("extension-equivalence")

	e.Tool("equivalence_gaps",
		"Scan a Pi TypeScript extension (ts) for Pi APIs the Go SDK does not provide (for example pi.events, withSession), or Go source (go) for PiG-internal imports, process-global calls PiG's fuse check rejects and SDK stand-ins. Run it before porting and again on the port: a blocking finding needs a PiG SDK feature or an owner-approved exclusion.",
		sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"ts":      map[string]any{"type": "string", "description": "Path to the TypeScript extension file, or its directory for a multi-file extension"},
				"go":      map[string]any{"type": "string", "description": "Path to Go source (an original or the port) to scan instead of ts"},
				"surface": map[string]any{"type": "string", "description": "Optional PiG docs/extension-sdk-surface.md; the embedded snapshot is used when omitted"},
			},
		},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			if (strings.TrimSpace(str(params, "ts")) == "") == (strings.TrimSpace(str(params, "go")) == "") {
				return nil, fmt.Errorf("give ts (a TypeScript original) or go (Go source), not both and not neither")
			}
			surface := ""
			if p := str(params, "surface"); p != "" {
				b, err := os.ReadFile(resolve(ctx, p))
				if err != nil {
					return nil, err
				}
				surface = string(b)
			}
			var gaps []eq.Gap
			var err error
			if str(params, "ts") != "" {
				if ok, oerr := eq.PathLooksLikeExtension(resolve(ctx, str(params, "ts"))); oerr == nil && !ok {
					return verdictText(false, eq.NotAnExtensionNote()), nil
				}
				gaps, err = eq.ScanGapsPath(resolve(ctx, str(params, "ts")), surface)
			} else {
				gaps, err = eq.ScanGoPath(resolve(ctx, str(params, "go")), surface)
			}
			if err != nil {
				return nil, err
			}
			return verdictText(!eq.Blocking(gaps), eq.FormatGaps(gaps)), nil
		})

	e.Tool("equivalence_run",
		"Run the equivalence harness. mode=run executes the original under Pi and under PiG and the Go port under PiG with the same scripted scenarios and requires identical traces. mode=check compares only the Go port with recorded golden traces. mode=record writes golden traces from Pi. mode=mutate applies each deliberate defect in mutations to a copy of the port and requires the golden traces (and, with sdkDir, the port's Go tests) to catch it.",
		sdk.Schema{
			"type":     "object",
			"required": []any{"mode"},
			"properties": map[string]any{
				"mode":           map[string]any{"type": "string", "enum": []any{"run", "check", "record", "mutate"}},
				"mutations":      map[string]any{"type": "string", "description": "Mutation list, JSON (mutate)"},
				"sdkDir":         map[string]any{"type": "string", "description": "PiG's staged Go SDK (mutate: also run the port's go tests on each mutant; default $PIG_SDK_DIR)"},
				"scenarios":      map[string]any{"type": "string", "description": "Scenario file or directory"},
				"golden":         map[string]any{"type": "string", "description": "Golden trace directory (check and record)"},
				"ts":             map[string]any{"type": "string", "description": "The original TypeScript extension (oracle: run under Pi)"},
				"goOracle":       map[string]any{"type": "string", "description": "An original that is already a Go extension (oracle: run under PiG); use instead of ts"},
				"self":           map[string]any{"type": "boolean", "description": "No original exists: record the port as its own regression baseline (record mode). Not equivalence."},
				"go":             map[string]any{"type": "string", "description": "The Go port directory"},
				"pi":             map[string]any{"type": "string", "description": "pi executable (default $PIGEQ_PI)"},
				"pig":            map[string]any{"type": "string", "description": "pig executable (default $PIGEQ_PIG)"},
				"out":            map[string]any{"type": "string", "description": "Directory for recorded traces"},
				"acceptGaps":     map[string]any{"type": "object", "description": "Accepted gap symbol -> owner approval text"},
				"surface":        map[string]any{"type": "string", "description": "PiG docs/extension-sdk-surface.md"},
				"skipHostRun":    map[string]any{"type": "boolean", "description": "Skip running the original under PiG"},
				"stepTimeoutSec": map[string]any{"type": "number", "description": "Seconds to wait for each scenario step (default 60; 20 for mutants)"},
				"unitOnly":       map[string]any{"type": "boolean", "description": "mutate: judge mutants by the port's own Go tests alone (needs sdkDir); no scenarios, golden traces or pig, for a port with no scenarios"},
				"jobs":           map[string]any{"type": "number", "description": "mutate: how many mutants to run at once (default 1; each has its own copy)"},
			},
		},
		runTool)

	e.Tool("equivalence_twins",
		"The twin contract for a port whose original has tests. mode=list writes the ledger of upstream test titles per file (JSON). mode=check verifies that every ledger title has a Go twin (tw) or a named skip (tskip) in the port's tests, that no twin carries an unknown title, and that skips do not share one blanket reason. The passing report is the 'N exact twins, M skipped' line for PORT.md.",
		sdk.Schema{
			"type":     "object",
			"required": []any{"mode"},
			"properties": map[string]any{
				"mode":          map[string]any{"type": "string", "enum": []any{"list", "check"}},
				"tests":         map[string]any{"type": "string", "description": "list: the upstream test directory"},
				"ledger":        map[string]any{"type": "string", "description": "check: the ledger JSON written by list"},
				"go":            map[string]any{"type": "string", "description": "check: the port directory with the Go twins"},
				"files":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "check: only these ledger files (the slice shipped so far)"},
				"maxSameReason": map[string]any{"type": "number", "description": "check: most skips that may share one reason (default 3)"},
				"deferred":      map[string]any{"type": "object", "description": "check: ledger file -> reason; that file's cases without a twin or named skip are deferred to a later slice (stated once)"},
			},
		},
		twinsTool)

	e.Tool("equivalence_diff",
		"Compare two recorded traces (JSON lines) and report the first difference.",
		sdk.Schema{
			"type":     "object",
			"required": []any{"want", "got"},
			"properties": map[string]any{
				"want": map[string]any{"type": "string"},
				"got":  map[string]any{"type": "string"},
			},
		},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			want, err := eq.ReadTrace(resolve(ctx, str(params, "want")))
			if err != nil {
				return nil, err
			}
			got, err := eq.ReadTrace(resolve(ctx, str(params, "got")))
			if err != nil {
				return nil, err
			}
			if d := eq.Diff(want, got); d != nil {
				return verdictText(false, d.String()), nil
			}
			return verdictText(true, fmt.Sprintf("identical (%d events)", len(want.Events))), nil
		})
	return e
}

func runTool(ctx sdk.Context, params map[string]any) (any, error) {
	mode := str(params, "mode")
	var scs []*eq.Scenario
	if !(mode == "mutate" && params["unitOnly"] == true) {
		if str(params, "scenarios") == "" {
			return nil, fmt.Errorf("scenarios is required for this mode")
		}
		var err error
		if scs, err = eq.LoadScenarios(resolve(ctx, str(params, "scenarios"))); err != nil {
			return nil, err
		}
	}
	var err error
	var stderr bytes.Buffer
	cfg := eq.Config{
		Pi:            firstNonEmpty(str(params, "pi"), os.Getenv("PIGEQ_PI")),
		Pig:           firstNonEmpty(str(params, "pig"), os.Getenv("PIGEQ_PIG")),
		TS:            resolve(ctx, str(params, "ts")),
		Go:            resolve(ctx, str(params, "go")),
		GoOracle:      resolve(ctx, str(params, "goOracle")),
		Self:          params["self"] == true,
		Out:           resolve(ctx, str(params, "out")),
		Surface:       resolve(ctx, str(params, "surface")),
		SkipHostCheck: params["skipHostRun"] == true,
		Options:       eq.Options{Stderr: &stderr, StepTimeout: time.Duration(num(params, "stepTimeoutSec") * float64(time.Second))},
	}
	if m, ok := params["acceptGaps"].(map[string]any); ok {
		cfg.AcceptedGaps = map[string]string{}
		for k, v := range m {
			cfg.AcceptedGaps[k], _ = v.(string)
		}
	}
	golden := resolve(ctx, str(params, "golden"))
	var results []eq.Result
	switch mode {
	case "run":
		err = need(cfg.Pig, "pig", str(params, "go"), "go")
		if err == nil {
			results, err = eq.Run(scs, cfg)
		}
	case "record":
		err = need(str(params, "golden"), "golden")
		if err == nil && cfg.TS == "" && cfg.GoOracle == "" && !cfg.Self {
			err = fmt.Errorf("record needs the oracle: ts (TypeScript original under Pi), goOracle (Go original under PiG) or self (no original: regression baseline only)")
		}
		if err == nil {
			results, err = eq.Record(scs, cfg, golden)
		}
	case "check":
		err = need(cfg.Pig, "pig", str(params, "go"), "go", str(params, "golden"), "golden")
		if err == nil {
			results, err = eq.Check(scs, cfg, golden)
		}
	case "mutate":
		if params["unitOnly"] == true {
			err = need(str(params, "go"), "go", str(params, "mutations"), "mutations")
		} else {
			err = need(cfg.Pig, "pig", str(params, "go"), "go", str(params, "golden"), "golden", str(params, "mutations"), "mutations")
		}
		if err == nil {
			return mutateTool(ctx, scs, cfg, golden, params)
		}
	default:
		err = fmt.Errorf("mode must be run, check, record or mutate, got %q", mode)
	}
	if err != nil {
		return nil, err
	}
	var report bytes.Buffer
	pass := eq.WriteReport(&report, results)
	return verdictText(pass, report.String()), nil
}

func mutateTool(ctx sdk.Context, scs []*eq.Scenario, cfg eq.Config, golden string, params map[string]any) (any, error) {
	list, err := eq.LoadMutations(resolve(ctx, str(params, "mutations")))
	if err != nil {
		return nil, err
	}
	var unit *eq.UnitTest
	if dir := firstNonEmpty(resolve(ctx, str(params, "sdkDir")), os.Getenv("PIG_SDK_DIR")); dir != "" {
		unit = eq.NewUnitTest(dir)
	}
	cfg.Jobs = int(num(params, "jobs"))
	cfg.UnitOnly = params["unitOnly"] == true
	if cfg.UnitOnly && unit == nil {
		return nil, fmt.Errorf("unitOnly needs sdkDir (or $PIG_SDK_DIR)")
	}
	results, err := eq.Mutate(scs, cfg, golden, list, unit)
	if err != nil {
		return nil, err
	}
	var report bytes.Buffer
	notKilled := 0
	for _, m := range results {
		status := "KILLED  "
		if m.Invalid {
			status = "INVALID "
			notKilled++
		} else if !m.Killed {
			status = "SURVIVED"
			notKilled++
		}
		fmt.Fprintf(&report, "%s %s\n         %s\n", status, m.Mutation.Name, m.Detail)
	}
	fmt.Fprintf(&report, "%d mutation(s), %d not killed\n", len(results), notKilled)
	return verdictText(notKilled == 0, report.String()), nil
}

func verdictText(pass bool, body string) string {
	if pass {
		return "PASS\n" + body
	}
	return "FAIL\n" + body
}

func str(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

func num(params map[string]any, key string) float64 {
	f, _ := params[key].(float64)
	return f
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// need takes value/name pairs and reports the first empty value.
func need(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "" {
			return fmt.Errorf("%s is required for this mode", pairs[i+1])
		}
	}
	return nil
}

// resolve anchors a relative path at the session's working directory.
func resolve(ctx sdk.Context, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(ctx.Cwd(), path)
}

func twinsTool(ctx sdk.Context, params map[string]any) (any, error) {
	switch str(params, "mode") {
	case "list":
		if str(params, "tests") == "" {
			return nil, fmt.Errorf("tests is required for mode list")
		}
		ledger, err := eq.ListUpstreamTitles(resolve(ctx, str(params, "tests")))
		if err != nil {
			return nil, err
		}
		b, err := json.MarshalIndent(ledger, "", " ")
		return string(b), err
	case "check":
		if err := need(str(params, "ledger"), "ledger", str(params, "go"), "go"); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(resolve(ctx, str(params, "ledger")))
		if err != nil {
			return nil, err
		}
		var ledger map[string][]string
		if err := json.Unmarshal(b, &ledger); err != nil {
			return nil, err
		}
		opts := eq.TwinOptions{MaxSameReason: int(num(params, "maxSameReason"))}
		if d, ok := params["deferred"].(map[string]any); ok {
			opts.Deferred = map[string]string{}
			for f, r := range d {
				opts.Deferred[f], _ = r.(string)
			}
		}
		if files, ok := params["files"].([]any); ok {
			for _, f := range files {
				if s, ok := f.(string); ok {
					opts.Files = append(opts.Files, s)
				}
			}
		}
		rep, err := eq.CheckTwins(ledger, resolve(ctx, str(params, "go")), opts)
		if err != nil {
			return nil, err
		}
		return verdictText(rep.OK(), rep.Summary()), nil
	}
	return nil, fmt.Errorf("mode must be list or check")
}
