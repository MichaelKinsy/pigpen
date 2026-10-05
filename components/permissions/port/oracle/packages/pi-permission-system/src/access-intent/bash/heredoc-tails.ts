import {
  childrenOf,
  rewriteStatements,
  rewrittenNode,
  type Source,
  sourceOf,
} from "./parse-view";
import type { TSNode } from "./parser";

/**
 * `tree-sitter-bash`'s parse with the rest of each heredoc's line moved out of
 * the heredoc, to where the grammar puts it when the heredoc is written as a
 * plain input redirect.
 *
 * The grammar (0.25.1) lets a `heredoc_redirect` carry whatever follows its
 * delimiter on the same line: further redirects (`cat <<EOF > /tmp/o`), a
 * `| …` statement, or an `&& …` / `|| …` statement. They parse as children of
 * the heredoc, which every walker reads only for the substitutions it hosts, so
 * `cat <<EOF | rm -rf /tmp/x` would enumerate as `cat` alone and
 * `cat <<EOF > /tmp/o` would project no path (#979).
 *
 * The corrected tree is the grammar's own parse of the same line with `< in` in
 * place of `<<EOF`: a redirect becomes a sibling after the heredoc, and a
 * `| …` or `&& …` statement is joined to the redirected statement the way the
 * grammar joins it to `cat < in`. That reproduces the grammar's groupings
 * rather than bash's where the two differ (a redirect on a list's last command
 * hangs off the whole list), because every walker is already built to read
 * those groupings.
 *
 * Words after the delimiter (`git <<EOF push --force`) stay in the heredoc:
 * `reattachRedirectArguments` hands them to the command, exactly as it does for
 * the words after a file redirect's target. The heredoc keeps its body, which
 * is written after the rest of the line, so it ends after the nodes moved out
 * of it.
 *
 * Returns `root` itself when no heredoc carries such a tail.
 */
export function hoistHeredocTails(root: TSNode): TSNode {
  return rewriteStatements(root, hoistStatement);
}

/**
 * `statement` with its heredoc's tail moved out, or `undefined` when no
 * heredoc among `children` carries one.
 */
function hoistStatement(
  statement: TSNode,
  children: readonly TSNode[],
): TSNode | undefined {
  const heredocIndex = children.findIndex(
    (child) => child.type === "heredoc_redirect" && tailOf(child) !== undefined,
  );
  const heredoc = children.at(heredocIndex);
  const tail = heredoc && tailOf(heredoc);
  if (!tail) return undefined;

  const after = children.slice(heredocIndex + 1);
  // A statement tail runs to the end of the line, so nothing of the statement
  // can follow it; anything that does is a shape this correction does not know.
  if (tail.joined && after.some((child) => child.isNamed)) return undefined;

  const source = sourceOf(statement);
  const redirected = rewrittenNode(
    statement,
    [
      ...children.slice(0, heredocIndex),
      rewrittenNode(
        heredoc,
        tail.kept,
        heredoc.startIndex,
        heredoc.endIndex,
        source,
      ),
      ...tail.redirects,
      ...after,
    ],
    statement.startIndex,
    statement.endIndex,
    source,
  );
  if (!tail.joined) return redirected;
  return join(
    redirected,
    tail.joined.operator,
    withTrailingRedirectsOutermost(tail.joined.statement, source),
    source,
  );
}

/**
 * `statement` with a redirect written at its end hung off all of it, as the
 * grammar hangs it when the same text is not a heredoc's tail.
 *
 * At the top level `a | b | c > o` parses as `(a | b | c) > o`, but in a
 * heredoc's tail the grammar parses it as `a | ((b | c) > o)`. Left that way,
 * the joined write would not reach the heredoc's own command, so
 * `xargs grep foo <<EOF && a | b | c > o` would keep a floor exemption that
 * `xargs grep foo < in && a | b | c > o` withholds.
 */
function withTrailingRedirectsOutermost(
  statement: TSNode,
  source: Source,
): TSNode {
  if (statement.type === "redirected_statement") return statement;
  const detached = detachTrailingRedirects(statement, source);
  if (!detached) return statement;
  return rewrittenNode(
    { type: "redirected_statement", isNamed: true },
    [detached.body, ...detached.redirects],
    statement.startIndex,
    statement.endIndex,
    source,
  );
}

/**
 * `node` without the `redirected_statement` that ends it, plus that
 * statement's redirects, or `undefined` when `node` does not end in one.
 *
 * The walk follows the last element of each `list` and `pipeline`, and a
 * pipeline left holding a pipeline is flattened into one, as the grammar
 * parses `a | b | c`.
 */
function detachTrailingRedirects(
  node: TSNode,
  source: Source,
): { body: TSNode; redirects: TSNode[] } | undefined {
  const children = childrenOf(node);
  if (node.type === "redirected_statement") {
    const bodyIndex = children.findIndex((child) => child.isNamed);
    return {
      body: children[bodyIndex],
      redirects: children.slice(bodyIndex + 1),
    };
  }
  if (!GROUPING_TYPES.has(node.type)) return undefined;
  const lastIndex = children.findLastIndex((child) => child.isNamed);
  const inner = detachTrailingRedirects(children[lastIndex], source);
  if (!inner) return undefined;
  const replaced =
    node.type === "pipeline" && inner.body.type === "pipeline"
      ? [...children.slice(0, lastIndex), ...childrenOf(inner.body)]
      : children.with(lastIndex, inner.body);
  return {
    body: rewrittenNode(
      node,
      replaced,
      node.startIndex,
      inner.body.endIndex,
      source,
    ),
    redirects: inner.redirects,
  };
}

