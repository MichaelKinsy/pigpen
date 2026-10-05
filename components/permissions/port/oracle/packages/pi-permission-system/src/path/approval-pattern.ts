import type { PathFlavor } from "./path-flavor";

/**
 * Derive the wildcard globs to record when a user approves an accessed path
 * for the session.
 *
 * A non-directory (or a path the caller could not confirm is a directory) is
 * scoped to its enclosing directory: the value up to and including its last
 * path separator, with `*` appended. A directory is scoped to itself and its
 * contents — `D` and `D<sep>*` — rather than to its parent, which would grant
 * every sibling directory (#989). Two patterns are needed because `D<sep>*`
 * does not match `D` itself, and `D*` would also match a sibling `D-evil`.
 *
 * Each pattern is spelled with the separator the value itself carries. That
 * matters on a win32 host, where Git Bash tokens are POSIX-shaped while Node's
 * own `sep` is a backslash: deriving `/tmp/logs\*` from `/tmp/logs/` widens the
 * grant to the parent directory once the `windowsSeparators` fold (#653)
 * normalizes both operands. A value carrying no separator falls back to the
 * current directory, which is what callers see only if they skipped resolving
 * the path to its absolute form first (#438).
 *
 * The platform's separator alphabet arrives as an injected {@link PathFlavor},
 * never an ambient `node:path` read, so win32 derivation is decidable — and
 * testable — on a POSIX host (#655).
 */
export function deriveApprovalPatterns(
  pathValue: string,
  flavor: PathFlavor,
  isDirectory: boolean,
): readonly string[] {
  if (!isDirectory) return [parentScopePattern(pathValue, flavor)];
  return [pathValue, directoryContentsPattern(pathValue, flavor)];
}

/** The value's enclosing directory scope (up to its last separator) plus `*`. */
function parentScopePattern(pathValue: string, flavor: PathFlavor): string {
  const lastSeparator = flavor.lastSeparatorIndex(pathValue);
  if (lastSeparator < 0) return `.${flavor.impl.sep}*`;
  return `${pathValue.slice(0, lastSeparator + 1)}*`;
}

/** Everything beneath a directory value: the value, a separator, then `*`. */
function directoryContentsPattern(
  pathValue: string,
  flavor: PathFlavor,
): string {
  const lastSeparator = flavor.lastSeparatorIndex(pathValue);
  if (lastSeparator === pathValue.length - 1) return `${pathValue}*`;
  const separator =
    lastSeparator < 0 ? flavor.impl.sep : pathValue[lastSeparator];
  return `${pathValue}${separator}*`;
}
