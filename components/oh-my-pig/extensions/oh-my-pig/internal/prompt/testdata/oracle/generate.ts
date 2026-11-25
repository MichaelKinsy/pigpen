import * as fs from "node:fs";
import * as path from "node:path";
import { renderSpec } from "./oracle.ts";

const dir = import.meta.dir;
const testdataDir = path.resolve(dir, "..");

function toOhMyPig(blocks: string[]): string {
	return blocks.join("\n\n").replace("You are omp's trusted", "You are oh-my-pig's trusted");
}

// 1. Golden: wave 1's 10 tools, no URL handlers, no preludes.
const wave1Spec = {
	tools: ["read", "edit", "write", "grep", "glob", "bash", "todo", "ask", "web_search", "fetch"],
	contextFiles: [{ path: "/work/repo/AGENTS.md", content: "Always run go test before committing." }],
	skills: [{ name: "deploy", description: "Deploy services safely to staging." }],
	model: "anthropic/claude-opus-4",
	personality: "default",
	urls: [],
	urlRef: [],
	preludes: [],
	bias: "balanced",
	eager: false,
	eagerAlways: false,
	taskBatch: true,
	scout: true,
	irc: false,
	secrets: false,
	mermaid: false,
	svg: false,
	embeds: false,
	autoGraph: false,
	reactions: false,
	encourageEdits: true,
	autoQa: false,
	subagent: false,
};
fs.writeFileSync(path.join(testdataDir, "golden_prompt.txt"), toOhMyPig(renderSpec(wave1Spec)) + "\n");

