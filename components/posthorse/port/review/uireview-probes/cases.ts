// uireview differential probe: renders the original's cards and message renderers (port/oracle) for a battery of
// adversarial inputs at many widths and writes inputs + rows as JSON; review_uireview_test.go recomputes them in Go.
// Run (xd6702): copy port/oracle/src and port/oracle/ui.ts next to this file, symlink node_modules to the Pi oracle's,
// {"type":"module"} package.json, `node cases.ts OUT.json`.
import { writeFileSync } from "node:fs";
import { toolCards } from "./src/ui/cards.ts";
import { displayOf, displaySummary, pageSummary } from "./src/ui/display.ts";
import { registerPosthorseMessages } from "./ui.ts";

const open = "\x1b[1m";
const close = "\x1b[22m";
// The Go zero UITheme: no palette (fg/bg leave text as is), chalk's bold with its close re-open and line-break encasing.
const theme = {
	fg: (_c: string, t: string) => t,
	bg: (_c: string, t: string) => t,
	bold: (t: string) => {
		if (!t) return t;
		if (t.includes("\x1b")) t = t.replaceAll(close, close + open);
		if (t.includes("\n")) t = t.replace(/\r?\n/g, (m) => close + m + open);
		return open + t + close;
	},
} as any;

const texts: Record<string, string> = {
	plain: "A readable note\nsecond line\nthird line\nfourth line\nfifth",
	cjk: "界".repeat(30) + " 漢字かなカナ 한국어 텍스트입니다 全角スペース\u3000あり " + "中文字符".repeat(20),
	emoji: "👩🏽‍💻 🇺🇦 👨‍👩‍👧‍👦 ❤️ ✋🏿 1️⃣ ".repeat(8),
	combining: "e\u0301 a\u0308 n\u0303 " + "Z\u0335\u0321\u0322".repeat(10) + " ok",
	zalgo: "a" + "\u0301".repeat(5000) + " tail words here and more words to wrap",
	thai: "ภาษาไทยเป็นภาษาที่ไม่มีการเว้นวรรคระหว่างคำ ".repeat(20),
	arabic: "مرحبا بالعالم هذا نص عربي طويل للاختبار ".repeat(20),
	hebrew: "שלום עולם זהו טקסט בעברית ".repeat(20),
	rtlMarks: "abc \u202Eevil\u202C def \u200Fx\u200E \u2066iso\u2069 \u061Cend " + "مرحبا abc ".repeat(6),
	ansi:
		"\x1b[31mred\x1b[0m plain \x1b[1;4mbold\x1b[22m \x1b]8;;http://e.com\x07link\x1b]8;;\x07 " +
		"\x1b]8;;http://e.com\x1b\\st\x1b]8;;\x1b\\ \x1b[2Ahello m world \x1b[31 unterminated \x1b_apc\x1b\\ " +
		"\x1bc reset \x9b31mC1 \x85nel \ufff9ann\ufffa\ufffb \x7fdel \x00nul \x07bel \x08bs \x1b",
	ansiOnly: "\x1b[31m\x1b[0m",
	tabs: "a\tb\t\tc\n\tindented\n  trailing   \n\u00a0nbsp\u00a0 \u3000ideo\u3000\n\ufeffbom",
	crlf: "one\r\ntwo\rthree\u2028four\u2029five\x0bsix\x0cseven",
	longWord: "x".repeat(500) + " " + "y".repeat(3),
	mixedWide: "a界".repeat(40) + "\n" + "界a".repeat(40),
	spaces: "   ",
	empty: "",
	ws: " \n\t ",
	bigLines: Array.from({ length: 10000 }, (_, i) => `line ${i} ` + "word ".repeat(i % 7)).join("\n"),
	thaiLong: "ภาษาไทย".repeat(3000),
	arabicLine: "مرحبا بالعالم هذا نص عربي ".repeat(1500),
};

const allWidths = [1, 2, 3, 4, 5, 6, 7, 8, 13, 24, 40, 100];
const bigWidths = [3, 40];
const widthsFor = (s: string, expanded: boolean) => (expanded && s.length > 3000 ? bigWidths : allWidths);

type Case = Record<string, unknown> & { rows?: Record<string, string[]> };
const cases: Case[] = [];

