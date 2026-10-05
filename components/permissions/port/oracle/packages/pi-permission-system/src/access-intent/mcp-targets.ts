import { getNonEmptyString, toRecord } from "#src/value-guards";
import { PI_MCP_TOOL_PREFIX } from "./tool-kind";

/**
 * An ordered accumulator that owns the uniqueness invariant.
 *
 * `add` ignores null/empty values and silently skips duplicates (first-insertion
 * wins). `toArray` returns the ordered result as an independent copy.
 */
export class McpTargetList {
  private readonly targets: string[] = [];

  add(value: string | null): void {
    if (!value) {
      return;
    }
    if (!this.targets.includes(value)) {
      this.targets.push(value);
    }
  }

  toArray(): string[] {
    return [...this.targets];
  }
}

/**
 * Parse a qualified MCP tool name of the form `server:tool`.
 *
 * Returns `{ server, tool }` when the string contains exactly one colon with
 * non-empty text on both sides; otherwise returns `null`.
 */
export function parseQualifiedMcpToolName(
  value: string,
): { server: string; tool: string } | null {
  const trimmed = value.trim();
  if (!trimmed) {
    return null;
  }

  const colonIndex = trimmed.indexOf(":");
  if (colonIndex <= 0 || colonIndex >= trimmed.length - 1) {
    return null;
  }

  const server = trimmed.slice(0, colonIndex).trim();
  const tool = trimmed.slice(colonIndex + 1).trim();
  if (!server || !tool) {
    return null;
  }

  return { server, tool };
}

/**
 * Find the configured server that owns `toolName` by the prefix convention.
 *
 * Returns the **longest** configured name that is the tool's leading
 * `<server>_` segment, so `foo_bar_baz` belongs to `foo_bar` and never also to
 * `foo`. Selecting here rather than trusting the caller's ordering keeps the
 * rule true for any caller: the production loader sorts longest-first, but the
 * `mcpServerNames` option does not.
 */
function findLongestPrefixServer(
  toolName: string,
  configuredServerNames: readonly string[],
): string | null {
  let longest: string | null = null;

  for (const serverName of configuredServerNames) {
    const trimmedServerName = serverName.trim();
    if (!trimmedServerName || !toolName.startsWith(`${trimmedServerName}_`)) {
      continue;
    }

    if (longest === null || trimmedServerName.length > longest.length) {
      longest = trimmedServerName;
    }
  }

  return longest;
}

function addDerivedMcpServerTargets(
  toolName: string,
  configuredServerNames: readonly string[],
  targets: McpTargetList,
): void {
  const trimmedToolName = toolName.trim();
  if (!trimmedToolName) {
    return;
  }

  // Prefix convention (`github_search_code`): the name already carries its
  // server, so the bare server is the only candidate worth deriving. A prefix
  // hit also settles the name's convention, which is why the suffix pass below
  // does not run — a tool ending in another configured server's name is a
  // coincidence, not a second owner.
  const prefixServer = findLongestPrefixServer(
    trimmedToolName,
    configuredServerNames,
  );
  if (prefixServer) {
    targets.add(trimmedToolName);
    targets.add(prefixServer);
    return;
  }

  // Suffix convention (`search_code_github`): the server is not part of any
  // candidate the caller will add, so the qualified forms are derived too.
  for (const serverName of configuredServerNames) {
    const trimmedServerName = serverName.trim();
    if (!trimmedServerName) {
      continue;
    }

    if (!trimmedToolName.endsWith(`_${trimmedServerName}`)) {
      continue;
    }

    targets.add(`${trimmedServerName}_${trimmedToolName}`);
    targets.add(`${trimmedServerName}:${trimmedToolName}`);
    targets.add(trimmedServerName);
  }
}

function pushMcpToolPermissionTargets(
  rawReference: string,
  serverHint: string | null,
  configuredServerNames: readonly string[],
  targets: McpTargetList,
): void {
  const qualified = parseQualifiedMcpToolName(rawReference);
  const resolvedServer = serverHint ?? qualified?.server ?? null;
  const resolvedTool = qualified?.tool ?? rawReference;

  if (resolvedServer) {
    // A name already carrying its server needs no re-prefixing: the qualified
    // forms would be `github_github_search_code`, which no rule can usefully
    // name, and which led the list as the reported target. The tool name is
    // itself the qualified form, so it leads instead — matching what prefix
    // derivation produces when no explicit server accompanies the call.
    if (resolvedTool.startsWith(`${resolvedServer}_`)) {
      targets.add(resolvedTool);
    } else {
      targets.add(`${resolvedServer}_${resolvedTool}`);
      targets.add(`${resolvedServer}:${resolvedTool}`);
    }
    targets.add(resolvedServer);
  } else {
    addDerivedMcpServerTargets(resolvedTool, configuredServerNames, targets);
  }

  targets.add(resolvedTool);
  targets.add(rawReference);
}

