/**
 * Frame recorder: runs the UNMODIFIED pi-schedule-prompt 0.4.1 JobsView (the `/schedule-prompt` Jobs overlay) and runAddFlow (its add
 * flow) from port/oracle under Pi 1402973a9d's real pi-tui, replays every scenario in port/frames/scenarios/*.json against them and
 * writes what they did to port/frames/golden/<name>.json. The Go test (extensions/schedule-prompt/frames_test.go) replays the same
 * scenarios against the port's jobsView and runAddFlow and compares everything.
 *
 * Regenerate (Node 24, TZ=UTC, from this directory):   PI_SRC=<built Pi checkout at 1402973a9d> ./record.sh
 * The goldens committed here were recorded on the xd670 host in the tern-ci:v3 image (Node 24.19.0).
 *
 * What is real: JobsView, runAddFlow, CronStorage (a file store in a scratch directory), CronScheduler's static helpers
 * (isLoadedFor, describeSchedule, validateSchedule), pi-tui's matchesKey. What is faked: the scheduler instance (a recorder that
 * answers getNextRun from the scenario and records removeJob and updateJob), the theme (fg and bold emit tags such as
 * `<fg:dim>text</fg>` and `<b>text</b>`: the colour tokens and their nesting are compared, the SGR spelling is the host's) and
 * ctx.ui (input, select, confirm and notify answer from the scenario and are recorded).
 *
 * Scenario kinds:
 *   jobs:    { session, jobs, nextRuns, widths, runAdd: "ok"|"fail"|"adds", addJob?, steps: [raw input, ...] }
 *            The golden holds, for the start and after each step: the frames at every width, whether done() ran, the store file,
 *            the scheduler calls, how many times runAdd ran and requestRender was called.
 *   addflow: { session, settings, jobs, answers: [string | null | boolean, ...] }
 *            The golden holds every ui call in order, the notifications, the store file and the scheduler calls (id and createdAt
 *            are made up on every run and recorded as <id> and <now>).
 */
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { runAddFlow } from "../../oracle/src/ui/add-flow.ts";
import { JobsView } from "../../oracle/src/ui/jobs-view.ts";
import { CronStorage } from "../../oracle/src/storage.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const framesDir = path.resolve(here, "..");

if (process.env.TZ !== "UTC") throw new Error("TZ must be UTC (run record.sh)");
if (!process.env.PI_COMMIT) throw new Error("PI_COMMIT must be set (run record.sh)");
const scratchRoot = process.env.SCRATCH;
if (!scratchRoot?.includes("sp-frames-")) throw new Error("SCRATCH must be the scratch directory (run record.sh)");

const theme = {
	fg: (color: string, text: string) => `<fg:${color}>${text}</fg>`,
	bold: (text: string) => `<b>${text}</b>`,
};

function storeText(storePath: string): string | null {
	return existsSync(storePath) ? readFileSync(storePath, "utf8") : null;
}

function seed(cwd: string, jobs: unknown[]): CronStorage {
	if (jobs.length > 0) {
		mkdirSync(path.join(cwd, ".pi"), { recursive: true });
		writeFileSync(path.join(cwd, ".pi", "schedule-prompts.json"), JSON.stringify({ jobs, version: 1 }, null, 2));
	}
	return new CronStorage(cwd);
}

const tick = () => new Promise<void>((r) => setTimeout(r, 20));