function resultCase(id: string, name: string, args: unknown, text: string, details: unknown, o: {
	isError?: boolean; isPartial?: boolean; images?: number; showImages?: boolean; key?: string; expandedOnly?: boolean; widths?: number[];
} = {}) {
	const content: unknown[] = [{ type: "text", text }];
	for (let i = 0; i < (o.images ?? 0); i++) content.push({ type: "image", mimeType: "image/png", data: "AA==" });
	for (const expanded of [false, true]) {
		const c: Case = { id: `${id}/${expanded ? "x" : "c"}`, type: "result", name, args, content, details, isError: o.isError ?? false,
			isPartial: o.isPartial ?? false, expanded, showImages: o.showImages ?? true, key: o.key ?? "ctrl+o", rows: {} };
		const comp = toolCards(name as any).renderResult({ content, details } as any, { expanded, isPartial: o.isPartial ?? false } as any, theme,
			{ args, isError: o.isError ?? false, showImages: o.showImages ?? true, keyText: () => o.key ?? "ctrl+o" } as any);
		for (const w of o.widths ?? widthsFor(text, expanded)) c.rows![w] = [...comp.render(w)];
		cases.push(c);
	}
}

function callCase(id: string, name: string, args: unknown, state: { isError?: boolean; isPartial?: boolean; executionStarted?: boolean } = {}) {
	for (const expanded of [false, true]) {
		const st = { isError: false, isPartial: false, executionStarted: false, ...state, expanded };
		const c: Case = { id: `${id}/${expanded ? "x" : "c"}`, type: "call", name, args, ...st, rows: {} };
		const comp = toolCards(name as any).renderCall(args as any, theme, st as any);
		for (const w of allWidths) c.rows![w] = [...comp.render(w)];
		cases.push(c);
	}
}

let renderer: any;
registerPosthorseMessages({ registerMessageRenderer: (_t: string, r: unknown) => { renderer = r; } } as any);
function messageCase(id: string, customType: string, content: unknown, outputPad = 1) {
	for (const expanded of [false, true]) {
		const message = { role: "custom", customType, content, display: true, timestamp: 0 };
		const c: Case = { id: `${id}/${expanded ? "x" : "c"}`, type: "message", message, expanded, outputPad, rows: {} };
		const comp = renderer(message, { expanded, outputPad, keyText: () => "" }, theme);
		const s = typeof content === "string" ? content : "";
		for (const w of widthsFor(s, expanded)) c.rows![w] = [...comp.render(w)];
		cases.push(c);
	}
}

const label = "\n\nHandoff from the previous window:\n";
for (const [k, s] of Object.entries(texts)) {
	const header = "File: /work/.pi/notes/x.md\n";
	resultCase(`note-read:${k}`, "notes", { op: "read", path: "x.md" }, header + s, { kind: "note-read", headerLength: header.length, offset: 0, end: s.length, total: s.length });
	resultCase(`legacy-notes:${k}`, "notes", { op: "read", path: "x.md" }, s, undefined);
	resultCase(`error:${k}`, "notes", { op: "read", path: "x.md" }, s, undefined, { isError: true });
	resultCase(`partial:${k}`, "notes", { op: "write", path: "x.md", content: s }, s, { kind: "note-write" }, { isPartial: true });
	resultCase(`write:${k}`, "notes", { op: "write", path: "x.md", content: s }, "Wrote x.md", { kind: "note-write" });
	resultCase(`handoff:${k}`, "new_context", { handoff: s }, "Requested", { kind: "new-context" });
	const h1 = `2026-09-06T17:43:46Z [window w1] [e1] [user] `;
	const h2 = `2026-09-06T17:44:46Z [window w1] [e2] [assistant] `;
	const body = `${h1}${s}\n${h2}tail ${k}`;
	const footer = '\n[More results; continue with cursor "abc"]';
	resultCase(`hsearch:${k}`, "history", { op: "search", query: "q", all: true }, body + footer, {
		kind: "history-search", entries: [{ headerLength: h1.length, length: h1.length + s.length }, { headerLength: h2.length, length: h2.length + 5 + k.length }],
		footerLength: footer.length, skipped: 3,
	});
	resultCase(`hlegacy:${k}`, "history", { op: "search", query: "q" }, `${h1}${s}\nx.jsonl ${h2}${k}`, undefined);
	messageCase(`reminder:${k}`, "posthorse-reminder", s);
	messageCase(`window:${k}`, "context-window", `Context window w2 starts here.${label}${s}`);
	if (s.length < 3000) {
		callCase(`call-path:${k}`, "notes", { op: "read", path: s, offset: 5 });
		callCase(`call-query:${k}`, "history", { op: "search", query: s, all: true });
	}
}
messageCase("window-nolabel", "context-window", "no label here\nline2\nline3");
messageCase("reminder-array", "posthorse-reminder", [{ type: "text", text: "part one" }, { type: "image", data: "x" }, { type: "text", text: "part two\nthree\nfour\nfive" }, { type: "text", text: 5 }]);
for (const pad of [0, 2, 5, 50]) messageCase(`reminder-pad${pad}`, "posthorse-reminder", texts.cjk, pad);

