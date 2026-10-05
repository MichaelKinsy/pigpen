import { type TokenEffect, UNPROVEN_EFFECT } from "#src/access-intent/effect";
import { redirectDestinationEffect } from "./command-effects";
import { parseUnresolvedAt } from "./parse-health";
import type { TSNode } from "./parser";

/**
 * What a redirect node in the parse tree proves.
 *
 * `command-effects.ts` owns the operator *table* — which spelling means read,
 * which means write — and this module owns reading a `file_redirect` node well
 * enough to consult it: finding the operator among the node's children,
 * telling a destination that names a file from one that names a descriptor,
 * and naming which destination is the redirect's own target.
 *
 * The split exists because two callers need different answers from the same
 * read, and — importantly — they need them under different burdens of proof.
 * The token collector asks what effect to *attribute* to a destination it is
 * about to emit, so it answers with a proof. The command enumerator asks
 * whether it is safe to *remove* the wrapper floor, so it answers with a
 * refusal: anything it cannot resolve counts against the exemption (#803).
 * One reader of the node keeps the two from drifting on what a redirect is.
 *
 * The two burdens meet at one fact: whether the parse resolved at all. Both
 * answers ask `parseUnresolvedAt`, so a syntax form the grammar could not
 * handle cannot be a proof to one caller and a resolvable read to the other
 * (#814).
 */

/**
 * The redirect node types a `command` or a statement can host.
 *
 * A redirect is not a word of the command it sits in, wherever it sits: bash
 * accepts one before, between, or after the words (`2>/dev/null git push`), and
 * none of them changes which command runs (#977).
 */
export const REDIRECT_NODE_TYPES: ReadonlySet<string> = new Set([
  "file_redirect",
  "herestring_redirect",
  "heredoc_redirect",
]);

/**
 * The effect `redirect` proves for `destination`, or `null` when the redirect
 * names no file and no token should be collected.
 *
 * `>&` and `<&` are the two operators that may name either a file descriptor
 * (`2>&1`) or a real file (`cmd >& out`); the destination node's type is the
 * parse-tree fact that tells them apart.
 *
 * A redirect the parse could not resolve proves nothing — ADR 0013 §10's base
 * case, which consults both directional surfaces. Reading a proof off whichever
 * operator survived error recovery made `cat <> rw.txt` a read and
 * `cat <> ~/rw.txt` a write, so one command's answer was a function of its
 * filename, and the read half was a fail-open on a destination the shell may
 * truncate (#814).
 *
 * The demotion applies to a *proof*, never to the `null`: a descriptor
 * duplication names no file whatever the operator around it did, so demoting
 * first would emit a descriptor number as a path candidate.
 */
export function redirectEffectForDestination(
  redirect: TSNode,
  destination: TSNode,
): TokenEffect | null {
  const proven = redirectDestinationEffect(
    redirectOperatorOf(redirect),
    DESCRIPTOR_NODE_TYPES.has(destination.type),
  );
  if (proven === null) return null;
  return parseUnresolvedAt(redirect) ? UNPROVEN_EFFECT : proven;
}

/**
 * True unless `redirect` provably only reads — the fail-closed question the
 * floor exemption asks.
 *
 * Deliberately **not** the negation of a write proof. A destination this module
 * cannot resolve counts as a write here, because the caller is deciding whether
 * to remove a guard: `> $OUT`, `>${OUT}`, and `> $(mktemp)` name a file chosen
 * at run time, and the parse can say nothing about which. Reading those as "no
 * write proved, therefore no write" would hand the exemption to exactly the
 * shapes least visible to every other surface — the path projection does not
 * collect them either (#609).
 *
 * An unresolved parse is refused up front rather than left to the loop. The
 * loop would usually reach the same answer — a demoted destination is unproven,
 * which is not a read — but `cat <>&1` parses to a redirect whose only children
 * are the operator and a descriptor, so the loop finds nothing to refuse on and
 * clears the exemption for a form nobody understood (#814).
 *
 * Past that, only two things clear it: a descriptor duplication (`2>&1`), which
 * names no file, and an operator that proves a read — reading a file alongside
 * a pure reader leaves it a pure reader.
 */
export function redirectMayWriteFile(redirect: TSNode): boolean {
  if (parseUnresolvedAt(redirect)) return true;
  for (let i = 0; i < redirect.childCount; i++) {
    const child = redirect.child(i);
    // The operator itself is the redirect's only unnamed child.
    if (!child?.isNamed) continue;
    // A source or duplicated descriptor (`2`, `&1`) names no file.
    if (DESCRIPTOR_NODE_TYPES.has(child.type)) continue;
    if (redirectEffectForDestination(redirect, child)?.effect !== "read") {
      return true;
    }
  }
  return false;
}

