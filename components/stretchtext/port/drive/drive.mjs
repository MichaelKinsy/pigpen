// Runs the unmodified original (port/oracle) on cases.json and prints what it produced. diff_test.go replays the
// same cases through the Go port and compares with the committed golden (golden/states.json).
//
//   node --experimental-transform-types drive.mjs <oracle dir> <cases.json> > golden/states.json
//
// The oracle's node_modules must hold @earendil-works/pi-tui (Pi 1.3.0): the original imports it, and its
// own code is what mixes colours, measures columns and patches Markdown.prototype.handleMouse.
import { readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const [oracle, casesFile] = process.argv.slice(2);
const cases = JSON.parse(readFileSync(casesFile, "utf8"));
const load = (file) => import(pathToFileURL(path.join(oracle, file)).href);
const { parse, stretches, drawnAs, isLinkable } = await load("src/parse.ts");
const { grow, lengthOf } = await load("src/grow.ts");
const { GUIDE } = await load("src/prompt.ts");
const stretchtext = (await load("index.ts")).default;
const tuiModule = await import(pathToFileURL(path.join(oracle, "node_modules/@earendil-works/pi-tui/dist/index.js")).href);
const { foregroundAnsi, visibleWidth, Markdown } = tuiModule;

const ser = (segments) =>
	segments.map((s) =>
		typeof s === "string" ? s : { key: s.key, short: s.short, long: s.long.isSome ? ser(s.long.getOrElse([])) : null, closed: s.isClosed },
	);

// ---- fake clock: the original's Animator uses setInterval and Date.now.
let now = 1_000_000_000_000;
let timers = [];
let nextTimer = 1;
globalThis.setInterval = (fn, ms) => {
	timers.push({ id: nextTimer, fn, ms, due: now + ms });
	return nextTimer++;
};
globalThis.clearInterval = (id) => {
	timers = timers.filter((t) => t.id !== id);
};
Date.now = () => now;
const flush = () => new Promise((resolve) => setImmediate(resolve));
async function tick(ms) {
	const end = now + ms;
	for (;;) {
		const due = timers.filter((t) => t.due <= end).sort((a, b) => a.due - b.due || a.id - b.id)[0];
		if (!due) break;
		now = due.due;
		due.due += due.ms;
		due.fn();
		await flush();
	}
	now = end;
}

function fakeTheme(spec) {
	return {
		appearance: spec.appearance,
		colors: Object.fromEntries(
			Object.entries(spec.colors).map(([token, c]) => [token, c.kind === "indexed" ? { kind: "indexed", index: c.index } : c.kind === "rgb" ? { kind: "rgb", r: c.r, g: c.g, b: c.b } : { kind: "oklch", l: c.l, c: c.c, h: c.h }]),
		),
		getColorMode: () => spec.mode,
		fg(token, text) {
			const color = this.colors[token];
			return color ? `${foregroundAnsi(color, spec.mode)}${text}\x1b[39m` : `⟦${token}⟧${text}⟦/⟧`;
		},
	};
}

// ---- parse
const parseOut = cases.parse.map((text) => {
	const segments = parse(text);
	return { segments: ser(segments), kinds: stretches(segments).map((s) => [drawnAs(s), isLinkable(s)]) };
});

// ---- grow
const growOut = cases.grow.map(({ short, long }) => {
	const segments = parse(long);
	return {
		length: lengthOf(segments),
		frames: cases.progress.map((p) => grow(short, segments, p, (n) => `<${n.key}>`, (w) => `[${w}]`)),
	};
});

// ---- sessions: the real index.ts, driven by a scripted host
async function runSession(session) {
	let redraws = 0;
	const tui = { invalidate: () => redraws++, requestRender: () => {} };
	const theme = session.theme ? fakeTheme(cases.themes[session.theme]) : undefined;
	const registered = { handlers: {}, shortcuts: {}, transformer: undefined };
	const pi = {
		registerMarkdownTransformer: (fn) => (registered.transformer = fn),
		on: (name, fn) => (registered.handlers[name] = fn),
		registerShortcut: (key, options) => (registered.shortcuts[key] = options),
	};
	const notices = [];
	let custom;
	const ctx = {
		mode: "tui",
		ui: {
			setWidget: (_key, factory) => factory(tui, theme),
			notify: (message, level) => notices.push([message, level]),
			custom: (factory) =>
				new Promise((resolve) => {
					const done = () => {
						custom.closed = true;
						resolve();
					};
					custom = { closed: false, component: factory(tui, theme, {}, done) };
				}),
		},
	};
	stretchtext(pi);
	const outputs = new Map();
	const trace = [];
	for (const step of session.steps) {
		const before = redraws;
		const entry = { op: step.op };
		notices.length = 0;
		switch (step.op) {
			case "session_start":
				await registered.handlers.session_start({}, ctx);
				break;
			case "transform": {
				const out = registered.transformer(step.md, { messageType: step.type ?? "assistant" });
				outputs.set(step.md, out);
				entry.out = out;
				break;
			}
			case "tick":
				await tick(step.ms);
				break;
			case "click": {
				const out = outputs.get(step.md) ?? "";
				const lines = out.split("\n");
				let seen = -1;
				let hit;
				lines.forEach((line, y) => {
					for (let at = line.indexOf("\x1b]8;;stretchtext:"); at !== -1; at = line.indexOf("\x1b]8;;stretchtext:", at + 1)) {
						if (++seen === step.link) hit = { y, x: visibleWidth(line.slice(0, at)) };
					}
				});
				const event = { type: step.type ?? "click", button: "left", x: hit?.x ?? 0, y: hit?.y ?? (step.link === -1 ? 0 : 99) };
				const result = Markdown.prototype.handleMouse.call({ cachedLines: lines }, event);
				entry.handled = result?.handled === true;
				break;
			}
			case "alt":
				await registered.shortcuts[step.key].handler(ctx);
				await flush();
				entry.opened = custom !== undefined && !custom.closed;
				if (entry.opened) entry.prompt = custom.component.render(80);
				break;
			case "input":
				if (custom && !custom.closed) custom.component.handleInput(step.data);
				await flush();
				entry.closed = custom?.closed ?? null;
				break;
			case "before_agent_start": {
				const sections = { base: "base prompt" };
				registered.handlers.before_agent_start({ systemPromptOptions: { sections } });
				entry.sections = Object.entries(sections);
				break;
			}
			case "shutdown":
				registered.handlers.session_shutdown();
				break;
			default:
				throw new Error(`unknown step ${step.op}`);
		}
		await flush();
		entry.notices = [...notices];
		entry.redraw = redraws > before;
		trace.push(entry);
	}
	await tick(10_000);
	return trace;
}

const sessionsOut = [];
for (const session of cases.sessions) sessionsOut.push({ name: session.name, trace: await runSession(session) });

console.log(JSON.stringify({ guide: GUIDE, parse: parseOut, grow: growOut, sessions: sessionsOut }));
