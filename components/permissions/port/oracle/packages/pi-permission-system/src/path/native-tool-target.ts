import { homedir } from "node:os";

import type { PathFlavor } from "./path-flavor";

/**
 * The file one of Pi's built-in file tools (`read`, `write`, `edit`, `ls`,
 * `find`, `grep`) opens for a `path` argument.
 *
 * Pi resolves that argument through its own resolver before touching the disk
 * (`core/tools/path-utils.ts` `resolveToCwd`, plus `resolveReadPath` for
 * `read`), and the resolver rewrites some spellings. A gate that matched the
 * typed spelling would check one file while the tool opened another, so the
 * path gates evaluate this target instead.
 */
export interface NativeToolTarget {
  /** Absolute path the built-in tool opens. */
  readonly target: string;
  /**
   * True when Pi's resolver changed the spelling in a way the typed path does
   * not show: a Unicode space, a win32 drive mount, a `file://` URL, or a
   * `read` fallback. `@`, `~`, and cwd resolution are not rewrites.
   */
  readonly rewritten: boolean;
  /**
   * The normalized spelling before cwd resolution, when it is relative and no
   * read fallback fired, so a relative rule keeps matching it.
   */
  readonly relativeSpelling?: string;
}

/** What {@link resolveNativeToolTarget} needs besides the typed path. */
export interface NativeToolTargetOptions {
  cwd: string;
  flavor: PathFlavor;
  /** Whether the tool also tries `read`'s variant spellings. */
  readFallbacks: boolean;
  /** Existence probe, following symlinks like Pi's `access(F_OK)`. */
  exists: (absolutePath: string) => boolean;
}

/**
 * Mirror Pi's built-in tool path resolution, in Pi's order: Unicode spaces,
 * one leading `@`, the win32 drive mount, `~`, a `file://` URL, cwd
 * resolution, then (for `read`) the variant spellings Pi tries when the
 * resolved path does not exist.
 */
export function resolveNativeToolTarget(
  rawPath: string,
  options: NativeToolTargetOptions,
): NativeToolTarget {
  const { cwd, flavor } = options;
  const spelling = normalizeToolPathSpelling(rawPath, flavor);
  const resolved = flavor.impl.resolve(cwd, spelling.value);
  const fallback = options.readFallbacks
    ? findReadVariant(resolved, options.exists)
    : undefined;
  if (fallback !== undefined) {
    return { target: fallback, rewritten: true };
  }
  return {
    target: resolved,
    rewritten: spelling.rewritten,
    ...(flavor.impl.isAbsolute(spelling.value)
      ? {}
      : { relativeSpelling: spelling.value }),
  };
}

/** Pi's `normalizeUnicodeSpaces` class. */
const UNICODE_SPACES = /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g;

const NARROW_NO_BREAK_SPACE = "\u202F";

interface NormalizedSpelling {
  value: string;
  rewritten: boolean;
}

/** Pi's `normalizePath` with `normalizeUnicodeSpaces` and `stripAtPrefix`. */
function normalizeToolPathSpelling(
  rawPath: string,
  flavor: PathFlavor,
): NormalizedSpelling {
  const spaced = rawPath.replace(UNICODE_SPACES, " ");
  const unprefixed = spaced.startsWith("@") ? spaced.slice(1) : spaced;
  const driveMapped = flavor.toolShellPath(unprefixed);
  const rewritten = spaced !== rawPath || driveMapped !== unprefixed;

  const home = expandToolHome(driveMapped, flavor);
  if (home !== undefined) return { value: home, rewritten };

  if (driveMapped.startsWith("file://")) {
    try {
      return { value: flavor.fileUrlToPath(driveMapped), rewritten: true };
    } catch {
      // Pi's tool throws here and opens nothing; keep the spelling as typed.
    }
  }
  return { value: driveMapped, rewritten };
}

/**
 * Pi's tilde expansion: `~` alone, `~/`, and on win32 `~\`. Unlike
 * `expandHomePath`, it leaves `$HOME` and `${HOME}` literal.
 */
function expandToolHome(value: string, flavor: PathFlavor): string | undefined {
  if (value === "~") return homedir();
  const separators = flavor.hasPathSeparator("\\") ? ["~/", "~\\"] : ["~/"];
  if (separators.some((prefix) => value.startsWith(prefix))) {
    return flavor.impl.join(homedir(), value.slice(2));
  }
  return undefined;
}

/**
 * Pi's `resolveReadPath` fallbacks: the first existing of the macOS AM/PM,
 * NFD, curly-quote, and NFD + curly-quote variants, tried only when the
 * resolved path itself does not exist. `undefined` when none applies.
 */
function findReadVariant(
  resolved: string,
  exists: (absolutePath: string) => boolean,
): string | undefined {
  if (exists(resolved)) return undefined;
  const nfd = resolved.normalize("NFD");
  const variants = [
    resolved.replace(/ (AM|PM)\./gi, `${NARROW_NO_BREAK_SPACE}$1.`),
    nfd,
    toCurlyQuotes(resolved),
    toCurlyQuotes(nfd),
  ];
  return variants.find((variant) => variant !== resolved && exists(variant));
}

function toCurlyQuotes(value: string): string {
  return value.replace(/'/g, "\u2019");
}
