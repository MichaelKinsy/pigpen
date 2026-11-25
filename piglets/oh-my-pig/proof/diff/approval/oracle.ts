// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Michael Kinsy
// Drives omp v19.1.1's REAL approval engine from $OMP_SRC (tag v19.1.1, commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a).
//
//   OMP_SRC=/path/to/omp-v19.1.1 bun --no-install oracle.ts <cases.jsonl> <corpus.jsonl>
//
// What runs is omp's own code from $OMP_SRC:
//   tools/approval.ts   resolveApprovalFromContext, approvalRestrictions, resolveApproval, sessionApprovalScope,
//                       denyError, requestApproval (its select fallback gives the prompt title and the choices),
//                       recordApprovalGrant
//   the real tool instances of tools/index.ts (BUILTIN_TOOLS, HIDDEN_TOOLS), tools/resolve.ts (specialToolCommands),
//   mcp/tool-bridge.ts (MCPTool), task/executor.ts (createSubagentSettings), config/settings.ts (Settings, with a
//   real global config.yml and a real project .omp/config.yml where a case has a project layer).
// Note on omp-derived text: the two headless refusal format strings below (lines 180-182) mirror
// packages/coding-agent/src/extensibility/extensions/wrapper.ts:476-490 (MIT, Can Bölük / Stencil Labs),
// because that wrapper lives inside a runner class that cannot be instantiated standalone.
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

interface OmpTool {
	name: string;
	approvalCacheTargets?: (args: unknown) => readonly unknown[] | undefined;
}

interface SettingsLike {
	grantAuthority(): SettingsLike;
}

interface ResolvedApproval {
	policy: string;
	source: string;
	tier: string;
	reason?: string;
}

interface ScopeInfo {
	key: string;
	subject: string;
}

interface ApprovalModule {
	resolveApprovalFromContext(ctx: { settings: SettingsLike } | undefined): {
		approvalMode: string;
		userPolicies: Record<string, unknown>;
	};
	approvalRestrictions(settings: SettingsLike, cwd: string): unknown;
	resolveApproval(
		tool: OmpTool,
		args: unknown,
		mode: string,
		policies: Record<string, unknown>,
		grants: unknown,
		restrictions: unknown,
		ctx: unknown,
	): ResolvedApproval;
	sessionApprovalScope(resolved: ResolvedApproval, toolName: string): ScopeInfo | undefined;
	denyError(resolved: ResolvedApproval, toolName: string): Error;
	requestApproval(
		ui: { select(title: string, choices: string[]): Promise<string | undefined> },
		tool: OmpTool,
		args: unknown,
		opts: { reason?: string; sessionSubject?: string; grants: boolean },
	): Promise<unknown>;
	recordApprovalGrant(
		tool: OmpTool,
		args: unknown,
		grants: unknown,
		tier: string,
		opts: { grant: string; always: boolean },
	): void;
}

interface CaseSettings {
	operator: Record<string, unknown>;
	project: Record<string, unknown> | null;
	trusted: boolean;
	operatorRaw?: string;
}

interface PriorCall {
	tool: string;
	args: string;
	answer: string;
}

interface ApprovalCase {
	id: number;
	section: string;
	tool: string;
	args: string;
	settings: CaseSettings;
	ui: string;
	session: string;
	state?: {
		nilSetting?: boolean;
		prior?: PriorCall;
	};
}

interface Verdict {
	decision: string;
	source?: string;
	tier?: string;
	reason?: string;
	text?: string;
	promptTitle?: string;
	choices?: string[];
}

interface Env {
	settings: SettingsLike | null;
	authority: SettingsLike | null;
	cwd: string;
	tools: Record<string, OmpTool>;
	operatorView?: Env;
	error?: string;
}

type ToolFactory = (session: unknown) => Promise<OmpTool | null | undefined>;

const EXPECTED_TAG = "v19.1.1";
const EXPECTED_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a";
const OMP_SRC = process.env.OMP_SRC ?? "";
if (!OMP_SRC || !fs.existsSync(path.join(OMP_SRC, "packages", "coding-agent", "package.json"))) {
	console.error(`error: OMP_SRC=${JSON.stringify(OMP_SRC)} does not point to an omp ${EXPECTED_TAG} (commit ${EXPECTED_COMMIT}) source tree.`);
	process.exit(2);
}

