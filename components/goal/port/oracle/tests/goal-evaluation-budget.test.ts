import test from "node:test";
import assert from "node:assert/strict";
// @ts-expect-error The offline evaluation harness is JavaScript.
import { EvaluationBudget } from "../experiments/live/budget.mjs";

const model = { provider: "fixture", id: "fixture", contextWindow: 1_000_000, maxTokens: 10_000, cost: { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 1 } };
const usage = { input: 100, output: 20, cacheRead: 30, cacheWrite: 0, cost: { total: 0.000143 } };

test("evaluation allowance reserves concurrent parent/child requests before dispatch", async () => {
	const budget = new EvaluationBudget({ limit: 2.1 });
	let calls = 0;
	const pending: Array<(value: unknown) => void> = [];
	const runtime = { streamSimple: (_model: unknown, _context: unknown, options: any) => {
		calls++;
		assert.equal(options.maxTokens, 8192);
		assert.equal(options.maxRetries, 0);
		const result = new Promise(resolve => pending.push(resolve));
		return { result: () => result };
	} };
	budget.install(runtime);
	runtime.streamSimple(model, {}, {});
	runtime.streamSimple(model, {}, {});
	assert.throws(() => runtime.streamSimple(model, {}, {}), /ceiling/);
	assert.equal(calls, 2);
	assert.ok(budget.reserved > 2);
	for (const resolve of pending) resolve({ usage });
	await Promise.resolve();
	assert.equal(budget.reserved, 0);
	assert.equal(budget.spent, 0.000286);
	assert.equal(budget.requests[0].cacheRead, 30);
});

test("unknown billing consumes its reservation and invalid pricing fails closed", async () => {
	const budget = new EvaluationBudget({ limit: 2 });
	const runtime = { streamSimple: (..._args: unknown[]) => ({ result: async () => ({ errorMessage: "interrupted" }) }) };
	budget.install(runtime);
	runtime.streamSimple(model, {}, {});
	await Promise.resolve();
	assert.equal(budget.spent, budget.reservation(model));
	assert.equal(budget.requests[0].uncertain, true);
	assert.equal(budget.canRunPair(model), false);
	assert.throws(() => runtime.streamSimple({ ...model, cost: {} }, {}, {}), /price/);
	assert.equal(budget.requests.length, 1);
	assert.throws(() => new EvaluationBudget({ maxTokens: NaN }), /allowance/);
});
