#!/usr/bin/env bun
/**
 * Deterministic generator + omp v19.1.1 oracle for the four hand-written bash
 * interceptor matchers (`exec/settings.ts:38,87,93,100`):
 *   - rule 0 (`cat_read`)
 *   - rule 6 (`echo_cat_write`)
 *   - rule 7 (`nohup_bg`)
 *   - rule 8 (`dev_service`)
 *
 * Generates 50,000 deterministic command lines per rule (200,000 total) using
 * a fixed Mulberry32 PRNG seed, runs omp v19.1.1's own RegExp from
 * `packages/coding-agent/src/exec/settings.ts`, and writes a compact fixture:
 *   `interceptor_diff.txt`
 * where each line is `<ruleIdx><0|1>\t<JSON-encoded command>` (sorted by rule).
 *
 * Usage:
 *   OMP_SRC=/path/to/omp-v19.1.1 bun run internal/shell/testdata/gen_interceptor_diff.ts
 */
import * as fs from "node:fs";
import * as path from "node:path";
import * as zlib from "node:zlib";

const ompRoot = process.env.OMP_SRC;
if (!ompRoot) {
	console.error("Set OMP_SRC to the pinned omp v19.1.1 checkout path.");
	process.exit(2);
}
const settingsPath = path.join(ompRoot, "packages/coding-agent/src/exec/settings.ts");
// Runtime-selected checkout path via OMP_SRC cannot use a static import.
const { DEFAULT_BASH_INTERCEPTOR_RULES } = await import(settingsPath);

