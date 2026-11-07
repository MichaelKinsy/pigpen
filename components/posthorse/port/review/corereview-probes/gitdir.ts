let seed = 4242;
const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const pool = ["gitdir:", "gitdir: ", " ", "\t", "\n", "\r", "\r\n", "\u2028", "\u2029", "x", "/a b", "\uFEFF", "\u00A0", "😀", "\u000B", "g"];
const out: any[] = [];
for (let c = 0; c < 8000; c++) {
	let s = ""; const k = rnd(7); for (let i = 0; i < k; i++) s += pool[rnd(pool.length)];
	const m = s.match(/^gitdir:\s*(.+?)\s*$/m);
	out.push({ s, m: m === null ? null : m[1] });
}
console.log(JSON.stringify(out));