const OMP = path.join(OMP_SRC, "packages");
const C = `${OMP}/coding-agent/src`;
const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "ompappr-")));
const home = path.join(root, "home");
const defaultAgentDir = path.join(home, ".omp", "agent");
const defaultCwd = path.join(root, "work");
fs.mkdirSync(defaultAgentDir, { recursive: true });
fs.mkdirSync(defaultCwd, { recursive: true });
process.env.HOME = home;
process.env.XDG_CONFIG_HOME = path.join(home, ".config");
process.env.PI_CODING_AGENT_DIR = defaultAgentDir;

// Dynamic imports are required because the omp source root ($OMP_SRC) is selected at runtime
// and HOME/PI_CODING_AGENT_DIR must point to an isolated temp dir before omp initializes.
const { Settings } = (await import(`${C}/config/settings.ts`)) as {
	Settings: {
		isolated(overrides: Record<string, unknown>): SettingsLike;
		loadIsolated(opts: { cwd: string; agentDir: string }): Promise<SettingsLike>;
	};
};
const idx = (await import(`${C}/tools/index.ts`)) as {
	BUILTIN_TOOLS: Record<string, ToolFactory>;
	HIDDEN_TOOLS: Record<string, ToolFactory>;
};
const res = (await import(`${C}/tools/resolve.ts`)) as {
	specialToolCommands(session: unknown): OmpTool[];
};
const A = (await import(`${C}/tools/approval.ts`)) as ApprovalModule;
const EX = (await import(`${C}/task/executor.ts`)) as {
	createSubagentSettings(settings: SettingsLike): SettingsLike;
};
const { MCPTool } = (await import(`${C}/mcp/tool-bridge.ts`)) as {
	MCPTool: new (conn: Record<string, unknown>, def: Record<string, unknown>) => OmpTool;
};

// The port's facts the corpus is built for (meta.json; the Go test reports when the port's differ).
const MOUNTED = (process.env.OMP_APPROVAL_MOUNTED ?? "ast_edit,debug,lsp").split(",").filter(Boolean);
const ACTIVE = (process.env.OMP_APPROVAL_ACTIVE ?? "read,bash,edit,ast_edit,ask,debug,eval,glob,grep,lsp,task,wait,todo,web_search,write").split(",").filter(Boolean);

function nest(flat: Record<string, unknown>): Record<string, unknown> {
	const out: Record<string, unknown> = {};
	for (const [id, value] of Object.entries(flat)) {
		const parts = id.split(".");
		let at: Record<string, unknown> = out;
		for (const part of parts.slice(0, -1)) {
			let next = at[part];
			if (!next || typeof next !== "object") {
				next = {};
				at[part] = next;
			}
			at = next as Record<string, unknown>;
		}
		at[parts[parts.length - 1]!] = value;
	}
	return out;
}

async function buildTools(settings: SettingsLike, sub: boolean, tui: boolean, cwd: string): Promise<Record<string, OmpTool>> {
	const base: Record<string, unknown> = {
		cwd,
		hasUI: tui,
		settings,
		getSessionFile: () => null,
		getSessionId: () => "s",
		enableLsp: true,
		skills: [],
		getSessionSpawns: () => "*",
		taskDepth: sub ? 1 : 0,
		tuiTranscript: tui && !sub,
		toolCommands: { mountedNames: new Set(MOUNTED), tools: new Map(), builtInNames: new Set(), isActive: () => true },
		isToolActive: (_n: string) => true,
	};
	const session = new Proxy(base, { get: (t, p) => (p in t ? t[String(p)] : undefined) });
	const all: Record<string, ToolFactory> = { ...idx.BUILTIN_TOOLS, ...idx.HIDDEN_TOOLS };
	const tools: Record<string, OmpTool> = {};
	for (const name of Object.keys(all)) {
		try {
			const tool = await all[name]!(session);
			if (tool) tools[tool.name] = tool;
		} catch {
			/* tool not available in this session */
		}
	}
	for (const t of res.specialToolCommands(session)) tools[t.name] = t;
	// The real MCP tool class (mcp/tool-bridge.ts:794): approval "write", formatApprovalDetails = formatApprovalArguments.
	const conn: Record<string, unknown> = { name: "srv", config: {}, capabilities: {} };
	tools["mcp__srv_do_thing"] = new MCPTool(conn, { name: "do_thing", description: "d", inputSchema: { type: "object", properties: {} } });
	// An extension tool that declares no approval (extensibility/extensions/types.ts:1047-1049: omitted means exec).
	tools["ext_plain"] = { name: "ext_plain" };
	return tools;
}

