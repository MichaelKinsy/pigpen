// Authority probe: runs port/probe/authority-cases.json through the original's own src/authority modules and writes what they do, for
// the Go oracle test (extensions/pi-permission-system/auth_oracle_test.go reads testdata/authority_oracle.json). It is copied into an
// installed copy of the original by port/record-authority-probe.sh and never runs from here; the port itself has no Node.
import { readFileSync, writeFileSync } from "node:fs";
import { it, vi } from "vitest";
import { AskDialogQueue } from "#src/authority/ask-dialog-queue";
import { selectAuthorizer } from "#src/authority/authorizer";
import { AuthorizerRegistry } from "#src/authority/authorizer-registry";
import { composeAuthorizerChain } from "#src/authority/authorizer-chain";
import { collapsePastedNewlines } from "#src/authority/bracketed-paste";
import { LocalUserAuthorizer } from "#src/authority/local-user-authorizer";
import { requestPermissionDecisionFromUi } from "#src/authority/permission-dialog";
import {
  presentInlinePermissionPrompt,
  requestPermissionDecision,
} from "#src/authority/permission-prompt-component";
import {
  initialPromptState,
  reducePrompt,
} from "#src/authority/permission-prompt-decision";
import { PermissionPrompter } from "#src/authority/permission-prompter";
import { SUBAGENT_ENV_HINT_KEYS } from "#src/authority/permission-forwarding";
import { buildUiPrompt } from "#src/service/permission-ui-prompt";
import { makeSubagentRegistry } from "#test/helpers/forwarding-fixtures";
import { DEFAULT_RENDER_BUDGET } from "#src/presentation/dialog-renderer";

type Json = unknown;