/**
 * The child index of the node `redirect` reads or writes (its first named
 * child after the operator), or `undefined` when it names none: nothing
 * follows the operator, or the operator closes a descriptor (`>&-`, `<&-`).
 *
 * The operator is the redirect's only unnamed child, and a source descriptor
 * (`2` in `2>`) precedes it, so the first named child after it is the
 * target without asking its type.
 *
 * Only the first: tree-sitter-bash 0.25.1 declares the destination
 * `repeat1`, so the words after it in `grep pat 2>/dev/null f.txt` parse as
 * further destinations, while bash passes them to the redirected command as
 * arguments ({@link trailingArgumentIndex} names where they begin, #977). A
 * close operator takes an optional destination in the grammar, but it closes a
 * descriptor and names no file, so a word after it is the command's too. An
 * index rather than a node, because a caller iterating the children compares
 * positions rather than wrapper identity.
 */
export function redirectTargetIndex(redirect: TSNode): number | undefined {
  const operator = redirectOperatorIndex(redirect);
  if (operator === undefined) return undefined;
  if (CLOSE_OPERATORS.has(redirect.child(operator)?.type ?? "")) {
    return undefined;
  }
  return namedChildIndexAfter(redirect, operator);
}

/**
 * The child index of the first word the grammar appended after `redirect`'s
 * own target, or `undefined` when none follows.
 *
 * Every {@link LITERAL_NODE_TYPES} child from there on is a word bash passes to
 * the redirected command rather than a destination of the redirect: `f.txt` in
 * `grep pat 2>/dev/null f.txt`, and `arg` in `cmd >&- arg`, where the close
 * operator has no target at all (#977).
 *
 * A heredoc carries them the same way: `git <<EOF push --force` hangs `push`
 * and `--force` after its delimiter, where its own target would be. Its body
 * follows them and is not a word, and neither is any other tail the grammar
 * lets a heredoc carry (a redirect, a `| …` or `&& …` statement), so the first
 * child after the target counts only when it is a literal (#979).
 */
export function trailingArgumentIndex(redirect: TSNode): number | undefined {
  const operator = redirectOperatorIndex(redirect);
  if (operator === undefined) return undefined;
  const target = redirectTargetIndex(redirect);
  const index = namedChildIndexAfter(redirect, target ?? operator);
  if (index === undefined) return undefined;
  return LITERAL_NODE_TYPES.has(redirect.child(index)?.type ?? "")
    ? index
    : undefined;
}

/**
 * The node types `tree-sitter-bash` 0.25.1 parses a literal word as: its
 * `_literal` rule, which a file redirect's destinations and a heredoc's
 * trailing words are both declared as.
 */
export const LITERAL_NODE_TYPES: ReadonlySet<string> = new Set([
  "concatenation",
  "word",
  "string",
  "raw_string",
  "translated_string",
  "ansi_c_string",
  "number",
  "expansion",
  "simple_expansion",
  "command_substitution",
  "process_substitution",
  "arithmetic_expansion",
  "brace_expression",
]);

/** Operators that close a descriptor, naming no file (`>&-`, `<&-`). */
const CLOSE_OPERATORS: ReadonlySet<string> = new Set([">&-", "<&-"]);

/** The index of `redirect`'s operator, its only unnamed child. */
function redirectOperatorIndex(redirect: TSNode): number | undefined {
  for (let i = 0; i < redirect.childCount; i++) {
    if (redirect.child(i)?.isNamed === false) return i;
  }
  return undefined;
}

/** The index of the first named child of `node` after index `after`. */
function namedChildIndexAfter(node: TSNode, after: number): number | undefined {
  for (let i = after + 1; i < node.childCount; i++) {
    if (node.child(i)?.isNamed) return i;
  }
  return undefined;
}

/**
 * Destination node types that name a file descriptor rather than a file, so
 * `>&` / `<&` duplicate a stream instead of touching the filesystem.
 *
 * Neither type is in {@link ARG_NODE_TYPES}, so `2>&1`'s `1` is already never
 * collected; the check is what keeps that true if the argument set widens.
 */
const DESCRIPTOR_NODE_TYPES: ReadonlySet<string> = new Set([
  "file_descriptor",
  "number",
]);

/**
 * The redirect operator of a redirect node.
 *
 * tree-sitter-bash emits it as an unnamed child whose `type` is the operator
 * text itself, and a redirect's only unnamed child is that operator — so the
 * syntax proof is a lookup on the first one found.
 */
function redirectOperatorOf(node: TSNode): string {
  const operator = redirectOperatorIndex(node);
  return operator === undefined ? "" : (node.child(operator)?.type ?? "");
}
