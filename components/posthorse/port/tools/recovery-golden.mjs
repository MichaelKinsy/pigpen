// Generates extensions/posthorse/testdata/recovery_golden.json.gz: scenarios for buildAutoHandoff with the output
// of the unmodified original (port/oracle/src/recovery.ts), recorded as length + SHA-256 (and the text itself when
// short). Run from the Package root inside the CI container:
//   ORACLE_COPY=<dir containing a copy of port/oracle/src and a node_modules with typebox> node port/tools/recovery-golden.mjs
// Strings longer than a few dozen characters are stored as {"$rep":[unit,count]} / {"$cat":[parts]} markers.
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { pathToFileURL } from "node:url";

const root = process.env.ORACLE_COPY;
const { buildAutoHandoff } = await import(pathToFileURL(`${root}/src/recovery.ts`).href);

function mulberry32(a) {
	return () => {
		a = (a + 0x6d2b79f5) | 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}
const R = (unit, count) => ({ $rep: [unit, count] });
const C = (...parts) => ({ $cat: parts });
function expand(v) {
	if (Array.isArray(v)) return v.map(expand);
	if (v !== null && typeof v === "object") {
		if ("$rep" in v) return v.$rep[0].repeat(v.$rep[1]);
		if ("$cat" in v) return v.$cat.map((p) => (typeof p === "string" ? p : expand(p))).join("");
		return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, expand(x)]));
	}
	return v;
}

// Decoded JSON objects lose their member order in the Go SDK (port/lane-CoreLane.md finding 1), so the text a
// record prints for a tool call's arguments is in sorted order there. The scenarios use sorted members.
function sortKeys(v) {
	if (Array.isArray(v)) return v.map(sortKeys);
	if (v !== null && typeof v === "object" && !("$rep" in v) && !("$cat" in v)) {
		return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys(v[k])]));
	}
	return v;
}
const seedBase = Number(process.env.SEED ?? 20261106);
const scenarios = [];

function build(name, rnd, make) {
	const sc = make(rnd);
	const entries = expand(sc.entries);
	const projected =
		sc.projected === "default"
			? entries
					.filter((e) => e.type === "message")
					.map((sourceEntry) => ({ sourceEntry, messages: [sourceEntry.message] }))
			: sc.projected.map((p) => ({
					sourceEntry: entries[p.source],
					messages: p.messages === "same" ? [entries[p.source].message] : expand(p.messages),
				}));
	let out;
	try {
		out = buildAutoHandoff(entries, projected, sc.maxChars, {
			ownerQuestionRegistered: sc.ownerQ,
			retainedEntryIds: new Set(sc.retained),
		});
	} catch (error) {
		return;
	}
	if (process.env.DUMP_DIR) writeFileSync(`${process.env.DUMP_DIR}/${name}.txt`, out);
	const record = {
		name,
		entries: sc.entries,
		projected: sc.projected,
		maxChars: sc.maxChars,
		ownerQ: sc.ownerQ,
		retained: sc.retained,
		length: out.length,
		sha256: createHash("sha256").update(out, "utf8").digest("hex"),
	};
	if (out.length <= 500) record.expected = out;
	scenarios.push(record);
}

const pick = (rnd, list) => list[Math.floor(rnd() * list.length)];
const chance = (rnd, p) => rnd() < p;