// Numbers formatted by n(): call-card labels.
const nums = [0, -0, 1, 0.0625, 1.0005, 2.5, 1234.5678, 0.1 + 0.2, 1e21, 1.5e21, 12345678901234567890, 1e23, -1234.5, 2.5e-7, 999999.9996, 9007199254740993];
for (const [i, v] of nums.entries()) callCase(`call-num:${i}`, "history", { op: "read", id: "e", offset: v, imageOffset: v, limit: v });
callCase("call-bad", "notes", { op: 3, path: {}, offset: "x", limit: null, imageOffset: [1] });
callCase("call-null-path", "notes", { op: "list", path: null });
callCase("call-array-args", "notes", [1, 2]);
callCase("call-string-args", "history", "op");
callCase("call-ctx", "get_context_remaining", {}, { isPartial: true });
callCase("call-new", "new_context", { handoff: "x" }, { isPartial: true, executionStarted: true });
callCase("call-err", "history", { op: "search", query: "q\tw\nz" }, { isError: true });

// Display edge cases.
const ctx = (usage: unknown, extra: Record<string, unknown> = {}) => ({ kind: "context", usage, ...extra });
const usages: [string, unknown][] = [
	["p1250", ctx({ tokens: 200000, contextWindow: 16000, percent: 1250.4 }, { rollover: "unsupported" })],
	["p999.5", ctx({ tokens: 1, contextWindow: 2, percent: 999.5 }, { rollover: "enabled", rolloverAt: 3 })],
	["phalf", ctx({ tokens: 1, contextWindow: 2, percent: 0.49999999999999994 }, { rollover: "disabled" })],
	["pneg", ctx({ tokens: 1, contextWindow: 2, percent: -0.4 })],
	["pnull", ctx({ tokens: 1, contextWindow: 2, percent: null })],
	["tnull", ctx({ tokens: null, contextWindow: 2, percent: null }, { rollover: "unsupported" })],
	["badroll", ctx({ tokens: 1, contextWindow: 2, percent: 1 }, { rollover: "weird" })],
	["nullAt", ctx({ tokens: 1, contextWindow: 2, percent: 1 }, { rollover: "enabled", rolloverAt: null })],
	["noUsage", { kind: "context", rollover: "enabled", rolloverAt: 5 }],
];
for (const [k, d] of usages) resultCase(`ctx:${k}`, "get_context_remaining", {}, "report", d);
const notesDetails: [string, string, unknown][] = [
	["hl0.5", "Notes directory: /d\na.md", { kind: "notes-list", headerLength: 0.5, count: 2, page: { offset: 0, end: 5, total: 5 } }],
	["hl1e19", "Notes directory: /d\na.md", { kind: "notes-list", headerLength: 1e19, count: 2 }],
	["hlneg", "Notes directory: /d\na.md", { kind: "notes-list", headerLength: -3, count: 1 }],
	["page-more", "a.md:1: x", { kind: "notes-search", headerLength: 0, count: 1, page: { offset: 10, end: 20, total: 20 }, more: true }],
	["page-mid", "a.md:1: x", { kind: "notes-list", count: 1, page: { offset: 0, end: 20, total: 1234567 } }],
	["count-frac", "a.md", { kind: "notes-list", count: 1.5 }],
	["bad-more", "a.md", { kind: "notes-list", count: 1, more: "yes" }],
	["read-frac", "File: x\nbody", { kind: "note-read", headerLength: 8, offset: 0.5, end: 1.25, total: 3.75 }],
];
for (const [k, text, d] of notesDetails) resultCase(`notes:${k}`, "notes", { op: k.startsWith("read") ? "read" : "list" }, text, d);
const hsearch: [string, string, unknown][] = [
	["frac-span", "abc", { kind: "history-search", entries: [{ headerLength: 1.5, length: 3 }] }],
	["neg-footer", "abc", { kind: "history-search", entries: [{ headerLength: 1, length: 3 }], footerLength: -1 }],
	["big-footer", "abc", { kind: "history-search", entries: [{ headerLength: 1, length: 3 }], footerLength: 9 }],
	["gap", "abc\ndef", { kind: "history-search", entries: [{ headerLength: 1, length: 2 }] }],
	["null-entry", "abc", { kind: "history-search", entries: [null] }],
	["empty", "No history matches", { kind: "history-search", entries: [], footerLength: 0, more: false, skipped: 0 }],
	["more-undef", "abc\nF", { kind: "history-search", entries: [{ headerLength: 1, length: 3 }], footerLength: 2 }],
	["astral-split", "a😀b\nc", { kind: "history-search", entries: [{ headerLength: 2, length: 4 }, { headerLength: 0, length: 1 }] }],
];
for (const [k, text, d] of hsearch) resultCase(`hs:${k}`, "history", { op: "search", query: "q", all: k === "empty" }, text, d);
const hreads: [string, unknown, number][] = [
	["imgs", { kind: "history-read", headerLength: 5, offset: 0, end: 40, total: 9000, imageOffset: 0, imageEnd: 1, imageTotal: 2 }, 1],
	["imgs-done", { kind: "history-read", headerLength: 5, offset: 0, end: 10, total: 10, imageEnd: 2, imageTotal: 2 }, 2],
	["no-imgs", { kind: "history-read", headerLength: 5, offset: 3, end: 10, total: 10 }, 0],
	["bad-hl", { kind: "history-read", headerLength: 99, offset: 0, end: 10, total: 10 }, 0],
];
for (const [k, d, images] of hreads)
	resultCase(`hr:${k}`, "history", { op: "read", id: "e1" }, "2026 [window w] [e1] [chars 0-10 of 10] body text", d, { images, showImages: k !== "imgs" });
