// The install check behind the PiG provider's status. It has no Paseo imports, so it runs under node --test.

/** The oldest pig-acp this plugin drives; earlier releases lack the session behavior Paseo needs. */
export const MIN_PIG_ACP_VERSION = "0.2.1";

export interface ExecResult {
  stdout: string;
}

export type Exec = (
  command: string,
  args: string[],
  options: { env: Record<string, string>; timeout: number },
) => Promise<ExecResult>;

export interface LaunchLike {
  command: string;
  args: readonly string[];
  env: Readonly<Record<string, string>>;
}

export type PigStatus = { available: true; diagnostic: string } | { available: false; diagnostic: string };

const VERSION_TIMEOUT_MS = 5000;

export function parsePigAcpVersion(stdout: string): { version: string; protocol: string | undefined } | undefined {
  const match = /^pig-acp (\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)(?: \(ACP protocol version (\d+)\))?/m.exec(stdout.trim());
  return match ? { version: match[1], protocol: match[2] } : undefined;
}

/**
 * `pig --version` prints Pi's bare version (1.5.0) from PiG 0.7.1 on, and `<PiG>+<Pi>` (0.7.0+1.5.0) in earlier releases.
 */
export function parsePigVersion(stdout: string): { pig: string | undefined; pi: string } | undefined {
  const match = /^(?:(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\+)?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/.exec(stdout.trim());
  return match ? { pig: match[1], pi: match[2] } : undefined;
}

interface ParsedSemver {
  core: [number, number, number];
  prerelease: string[];
}

function splitSemver(version: string): ParsedSemver {
  const withoutBuild = version.split("+", 1)[0];
  const dash = withoutBuild.indexOf("-");
  const coreText = dash >= 0 ? withoutBuild.slice(0, dash) : withoutBuild;
  const prereleaseText = dash >= 0 ? withoutBuild.slice(dash + 1) : "";
  const parts = coreText.split(".").map(Number);
  return {
    core: [parts[0] ?? 0, parts[1] ?? 0, parts[2] ?? 0],
    prerelease: prereleaseText ? prereleaseText.split(".") : [],
  };
}

/** Semver 2.0.0 section 11 precedence (build metadata after `+` is ignored). */
export function compareVersions(a: string, b: string): number {
  const left = splitSemver(a);
  const right = splitSemver(b);
  for (let i = 0; i < 3; i++) {
    if (left.core[i] !== right.core[i]) return left.core[i] < right.core[i] ? -1 : 1;
  }
  if (left.prerelease.length === 0 && right.prerelease.length === 0) return 0;
  if (left.prerelease.length === 0) return 1;
  if (right.prerelease.length === 0) return -1;
  const limit = Math.min(left.prerelease.length, right.prerelease.length);
  for (let i = 0; i < limit; i++) {
    const l = left.prerelease[i];
    const r = right.prerelease[i];
    if (l === r) continue;
    const lNum = /^\d+$/.test(l);
    const rNum = /^\d+$/.test(r);
    if (lNum && rNum) return Number(l) < Number(r) ? -1 : 1;
    if (lNum) return -1;
    if (rNum) return 1;
    return l < r ? -1 : 1;
  }
  if (left.prerelease.length !== right.prerelease.length) {
    return left.prerelease.length < right.prerelease.length ? -1 : 1;
  }
  return 0;
}

const VALUE_FLAGS: Record<string, true> = { "-piglet": true, "--piglet": true, "-pig-arg": true, "--pig-arg": true };

/**
 * The pig executable pig-acp will drive: mirrors Go's flag.FlagSet (`--pig`, `--pig=`, `-pig`, `-pig=`, last wins,
 * stopping at `--`), then PIG_ACP_PIG_COMMAND, then PI_ACP_PI_COMMAND, then `pig`.
 */
export function pigCommand(launch: LaunchLike): string {
  let found: string | undefined;
  const args = launch.args;
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--") break;
    if (arg === "--pig" || arg === "-pig") {
      if (i + 1 < args.length) {
        found = args[i + 1];
        i += 1;
      }
      continue;
    }
    if (arg.startsWith("--pig=")) {
      found = arg.slice("--pig=".length);
      continue;
    }
    if (arg.startsWith("-pig=")) {
      found = arg.slice("-pig=".length);
      continue;
    }
    if (Object.hasOwn(VALUE_FLAGS, arg) && i + 1 < args.length) {
      i += 1;
    }
  }
  if (found) return found;
  return launch.env.PIG_ACP_PIG_COMMAND || launch.env.PI_ACP_PI_COMMAND || "pig";
}

function versionProbeArgs(args: readonly string[]): string[] {
  const stripped = args.length > 0 && args[args.length - 1] === "--" ? args.slice(0, -1) : args;
  return [...stripped, "--version"];
}

function reason(error: unknown): string {
  if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") return "not found";
  return error instanceof Error ? error.message.split("\n")[0] : String(error);
}

export async function checkPigInstall(exec: Exec, launch: LaunchLike): Promise<PigStatus> {
  const env = { ...launch.env };
  const run = (command: string, args: string[]) => exec(command, args, { env, timeout: VERSION_TIMEOUT_MS });

  let acp;
  try {
    acp = parsePigAcpVersion((await run(launch.command, versionProbeArgs(launch.args))).stdout);
  } catch (error) {
    return {
      available: false,
      diagnostic: `pig-acp (${launch.command}) did not run: ${reason(error)}. Install pig-acp, or set agents.providers.pig.command to its path.`,
    };
  }
  if (!acp) return { available: false, diagnostic: `${launch.command} --version did not print a pig-acp version.` };
  if (compareVersions(acp.version, MIN_PIG_ACP_VERSION) < 0) {
    return { available: false, diagnostic: `pig-acp ${acp.version} is older than ${MIN_PIG_ACP_VERSION}. Update pig-acp.` };
  }

  const pig = pigCommand(launch);
  let version;
  try {
    version = parsePigVersion((await run(pig, ["--version"])).stdout);
  } catch (error) {
    return {
      available: false,
      diagnostic: `pig (${pig}) did not run: ${reason(error)}. Install PiG, or point pig-acp at it with --pig <path>.`,
    };
  }
  if (!version) return { available: false, diagnostic: `${pig} --version did not print a version.` };

  const released = version.pig ? `PiG ${version.pig}, ` : "";
  return { available: true, diagnostic: `pig-acp ${acp.version}, ${released}Pi ${version.pi}` };
}
