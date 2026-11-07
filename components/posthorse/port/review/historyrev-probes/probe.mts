// Oracle side of the historyrev review differentials. Run from this directory on xd6702:
//   node probe.mts   (writes /home/kinsy/ph-work/scratch/historyrev/*.json, read by review_historyrev_test.go)
import { mkdirSync, writeFileSync, chmodSync, rmSync, readdirSync } from "node:fs";
import { readdir, stat } from "node:fs/promises";
import { needleLines, parseSessionLine, prefilterNeedles } from "../../oracle/src/session-files.ts";
import { historyHit, toWindowedEntry } from "../../oracle/src/history-entries.ts";
import { unknownArray } from "../../oracle/src/contracts.ts";

const out = "/home/kinsy/ph-work/scratch/historyrev";
mkdirSync(out, { recursive: true });
let seed = 4242;
const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const units = (s: string) => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i));
const pool = ["a", "A", "k", "K", "s", "S", "\u017f", "\u212a", "\u03a3", "\u03c3", "\u03c2", "\u0130", "I", "i", "\u0307", "'", ".", ":",
	"\u00b7", "\u0387", "\u055f", "\u05f4", "\u0391", "\u0392", "\u03c9", " ", "\u00e9", "\u00c9", "\u00df", "\u1e9e", "\ud83d\ude00", "\ud83d",
	"\ude00", "\u2126", "\u0345", "\u01c5", "\ud801\udc00", "\ud801\udc28", "-", "0", "\"", "\\", "[", "(", "$", "\u0085", "\u0001", "\u00a0",
	"\ufeff", ",", "1", " images", "image", "call id", "x", "\u0390", "\u1fd3", "\u13a0", "\uab70"];
const str = (k = 9) => { let s = ""; const n = rnd(k); for (let i = 0; i < n; i++) s += pool[rnd(pool.length)]; return s; };

// A: toLowerCase and lowercase indexOf.
const lower: any[] = [];
for (let c = 0; c < 20000; c++) {
	const s = str(12);
	const q = rnd(2) ? s.slice(rnd(s.length + 1), rnd(s.length + 1)) : str(3);
	lower.push({ s: units(s), lower: units(s.toLowerCase()), q: units(q), index: s.toLowerCase().indexOf(q.toLowerCase()) });
}
writeFileSync(`${out}/lower.json`, JSON.stringify(lower));

// B: prefilterNeedles over lowercased queries.
const prefilter: any[] = [];
for (let c = 0; c < 20000; c++) {
	const q = str(10).toLowerCase();
	const needles = prefilterNeedles(q);
	prefilter.push({ q: units(q), needles: needles === undefined ? null : needles.map(units) });
}
writeFileSync(`${out}/prefilter.json`, JSON.stringify(prefilter));

// C: needleLines matchers, Latin-1 byte windows for ASCII needles and UTF-8 text otherwise.
const asciiQueries = ["k", "s", "ks", "sk", "kiss", "x", "a k", "is", "image", "1", "call", "unknown", "i", "kk", "ss", "sks", "type", "k\"s", "s:k"];
const bytePool = ["k", "K", "s", "S", "\xe2\x84\xaa", "\xc2\x84\xaa", "\xc5\xbf", "\xe5\xbf", "\xc4\xb0", "a", "x", "\"", "i", "1", " ", "image", "type",
	"\xe2", "\x84", "\xaa", "\xbf", "\xc5", "\xe5", "\xc2", "\":\"", "call", "\"type\":\"image\"", "unknown", "\"type\":\"custom_message\""];
const matchers: any[] = [];
for (let c = 0; c < 20000; c++) {
	const q = asciiQueries[rnd(asciiQueries.length)]!;
	const needles = prefilterNeedles(q);
	if (needles === undefined) continue;
	let w = "";
	const n = rnd(10);
	for (let i = 0; i < n; i++) w += bytePool[rnd(bytePool.length)];
	const bytes = Array.from(w, (ch) => ch.charCodeAt(0));
	matchers.push({ needles: needles.map(units), window: bytes, latin1: true, test: needleLines(needles).test(Buffer.from(bytes).toString("latin1")) });
}
const textPool = pool.filter((p) => !/[\ud800-\udfff]/u.test(p) || p.length === 2);
for (let c = 0; c < 20000; c++) {
	let q = "";
	for (let i = 0, n = 1 + rnd(3); i < n; i++) q += textPool[rnd(textPool.length)];
	const needles = prefilterNeedles(q.toLowerCase());
	if (needles === undefined) continue;
	let w = "";
	for (let i = 0, n = rnd(10); i < n; i++) w += textPool[rnd(textPool.length)];
	if (rnd(3) === 0) w += q.toUpperCase();
	const m = needleLines(needles);
	const latin1 = m.encoding === "latin1";
	matchers.push({ needles: needles.map(units), window: latin1 ? [...Buffer.from(w)] : units(w), latin1, test: m.test(latin1 ? Buffer.from(w).toString("latin1") : w) });
}
writeFileSync(`${out}/matchers.json`, JSON.stringify(matchers));

