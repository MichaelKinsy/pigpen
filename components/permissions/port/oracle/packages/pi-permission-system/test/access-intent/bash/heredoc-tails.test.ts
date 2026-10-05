import { describe, expect, it } from "vitest";
import { hoistHeredocTails } from "#src/access-intent/bash/heredoc-tails";
import type { TSNode } from "#src/access-intent/bash/parser";
import {
  shape,
  viewContractViolations,
  withCorrected as withCorrectedBy,
} from "#test/helpers/bash-parse-tree";

// ── Helpers ───────────────────────────────────────────────────────────────────

function withHoisted<T>(
  command: string,
  read: (hoisted: TSNode, grammar: TSNode) => T,
): Promise<T> {
  return withCorrectedBy(command, hoistHeredocTails, read);
}

/** A heredoc line closed by a one-line body and its delimiter. */
function heredoc(line: string): string {
  return `${line}\nb\nEOF`;
}

/**
 * The shape of `line` with its heredoc hoisted, and the shape the grammar
 * gives the same line with `< in` in place of `<<EOF`, each with its redirect
 * rendered as one placeholder so the two can be compared.
 */
async function shapeAndOracle(
  line: string,
): Promise<{ hoisted: string; oracle: string }> {
  const hoisted = await withHoisted(heredoc(line), (root) =>
    shape(root).replace('(heredoc_redirect "EOF" "b\\n" "EOF")', "REDIRECT"),
  );
  const oracle = await withCorrectedBy(
    line.replace("<<EOF", "< in"),
    (root) => root,
    (root) => shape(root).replace('(file_redirect "in")', "REDIRECT"),
  );
  return { hoisted, oracle };
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe("hoistHeredocTails", () => {
  describe("a redirect written after the delimiter", () => {
    it("becomes a sibling after the heredoc", async () => {
      await expect(
        withHoisted(heredoc("cat <<EOF > /tmp/o"), (root) => shape(root)),
      ).resolves.toBe(
        '(program (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF") (file_redirect "/tmp/o")))',
      );
    });

    it("hoists a herestring the same way", async () => {
      // No `< in` oracle here: the grammar misparses `cat < in <<< x` itself,
      // reading the `<<<` as `<<` and a redirect.
      await expect(
        withHoisted(heredoc("cat <<EOF <<< x"), (root) => shape(root)),
      ).resolves.toBe(
        '(program (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF") (herestring_redirect "x")))',
      );
    });
  });

  describe("a statement joined after the delimiter", () => {
    it("joins a piped command to the redirected statement", async () => {
      await expect(
        withHoisted(heredoc("cat <<EOF | rm -rf /tmp/x"), (root) =>
          shape(root),
        ),
      ).resolves.toBe(
        '(program (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF")) (command (command_name "rm") "-rf" "/tmp/x")))',
      );
    });

    it("joins a `&&` command as a list", async () => {
      await expect(
        withHoisted(heredoc("cat <<EOF && rm -rf /tmp/x"), (root) =>
          shape(root),
        ),
      ).resolves.toBe(
        '(program (list (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF")) (command (command_name "rm") "-rf" "/tmp/x")))',
      );
    });
  });

  describe("the shape the grammar gives the line with `< in` in its place", () => {
    it.each([
      ["a redirect", "cat <<EOF > /tmp/o"],
      ["two redirects", "cat <<EOF > a 2> b"],
      ["a redirect carrying a word", "cat <<EOF 2>/dev/null arg"],
      ["a redirect, then a statement", "cat <<EOF > a && rm x"],
      ["a pipe", "cat <<EOF | rm -rf /tmp/x"],
      ["a pipe with stderr", "cat <<EOF |& rm x"],
      ["an `&&`", "cat <<EOF && rm x"],
      ["an `||`", "cat <<EOF || rm x"],
      ["a pipe into a list", "cat <<EOF | a && b"],
      ["an `&&` into a list", "cat <<EOF && a || b"],
      ["a pipe into a pipeline", "cat <<EOF | a | b"],
      ["a pipe into a redirected list", "cat <<EOF | a && b > o"],
      ["an `&&` into a redirected command", "cat <<EOF && a > o"],
      ["an `&&` into a pipeline", "cat <<EOF && b | c"],
      ["an `||` into a pipeline", "cat <<EOF || b | c"],
      [
        "an `&&` into a pipeline whose first stage redirects",
        "cat <<EOF && a > o | b",
      ],
      [
        "a pipe into a pipeline whose first stage redirects",
        "cat <<EOF | a > o | b",
      ],
      [
        "an `&&` into a list whose last element is such a pipeline",
        "cat <<EOF && x && a > o | b",
      ],
      [
        "an `&&` into a list whose first element is such a pipeline",
        "cat <<EOF && a > o | b && c",
      ],
      [
        "an `&&` into a pipeline ending in a redirect",
        "cat <<EOF && a | b | c > o",
      ],
      [
        "a pipe into a pipeline ending in a redirect",
        "cat <<EOF | a | b | c > o",
      ],
      [
        "an `&&` into a pipeline ending in a subshell's redirect",
        "cat <<EOF && a | b | (c) > o",
      ],
      [
        "an `&&` into a pipeline ending in a redirect carrying a word",
        "cat <<EOF && a | b | c 2>/dev/null y",
      ],
      ["a list body", "x && cat <<EOF | rm y"],
      ["a compound body", "{ cat; } <<EOF | rm x"],
      ["a list body with a redirect", "cd a && cat <<EOF > /tmp/x"],
    ])("is reproduced for %s", async (_label, line) => {
      const { hoisted, oracle } = await shapeAndOracle(line);
      expect(hoisted).toBe(oracle);
    });
  });

  describe("where the grammar groups the `< in` spelling unlike bash", () => {
    // Each of these keeps bash's grouping, or charges a write to more units
    // than the `< in` spelling does, so none is looser than it.
    it("keeps a pipe tail's `&&` outside the pipeline", async () => {
      // The grammar parses `cat < in | a | b && c` as `cat < in | (a | b && c)`.
      await expect(
        withHoisted(heredoc("cat <<EOF | a | b && c"), (root) => shape(root)),
      ).resolves.toBe(
        '(program (list (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF")) (command (command_name "a")) (command (command_name "b"))) (command (command_name "c"))))',
      );
    });

    it("charges a mid-pipeline redirect to the heredoc's stage too", async () => {
      // The grammar charges `> o` to `a | b` alone in `{ cat; } < in | a | b > o | c`.
      await expect(
        withHoisted(heredoc("{ cat; } <<EOF | a | b > o | c"), (root) =>
          shape(root),
        ),
      ).resolves.toBe(
        '(program (pipeline (redirected_statement (pipeline (redirected_statement (compound_statement (command (command_name "cat"))) (heredoc_redirect "EOF" "b\\n" "EOF")) (command (command_name "a")) (command (command_name "b"))) (file_redirect "o")) (command (command_name "c"))))',
      );
    });
  });

  describe("a heredoc tail nested in another node", () => {
    it("is hoisted inside a command substitution", async () => {
      await expect(
        withHoisted("echo $(cat <<EOF | sh\nb\nEOF\n)", (root) => shape(root)),
      ).resolves.toBe(
        '(program (command (command_name "echo") (command_substitution (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\\n" "EOF")) (command (command_name "sh"))))))',
      );
    });
  });

  describe("a tree with nothing to hoist", () => {
    it.each([
      ["a heredoc whose line ends at the delimiter", heredoc("cat <<EOF")],
      [
        "a heredoc that absorbed its command's operand",
        "git commit -F - <<'EOF'\nfeat: x\nEOF",
      ],
      ["words, which the reattachment pass moves", heredoc("git <<EOF push")],
      ["a statement whose parse failed", heredoc("cat <<EOF > /tmp/o | wc")],
      [
        "a redirect and a pipe the grammar cannot combine",
        "cat <<'MSG' 2>&1 | tail -4\nmsg\nMSG",
      ],
    ])("returns the grammar's own root for %s", async (_label, command) => {
      await withHoisted(command, (hoisted, grammar) => {
        expect(hoisted).toBe(grammar);
      });
    });
  });

  describe("the hoisted tree", () => {
    const rewritten = [
      heredoc("cat <<EOF > /tmp/o"),
      heredoc("cat <<EOF > a && rm x"),
      heredoc("cat <<EOF | rm -rf /tmp/x"),
      heredoc("cat <<EOF | a && b > o"),
      heredoc("cat <<EOF && b | c"),
      heredoc("cat <<EOF && a > o | b"),
      heredoc("cat <<EOF && x && a | b | c > o"),
      heredoc("x && cat <<EOF | rm y"),
      heredoc("cat <<EOF 2>/dev/null arg"),
      "echo é $(cat <<EOF | sh\nb\nEOF\n)",
    ];

    it.each(rewritten)("keeps the view contract in %j", async (command) => {
      await withHoisted(command, (hoisted, grammar) => {
        expect(viewContractViolations(command, hoisted, grammar)).toEqual([]);
      });
    });
  });
});
