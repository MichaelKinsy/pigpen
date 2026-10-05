import type { TSNode } from "./parser";
import type { ShellVariables } from "./shell-variable-expansion";

/**
 * Node types whose text content is never a command argument, so no path
 * candidate is ever read from it.
 *
 * This governs the subtree's *text*, not whether it is visited at all: an
 * interpolating `heredoc_body` is also an execution host, so it is still
 * descended for the commands it runs while its prose stays out of the path
 * surface (#741). See `EXECUTION_HOST_TYPES` in `nested-execution.ts`.
 */
export const SKIP_SUBTREE_TYPES = new Set([
  "heredoc_body",
  "heredoc_end",
  "comment",
]);

/**
 * Node types that represent argument values in the AST
 * (word, concatenation, single-quoted string, double-quoted string).
 */
export const ARG_NODE_TYPES = new Set([
  "word",
  "concatenation",
  "string",
  "raw_string",
]);

/**
 * One argument as the program receives it, and whether that is knowable.
 *
 * A capability proof must read the value after quote removal, since `'-o'` and
 * `-o` reach `sort` identically, and must know when the value is computed,
 * since a word only the shell decides can spell any option at all.
 */
export interface ArgWord {
  /** The string the shell passes after quote removal ({@link resolveText}). */
  readonly value: string;
  /**
   * Whether `value` may differ from what the program receives: a part only
   * running the command decides ({@link computedPart}), or a spelling the
   * shell rewrites before the program sees it — an escape, a glob, a brace
   * expansion, an ANSI-C string — which {@link resolveText} passes through as written.
   */
  readonly computed: boolean;
  /**
   * Whether the program may receive this argument, or a word split from it,
   * beginning with `-` — the shape every option has. Exact for a word that is
   * not computed; for a computed one, `false` only when a literal leading
   * character survives every rewrite the shell applies.
   */
  readonly mayLeadWithDash: boolean;
}

/**
 * Reads argument nodes as one program's shell expands them: the node-text
 * reads bound to the {@link ShellVariables} that program rebinds.
 */
export class WordReader {
  constructor(private readonly variables: ShellVariables) {}

  /** Read an argument node into the word the program receives. */
  argWord(node: TSNode): ArgWord {
    const value = this.text(node);
    const tilde = this.variables.readTilde(leadingUnquotedText(node));
    if (tilde?.known === false) {
      return { value, computed: true, mayLeadWithDash: true };
    }
    const computed =
      this.isComputed(node) || !isSpelledExactly(node, this.variables);
    if (tilde) {
      // The spelling `~` is not what the program receives; it is exact enough
      // to prove with only while the home it stands for cannot be an option.
      return {
        value,
        computed: computed || tilde.leadsWithDash,
        mayLeadWithDash:
          tilde.leadsWithDash || (computed && maySplitIntoWords(node, false)),
      };
    }
    return {
      value,
      computed,
      mayLeadWithDash: computed
        ? mayExpandToDashWord(node)
        : value.startsWith("-"),
    };
  }

  /** The string the shell passes after quote removal ({@link resolveText}). */
  text(node: TSNode): string {
    return resolveText(node, this.variables);
  }

  /** Whether the node's value is decided at run time ({@link computedPart}). */
  isComputed(node: TSNode): boolean {
    return computedPart(node, this.variables);
  }

  /** Whether a collected token is spelled from a rebound `HOME` ({@link ShellVariables.spellsReboundHome}). */
  spellsReboundHome(token: string): boolean {
    return this.variables.spellsReboundHome(token);
  }

  /** A command unit's text with its leading home prefix spelled out ({@link ShellVariables.spellHomeAtStart}). */
  spellHomeAtStart(text: string): string | undefined {
    return this.variables.spellHomeAtStart(text);
  }
}

/**
 * The unquoted literal an argument opens with, where bash expands a tilde
 * prefix: the word itself, or a concatenation's first part when that is a
 * word. A tilde after any other part, or inside quotes, stays literal.
 */
function leadingUnquotedText(node: TSNode): string {
  if (node.type === "word") return node.text;
  if (node.type !== "concatenation") return "";
  const first = node.child(0);
  return first?.type === "word" ? first.text : "";
}