function mulberry32(seed: number): () => number {
	let a = seed >>> 0;
	return () => {
		a |= 0;
		a = (a + 0x6d2b79f5) | 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

function pick<T>(rng: () => number, items: readonly T[]): T {
	return items[Math.floor(rng() * items.length)]!;
}

const LEAD_WS = ["", " ", "  ", "\t", "\u00a0"];

function genCatRead(rng: () => number, idx: number): string {
	const fixed = [
		"cat file.txt",
		"head -n 5 a.txt",
		"tail -f log",
		"less README.md",
		"more f",
		"cat ",
		"cat  ",
		"cat <<EOF",
		"cat > out.txt",
		"cat >> out.txt",
		"cat >| out.txt",
		"cat 1> out.txt",
		"cat 1>&2 file.txt",
		"cat >&2 file.txt",
		"cat &> out.txt",
		"cat 2>err.log file.txt",
		"cat 2>/dev/null > out.txt",
		"cat 2>|err.log 3>>x.log <<EOF",
		"cat 2>&1 file.txt",
		"cat 12>err > out",
		"cat\t\n>out",
		"cat  file.txt",
		"cat 0>in.txt file.txt",
		"echo cat file.txt",
	];
	if (idx < fixed.length) return fixed[idx]!;
	const cmd = pick(rng, ["cat", "head", "tail", "less", "more", "echo", "catx"]);
	const sep = pick(rng, [" ", "  ", "\t", "", "\n"]);
	const redirs: string[] = [];
	const nRedir = Math.floor(rng() * 3);
	for (let i = 0; i < nRedir; i++) {
		redirs.push(
			pick(rng, [
				"2>err ",
				"2>>/dev/null ",
				"2>|e.log ",
				"0>in ",
				"9>f ",
				"2>&1 ",
				"1>&2 ",
				">&2 ",
				"1>out ",
				">out ",
				">>out ",
				"&>out ",
				"<<EOF ",
				"2> err ",
				"12>err ",
			]),
		);
	}
	const tail = pick(rng, ["file.txt", "-n 10 f", "<<'EOF'\nx\nEOF", "> out", "1>out", ">&2 f", "&>all", ""]);
	return pick(rng, LEAD_WS) + cmd + sep + redirs.join("") + tail;
}

function genEchoCatWrite(rng: () => number, idx: number): string {
	const fixed = [
		'echo "say \\"a > b\\""',
		'printf "%s\\n" "\\"x\\" > y"',
		'echo "{\\"k\\": \\"a>b\\"}"',
		'echo "{\\"k\\": \\"v\\"}" > out.json',
		'echo "x \\"y\\"" >> notes.md',
		"echo foo > >(cat > log.txt)",
		"cat <<EOF > >(x) > f",
		"echo hi > out.txt",
		"echo hi >> out.txt",
		"echo hi >| out.txt",
		"echo hi >>| out.txt",
		"echo hi >>> out.txt",
		"echo hi > /dev/null",
		"echo hi >/dev/null>out",
		'echo hi > "/dev/null" > out.txt',
		'echo hi > "/dev/null"x',
		"echo hi > /dev/null/foo",
		"echo hi > /dev/null; > out.txt",
		"echo hi > /dev/null\n> out.txt",
		"echo hi > /dev/null\r> out.txt",
		"echo hi 2>err.log",
		"echo hi 2>err.log > out.txt",
		"echo hi 2>&1 > out.txt",
		"echo hi 1>&2 > out.txt",
		"echo hi >&2 > out.txt",
		"echo hi &> out.txt",
		"echo hi <&0 > out.txt",
		"echo hi <&> out.txt",
		"echo hi >&> out.txt",
		"echo hi 2>&> out.txt",
		"echo ${a:-${b}} > out.txt",
		"echo ${a:-${b};c} > out.txt",
		"echo ${x\\}} > out.txt",
		"echo $(printf a; printf b) > out.txt",
		'echo $(a"b\\"c") > out.txt',
		"echo $(( (1 + 2) * 3 )) > out.txt",
		"echo $(a; (b; (c))) > out.txt",
		"echo `a; b` > out.txt",
		"echo `a > out.txt",
		"echo `a;b > out.txt",
		"echo \\\" > out.txt",
		"echo \\> out.txt",
		"echo \\\\> out.txt",
		"echo a > @f",
		"echo a > +f",
		"echo a > -f",
		"echo a > ~f",
		"echo a > $f",
		"cat <<EOF > f\nx\nEOF",
		"cat 2>err > out",
		"cat 12>err > out",
		"cat 0>in",
		"cat > /dev/tty",
		"echo a >\n out.txt",
		"echo a \u00a02>err",
	];
	if (idx < fixed.length) return fixed[idx]!;
	const head = pick(rng, [
		"echo ",
		"printf ",
		"cat<<",
		"cat <<",
		"cat >",
		"cat 1>",
		"cat 2>",
		"cat &>",
		"cat 12>",
		"cat\t",
		"echo\t",
		"ls ",
	]);
	const chunks: string[] = [];
	const nChunks = Math.floor(rng() * 6);
	const atoms = [
		"foo",
		" ",
		"\t",
		"\\;",
		"\\>",
		"\\\"",
		"\\\\",
		"\\\n",
		'"a > b"',
		'"a \\"b\\" > c"',
		'"{\\"k\\":\\"v\\"}"',
		'"unclosed',
		"'a > b'",
		"'unclosed",
		"$(a; b)",
		'$(a "b\\"c")',
		"$((1+2))",
		"$(a; (b; (c)))",
		"$(unclosed",
		"${x:-a;b}",
		"${a:-${b}}",
		"${a:-${b};c}",
		"${x\\}}",
		"${unclosed",
		"`a; b`",
		"`unclosed",
		"$VAR",
		"2>err",
		"2>>err",
		"2>|err",
		"2>&1",
		"0>in",
		"1>out",
		"1>&2",
		">&2",
		"&>",
		"<&0",
		"x2>err",
		"> /dev/null",
		">/dev/null",
		'> "/dev/null"',
		"> '/dev/stderr'",
		">| /dev/tty",
		">> /dev/stdout",
		"> /dev/null/sub",
		"> /dev/nullx",
		"; ",
		"| ",
		"&& ",
		"& ",
		"\n",
		"\r",
		"> >(cat)",
		"> @bad",
		"> +bad",
		"> ",
		">>",
		">|",
		">>|",
		">>>",
	];
	for (let i = 0; i < nChunks; i++) {
		chunks.push(pick(rng, atoms));
	}
	const endRedir = pick(rng, [
		"",
		" > out.txt",
		" >> notes.md",
		" >| f",
		" >>| f",
		">out",
		" > $OUT",
		' > "f"',
		" > 'f'",
		" > /dev/null",
		" > /dev/stderr",
		" > >(cat > log)",
		" > @x",
		" > -f",
		" > ~f",
		" > ./dev/null",
		" >\n out.txt",
		" >",
	]);
	return pick(rng, LEAD_WS) + head + chunks.join("") + endRedir;
}

function genNohupBg(rng: () => number, idx: number): string {
	const fixed = [
		"nohup server",
		"sleep 5 &",
		"sleep 5 &  ",
		"a && b",
		"a &&",
		"a && ",
		"a & b",
		"printf 'server &'",
		"  nohup\tcmd",
		"nohup",
		"&",
		"&&",
		"cmd &\n",
	];
	if (idx < fixed.length) return fixed[idx]!;
	const lead = pick(rng, ["", " ", "\t", "nohup ", "nohup\t", "echo ", "server "]);
	const mid = pick(rng, ["cmd", "sleep 5", "a && b", "a & b", "a || b", "'x &'", ""]);
	const tail = pick(rng, ["", " &", "&", " & ", " &\t", " &&", " && ", " &\n", " 2>&1"]);
	return lead + mid + tail;
}

function genDevService(rng: () => number, idx: number): string {
	const fixed = [
		"npm run dev",
		"npm start",
		"bun dev",
		"pnpm run start --port 3000",
		"yarn dev",
		"vite --host 0.0.0.0",
		"next dev",
		"nuxt dev",
		"nodemon index.js",
		"lldb ./app",
		"gdb ./app",
		"tail -f app.log",
		"docker compose up",
		"docker compose up --build",
		"docker compose up -d",
		"docker compose up --detach",
		"docker compose up -d --build",
		"docker compose up -db",
		"docker compose up\n-d",
		"docker compose up --build\n--detach",
		"npm run build",
		"vite-node x",
		"tail -n 20 log",
	];
	if (idx < fixed.length) return fixed[idx]!;
	const kind = Math.floor(rng() * 4);
	if (kind === 0) {
		const pm = pick(rng, ["bun", "npm", "pnpm", "yarn", "cargo"]);
		const run = pick(rng, ["", "run ", "run\t"]);
		const sub = pick(rng, ["dev", "start", "test", "build", "devx", "start-prod"]);
		const rest = pick(rng, ["", " --port 3000", "\t--host", "\nfoo"]);
		return pick(rng, LEAD_WS) + pm + " " + run + sub + rest;
	}
	if (kind === 1) {
		const tool = pick(rng, ["vite", "next dev", "nuxt dev", "nodemon", "lldb", "gdb", "tail -f", "tail -n", "vitex"]);
		const rest = pick(rng, ["", " ./app", " --host", "x"]);
		return pick(rng, LEAD_WS) + tool + rest;
	}
	const flags = [
		"",
		" --build",
		" -d",
		" -d ",
		" -d --build",
		" --detach",
		" --build --detach",
		" -db",
		" -dx",
		"\n-d",
		"\n--detach",
		" svc1",
	];
	return pick(rng, LEAD_WS) + "docker compose up" + pick(rng, flags) + pick(rng, ["", " -d", " --detach", " web"]);
}

const SPECS = [
	{ idx: 0, name: "cat_read", count: 50_000, seed: 0x1111_0001, gen: genCatRead },
	{ idx: 6, name: "echo_cat_write", count: 50_000, seed: 0x6666_0006, gen: genEchoCatWrite },
	{ idx: 7, name: "nohup_bg", count: 50_000, seed: 0x7777_0007, gen: genNohupBg },
	{ idx: 8, name: "dev_service", count: 50_000, seed: 0x8888_0008, gen: genDevService },
] as const;

const outPath = path.join(import.meta.dir, "interceptor_diff.txt.gz");
const lines: string[] = [];
const stats: Record<string, { total: number; matches: number }> = {};

for (const spec of SPECS) {
	const re = new RegExp(DEFAULT_BASH_INTERCEPTOR_RULES[spec.idx]!.pattern);
	const rng = mulberry32(spec.seed);
	let matches = 0;
	for (let i = 0; i < spec.count; i++) {
		const cmd = spec.gen(rng, i);
		const hit = re.test(cmd);
		if (hit) matches++;
		lines.push(`${spec.idx}${hit ? "1" : "0"}\t${JSON.stringify(cmd)}`);
	}
	stats[spec.name] = { total: spec.count, matches };
}

const payload = zlib.gzipSync(Buffer.from(lines.join("\n") + "\n", "utf8"), { level: 9 });
fs.writeFileSync(outPath, payload);
console.log("Wrote", outPath, "bytes:", payload.length, "total lines:", lines.length, "stats:", JSON.stringify(stats));
