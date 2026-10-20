package warden

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// bench is `/warden bench [--runs N]` (extension.ts 2601-2615): the rules check of one built-in sample, `runs` times,
// with the latency and cost it measured. runBench and its formatting are the rules lane's (rules_audit.go).
func (w *warden) bench(ctx sdk.Context, tokens []string) error {
	parsed := parseBenchArgs(tokens)
	if parsed.Error != "" {
		w.say(ctx, parsed.Error, "warning")
		return nil
	}
	cfg := w.config(ctx)
	judge, off := w.judgeForCommand(ctx, cfg)
	options := BenchOptions{Ctx: newContext(), Runs: parsed.Runs, Cwd: ctx.Cwd(), Config: cfg.Rules, Set: w.rules.guard.Store.Load(ctx.Cwd(), cfg.Rules), Judge: judge, Timeout: w.timeout(cfg)}
	if judge != nil {
		options.GetUsage = w.budget.Usage
	}
	result := runBench(options)
	switch result.Status {
	// No key, no consent, or a spent budget: say why and send nothing.
	case "no-judge":
		w.say(ctx, "Bench sent nothing: Jev judgments are off"+off, "info")
	case "skipped":
		w.say(ctx, "Bench sent nothing: "+result.Reason+".", "info")
	case "done":
		w.say(ctx, formatBench(result), "info")
	}
	return nil
}
