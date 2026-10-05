import type { TSNode } from "./parser";

/**
 * Whether `tree-sitter-bash` resolved the syntax it was given: the parse's own
 * health, as opposed to the structure of a successful parse.
 *
 * Kept apart from `parser.ts`, which says where a tree comes from, so that a
 * module the parser depends on (the redirect-argument correction reads a
 * redirect through `redirect-analysis.ts`) can still ask these questions
 * without an import cycle.
 */

/**
 * Whether tree-sitter failed to resolve the syntax at `node`.
 *
 * Error recovery disposes of text it cannot attach in one of two places, and
 * which one it picks depends on what follows. The read-write open `<>`, which
 * `tree-sitter-bash` 0.25.1 has no node for, shows both: `cat <> rw.txt` keeps
 * the discarded `>` as an `ERROR` *child* of the redirect, while
 * `cat <> ~/rw.txt` strands the `<` as an `ERROR` *sibling* ahead of a redirect
 * that is otherwise indistinguishable from a genuine `> ~/rw.txt`. A reader
 * that consults only the node's own subtree sees the first and not the second.
 *
 * The immediate predecessor, rather than the enclosing statement, is what makes
 * the answer per-redirect: in `cat a > out.txt <> ~/rw.txt` the statement has
 * an error but its first redirect is a fully resolved write, and condemning it
 * would forfeit a proof the parse really did establish.
 *
 * The question is about the parse, not about `<>`, so the population is wider
 * than the form that exposed it: `cat $(( > out.txt` and `echo ) > out.txt`
 * both carry a perfectly good `> out.txt` whose predecessor failed for an
 * unrelated reason, and both go unproven. That is the accepted cost, and it is
 * the same shape as the only real occurrence measured across 5000+ logged
 * commands — `git commit -F - <<'MSG' 2>&1 | tail -4`, valid bash the grammar
 * cannot parse (ADR 0013's 2026-08-29 amendment), where the demoted token
 * belongs to no `<>` either. Over-refusing costs a prompt; under-refusing hands
 * a write to a read grant.
 *
 * This module is the one place {@link TSNode.hasError} and
 * {@link TSNode.previousSibling} are read. Keeping the lateral navigation here
 * is deliberate: recovering-parser behavior is a fact about tree-sitter rather
 * than about any construct, so a caller asks this question instead of
 * hand-rolling a sibling walk of its own.
 */
export function parseUnresolvedAt(node: TSNode): boolean {
  return node.hasError || (node.previousSibling?.hasError ?? false);
}

/**
 * Whether tree-sitter failed to resolve the syntax anywhere within `node`.
 *
 * The subtree-only question, and the one a walker descending statements asks:
 * a statement holding an unresolved region is one whose recovered shape is
 * invented rather than observed, so nothing beneath it is evidence of what
 * runs. The failure can sit well below the statement that exposes it —
 * `git commit -F - <<'MSG' 2>&1 | tail -4` strands its `ERROR` under
 * `heredoc_redirect → file_redirect`, where no command node sees it.
 *
 * {@link parseUnresolvedAt} answers the redirect-shaped question instead,
 * widening to the immediate predecessor because error recovery strands a
 * discarded operator ahead of the redirect it belonged to. That widening is a
 * fact about redirects, not about statements: a statement whose *predecessor*
 * failed is not itself unparsed, and borrowing the wider predicate here would
 * condemn every statement following a failed one.
 *
 * `unresolved-salvage.ts` asks the same question twice over: to locate the
 * innermost region worth re-parsing, and to refuse the re-parse's own result
 * when it failed too (#875).
 */
export function parseUnresolvedWithin(node: TSNode): boolean {
  return node.hasError;
}
