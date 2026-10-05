#!/usr/bin/env node
/**
 * pi-goal-x-recover — offline repair for session files affected by issue #30.
 *
 * Before the v2 bounded-checkpoint fix, every auto-continue turn persisted a
 * full continuation prompt (~6.4K chars) as a custom_message entry. This tool
 * rewrites ONLY those legacy checkpoint entries to the tiny v2 marker form:
 *
 *   - line count unchanged; entry ids/parentIds unchanged;
 *   - header and non-goal lines byte-identical (malformed lines preserved);
 *   - only checkpoint `content` and `details` change;
 *   - idempotent; dry-run default.
 *
 * SAFETY: never run against a live Pi session. Close Pi first. --apply is
 * refused without --confirm-pi-closed. A timestamped backup is created before
 * replacement, which itself is an atomic same-directory rename.
 *
 * Usage:
 *   pi-goal-x-recover --session <session.jsonl>                    # dry-run
 *   pi-goal-x-recover --session <session.jsonl> --apply --confirm-pi-closed
 */

import * as fs from "node:fs";
import * as path from "node:path";

const GOAL_EVENT_ENTRY = "pi-goal-event";
const V2_MARKER_PATTERN =
	/^<pi_goal_continuation\s+goal_id="[^"]+"\s+kind="checkpoint"\s+v="2"\s*\/>$/;

function escapeXmlAttribute(value) {
	return value
		.replaceAll("&", "&amp;")
		.replaceAll('"', "&quot;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;");
}

export function checkpointTriggerPrompt(goalId) {
	const content = `<pi_goal_continuation goal_id="${escapeXmlAttribute(goalId)}" kind="checkpoint" v="2"/>`;
	if (content.length > 160) throw new Error(`checkpoint marker too large: ${content.length}`);
	return content;
}

function asRecord(value) {
	return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}

/** Extract goalId from a checkpoint entry's details or content (v2 + legacy forms). */
function checkpointGoalId(entry) {
	const raw = asRecord(entry);
	if (!raw) return null;
	const details = asRecord(raw.details);
	if (details && typeof details.goalId === "string" && details.goalId) return details.goalId;
	if (typeof raw.content !== "string") return null;
	const xml = raw.content.match(/^<pi_goal_continuation\s+goal_id="([^"]+)"/);
	if (xml?.[1]) return xml[1];
	const bracket = raw.content.match(/^\[(?:GOAL CHECKPOINT|GOAL CONTINUATION|GOAL STALE) goalId=([^\]\s]+)\]/);
	return bracket?.[1] ?? null;
}

function isGoalCheckpointCustomMessage(entry) {
	const raw = asRecord(entry);
	return Boolean(
		raw
		&& raw.type === "custom_message"
		&& raw.customType === GOAL_EVENT_ENTRY
		&& typeof raw.content === "string"
		&& !V2_MARKER_PATTERN.test(raw.content),
	);
}

function graphIdentity(entry) {
	const raw = asRecord(entry);
	return { id: raw ? raw.id : undefined, parentId: raw ? raw.parentId : undefined };
}

function splitPreservingFinalNewline(text) {
	const lines = text.split("\n");
	// split() drops knowledge of a trailing newline; re-add an empty final
	// segment so join("\n") reproduces the input byte count exactly.
	if (text.endsWith("\n")) return lines; // last element is "" already
	return lines;
}

/**
 * Pure line-level transform used by both the CLI flow and benchmarks.
 * Returns the rewritten lines plus counters. Never touches the filesystem.
 */
export function transformSessionLines(originalText) {
	const outputLines = [];
	const originalGraph = [];
	const outputGraph = [];
	let changed = 0;
	let savedChars = 0;
	let malformed = 0;

	for (const rawLine of splitPreservingFinalNewline(originalText)) {
		if (!rawLine.trim()) {
			outputLines.push(rawLine);
			continue;
		}
		let entry;
		try {
			entry = JSON.parse(rawLine);
		} catch {
			malformed += 1;
			outputLines.push(rawLine); // never discard unrelated malformed data
			continue;
		}

		originalGraph.push(graphIdentity(entry));

		if (!isGoalCheckpointCustomMessage(entry)) {
			outputLines.push(rawLine); // byte-identical for untouched entries
			outputGraph.push(graphIdentity(entry));
			continue;
		}

		const goalId = checkpointGoalId(entry);
		if (!goalId) {
			outputLines.push(rawLine);
			outputGraph.push(graphIdentity(entry));
			continue;
		}

		const rewritten = { ...entry };
		const details = { ...asRecord(entry.details) };
		delete details.objective;
		details.version = 2;
		details.kind = "checkpoint";
		details.goalId = goalId;
		if (typeof details.timestamp !== "number") details.timestamp = Date.now();
		rewritten.details = details;
		rewritten.content = checkpointTriggerPrompt(goalId);

		const rewrittenLine = JSON.stringify(rewritten);
		outputLines.push(rewrittenLine);
		outputGraph.push(graphIdentity(rewritten));

		changed += 1;
		savedChars += rawLine.length - rewrittenLine.length;
	}
	return { outputLines, originalGraph, outputGraph, changed, savedChars, malformed };
}