/**
 * Derive the ordered list of MCP permission-lookup candidates from a raw MCP
 * tool invocation input.
 *
 * Candidates are ordered from most-specific to least-specific. The order does
 * not decide which rule wins — `evaluateAnyValue()` gives that to the last
 * matching rule — but it decides which candidate a winning rule is reported
 * against, so the most specific name the rule matches is the one the prompt
 * and the review log show.
 */
export function createMcpPermissionTargets(
  input: unknown,
  configuredServerNames: readonly string[] = [],
): string[] {
  const record = toRecord(input);
  const tool = getNonEmptyString(record.tool);
  const server = getNonEmptyString(record.server);
  const connect = getNonEmptyString(record.connect);
  const describe = getNonEmptyString(record.describe);
  const search = getNonEmptyString(record.search);

  const targets = new McpTargetList();

  if (tool) {
    pushMcpToolPermissionTargets(tool, server, configuredServerNames, targets);
    targets.add("mcp_call");
    return targets.toArray();
  }

  if (connect) {
    targets.add(`mcp_connect_${connect}`);
    targets.add(connect);
    targets.add("mcp_connect");
    return targets.toArray();
  }

  if (describe) {
    pushMcpToolPermissionTargets(
      describe,
      server,
      configuredServerNames,
      targets,
    );
    targets.add("mcp_describe");
    return targets.toArray();
  }

  if (search) {
    if (server) {
      targets.add(`mcp_server_${server}`);
      targets.add(server);
    }

    targets.add(search);
    targets.add("mcp_search");
    return targets.toArray();
  }

  if (server) {
    targets.add(`mcp_server_${server}`);
    targets.add(server);
    targets.add("mcp_list");
    return targets.toArray();
  }

  targets.add("mcp_status");
  return targets.toArray();
}

/**
 * Derive the ordered MCP permission-lookup candidates for a tool Pi's built-in
 * MCP registered under its own name, `mcp__<server>__<tool>`.
 *
 * The candidates mirror a proxied call's (`<server>_<tool>`, `<server>:<tool>`,
 * `<server>`, `<tool>`, `mcp_call`) plus the full Pi name, so an `mcp` rule
 * written for either client names the same call. Pi rewrites every character
 * outside `[A-Za-z0-9_]` to `_` in the name, so the server is emitted in both
 * spellings: the configured one from `mcp.json` (`danger-srv`) and the one in
 * the name (`danger_srv`). Pi rejects two servers that differ only in `-` and
 * `_`, which is what makes treating the two spellings as one server safe.
 *
 * The server is the longest configured name whose sanitized form prefixes the
 * tool name; with none, the name splits at its first `__`.
 */
export function createPiMcpToolTargets(
  toolName: string,
  configuredServerNames: readonly string[],
): string[] {
  const rest = toolName.trim().slice(PI_MCP_TOOL_PREFIX.length);
  const { spellings, tool } = resolvePiMcpServer(rest, configuredServerNames);

  const targets = new McpTargetList();
  for (const server of spellings) {
    targets.add(`${server}_${tool}`);
    targets.add(`${server}:${tool}`);
    targets.add(server);
  }
  targets.add(tool);
  targets.add(toolName.trim());
  targets.add("mcp_call");
  return targets.toArray();
}

/** The server spellings and tool a Pi MCP tool name (prefix removed) holds. */
function resolvePiMcpServer(
  rest: string,
  configuredServerNames: readonly string[],
): { spellings: string[]; tool: string } {
  let sanitizedServer: string | null = null;
  let configured: string[] = [];

  for (const serverName of configuredServerNames) {
    const trimmed = serverName.trim();
    const sanitized = sanitizePiMcpName(trimmed);
    if (!trimmed || !rest.startsWith(`${sanitized}__`)) continue;
    if (sanitizedServer === null || sanitized.length > sanitizedServer.length) {
      sanitizedServer = sanitized;
      configured = [trimmed];
    } else if (sanitized === sanitizedServer) {
      configured.push(trimmed);
    }
  }

  if (sanitizedServer === null) {
    const separator = rest.indexOf("__");
    sanitizedServer = rest.slice(0, separator);
  }

  return {
    spellings: [...new Set([...configured, sanitizedServer])],
    tool: rest.slice(sanitizedServer.length + 2),
  };
}

/** Pi's MCP name sanitization: every character outside `[A-Za-z0-9_]` becomes `_`. */
function sanitizePiMcpName(name: string): string {
  return name.replace(/[^A-Za-z0-9_]/g, "_");
}