/**
 * Whether a computed word may reach the program — whole, or as one of the
 * words the shell splits it into — beginning with `-`.
 *
 * Globbing, brace expansion, and escape removal each keep a literal prefix, so
 * a word whose leading character is a literal other than `-` cannot become an
 * option; an expansion that can split into several words decides nothing,
 * since only the first of them carries the prefix.
 */
function mayExpandToDashWord(node: TSNode): boolean {
  return (
    maySplitIntoWords(node, false) || (leadingCharacterMayBeDash(node) ?? true)
  );
}

/** Expansions whose unquoted result the shell splits into words. */
const WORD_SPLITTING_TYPES: ReadonlySet<string> = new Set([
  "simple_expansion",
  "expansion",
  "command_substitution",
  "arithmetic_expansion",
]);

/**
 * Nodes whose inside is never split into the argument's words: a
 * single-quoted or ANSI-C string, and a process substitution, whose body
 * belongs to its own command.
 */
const UNSPLIT_TYPES: ReadonlySet<string> = new Set([
  "raw_string",
  "ansi_c_string",
  "process_substitution",
]);

/**
 * Whether any expansion in the word may produce more than one word.
 *
 * Unquoted, every expansion splits. Inside double quotes a command
 * substitution or arithmetic expansion is one word, but a parameter expansion
 * may not be: `$@` and `${arr[@]}` expand to one word per element, an indirect
 * `${!name}` may name either, and any variable may be a nameref
 * (`declare -n s='arr[@]'`), with nothing in its own spelling to show it. So
 * every quoted parameter expansion counts, over-counting a joined `$*`.
 */
function maySplitIntoWords(node: TSNode, quoted: boolean): boolean {
  if (WORD_SPLITTING_TYPES.has(node.type)) {
    return !quoted || PARAMETER_EXPANSION_TYPES.has(node.type);
  }
  if (UNSPLIT_TYPES.has(node.type)) return false;
  const childrenQuoted = quoted || node.type === "string";
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child && maySplitIntoWords(child, childrenQuoted)) return true;
  }
  return false;
}

/** Parameter expansions, which may yield several words even when quoted. */
const PARAMETER_EXPANSION_TYPES: ReadonlySet<string> = new Set([
  "simple_expansion",
  "expansion",
]);

/**
 * Whether the first character the word produces may be `-`, or `undefined`
 * while every part read so far produced nothing (`""`, `''`).
 *
 * A part this does not know is non-literal, and so may lead with anything.
 */
function leadingCharacterMayBeDash(node: TSNode): boolean | undefined {
  switch (node.type) {
    case "word":
    case "number":
      return unquotedLiteralMayLeadWithDash(node.text);
    case "raw_string":
      return quotedLiteralMayLeadWithDash(node.text.slice(1, -1));
    case "string_content":
      return quotedLiteralMayLeadWithDash(node.text);
    case '"':
      return undefined;
    case "process_substitution":
      // The program receives a `/dev/fd/N` path.
      return false;
    case "string":
    case "concatenation":
      for (let i = 0; i < node.childCount; i++) {
        const child = node.child(i);
        const lead = child ? leadingCharacterMayBeDash(child) : undefined;
        if (lead !== undefined) return lead;
      }
      return undefined;
    default:
      return true;
  }
}

/**
 * An unquoted literal: a leading glob or brace can produce any character, and
 * an escape yields the character after it.
 */
function unquotedLiteralMayLeadWithDash(text: string): boolean | undefined {
  if (text === "") return undefined;
  if (text.startsWith("\\")) return text.length < 2 || text[1] === "-";
  return UNDETERMINED_LEAD.test(text);
}