resultCase("legacy-crlf", "history", { op: "search", query: "q" }, "a [window w] [e] [chars 1-2 of 3] x\rb [window w] [e] y\u2028c [window w] [e] z\u2029d [window w] [e] [chars 1-2 of 3]  w\n[window ] [e] no", undefined);
resultCase("hint-nokey", "notes", { op: "read" }, texts.plain, { kind: "note-read", headerLength: 2, offset: 0, end: 3, total: 3 }, { key: "" });
resultCase("notes-empty-read", "notes", { op: "read" }, "  ", undefined);
resultCase("history-empty", "history", { op: "read" }, "", undefined);
resultCase("error-blank", "history", { op: "read" }, " ", undefined, { isError: true });
resultCase("partial-blank", "history", { op: "read" }, "", undefined, { isPartial: true });

// Lone surrogates: a page cut through an astral character (history-pages.ts / notes.ts slice by UTF-16 units).
const lone: Record<string, string> = {
	hiPlain: "page ends here \uD83D",
	hiFullwidth: "页面在这里结束，\uD83D",
	loFullwidth: "\uDE00，下一页",
	hiEmojiVS: "❤️ love \uD83D",
	hiMiddot: "a · b \uD83D",
};
for (const [k, s] of Object.entries(lone)) {
	const header = "File: /work/.pi/notes/x.md\n";
	resultCase(`lone-read:${k}`, "notes", { op: "read", path: "x.md" }, header + s, { kind: "note-read", headerLength: header.length, offset: 0, end: s.length, total: s.length + 1 });
	resultCase(`lone-hread:${k}`, "history", { op: "read", id: "e" }, "2026 [window w] [e] [chars 0-1 of 2] " + s, { kind: "history-read", headerLength: 37, offset: 0, end: 1, total: 2 });
	messageCase(`lone-msg:${k}`, "posthorse-reminder", s);
	callCase(`lone-call:${k}`, "notes", { op: "read", path: s });
}

const summaries: Record<string, string> = {};
for (const [i, v] of nums.entries()) summaries[`page:${i}`] = pageSummary({ offset: v, end: 1, total: 2 });
for (const [k, d] of usages) summaries[`ctx:${k}`] = displaySummary(displayOf(d), {}, true);

const numsText = nums.map((v) => (Object.is(v, -0) ? "-0" : String(v)));
writeFileSync(process.argv[2], JSON.stringify({ numsText, summaries, usages, cases }));
console.log(`${cases.length} cases`);
