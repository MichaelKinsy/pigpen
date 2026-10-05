/**
 * B3 — Long-session ledger simulation.
 * readGoalLedger / reconstructGoalLedger at 1k / 5k / 10k events. BEFORE the
 * P1-2 change this must grow with session size (O(n)); the after-run must be
 * flat. The growth ratio 10k/1k is the headline number.
 */

import { makeFixtureCwd, makeLedger, measure, Baseline, cleanupFixture } from "./bench-common.mjs";
import { readGoalLedger, reconstructGoalLedger } from "../../extensions/goal-ledger.ts";

export function run(baseline) {
	for (const count of [1000, 5000, 10000]) {
		const cwd = makeFixtureCwd("b3-");
		try {
			makeLedger(cwd, count);
			const r = measure(() => readGoalLedger({ cwd }), { n: 10 });
			baseline.add({ id: `B3.parse.${count}`, label: `ledger full parse (${count} events)`, modules: "goal-ledger", fixture: `${count} events`, n: r.n, p50: r.p50, p95: r.p95, max: r.max, notes: `mean ${r.mean}ms` });
			// NAF: the event list is built once, outside the timed closure —
			// reconstruction is measured over an existing in-memory event list
			// (as it runs in production, over the ledger read result). The
			// original harness built the fixture (incl. new Date().toISOString()
			// per event) inside the timer, inflating the before numbers.
			const events = Array(count).fill(null).map((_, i) => ({ type: "goal_created", goalId: `g${i}`, objective: "x", sisyphus: false, autoContinue: true, at: "2026-08-06T00:00:00.000Z" }));
			const rc = measure(() => reconstructGoalLedger(events), { n: 30 }); // n=30: sub-millisecond p50 is noisy at n=10
			baseline.add({ id: `B3.reconstruct.${count}`, label: `ledger reconstruction (${count} events)`, modules: "goal-ledger", fixture: `${count} in-memory events`, n: rc.n, p50: rc.p50, p95: rc.p95, max: rc.max, notes: `mean ${rc.mean}ms` });
		} finally {
			cleanupFixture(cwd);
		}
	}
	return baseline;
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const baseline = new Baseline();
	run(baseline);
	process.stdout.write(baseline.markdown());
}