const TEXTS = [
	"fix the bug in parser",
	"  padded request\n",
	"line one\nline two\n\n   line three\t tabbed",
	"nbsp\u00a0here and\u2003em space and\u3000ideographic",
	"emoji 😀 text 👍🏽 end",
	"short",
	"Do not touch the billing tables\n\nuntil I confirm",
	"",
	"   ",
	R("a", 119),
	R("a", 120),
	R("a", 121),
	R("😀", 60),
	C(R("😀", 59), "a"),
	C("  ", R("b", 118), "   c"),
	"ascii-only but with a fairly long sentence that keeps going to pass one hundred and twenty characters so the preview ellipsis boundary is exercised in the index line",
];
const LONG = () => [R("L", 900), R("m", 3000), R("é", 4500), R("😀", 2600), R("x", 15000)];
const MIMES = ["image/png", "image/jpeg", "", "image/webp"];
const WORK_TOOLS = ["bash", "read", "write", "edit", "screenshot", "work", "grep"];
const NOTE_PATHS = [
	"/ws/.pi/notes/alpha.md",
	".pi/notes/beta.md",
	"./.pi/notes/gamma.md",
	"/ws/posthorse-notes/delta.md",
	"epsilon.md",
	"C:\\ws\\.pi\\notes\\zeta.md",
	"/home/u/.pi/package-notes/" + "ab".repeat(32) + "/notes/eta.md",
	"/home/u/.pi/agent/notes/p/q/r/theta.md",
	"/ws/.pi/notes/.forks/x/iota.md",
	"../kappa.md",
	"dir/../lambda.md",
	"/ws/.pi/notes/sub/mu.md",
	"  /ws/.pi/notes/nu.md  ",
	"/etc/other.txt",
	"c:\\ws\\lower.md",
	"C:\\ws\\upper.md",
	"sub\\deep.md",
	"././dot.md",
	"/ws/.pi/notes/.///t1.md",
	"/ws/.pi/notes///t2.md",
	"/ws/.pi/notes/././t3.md",
	"/ws/.pi/notes/t4/",
	".",
	"..",
	"sub\\file.md",
	"/home/u/.pi/notes/p/q/r/s.md",
	"/srv/notes-x/a/b.md",
	"/srv/notes-x/c.md",
	"é/ü😀.md",
	".pi/notes/ü.md",
];

