import type { ShellToolsConfig } from "#src/config/config-schema";
import { getNonEmptyString, toRecord } from "#src/value-guards";
import { PATH_BEARING_TOOLS } from "./path-surfaces";

/**
 * What a tool invocation accesses — decided once from the tool name at the
 * point an invocation enters the system.
 *
 * This is the single dispatch point that replaces the scattered
 * `toolName === "bash"`/`"mcp"` re-derivation across the extraction consumers
 * (`input-normalizer`, `tool-input-path`, the tool-call gate pipeline, and
 * `permission-manager`'s source derivation) and the presentation consumers
 * (`tool-preview-formatter`, `permission-prompts`, the payload builders, and
 * `deriveDecisionValue`), which dispatch on {@link classifyToolKind} or
 * {@link isMcpCheck}. Adding a tool kind means editing {@link classifyToolKind}
 * plus the exhaustive switches the compiler flags — an OCP win over silent
 * `===` comparisons a new variant sails past (#561).
 *
 * The value is plain data (a string union): `tool-kind.ts` imports no
 * `AccessPath`, so `permission-manager.ts` may consume it without breaching the
 * string boundary formalized in ADR-0002
 * (`docs/decisions/0002-path-values-string-boundary.md`).
 *
 * - `bash` — its own token-based path gates; extraction product is the command.
 * - `mcp` — the `mcp` proxy tool (e.g. pi-mcp-adapter), whose input names the
 *   MCP call (`{ tool, server, args }`); extraction product is the qualified
 *   target.
 * - `mcp-tool` — one MCP server tool registered under its own name,
 *   `mcp__<server>__<tool>` (Pi's built-in MCP); its input is the MCP
 *   arguments themselves, so it shares the extension tools' input shape while
 *   resolving on the `mcp` surface.
 * - `skill` — a distinct surface `normalizeInput`/`deriveSource` treat specially.
 * - `path` — a path-bearing built-in (`read`/`write`/`edit`/`grep`/`find`/`ls`);
 *   extraction product is `input.path`.
 * - `extension` — every other tool, plus the `external_directory`/`path` special
 *   surfaces that reach `deriveSource` as normalized names.
 */
export type ToolKind =
  | "bash"
  | "mcp"
  | "mcp-tool"
  | "skill"
  | "path"
  | "extension";

/** Classify a tool name into its {@link ToolKind}. */
export function classifyToolKind(toolName: string): ToolKind {
  const name = toolName.trim();
  if (name === "bash") return "bash";
  if (name === "mcp") return "mcp";
  if (isPiMcpToolName(name)) return "mcp-tool";
  if (name === "skill") return "skill";
  if (PATH_BEARING_TOOLS.has(name)) return "path";
  return "extension";
}

/** The prefix Pi's built-in MCP gives every server tool it registers. */
export const PI_MCP_TOOL_PREFIX = "mcp__";

/**
 * True when a tool name has the shape Pi's built-in MCP registers a server
 * tool under: `mcp__<server>__<tool>`, with non-empty server and tool parts.
 *
 * Pi builds the name with every character outside `[A-Za-z0-9_]` replaced by
 * `_`, and codemode scripts call the tool by the same identifier, so the name
 * itself is the stable contract (`extensions/mcp/tools.ts`,
 * `createMcpToolName`).
 */
export function isPiMcpToolName(toolName: string): boolean {
  if (!toolName.startsWith(PI_MCP_TOOL_PREFIX)) return false;
  const rest = toolName.slice(PI_MCP_TOOL_PREFIX.length);
  const separator = rest.indexOf("__");
  return separator > 0 && separator + 2 < rest.length;
}

/** A shell invocation's effective command and optional working directory. */
export interface ShellInvocation {
  /** The shell command string to decompose and gate. */
  command: string;
  /** The working directory the command runs in, if the tool projects one. */
  workdir: string | undefined;
}

/**
 * Decide whether a tool invocation carries shell semantics, and if so extract
 * its command and working directory.
 *
 * Native `bash` and any tool recorded in `shellTools` both yield a
 * {@link ShellInvocation}; every other tool yields `null`. This is the single
 * dispatch point the bash gate pipeline consults instead of re-deriving
 * `toolName === "bash"` and reading `input.command`, so an aliased shell tool
 * (e.g. `@howaboua/pi-codex-conversion`'s `exec_command`) is routed through the
 * same bash enforcement stack as native `bash` (#574).
 *
 * The command and workdir are read through {@link getNonEmptyString} (trimmed,
 * empty → `""`/`undefined`), matching the pipeline's existing native-bash
 * extraction. Kept separate from {@link classifyToolKind} because it needs
 * config (the alias map) and returns a richer product than a {@link ToolKind}
 * string — `classifyToolKind` stays AccessPath-free and config-free.
 */
export function resolveShellInvocation(
  toolName: string,
  input: unknown,
  aliases: ShellToolsConfig | undefined,
): ShellInvocation | null {
  const name = toolName.trim();
  const record = toRecord(input);

  if (name === "bash") {
    return {
      command: getNonEmptyString(record.command) ?? "",
      workdir: undefined,
    };
  }

  const alias = aliases?.[name];
  if (alias) {
    return {
      command: getNonEmptyString(record[alias.commandArgument]) ?? "",
      workdir: alias.workdirArgument
        ? (getNonEmptyString(record[alias.workdirArgument]) ?? undefined)
        : undefined,
    };
  }

  return null;
}

/** The resolved-check fields that decide MCP-ness. */
interface McpKindFields {
  toolName: string;
  source: string;
}

/**
 * True when a resolved check concerns an MCP call — the invoked tool is the
 * `mcp` proxy or a Pi MCP tool, or the winning rule matched on the `mcp`
 * surface (`source`). The `source` disjunct is why this cannot reduce to
 * `classifyToolKind(toolName)`: `deriveSource` can set `source` to `mcp` on a
 * result whose `toolName` is a server-qualified string.
 */
export function isMcpCheck(check: McpKindFields): boolean {
  if (check.source === "mcp") return true;
  const kind = classifyToolKind(check.toolName);
  return kind === "mcp" || kind === "mcp-tool";
}

/**
 * True when a check concerns an MCP call whose tool input is a request *about*
 * the call (the proxy's `{ tool, server, args }`) rather than the MCP
 * arguments themselves. Such input is not previewed — the target already
 * names the call — while a Pi MCP tool's arguments are shown like any other
 * tool's input.
 */
export function isProxyMcpCheck(check: McpKindFields): boolean {
  return isMcpCheck(check) && classifyToolKind(check.toolName) !== "mcp-tool";
}
