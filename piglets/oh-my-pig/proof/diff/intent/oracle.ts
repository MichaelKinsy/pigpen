// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Michael Kinsy
// Drives omp's REAL argument validation from $OMP_SRC (tag v19.1.1, commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a).
// Embeds no omp source code; imports omp's own modules at runtime via NODE_PATH.
//
//   OMP_SRC=/path/to/omp-v19.1.1 bun --no-install oracle.ts schemas [real_wire.json]
//   OMP_SRC=/path/to/omp-v19.1.1 bun --no-install oracle.ts run <cases.jsonl> <corpus.jsonl>
//
// Entry point is the function the agent loop calls for tool arguments:
//   packages/agent/src/agent-loop.ts:3226 -> validateAgentToolArguments (packages/agent/src/tool-arguments.ts:23)
//   -> validateToolArguments (packages/ai/src/utils/validation.ts:2052).
// Tools are the real instances from packages/coding-agent/src/tools/index.ts (BUILTIN_TOOLS, HIDDEN_TOOLS)
// and tools/resolve.ts (specialToolCommands), so `tool.parameters` is omp's own omptype schema and
// `tool.lenientArgValidation` is omp's own flag. See README.md for the command line (NODE_PATH shim).
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

interface OmpTool {
	name: string;
	parameters: unknown;
	lenientArgValidation?: boolean;
}

interface CaseRow {
	id: number;
	tool: string;
	tag: string;
	args: string;
}

interface OmpVerdict {
	ok: boolean;
	normalized: string | null;
	error: string | null;
	schemaOk?: boolean;
}

type ToolFactory = (session: unknown) => Promise<OmpTool | null | undefined>;

const EXPECTED_TAG = "v19.1.1";
const EXPECTED_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a";
const mode = process.argv[2];
const HERE = import.meta.dir;
const OMP_SRC = process.env.OMP_SRC ?? "";
if (!OMP_SRC || !fs.existsSync(path.join(OMP_SRC, "packages", "coding-agent", "package.json"))) {
	console.error(`error: OMP_SRC=${JSON.stringify(OMP_SRC)} does not point to an omp ${EXPECTED_TAG} (commit ${EXPECTED_COMMIT}) source tree.`);
	process.exit(2);
}
const OMP = path.join(OMP_SRC, "packages");
const C = `${OMP}/coding-agent/src`;
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "ompdiff-"));
process.env.HOME = tmp;
process.env.XDG_CONFIG_HOME = tmp;
process.env.PI_CODING_AGENT_DIR = tmp;

// Dynamic imports are required because the omp source root ($OMP_SRC) is selected at runtime
// and HOME/PI_CODING_AGENT_DIR must be redirected to an isolated temp dir before omp initializes.
const { Settings } = (await import(`${C}/config/settings.ts`)) as {
	Settings: { isolated(overrides: Record<string, unknown>): unknown };
};
const idx = (await import(`${C}/tools/index.ts`)) as {
	BUILTIN_TOOLS: Record<string, ToolFactory>;
	HIDDEN_TOOLS: Record<string, ToolFactory>;
};
const res = (await import(`${C}/tools/resolve.ts`)) as {
	specialToolCommands(session: unknown): OmpTool[];
};
const { toolWireSchema } = (await import(`${OMP}/ai/src/utils/schema/wire.ts`)) as {
	toolWireSchema(tool: OmpTool): unknown;
};
const { validateAgentToolArguments } = (await import(`${OMP}/agent/src/tool-arguments.ts`)) as {
	validateAgentToolArguments(tool: OmpTool, call: { type: "toolCall"; id: string; name: string; arguments: unknown }): unknown;
};
const { validateToolArguments } = (await import(`${OMP}/ai/src/utils/validation.ts`)) as {
	validateToolArguments(
		tool: OmpTool,
		call: { type: "toolCall"; id: string; name: string; arguments: unknown },
		opts?: { keepUnknownRootKeys?: boolean },
	): unknown;
};

const settings = Settings.isolated({});
const base: Record<string, unknown> = {
	cwd: tmp,
	hasUI: true,
	settings,
	getSessionFile: () => null,
	getSessionId: () => "s",
	enableLsp: true,
	skills: [],
	getSessionSpawns: () => "*",
};
const session = new Proxy(base, { get: (t, p) => (p in t ? t[String(p)] : undefined) });
const all: Record<string, ToolFactory> = { ...idx.BUILTIN_TOOLS, ...idx.HIDDEN_TOOLS };
const tools: Record<string, OmpTool> = {};
for (const name of Object.keys(all)) {
	try {
		const tool = await all[name]!(session);
		if (tool) {
			void tool.parameters;
			tools[tool.name] = tool;
		}
	} catch (e: unknown) {
		const msg = e instanceof Error ? e.message : String(e);
		console.error(name, "FAIL", msg.split("\n")[0]);
	}
}
for (const t of res.specialToolCommands(session)) tools[t.name] = t;

if (mode === "schemas") {
	const out: Record<string, { lenient: boolean; wire: unknown }> = {};
	for (const [n, t] of Object.entries(tools)) {
		out[n] = { lenient: t.lenientArgValidation === true, wire: toolWireSchema(t) };
	}
	const wireOut = process.argv[3] ?? path.join(HERE, "real_wire.json");
	fs.writeFileSync(wireOut, JSON.stringify(out, null, 1));
	console.error("tools:", Object.keys(out).join(","));
} else if (mode === "run") {
	const lines = fs.readFileSync(process.argv[3]!, "utf8").split("\n").filter(Boolean);
	const outF = fs.openSync(process.argv[4]!, "w");
	let n = 0;
	for (const line of lines) {
		const c = JSON.parse(line) as CaseRow; // {id, tool, tag, args: <JSON text>}
		const tool = tools[c.tool];
		if (!tool) throw new Error(`no real tool ${c.tool}`);
		const lenient = tool.lenientArgValidation === true;
		const omp: OmpVerdict = { ok: false, normalized: null, error: null };
		try {
			const v = validateAgentToolArguments(tool, { type: "toolCall", id: "c", name: c.tool, arguments: JSON.parse(c.args) as unknown });
			omp.ok = true;
			omp.normalized = JSON.stringify(v);
		} catch (e: unknown) {
			omp.error = e instanceof Error ? e.message : String(e);
		}
		// A lenient tool never fails here: on a schema mismatch it receives its raw arguments and refuses in
		// execute(). schemaOk says whether omp's validator itself accepted (normalized = healed arguments).
		if (lenient) {
			try {
				validateToolArguments(
					tool,
					{ type: "toolCall", id: "c", name: c.tool, arguments: JSON.parse(c.args) as unknown },
					{ keepUnknownRootKeys: true },
				);
				omp.schemaOk = true;
			} catch {
				omp.schemaOk = false;
			}
		}
		fs.writeSync(outF, JSON.stringify({ id: c.id, tool: c.tool, lenient, tag: c.tag, args: c.args, omp }) + "\n");
		n++;
	}
	fs.closeSync(outF);
	console.error("ran", n);
} else {
	console.error("usage: bun oracle.ts schemas [real_wire.json] | run <cases.jsonl> <corpus.jsonl>");
}
fs.rmSync(tmp, { recursive: true, force: true });
process.exit(0);
