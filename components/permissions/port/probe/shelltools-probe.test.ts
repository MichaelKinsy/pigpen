// ShellTools probe: writes each case's config files to a scratch agent dir and project, loads them through the original's real
// ConfigStore (loader, schema, merge, normalize) into a real PermissionSession, and records `getShellToolAliases()` and the shell
// invocation `resolveShellInvocation` decides for each tool call from it, for the Go differential test
// (extensions/pi-permission-system/shelltools_oracle_test.go, testdata/shelltools-oracle.json). Copied into an installed copy of the original by
// port/record-shelltools-probe.sh and never runs from here.
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { it } from "vitest";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import { resolveShellInvocation } from "#src/access-intent/tool-kind";
import { ConfigStore } from "#src/config/config-store";
import { posixPathFlavor } from "#src/path/path-flavor";
import { PermissionSession } from "#src/session/permission-session";
import { SessionRules } from "#src/session/session-rules";

const must = (v: string | undefined, name: string): string => {
  if (!v) throw new Error(`${name} is required`);
  return v;
};
const put = (p: string, text: string) => {
  mkdirSync(dirname(p), { recursive: true });
  writeFileSync(p, text);
};
const quiet = new Proxy({}, { get: () => () => undefined });

type Case = { id: string; global: string; project: string | null; trusted: boolean; tools: { name: string; input: unknown }[] };

it("records the shellTools corpus", () => {
  const cases: Case[] = JSON.parse(readFileSync(must(process.env.PROBE_SHELLTOOLS_CASES, "PROBE_SHELLTOOLS_CASES"), "utf8"));
  const tmp = mkdtempSync(join(tmpdir(), "shelltools-"));
  try {
    const out = cases.map((c, n) => {
      const agentDir = join(tmp, `agent${n}`);
      const cwd = join(tmp, `project${n}`);
      mkdirSync(cwd, { recursive: true });
      put(join(agentDir, "extensions/pi-permission-system/config.json"), c.global);
      if (c.project !== null) put(join(cwd, ".pi/extensions/pi-permission-system/config.json"), c.project);
      const store = new ConfigStore({ agentDir, policyPaths: { getResolvedPolicyPaths: () => ({}) }, logger: quiet } as never);
      const forwarding = { start: () => undefined, stop: () => undefined };
      const authorizer = { activate: () => undefined, deactivate: () => undefined };
      const manager = { configureForCwd: () => undefined };
      const session = new PermissionSession(
        { piInfrastructureDirs: [], piInfrastructureExcludedDirs: [] },
        forwarding, manager, new SessionRules(), store, authorizer, posixPathFlavor,
      ) as unknown as PermissionSession;
      const ctx = { cwd, hasUI: false, ui: { notify: () => undefined, setStatus: () => undefined }, sessionManager: { getEntries: () => [] } } as unknown as ExtensionContext;
      session.refreshConfig(ctx, c.trusted);
      const aliases = session.getShellToolAliases();
      return {
        id: c.id,
        aliases: JSON.parse(JSON.stringify(aliases ?? null)),
        issues: store.getConfigIssues(),
        invocations: c.tools.map((t) => {
          try {
            const r = resolveShellInvocation(t.name, t.input, aliases);
            return r === null ? null : { command: r.command, workdir: r.workdir ?? null };
          } catch (e) {
            return { throws: true };
          }
        }),
      };
    });
    writeFileSync(must(process.env.PROBE_SHELLTOOLS_OUT, "PROBE_SHELLTOOLS_OUT"), `${JSON.stringify(out)}\n`);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});
