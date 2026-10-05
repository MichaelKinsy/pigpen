import { parseUnresolvedWithin } from "./parse-health";
import { childrenOf } from "./parse-view";
import type { TSNode } from "./parser";

/**
 * The heredoc-free spelling of each line whose heredoc sits in a statement the
 * primary parse could not resolve, in source order.
 *
 * `tree-sitter-bash` 0.25.1's `heredoc_redirect` accepts one tail form after
 * the delimiter, so valid bash such as `cat <<EOF ; rm -rf x` or
 * `cat <<EOF arg > /tmp/o` yields an `ERROR` directly under the redirect. The
 * innermost unresolved node is then the redirect itself, whose own text fails
 * to re-parse too, so the region salvage recovers nothing and a `deny` on the
 * command after the heredoc never fires. The same line with its heredoc
 * operators cut out (`cat ; rm -rf x`, `cat arg > /tmp/o`) is what bash runs
 * on that line, and it parses.
 *
 * A heredoc's host is its nearest `redirected_statement`, or, when no
 * statement holds it, the nearest enclosing `ERROR` (`cat 0<<EOF | …` lexes
 * `0<<EOF` as one delimiter inside a top-level `ERROR`). Only a host the parse
 * could not resolve is spelled, so a clean heredoc beside an unrelated failure
 * yields nothing. The spelling runs from the host statement's start, or the
 * heredoc's line start for an `ERROR` host, to the end of the heredoc's line;
 * the statement anchor is what recovers a heredoc inside a compound opened
 * earlier on its line (`if true; then cat <<EOF ; rm x`).
 *
 * Each cut removes an operator the scanner tokenized: the `<<` / `<<-` token,
 * a `file_descriptor` before it, the delimiter, and the blanks before them,
 * so the result reads as its heredoc-free spelling (`cat arg`, not `cat  arg`)
 * that a rule is written against. It only removes text, and the caller admits
 * a spelling only when it re-parses cleanly (`unresolved-salvage.ts`).
 */
export function heredocFreeLinesWithin(root: TSNode): string[] {
  const source = root.text;
  const offset = root.startIndex;
  const spans = new Map<number, HeredocLine>();
  for (const heredoc of unresolvedHeredocsWithin(root)) {
    const start =
      heredoc.host.type === "redirected_statement"
        ? heredoc.host.startIndex
        : lineStartOf(source, offset, heredoc.delimiter.startIndex);
    const end = lineEndOf(source, offset, heredoc.delimiter.endIndex);
    const line = spans.get(start) ?? { start, end, cuts: [] };
    line.cuts.push(operatorSpanOf(heredoc, source, offset, start));
    spans.set(start, line);
  }
  return [...spans.values()].map((line) => spell(line, source, offset));
}

interface UnresolvedHeredoc {
  /** The `heredoc_start` token. */
  readonly delimiter: TSNode;
  /** The delimiter's siblings, in order, so its operator can be found. */
  readonly siblings: readonly TSNode[];
  readonly index: number;
  readonly host: TSNode;
}

interface HeredocLine {
  readonly start: number;
  readonly end: number;
  readonly cuts: [number, number][];
}

function unresolvedHeredocsWithin(root: TSNode): UnresolvedHeredoc[] {
  const found: UnresolvedHeredoc[] = [];
  collectUnresolvedHeredocs(root, null, null, found);
  return found;
}

/**
 * Descends every node, `ERROR`s included: the `heredoc_start` token is the
 * scanner's rather than recovery's invention, and it can sit inside one.
 */
function collectUnresolvedHeredocs(
  node: TSNode,
  statement: TSNode | null,
  error: TSNode | null,
  found: UnresolvedHeredoc[],
): void {
  const siblings = childrenOf(node);
  siblings.forEach((child, index) => {
    const host = statement ?? error;
    if (
      child.type === "heredoc_start" &&
      host !== null &&
      parseUnresolvedWithin(host)
    ) {
      found.push({ delimiter: child, siblings, index, host });
    }
    collectUnresolvedHeredocs(
      child,
      child.type === "redirected_statement" ? child : statement,
      child.type === "ERROR" ? child : error,
      found,
    );
  });
}

/**
 * The span of a heredoc's operator: its delimiter, the `<<` / `<<-` token and
 * a `file_descriptor` before it, and the blanks before those, but never
 * reaching before the line's own start.
 */
function operatorSpanOf(
  heredoc: UnresolvedHeredoc,
  source: string,
  offset: number,
  lineStart: number,
): [number, number] {
  const { delimiter, siblings, index } = heredoc;
  let start = delimiter.startIndex;
  let before = index - 1;
  const operator = siblings.at(before);
  if (
    before >= 0 &&
    operator &&
    !operator.isNamed &&
    operator.text.startsWith("<<")
  ) {
    start = operator.startIndex;
    before -= 1;
    const descriptor = siblings.at(before);
    if (before >= 0 && descriptor?.type === "file_descriptor") {
      start = descriptor.startIndex;
    }
  }
  while (start > lineStart && isBlank(source[start - 1 - offset])) start -= 1;
  return [start, delimiter.endIndex];
}

function spell(line: HeredocLine, source: string, offset: number): string {
  let text = "";
  let at = line.start;
  for (const [cutStart, cutEnd] of line.cuts.toSorted(([a], [b]) => a - b)) {
    text += source.slice(at - offset, cutStart - offset);
    at = cutEnd;
  }
  return text + source.slice(at - offset, line.end - offset);
}

function lineStartOf(source: string, offset: number, index: number): number {
  return source.lastIndexOf("\n", index - offset - 1) + 1 + offset;
}

function lineEndOf(source: string, offset: number, index: number): number {
  const newline = source.indexOf("\n", index - offset);
  return (newline === -1 ? source.length : newline) + offset;
}

function isBlank(character: string | undefined): boolean {
  return character === " " || character === "\t";
}
