import { parseUnresolvedWithin } from "./parse-health";
import type { TSNode } from "./parser";

/**
 * A view of `tree-sitter-bash`'s parse tree that a correction pass can rewrite.
 *
 * The grammar hangs command-line material on the wrong node in more than one
 * production (#977, #979), and each correction is a pass that rewrites the
 * `redirected_statement`s it recognizes. Each pass builds its nodes from the
 * primitives here, so a later pass reads an earlier one's output as the same
 * kind of view and can re-parent its nodes in turn.
 *
 * A view reads its text from the source by absolute offsets, so every caller's
 * `startIndex`/`endIndex` slicing still holds.
 */

/**
 * `root` with each `redirected_statement` whose parse resolved replaced by what
 * `rewrite` returns for it, or `root` itself when nothing beneath it changed.
 *
 * The walk is bottom-up: `rewrite` receives the statement's children already
 * rewritten, and returns `undefined` to leave the statement as it is.
 *
 * A statement whose parse failed is never handed to `rewrite`. Its units are
 * floored already, and moving a node out of an unresolvable region would hand it
 * a proof in place of that region's refusal to prove one (#814).
 */
export function rewriteStatements(
  root: TSNode,
  rewrite: (
    statement: TSNode,
    children: readonly TSNode[],
  ) => TSNode | undefined,
): TSNode {
  return rewriteBeneath(root, rewrite) ?? root;
}

/** The rewritten node, or `undefined` when nothing beneath `node` changed. */
function rewriteBeneath(
  node: TSNode,
  rewrite: (
    statement: TSNode,
    children: readonly TSNode[],
  ) => TSNode | undefined,
): TSNode | undefined {
  const children: TSNode[] = [];
  let changed = false;
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (!child) continue;
    const rewritten = rewriteBeneath(child, rewrite);
    if (rewritten) changed = true;
    children.push(rewritten ?? child);
  }

  if (node.type === "redirected_statement" && !parseUnresolvedWithin(node)) {
    const rewritten = rewrite(node, children);
    if (rewritten) return rewritten;
  }
  return changed
    ? adoptingView(node, children, parseUnresolvedWithin(node))
    : undefined;
}

/** A node a correction built, reading its text from the statement's source. */
export function rewrittenNode(
  original: Pick<TSNode, "type" | "isNamed">,
  children: readonly TSNode[],
  startIndex: number,
  endIndex: number,
  source: Source,
): TSNode {
  return adoptingView(
    {
      type: original.type,
      isNamed: original.isNamed,
      startIndex,
      endIndex,
      text: source(startIndex, endIndex),
    },
    children,
    false,
  );
}

/** A slice of the command by absolute offsets. */
export type Source = (startIndex: number, endIndex: number) => string;

/** The source slicer for every node within `statement`. */
export function sourceOf(statement: TSNode): Source {
  return (startIndex, endIndex) =>
    statement.text.slice(
      startIndex - statement.startIndex,
      endIndex - statement.startIndex,
    );
}

export function childrenOf(node: TSNode): TSNode[] {
  const children: TSNode[] = [];
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child) children.push(child);
  }
  return children;
}

export function lastNamedChild(node: TSNode): TSNode | undefined {
  return childrenOf(node).findLast((child) => child.isNamed);
}

/**
 * A view whose children are views too, so each one's `previousSibling` is its
 * neighbor in the rewritten tree rather than the one the grammar gave it.
 */
function adoptingView(
  fields: NodeFields,
  children: readonly TSNode[],
  hasError: boolean,
): TSNode {
  return new NodeView(fields, children.map(asView), hasError);
}

/**
 * `node` as a view, so a new parent can give it a new previous sibling.
 *
 * Its own children stay the grammar's nodes: nothing beneath it moved, so their
 * siblings are still the grammar's too.
 */
function asView(node: TSNode): TSNode {
  return node instanceof NodeView
    ? node
    : new NodeView(node, childrenOf(node), parseUnresolvedWithin(node));
}

/** The fields a {@link NodeView} copies from the node it stands for. */
interface NodeFields {
  readonly type: string;
  readonly isNamed: boolean;
  readonly startIndex: number;
  readonly endIndex: number;
  readonly text: string;
}

/**
 * A parse-tree node a correction presents in place of the grammar's.
 *
 * Adopting its children sets each view child's `previousSibling` to the child
 * before it, which is what lets a redirect moved into a command ask
 * `parseUnresolvedAt` about its new neighbor.
 */
class NodeView implements TSNode {
  readonly type: string;
  readonly isNamed: boolean;
  readonly startIndex: number;
  readonly endIndex: number;
  readonly text: string;
  readonly childCount: number;
  previousSibling: TSNode | null = null;

  constructor(
    fields: NodeFields,
    private readonly children: readonly TSNode[],
    readonly hasError: boolean,
  ) {
    this.type = fields.type;
    this.isNamed = fields.isNamed;
    this.startIndex = fields.startIndex;
    this.endIndex = fields.endIndex;
    this.text = fields.text;
    this.childCount = children.length;
    children.forEach((child, i) => {
      if (child instanceof NodeView)
        child.previousSibling = children[i - 1] ?? null;
    });
  }

  child(index: number): TSNode | null {
    return this.children[index] ?? null;
  }
}
