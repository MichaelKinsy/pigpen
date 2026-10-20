// Presentation probe: runs testdata/presentation-cases.json through the original's presentation, tool-input and exposure modules
// and writes what they answer, for the Go differential (extensions/pi-permission-system/testdata/presentation-oracle.json). Copied
// into an installed copy of the original by port/record-presentation-probe.sh and never runs from here; the port itself has no Node.
//
// Every string in the output goes through toWellFormed(): a lone surrogate (a field cap that splits an astral character) reads as the
// U+FFFD a terminal or a UTF-8 file shows for it, which is the form the port writes.
import { readFileSync, writeFileSync } from "node:fs";
import { basename } from "node:path";
import { it } from "vitest";
import { registerBuiltinToolInputFormatters, formatMcpInputForPrompt } from "#src/tool-input/builtin-tool-input-formatters";
import { ToolInputFormatterRegistry, type ToolInputFormatterLookup } from "#src/tool-input/tool-input-formatter-registry";
import {
  countTextLines, formatCount, serializeToolInputPreview, serializeRedactedToolInputPreview, truncateInlineText,
} from "#src/tool-input/tool-input-preview";
import { ToolPreviewFormatter, resolveToolPreviewLimits } from "#src/tool-input/tool-preview-formatter";
import { fitLinesToWidth } from "#src/presentation/line-fitting";
import { completeViewBudget, renderPromptDialog } from "#src/presentation/dialog-renderer";
import { describePromptNotice, renderPromptNotification } from "#src/presentation/prompt-notification";
import {
  EXTENSION_TAG, renderAuthorizerDenial, renderEscalatedPolicyDenial, renderGateErrorDenial, renderPolicyDenial, renderRefusal,
  renderUnavailableDenial, renderUserDenial,
} from "#src/presentation/agent-renderer";
import {
  buildDirectionalSessionLabels, buildForwardedScopeLabels, buildPathAccessSessionLabel, describeGrantTarget, suggestBashPattern,
  suggestExactSessionPattern, suggestMcpPattern, suggestPathSessionPattern, suggestSessionPattern,
} from "#src/presentation/pattern-suggest";
import { formatMissingToolNameReason, formatUnknownToolReason } from "#src/presentation/permission-prompts";
import { buildToolAskPayload } from "#src/presentation/tool-ask-payload";
import {
  buildBashExternalDirectoryAskPayload, buildExternalDirectoryAskPayload, buildPathAskPayload,
} from "#src/presentation/path-ask-payload";
import { buildSkillAskPayload, buildSkillPathAskPayload } from "#src/presentation/skill-ask-payload";
import { buildForwardedAskPayload } from "#src/presentation/forwarded-ask-payload";
import {
  checkRequestedToolRegistration, getToolNameFromValue, getToolPromptGuidelinesFromValue, readRegisteredTools,
} from "#src/exposure/tool-registry";
import { renderToolSurfaceSections } from "#src/exposure/tool-surface-prompt";
import {
  findSkillPathMatch, parseAllSkillPromptSections, visibleSkillPromptEntries, withoutDeniedSkills,
} from "#src/exposure/skill-prompt-sanitizer";
import { posixPathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";
import { toRecord } from "#src/value-guards";

type Spec = unknown;

/** Builds a value from a spec; see port/probe/gen-logging-cases.py for the markers. */
function build(spec: Spec): unknown {
  if (Array.isArray(spec)) return spec.map((entry) => build(entry));
  if (spec === null || typeof spec !== "object") return spec;
  const record = spec as Record<string, unknown>;
  if ("$bigint" in record) return BigInt(record.$bigint as string);
  if ("$undefined" in record) return undefined;
  if ("$fn" in record) return () => 1;
  if ("$nan" in record) return Number.NaN;
  if ("$inf" in record) return (record.$inf as number) * Number.POSITIVE_INFINITY;
  if ("$negzero" in record) return -0;
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(record)) {
    Object.defineProperty(out, key, { value: build(value), enumerable: true, writable: true, configurable: true });
  }
  return out;
}

