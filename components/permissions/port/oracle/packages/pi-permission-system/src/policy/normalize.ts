import { surfaceFamilyMembers } from "#src/access-intent/path-surfaces";
import {
  isPiMcpToolName,
  PI_MCP_TOOL_PREFIX,
} from "#src/access-intent/tool-kind";
import type { FlatPermissionConfig, PatternValue } from "#src/types";
import { isDenyWithReason, isPermissionState } from "#src/types";
import type { Rule, Ruleset } from "./rule";

/**
 * A surface's value in a flat permission config: a catch-all or a pattern map.
 * `NonNullable` because the four named directional properties are optional.
 */
type SurfaceValue = NonNullable<FlatPermissionConfig[string]>;

/** A surface's pattern → action map. */
type PatternMap = Record<string, PatternValue>;

/**
 * Rewrite one scope's flat permission object so the bare `path` and
 * `external_directory` sugar keys are replaced by their directional members
 * (ADR 0013 §4).
 *
 * After expansion **no rule lives on a bare family surface at all** — that is
 * what makes `PermissionResolver`'s family fold the read path.
 *
 * The intra-surface merge order is normative: sugar-derived entries come first
 * and explicit directional entries append after them, whatever the keys'
 * textual order in the file, so a config and its key-order-swapped twin mean
 * the same thing. A pattern the explicit entry redefines is emitted once, at
 * the explicit entry's position, so last-match-wins gives it the final say.
 *
 * Called per scope at load, before composition — so origins stay attributed to
 * the authoring scope rather than collapsing to `builtin`.
 */
export function expandDirectionalSugar(
  permission: FlatPermissionConfig,
): FlatPermissionConfig {
  const expanded: FlatPermissionConfig = {};
  for (const [surface, value] of Object.entries(permission)) {
    // A key present with an explicit `undefined` value carries no rules.
    // `Object.entries` resolves to the catchall's non-optional value type, so
    // the type cannot see this case even though a named optional surface
    // admits it; dropping the guard expands `{ path: undefined }` into two
    // empty directional surfaces. Pinned in `normalize.test.ts`.
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime-reachable; see above
    if (value === undefined) continue;
    const members = surfaceFamilyMembers(surface);
    if (members === null) {
      // A directional key the sugar already absorbed keeps the merged value.
      if (!Object.hasOwn(expanded, surface)) expanded[surface] = value;
      continue;
    }
    for (const member of members) {
      expanded[member] = appendExplicitEntries(value, permission[member]);
    }
  }
  return expanded;
}

/** The sugar-derived entries, followed by the explicit directional entries. */
function appendExplicitEntries(
  sugar: SurfaceValue,
  explicit: SurfaceValue | undefined,
): SurfaceValue {
  if (explicit === undefined) {
    return typeof sugar === "string" ? sugar : { ...sugar };
  }
  const explicitPatterns = toPatternMap(explicit);
  const sugarPatterns = Object.fromEntries(
    Object.entries(toPatternMap(sugar)).filter(
      ([pattern]) => !Object.hasOwn(explicitPatterns, pattern),
    ),
  );
  return { ...sugarPatterns, ...explicitPatterns };
}

/** A catch-all string is shorthand for `{ "*": action }` (see `normalizeFlatConfig`). */
function toPatternMap(value: SurfaceValue): PatternMap {
  return typeof value === "string" ? { "*": value } : value;
}

/**
 * Convert a flat permission config into a Ruleset.
 *
 * Each key is a surface name. A string value is shorthand for
 * `{ "*": action }`. An object value maps patterns to actions.
 * A pattern value may be a PermissionState string or a `DenyWithReason`
 * object (`{ action: "deny", reason?: string }`).
 * Invalid action values are silently skipped.
 *
 * The universal fallback key `"*"` is included if present — callers
 * that use `"*"` only for `synthesizeDefaults()` should strip it before
 * calling this function.
 */
export function normalizeFlatConfig(permission: FlatPermissionConfig): Ruleset {
  const rules: Rule[] = [];
  for (const [surface, value] of Object.entries(permission)) {
    if (typeof value === "string") {
      if (isPermissionState(value)) {
        rules.push({ surface, pattern: "*", action: value, origin: "builtin" });
      }
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- defensive null check; value type does not include null but runtime JSON may
    } else if (typeof value === "object" && value !== null) {
      for (const [pattern, action] of Object.entries(value)) {
        if (isDenyWithReason(action)) {
          rules.push({
            surface,
            pattern,
            action: "deny",
            reason: action.reason,
            origin: "builtin",
          });
        } else if (isPermissionState(action)) {
          rules.push({ surface, pattern, action, origin: "builtin" });
        }
      }
    }
  }
  return rules;
}

/** The rules with relocated copies appended, and the keys that were copied. */
export interface McpToolKeyRelocation {
  rules: Ruleset;
  /** Each relocated surface key, in rule order. */
  relocatedKeys: string[];
}

/**
 * Copy the catch-all rule of every top-level key that can name a Pi MCP tool
 * onto the `mcp` surface, with the key as its pattern.
 *
 * A Pi MCP tool (`mcp__<server>__<tool>`) once resolved on its own name, so a
 * top-level key naming it (or a surface wildcard such as `mcp__srv__*`) was the
 * only rule that reached it. The tool now resolves on `mcp`, where its full
 * name is one of its candidates, so the same key is the same rule there.
 *
 * The copies are appended after every other rule, in their original order:
 * the key was the only rule that applied to the tool before, so it keeps the
 * final say over the `mcp` rules that newly reach it. The original rule stays
 * where it was, because a key such as `mcp__*` also matches tools that still
 * resolve on their own name (`mcp__foo`), and moving it would drop them. A key
 * that can name no Pi MCP tool (`mcp__foo`) is not copied, and a non-`*`
 * pattern under any key never matched (a tool surface is evaluated with `*`).
 */
export function relocateMcpToolKeyRules(rules: Ruleset): McpToolKeyRelocation {
  const relocated = rules
    .filter((rule) => rule.pattern === "*" && canNamePiMcpTool(rule.surface))
    .map((rule): Rule => ({ ...rule, surface: "mcp", pattern: rule.surface }));
  return {
    rules: [...rules, ...relocated],
    relocatedKeys: relocated.map((rule) => rule.pattern),
  };
}

/**
 * Whether a top-level surface key can match a Pi MCP tool name: the name
 * itself, or an `mcp__`-prefixed wildcard.
 */
function canNamePiMcpTool(surfaceKey: string): boolean {
  if (isPiMcpToolName(surfaceKey)) return true;
  return surfaceKey.startsWith(PI_MCP_TOOL_PREFIX) && /[*?]/.test(surfaceKey);
}
