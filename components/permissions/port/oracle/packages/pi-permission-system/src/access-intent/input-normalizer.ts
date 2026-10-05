import { stripBashCommentLines } from "#src/access-intent/bash/bash-arity";
import type { PathNormalizer } from "#src/path/path-normalizer";
import { getNonEmptyString, toRecord } from "#src/value-guards";
import type {
  AccessIntent,
  PathValuesAccessIntent,
  ResolvedAccessIntent,
} from "./access-intent";
import {
  createMcpPermissionTargets,
  createPiMcpToolTargets,
} from "./mcp-targets";
import { PATH_SURFACES, surfaceFamilyOf } from "./path-surfaces";
import { classifyToolKind } from "./tool-kind";

/**
 * Build the {@link AccessIntent} an external policy query (the `Symbol.for()`
 * service and the event-bus RPC) feeds to the resolver from a `(surface, value)`
 * pair.
 *
 * For a path-shaped surface (`path`, `external_directory`, or a path-bearing
 * tool) carrying a non-empty value, it builds an `AccessPath` and emits an
 * `access-path` intent, so the resolver matches the lexical aliases ∪ canonical
 * (symlink-resolved) set — at parity with the gates (#486, #502); a built-in
 * tool surface resolves the value to the file that tool would open. An `mcp`
 * query carrying a value evaluates that value as-is: an MCP target is already
 * a candidate name, and rebuilding proxy input from it would derive only the
 * status probe `mcp_status`. Every other surface, and any value-less
 * surface-level query, keeps the `tool` intent so the manager's
 * `normalizeInput` `["*"]` fallback is preserved.
 */
export function buildAccessIntentForSurface(
  surface: string,
  value: string | undefined,
  normalizer: PathNormalizer,
  agentName: string | undefined,
): AccessIntent | PathValuesAccessIntent {
  const pathValue = getNonEmptyString(value);
  if (pathValue !== null && PATH_SURFACES.has(surface)) {
    return {
      kind: "access-path",
      surface,
      path: normalizer.forToolPath(surface, pathValue),
      agentName,
    };
  }
  if (pathValue !== null && surface === "mcp") {
    return { kind: "path-values", surface, values: [pathValue], agentName };
  }
  return {
    kind: "tool",
    surface,
    input: buildInputForSurface(surface, value),
    agentName,
  };
}

/**
 * Build a {@link ResolvedAccessIntent} directly from a forwarded request's
 * child-fixed match values (ADR 0008 §2), for the forwarded-serving wire
 * (#597).
 *
 * Unlike {@link buildAccessIntentForSurface}, this never touches a
 * `PathNormalizer` and never rebuilds an `AccessPath` — a path-shaped surface
 * or `mcp` gets a `path-values` intent carrying `matchValues` as-is (the values
 * the child already fixed), and every other surface gets a `tool` intent built
 * from its single portable value. An `mcp` request carries the child's target;
 * evaluating it alone can only be stricter than the child, since what the
 * serving node adds is session grants. `agentName` is always the requester's
 * `principal.agentName` (ADR 0008 §3, agent-scoped serving).
 */
export function buildResolvedIntentFromMatchValues(
  surface: string,
  matchValues: readonly string[],
  agentName: string,
): ResolvedAccessIntent {
  if (PATH_SURFACES.has(surface) || surface === "mcp") {
    return {
      kind: "path-values",
      surface,
      values: [...matchValues],
      agentName,
    };
  }
  return {
    kind: "tool",
    surface,
    input: buildInputForSurface(surface, matchValues[0]),
    agentName,
  };
}

/**
 * Construct a surface-appropriate input object from a raw value string for the
 * `tool`-intent branch of {@link buildAccessIntentForSurface} (the non-path
 * surfaces and value-less path queries).
 *
 * This is the inverse of `normalizeInput()` — it builds the minimal input
 * object that the manager expects for a given surface, from a single string
 * value.
 *
 * Note: MCP inputs are complex (server name + tool name derivation). Callers
 * providing an MCP surface receive a best-effort policy evaluation using the
 * value as a pre-qualified target string. Pass the fully-qualified target
 * (e.g. "exa:search" or "exa") directly.
 */
function buildInputForSurface(
  surface: string,
  value: string | undefined,
): unknown {
  const v = value ?? "";
  if (surface === "bash") return { command: v };
  if (surface === "skill") return { name: v };
  if (surfaceFamilyOf(surface) === "external_directory") return { path: v };
  // MCP and tool surfaces: normalizeInput handles them from the surface alone.
  return {};
}