const num = (v: unknown): number =>
  typeof v === "object" && v !== null ? (v as { $inf: number }).$inf * Number.POSITIVE_INFINITY : (v as number);

/** Every string made well-formed, the way a UTF-8 sink writes it. */
function wf(value: unknown): unknown {
  if (typeof value === "string") return value.toWellFormed();
  if (Array.isArray(value)) return value.map(wf);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k.toWellFormed(), wf(v)]));
  }
  return value;
}

/** A case's answer, or what it threw. */
function run(fn: () => unknown): unknown {
  try {
    const v = fn();
    return { ok: v === undefined ? { $undefined: true } : v };
  } catch (error) {
    return { throws: String((error as Error).message) };
  }
}

const PAINTS: Record<string, ((t: string) => string) | undefined> = {
  none: undefined,
  brackets: (t) => `[${t}]`,
  ansi: (t) => `\x1b[33m${t}\x1b[39m`,
};

const normalizer = (cwd: string) => new PathNormalizer(posixPathFlavor, cwd);

function formatterFor(limits: { toolInputPreviewMaxLength: number; toolTextSummaryMaxLength: number } | null, lookup?: ToolInputFormatterLookup) {
  return new ToolPreviewFormatter(limits ?? resolveToolPreviewLimits(), lookup);
}

function builtinRegistry(): ToolInputFormatterRegistry {
  const registry = new ToolInputFormatterRegistry();
  registerBuiltinToolInputFormatters(registry);
  return registry;
}

function lookupOf(kind: string | null): ToolInputFormatterLookup | undefined {
  if (kind === "builtin") return builtinRegistry();
  if (kind === "decline") return { get: () => () => undefined };
  if (kind === "fixed") return { get: () => () => "fixed preview" };
  return undefined;
}

