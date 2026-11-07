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

export function compareVersions(a: string, b: string): number {
  const left = a.split("-")[0].split(".").map(Number);
  const right = b.split("-")[0].split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (left[i] !== right[i]) return left[i] < right[i] ? -1 : 1;
  }
  return 0;
}

/** The pig executable pig-acp will drive: its `--pig` argument, then PIG_ACP_PIG_COMMAND, then `pig`. */
export function pigCommand(launch: LaunchLike): string {
  const at = launch.args.indexOf("--pig");
  if (at >= 0 && launch.args[at + 1]) return launch.args[at + 1];
  return launch.env.PIG_ACP_PIG_COMMAND || "pig";
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
    acp = parsePigAcpVersion((await run(launch.command, [...launch.args, "--version"])).stdout);
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
