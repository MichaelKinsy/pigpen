import * as fs from "node:fs";
import * as path from "node:path";

const ompSrc = process.env.OMP_SRC;
if (!ompSrc) {
	console.error("OMP_SRC is not set; set OMP_SRC to omp tag v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a)");
	process.exit(1);
}
// Dynamic imports: module paths are runtime-selected from $OMP_SRC.
const prompt = await import(path.join(ompSrc, "packages/utils/src/prompt.ts"));
const { quotePosixArgument } = await import(path.join(ompSrc, "packages/coding-agent/src/utils/shell-quote.ts"));
const base = path.join(ompSrc, "packages/coding-agent/src/prompts/system") + path.sep;
const sysT = fs.readFileSync(base + "system-prompt.md", "utf8");
const projT = fs.readFileSync(base + "project-prompt.md", "utf8");
const repoT = fs.readFileSync(base + "active-repo-context.md", "utf8");
const pers = fs.readFileSync(base + "personalities/default.md", "utf8").trim();

export function renderSpec(s: any, mounted: any[] = [], toolCommandDocs = ""): string[] {
	const toolNames: string[] = s.tools ?? [];
	const hasRead = toolNames.includes("read");
	const skillsAll = s.skills ?? [];
	const filteredSkills = hasRead ? skillsAll.filter((k: any) => !k.hide) : [];
	const mountedNames = new Set(mounted.map(m => m.name));
	const inv = toolNames;
	const toolRefs: any = {};
	for (const n of toolNames) toolRefs[n] = n;
	for (const m of mounted) toolRefs[m.name] ??= m.name;
	const preludes = s.preludes ?? [];
	const data: any = {
		appendPrompt: s.append ?? "",
		tools: [...new Set([...toolNames, ...mounted.map(m => m.name)])],
		toolInfo: inv.map(name => ({ name, internalName: name, label: "" })),
		toolInventory: "",
		toolListMode: true,
		toolRefs,
		environment: [{ label: "OS", value: "darwin 25.5.0" }, { label: "Arch", value: "arm64" }],
		contextFiles: (s.contextFiles ?? []).map((f: any) => ({ ...f, content: prompt.closeCodeFence(f.content) })),
		agentsMdSearch: { files: [] },
		workspaceTree: { rendered: "" },
		hasSkillUriAccess: hasRead && skillsAll.length > 0,
		internalUrls: s.urls ?? [],
		internalUrlReference: s.urlRef ?? [],
		skills: filteredSkills,
		rules: s.rules ?? [],
		alwaysApplyRules: s.alwaysApply ?? [],
		additionalWorkspaceRoots: [],
		model: s.model ?? "",
		delegationTier: s.eager && s.bias !== "gated" ? "eager" : s.bias,
		personality: s.personality === "none" ? "" : pers,
		intentTracing: !!s.intentField,
		intentField: s.intentField ?? "",
		intentGuidance: s.intentGuidance ?? "",
		eagerTasks: s.eager, eagerTasksAlways: s.eagerAlways, taskBatch: s.taskBatch, scoutAvailable: s.scout,
		taskIrcEnabled: s.irc, secretsEnabled: s.secrets, resultShaping: s.shaping ?? "",
		browserEnabled: preludes.some((p: any) => p.name === "browser"),
		computerEnabled: preludes.some((p: any) => p.name === "computer"),
		includeWorkspaceTree: false,
		renderMermaid: s.mermaid, renderSvg: s.svg, renderEmbeds: s.embeds, autoGraph: s.autoGraph, reactions: s.reactions,
		encourageEdits: s.encourageEdits, mountedCommands: mounted, toolCommandDocs, autoQaEnabled: s.autoQa,
		ovnfs: s.ovnfs ? quotePosixArgument(s.ovnfs) : "",
		subagent: s.subagent,
	};
	const blocks = [prompt.render(sysT, data)];
	for (const p of preludes) { const g = (p.guidance ?? "").trim(); if (g) blocks.push(g); }
	const arc = s.activeRepo ? prompt.render(repoT, { relativeRepoRoot: s.activeRepo }).trim() : "";
	const proj = prompt.render(projT, { ...data, activeRepoContext: arc }).trim();
	if (proj) blocks.push(proj);
	return blocks;
}

if (import.meta.main) {
	const specs = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
	let bad = 0; const kinds = new Map<string, number>(); const examples = new Map<string, any>();
	specs.forEach((s: any, idx: number) => {
		const want = renderSpec(s).join("\n\n").replace("You are omp's trusted", "You are oh-my-pig's trusted");
		let got: string = s.output;
		if (s.ovnfs) got = got; 
		if (want !== got) {
			bad++;
			const wl = want.split("\n"), gl = got.split("\n");
			// classify by first differing line pair
			let i = 0; while (i < wl.length && i < gl.length && wl[i] === gl[i]) i++;
			const key = `omp:${JSON.stringify((wl[i] ?? "<EOF>").slice(0, 70))} | go:${JSON.stringify((gl[i] ?? "<EOF>").slice(0, 70))} | prev:${JSON.stringify((wl[i - 1] ?? "").slice(0, 50))}`;
			kinds.set(key, (kinds.get(key) ?? 0) + 1);
			if (!examples.has(key)) examples.set(key, idx);
		}
	});
	console.log(`specs=${specs.length} mismatched=${bad}`);
	for (const [k, v] of [...kinds].sort((a, b) => b[1] - a[1])) console.log(v, "ex#" + examples.get(k), k);
}
