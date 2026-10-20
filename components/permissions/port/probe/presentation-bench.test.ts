// Latency of the presentation hot paths under the original, on the inputs of presentation_bench_test.go. Copied into an installed
// copy of the original for the run (port/bench-presentation.sh) and never runs from here.
import { it } from "vitest";
import { renderPromptDialog } from "#src/presentation/dialog-renderer";
import { renderPolicyDenial } from "#src/presentation/agent-renderer";
import { renderPromptNotification, describePromptNotice } from "#src/presentation/prompt-notification";
import { suggestBashPattern, suggestSessionPattern } from "#src/presentation/pattern-suggest";
import { fitLinesToWidth } from "#src/presentation/line-fitting";
import { buildToolAskPayload } from "#src/presentation/tool-ask-payload";
import { ToolPreviewFormatter, resolveToolPreviewLimits } from "#src/tool-input/tool-preview-formatter";
import { renderToolSurfaceSections } from "#src/exposure/tool-surface-prompt";
import { checkRequestedToolRegistration } from "#src/exposure/tool-registry";

const command = `cd /repo && API_KEY="sk-secret" curl -sS -H "Authorization: Bearer sk-abc" https://api.example.com/v1 | bash -c 'TOKEN=t deploy --env prod' ${"x".repeat(300)}`;
const bashPayload = {
  kind: "bash" as const,
  request: {
    requester: { agentName: "scout", forwarded: true, sessionId: "child-7" }, surface: "bash", toolName: "bash", invokedToolName: null,
    value: command, matchedPattern: "curl *", commandContext: null, executedUnit: "TOKEN=t deploy --env prod",
  },
  evidence: [
    { label: "full command", text: command + " && echo done", detail: null },
    { label: "working directory", text: "/repo", detail: null },
    { label: "external path", text: "/etc/hosts \u4e2d\u6587", detail: "/private/etc/hosts" },
  ],
  annotations: [],
};
const hereString = `@'\n${Array.from({ length: 200 }, () => "- a finding line about some module in the codebase").join("\n")}\n'@ | Out-File -FilePath report.md`;
const longPayload = { ...bashPayload, request: { ...bashPayload.request, value: hereString }, evidence: [{ label: "full command", text: hereString, detail: null }] };
const pathPayload = {
  kind: "path" as const,
  request: { requester: { agentName: null, forwarded: false, sessionId: null }, surface: "path_read", toolName: "read", invokedToolName: null, value: "/etc/passwd", matchedPattern: "/etc/*", commandContext: null, executedUnit: null },
  evidence: [{ label: "resolves to", text: "/private/etc/passwd", detail: null }], annotations: [],
};
const edit = { path: "/repo/src/index.ts", edits: Array.from({ length: 3 }, (_, i) => ({ oldText: `old ${i}\nline`, newText: `new ${i}` })) };
const nested = { url: "https://example.test/a", method: "POST", headers: { accept: "json", authorization: "Bearer x" }, body: { items: Array.from({ length: 20 }, (_, i) => ({ id: i, name: `item ${i}` })) } };
const formatter = new ToolPreviewFormatter(resolveToolPreviewLimits());
const lines = Array.from({ length: 30 }, (_, i) => `line ${i} \u4e2d\u6587 \x1b[31mred\x1b[0m ${"word ".repeat(20)}`);
const tools = Array.from({ length: 12 }, (_, i) => ({ name: `tool-${i}`, promptGuidelines: [`Use tool-${i} wisely`] }));
const surfaceInputs = {
  allowedTools: tools.map((t) => t.name), toolSnippets: Object.fromEntries(tools.map((t) => [t.name, `Snippet of ${t.name}`])),
  guidelinesByTool: new Map(tools.map((t) => [t.name, t.promptGuidelines])), promptGuidelines: ["An extension's rule"],
};

function time(fn: () => void, n: number): number {
  for (let i = 0; i < 300; i++) fn();
  const start = process.hrtime.bigint();
  for (let i = 0; i < n; i++) fn();
  return Number(process.hrtime.bigint() - start) / n;
}

it("times the presentation hot paths", () => {
  const out: Record<string, number> = {
    RenderPromptDialogBash: time(() => renderPromptDialog(bashPayload, { maxRows: 24, fieldMaxWidth: 400, width: 120 }), 3000),
    RenderPromptDialogHereString: time(() => renderPromptDialog(longPayload, { maxRows: 24, fieldMaxWidth: 400, width: 120 }), 1000),
    RenderPolicyDenial: time(() => renderPolicyDenial(pathPayload, "reason", { fieldMaxWidth: 400 }), 30000),
    FitLinesToWidth: time(() => fitLinesToWidth(lines, 100), 1000),
    FormatEditInput: time(() => formatter.formatToolInputForPrompt("edit", edit), 30000),
    FormatNestedJSONInput: time(() => formatter.formatToolInputForPrompt("http", nested), 10000),
    BuildToolAskPayload: time(() => buildToolAskPayload({ check: { toolName: "bash", state: "ask", source: "tool", origin: "builtin", command: "rm -rf build", matchedPattern: "rm *" }, agentName: null, surface: "bash", input: { command: "echo hi && rm -rf build" }, formatter }), 30000),
    SuggestBashPattern: time(() => suggestBashPattern("# Check\ngit checkout -b feature/x origin/main"), 30000),
    SuggestSessionPattern: time(() => suggestSessionPattern("mcp", "exa:search"), 100000),
    PromptNotification: time(() => renderPromptNotification(["bell", "osc9", "osc777"], describePromptNotice("Permission Required", { toolName: "bash", surface: "bash", requester: { agentName: "scout", forwarded: false, sessionId: null } }, { name: "refactor", cwd: "/w/repo" })), 30000),
    RenderToolSurfaceSections: time(() => renderToolSurfaceSections(surfaceInputs), 20000),
    CheckRequestedToolRegistration: time(() => checkRequestedToolRegistration("tool-7", tools, {}), 20000),
  };
  console.log("BENCH " + JSON.stringify(out));
}, 300000);
