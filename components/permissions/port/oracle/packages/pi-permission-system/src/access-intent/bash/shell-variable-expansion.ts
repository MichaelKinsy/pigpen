/**
 * Resolution of the shell variable references the bash path projection can
 * settle statically.
 *
 * Runs at token collection, upstream of classification: by the time a token
 * reaches `classifyTokenAsPathCandidate` it already carries the expanded path,
 * so `$HOME/x` is accepted by the ordinary absolute-shape branch and needs no
 * per-variable knowledge in the classifiers (#694). Keeping the vocabulary here
 * — rather than teaching each classifier a `$HOME` prefix — is what stops the
 * two from drifting apart, which is the defect this module closes.
 *
 * The resolvable set is deliberately tiny and closed. `HOME` is the spelling
 * `expandHomePath` already resolves for config patterns and path literals, so
 * resolving it here removes an inconsistency rather than widening the
 * determinism boundary; `PWD` reads no environment at all. Every other name
 * keeps its literal text, so ADR 0003's exclusion of ambient host state stands.
 * See `docs/decisions/0009-bash-path-projection-completeness-contract.md`.
 */
import { homedir } from "node:os";
import { hasHomePrefix, splitHomePrefix } from "#src/path/expand-home";

import type { TSNode } from "./parser";

/**
 * The resolvable variables as one program sees them: which of them the program
 * rebinds, so that a reference to a rebound one no longer reads as its startup
 * value.
 *
 * A closed set of rebound names, never their values — tracking what a program
 * assigns is the dataflow ADR 0009 declines.
 */
export class ShellVariables {
  /** A program that rebinds none of the resolvable variables. */
  static readonly UNREBOUND = new ShellVariables(new Set());

  /**
   * The resolvable variables `roots` rebind: the primary parse and every
   * salvaged region, since an assignment in one governs a reference in another.
   *
   * A resolvable name rebinds when a `variable_name` carries it anywhere but as
   * a plain reference's name: an assignment (a prefix one too), a declaration, a
   * `for` variable, `unset`, an arithmetic assignment, `${HOME:=x}`. It also
   * rebinds when a name-binding builtin is handed it as an argument (`read`,
   * `printf -v`, `let`, a quoted `export "HOME=…"`), and both rebind under a
   * command running code the walk never parses (`eval`, `source`, `.`, `trap`). Position is
   * ignored, because a loop or a function body can run a later assignment
   * first; a prefix assignment and an operator read (`${HOME:-x}`) count
   * although neither rebinds the current shell, which costs only the
   * projection of a program already spelling the name oddly.
   */
  static scan(roots: readonly TSNode[]): ShellVariables {
    const rebound = new Set<string>();
    for (const root of roots) collectRebound(root, rebound, false);
    return rebound.size === 0
      ? ShellVariables.UNREBOUND
      : new ShellVariables(rebound);
  }

  private constructor(private readonly rebound: ReadonlySet<string>) {}

  /**
   * The value of a plain `$NAME` / `${NAME}` reference, or `null` when the
   * node is not a plain reference or names a variable outside the resolvable
   * set.
   *
   * Plainness is decided structurally, not by matching the node's text: a
   * plain reference carries exactly one `variable_name` child and nothing else
   * but delimiters. An operator form (`${HOME:-/tmp}`, `${#HOME}`,
   * `${HOME%/*}`) carries additional children and is therefore rejected without
   * this module needing to enumerate bash's expansion operators.
   */
  resolveReference(node: TSNode): string | null {
    const name = plainVariableName(node);
    if (name === null || this.rebound.has(name)) return null;
    return RESOLVABLE_VARIABLES.get(name)?.() ?? null;
  }

  /**
   * Whether a collected path token is spelled from a `HOME` this program
   * rebinds (`$HOME/x`, `${HOME}`, `~/x`).
   *
   * Such a token names no path the projection can know, yet path normalization
   * expands its prefix to the startup home exactly as it does for a config
   * pattern, so it must leave the path surfaces before it reaches them.
   */
  spellsReboundHome(token: string): boolean {
    return this.rebound.has("HOME") && hasHomePrefix(token);
  }

  /**
   * `text` with a leading `~` / `$HOME` / `${HOME}` spelled as the startup
   * home, or `undefined` when it opens with none or the program rebinds `HOME`.
   *
   * The complement of {@link spellsReboundHome}: the spelling a command-pattern
   * rule written with a home prefix is compiled to, so the rule matches the
   * command as the shell runs it. The rest is kept verbatim, never
   * path-normalized, because `..` in an argument is not a path step of the
   * command's name. A backslash after the prefix gets none, because bash does
   * not expand a tilde prefix holding a quoted character.
   */
  spellHomeAtStart(text: string): string | undefined {
    if (this.rebound.has("HOME")) return undefined;
    const rest = splitHomePrefix(text);
    if (rest === undefined || rest.startsWith("\\")) return undefined;
    return homedir() + rest;
  }

  /**
   * How a word's leading unquoted text expands when it opens with a tilde
   * prefix that reads `HOME` (`~` or `~/…`; `~user` reads the password
   * database instead), or `undefined` when it does not.
   *
   * Unknown once the program rebinds `HOME`: bash 3.2, which Pi runs as
   * `/bin/bash` on macOS, expands it from the reassigned value. Otherwise it is
   * the inherited `HOME`, which `homedir()` returns verbatim, so the word leads
   * with whatever that does.
   */
  readTilde(leadingText: string): TildeReading | undefined {
    if (leadingText !== "~" && !leadingText.startsWith("~/")) return undefined;
    if (this.rebound.has("HOME")) return { known: false };
    return { known: true, leadsWithDash: homedir().startsWith("-") };
  }
}