/** The groupings whose last element a trailing redirect is written after. */
const GROUPING_TYPES: ReadonlySet<string> = new Set(["list", "pipeline"]);

/** What a heredoc carries after its delimiter, split by where each part goes. */
interface HeredocTail {
  /** The heredoc's own children: operator, delimiter, any words, and body. */
  readonly kept: readonly TSNode[];
  /** Redirects written after the delimiter. */
  readonly redirects: readonly TSNode[];
  /** A `| …`, `|& …`, `&& …`, or `|| …` statement after them, if any. */
  readonly joined?: {
    readonly operator: TSNode;
    readonly statement: TSNode;
  };
}

/**
 * `heredoc`'s tail, or `undefined` when it carries neither a redirect nor a
 * statement after its delimiter.
 *
 * The grammar spells a `| …` tail as one `pipeline` child holding the operator
 * and the statement, and an `&& …` / `|| …` tail as the operator token and the
 * statement as two children of the heredoc itself. That statement can be a
 * pipeline too (`cat <<EOF && ls | rm x`), so a `pipeline` child is the `| …`
 * form only when no operator came before it.
 */
function tailOf(heredoc: TSNode): HeredocTail | undefined {
  const kept: TSNode[] = [];
  const redirects: TSNode[] = [];
  let operator: TSNode | undefined;
  let statement: TSNode | undefined;
  for (const child of childrenOf(heredoc)) {
    if (TAIL_REDIRECT_TYPES.has(child.type)) {
      redirects.push(child);
    } else if (child.type === "pipeline" && !operator) {
      const [pipe] = childrenOf(child);
      operator = pipe;
      statement = childrenOf(child).find((node) => node.isNamed);
    } else if (LIST_OPERATORS.has(child.type) && !child.isNamed) {
      operator = child;
    } else if (operator && !statement && child.isNamed) {
      statement = child;
    } else {
      kept.push(child);
    }
  }
  if (operator && !statement) return undefined;
  const joined = operator && statement ? { operator, statement } : undefined;
  if (redirects.length === 0 && !joined) return undefined;
  return joined ? { kept, redirects, joined } : { kept, redirects };
}

/** The redirects a heredoc can carry after its delimiter. */
const TAIL_REDIRECT_TYPES: ReadonlySet<string> = new Set([
  "file_redirect",
  "herestring_redirect",
]);

/** The operators that join an `&& …` / `|| …` tail. */
const LIST_OPERATORS: ReadonlySet<string> = new Set(["&&", "||"]);

/** The operators that join a `| …` tail. */
const PIPE_OPERATORS: ReadonlySet<string> = new Set(["|", "|&"]);

/**
 * `redirected` joined to `statement` by `operator`, grouped as the grammar
 * groups `cat < in <operator> <statement>`.
 *
 * The grammar hangs a redirect off everything before it, so a joined
 * `redirected_statement` keeps its redirects outermost. That holds inside a
 * pipeline too: `cat < in && a > o | b` parses as `(cat < in && a > o) | b`,
 * so a pipeline whose first stage is redirected is joined at that stage. A
 * `list` is left-associative and binds looser than a pipe, so the join reaches
 * its first element. A pipe tail onto any other pipeline extends it. Anything
 * else becomes the operator's own two-element node.
 *
 * Each rebuilt node spans from `redirected`'s start to the furthest end beneath
 * it, which is the heredoc's, since its body is written after the tail.
 */
function join(
  redirected: TSNode,
  operator: TSNode,
  statement: TSNode,
  source: Source,
): TSNode {
  const isPipe = PIPE_OPERATORS.has(operator.type);
  const children = childrenOf(statement);
  const end = Math.max(redirected.endIndex, statement.endIndex);
  const rebuild = (type: Pick<TSNode, "type" | "isNamed">, kids: TSNode[]) =>
    rewrittenNode(type, kids, redirected.startIndex, end, source);

  const first = children.findIndex((child) => child.isNamed);
  if (joinsAtFirstElement(statement, children[first])) {
    return rebuild(
      statement,
      children.with(first, join(redirected, operator, children[first], source)),
    );
  }
  if (isPipe && statement.type === "pipeline") {
    return rebuild(statement, [redirected, operator, ...children]);
  }
  return rebuild({ type: isPipe ? "pipeline" : "list", isNamed: true }, [
    redirected,
    operator,
    statement,
  ]);
}

/**
 * Whether the grammar joins what precedes `statement` to its first element
 * rather than to `statement` as a whole.
 */
function joinsAtFirstElement(
  statement: TSNode,
  first: TSNode | undefined,
): boolean {
  if (statement.type === "redirected_statement") return true;
  if (statement.type === "list") return true;
  return (
    statement.type === "pipeline" && first?.type === "redirected_statement"
  );
}
