import type { AccessPath } from "./access-path";

/**
 * Raw tool input the manager must normalize (path / bash / MCP / extension tools).
 *
 * The `surface` is the tool name fed to `normalizeInput` (e.g. `"read"`, `"bash"`,
 * an MCP server name).
 */
export interface ToolAccessIntent {
  kind: "tool";
  /** Tool name fed to input normalization. */
  surface: string;
  input: unknown;
  agentName?: string;
}

/**
 * Precomputed equivalent policy values for a surface, evaluated as-is.
 *
 * Not gate-emitted: the resolver produces it internally by unwrapping an
 * `access-path` intent via `matchValues()`, keeping the low-level manager
 * string-based (it never imports `AccessPath`). See {@link ResolvedAccessIntent}.
 * A forwarded or service `mcp` query also arrives in this form, carrying the
 * MCP target it names; the name predates that second use.
 *
 * This string seam is a deliberate, formalized boundary — not transitional
 * scaffolding to collapse into the manager (ADR-0002,
 * `docs/decisions/0002-path-values-string-boundary.md`).
 */
export interface PathValuesAccessIntent {
  kind: "path-values";
  /** A path-shaped surface (`path`, `external_directory`, a path tool) or `mcp`. */
  surface: string;
  values: readonly string[];
  agentName?: string;
}

/**
 * An `AccessPath` value object for a path-shaped surface.
 *
 * Built for every path-shaped surface: the cross-cutting `path` and
 * `external_directory` gates, the per-tool path-bearing surfaces
 * (`read`/`write`/`edit`/`grep`/`find`/`ls`, #502), and the service/RPC policy
 * queries for those surfaces (#503). Lets `AccessPath` flow into the resolver
 * as a first-class variant so the resolver — not the producer — asks it for
 * `matchValues()` (Tell-Don't-Ask).
 */
export interface AccessPathAccessIntent {
  kind: "access-path";
  surface: string;
  path: AccessPath;
  agentName?: string;
}

/** What a gate emits: a raw tool input, an `AccessPath`, or a bash command unit. */
export type AccessIntent =
  | ToolAccessIntent
  | AccessPathAccessIntent
  | BashCommandAccessIntent;

/**
 * One bash command unit, with the other spellings the shell runs identically.
 *
 * `command` is the unit as typed: the prompt, the decision value, and the
 * session-approval suggestion read it. `spellings` come from the program
 * analysis, the only party that knows what a spelling means in this program
 * (a `~` names the startup home only while the program leaves `HOME` alone),
 * and the manager evaluates them with `command` as aliases of one invocation:
 * the last rule matching any of them decides.
 *
 * String-only, so it stays on the manager's side of the ADR-0002 boundary.
 */
export interface BashCommandAccessIntent {
  kind: "bash-command";
  /**
   * Always `"bash"`. Typed `string` because the resolver re-surfaces every
   * intent across a surface family (`{ ...intent, surface }`); `bash` is never
   * a family, so that never happens to this one.
   */
  surface: string;
  command: string;
  spellings: readonly string[];
  agentName?: string;
}

/**
 * What the manager consumes — the `access-path` variant has already been
 * unwrapped to `path-values` by the resolver via `path.matchValues()`; a
 * `bash-command` intent passes through as emitted.
 *
 * The manager stays string-based and never imports `AccessPath`: this is the
 * deliberate boundary formalized in ADR-0002
 * (`docs/decisions/0002-path-values-string-boundary.md`), guarded by a
 * `no-restricted-imports` lint rule on `permission-manager.ts`.
 */
export type ResolvedAccessIntent =
  | ToolAccessIntent
  | PathValuesAccessIntent
  | BashCommandAccessIntent;
