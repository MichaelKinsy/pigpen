// Review probe: runs port/probe/cases.json through the original and writes what it decides, for the Go oracle tests
// (extensions/pi-permission-system/testdata/oracle.json). It is copied into an installed copy of the original by
// port/record-probe.sh and never runs from here; the port itself has no Node.
import { readFileSync, writeFileSync } from "node:fs";
import { it } from "vitest";
import { warmBashParser } from "#src/access-intent/bash/parser";
import { parseBashCommandsSync } from "#src/access-intent/bash/sync-commands";
import { classifyWrapperWords } from "#src/access-intent/bash/wrapper-analysis";
import { stripJsonComments, validateUnifiedConfig } from "#src/config/config-loader";
import { resolveBashCommandCheck } from "#src/handlers/gates/bash-command";
import { resolveYoloGrant } from "#src/handlers/gates/helpers";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";
import { createInMemoryPolicyLoader } from "#test/helpers/manager-harness";

it("records the original's decisions for the review cases", async () => {
  const input = process.env.PROBE_IN;
  const output = process.env.PROBE_OUT;
  if (!input || !output) throw new Error("PROBE_IN and PROBE_OUT are required");
  await warmBashParser();
  const cases = JSON.parse(readFileSync(input, "utf8"));

  // The bash surface's decision, as the tool-call gate pipeline takes it for a shell invocation (tool-call-gate-pipeline.ts):
  // the command trimmed, its units parsed once, resolveBashCommandCheck, then the runner's yolo grant (runner.ts, helpers.ts).
  // The permission map goes through the schema first, as loadUnifiedConfig takes it from the file: the schema's output puts the
  // surfaces it names before the others.
  const bash = cases.bash.map((c: { permission: unknown; yolo: boolean; command: string }) => {
    const validated = validateUnifiedConfig({ permission: c.permission });
    if (validated.issues.length > 0) throw new Error(`invalid case config: ${validated.issues.join("; ")}`);
    const manager = new PermissionManager({
      policyLoader: createInMemoryPolicyLoader({ global: { permission: validated.config.permission } }),
      isYoloEnabled: () => c.yolo,
    });
    const resolver = new PermissionResolver(manager, { getRuleset: () => [] });
    const command = c.command.trim();
    let check;
    if (command === "") {
      check = resolver.resolve({ kind: "tool", surface: "bash", input: { command }, agentName: undefined });
    } else {
      const units = parseBashCommandsSync(command);
      if (units === null) throw new Error("the bash parser is not warm");
      check = resolveBashCommandCheck(command, units, undefined, resolver);
    }
    const grant = resolveYoloGrant(check, c.yolo);
    return {
      ...c,
      state: grant ? "allow" : check.state,
      matchedPattern: check.matchedPattern ?? null,
      reason: check.reason ?? null,
    };
  });

  // loadUnifiedConfig without the file: JSON.parse(stripJsonComments(raw)), then the schema.
  const config = cases.config.map((raw: string) => {
    try {
      const { config: value, issues } = validateUnifiedConfig(JSON.parse(stripJsonComments(raw)));
      return {
        raw,
        issues,
        yoloMode: value.yoloMode ?? null,
        shellTools: Object.keys(value.shellTools ?? {}),
        permissionJSON: JSON.stringify(value.permission ?? null),
      };
    } catch (error) {
      return { raw, parseError: (error as Error).message };
    }
  });

  // classifyWrapperWords on space-separated words (the upstream test's own stand-in for the parse, unquoted words only).
  const wrapper = cases.wrapper.map((unit: string) => {
    const words = unit
      .split(" ")
      .filter((w) => w !== "")
      .map((text, offset) => ({ text, offset, value: text, computed: false, mayLeadWithDash: text.startsWith("-") }));
    return { unit, kind: classifyWrapperWords(words) ?? null };
  });

  writeFileSync(output, `${JSON.stringify({ bash, config, wrapper }, null, 1)}\n`);
});
