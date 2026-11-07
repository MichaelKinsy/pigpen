import { StringDecoder } from "node:string_decoder";
// Deterministic pseudo-random byte chunks; print per-write outputs as code-unit arrays.
let seed = 12345;
const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const pool = [0x41, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xc2, 0xdf, 0xe0, 0xe1, 0xed, 0xef, 0xf0, 0xf1, 0xf4, 0xf5, 0xf8, 0xff];
const cases: number[][][] = [];
for (let c = 0; c < 4000; c++) {
	const chunks: number[][] = [];
	const k = 1 + rnd(4);
	for (let i = 0; i < k; i++) { const len = rnd(5); const ch: number[] = []; for (let j = 0; j < len; j++) ch.push(pool[rnd(pool.length)]); chunks.push(ch); }
	cases.push(chunks);
}
const out = cases.map((chunks) => {
	const d = new StringDecoder("utf8");
	const parts = chunks.map((ch) => d.write(Buffer.from(ch)));
	parts.push(d.end());
	return { chunks, parts: parts.map((p) => Array.from(p, (ch) => ch.charCodeAt(0))) };
});
console.log(JSON.stringify(out));