function makeScenario(rnd) {
	let n = 0;
	let inNote = false;
	const id = (prefix) => {
		n++;
		const base = `${prefix}-${n}`;
		if (chance(rnd, 0.06)) return C(base, "-", R("i", 125));
		if (chance(rnd, 0.02)) return C("a", R("😀", 61), base);
		if (!inNote && chance(rnd, 0.015)) return "";
		return base;
	};
	const entries = [];
	const retained = [];
	const idOf = (e) => (e.id === undefined ? undefined : expand(e.id));
	const ts = () => (chance(rnd, 0.05) ? C("2026-11-01T00:00:", R("9", 85)) : chance(rnd, 0.1) ? undefined : `2026-11-01T10:${String(n % 60).padStart(2, "0")}:00Z`);
	const add = (e) => {
		for (const k of Object.keys(e)) if (e[k] === undefined) delete e[k];
		// Entries without an id (or with ""): only where no note receipt can hang on them, because the Go noteTouch
		// cannot tell receiptId "" from undefined (port/lane-RecoveryLane.md, finding 3).
		const plain = e.message?.role === "user" || (e.message?.role === "toolResult" && !inNote && e.message.toolName !== "codemode" && e.message.toolName !== "notes");
		if (plain && !inNote && chance(rnd, 0.03)) delete e.id;
		if (Array.isArray(e.message?.content)) {
			for (const part of e.message.content) {
				if (part?.type === "toolCall") {
					if (part.arguments !== undefined) part.arguments = sortKeys(part.arguments);
					if (part.executionArguments !== undefined) part.executionArguments = sortKeys(part.executionArguments);
				}
			}
		}
		entries.push(e);
		return e;
	};
	const content = () => {
		const r = rnd();
		if (r < 0.15) return pick(rnd, LONG());
		if (r < 0.28) {
			const parts = [{ type: "text", text: pick(rnd, TEXTS) }];
			if (chance(rnd, 0.6)) parts.push({ type: "image", data: "QUJD", mimeType: pick(rnd, MIMES) });
			if (chance(rnd, 0.3)) parts.push({ type: "image", data: "REVG", mimeType: pick(rnd, MIMES) });
			if (chance(rnd, 0.3)) parts.push({ type: "text", text: pick(rnd, TEXTS) });
			return parts;
		}
		return pick(rnd, TEXTS);
	};
	const windowBoundary = () => {
		const kind = pick(rnd, ["auto-cw", "auto-compaction", "manual-cw", "manual-compaction", "overflow", "explicit"]);
		const handoff = chance(rnd, 0.8)
			? C(
					kind.startsWith("manual") ? "Manual task summary " : C(pick(rnd, ["", "", "", "  \n", "\u00a0", "\n\t"]), "Automatic context rollover recovery record.\nPRIOR_"),
					pick(rnd, [R("h", 50), R("h", 3000), R("h", 9000), "short"]),
				)
			: undefined;
		const base = { id: id("win"), timestamp: ts() };
		if (kind === "auto-cw" || kind === "manual-cw") return add({ type: "context_window", ...base, handoff });
		const reason = kind === "overflow" ? "overflow" : kind === "explicit" ? "explicit" : "threshold";
		return add({
			type: "compaction",
			...base,
			summary: handoff,
			firstKeptEntryId: null,
			details: kind.startsWith("manual") ? undefined : { posthorse: chance(rnd, 0.9) ? 1 : 2, reason },
		});
	};
	// prefix windows
	const windows = chance(rnd, 0.15) ? 3 + Math.floor(rnd() * 10) : Math.floor(rnd() * 4);
	for (let w = 0; w < windows; w++) {
		for (let k = Math.floor(rnd() * 3); k > 0; k--) bodyEntry();
		windowBoundary();
	}
	const bodyCount = 2 + Math.floor(rnd() * 22);
	for (let k = 0; k < bodyCount; k++) bodyEntry();
	if (chance(rnd, 0.15)) windowBoundary();
	if (chance(rnd, 0.3)) for (let k = Math.floor(rnd() * 4); k > 0; k--) bodyEntry();

	function noteToolCall() {
		inNote = true;
		try {
			noteToolCallBody();
		} finally {
			inNote = false;
		}
	}
	function noteToolCallBody() {
		const callId = id("call");
		const path = pick(rnd, NOTE_PATHS);
		const kind = pick(rnd, ["dir-learn", "notes-op", "notes-write", "notes-append", "notes-read", "notes-clear", "notes-scope", "write", "edit", "read", "replace_text", "apply_patch", "codemode"]);
		const keep = chance(rnd, 0.4);
		const addK = (e) => {
			const added = add(e);
			if (keep) retained.push(idOf(added));
			return added;
		};
		const asst = (calls) => addK({ type: "message", id: id("a"), timestamp: ts(), message: { role: "assistant", content: calls, ...(chance(rnd, 0.3) ? { stopReason: "toolUse" } : {}) } });
		const res = (toolName, toolCallId, text, extra = {}, editP = 0.25) => {
			const added = addK({ type: "message", id: id("r"), timestamp: ts(), message: { role: "toolResult", toolName, toolCallId, isError: chance(rnd, 0.08), content: text, ...extra } });
			if (chance(rnd, editP)) add({ type: "context_edit", id: id("ed"), targetId: idOf(added), replacement: pick(rnd, [{ content: "edited receipt" }, { content: text }]) });
			return added;
		};
		const slash = path.trim().replaceAll("\\", "/");
		const shown = () => pick(rnd, [path.trim(), slash, path.trim(), `/ws/.pi/notes/${slash.replace(/^(\.\/)+/, "")}`]);
		const wrote = (verb) => {
			const form = pick(rnd, [0, 0, 0, 1, 2, 3, 4, 5]);
			const line = `${verb} ${shown()}`;
			if (form === 1) return `${verb} \n${line}`;
			if (form === 2) return `intro\u2028${line}`;
			if (form === 3) return `intro\u2029${line}`;
			if (form === 4) return `${line}\rtrailing text`;
			if (form === 5) return `${line}\r\nextra`;
			return line;
		};
		const dirLine = () => (chance(rnd, 0.3) ? `Notes directory: ${pick(rnd, ["/ws/.pi/notes", "/srv/notes-x", "C:\\srv\\nd\\", "/ws/.pi/notes//", "/other/dir"])}\n` : "");
		switch (kind) {
			case "notes-write":
			case "notes-clear": {
				asst([{ type: "toolCall", id: callId, name: pick(rnd, ["notes", "notes", "ns:notes"]), arguments: { op: "write", path, content: kind === "notes-clear" ? "" : "state" } }]);
				if (chance(rnd, 0.9)) res("notes", callId, `${dirLine()}${wrote("Wrote")}`);
				return;
			}
			case "dir-learn": {
				// A notes result teaches a directory; file tools only resolve note paths through known directories.
				const dir = pick(rnd, ["/srv/notes-x", "/srv/notes-y/", "D:\\data\\notes\\"]);
				const listed = id("call");
				const dirSlash = dir.replaceAll("\\", "/").replace(/\/+$/, "");
				if (chance(rnd, 0.5)) {
					// the directory is inferred from a relative path (with backslashes) and the path the result reports
					const rel = pick(rnd, ["sub\\deep.md", "./sub/deep.md", "x.md", ".\\y\\z.md"]);
					asst([{ type: "toolCall", id: listed, name: "notes", arguments: { op: "write", path: rel, content: "x" } }]);
					res("notes", listed, `Wrote ${dirSlash}/${rel.replaceAll("\\", "/").replace(/^(\.\/)+/, "")}`, { isError: false });
				} else {
					asst([{ type: "toolCall", id: listed, name: "notes", arguments: { op: "list" } }]);
					res("notes", listed, `Notes directory: ${dir}\n(no notes)`, chance(rnd, 0.6) ? { isError: true } : {});
				}
				const target = `${dir.replaceAll("\\", "/").replace(/\/+$/, "")}/${pick(rnd, ["a/b.md", "c.md", ".forks/x.md"])}`;
				asst([{ type: "toolCall", id: callId, name: pick(rnd, ["write", "edit", "notes"]), arguments: { path: target, op: "write", content: "x" } }]);
				res("write", callId, "ok", { isError: false });
				return;
			}
			case "notes-op": {
				const verb = pick(rnd, ["Wrote", "Appended to", "File:"]);
				asst([{ type: "toolCall", id: callId, name: "notes", arguments: pick(rnd, [{ op: "list", path }, { path }, { op: "search", path }]) }]);
				res("notes", callId, `${dirLine()}${wrote(verb)}`);
				return;
			}
			case "notes-append":
				asst([{ type: "toolCall", id: callId, name: "notes", arguments: { op: "append", path, content: "more" } }]);
				if (chance(rnd, 0.9)) res("notes", callId, wrote("Appended to"));
				return;
			case "notes-read":
				asst([{ type: "toolCall", id: callId, name: "notes", arguments: { op: "read", path } }]);
				if (chance(rnd, 0.9)) res("notes", callId, `${wrote("File:")}\nbody`.replace("File: ", "File: "));
				return;
			case "notes-scope":
				asst([{ type: "toolCall", id: callId, name: "notes", arguments: { op: "write", path: "scoped.md", scope: pick(rnd, ["/ws/.pi/notes", "project", "/home/u/.pi/agent/notes/p/q/r"]), content: "x" } }]);
				res("notes", callId, "Wrote /ws/.pi/notes/scoped.md");
				return;
			case "write":
			case "edit":
			case "read":
				asst([{ type: "toolCall", id: callId, name: kind, arguments: { path, content: kind === "write" ? pick(rnd, ["body", ""]) : undefined } }]);
				if (chance(rnd, 0.9)) res(kind, callId, "ok");
				return;
			case "replace_text":
				asst([{ type: "toolCall", id: callId, name: "replace_text", arguments: { files: [{ path, edits: [] }, { path: pick(rnd, NOTE_PATHS), edits: [] }], ...(chance(rnd, 0.1) ? { preview: true } : {}) } }]);
				res("replace_text", callId, "Updated", chance(rnd, 0.5) ? { details: { modifiedFiles: [path.trim()], files: [{ path: path.trim(), operation: "edit" }] } } : {});
				return;
			case "apply_patch": {
				const second = pick(rnd, NOTE_PATHS);
				const input = `*** Begin Patch\n*** Update File: ${path}\n${chance(rnd, 0.4) ? `*** Move to: ${second}\n` : ""}@@\n-a\n+b\n${chance(rnd, 0.4) ? `*** Delete File: ${second}\n` : ""}${chance(rnd, 0.3) ? `*** Add File: ${pick(rnd, NOTE_PATHS)}\n` : ""}*** End Patch`;
				asst([{ type: "toolCall", id: callId, name: "apply_patch", arguments: { input } }]);
				res("apply_patch", callId, "Applied", chance(rnd, 0.5) ? { details: { modifiedFiles: [second.trim()], files: [{ path, moveTo: second, operation: "move", status: "failed" }] } } : {});
				return;
			}
			case "codemode": {
				const nestedKinds = [
					{ name: "notes", arguments: { op: "write", path, content: "x" }, result: wrote("Wrote"), status: pick(rnd, ["ok", "error", "unfinished", undefined]) },
					{ name: "notes", arguments: { op: "read", path }, result: wrote("File:") + "\nbody", status: pick(rnd, ["ok", undefined]) },
					{ name: "notes", arguments: { op: "read", path: "decoy.md" }, args: { op: "write", path, content: "y" }, input: { op: "append", path }, result: wrote("File:") },
					{ name: "notes", args: { op: "append", path }, input: { op: "read", path }, result: wrote("Appended to") },
					{ toolName: "notes", input: { op: "read", path }, isError: pick(rnd, [true, false]), result: wrote("File:") },
					{ name: "notes", arguments: { op: "write", path, content: "" }, result: wrote("Wrote") },
					{ toolName: "write", args: { path, content: "x" }, result: {} },
					{ name: "edit", input: { path }, result: {}, status: pick(rnd, ["ok", "error", "unfinished"]) },
					{ name: "notes", arguments: { op: "append", path }, result: wrote("Appended to"), error: pick(rnd, ["", "boom"]) },
				];
				if (chance(rnd, 0.5)) {
					const earlier = id("call");
					asst([{ type: "toolCall", id: earlier, name: "notes", arguments: { op: "write", path, content: "before" } }]);
					res("notes", earlier, wrote("Wrote"));
				}
				const nested = [pick(rnd, nestedKinds), pick(rnd, nestedKinds)];
				const holder = pick(rnd, ["nestedCalls", "details", "details.nestedCalls"]);
				const extra = holder === "nestedCalls" ? { nestedCalls: { calls: nested } } : holder === "details" ? { details: { calls: nested } } : { details: { nestedCalls: { calls: nested } } };
				res("codemode", callId, "done", extra, 0.7);
				return;
			}
		}
	}

	function bodyEntry() {
		const r = rnd();
		if (r < 0.2) {
			const e = add({ type: "message", id: id("u"), timestamp: ts(), message: { role: "user", content: content() } });
			if (chance(rnd, 0.25)) retained.push(idOf(e));
		} else if (r < 0.33) {
			const e = add({
				type: "custom_message",
				id: id("cm"),
				timestamp: ts(),
				customType: pick(rnd, ["intercom", "agent-irc", "posthorse-reminder", "todo-list-context", C("t", "-", R("T", 100)), undefined]),
				content: content(),
				display: pick(rnd, [true, true, false, undefined]),
			});
			if (chance(rnd, 0.2)) retained.push(idOf(e));
		} else if (r < 0.48) {
			const calls = [];
			const count = 1 + Math.floor(rnd() * 3);
			const ids = [];
			for (let i = 0; i < count; i++) {
				const callId = id("tc");
				ids.push(callId);
				calls.push({
					type: "toolCall",
					id: callId,
					name: pick(rnd, [...WORK_TOOLS, "ask_question", "history"]),
					...(chance(rnd, 0.1) ? { namespace: pick(rnd, ["ns", "mcp"]) } : {}),
					arguments: pick(rnd, [{}, { command: "npm test" }, { path: "a.txt" }, null, undefined, { n: 1, s: "é😀" }]),
					...(chance(rnd, 0.15) ? { executionArguments: pick(rnd, [{ x: 1 }, null, "raw"]) } : {}),
					...(chance(rnd, 0.2) ? { async: true } : {}),
				});
			}
			const e = add({ type: "message", id: id("as"), timestamp: ts(), message: { role: "assistant", stopReason: pick(rnd, ["toolUse", "stop", "error", "aborted", "length", undefined]), content: chance(rnd, 0.85) ? [...(chance(rnd, 0.4) ? [{ type: "text", text: "PROSE" }] : []), ...calls] : "plain reply" } });
			if (chance(rnd, 0.15)) retained.push(idOf(e));
			for (const callId of ids) {
				if (chance(rnd, 0.8)) {
					const rr = add({
						type: "message",
						id: id("tr"),
						timestamp: ts(),
						message: {
							role: "toolResult",
							toolCallId: callId,
							toolName: chance(rnd, 0.85) ? pick(rnd, [...WORK_TOOLS, "ask_question"]) : undefined,
							...(chance(rnd, 0.08) ? { namespace: "ns" } : {}),
							...(chance(rnd, 0.25) ? { isError: true } : chance(rnd, 0.3) ? { isError: false } : {}),
							content: pick(rnd, [pick(rnd, TEXTS), pick(rnd, LONG()), [{ type: "text", text: "3 failed " }, { type: "image", data: "QUJD", mimeType: pick(rnd, MIMES) }], "", [{ type: "image", data: "QQ", mimeType: "image/png" }]]),
						},
					});
					if (chance(rnd, 0.15)) retained.push(idOf(rr));
				}
			}
		} else if (r < 0.58) {
			add({ type: "message", id: id("tr"), timestamp: ts(), message: { role: "toolResult", toolCallId: pick(rnd, ["orphan", undefined, "ask"]), toolName: pick(rnd, ["bash", "ask_question"]), ...(chance(rnd, 0.1) ? { namespace: "ns" } : {}), isError: pick(rnd, [false, true, undefined]), content: pick(rnd, TEXTS) } });
		} else if (r < 0.7) {
			noteToolCall();
		} else if (r < 0.76) {
			// ask_question exchange
			const callId = id("q");
			add({ type: "message", id: id("qa"), message: { role: "assistant", content: [{ type: "toolCall", id: callId, name: "ask_question", ...(chance(rnd, 0.15) ? { namespace: "ns" } : {}), arguments: {} }] } });
			add({ type: "message", id: id("qr"), timestamp: ts(), message: { role: "toolResult", toolName: "ask_question", toolCallId: callId, ...(chance(rnd, 0.1) ? { namespace: "ns" } : {}), isError: pick(rnd, [false, true, undefined]), content: pick(rnd, [pick(rnd, TEXTS), [{ type: "image", data: "QUJD", mimeType: "image/png" }]]) } });
		} else if (r < 0.84) {
			const target = entries.length > 0 ? idOf(pick(rnd, entries)) : undefined;
			add({ type: "context_edit", id: id("ed"), targetId: target, replacement: pick(rnd, [null, { content: "EDITED " + pick(rnd, TEXTS) }, { content: [{ type: "text", text: "EDITED" }] }, {}]) });
		} else if (r < 0.88) {
			const bs = add({ type: "branch_summary", id: id("bs"), summary: "branch summary" });
			if (chance(rnd, 0.4)) retained.push(idOf(bs));
		} else if (r < 0.93) {
			add({ type: "message", id: id("as"), timestamp: ts(), message: { role: "assistant", stopReason: "stop", content: "All done." } });
		} else {
			add({ type: pick(rnd, ["custom", "label", "model_change"]), id: id("x"), customType: "state", data: {}, ...(chance(rnd, 0.4) ? { message: { role: "toolResult", toolName: "notes", toolCallId: "ghost", content: "Wrote /ws/.pi/notes/ghost.md" } } : {}) });
		}
	}

	const maxChars = pick(rnd, [150, 400, 700, 1000, 1000, 1500, 2500, 4000, 7000, 12000, 20000, 20000]);
	const ownerQ = chance(rnd, 0.5);
	// projected: default, or an explicit one with some entries omitted/edited
	let projected = "default";
	if (chance(rnd, 0.4)) {
		projected = [];
		entries.forEach((e, index) => {
			if (e.type !== "message") return;
			const r = rnd();
			if (r < 0.08) projected.push({ source: index, messages: [] });
			else if (r < 0.18) projected.push({ source: index, messages: [{ ...e.message, content: "PROJECTED EDIT " + index }] });
			else projected.push({ source: index, messages: "same" });
		});
	}
	const retainedFinal = [...new Set(retained.filter((x) => x !== undefined))];
	if (chance(rnd, 0.1)) retainedFinal.push("");
	const retainedSet = chance(rnd, 0.55) ? retainedFinal : [];
	return { entries, projected, maxChars, ownerQ, retained: retainedSet };
}

const count = Number(process.env.COUNT ?? 600);
for (let i = 0; i < count; i++) {
	const rnd = mulberry32(seedBase + i * 7919);
	build(`random-${i}`, rnd, makeScenario);
}
writeFileSync(process.env.OUT ?? "recovery_golden.json.gz", gzipSync(JSON.stringify({ seed: seedBase, scenarios }), { level: 9 }));
console.log(`wrote ${scenarios.length} scenarios`);
