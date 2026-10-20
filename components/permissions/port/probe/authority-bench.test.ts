// Authority benchmark: the original's cost of the paths the approval dialog runs on a keystroke or a render, for comparison with the Go
// benchmarks in auth_bench_test.go (same operations, same inputs). Copied into an installed copy of the original by
// port/bench-authority.sh and never run from here. Writes {op: {nsPerOp, bytesPerOp}} to BENCH_OUT.
import { writeFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { setFlagsFromString } from "node:v8";
import { it, vi } from "vitest";
import { AskDialogQueue } from "#src/authority/ask-dialog-queue";
import { composeAuthorizerChain } from "#src/authority/authorizer-chain";
import { collapsePastedNewlines } from "#src/authority/bracketed-paste";
import { LocalUserAuthorizer } from "#src/authority/local-user-authorizer";
import { presentInlinePermissionPrompt } from "#src/authority/permission-prompt-component";
import { initialPromptState, reducePrompt } from "#src/authority/permission-prompt-decision";
import { PermissionPrompter } from "#src/authority/permission-prompter";
import { DEFAULT_DIALOG_KEYS } from "#src/config/dialog-keys";
import { DEFAULT_RENDER_BUDGET } from "#src/presentation/dialog-renderer";
import { makePromptDetails, makePromptPayload } from "#test/helpers/prompt-details-fixtures";

setFlagsFromString("--expose-gc");
const gc = runInNewContext("gc") as () => void;

async function measure(iterations: number, body: () => unknown): Promise<{ nsPerOp: number; bytesPerOp: number }> {
  for (let i = 0; i < 50; i++) await body(); // warm up
  gc();
  let total = 0n;
  let bytes = 0;
  for (let i = 0; i < iterations; i++) {
    const heapBefore = process.memoryUsage().heapUsed;
    const t = process.hrtime.bigint();
    await body();
    total += process.hrtime.bigint() - t;
    bytes += Math.max(0, process.memoryUsage().heapUsed - heapBefore);
  }
  return { nsPerOp: Number(total) / iterations, bytesPerOp: bytes / iterations };
}

const plainTheme = { fg: (_c: string, t: string) => t, bg: (_c: string, t: string) => t };

function ask(value: string) {
  return makePromptPayload({
    kind: "path",
    request: { ...makePromptPayload().request, surface: "path", toolName: "read", value, matchedPattern: null },
  });
}

function mount(payload: ReturnType<typeof makePromptPayload>) {
  let component: { render(w: number): string[]; handleInput(d: string): void } | undefined;
  let decided: Promise<unknown> | undefined;
  const ui = {
    select: vi.fn(), input: vi.fn(), getToolsExpanded: () => false, setToolsExpanded: () => undefined,
    custom: (factory: any) => {
      decided = new Promise((resolve) => {
        component = factory({ requestRender: () => undefined, terminal: { write: () => undefined } }, plainTheme, { matches: () => false }, resolve);
      });
      return decided;
    },
  };
  const view = { mode: "tui" as const, ui: ui as any, doublePressToConfirm: true, budget: DEFAULT_RENDER_BUDGET, dialogKeys: DEFAULT_DIALOG_KEYS, promptNotifications: [], notice: { title: "pi", body: "b" } };
  const settled = presentInlinePermissionPrompt(view, "Permission Required", payload);
  return { component: component!, settled };
}

it("measures the approval dialog paths", { timeout: 300_000 }, async () => {
  const output = process.env.BENCH_OUT;
  if (!output) throw new Error("BENCH_OUT is required");
  const out: Record<string, unknown> = {};

  const decision = mount(ask("/repo/src/some/deeply/nested/file.ts"));
  out.ComponentRenderDecision = await measure(2000, () => decision.component.render(80));

  const body = Array.from({ length: 200 }, () => "- a finding line about some module in the codebase").join("\n");
  const command = `@'\n${body}\n'@ | Out-File -FilePath report.md`;
  const forwarded = mount(
    makePromptPayload({
      kind: "forwarded",
      request: { ...makePromptPayload().request, requester: { agentName: "scout", forwarded: true, sessionId: "abc123" }, surface: "bash", toolName: null, value: command, matchedPattern: null },
      evidence: [{ label: "requested", text: command, detail: null }],
    }),
  );
  out.ComponentRenderForwardedBounded = await measure(2000, () => forwarded.component.render(120));

  out.ComponentSession = await measure(2000, async () => {
    const m = mount(ask("/repo/src/file.ts"));
    m.component.render(80);
    m.component.handleInput("y");
    m.component.render(80);
    m.component.handleInput("y");
    await m.settled;
  });

  const typing = mount(ask("/repo/src/file.ts"));
  typing.component.handleInput("r");
  typing.component.handleInput("r");
  out.ReasonTyping = await measure(2000, () => {
    typing.component.handleInput("a");
    typing.component.render(80);
  });

  const config = { doublePressToConfirm: true, sessionLabel: "Yes, for this session", keys: DEFAULT_DIALOG_KEYS } as any;
  let state = initialPromptState(config);
  out.ReducePrompt = await measure(20000, () => {
    const o = reducePrompt(config, state, { type: "nav", direction: "down" });
    if (o.kind === "render") state = o.state;
  });

  const data = `\u001b[200~${"a line of pasted text\r\n".repeat(90)}\u001b[201~`;
  out.CollapsePastedNewlines = await measure(20000, () => collapsePastedNewlines(data));

  const terminal = { authorize: () => Promise.resolve({ approved: true, state: "approved", decidedBy: { kind: "user", via: "dialog" } } as any) };
  const links = ["a", "b", "c"].map((name, i) => ({ name, authorize: () => Promise.resolve(i < 2 ? { kind: "defer" as const } : { kind: "deny" as const, reason: "no" }) }));
  const details = makePromptDetails();
  out.ComposeChainThreeLinks = await measure(20000, () => composeAuthorizerChain(links, terminal, {} as any, {} as any).authorize(details));

  const prompter = new PermissionPrompter({ logger: { review: () => undefined } as any });
  const promptDetails = makePromptDetails({ requestId: "req-123", agentName: "test-agent", toolName: "read" });
  out.PrompterPrompt = await measure(20000, () => prompter.prompt(terminal as any, promptDetails));

  const local = new LocalUserAuthorizer({
    ui: {} as any, mode: "tui", events: { emit: () => undefined, on: () => () => undefined } as any, dialogs: new AskDialogQueue(),
    getPromptPreferences: () => ({ doublePressToConfirm: true, budget: DEFAULT_RENDER_BUDGET, dialogKeys: DEFAULT_DIALOG_KEYS, promptNotifications: [] }),
    requestPermissionDecision: () => Promise.resolve({ approved: true, state: "approved", decidedBy: { kind: "user", via: "dialog" } } as any),
    describeSession: () => ({ name: "refactor-auth", cwd: "/w/repo" }),
  });
  const localDetails = makePromptDetails({ requestId: "req-123", agentName: "test-agent", toolName: "read" });
  out.LocalAuthorizerAsk = await measure(20000, () => local.authorize(localDetails));

  writeFileSync(output, JSON.stringify(out));
});