/** A leading character that may become, or already is, `-`. */
const UNDETERMINED_LEAD = /^[-*?[{]/;

/** A quoted literal reaches the program as written. */
function quotedLiteralMayLeadWithDash(text: string): boolean | undefined {
  if (text === "") return undefined;
  return text.startsWith("-");
}

/**
 * Whether {@link resolveText} returns exactly the string the shell passes.
 *
 * Answers `false` for any node type it does not know, which is the
 * fail-closed direction for a caller proving what a word cannot be.
 */
function isSpelledExactly(node: TSNode, variables: ShellVariables): boolean {
  switch (node.type) {
    case "raw_string":
      return true;
    case "word":
      return !SHELL_REWRITTEN_CHARACTERS.test(node.text);
    case "string_content":
      return !node.text.includes("\\");
    case "simple_expansion":
    case "expansion":
      return variables.resolveReference(node) !== null;
    case "string":
    // A digit run is an ordinary word to the shell; the grammar's `10#$x` form
    // carries an expansion child, which answers for itself.
    case "number":
      return childrenSpelledExactly(node, variables);
    case "concatenation":
      // The grammar splits `{-i,-n}` into plain words, so the expansion is
      // visible only across the whole concatenation's text.
      return (
        !BRACE_EXPANSION.test(node.text) &&
        childrenSpelledExactly(node, variables)
      );
    default:
      return false;
  }
}

/** A `"` delimiter is spelled exactly; every named child must be too. */
function childrenSpelledExactly(
  node: TSNode,
  variables: ShellVariables,
): boolean {
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (!child || child.type === '"') continue;
    if (!isSpelledExactly(child, variables)) return false;
  }
  return true;
}

/** An escape, or a glob the shell may expand into other words. */
const SHELL_REWRITTEN_CHARACTERS = /[\\*?[]/;

/**
 * A brace the shell expands: one holding a `,` or a `..` range.
 *
 * An empty `{}` is left alone by bash, which is why `find -exec … {} +` keeps
 * its placeholder as written.
 */
const BRACE_EXPANSION = /\{[^}]*(,|\.\.)[^}]*\}/;

/**
 * Whether an argument node's value is decided at run time: it contains a
 * command or process substitution, an arithmetic expansion, or a variable
 * expansion {@link ShellVariables.resolveReference} cannot resolve.
 *
 * The complement of what {@link resolveText} can spell exactly. A plain
 * `$HOME` / `$PWD` reference resolves, so `"$HOME/out"` is not computed; any
 * other expansion falls back to its own source text there, which names a file
 * that is not the one the shell will touch (ADR 0009's computed-path residual).
 * A single-quoted `'$x'` is a literal.
 */
function computedPart(node: TSNode, variables: ShellVariables): boolean {
  if (COMPUTED_NODE_TYPES.has(node.type)) return true;
  if (VARIABLE_EXPANSION_TYPES.has(node.type)) {
    return variables.resolveReference(node) === null;
  }
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child && computedPart(child, variables)) return true;
  }
  return false;
}

/** Node types whose value only running the command can produce. */
const COMPUTED_NODE_TYPES: ReadonlySet<string> = new Set([
  "command_substitution",
  "process_substitution",
  "arithmetic_expansion",
]);

/** Variable references, computed unless they resolve as a plain reference. */
const VARIABLE_EXPANSION_TYPES: ReadonlySet<string> = new Set([
  "simple_expansion",
  "expansion",
]);

/**
 * Resolve the "shell value" of an argument node — the string the shell
 * would pass to the command after quote removal.
 *
 * - `word`          → `.text` (already unquoted)
 * - `raw_string`    → strip surrounding single quotes
 * - `string`        → strip surrounding double quotes, concatenate children text
 * - `concatenation` → concatenate resolved children
 * - expansions      → the resolved value of a plain `$HOME`/`$PWD` reference,
 *   else `.text` (see `shell-variable-expansion.ts`)
 * - other           → `.text` as fallback
 */
function resolveText(node: TSNode, variables: ShellVariables): string {
  switch (node.type) {
    case "word":
      return node.text;
    case "raw_string": {
      // Strip surrounding single quotes: 'content' → content
      const t = node.text;
      if (t.length >= 2 && t.startsWith("'") && t.endsWith("'")) {
        return t.slice(1, -1);
      }
      return t;
    }
    case "string": {
      // Double-quoted string: concatenate the resolved text of inner children,
      // skipping the quote-delimiter nodes (literal `"`).
      let result = "";
      for (let i = 0; i < node.childCount; i++) {
        const child = node.child(i);
        if (!child) continue;
        // Skip the literal `"` delimiters
        if (child.type === '"') continue;
        result += resolveText(child, variables);
      }
      return result;
    }
    case "string_content":
      return node.text;
    case "simple_expansion":
    case "expansion":
      return variables.resolveReference(node) ?? node.text;
    case "concatenation": {
      let result = "";
      for (let i = 0; i < node.childCount; i++) {
        const child = node.child(i);
        if (!child) continue;
        result += resolveText(child, variables);
      }
      return result;
    }
    default:
      return node.text;
  }
}