function recoverSession(inputPath, apply) {
	const absolute = path.resolve(inputPath);
	let lstat;
	try {
		lstat = fs.lstatSync(absolute);
	} catch (error) {
		throw new Error(`cannot stat ${absolute}: ${error instanceof Error ? error.message : String(error)}`);
	}
	if (lstat.isSymbolicLink()) {
		throw new Error(`refusing symbolic link: ${absolute}`);
	}
	if (!lstat.isFile()) {
		throw new Error(`not a regular file: ${absolute}`);
	}

	const original = fs.readFileSync(absolute, "utf8");
	const originalMode = lstat.mode;

	const { outputLines, originalGraph, outputGraph, changed, savedChars, malformed } =
		transformSessionLines(original);

	// ── invariants before any write ──────────────────────────────────────
	if (JSON.stringify(outputGraph) !== JSON.stringify(originalGraph)) {
		throw new Error("graph identity mismatch: ids/parentIds would change — aborting");
	}
	const originalLines = original.split("\n");
	if (originalLines.length !== outputLines.length) {
		throw new Error("line count would change — aborting");
	}
	for (let i = 0; i < originalLines.length; i += 1) {
		if (originalLines[i] === outputLines[i]) continue;
		const parsed = JSON.parse(originalLines[i] ?? "{}");
		const isGoalEvent = asRecord(parsed)?.type === "custom_message" && asRecord(parsed)?.customType === GOAL_EVENT_ENTRY;
		if (!isGoalEvent) {
			throw new Error(`non-goal line ${i + 1} would change — aborting`);
		}
	}
	// Every non-header parentId must reference some id in the file.
	const ids = new Set(outputGraph.map((g) => g.id).filter(Boolean));
	for (const g of outputGraph) {
		if (g.parentId != null && !ids.has(g.parentId)) {
			throw new Error(`parentId ${g.parentId} has no matching entry — aborting`);
		}
	}

	console.log(`Session checkpoints: ${changed} legacy checkpoint(s) rewriteable`);
	console.log(`Estimated savings: ${savedChars} chars (${malformed} malformed unrelated line(s) preserved)`);

	if (!apply) {
		console.log("dry-run: no changes written.");
		return;
	}

	const timestamp = new Date().toISOString().replace(/[:.]/g, "-");
	const backup = `${absolute}.backup-${timestamp}`;
	fs.copyFileSync(absolute, backup);
	fs.chmodSync(backup, originalMode & 0o777);

	const temp = path.join(
		path.dirname(absolute),
		`.${path.basename(absolute)}.${process.pid}.${Date.now()}.tmp`,
	);
	let fd;
	try {
		fd = fs.openSync(temp, "wx", originalMode & 0o777);
		fs.writeFileSync(fd, outputLines.join("\n"), "utf8");
		fs.fsyncSync(fd);
		fs.closeSync(fd);
		fd = undefined;
		fs.renameSync(temp, absolute);
	} catch (error) {
		if (fd !== undefined) {
			try { fs.closeSync(fd); } catch { /* best effort */ }
		}
		try { fs.unlinkSync(temp); } catch { /* best effort */ }
		throw error;
	}

	try {
		fs.fsyncSync(fs.openSync(path.dirname(absolute), "r"));
	} catch { /* directory fsync is platform-dependent; best effort */ }

	// Post-write validation by re-reading.
	const reread = fs.readFileSync(absolute, "utf8");
	const rereadLines = reread.split("\n");
	if (rereadLines.length !== outputLines.length) throw new Error("post-write validation failed: line count changed");
	for (let i = 0; i < rereadLines.length; i += 1) {
		if (rereadLines[i] !== outputLines[i]) throw new Error(`post-write validation failed at line ${i + 1}`);
	}

	console.log(`Applied: ${changed} checkpoint(s) repaired.`);
	console.log(`Backup: ${backup}`);
}

// ── CLI parsing ────────────────────────────────────────────────────────────

function main(argv) {
	let session = null;
	let apply = false;
	let confirmPiClosed = false;
	for (let i = 0; i < argv.length; i += 1) {
		const arg = argv[i];
		if (arg === "--session") {
			session = argv[++i] ?? null;
		} else if (arg?.startsWith("--session=")) {
			session = arg.slice("--session=".length);
		} else if (arg === "--apply") {
			apply = true;
		} else if (arg === "--confirm-pi-closed") {
			confirmPiClosed = true;
		} else if (arg === "--dry-run") {
			apply = false;
		} else if (arg === "--help" || arg === "-h") {
			console.log("Usage: pi-goal-x-recover --session <session.jsonl> [--dry-run]");
			console.log("       pi-goal-x-recover --session <session.jsonl> --apply --confirm-pi-closed");
			return 0;
		} else {
			console.error(`error: unknown argument: ${arg}`);
			return 1;
		}
	}

	if (!session) {
		console.error("error: --session <path> is required");
		return 1;
	}
	if (apply && !confirmPiClosed) {
		console.error("error: --apply requires --confirm-pi-closed.");
		console.error("Close Pi first: rewriting the session behind Pi's in-memory state is unsafe.");
		return 1;
	}

	try {
		recoverSession(session, apply);
	} catch (error) {
		console.error(`error: ${error instanceof Error ? error.message : String(error)}`);
		return 1;
	}
	return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(import.meta.filename ?? "")) {
	process.exit(main(process.argv.slice(2)));
}