let groupSeq = 0;
const envCache = new Map<string, Env>();

/** Settings and tools of one case group. `withProject` false drops the project layer (PiG's untrusted project). */
async function envFor(c: ApprovalCase, withProject: boolean): Promise<Env> {
	const sub = c.session === "subagent";
	const tui = c.ui === "interactive";
	const s = c.settings;
	const project = withProject ? s.project : null;
	const key = JSON.stringify([s.operator, s.operatorRaw ?? null, project, sub, tui]);
	const hit = envCache.get(key);
	if (hit) return hit;
	let settings: SettingsLike;
	let cwd = defaultCwd;
	let env: Env;
	try {
		if (project || s.operatorRaw !== undefined) {
			const dir = path.join(root, `g${++groupSeq}`);
			const agentDir = path.join(dir, "agent");
			cwd = path.join(dir, "work");
			fs.mkdirSync(agentDir, { recursive: true });
			fs.mkdirSync(path.join(cwd, ".omp"), { recursive: true });
			// JSON is YAML: the layers are written as the nested object of the setting ids.
			fs.writeFileSync(path.join(agentDir, "config.yml"), s.operatorRaw ?? JSON.stringify(nest(s.operator), null, 1));
			if (project) fs.writeFileSync(path.join(cwd, ".omp", "config.yml"), JSON.stringify(nest(project), null, 1));
			settings = await Settings.loadIsolated({ cwd, agentDir });
		} else {
			settings = Settings.isolated(s.operator);
		}
		const authority = settings.grantAuthority();
		const sessionSettings = sub ? EX.createSubagentSettings(settings) : settings; // task/executor.ts:1258-1322
		env = { settings: sessionSettings, authority, cwd, tools: await buildTools(sessionSettings, sub, tui, cwd) };
		if (project) {
			// The OPERATOR view of the same session: omp's Settings.grantAuthority() (global, --config and runtime layers,
			// no project layer; config/settings.ts:954-971), same working directory, a subagent overlay on top for a child.
			const operatorSettings = sub ? EX.createSubagentSettings(authority) : authority;
			env.operatorView = { settings: operatorSettings, authority, cwd, tools: await buildTools(operatorSettings, sub, tui, cwd) };
		}
	} catch (e: unknown) {
		const msg = e instanceof Error ? e.message : String(e);
		env = { settings: null, authority: null, cwd, tools: {}, error: msg.split("\n")[0]!.replace(/\(moved to [^)]*\)/, "(moved to <backup>)") };
	}
	envCache.set(key, env);
	return env;
}

// The project directory of omp is .omp, of PiG .pig: a folder listing in a prompt names it.
const norm = (cwd: string) => (s: string) => s.split(cwd).join("$CWD").replace(/^(\s*- )\.omp\/$/gm, "$1.pig/");

interface GrantStore {
	set: Set<string>;
	present(keys: readonly string[]): Set<string>;
	record(key: string): void;
}

function makeGrants(): GrantStore {
	const set = new Set<string>();
	return { set, present: (keys: readonly string[]) => new Set(keys.filter(k => set.has(k))), record: (key: string) => void set.add(key) };
}

interface GateState {
	grants?: GrantStore;
	session: Set<string>;
}