// D: Buffer.from(s, "base64url").
const b64Pool = ["A", "Z", "a", "z", "0", "9", "-", "_", "+", "/", "=", " ", "\n", ".", "\u00e9", "Q", "w", "==", "%", "*"];
const b64: any[] = [];
for (let c = 0; c < 20000; c++) {
	let s = "";
	for (let i = 0, n = rnd(24); i < n; i++) s += b64Pool[rnd(b64Pool.length)];
	b64.push({ s, bytes: [...Buffer.from(s, "base64url")] });
}
writeFileSync(`${out}/base64.json`, JSON.stringify(b64));

// E: decodeCursor (history-search.ts:45-70, copied verbatim; not exported).
function cursorOffset(value: unknown, storedKey: unknown, key: string): number {
	if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || storedKey !== key) throw new Error("Invalid cursor offset or scope.");
	return value;
}
function decodeCursor(encoded: string | undefined, key: string) {
	if (encoded === undefined || encoded === "") return undefined;
	const value: unknown = JSON.parse(Buffer.from(encoded, "base64url").toString());
	const parts = unknownArray(value);
	if (parts === undefined || parts.length !== 4) throw new Error("Invalid cursor tuple.");
	const [id, priority, offset, storedKey] = parts;
	if (typeof id !== "string" || id === "" || (priority !== 0 && priority !== 1)) throw new Error("Invalid cursor anchor.");
	return [id, priority, cursorOffset(offset, storedKey, key), key] as const;
}
const payloads = ['["a",0,5,"KEY"]', '[ "a" , 1 , 0 , "KEY" ]', '["a",1.0,5e0,"KEY"]', '["a",0,-0,"KEY"]', '["a",0,1e400,"KEY"]', '["a",-0,0,"KEY"]',
	'["\\ud800",0,0,"KEY"]', '\ufeff["a",0,0,"KEY"]', '["a",0,0,"KEY",]', '["a",0,9007199254740991,"KEY"]', '["a",0,9007199254740992,"KEY"]',
	'["a",0,0.5,"KEY"]', '["a",true,0,"KEY"]', '["a",0,0,"KEX"]', '["",0,0,"KEY"]', '{"0":"a"}', '["a",0,0,"KEY"]\n', '["a",0,0,"KEY"] x',
	'["a",0,1E2,"KEY"]', '["a",0,100e-2,"KEY"]', '["a",0,0,"K\\u0045Y"]', '["a",01,0,"KEY"]', '["a",0,0,"KEY"]\u2028', '\t["a",0,0,"KEY"]\r',
	'["a",2,0,"KEY"]', '["a",0,1e-400,"KEY"]', '["a",0,0,"KEY"]\u00a0'];
const cursors: any[] = [];
for (const p of payloads) {
	for (const form of ["base64url", "base64", "junk"]) {
		let enc = Buffer.from(p).toString(form === "junk" ? "base64url" : (form as BufferEncoding));
		if (form === "junk") enc = enc.split("").join(rnd(2) ? "." : " ");
		let result: any;
		try { const c = decodeCursor(enc, "KEY"); result = { ok: true, id: units(c![0]), priority: c![1], offset: Object.is(c![2], -0) ? 0 : c![2] }; } catch { result = { ok: false }; }
		cursors.push({ encoded: enc, ...result });
	}
}
writeFileSync(`${out}/cursors.json`, JSON.stringify(cursors));

