// Malformed-intent probe: what the original's real PermissionResolver does with an access intent whose shape the types forbid (an unknown
// kind, a missing kind or surface, null, undefined), over a config that allows `read` and denies `bash`. Recorded for the Go test
// (extensions/pi-permission-system/intent_oracle_test.go, testdata/intent-oracle.json). Copied into an installed copy of the original by
// port/record-intent-probe.sh and never runs from here.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { PermissionManager } from "#src/policy/permission-manager";
import { PermissionResolver } from "#src/policy/permission-resolver";

it("records the malformed intents", () => {
  const base = mkdtempSync(join(tmpdir(), "intent-"));
  try {
    mkdirSync(join(base, "agents"), { recursive: true });
    const globalConfigPath = join(base, "config.json");
    writeFileSync(globalConfigPath, JSON.stringify({ permission: { "*": "ask", read: "allow", bash: "deny" } }));
    const manager = new PermissionManager({ globalConfigPath, agentsDir: join(base, "agents"), projectGlobalConfigPath: join(base, "none.json"), projectAgentsDir: join(base, "none"), mcpServerNames: [], isYoloEnabled: () => false });
    const resolver = new PermissionResolver(manager, { getRuleset: () => [] });
    const intents: [string, unknown][] = [
      ["tool read", { kind: "tool", surface: "read", input: {} }],
      ["tool bash", { kind: "tool", surface: "bash", input: { command: "ls" } }],
      ["unknown kind, surface read", { kind: "bogus", surface: "read", input: {} }],
      ["unknown kind, surface bash", { kind: "bogus", surface: "bash", input: { command: "ls" } }],
      ["unknown kind, no surface", { kind: "bogus", input: {} }],
      ["no kind, surface read", { surface: "read", input: {} }],
      ["no kind, surface bash", { surface: "bash", input: { command: "ls" } }],
      ["kind null", { kind: null, surface: "read" }],
      ["tool without surface", { kind: "tool", input: {} }],
      ["path-values without values", { kind: "path-values", surface: "read" }],
      ["bash-command without command", { kind: "bash-command", surface: "bash", spellings: [] }],
      ["access-path without path", { kind: "access-path", surface: "read" }],
      ["null", null],
      ["undefined", undefined],
      ["number", 5],
      ["string", "read"],
    ];
    const out = intents.map(([name, intent]) => {
      try {
        const r = resolver.resolve(intent as never);
        return { name, result: JSON.parse(JSON.stringify(r)) };
      } catch (e) {
        return { name, throws: e instanceof Error ? e.name : String(e) };
      }
    });
    writeFileSync(process.env.PROBE_INTENT_OUT as string, `${JSON.stringify(out, null, 1)}\n`);
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});
