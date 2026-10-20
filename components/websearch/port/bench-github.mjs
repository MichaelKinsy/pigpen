// Times the ORIGINAL GitHub extractors on the inputs of extensions/websearch/github_bench_test.go, for the
// performance section of the port's PR: the same fake gh and git (shell scripts), a warm loop in one process
// (median and p90 of N calls) and the cold first call of a fresh node process (module import plus the call).
// Run it beside `go test -bench GitHub -benchmem` on the same host and CPU set.
//
//   cd components/websearch/port && node bench-github.mjs [warmRuns=200] [coldRuns=10]
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const warmRuns = Number(process.argv[2] ?? 200);
const coldRuns = Number(process.argv[3] ?? 10);

const cloneGit = `for a; do dest="$a"; done
mkdir -p "$dest/src"
i=0
while [ $i -lt 60 ]; do printf 'x' > "$dest/src/f$i.txt"; i=$((i+1)); done
printf '# Bench\\n' > "$dest/README.md"`;
const prGh = `case "$*" in
  '--version') echo "gh version 2.0.0"; exit 0;;
  'pr view'*) cat "$BENCH_PR_JSON"; exit 0;;
  'api'*) echo '[]'; exit 0;;
esac
exit 1`;

const workloads = {
	"clone view (60 files)": { gh: "exit 1", git: cloneGit, body: (m) => `const r = await ${m}.extractGitHub("https://github.com/o/r", undefined, true); if (!r) throw new Error("no result"); ${m}.clearCloneCache();`, module: "github-extract.ts" },
	"pull request via gh": { gh: prGh, git: "exit 1", body: (m) => `const r = await ${m}.extractGitHubIssuePr("https://github.com/o/r/pull/7"); if (!r || r.error) throw new Error("no result");`, module: "github-issue-pr.ts" },
};

function script(w, runs) {
	return `
		const t0 = process.hrtime.bigint();
		const m = await import(${JSON.stringify(join(here, "oracle", w.module))});
		const once = async () => { ${w.body("m")} };
		await once();
		const first = Number(process.hrtime.bigint() - t0) / 1e3;
		const times = [];
		for (let n = 0; n < ${runs}; n++) {
			const s = process.hrtime.bigint();
			await once();
			times.push(Number(process.hrtime.bigint() - s) / 1e3);
		}
		times.sort((a, b) => a - b);
		console.log(JSON.stringify({ first, median: times[times.length >> 1] ?? first, p90: times[Math.floor(times.length * 0.9)] ?? first }));
	`;
}

function run(w, runs) {
	const root = mkdtempSync(join(tmpdir(), "ghbench-"));
	const bin = join(root, "bin"), agent = join(root, "agent");
	for (const d of [bin, agent]) mkdirSync(d, { recursive: true });
	for (const [name, body] of [["gh", w.gh], ["git", w.git]]) {
		writeFileSync(join(bin, name), `#!/bin/sh\n${body}\n`);
		chmodSync(join(bin, name), 0o755);
	}
	writeFileSync(join(agent, "web-search.json"), JSON.stringify({ githubClone: { clonePath: join(root, "clones") } }));
	const env = { PATH: `${bin}:/usr/local/bin:/usr/bin:/bin`, HOME: root, PI_CODING_AGENT_DIR: agent, BENCH_PR_JSON: join(here, "bench-github-pr.json") };
	const r = spawnSync(process.execPath, ["--input-type=module"], { input: script(w, runs), encoding: "utf8", env, maxBuffer: 16 << 20 });
	if (r.status !== 0) throw new Error(r.stderr);
	return JSON.parse(r.stdout.trim().split("\n").pop());
}

const med = (xs) => xs.slice().sort((a, b) => a - b)[xs.length >> 1];
console.log("workload\twarm median us\twarm p90 us\tcold first call us (module import + call)");
for (const [name, w] of Object.entries(workloads)) {
	const warm = run(w, warmRuns);
	const cold = Array.from({ length: coldRuns }, () => run(w, 0).first);
	console.log(`${name}\t${warm.median.toFixed(0)}\t${warm.p90.toFixed(0)}\t${med(cold).toFixed(0)}`);
}