/** What a leading tilde prefix expands to, as far as the program shows. */
export type TildeReading =
  | { readonly known: false }
  | { readonly known: true; readonly leadsWithDash: boolean };

/**
 * How each resolvable variable is spelled as a path.
 *
 * `PWD` resolves to the base-relative marker rather than a directory: the
 * shell's working directory at a given point *is* the projection's effective
 * base, which the resolver already applies via `resolveBase`. Handing back `.`
 * therefore lands `$PWD/x` on the same footing as `./x` — correct after any
 * `cd` folding, conservative under an unknown base (#393), and free of both a
 * threaded base parameter and a platform branch.
 */
const RESOLVABLE_VARIABLES: ReadonlyMap<string, () => string> = new Map([
  ["HOME", homedir],
  ["PWD", () => "."],
]);

/**
 * Record each resolvable name `node` binds.
 *
 * `bindsNames` holds beneath a builtin that binds the names it is handed
 * (`read HOME`, `export "HOME=/etc"`, `declare -n r=HOME`), and only there does
 * an argument spelling a name count: `grep HOME ~/.bashrc` binds nothing, and
 * reading it as a rebinding would drop the `~/.bashrc` it does read.
 */
function collectRebound(
  node: TSNode,
  rebound: Set<string>,
  inheritedBindsNames: boolean,
): void {
  if (REFERENCE_TYPES.has(node.type) && plainVariableName(node) !== null) {
    return;
  }
  const bindsNames = bindsNamesBeneath(node, inheritedBindsNames);
  if (node.type === "variable_name" && RESOLVABLE_VARIABLES.has(node.text)) {
    rebound.add(node.text);
  }
  if (bindsNames && NAME_ARGUMENT_TYPES.has(node.type)) {
    const name = NAMED_ARGUMENT.exec(node.text.replace(QUOTING, ""))?.[1];
    if (name) rebound.add(name);
  }
  if (node.type === "command" && UNSEEN_CODE_COMMANDS.has(commandName(node))) {
    for (const name of RESOLVABLE_VARIABLES.keys()) rebound.add(name);
  }
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (child) collectRebound(child, rebound, bindsNames);
  }
}

/**
 * Whether the arguments beneath `node` are names its command binds. A command
 * decides for itself; a `declare`-family or `unset` statement always binds;
 * anything else inherits.
 */
function bindsNamesBeneath(node: TSNode, inherited: boolean): boolean {
  if (node.type === "declaration_command" || node.type === "unset_command") {
    return true;
  }
  if (node.type !== "command") return inherited;
  const name = commandName(node);
  if (name === "printf") return hasArgument(node, "-v");
  return NAME_BINDING_COMMANDS.has(name);
}

/**
 * Builtins that bind a name passed as an argument. `declare` and its family
 * parse as a `declaration_command` when spelled plainly and as a `command`
 * when quoted, so they are listed here too; `printf` binds only under `-v`.
 */
const NAME_BINDING_COMMANDS: ReadonlySet<string> = new Set([
  "read",
  "mapfile",
  "readarray",
  "getopts",
  "let",
  "unset",
  "declare",
  "typeset",
  "local",
  "export",
  "readonly",
]);

/**
 * Argument nodes that can spell a name a builtin binds, whole or as the head
 * of an assignment (`HOME=…`, `HOME+=…`, `HOME[0]=…`, `let HOME++`).
 */
const NAME_ARGUMENT_TYPES: ReadonlySet<string> = new Set([
  "word",
  "string",
  "raw_string",
  "concatenation",
]);

/** A resolvable name leading an argument, not followed by more of an identifier. */
const NAMED_ARGUMENT = new RegExp(
  `^(${[...RESOLVABLE_VARIABLES.keys()].join("|")})(?![A-Za-z0-9_])`,
);

/** Quote and escape characters, which the shell removes before a builtin sees the name. */
const QUOTING = /["'\\]/g;

/** A command's name as the shell reads it, quotes and escapes removed. */
function commandName(command: TSNode): string {
  for (let i = 0; i < command.childCount; i++) {
    const child = command.child(i);
    if (child?.type === "command_name") return child.text.replace(QUOTING, "");
  }
  return "";
}

/** Whether a command carries `flag` as one of its own arguments. */
function hasArgument(command: TSNode, flag: string): boolean {
  for (let i = 0; i < command.childCount; i++) {
    const child = command.child(i);
    if (child && child.type !== "command_name" && child.text === flag) {
      return true;
    }
  }
  return false;
}

/**
 * Commands that run code the walk never parses, which may rebind anything:
 * `eval`'s joined arguments, a file `source`/`.` reads, and a `trap` action.
 */
const UNSEEN_CODE_COMMANDS: ReadonlySet<string> = new Set([
  "eval",
  "source",
  ".",
  "trap",
]);

/** The node types a variable reference parses as. */
const REFERENCE_TYPES: ReadonlySet<string> = new Set([
  "simple_expansion",
  "expansion",
]);

/** Node types that delimit an expansion without altering what it evaluates to. */
const EXPANSION_DELIMITERS: ReadonlySet<string> = new Set(["$", "${", "}"]);

/**
 * The variable a node plainly references, or `null` when it references none —
 * because it has no `variable_name` child, has more than one, or carries a
 * child that is neither the name nor a delimiter (an expansion operator and its
 * operand, or an assignment's `=` and value).
 */
function plainVariableName(node: TSNode): string | null {
  let name: string | null = null;

  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    if (!child) continue;
    if (child.type === "variable_name") {
      if (name !== null) return null;
      name = child.text;
      continue;
    }
    if (!EXPANSION_DELIMITERS.has(child.type)) return null;
  }

  return name;
}