/** What JSON.stringify would write: undefined members dropped. */
function plain(value: Json): Json {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

const KEYSETS: Record<string, Record<string, string>> = {
  default: { approve: "y", approveSession: "s", approveSessionBoth: "b", deny: "n", denyWithReason: "r" },
  digits: { approve: "1", approveSession: "2", approveSessionBoth: "3", deny: "4", denyWithReason: "5" },
  swapped: { approve: "n", approveSession: "s", approveSessionBoth: "b", deny: "y", denyWithReason: "r" },
};

/** The five fields a decision model states, as one comparable string. */
function decisionKey(d: {
  approved: boolean;
  state: string;
  denialReason?: string;
  sessionGrantWidth?: string;
  confirmationUnavailable?: boolean;
}): string {
  return JSON.stringify([d.approved, d.state, d.denialReason ?? null, d.sessionGrantWidth ?? null, d.confirmationUnavailable ?? null]);
}

function stateKey(s: ReturnType<typeof initialPromptState>): string {
  return ["R", s.step, s.highlightedAction, s.armedAction ?? "", s.hint, s.reasonError ?? "", s.scopeServing ? 1 : 0, s.grantWidth].join("|");
}

const tagTheme = {
  fg: (color: string, text: string) => `<${color}>${text}</>`,
  bg: (_color: string, text: string) => text,
};

it("records the original's authority behaviour for the authority cases", { timeout: 600_000 }, async () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  const cases = JSON.parse(readFileSync(input, "utf8"));
  const out: Record<string, Json> = {};

  // ── the pure decision model ──
  out.model = (cases.model as any[]).map((c) => {
    const config = {
      doublePressToConfirm: c.config.double,
      sessionLabel: "Yes, for this session",
      keys: KEYSETS[c.config.keys],
      ...(c.config.widthLabel ? { widthLabel: c.config.widthLabel } : {}),
      ...(c.config.scope ? { sessionScope: c.config.scope } : {}),
    } as any;
    let state = initialPromptState(config);
    const steps: string[] = [];
    for (const event of c.events) {
      const outcome = reducePrompt(config, state, event);
      if (outcome.kind === "decision") {
        steps.push(`D|${decisionKey(outcome.decision)}`);
        break;
      }
      state = outcome.state;
      steps.push(stateKey(state));
    }
    return steps;
  });

  // ── the select/input fallback ──
  out.dialog = [];
  for (const c of cases.dialog as any[]) {
    const selects = [...c.selects];
    const calls: Json[] = [];
    const inputs: Json[] = [];
    const ui = {
      select: vi.fn((title: string, options: string[]) => {
        calls.push({ title, options });
        const next = selects.shift();
        return Promise.resolve(next === null || next === undefined ? undefined : next);
      }),
      input: vi.fn((title: string, placeholder?: string) => {
        inputs.push({ title, placeholder });
        return Promise.resolve(c.input === null ? undefined : c.input);
      }),
    };
    const decision = await requestPermissionDecisionFromUi(ui as any, "Title", "Message", c.options ?? undefined);
    out.dialog.push({ decision: decisionKey(decision), selects: calls, inputs });
  }

  // ── the chain ──
  out.chain = [];
  for (const c of cases.chain as any[]) {
    const consulted: string[] = [];
    const links = c.links.map((l: any) => ({
      name: l.name,
      authorize: vi.fn(() => {
        consulted.push(l.name);
        if (l.verdict.throw !== undefined) {
          return Promise.reject(new Error(l.verdict.throw));
        }
        return Promise.resolve(l.verdict.undefined ? undefined : l.verdict);
      }),
    }));
    const terminalCalls: number[] = [];
    const terminal = { authorize: vi.fn(() => { terminalCalls.push(1); return Promise.resolve(c.terminal); }) };
    const composed = composeAuthorizerChain(links, terminal as any, {} as any, {} as any);
    let decision: unknown = null;
    let rejected = false;
    try {
      decision = await composed.authorize({} as any);
    } catch {
      rejected = true;
    }
    out.chain.push({ decision: plain(decision), rejected, consulted, terminalCalls: terminalCalls.length, identity: composed === (terminal as any) });
  }

  // ── the registry's disposer ──
  out.registry = [];
  for (const c of cases.registry as any[]) {
    const fns: Record<string, any> = { a: () => Promise.resolve({ kind: "defer" }), b: () => Promise.resolve({ kind: "deny" }) };
    const registry = new AuthorizerRegistry();
    const disposeFirst = registry.register("link", fns[c.first]);
    disposeFirst();
    registry.register("link", fns[c.second]);
    disposeFirst();
    out.registry.push({ stillRegistered: registry.get("link") !== undefined });
  }

  // ── bracketed paste ──
  out.paste = (cases.paste as string[]).map((s) => collapsePastedNewlines(s));

  // ── LocalUserAuthorizer: ui_prompt, request options and notice ──
  out.local = [];
  for (const c of cases.local as any[]) {
    const emitted: Json[] = [];
    const calls: Json[] = [];
    const deps = {
      ui: {} as any,
      mode: "tui" as const,
      events: { emit: (channel: string, data: unknown) => { emitted.push({ channel, data: plain(data) }); }, on: () => () => undefined },
      dialogs: new AskDialogQueue(),
      getPromptPreferences: () => ({ doublePressToConfirm: true, budget: DEFAULT_RENDER_BUDGET, dialogKeys: KEYSETS.default as any, promptNotifications: [] }),
      requestPermissionDecision: (view: any, title: string, payload: unknown, options: unknown) => {
        calls.push({ title, notice: plain(view.notice), options: plain(options ?? null), mode: view.mode });
        return Promise.resolve({ approved: true, state: "approved", decidedBy: { kind: "user", via: "dialog" } } as any);
      },
      describeSession: () => ({ name: c.session.name ?? undefined, cwd: c.session.cwd }),
    };
    await new LocalUserAuthorizer(deps as any).authorize(c.details);
    out.local.push({ emitted, calls });
  }

  // ── the prompter's review entries ──
  out.prompter = [];
  for (const c of cases.prompter as any[]) {
    const entries: Json[] = [];
    const prompter = new PermissionPrompter({ logger: { review: (event: string, details: unknown) => { entries.push({ event, details: plain(details) }); } } as any });
    await prompter.prompt({ authorize: () => Promise.resolve(c.decision) } as any, c.details);
    out.prompter.push(entries);
  }

  // ── selection ──
  const hintKeys = [...SUBAGENT_ENV_HINT_KEYS];
  out.selection = (cases.selection as any[]).map((c) => {
    const saved: Record<string, string | undefined> = {};
    for (const key of hintKeys) { saved[key] = process.env[key]; delete process.env[key]; }
    if (c.env) process.env.PI_SUBAGENT_PARENT_SESSION = c.env;
    try {
      const asked: Json[] = [];
      const serving = {
        isServing: (target: unknown) => { asked.push(plain(target)); return c.serving; },
        describe: () => ({ channel: "none", state: null, servingIds: [] }),
      };
      const ctx = {
        hasUI: c.hasUI, mode: "tui", cwd: "/w", ui: { select: vi.fn(), input: vi.fn(), custom: vi.fn() },
        sessionManager: { getSessionId: () => "session-1", getSessionDir: () => "/sessions/session-1", getEntries: () => [], getSessionName: () => undefined },
      } as any;
      const deps = {
        detection: { isSubagent: () => c.subagent },
        events: { emit: vi.fn(), on: vi.fn() },
        dialogs: new AskDialogQueue(),
        getPromptPreferences: () => ({}) as any,
        requestPermissionDecision: vi.fn(),
        forwardingDir: "/tmp/f",
        registry: c.registry ? makeSubagentRegistry("session-1", { parentSessionId: c.registry }) : undefined,
        serving,
        getForwardingTimeoutMs: () => 1000,
        logger: { review: vi.fn(), debug: vi.fn() },
      } as any;
      const authority = selectAuthorizer(ctx, deps);
      return { terminal: authority.terminal.constructor.name, adjudicatesLocally: authority.adjudicatesLocally, relayTarget: plain(authority.relayTarget ?? null), asked };
    } finally {
      for (const key of hintKeys) { if (saved[key] === undefined) delete process.env[key]; else process.env[key] = saved[key]; }
      delete process.env.PI_SUBAGENT_PARENT_SESSION;
    }
  });

  // ── buildUiPrompt ──
  out.uiPrompt = (cases.uiPrompt as any[]).map((c) => plain(buildUiPrompt(c)));

  // ── the inline component ──
  out.component = [];
  for (const c of cases.component as any[]) {
    const writes: string[] = [];
    let renders = 0;
    let setCalls: boolean[] = [];
    let toolsExpanded = false;
    let component: { render(w: number): string[]; handleInput(d: string): void } | undefined;
    let optionsSeen: unknown;
    const decided: Json[] = [];
    const ui = {
      select: vi.fn(),
      input: vi.fn(),
      getToolsExpanded: () => toolsExpanded,
      setToolsExpanded: (v: boolean) => { toolsExpanded = v; setCalls.push(v); },
      custom: (factory: any, options: unknown) => {
        optionsSeen = options;
        return new Promise((resolve) => {
          component = factory(
            { requestRender: () => { renders++; }, terminal: { write: (d: string) => { writes.push(d); } } },
            tagTheme,
            { matches: (data: string, action: string) => action === "app.tools.expand" && data === c.expandKey },
            (d: unknown) => { decided.push(plain(d)); resolve(d); },
          );
        });
      },
    };
    const budget = c.pref.budget ? { maxRows: c.pref.budget.maxRows, fieldMaxWidth: c.pref.budget.fieldMaxWidth } : DEFAULT_RENDER_BUDGET;
    const view = {
      mode: "tui" as const,
      ui: ui as any,
      doublePressToConfirm: c.pref.double,
      budget,
      dialogKeys: KEYSETS[c.pref.keys] as any,
      promptNotifications: c.notifications,
      notice: c.notice,
    };
    const promise = presentInlinePermissionPrompt(view, "Permission Required", c.payloadJson, c.options ?? undefined);
    void promise;
    const frames: Json[] = [];
    frames.push({ lines: (c.widths as number[]).map((w) => component!.render(w)) });
    let final: Json = null;
    for (const key of c.keys as string[]) {
      renders = 0;
      const setBefore = setCalls.length;
      component!.handleInput(key);
      const step: Record<string, Json> = { renders, set: setCalls.slice(setBefore) };
      if (decided.length > 0) {
        step.decision = decided[0];
        frames.push(step);
        final = decided[0];
        break;
      }
      step.lines = (c.widths as number[]).map((w) => component!.render(w));
      frames.push(step);
    }
    out.component.push({ options: plain(optionsSeen), writes, frames, decision: final });
  }

  // ── the fallback flow through requestPermissionDecision ──
  out.fallback = [];
  for (const c of cases.fallback as any[]) {
    const selects = [...c.selects];
    const calls: Json[] = [];
    const inputs: Json[] = [];
    const ui = {
      select: (title: string, options: string[]) => { calls.push({ title, options }); const n = selects.shift(); return Promise.resolve(n === null || n === undefined ? undefined : n); },
      input: (title: string, placeholder?: string) => { inputs.push({ title, placeholder }); return Promise.resolve(c.input); },
      custom: vi.fn(), getToolsExpanded: vi.fn(), setToolsExpanded: vi.fn(),
    };
    const budget = c.budget ? { maxRows: c.budget.maxRows, fieldMaxWidth: c.budget.fieldMaxWidth } : DEFAULT_RENDER_BUDGET;
    const view = { mode: "rpc" as const, ui: ui as any, doublePressToConfirm: true, budget, dialogKeys: KEYSETS.default as any, promptNotifications: [], notice: { title: "t", body: "b" } };
    const decision = await requestPermissionDecision(view, "Title", c.payloadJson, c.options ?? undefined);
    out.fallback.push({ decision: plain(decision), selects: calls, inputs });
  }

  writeFileSync(output, JSON.stringify(out));
});
