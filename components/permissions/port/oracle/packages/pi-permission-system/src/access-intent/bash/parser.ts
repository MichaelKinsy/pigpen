import { createRequire } from "node:module";
import { memoizeAsyncWithRetry } from "./async-cache";
import { hoistHeredocTails } from "./heredoc-tails";
import { reattachRedirectArguments } from "./redirect-arguments";

/**
 * Minimal subset of web-tree-sitter's SyntaxNode used by the AST walker.
 * Defined locally so callers do not need to import web-tree-sitter types.
 *
 * The last two members are the parse's own health, where every other member
 * describes a *successful* parse's structure. They are read only by
 * `parse-health.ts`'s two `parseUnresolved*` predicates — see their doc comments for why
 * that boundary matters.
 */
export interface TSNode {
  readonly type: string;
  readonly text: string;
  /** Absolute byte offset of this node's start in the parsed source. */
  readonly startIndex: number;
  /** Absolute byte offset one past this node's end in the parsed source. */
  readonly endIndex: number;
  readonly childCount: number;
  /** False for anonymous tokens (operators, delimiters); true for named nodes. */
  readonly isNamed: boolean;
  /** True when this node is an error or missing token, or contains one. */
  readonly hasError: boolean;
  /** The node immediately before this one under the same parent, named or not. */
  readonly previousSibling: TSNode | null;
  child(index: number): TSNode | null;
}

/**
 * The one parse capability a consumer needs to re-parse a fragment of a
 * command on its own.
 *
 * Narrower than {@link TSParser} on purpose: that interface also carries the
 * parser's own `delete()`, which destroys the process-wide memoized parser for
 * every later command. A consumer re-parsing a fragment has no business
 * holding that, so it takes this instead (`unresolved-salvage.ts`, #875).
 */
export interface BashReparser {
  parse(input: string): { rootNode: TSNode; delete(): void } | null;
}

/**
 * Minimal subset of web-tree-sitter's Parser used by this module.
 */
interface TSParser extends BashReparser {
  delete(): void;
}

async function initParser(): Promise<TSParser> {
  // Use named imports — web-tree-sitter exports Parser as a named class.
  const { Parser, Language } = await import("web-tree-sitter");
  const req = createRequire(import.meta.url);
  const treeSitterWasm = req.resolve("web-tree-sitter/web-tree-sitter.wasm");
  await Parser.init({ locateFile: () => treeSitterWasm });

  const parser = new Parser();
  const bashWasm = req.resolve("tree-sitter-bash/tree-sitter-bash.wasm");
  const bash = await Language.load(bashWasm);
  parser.setLanguage(bash);
  return parser;
}

// Memoize on success but drop a rejected result so a transient init failure
// (e.g. a slow WASM load) is retried on the next tool call instead of poisoning
// the parser for the process lifetime.

/**
 * The parser every consumer reads the bash grammar through.
 *
 * Its trees are the grammar's with two corrections applied where they enter the
 * package, each moving command-line material `tree-sitter-bash` hangs on a
 * redirect to where bash gives it:
 *
 * 1. `hoistHeredocTails` moves the redirects and the `| …` / `&& …` statement
 *    written after a heredoc's delimiter out of the heredoc (#979).
 * 2. `reattachRedirectArguments` hands the words a redirect carries, a file
 *    redirect's or a heredoc's, back to the command (#977).
 *
 * The order matters: a redirect hoisted out of a heredoc can itself carry the
 * command's words (`cat <<EOF 2>/dev/null arg`), which the second pass then
 * hands back. Every walker, the salvage re-parse, and the log masker read the
 * corrected tree, so none of them has to learn the grammar's quirks on its own.
 */
export const getParser = memoizeAsyncWithRetry(async () =>
  correctingParser(await getGrammarParser()),
);

function correctingParser(grammar: TSParser): TSParser {
  return {
    parse: (input) => {
      const tree = grammar.parse(input);
      if (!tree) return null;
      return {
        rootNode: reattachRedirectArguments(hoistHeredocTails(tree.rootNode)),
        delete: () => {
          tree.delete();
        },
      };
    },
    delete: () => {
      grammar.delete();
    },
  };
}

/**
 * `tree-sitter-bash`'s own parser, whose trees are exactly what the grammar
 * produced.
 *
 * Production code reads {@link getParser}; this one exists so a test whose
 * subject is the grammar's own shape can still see it.
 */
export const getGrammarParser = memoizeAsyncWithRetry(initParser);

// Resolved parser cached for synchronous access after warm-up. The tree-sitter
// parser is stateless (parse is a pure function of its input), so caching it at
// module scope is safe even though module state now persists across same-cwd
// session switches.
let warmedParser: TSParser | null = null;

/**
 * Warm the tree-sitter parser so {@link getWarmBashParser} can hand it out
 * synchronously. Triggered at `before_agent_start` (which precedes any tool
 * call) so the synchronous advisory bash path can decompose at gate parity
 * (#309).
 *
 * Best-effort and idempotent: it swallows a WASM init failure (the sync
 * accessor stays cold and callers fall back to whole-string matching), and it
 * returns immediately once warm, so calling it every turn is free.
 */
export async function warmBashParser(): Promise<void> {
  if (warmedParser) return;
  try {
    warmedParser = await getParser();
  } catch {
    // Leave cold → advisory bash queries fall back to whole-string matching.
    // getParser's own retry memoization re-attempts init on the next call.
  }
}

/**
 * The warmed parser for synchronous use, or `null` when it has not been warmed
 * yet (the pre-warm window). Callers that get `null` must degrade gracefully.
 */
export function getWarmBashParser(): TSParser | null {
  return warmedParser;
}

/** Test-only: clear the warmed-parser cache so cold/warm cases are isolatable. */
export function resetWarmBashParser(): void {
  warmedParser = null;
}
