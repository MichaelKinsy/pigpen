// Session probe: drives scripts of operations through the original's PermissionSession (real SessionRules and active-agent lookups, recording
// fakes for the forwarding controller, the authorizer selection, the permission manager and the config store) and writes every return value
// and every collaborator call, for the Go differential test (extensions/pi-permission-system/testdata/session-oracle.json). It is copied into
// an installed copy of the original by port/record-session-probe.sh and never runs from here; the port itself has no Node.
import { readFileSync, writeFileSync } from "node:fs";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import { it } from "vitest";
import type { PermissionSystemExtensionConfig } from "#src/config/extension-config";
import { posixPathFlavor, win32PathFlavor } from "#src/path/path-flavor";
import { PermissionSession } from "#src/session/permission-session";
import { SessionRules } from "#src/session/session-rules";

const must = (v: string | undefined, name: string): string => {
  if (!v) throw new Error(`${name} is required`);
  return v;
};

type Ctx = { cwd: string; hasUI: boolean; entries: unknown[] | "throws" };
type Op = {
  op: string;
  ctx: number | null;
  trusted: boolean;
  msg: string;
  prompt: string | null;
  names: string[];
  active: string[];
  registered: string[];
  denied: string[];
  surface: string;
  pattern: string;
};
type Script = {
  id: string;
  flavor: "posix" | "win32";
  ctxs: Ctx[];
  configs: { yoloMode: boolean; piInfrastructureReadPaths: string[] | null; shellTools: unknown | null }[];
  ops: Op[];
};

it("records the session corpus", () => {
  const scripts: Script[] = JSON.parse(readFileSync(must(process.env.PROBE_SESSION_CASES, "PROBE_SESSION_CASES"), "utf8"));
  const out = scripts.map((s) => {
    const log: unknown[][] = [];
    const makeCtx = (c: Ctx): ExtensionContext => ({
      cwd: c.cwd,
      hasUI: c.hasUI,
      ui: {
        notify: (m: string, level: string) => log.push(["notify", m, level]),
        setStatus: (k: string, v: string | undefined) => log.push(["setStatus", k, v ?? null]),
      },
      sessionManager: {
        getEntries: () => {
          if (c.entries === "throws") throw new Error("boom");
          return c.entries;
        },
      },
    }) as unknown as ExtensionContext; // a recording fake of the slice of the context the session reads
    const ctxs = s.ctxs.map(makeCtx);
    let refreshes = 0;
    const current = () => {
      const c = s.configs[Math.min(refreshes, s.configs.length - 1)];
      const cfg: Record<string, unknown> = { yoloMode: c.yoloMode };
      if (c.piInfrastructureReadPaths !== null) cfg.piInfrastructureReadPaths = c.piInfrastructureReadPaths;
      if (c.shellTools !== null) cfg.shellTools = c.shellTools;
      return cfg as unknown as PermissionSystemExtensionConfig;
    };
    const forwarding = { start: (c: ExtensionContext) => log.push(["forward.start", c.cwd]), stop: () => log.push(["forward.stop"]) };
    const authorizer = { activate: (c: ExtensionContext) => log.push(["auth.activate", c.cwd]), deactivate: () => log.push(["auth.deactivate"]) };
    const manager = { configureForCwd: (cwd: string | undefined) => log.push(["manager.configure", cwd ?? null]) };
    const store = {
      current,
      refresh: (cwd: string | undefined, trusted: boolean) => {
        refreshes += 1;
        log.push(["store.refresh", cwd ?? null, trusted]);
      },
      logResolvedPaths: (cwd?: string) => log.push(["store.logPaths", cwd ?? null]),
    };
    const rules = new SessionRules();
    // recording fakes of the narrow collaborator interfaces
    const deps = [
      { piInfrastructureDirs: ["/pi/a", "/pi/b"], piInfrastructureExcludedDirs: ["/pi/x"] },
      forwarding,
      manager,
      rules,
      store,
      authorizer,
      s.flavor === "win32" ? win32PathFlavor : posixPathFlavor,
    ] as unknown as ConstructorParameters<typeof PermissionSession>;
    const session = new PermissionSession(...deps);
    const results = s.ops.map((o) => {
      log.length = 0;
      let value: unknown = null;
      try {
        switch (o.op) {
          case "activate": session.activate(ctxs[o.ctx ?? 0]); break;
          case "deactivate": session.deactivate(); break;
          case "reset": session.resetForNewSession(ctxs[o.ctx ?? 0], o.trusted); break;
          case "reload": session.reload(o.trusted); break;
          case "shutdown": session.shutdown(); break;
          case "notify": session.notify(o.msg); break;
          case "resolveAgent": value = session.resolveAgentName(ctxs[o.ctx ?? 0], o.prompt ?? undefined); break;
          case "lastKnown": value = session.lastKnownActiveAgentName; break;
          case "setSkills": session.setActiveSkillEntries(o.names.map((n: string) => ({ name: n, description: "", location: "", state: "allow", normalizedLocation: "", normalizedBaseDir: "" }))); break;
          case "getSkills": value = session.getActiveSkillEntries().map((e) => e.name); break;
          case "exposed": {
            const denied = new Set<string>(o.denied);
            value = session.resolveExposedTools({ active: o.active, registered: new Set<string>(o.registered) }, (t) => !denied.has(t));
            break;
          }
          case "refresh": session.refreshConfig(o.ctx === null ? undefined : ctxs[o.ctx], o.trusted); break;
          case "logPaths": session.logResolvedConfigPaths(); break;
          case "config": value = session.config; break;
          case "infra": value = session.getInfrastructureReadScope(); break;
          case "shell": value = session.getShellToolAliases() ?? null; break;
          case "previewLimits": value = session.getToolPreviewLimits(); break;
          case "normalizer": {
            const n = session.getPathNormalizer();
            value = { resolveBase: n.resolveBase("sub"), isAbsolute: [n.isAbsolute("/a"), n.isAbsolute("C:\\a"), n.isAbsolute("rel")] };
            break;
          }
          case "approve": rules.approve(o.surface, o.pattern); break;
          case "rules": value = rules.getRuleset(); break;
          default: throw new Error(`unknown op ${o.op}`);
        }
      } catch (e) {
        return { throws: true, log: [...log] };
      }
      return { value: JSON.parse(JSON.stringify(value ?? null)), log: [...log] };
    });
    return { id: s.id, results };
  });
  writeFileSync(must(process.env.PROBE_SESSION_OUT, "PROBE_SESSION_OUT"), `${JSON.stringify(out)}\n`);
});
