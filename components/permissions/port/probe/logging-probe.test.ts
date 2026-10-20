// Logging probe: runs port/probe/logging-cases.json through the original's own functions and writes what they answer, for the Go
// differential (extensions/pi-permission-system/testdata/logging-oracle.json). Copied into an installed copy of the original by
// port/record-logging-probe.sh and never runs from here; the port itself has no Node.
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { warmBashParser } from "#src/access-intent/bash/parser";
import {
  describeBashCommandContext,
  flaggedElementLabel,
  flaggedElements,
  valueLabel,
} from "#src/presentation/fact-vocabulary";
import { renderReviewLogFacts } from "#src/presentation/review-log-renderer";
import {
  maskCommandFields,
  redactCommandSecrets,
} from "#src/logging/command-redaction";
import { capLogFieldWidths } from "#src/logging/log-field-cap";
import { createPermissionSystemLogger } from "#src/logging/logging";
import { safeJsonStringify } from "#src/logging/json-safe-stringify";
import {
  isSensitiveName,
  redactedJsonStringify,
} from "#src/logging/log-redaction";

type Spec = unknown;

/** Builds a value from a spec; see port/probe/gen-logging-cases.py for the markers. */
function build(spec: Spec, ids: Map<string, object> = new Map()): unknown {
  if (Array.isArray(spec)) return spec.map((entry) => build(entry, ids));
  if (spec === null || typeof spec !== "object") return spec;
  const record = spec as Record<string, unknown>;
  if ("$bigint" in record) return BigInt(record.$bigint as string);
  if ("$undefined" in record) return undefined;
  if ("$fn" in record) return () => 1;
  if ("$nan" in record) return Number.NaN;
  if ("$inf" in record) return (record.$inf as number) * Number.POSITIVE_INFINITY;
  if ("$negzero" in record) return -0;
  if ("$ref" in record) return ids.get(record.$ref as string);
  if ("$error" in record) {
    const e = record.$error as { name: string; message: string; stack: string | null };
    const error = new Error(e.message);
    error.name = e.name;
    (error as { stack?: string }).stack = e.stack === null ? undefined : e.stack;
    return error;
  }
  const out: Record<string, unknown> = {};
  if ("$id" in record) ids.set(record.$id as string, out);
  for (const [key, value] of Object.entries(record)) {
    if (key === "$id") continue;
    Object.defineProperty(out, key, {
      value: build(value, ids),
      enumerable: true,
      writable: true,
      configurable: true,
    });
  }
  return out;
}

const widthOf = (w: unknown): number =>
  typeof w === "object" && w !== null
    ? "$nan" in w
      ? Number.NaN
      : (w as { $inf: number }).$inf * Number.POSITIVE_INFINITY
    : (w as number);

it("records the original's logging answers", async () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  await warmBashParser();
  const cases = JSON.parse(readFileSync(input, "utf8"));

  const sensitive = cases.sensitive.map((c: { name: string }) => ({
    name: c.name,
    out: isSensitiveName(c.name),
  }));

  const stringify = cases.stringify.map((spec: Spec) => ({
    spec,
    safe: safeJsonStringify(build(spec)) ?? null,
    redacted: redactedJsonStringify(build(spec)) ?? null,
  }));

  const cap = cases.cap.map((c: { value: Spec; width: unknown }) => ({
    ...c,
    out: safeJsonStringify(capLogFieldWidths(build(c.value), widthOf(c.width))) ?? null,
  }));

  const redact = cases.redact.map((command: string) => ({
    in: command,
    out: redactCommandSecrets(command),
  }));

  const mask = cases.mask.map((spec: Spec) => ({
    spec,
    out: safeJsonStringify(maskCommandFields(build(spec))) ?? null,
  }));

  const dir = mkdtempSync(join(tmpdir(), "logging-probe-"));
  const lines = cases.lines.map(
    (c: { stream: "debug" | "review"; event: string; details: Spec; width: number | null }) => {
      const logs = join(dir, "logs");
      const debugLogPath = join(logs, "debug.jsonl");
      const reviewLogPath = join(logs, "review.jsonl");
      rmSync(logs, { recursive: true, force: true });
      const logger = createPermissionSystemLogger({
        getConfig: () =>
          ({
            debugLog: true,
            permissionReviewLog: true,
            ...(c.width === null ? {} : { reviewLogFieldMaxWidth: c.width }),
          }) as never,
        debugLogPath,
        reviewLogPath,
        ensureLogsDirectory: () => {
          mkdirSync(logs, { recursive: true });
          return undefined;
        },
      });
      const details = build(c.details) as Record<string, unknown>;
      const warning =
        c.stream === "debug"
          ? logger.debug(c.event, details)
          : logger.review(c.event, details);
      const line = warning
        ? null
        : readFileSync(c.stream === "debug" ? debugLogPath : reviewLogPath, "utf8");
      return { ...c, warning: warning ?? null, line };
    },
  );
  rmSync(dir, { recursive: true, force: true });

  const payloads = cases.payloads.map((payload: never) => {
    const p = payload as { kind: string; request: { surface: string; commandContext: never } };
    return {
      payload,
      facts: JSON.stringify(renderReviewLogFacts(payload)),
      flagged: JSON.stringify(flaggedElements(payload)),
      flaggedLabel: flaggedElementLabel(payload),
      valueLabel: valueLabel(payload),
      context: describeBashCommandContext(p.request.commandContext) ?? null,
    };
  });

  writeFileSync(output, JSON.stringify({ sensitive, stringify, cap, redact, mask, lines, payloads }));
}, 120000);
