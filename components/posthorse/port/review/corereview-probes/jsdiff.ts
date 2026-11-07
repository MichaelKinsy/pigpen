import { excerpt, excerptAround, boundedBlock, shortKey } from "./src/message-text.ts";
let seed = 987654;
const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const pool = ["a", "A", "x", "Σ", "σ", "ς", "İ", "I", "i", "\u0307", "'", ".", ":", "\u00B7", " ", "\t", "\n", "\u00A0", "\u2028", "\uFEFF", "\u180E", "\u200B", "é", "ß", "ﬃ", "😀", "\uD83D", "\uDE00", "\u1E9E", "Ω", "\u212A", "\u0345", "\u02B0", "ǅ", "Ǆ", "𐐀", "-", "0", "\u0130Σ", "ΑΣ", "ΑΣ'", "\u3000", "\u2000", "\u2029", "\u000B"];
const units = (s: string) => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i));
const str = () => { let s = ""; const k = rnd(9); for (let i = 0; i < k; i++) s += pool[rnd(pool.length)]; return s; };
const cases: any[] = [];
for (let c = 0; c < 6000; c++) {
	const s = str();
	const L = s.length;
	const a = rnd(L + 5) - 2 - (rnd(3) === 0 ? L : 0), b = rnd(L + 5) - 2 - (rnd(3) === 0 ? L : 0);
	const sub = rnd(2) === 0 ? s.slice(rnd(L + 1), rnd(L + 1)) : str().slice(0, 2);
	const from = rnd(L + 4) - 2;
	const lim = rnd(L + 30) - 3;
	const header = str();
	cases.push({
		s: units(s), a, b, sub: units(sub), from, lim, header: units(header),
		lower: units(s.toLowerCase()), trim: units(s.trim()), trimStart: units(s.trimStart()), trimEnd: units(s.trimEnd()),
		slice: units(s.slice(a, b)), substring: units(s.substring(a, b)),
		indexOf: s.indexOf(sub, from), lastIndexOf: s.lastIndexOf(sub, from),
		charAt: units(s.charAt(a)), charCodeAt: Number.isNaN(s.charCodeAt(a)) ? -1 : s.charCodeAt(a),
		excerpt: units(excerpt(s.repeat(4), lim)), around: units(excerptAround(s.repeat(3), Math.max(0, from), rnd(6), rnd(12))),
		bounded: units(boundedBlock(header, s.repeat(3), lim + 5)), short: shortKey(s),
	});
}
const nums: any[] = [];
for (let c = 0; c < 2000; c++) {
	const n = (rnd(2) ? -1 : 1) * Math.floor(Math.pow(10, rnd(16)) * (rnd(1000) / 997));
	const f = (rnd(2) ? -1 : 1) * (rnd(20000) / 4) * (rnd(5) === 0 ? 1e-3 : 1);
	nums.push({ n, loc: n.toLocaleString("en-US"), f, round: Object.is(Math.round(f), -0) ? "-0" : String(Math.round(f)) });
}
console.log(JSON.stringify({ cases, nums }));