// 2. Golden: all default tools with a fake URL handler and a fake browser prelude.
const fullSpec = {
	tools: ["read", "bash", "edit", "ask", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write"],
	contextFiles: [{ path: "/work/repo/AGENTS.md", content: "Always run go test before committing." }],
	skills: [{ name: "deploy", description: "Deploy services safely to staging." }],
	model: "anthropic/claude-opus-4",
	personality: "default",
	urls: ["fake://<name>: fake test resource."],
	urlRef: [],
	preludes: [{ name: "browser", guidance: "# Browser sessions\n- Fake prelude guidance line." }],
	bias: "balanced",
	eager: false,
	eagerAlways: false,
	taskBatch: true,
	scout: true,
	irc: false,
	secrets: false,
	mermaid: false,
	svg: false,
	embeds: false,
	autoGraph: false,
	reactions: false,
	encourageEdits: true,
	autoQa: false,
	subagent: false,
};
fs.writeFileSync(path.join(testdataDir, "golden_prompt_full.txt"), toOhMyPig(renderSpec(fullSpec)) + "\n");

// 3. Golden: default omp v19.1.1 session with tools.commands=true (catalog mode, real omp summaries).
const toolCmdMounted = [
	{ name: "ast_edit", summary: "Perform AST-aware code edits (SHOULD be preferred for codemods)" },
	{ name: "debug", summary: "Debug a running process with DAP (debugger adapter protocol)" },
	{ name: "lsp", summary: "Query LSP (language server) for diagnostics, hover info, and references" },
];
const toolCmdDocs = [
	"## More commands (help on demand)",
	"- `ast_edit` — Perform AST-aware code edits (SHOULD be preferred for codemods)",
	"- `debug` — Debug a running process with DAP (debugger adapter protocol)",
	"- `lsp` — Query LSP (language server) for diagnostics, hover info, and references",
	"",
	"Run `<tool> --help` before first use.",
].join("\n");
const toolCmdSpec = {
	tools: ["read", "bash", "edit", "ask", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write"],
	contextFiles: [{ path: "/work/repo/AGENTS.md", content: "Always run go test before committing." }],
	skills: [{ name: "deploy", description: "Deploy services safely to staging." }],
	model: "anthropic/claude-opus-4",
	personality: "default",
	urls: [],
	urlRef: [],
	preludes: [],
	bias: "balanced",
	eager: false,
	eagerAlways: false,
	taskBatch: true,
	scout: true,
	irc: false,
	secrets: false,
	mermaid: false,
	svg: false,
	embeds: false,
	autoGraph: false,
	reactions: false,
	encourageEdits: true,
	autoQa: false,
	subagent: false,
};
fs.writeFileSync(
	path.join(testdataDir, "golden_prompt_toolcmd.txt"),
	toOhMyPig(renderSpec(toolCmdSpec, toolCmdMounted, toolCmdDocs)) + "\n",
);

// 4. Matrix of 48 branch-covering cases for stored oracle parity fixtures.
const base = {
	tools: ["read", "bash", "edit", "write"],
	contextFiles: [],
	skills: [],
	rules: [],
	alwaysApply: [],
	model: "",
	personality: "default",
	urls: [],
	urlRef: [],
	preludes: [],
	bias: "balanced",
	eager: false,
	eagerAlways: false,
	taskBatch: true,
	scout: true,
	irc: false,
	secrets: false,
	shaping: "",
	intentField: "",
	intentGuidance: "",
	mermaid: false,
	svg: false,
	embeds: false,
	autoGraph: false,
	reactions: false,
	encourageEdits: true,
	autoQa: false,
	ovnfs: "",
	subagent: false,
	activeRepo: "",
	append: "",
	mounted: [] as string[],
	toolCommandDocs: "",
};

const cases: Array<{ name: string; spec: typeof base }> = [
	{ name: "minimal-default", spec: { ...base } },
	{ name: "empty-tools", spec: { ...base, tools: [] } },
	{ name: "personality-none", spec: { ...base, personality: "none" } },
	{ name: "with-model", spec: { ...base, model: "anthropic/claude-opus-4" } },
	{ name: "visuals-mermaid-only", spec: { ...base, mermaid: true } },
	{ name: "visuals-svg-only", spec: { ...base, svg: true } },
	{ name: "visuals-svg-and-mermaid", spec: { ...base, mermaid: true, svg: true } },
	{ name: "visuals-svg-and-autograph", spec: { ...base, svg: true, autoGraph: true } },
	{ name: "visuals-all", spec: { ...base, mermaid: true, svg: true, embeds: true, autoGraph: true, reactions: true } },
	{ name: "reactions-only", spec: { ...base, reactions: true } },
	{ name: "skills-only", spec: { ...base, skills: [{ name: "s1", description: "Skill one." }] } },
	{ name: "skills-hidden-filtered", spec: { ...base, skills: [{ name: "s1", description: "Visible." }, { name: "h1", description: "Hidden.", hide: true }] } },
	{ name: "skills-without-read", spec: { ...base, tools: ["bash"], skills: [{ name: "s1", description: "No read." }] } },
	{ name: "always-apply-only", spec: { ...base, alwaysApply: [{ name: "r1", content: "Always follow rule 1." }] } },
	{ name: "domain-rules-only", spec: { ...base, rules: [{ name: "go-style", globs: ["*.go", "cmd/**/*.go"], description: "Go style rules." }] } },
	{
		name: "skills-generic-and-domain-rules",
		spec: {
			...base,
			skills: [{ name: "s1", description: "Skill one." }],
			alwaysApply: [{ name: "r1", content: "Generic rule." }],
			rules: [{ name: "ts", globs: ["*.ts"], description: "TS rules." }],
		},
	},
	{ name: "urls-with-read", spec: { ...base, urls: ["skill://<name>: skill docs.", "local://<name>.md: shared artifact."] } },
	{ name: "urls-without-read-with-refs", spec: { ...base, tools: ["bash"], urls: ["proc://: jobs."], urlRef: ["proc://<id>: status", "local://<N>.log: spill"] } },
	{ name: "urls-empty-without-read-with-refs", spec: { ...base, tools: ["bash"], urls: [], urlRef: ["proc://<id>: status"] } },
	{ name: "think-scratchpad", spec: { ...base, tools: ["read", "think"] } },
	{ name: "tool-commands-catalog", spec: { ...base, mounted: ["lsp", "debug"], toolCommandDocs: toolCmdDocs } },
	{ name: "tool-commands-empty-docs", spec: { ...base, mounted: ["lsp"], toolCommandDocs: "" } },
	{ name: "intent-default-guidance", spec: { ...base, intentField: "i" } },
	{ name: "intent-custom-guidance", spec: { ...base, intentField: "i", intentGuidance: "short verb phrase." } },
	{ name: "secrets-without-intent", spec: { ...base, secrets: true } },
	{ name: "intent-secrets-and-shaping", spec: { ...base, intentField: "i", secrets: true, shaping: "- Shape output concisely." } },
	{ name: "specialized-no-read-with-write", spec: { ...base, tools: ["write"] } },
	{ name: "specialized-read-lsp-no-write", spec: { ...base, tools: ["read", "lsp"] } },
	{ name: "specialized-find-and-lsp", spec: { ...base, tools: ["read", "write", "lsp", "find"] } },
	{ name: "edit-without-bash-or-eval", spec: { ...base, tools: ["read", "edit"] } },
	{ name: "edit-with-eval-no-bash", spec: { ...base, tools: ["read", "edit", "eval"] } },
	{ name: "edit-encourage-false", spec: { ...base, encourageEdits: false } },
	{ name: "autoqa-with-edit", spec: { ...base, autoQa: true } },
	{ name: "autoqa-without-edit", spec: { ...base, tools: ["read", "bash"], autoQa: true } },
	{ name: "ovnfs-plain", spec: { ...base, ovnfs: "ovnfs" } },
	{ name: "ovnfs-with-quote", spec: { ...base, ovnfs: "/opt/my's/ovnfs" } },
	{ name: "ovnfs-without-bash", spec: { ...base, tools: ["read"], ovnfs: "ovnfs" } },
	{ name: "delegation-balanced-scout", spec: { ...base, tools: ["read", "task"], bias: "balanced", scout: true } },
	{ name: "delegation-balanced-no-scout", spec: { ...base, tools: ["read", "task"], bias: "balanced", scout: false, taskBatch: false } },
	{ name: "delegation-restrained-find-wait", spec: { ...base, tools: ["read", "task", "find", "wait"], bias: "restrained" } },
	{ name: "delegation-restrained-minimal", spec: { ...base, tools: ["read", "task"], bias: "restrained" } },
	{ name: "delegation-eager-with-irc", spec: { ...base, tools: ["read", "task"], bias: "eager", eager: true, irc: true } },
	{ name: "delegation-eager-always", spec: { ...base, tools: ["read", "task"], bias: "balanced", eager: true, eagerAlways: true } },
	{ name: "delegation-gated-off", spec: { ...base, tools: ["read", "task"], bias: "gated", eager: false } },
	{ name: "delegation-gated-on", spec: { ...base, tools: ["read", "task"], bias: "gated", eager: true } },
	{ name: "workflow-todo-ask-lsp", spec: { ...base, tools: ["read", "edit", "todo", "ask", "lsp"] } },
	{ name: "workflow-subagent", spec: { ...base, tools: ["read", "edit", "bash"], subagent: true, personality: "none" } },
	{
		name: "preludes-browser-and-computer",
		spec: {
			...base,
			preludes: [
				{ name: "browser", guidance: "# Browser\n- Line 1\n\n\n- Keep raw blank lines." },
				{ name: "computer", guidance: "# Computer\n- Desktop guidance." },
			],
		},
	},
	{
		name: "context-unclosed-fence-and-active-repo-and-append",
		spec: {
			...base,
			contextFiles: [{ path: "/work/AGENTS.md", content: "Rules:\n```bash\necho hi" }],
			activeRepo: "subrepo",
			append: "# Extra\nCustom append text.",
		},
	},
];

const fixtures = cases.map(({ name, spec }) => {
	const mountedObjs = spec.mounted.map(m => ({ name: m, summary: "" }));
	const expected = toOhMyPig(renderSpec(spec, mountedObjs, spec.toolCommandDocs));
	return { name, spec, expected };
});

fs.writeFileSync(path.join(dir, "fixtures.json"), JSON.stringify(fixtures, null, 2) + "\n");
console.log(`Generated 3 goldens and ${fixtures.length} oracle fixtures.`);