/**
 * Surface-normalized representation of a tool invocation used by
 * `checkPermission()` to feed a single `evaluateAnyValue()` call.
 */
export interface NormalizedInput {
  /** The permission surface for `evaluate()` (e.g. "bash", "mcp", "skill"). */
  surface: string;
  /**
   * Alternative lookup names for this one access, most-specific first.
   * MCP targets and a bash command's spellings are the only sources of more
   * than one; every other surface produces a single-element array. Order does
   * not decide which rule wins (rule position does); it decides which name the
   * decision is reported under when the winning rule matches several.
   */
  values: string[];
  /**
   * Surface-specific fields forwarded verbatim into `PermissionCheckResult`
   * (e.g. `{ command }` for bash, `{ target }` for mcp).
   */
  resultExtras: Record<string, unknown>;
}

/**
 * Map a raw tool invocation to the surface/values/extras triple needed by
 * `checkPermission()`.
 *
 * Handles bash, skill, mcp, and extension surfaces. Path-bearing tool surfaces
 * (`path`, `external_directory`, `read`, `write`, `edit`, `grep`, `find`,
 * `ls`) now route through the access-path gate (#502) and service/RPC builder
 * (#503) before reaching the manager, so they never arrive here with a real
 * path value — all fall through to the extension catch-all `["*"]`.
 *
 * @param toolName - Normalized (trimmed) tool name from the tool-call event.
 * @param input    - Raw input payload from the tool-call event.
 * @param configuredMcpServerNames - Ordered list of MCP server names from
 *   Pi's `mcp.json` files, used to derive server-qualified MCP targets.
 */
export function normalizeInput(
  toolName: string,
  input: unknown,
  configuredMcpServerNames: readonly string[],
): NormalizedInput {
  switch (classifyToolKind(toolName)) {
    // --- Skill ---
    case "skill": {
      const record = toRecord(input);
      const skillName = record.name;
      const lookupValue = typeof skillName === "string" ? skillName : "*";
      return {
        surface: "skill",
        values: [lookupValue],
        resultExtras: {},
      };
    }

    // --- Bash ---
    case "bash": {
      const record = toRecord(input);
      const command = typeof record.command === "string" ? record.command : "";
      return normalizeBashCommand(command, []);
    }

    // --- MCP ---
    case "mcp":
      return normalizeMcpTargets(
        createMcpPermissionTargets(input, configuredMcpServerNames),
      );

    // A Pi MCP tool's identity is its name; its input is the MCP arguments.
    case "mcp-tool":
      return normalizeMcpTargets(
        createPiMcpToolTargets(toolName, configuredMcpServerNames),
      );

    // --- All other surfaces (path-bearing tools and extension tools) ---
    // Path-bearing tools with a present path never reach here — the gate emits
    // an access-path intent (#502). Missing-path and extension-tool cases both
    // collapse to the surface catch-all.
    case "path":
    case "extension":
      return {
        surface: toolName,
        values: ["*"],
        resultExtras: {},
      };
  }
}

/**
 * The `mcp` surface's normalized form: the derived candidates, then the
 * surface-wide `mcp` name, reported under the most specific candidate until a
 * rule match names the one it matched.
 */
function normalizeMcpTargets(targets: readonly string[]): NormalizedInput {
  const values = [...targets, "mcp"];
  return {
    surface: "mcp",
    values,
    resultExtras: { target: values[0] },
  };
}

/**
 * The `bash` surface's normalized form: the command as typed, then the other
 * spellings the shell runs identically, matched as aliases of one invocation.
 *
 * Leading shell comment lines are stripped from the typed command so pattern
 * matching operates on the actual command, not a `# description` prefix agents
 * often prepend; when stripping leaves nothing, an all-comment command still
 * evaluates against its literal text. A spelling comes from the program
 * analysis and is matched as given. `command` stays the typed text, because the
 * prompt, the decision value, and the session-approval suggestion read it.
 */
export function normalizeBashCommand(
  command: string,
  spellings: readonly string[],
): NormalizedInput {
  const matchValue = stripBashCommentLines(command) || command;
  return {
    surface: "bash",
    values: [...new Set([matchValue, ...spellings])],
    resultExtras: { command },
  };
}
