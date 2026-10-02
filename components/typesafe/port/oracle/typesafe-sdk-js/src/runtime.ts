/** Runtime detection for browser guards and request headers. */

interface RuntimeGlobals {
  window?: { document?: unknown };
  navigator?: { userAgent?: string };
  process?: { versions?: Record<string, string | undefined>; platform?: string; arch?: string };
  Deno?: { version?: { deno?: string } };
  Bun?: { version?: string };
  EdgeRuntime?: unknown;
}

const g = globalThis as RuntimeGlobals;

/** Whether browser page globals are present. */
export const isBrowser = (): boolean =>
  typeof g.window !== "undefined" &&
  typeof g.window.document !== "undefined" &&
  typeof g.navigator !== "undefined";

/** Runtime name, version, and platform for the `X-TypeSafe-Runtime` header. */
export const describeRuntime = (): string => {
  const platform =
    g.process?.platform && g.process?.arch ? ` (${g.process.platform}; ${g.process.arch})` : "";
  if (g.Bun?.version) return `bun/${g.Bun.version}${platform}`;
  if (g.Deno?.version?.deno) return `deno/${g.Deno.version.deno}${platform}`;
  if (g.EdgeRuntime !== undefined) return "vercel-edge";
  if (g.navigator?.userAgent === "Cloudflare-Workers") return "cloudflare-workers";
  if (g.process?.versions?.node) return `node/${g.process.versions.node}${platform}`;
  if (isBrowser()) return "browser";
  return "unknown";
};