it("records the original's presentation answers", () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  // biome-ignore lint/suspicious/noExplicitAny: the case file is the probe's own format
  const cases: any = JSON.parse(readFileSync(input, "utf8"));
  const out: Record<string, unknown> = {};

  out.fit = cases.fit.map((c: { lines: string[]; width: number }) => run(() => fitLinesToWidth(c.lines, c.width)));

  out.dialog = cases.dialog.map((c: { payload: never; budget: { maxRows: unknown; fieldMaxWidth: unknown; width: number }; paint: string }) =>
    run(() =>
      renderPromptDialog(
        c.payload,
        { maxRows: num(c.budget.maxRows), fieldMaxWidth: num(c.budget.fieldMaxWidth), width: c.budget.width },
        PAINTS[c.paint],
      ),
    ),
  );

  out.notice = cases.notice.map((c: { title: string; channels: never[]; request: never; session: { name: string | null; cwd: string } }) =>
    run(() => {
      const notice = describePromptNotice(c.title, c.request, { name: c.session.name ?? undefined, cwd: c.session.cwd });
      return { notice, bytes: renderPromptNotification(c.channels, notice) };
    }),
  );

  out.agent = cases.agent.map(
    (c: {
      fn: string; payload: never; budget: unknown; reason: string | null; decision: never; linkName: string; pattern: string | null; decidedElsewhere: boolean;
    }) =>
      run(() => {
        const budget = { fieldMaxWidth: num(c.budget) };
        switch (c.fn) {
          case "policy": return renderPolicyDenial(c.payload, c.reason, budget);
          case "user": return renderUserDenial(c.payload, c.reason, budget);
          case "authorizer": return renderAuthorizerDenial(c.payload, c.linkName, c.reason, budget);
          case "escalated": return renderEscalatedPolicyDenial(c.payload, { pattern: c.pattern, decidedElsewhere: c.decidedElsewhere }, c.reason, budget);
          case "gateError": return renderGateErrorDenial(c.payload, { reason: c.reason ?? "", decidedElsewhere: c.decidedElsewhere }, budget);
          case "unavailable": return renderUnavailableDenial(c.payload, c.reason, budget);
          default: return renderRefusal(c.payload, c.decision, c.reason, budget);
        }
      }),
  );

  const s = cases.suggest;
  out.suggest = {
    tag: EXTENSION_TAG,
    bash: s.bash.map((c: { command: string }) => run(() => suggestBashPattern(c.command))),
    mcp: s.mcp.map((c: { target: string }) => run(() => suggestMcpPattern(c.target))),
    session: s.session.map((c: { surface: string; value: string }) => run(() => suggestSessionPattern(c.surface, c.value))),
    path: s.path.map((c: { surface: string; patterns: string[] }) => run(() => suggestPathSessionPattern(c.surface, c.patterns))),
    exact: s.exact.map((c: { surface: string; pattern: string }) => run(() => suggestExactSessionPattern(c.surface, c.pattern))),
    grantTarget: s.grantTarget.map((g: never[]) => run(() => describeGrantTarget(g))),
    directional: s.directional.map((c: { direction: "read" | "write"; target: string }) => run(() => buildDirectionalSessionLabels(c.direction, c.target))),
    forwarded: s.forwarded.map((c: { agentName: string | null; grants: never[] }) => run(() => buildForwardedScopeLabels(c.agentName, c.grants))),
    pathAccess: s.pathAccess.map((c: { grants: never[] }) => run(() => buildPathAccessSessionLabel(c.grants))),
  };

  out.inputs = cases.inputs.map((c: { tool: string; input: Spec; limits: never }) =>
    run(() => {
      const value = build(c.input);
      const plain = formatterFor(c.limits);
      const withBuiltin = formatterFor(c.limits, builtinRegistry());
      return {
        prompt: plain.formatToolInputForPrompt(c.tool, value),
        builtin: withBuiltin.formatToolInputForPrompt(c.tool, value),
        log: plain.formatGenericToolInputForLog(value) ?? null,
      };
    }),
  );
  out.mcpinput = cases.mcpinput.map((c: { input: Spec }) =>
    run(() => formatMcpInputForPrompt(toRecord(build(c.input))) ?? { $undefined: true }),
  );
  out.sanitize = cases.sanitize.map((c: { value: string; max: number | null }) =>
    run(() => formatterFor(null).sanitizeInlineText(c.value, c.max ?? undefined)),
  );
  out.preview = {
    truncate: cases.preview.truncate.map((c: { value: string; max: number }) => run(() => truncateInlineText(c.value, c.max))),
    count: cases.preview.count.map((c: { value: string }) => run(() => countTextLines(c.value))),
    format: cases.preview.format.map((c: { n: number }) => run(() => [formatCount(c.n, "line", "lines")])),
    serialize: cases.preview.serialize.map((c: { input: Spec }) =>
      run(() => ({ prompt: serializeToolInputPreview(build(c.input)), redacted: serializeRedactedToolInputPreview(build(c.input)) })),
    ),
  };

  out.log = cases.log.map((c: { result: never; input: Spec; pathBearing: string[] }) =>
    run(() => {
      const f = formatterFor(null);
      const value = build(c.input);
      const pathBearing = new Set(c.pathBearing);
      return {
        preview: f.getToolInputPreviewForLog(c.result, value, pathBearing) ?? null,
        context: f.getPermissionLogContext(c.result, value, pathBearing),
      };
    }),
  );

  out.askpayload = cases.askpayload.map((c: Record<string, never>) =>
    run(() => {
      let payload;
      switch (c.fn as unknown as string) {
        case "tool":
          payload = buildToolAskPayload({
            check: c.check, agentName: c.agentName, surface: c.surface, invokedToolName: c.invokedToolName as unknown as string | null,
            input: build(c.input), formatter: c.formatter === null ? undefined : formatterFor(null, lookupOf(c.formatter as unknown as string)),
          });
          break;
        case "path": payload = buildPathAskPayload(c.facts); break;
        case "extdir": payload = buildExternalDirectoryAskPayload(c.facts); break;
        case "bashext": payload = buildBashExternalDirectoryAskPayload(c.facts); break;
        case "skill": payload = buildSkillAskPayload(c.name as unknown as string, c.agentName as unknown as string | null); break;
        case "skillpath":
          payload = buildSkillPathAskPayload({ name: c.name as unknown as string } as never, c.readPath as unknown as string, c.agentName as unknown as string | null);
          break;
        default: {
          const r = c.request as unknown as Record<string, unknown>;
          const request = { ...r };
          for (const k of ["surface", "value"]) if (request[k] === "$none") delete request[k];
          payload = buildForwardedAskPayload(request as never);
        }
      }
      return { payload, rendered: renderPromptDialog(payload, { maxRows: 24, fieldMaxWidth: 400, width: 80 }) };
    }),
  );

  const r = cases.registry;
  out.registry = {
    name: r.name.map((c: { value: Spec }) => run(() => getToolNameFromValue(build(c.value)) ?? null)),
    guidelines: r.guidelines.map((c: { value: Spec }) => run(() => getToolPromptGuidelinesFromValue(build(c.value)))),
    read: r.read.map((c: { tools: Spec[] }) =>
      run(() => {
        const tools = readRegisteredTools(c.tools.map((t) => build(t)));
        return { names: tools.names, guidelines: [...tools.guidelinesByTool.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0)) };
      }),
    ),
    check: r.check.map((c: { requested: string | null; tools: Spec[]; aliases: Record<string, string> | null }) =>
      run(() => {
        const result = checkRequestedToolRegistration(c.requested, c.tools.map((t) => build(t)), c.aliases ?? undefined);
        const normalized = "normalizedToolName" in result ? result.normalizedToolName : undefined;
        return { ...result, normalizedToolName: typeof normalized === "string" || normalized === undefined ? normalized : "<non-string>" };
      }),
    ),
    reasons: r.reasons.map((c: { toolName: string; available: string[] }) => run(() => formatUnknownToolReason(c.toolName, c.available))),
    missing: formatMissingToolNameReason(),
  };

  out.surface = cases.surface.map(
    (c: { allowedTools: string[]; toolSnippets: Record<string, string>; guidelinesByTool: Record<string, string[]>; promptGuidelines: string[] }) =>
      run(() => {
        const sections = renderToolSurfaceSections({
          allowedTools: c.allowedTools, toolSnippets: c.toolSnippets, guidelinesByTool: new Map(Object.entries(c.guidelinesByTool)), promptGuidelines: c.promptGuidelines,
        });
        return { tools: sections.tools ?? null, rules: sections.rules };
      }),
  );

  const checker = (states: Record<string, string>, defaultState: string, calls: unknown[]) => ({
    checkPermission: (surface: string, value: unknown, agentName?: string) => {
      const name = (value as { name?: string }).name ?? "";
      calls.push([surface, name, agentName ?? null]);
      return { toolName: "skill", state: states[name] ?? defaultState, source: "tool", origin: "builtin" } as never;
    },
  });
  const k = cases.skills;
  out.skills = {
    parse: k.parse.map((c: { prompt: string }) => run(() => parseAllSkillPromptSections(c.prompt))),
    visible: k.visible.map(
      (c: { prompt: string; states: Record<string, string>; defaultState: string; agentName: string | null; cwd: string; probes: string[] }) =>
        run(() => {
          const calls: unknown[] = [];
          const n = normalizer(c.cwd);
          const entries = visibleSkillPromptEntries(c.prompt, checker(c.states, c.defaultState, calls), c.agentName, n);
          return { entries, calls, matches: c.probes.map((p) => findSkillPathMatch(p, entries, n)?.name ?? null) };
        }),
    ),
    without: k.without.map((c: { names: string[]; states: Record<string, string>; defaultState: string; agentName: string | null }) =>
      run(() => {
        const calls: unknown[] = [];
        const kept = withoutDeniedSkills(c.names.map((name) => ({ name })), checker(c.states, c.defaultState, calls), c.agentName);
        return { kept: kept.map((s) => s.name), calls };
      }),
    ),
    basename: ["/w/repo", "/", "", "/w/repo/", "//", "/a/b/c//", "rel/dir", "name", ".", ".."].map((p) => basename(p)),
  };

  writeFileSync(output, JSON.stringify(wf(out)));
}, 300000);
