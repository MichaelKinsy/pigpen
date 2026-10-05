import {
  childrenOf,
  lastNamedChild,
  rewriteStatements,
  rewrittenNode,
  type Source,
  sourceOf,
} from "./parse-view";
import type { TSNode } from "./parser";
import { LITERAL_NODE_TYPES, trailingArgumentIndex } from "./redirect-analysis";

/**
 * `tree-sitter-bash`'s parse with each word it hung on a redirect handed back
 * to the command it belongs to.
 *
 * The grammar (0.25.1) declares a file redirect's destination `repeat1`, so in
 * `git 2>/dev/null push --force` the words `push --force` parse as further
 * destinations of the statement's redirect, where bash passes them to `git`
 * (tree-sitter/tree-sitter-bash#233). Every consumer of the parse (the command
 * enumerator, the path walkers, the effect proofs, the log masker) would
 * otherwise have to learn that quirk on its own, so it is corrected once, here,
 * where the tree enters the package (#977).
 *
 * The corrected shape is the one the grammar already produces for a redirect
 * written before the command: the redirect becomes a child of the `command`,
 * between its words. Every redirect from the body up to the last one carrying
 * words moves into the command, each truncated after its own target and
 * followed by its words; a redirect after the last word stays at the
 * statement, and a statement left with no redirect gives way to its body.
 *
 * A heredoc carries words the same way (`git <<EOF push --force`), and they
 * move into the command after it; its body stays in the heredoc, which is
 * written after the words and so ends after them (#979). Every other tail a
 * heredoc can carry is `heredoc-tails.ts`'s to move.
 *
 * Three kinds of statement are left exactly as the grammar produced them:
 *
 * - One whose parse failed. Its units are floored already, and moving a word
 *   out of an unresolvable redirect would hand it the command's effect proof in
 *   place of the redirect's refusal to prove one (#814).
 * - One whose body is not a command (`{ a; } 2>/dev/null b`), which bash
 *   rejects as a syntax error, so nothing runs.
 * - One with no words after any redirect's target, which is almost every one.
 *
 * Returns `root` itself when nothing in the tree needs correcting. A corrected
 * node reads its offsets and text from the source, so `startIndex`/`endIndex`
 * stay positions in the command string every caller already slices.
 */
export function reattachRedirectArguments(root: TSNode): TSNode {
  return rewriteStatements(root, reattachStatement);
}

/**
 * Move the words the grammar hung on `statement`'s redirects into the command
 * they belong to, or `undefined` when there are none or no command to take
 * them. `children` are the statement's children, already corrected.
 */
function reattachStatement(
  statement: TSNode,
  children: readonly TSNode[],
): TSNode | undefined {
  const bodyIndex = children.findIndex((child) => child.isNamed);
  const body = children.at(bodyIndex);
  if (!body || !reachesCommand(body)) return undefined;

  const lastCarrier = children.findLastIndex(
    (child) =>
      WORD_CARRYING_TYPES.has(child.type) &&
      trailingArgumentIndex(child) !== undefined,
  );
  if (lastCarrier === -1) return undefined;

  const source = sourceOf(statement);
  const moved = children
    .slice(bodyIndex + 1, lastCarrier + 1)
    .flatMap((child) => splitRedirect(child, source));
  const newBody = appendToRightmostCommand(body, moved, source);
  const rest = children.slice(lastCarrier + 1);
  if (!rest.some((child) => child.isNamed)) return newBody;
  return rewrittenNode(
    statement,
    [...children.slice(0, bodyIndex), newBody, ...rest],
    statement.startIndex,
    statement.endIndex,
    source,
  );
}

/**
 * Whether the words after a redirect on `body` belong to a command: `body` is
 * one, or is a `list` or `pipeline` whose last element reaches one. The grammar
 * hangs a redirect on the last command of `cd a && git 2>/dev/null push` or
 * `rg x | xargs ls 2>&1 ~/x` off the whole list or pipeline, while bash gives
 * it, and its words, to that last command.
 */
function reachesCommand(body: TSNode): boolean {
  if (body.type === "command") return true;
  if (!GROUPING_TYPES.has(body.type)) return false;
  const last = lastNamedChild(body);
  return last !== undefined && reachesCommand(last);
}

/** The bodies whose last element a statement-level redirect belongs to. */
const GROUPING_TYPES: ReadonlySet<string> = new Set(["list", "pipeline"]);

/** The redirects the grammar can hang a command's words on. */
const WORD_CARRYING_TYPES: ReadonlySet<string> = new Set([
  "file_redirect",
  "heredoc_redirect",
]);

/**
 * `redirect` as it belongs in the command: without the words the grammar
 * appended to it, and followed by them. A redirect carrying no words, or a node
 * that cannot carry any, moves as it is.
 *
 * A file redirect's words are the rest of its children, so it is truncated
 * after its target. A heredoc keeps its body and delimiter, which follow the
 * words in the source, so it keeps its own end.
 */
function splitRedirect(redirect: TSNode, source: Source): TSNode[] {
  const trailing = WORD_CARRYING_TYPES.has(redirect.type)
    ? trailingArgumentIndex(redirect)
    : undefined;
  if (trailing === undefined) return [redirect];

  const kept: TSNode[] = [];
  const words: TSNode[] = [];
  for (let i = 0; i < redirect.childCount; i++) {
    const child = redirect.child(i);
    if (!child) continue;
    const isWord = i >= trailing && LITERAL_NODE_TYPES.has(child.type);
    (isWord ? words : kept).push(child);
  }
  const end = kept.at(-1)?.endIndex ?? redirect.startIndex;
  return [
    rewrittenNode(redirect, kept, redirect.startIndex, end, source),
    ...words,
  ];
}

/**
 * `body` with `moved` appended to its rightmost command: the command itself,
 * or the last element of a `list` or `pipeline`, recursively. Each node on the
 * way grows to the furthest end among the moved nodes, which is a heredoc's
 * when one moved, since its body is written after the words that follow it.
 */
function appendToRightmostCommand(
  body: TSNode,
  moved: readonly TSNode[],
  source: Source,
): TSNode {
  const children = childrenOf(body);
  const end = Math.max(body.endIndex, ...moved.map((node) => node.endIndex));
  if (body.type === "command") {
    return rewrittenNode(
      body,
      [...children, ...moved],
      body.startIndex,
      end,
      source,
    );
  }
  const lastIndex = children.findLastIndex((child) => child.isNamed);
  const last = children[lastIndex];
  const replaced = children.with(
    lastIndex,
    appendToRightmostCommand(last, moved, source),
  );
  return rewrittenNode(body, replaced, body.startIndex, end, source);
}