// F: parseSessionLine (JSON.parse) on lines the SDK decoder may read differently.
const deep = (n: number) => "[".repeat(n) + "]".repeat(n);
const lines = ['{"type":"message","id":"a","id":"b"}', '{"type":"message","id":"\\ud800x"}', '{"type":"message","id":"a\\u0000b"}',
	`{"type":"message","id":"d","content":${deep(10001)}}`, `{"type":"message","id":"d2","content":${deep(9999)}}`, `{"type":"message","id":"d3","content":${deep(100000)}}`,
	'{"type":"message","id":"n","details":1e999}', '{"type":"message","id":"m","details":-1e-999}', '\ufeff{"type":"message","id":"bom"}',
	'{"__proto__":{"id":"p"},"type":"message","id":"q"}', '{"type":"message","id":"big","details":123456789012345678901234567890}',
	'{"type":"message","id":"t"}\r', '{"type":"message","id":"u"} ', '{"type":"message","id":"v",}', "{'type':'message'}",
	'{"type":"message","id":"w","message":{"role":"user","content":"x","role":"assistant"}}', '{"type":"message","id":"\\uD83D\\uDE00"}',
	'{"type":"message","id":"esc\\/"}', '{"type":"message","id":"ctl\u0001"}', '{"type":"message","id":"nul\u0000"}'];
const parsed = lines.map((line) => {
	const entry = parseSessionLine(line);
	return { line: units(line), ok: entry !== undefined, id: entry?.id === undefined ? null : units(entry.id), role: entry?.message?.role ?? null };
});
writeFileSync(`${out}/lines.json`, JSON.stringify(parsed));

// G: fs error texts of sessionFiles (readdir) and newestFirst (stat), and readdir order.
const base = `${out}/fs`;
rmSync(base, { recursive: true, force: true });
mkdirSync(`${base}/sess/locked`, { recursive: true });
chmodSync(`${base}/sess/locked`, 0);
const fsTexts: Record<string, string> = {};
try { await readdir(`${base}/sess/locked`, { withFileTypes: true }); } catch (error) { fsTexts.readdir = (error as Error).message.replaceAll(base, "<base>"); }
try { await stat(`${base}/sess/missing.jsonl`); } catch (error) { fsTexts.stat = (error as Error).message.replaceAll(base, "<base>"); }
chmodSync(`${base}/sess/locked`, 0o755);
mkdirSync(`${base}/order`);
const names = ["b.jsonl", "a.jsonl", "B.jsonl", "\u00e9.jsonl", "_x.jsonl", "a0.jsonl", "a-.jsonl", "Z.jsonl", "10.jsonl", "9.jsonl"];
for (const name of names) writeFileSync(`${base}/order/${name}`, "");
fsTexts.order = JSON.stringify((await readdir(`${base}/order`, { withFileTypes: true })).map((e) => e.name));
fsTexts.orderSync = JSON.stringify(readdirSync(`${base}/order`));
rmSync(base, { recursive: true, force: true });
writeFileSync(`${out}/fs.json`, JSON.stringify(fsTexts));

// I: /iu equivalence classes over every code point with a case mapping (the non-ASCII needleLines regexp).
const cased: string[] = [];
for (let cp = 0; cp <= 0x10ffff; cp++) {
	if (cp >= 0xd800 && cp <= 0xdfff) continue;
	const c = String.fromCodePoint(cp);
	if (c.toLowerCase() !== c || c.toUpperCase() !== c) cased.push(c);
}
const folds: any[] = [];
for (const c of cased) {
	const re = new RegExp(`^${RegExp.escape(c)}$`, "iu");
	const matched = cased.filter((d) => re.test(d)).map((d) => d.codePointAt(0));
	folds.push({ c: c.codePointAt(0), matched });
}
writeFileSync(`${out}/folds.json`, JSON.stringify(folds));

// H: historyHit over random user messages.
const hits: any[] = [];
for (let c = 0; c < 20000; c++) {
	const text = str(14);
	const entry = { type: "message", id: "e1", timestamp: "t", message: { role: "user", content: text } };
	const item = toWindowedEntry(entry, "initial");
	if (item === undefined) continue;
	let q = rnd(2) ? item.text.slice(rnd(item.text.length + 1), rnd(item.text.length + 1)) : str(3);
	if (rnd(2)) q = q.toUpperCase();
	q = q.toLowerCase();
	if (q === "") continue;
	const hit = historyHit(item, q, rnd(2) ? "" : "sub/x.jsonl");
	hits.push({ text: units(text), q: units(q), source: hit === undefined ? "" : hit.id.includes("@") ? "sub/x.jsonl" : "", hit: hit === undefined ? null : { id: hit.id, priority: hit.priority, text: units(hit.text), headerLength: hit.headerLength } });
}
writeFileSync(`${out}/hits.json`, JSON.stringify(hits));
console.log("lower", lower.length, "prefilter", prefilter.length, "matchers", matchers.length, "b64", b64.length, "cursors", cursors.length, "lines", parsed.length, "hits", hits.length, JSON.stringify(fsTexts));