/** The gate of wrapper.ts:286-524 for one call. `answer` is what the user picks when it prompts (undefined: dismiss). */
async function gate(
	env: Env,
	tool: OmpTool,
	args: unknown,
	mode: string,
	policies: Record<string, unknown>,
	c: ApprovalCase,
	st: GateState,
	answer?: string,
): Promise<Verdict & { answerLabel?: string }> {
	const N = norm(env.cwd);
	const restrictions = env.settings ? A.approvalRestrictions(env.settings, env.cwd) : undefined;
	const ctx = { toolNames: ACTIVE, settings: env.settings };
	const grants = mode === "always-ask-cached" ? st.grants : undefined; // wrapper.ts:296-299
	const resolved = A.resolveApproval(tool, args, mode, policies, grants, restrictions, ctx);
	const v: Verdict & { answerLabel?: string } = { decision: resolved.policy, source: resolved.source, tier: resolved.tier };
	if (resolved.reason) v.reason = N(resolved.reason);
	if (resolved.policy === "allow") return v;
	if (resolved.policy === "deny") {
		v.text = N(A.denyError(resolved, tool.name).message);
		return v;
	}
	// wrapper.ts:402-412
	const grantable = grants !== undefined && resolved.source === "mode";
	const cacheTargets = grantable ? (tool.approvalCacheTargets?.(args)?.length ?? 0) > 0 : false;
	const scope = !cacheTargets ? A.sessionApprovalScope(resolved, tool.name) : undefined;
	if (scope && st.session.has(scope.key)) return { decision: "allow", source: "session-grant", tier: resolved.tier };
	const interactive = c.ui === "interactive" && c.session !== "subagent"; // a subagent has no UI (task/executor.ts:1298-1300)
	if (!interactive) {
		v.text =
			resolved.source === "restriction" // wrapper.ts:476-490
				? `Tool "${tool.name}" requires approval but no interactive UI available.\nReason: ${N(resolved.reason ?? "")}\nA restricted folder (tools.restrictedFolders) asks in every approval mode.`
				: `Tool "${tool.name}" requires approval but no interactive UI available.\nOptions:\n  1. Set tools.approvalMode: yolo in /settings\n  2. Add tools.approval.${tool.name}: allow to config\n  3. Use an interactive UI to approve the tool call`;
		return v;
	}
	let title = "";
	let choices: string[] = [];
	await A.requestApproval(
		{
			select: async (t: string, ch: string[]) => {
				title = t;
				choices = ch;
				return undefined;
			},
		},
		tool,
		args,
		{ reason: resolved.reason, sessionSubject: scope?.subject, grants: cacheTargets },
	);
	v.promptTitle = N(title);
	v.choices = choices.map(N);
	if (answer === "session" && scope) {
		st.session.add(scope.key);
		v.answerLabel = `Approve ${scope.subject} for this session`;
	} else if ((answer === "target" || answer === "folder") && cacheTargets && grants) {
		const label =
			answer === "folder"
				? choices.find(ch => ch.startsWith("Approve Directory: "))
				: choices.find(ch => ch === "Approve File" || ch === "Approve");
		if (label) {
			A.recordApprovalGrant(tool, args, grants, resolved.tier, { grant: answer, always: false });
			v.answerLabel = N(label);
		}
	} else if (answer === "once") {
		v.answerLabel = choices.includes("Approve Once") ? "Approve Once" : "Approve";
	}
	return v;
}

const RANK: Record<string, number> = { allow: 1, prompt: 2, deny: 3 };
const same = (a: Verdict, b: Verdict) =>
	a.decision === b.decision &&
	a.text === b.text &&
	a.promptTitle === b.promptTitle &&
	JSON.stringify(a.choices) === JSON.stringify(b.choices);
const strip = (v: Verdict & { answerLabel?: string }): Verdict => {
	const { answerLabel: _drop, ...rest } = v;
	return rest;
};

/** omp's verdict for the call under the OPERATOR view alone (no project layer). */
async function operatorVerdict(env: Env, c: ApprovalCase, args: unknown): Promise<Verdict | undefined> {
	const view = env.operatorView;
	const tool = view?.tools[c.tool];
	if (!view || !tool || !view.settings) return undefined;
	const { approvalMode, userPolicies } = A.resolveApprovalFromContext({ settings: view.settings });
	return strip(await gate(view, tool, args, approvalMode, userPolicies, c, { grants: makeGrants(), session: new Set() }));
}

/**
 * The TWO-VIEW TARGET (integrator decision, README.md "The two views"): the call is resolved twice with omp's
 * resolver, under the operator view and under the session view (omp's own), and the STRICTER verdict is the
 * target: deny > prompt > allow, with the text of the stricter one. On a tie the session view's (omp's) verdict
 * stands, so the port equals omp unless the operator layers alone are strictly stricter.
 */
function stricter(operator: Verdict, session: Verdict): Verdict {
	return (RANK[operator.decision] ?? 0) > (RANK[session.decision] ?? 0) ? operator : session;
}

const cases = fs
	.readFileSync(process.argv[2]!, "utf8")
	.split("\n")
	.filter(Boolean)
	.map(l => JSON.parse(l) as ApprovalCase);
