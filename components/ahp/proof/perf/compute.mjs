// The oracle's GitChanges.compute (pi-ahp src/pi/git-changes.ts, unmodified) on a repository:
// median milliseconds of 5 computations per kind, as one JSON line.
//   node compute.mjs <oracle src/pi directory> <repository>
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const [src, repo] = process.argv.slice(2);
const { GitChanges } = await import(pathToFileURL(join(src, "git-changes.ts")).href);
const git = new GitChanges();
const workspace = await git.inspect(repo);
const median = (xs) => xs.sort((a, b) => a - b)[Math.floor(xs.length / 2)];
const out = {};
for (const kind of ["latest-commit", "uncommitted"]) {
	const times = [];
	let files = 0;
	for (let i = 0; i < 5; i++) {
		const start = performance.now();
		files = (await git.compute(workspace, "sid", kind)).length;
		times.push(performance.now() - start);
	}
	out[kind] = { ms: Number(median(times).toFixed(2)), files };
}
out.rssMB = Math.round(process.memoryUsage().rss / 1048576);
console.log(JSON.stringify(out));
