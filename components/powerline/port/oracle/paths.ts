import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

export function getHomeDir(): string {
  return process.env.HOME || process.env.USERPROFILE || homedir();
}

export function normalizeAgentDirPath(value: string): string {
  const trimmed = value.trim();
  if (trimmed === "~") return getHomeDir();
  if (trimmed.startsWith("~/")) return join(getHomeDir(), trimmed.slice(2));
  if (process.platform === "win32" && trimmed.startsWith("~\\")) return join(getHomeDir(), trimmed.slice(2));
  if (trimmed.startsWith("file://")) return fileURLToPath(trimmed);
  return trimmed;
}

export function getAgentDir(): string {
  const configured = process.env.PI_CODING_AGENT_DIR;
  return configured && configured.trim() ? normalizeAgentDirPath(configured) : join(getHomeDir(), ".pi", "agent");
}

export function getAgentPath(...segments: string[]): string {
  return join(getAgentDir(), ...segments);
}

export function getLegacyPiPath(...segments: string[]): string {
  return join(getHomeDir(), ".pi", ...segments);
}

export function getAgentSessionDirs(): string[] {
  const primary = getAgentPath("sessions");
  const legacy = getLegacyPiPath("sessions");
  return existsSync(legacy) && legacy !== primary ? [primary, legacy] : [primary];
}
