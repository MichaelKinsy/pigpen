import { getGrammarParser, type TSNode } from "#src/access-intent/bash/parser";

/**
 * Parse `command` with the grammar's own parser and hand the corrected root,
 * alongside the grammar's, to `read`.
 *
 * The grammar's parser, not `getParser()`: the subject is what a correction
 * does to `tree-sitter-bash`'s real output, so the input must be that output.
 */
export async function withCorrected<T>(
  command: string,
  correct: (root: TSNode) => TSNode,
  read: (corrected: TSNode, grammar: TSNode) => T,
): Promise<T> {
  const parser = await getGrammarParser();
  const tree = parser.parse(command);
  if (!tree) throw new Error("parser.parse returned null");
  try {
    // Read once: web-tree-sitter builds a new wrapper on every access.
    const root = tree.rootNode;
    return read(correct(root), root);
  } finally {
    tree.delete();
  }
}

/**
 * A node's named structure as an S-expression: an inner node renders as
 * `(type child…)`, and a node with no named children as its text in quotes.
 */
export function shape(node: TSNode): string {
  const named: TSNode[] = [];
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child?.isNamed) named.push(child);
  }
  if (named.length === 0) return JSON.stringify(node.text);
  return `(${node.type} ${named.map(shape).join(" ")})`;
}

/** Every node of a tree, depth-first. */
function allNodes(node: TSNode, out: TSNode[] = []): TSNode[] {
  out.push(node);
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child) allNodes(child, out);
  }
  return out;
}

/** A node's range and type, which identify it across a real node and a view. */
function spanOf(node: TSNode | null): string | null {
  return node ? `${node.type}@${node.startIndex}-${node.endIndex}` : null;
}

/**
 * Every way `corrected` breaks the contract a corrected parse tree keeps with
 * the grammar's tree it was built from, or an empty list when it keeps it.
 *
 * The contract is what lets every walker read a corrected tree as if the
 * grammar had produced it:
 *
 * - Each node's text is its slice of the source.
 * - Each node's children are in source order and do not overlap, except that a
 *   child holding a heredoc may enclose the siblings after it: the heredoc's
 *   body is written after the rest of its line.
 * - Each child lies within its parent's range.
 * - Every leaf of the grammar's tree appears exactly once, so nothing is dropped
 *   and nothing is walked twice.
 * - Each child's `previousSibling` is the child before it.
 * - No node reports a parse error.
 */
export function viewContractViolations(
  source: string,
  corrected: TSNode,
  grammar: TSNode,
): string[] {
  const violations: string[] = [];
  for (const node of allNodes(corrected)) {
    if (node.text !== source.slice(node.startIndex, node.endIndex)) {
      violations.push(`${spanOf(node)}: text is not its source slice`);
    }
    if (node.hasError) violations.push(`${spanOf(node)}: reports an error`);
    for (let i = 0; i < node.childCount; i++) {
      const child = node.child(i);
      if (!child) continue;
      const before = i === 0 ? null : node.child(i - 1);
      if (before && !followsInSource(before, child)) {
        violations.push(`${spanOf(child)}: overlaps ${spanOf(before)}`);
      }
      if (
        child.startIndex < node.startIndex ||
        child.endIndex > node.endIndex
      ) {
        violations.push(`${spanOf(child)}: outside ${spanOf(node)}`);
      }
      if (spanOf(child.previousSibling) !== spanOf(before)) {
        violations.push(
          `${spanOf(child)}: previous sibling is ${spanOf(child.previousSibling)}, not ${spanOf(before)}`,
        );
      }
    }
  }
  const expected = leavesOf(grammar);
  const actual = leavesOf(corrected);
  if (actual.join(" ") !== expected.join(" ")) {
    violations.push(
      `leaves differ: expected ${expected.join(" ")}, got ${actual.join(" ")}`,
    );
  }
  return violations;
}

/**
 * Whether `after` is where a sibling following `before` belongs: past its end,
 * or, when `before` holds a heredoc, anywhere past its start.
 */
function followsInSource(before: TSNode, after: TSNode): boolean {
  if (after.startIndex >= before.endIndex) return true;
  const holdsHeredoc = allNodes(before).some(
    (node) => node.type === "heredoc_redirect",
  );
  return holdsHeredoc && after.startIndex >= before.startIndex;
}

/** The leaves of a tree, identified by span and sorted, duplicates kept. */
function leavesOf(root: TSNode): string[] {
  return allNodes(root)
    .filter((node) => node.childCount === 0)
    .map((node) => spanOf(node) ?? "")
    .toSorted();
}