const out = fs.openSync(process.argv[3]!, "w");
const stats: Record<string, number> = {};
const bump = (k: string) => {
	stats[k] = (stats[k] ?? 0) + 1;
};

for (const c of cases) {
	const args: unknown = JSON.parse(c.args);
	const env = await envFor(c, true);
	const line: Record<string, unknown> = { ...c };
	if (env.error) {
		// Settings.init rejects (config/settings.ts:896-916, 2748-2757): omp moves the file aside and does not start.
		line.omp = { decision: "no-session", text: env.error.split(env.cwd).join("$CWD").replace(/: \/.*?config\.yml/, ": <agentDir>/config.yml") };
		// Integrator decision: the port cannot refuse to start, so it fails closed to what omp does without settings
		// (approval.ts:95-106: always-ask, no policies) and tells the user once per session.
		const plain = await envFor({ ...c, settings: { operator: {}, project: null, trusted: true } }, true);
		const tool = plain.tools[c.tool];
		if (tool) {
			const fc = A.resolveApprovalFromContext(undefined);
			line.expect = "fail-closed";
			line.expectPort = strip(await gate(plain, tool, args, fc.approvalMode, fc.userPolicies, c, { grants: makeGrants(), session: new Set() }));
		}
		bump("no-session");
	} else if (!env.tools[c.tool]) {
		line.omp = { decision: "no-such-tool" };
		line.compare = false;
		line.why = "omp registers no tool of this name in such a session";
		bump("no-such-tool");
	} else {
		const tool = env.tools[c.tool]!;
		const st: GateState = { grants: makeGrants(), session: new Set() };
		// wrapper.ts:286-289; without settings (the port's nil approval.Setting) omp is always-ask with no policies.
		const { approvalMode, userPolicies } = A.resolveApprovalFromContext(
			c.state?.nilSetting || !env.settings ? undefined : { settings: env.settings },
		);
		if (c.state?.prior) {
			const p = c.state.prior;
			const priorTool = env.tools[p.tool]!;
			const pv = await gate(env, priorTool, JSON.parse(p.args) as unknown, approvalMode, userPolicies, c, st, p.answer);
			line.state = { prior: { ...p, omp: strip(pv), ...(pv.answerLabel ? { choice: pv.answerLabel } : {}) } };
		}
		const ompVerdict = strip(await gate(env, tool, args, approvalMode, userPolicies, c, st));
		line.omp = ompVerdict;
		const argsObj = args && typeof args === "object" ? (args as Record<string, unknown>) : undefined;
		if (c.tool === "glob" && typeof argsObj?.pattern === "string" && argsObj.pattern.includes("://")) {
			line.compare = false;
			line.why =
				"glob has no `pattern` key at the tag: omp's hook still reads it (tools/glob.ts:56-73) and refuses the call in execute (glob.ts:241); the port's PrepareArguments refuses it before the gate";
		}
		if (c.tool === "edit") {
			line.compare = false;
			line.why = "omp reads edit targets through its native addon (crates/edit), which the oracle stubs: omp's tier here is the no-target fallback";
		}
		if (c.settings.project && !c.state) {
			const op = await operatorVerdict(env, c, args);
			if (op && c.settings.trusted) {
				const target = stricter(op, ompVerdict);
				if (!same(target, ompVerdict)) {
					line.expect = "port-stricter";
					line.expectPort = target;
					bump("port-stricter");
				}
			} else if (op) {
				// PiG loads a project layer only for a trusted project; omp has no such gate. The operator view alone decides.
				if (!same(op, ompVerdict)) {
					line.expect = "port-ignores-untrusted-project";
					line.expectPort = op;
					bump("port-ignores-untrusted-project");
				}
			}
		}
		bump(ompVerdict.decision);
	}
	fs.writeSync(out, JSON.stringify(line) + "\n");
}
fs.closeSync(out);
fs.writeFileSync(
	path.join(path.dirname(process.argv[3]!), "meta.json"),
	JSON.stringify({ omp: "v19.1.1 (31ffee513f)", cases: cases.length, mounted: MOUNTED, active: ACTIVE, ompDecisions: stats }, null, 1) + "\n",
);
console.error("cases", cases.length, JSON.stringify(stats));
fs.rmSync(root, { recursive: true, force: true });
process.exit(0);
