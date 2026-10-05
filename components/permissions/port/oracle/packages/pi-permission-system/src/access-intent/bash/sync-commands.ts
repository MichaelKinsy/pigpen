import {
  type BashCommand,
  collectCommands,
  collectSalvagedCommands,
} from "./command-enumeration";
import { WordReader } from "./node-text";
import { getWarmBashParser } from "./parser";
import { ShellVariables } from "./shell-variable-expansion";
import { withSalvagedRoots } from "./unresolved-salvage";

/**
 * Synchronously enumerate the command-pattern units of a bash command using the
 * warmed tree-sitter parser.
 *
 * Returns `null` when the parser has not been warmed yet (the pre-warm window),
 * so the caller can fall back to whole-string matching rather than block. Once
 * warm it mirrors the enumeration the gate performs (`BashProgram.commands()`):
 * chains split, nested substitutions/subshells descend, opaque wrappers flagged
 * (#306). Only the command-pattern surface is produced — no path slices, so no
 * `PathNormalizer` is needed.
 *
 * An unparseable command yields an empty array (the caller's decompose path
 * fails it closed via `resolveBashCommandCheck`, #452).
 */
export function parseBashCommandsSync(command: string): BashCommand[] | null {
  const parser = getWarmBashParser();
  if (!parser) return null;
  const tree = parser.parse(command);
  if (!tree) return [];
  try {
    return withSalvagedRoots(tree.rootNode, parser, (salvaged) => {
      const words = new WordReader(
        ShellVariables.scan([tree.rootNode, ...salvaged]),
      );
      return [
        ...collectCommands(tree.rootNode, words),
        ...salvaged.flatMap((root) => collectSalvagedCommands(root, words)),
      ];
    });
  } finally {
    tree.delete();
  }
}
