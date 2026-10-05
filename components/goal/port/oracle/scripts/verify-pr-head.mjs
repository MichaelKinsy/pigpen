#!/usr/bin/env node
/**
 * verify-pr-head — maintainer-provenance gate (plan §6.4).
 *
 * Proves that the current HEAD history (baseRef..HEAD) contains no forbidden
 * contributor commits: no forbidden ancestor SHA, no commit whose author,
 * committer, or message matches a forbidden pattern, and no Co-authored-by
 * trailer anywhere in the range.
 *
 * This is a provenance gate, not an authorship claim about individual ideas:
 * it proves the merged Git history contains only the new maintainer
 * implementation.
 *
 * Usage:
 *   node scripts/verify-pr-head.mjs --base origin/main \
 *     [--forbid-ancestor <sha>]... [--forbid-author <pattern>]...
 */

import { execFileSync } from "node:child_process";

function git(...args) {
	return execFileSync("git", args, { encoding: "utf8" });
}

function parseArgs(argv) {
	const opts = { base: null, forbidAncestors: [], forbidAuthors: [], requireCleanWorktree: true };
	for (let i = 0; i < argv.length; i += 1) {
		const arg = argv[i];
		if (arg === "--base") opts.base = argv[++i];
		else if (arg === "--forbid-ancestor") opts.forbidAncestors.push(argv[++i]);
		else if (arg === "--forbid-author") opts.forbidAuthors.push(argv[++i]);
		else if (arg === "--allow-dirty") opts.requireCleanWorktree = false;
		else {
			console.error(`error: unknown argument: ${arg}`);
			process.exit(2);
		}
	}
	if (!opts.base) {
		console.error("error: --base <ref> is required");
		process.exit(2);
	}
	return opts;
}

const opts = parseArgs(process.argv.slice(2));
const failures = [];
let commits = [];

try {
	if (opts.requireCleanWorktree && git("status", "--porcelain").trim() !== "") {
		failures.push("worktree is dirty");
	}

	const listed = git("rev-list", "--reverse", `${opts.base}..HEAD`).split("\n").map((s) => s.trim()).filter(Boolean);
	commits = listed;
	if (commits.length === 0) failures.push(`no commits found in ${opts.base}..HEAD`);

	for (const sha of opts.forbidAncestors) {
		let isAncestor = false;
		try {
			execFileSync("git", ["merge-base", "--is-ancestor", sha, "HEAD"], { stdio: "ignore" });
			isAncestor = true;
		} catch {
			isAncestor = false;
		}
		if (isAncestor) failures.push(`forbidden contributor head is an ancestor of HEAD: ${sha}`);
	}

	for (const commit of commits) {
		const meta = git("show", "-s", "--format=%H%n%an%n%ae%n%cn%n%ce%n%B", commit);
		for (const pattern of opts.forbidAuthors) {
			let re;
			try {
				re = new RegExp(pattern, "i");
			} catch {
				failures.push(`invalid --forbid-author regex: ${pattern}`);
				continue;
			}
			if (re.test(meta)) failures.push(`commit ${commit.slice(0, 12)} matches forbidden author pattern ${pattern}`);
		}
		if (/^Co-authored-by:/im.test(meta)) {
			failures.push(`commit ${commit.slice(0, 12)} carries a Co-authored-by trailer`);
		}
	}
} catch (error) {
	console.error(`error: ${error instanceof Error ? error.message : String(error)}`);
	process.exit(2);
}

if (failures.length > 0) {
	console.error(`verify-pr-head: FAIL (${opts.base}..HEAD, ${commits?.length ?? 0} commits)`);
	for (const failure of failures) console.error(`  - ${failure}`);
	process.exit(1);
}

console.log(`verify-pr-head: PASS — ${commits.length} commit(s) in ${opts.base}..HEAD contain no forbidden ancestors, authors, or trailers.`);