async function recordJobs(sc: any, cwd: string) {
	const storage = seed(cwd, sc.jobs ?? []);
	const storePath = path.join(cwd, ".pi", "schedule-prompts.json");
	const calls: unknown[] = [];
	const scheduler: any = {
		getNextRun: (id: string) => (sc.nextRuns?.[id] ? new Date(sc.nextRuns[id]) : null),
		removeJob: (id: string) => calls.push({ op: "removeJob", id }),
		updateJob: (id: string, job: unknown) => calls.push({ op: "updateJob", id, job: JSON.stringify(job) }),
	};
	let addRuns = 0;
	let renders = 0;
	let done = false;
	const runAdd = async () => {
		addRuns++;
		if (sc.runAdd === "fail") throw new Error("add flow failed");
		if (sc.runAdd === "adds") storage.addJob(sc.addJob);
	};
	const origError = console.error;
	console.error = () => {};
	const view = new JobsView(storage, scheduler, sc.session, runAdd, theme, () => renders++, () => (done = true));
	const snap = (input: string | null) => {
		const frames: Record<string, string[]> = {};
		for (const w of sc.widths) frames[String(w)] = view.render(w);
		return { input, done, frames, store: storeText(storePath), calls: [...calls], addRuns, renders };
	};
	const out = [snap(null)];
	for (const input of sc.steps) {
		view.handleInput(input);
		await tick();
		out.push(snap(input));
	}
	console.error = origError;
	return { steps: out };
}

async function recordAddFlow(sc: any, cwd: string) {
	const storage = seed(cwd, sc.jobs ?? []);
	const storePath = path.join(cwd, ".pi", "schedule-prompts.json");
	const answers: unknown[] = [...sc.answers];
	const ui: unknown[] = [];
	const calls: unknown[] = [];
	const next = (m: string, rec: object) => {
		ui.push({ m, ...rec });
		if (answers.length === 0) {
			ui.push({ m: "exhausted" });
			throw new Error("the scenario ran out of answers");
		}
		return answers.shift();
	};
	const ctx: any = {
		ui: {
			input: async (title: string, placeholder: string) => (next("input", { title, placeholder: placeholder.replace(/Current time: \S+/, "Current time: <now>") }) as string | null) ?? undefined,
			select: async (title: string, options: string[]) => (next("select", { title, options }) as string | null) ?? undefined,
			confirm: async (title: string, message: string) => next("confirm", { title, message }) as boolean,
			notify: (message: string, level: string) => ui.push({ m: "notify", message, level }),
		},
	};
	const scheduler: any = { addJob: (job: any) => calls.push({ op: "addJob", job: norm(job) }) };
	let thrown: string | null = null;
	try {
		await runAddFlow(ctx, storage, scheduler, sc.settings ?? {}, sc.session);
	} catch (e) {
		thrown = (e as Error).message;
	}
	const text = storeText(storePath);
	let store: string | null = null;
	if (text !== null) {
		const parsed = JSON.parse(text);
		parsed.jobs = parsed.jobs.map(norm);
		store = JSON.stringify(parsed, null, 2);
	}
	return { ui, calls, store, thrown, unusedAnswers: answers.length };
}

// The job's made-up values, in place and in key order.
function norm(job: any) {
	const out: any = {};
	for (const [k, v] of Object.entries(job)) {
		if (k === "id" && typeof v === "string" && v.length === 10 && !job.__keep) out[k] = "<id>";
		else if (k === "createdAt") out[k] = "<now>";
		else out[k] = v;
	}
	return out;
}

const only = process.argv.slice(2);
mkdirSync(path.join(framesDir, "golden"), { recursive: true });
let n = 0;
for (const f of readdirSync(path.join(framesDir, "scenarios")).sort()) {
	if (!f.endsWith(".json")) continue;
	const name = f.replace(/\.json$/, "");
	if (only.length > 0 && !only.includes(name)) continue;
	const sc = JSON.parse(readFileSync(path.join(framesDir, "scenarios", f), "utf8"));
	const cwd = mkdtempSync(path.join(scratchRoot, "cwd-"));
	const result = sc.kind === "jobs" ? await recordJobs(sc, cwd) : await recordAddFlow(sc, cwd);
	rmSync(cwd, { recursive: true, force: true });
	writeFileSync(
		path.join(framesDir, "golden", f),
		JSON.stringify({ recordedWith: { pi: process.env.PI_COMMIT, node: process.version, tz: process.env.TZ }, scenario: name, ...result }, null, 2) + "\n",
	);
	n++;
}
console.log(`recorded ${n} scenario(s)`);
process.exit(0);
