import { describe, expect, it } from "vitest";
import { heredocFreeLinesWithin } from "#src/access-intent/bash/heredoc-free-lines";
import { getParser } from "#src/access-intent/bash/parser";

/** The heredoc-free lines of a real parse of `command`. */
async function heredocFreeLinesOf(command: string): Promise<string[]> {
  const parser = await getParser();
  const tree = parser.parse(command);
  if (!tree) throw new Error("parse returned null");
  try {
    return heredocFreeLinesWithin(tree.rootNode);
  } finally {
    tree.delete();
  }
}

describe("heredocFreeLinesWithin", () => {
  describe("a heredoc tail the grammar cannot parse", () => {
    it.each([
      ["a `;` command", "cat <<EOF ; rm -rf x\nb\nEOF", "cat ; rm -rf x"],
      ["an `&` command", "cat <<EOF & rm -rf x\nb\nEOF", "cat & rm -rf x"],
      [
        "words and a redirect",
        "cat <<EOF arg > /tmp/o\nb\nEOF",
        "cat arg > /tmp/o",
      ],
      [
        "a redirect then a pipe",
        "git commit -F - <<'MSG' 2>&1 | rm -rf x\nb\nMSG",
        "git commit -F - 2>&1 | rm -rf x",
      ],
    ])(
      "spells the line without its heredoc for %s",
      async (_label, command, line) => {
        expect(await heredocFreeLinesOf(command)).toEqual([line]);
      },
    );
  });

  describe("the operator it cuts", () => {
    it.each([
      ["a descriptor", "cat 2<<EOF ; rm -rf x\nb\nEOF"],
      ["a tab-stripping heredoc", "cat <<-EOF ; rm -rf x\nb\nEOF"],
      ["a quoted delimiter", 'cat <<"EOF" ; rm -rf x\nb\nEOF'],
    ])("cuts %s whole", async (_label, command) => {
      expect(await heredocFreeLinesOf(command)).toEqual(["cat ; rm -rf x"]);
    });

    it("cuts a descriptor the grammar lexed into the delimiter", async () => {
      // `0<<EOF` lands as one `heredoc_start` inside a top-level `ERROR`, so no
      // statement holds it and the line is the span.
      expect(await heredocFreeLinesOf("cat 0<<EOF | rm -rf x\nb\nEOF")).toEqual(
        ["cat | rm -rf x"],
      );
    });
  });

  describe("the span it spells", () => {
    it("starts at the heredoc's statement, which may hold a list", async () => {
      expect(
        await heredocFreeLinesOf("echo hi && cat <<EOF ; rm -rf x\nb\nEOF"),
      ).toEqual(["echo hi && cat ; rm -rf x"]);
    });

    it("starts at the statement rather than the line inside a compound", async () => {
      // The line alone re-parses as an unterminated `if`.
      expect(
        await heredocFreeLinesOf("if true; then cat <<EOF ; rm x\nb\nEOF\nfi"),
      ).toEqual(["cat ; rm x"]);
    });

    it("spells each unresolved heredoc's line, in source order", async () => {
      expect(
        await heredocFreeLinesOf("cat <<A ; rm x\na\nA\ncat <<B & rm y\nb\nB"),
      ).toEqual(["cat ; rm x", "cat & rm y"]);
    });

    it("leaves an unterminated heredoc's body out", async () => {
      expect(
        await heredocFreeLinesOf("cat <<'EOF'\nsee `rm -rf x` here"),
      ).toEqual(["cat"]);
    });
  });

  describe("a heredoc whose statement parsed", () => {
    it.each([
      ["a clean tail", "cat <<EOF | rm -rf x\nb\nEOF"],
      ["a failure in another statement", "cat <<EOF\nb\nEOF\necho 'x"],
      ["no heredoc at all", "echo hi > out.txt <> rw.txt"],
    ])("spells nothing for %s", async (_label, command) => {
      expect(await heredocFreeLinesOf(command)).toEqual([]);
    });
  });
});
