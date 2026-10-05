import { describe, expect, it } from "vitest";
import type { TSNode } from "#src/access-intent/bash/parser";
import { reattachRedirectArguments } from "#src/access-intent/bash/redirect-arguments";
import {
  shape,
  viewContractViolations,
  withCorrected as withCorrectedBy,
} from "#test/helpers/bash-parse-tree";

// ── Helpers ───────────────────────────────────────────────────────────────────

function withCorrected<T>(
  command: string,
  read: (corrected: TSNode, grammar: TSNode) => T,
): Promise<T> {
  return withCorrectedBy(command, reattachRedirectArguments, read);
}

function correctedShape(command: string): Promise<string> {
  return withCorrected(command, (corrected) => shape(corrected));
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe("reattachRedirectArguments", () => {
  describe("a redirect the grammar hung the command's words on", () => {
    it("hands the words back to a command body", async () => {
      // Nothing is left at the statement, so the corrected command takes its
      // place, exactly as the grammar parses `2>/dev/null git push --force`.
      await expect(
        correctedShape("git 2>/dev/null push --force"),
      ).resolves.toBe(
        '(program (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force"))',
      );
    });

    it("keeps a redirect after the last word at the statement", async () => {
      await expect(correctedShape("cmd a > out b 2>&1")).resolves.toBe(
        '(program (redirected_statement (command (command_name "cmd") "a" (file_redirect "out") "b") (file_redirect "2" "1")))',
      );
    });

    it("moves every redirect up to the last word, in source order", async () => {
      // `<in` carries no words of its own, but it sits before `arg1`, so it
      // moves with the rest and the command's children stay in source order.
      await expect(
        correctedShape("cmd <in 2>err arg1 >out arg2"),
      ).resolves.toBe(
        '(program (command (command_name "cmd") (file_redirect "in") (file_redirect "2" "err") "arg1" (file_redirect "out") "arg2"))',
      );
    });

    it("hands the words to a close operator's command, since it has no target", async () => {
      await expect(correctedShape("cmd >&- arg")).resolves.toBe(
        '(program (command (command_name "cmd") ">&-" "arg"))',
      );
    });

    it("hands the words to the last command of a list the grammar grouped", async () => {
      // The grammar hangs the redirect off the whole `&&` list; bash gives it
      // and its words to `git`.
      await expect(
        correctedShape("cd a && b && git 2>/dev/null push"),
      ).resolves.toBe(
        '(program (list (list (command (command_name "cd") "a") (command (command_name "b"))) (command (command_name "git") (file_redirect "2" "/dev/null") "push")))',
      );
    });

    it("hands the words to the last stage of a pipeline the grammar grouped", async () => {
      // A redirect on the last stage hangs off the whole pipeline in the
      // grammar; bash gives it and its words to that stage.
      await expect(correctedShape("rg -l x | xargs ls 2>&1 ~/x")).resolves.toBe(
        '(program (pipeline (command (command_name "rg") "-l" "x") (command (command_name "xargs") "ls" (file_redirect "2" "1") "~/x")))',
      );
    });
  });

  describe("a heredoc the grammar hung the command's words on", () => {
    it("hands the words back to the command, after the heredoc", async () => {
      // The body stays in the heredoc: it is written after the words, and its
      // substitutions still run.
      await expect(
        correctedShape("git <<EOF push --force\nb\nEOF"),
      ).resolves.toBe(
        '(program (command (command_name "git") (heredoc_redirect "EOF" "b\\n" "EOF") "push" "--force"))',
      );
    });

    it("hands the words to the last command of a list the grammar grouped", async () => {
      await expect(correctedShape("x && git <<EOF push\nb\nEOF")).resolves.toBe(
        '(program (list (command (command_name "x")) (command (command_name "git") (heredoc_redirect "EOF" "b\\n" "EOF") "push")))',
      );
    });

    it("hands back a substitution and a string as words too", async () => {
      await expect(
        correctedShape('cat <<EOF $(rm x) "q s"\nb\nEOF'),
      ).resolves.toBe(
        '(program (command (command_name "cat") (heredoc_redirect "EOF" "b\\n" "EOF") (command_substitution (command (command_name "rm") "x")) (string "q s")))',
      );
    });
  });

  describe("a statement nested in another node", () => {
    it.each([
      [
        "a pipeline",
        "git 2>/dev/null push --force | tail",
        '(program (pipeline (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force") (command (command_name "tail"))))',
      ],
      [
        "a command substitution",
        "x=$(git 2>/dev/null push --force)",
        '(program (variable_assignment "x" (command_substitution (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force"))))',
      ],
      [
        "a word moved out of another redirect",
        "echo 2>/dev/null $(git 2>/dev/null push)",
        '(program (command (command_name "echo") (file_redirect "2" "/dev/null") (command_substitution (command (command_name "git") (file_redirect "2" "/dev/null") "push"))))',
      ],
    ])("is corrected inside %s", async (_label, command, expected) => {
      await expect(correctedShape(command)).resolves.toBe(expected);
    });
  });

  describe("a tree with nothing to correct", () => {
    it.each([
      ["no redirect", "git push --force"],
      ["a redirect after the last word", "git push --force 2>/dev/null"],
      ["a redirect before the command", "2>/dev/null git push --force"],
      ["a statement whose parse failed", "cat <> rw.txt extra"],
      ["a compound body bash rejects", "{ a; } 2>/dev/null b"],
      ["a heredoc whose line ends at the delimiter", "cat <<EOF\nb\nEOF"],
      [
        "a heredoc on a compound body bash rejects",
        "{ echo a; } <<EOF b\nb\nEOF",
      ],
    ])("returns the grammar's own root for %s", async (_label, command) => {
      await withCorrected(command, (corrected, grammar) => {
        expect(corrected).toBe(grammar);
      });
    });
  });

  describe("the corrected tree", () => {
    const rewritten = [
      "git 2>/dev/null push --force",
      "cmd a > out b 2>&1",
      "cmd <in 2>err arg1 >out arg2",
      "cmd >&- arg",
      "cd a && b && git 2>/dev/null push",
      "echo é 2>/dev/null $(git 2>/dev/null push) | tail",
      "git <<EOF push --force\nb\nEOF",
      "x && git <<EOF push\nb\nEOF",
      'cat <<EOF $(rm x) "q s"\nb\nEOF',
    ];

    it.each(rewritten)("keeps the view contract in %s", async (command) => {
      await withCorrected(command, (corrected, grammar) => {
        expect(viewContractViolations(command, corrected, grammar)).toEqual([]);
      });
    });
  });
});
