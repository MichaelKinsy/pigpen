import { describe, expect, it } from "vitest";
import { sedWithdrawsReadClaim } from "#src/access-intent/bash/sed-invocation";
import {
  computedArgWord,
  literalArgWords as literal,
} from "#test/helpers/arg-words";

describe("sedWithdrawsReadClaim", () => {
  describe("a print-only invocation", () => {
    it.each([
      [["-n", "1,80p", "f.md"]],
      [["-n", "-e", "1p", "-e", "$p", "f.md"]],
      [["-ne", "5q", "f.md"]],
      [["-nE", "10,20p", "f.md"]],
      [["--quiet", "10,20p", "f.md"]],
      [["--expression=3p", "f.md"]],
      [["--expression", "3p", "f.md"]],
      [["1d;$d", "f.md"]],
      [["-n", "0~4p", "f.md"]],
      [["-n", "5,+3p", "f.md"]],
      [["-n", "5,~4p", "f.md"]],
      [["-n", "$!N;P;D", "f.md"]],
      [["-n", "l 40", "f.md"]],
      [["-n", "=", "f.md"]],
      [["", "f.md"]],
      [["-n", "1p", "--", "-n"]],
      [["-n", "1p", "-"]],
    ])("keeps the claim for sed %j", (args) => {
      expect(sedWithdrawsReadClaim(literal(...args))).toBe(false);
    });

    it.each([
      [["-n", "/x/,/y/p", "f.md"]],
      [["-n", "/```mermaid/,/```/p", "f.md"]],
      [["-n", "/^export/Ip", "f.md"]],
      [["-n", "\\|a/b|p", "f.md"]],
      [["-n", "0,/re/p", "f.md"]],
      [["s/a/b/g", "f.md"]],
      [["s|a|b|", "f.md"]],
      [["-n", "s/a/b/2p", "f.md"]],
      [["-E", "s/(a)\\/b/\\1/gI", "f.md"]],
      [["s/\\.[0-9]\\+/[]/g", "f.md"]],
      [["s/[[:space:]]*$//", "f.md"]],
      [["y/abc/xyz/", "f.md"]],
      [["/^#/d", "f.md"]],
      [["-n", "/start/,/end/{/skip/!p}", "f.md"]],
      [["-n", "/a/{p;q}", "f.md"]],
      [[":a;N;$!ba;s/\\n/ /g", "f.md"]],
      [["-n", "/x/{s/a/b/;t done;p;:done\n}", "f.md"]],
      [["-n", "# a comment\n1p", "f.md"]],
    ])("keeps the claim for the extended script in sed %j", (args) => {
      expect(sedWithdrawsReadClaim(literal(...args))).toBe(false);
    });

    it("keeps the claim across a multi-line script", () => {
      expect(sedWithdrawsReadClaim(literal("-n", "1p\n$p", "f.md"))).toBe(
        false,
      );
    });
  });

  describe("an option outside the allowlist", () => {
    it.each([
      [["-i", "s/a/b/", "f"]],
      [["-i.bak", "1d", "f"]],
      [["-i", "", "1d", "f"]],
      [["-ni", "1p", "f"]],
      [["-I", "", "1d", "f"]],
      [["--in-place", "1d", "f"]],
      [["--in-place=.bak", "1d", "f"]],
      [["--in", "1d", "f"]],
      [["--qui", "1p", "f"]],
      [["-f", "script.sed", "f"]],
      [["--file=script.sed", "f"]],
      [["-l", "5", "1p", "f"]],
      [["--version"]],
      [["-n", "1p", "f", "-i"]],
    ])("withdraws the claim for sed %j", (args) => {
      expect(sedWithdrawsReadClaim(literal(...args))).toBe(true);
    });

    it("withdraws the claim when an -e follows a positional", () => {
      // GNU reads the -e as a script and `p` as a file; BSD reads `p` as the
      // script and the rest as files. The two disagree about the script.
      expect(sedWithdrawsReadClaim(literal("p", "-e", "q", "f"))).toBe(true);
    });

    it("withdraws the claim when -e has no script to take", () => {
      expect(sedWithdrawsReadClaim(literal("-n", "-e"))).toBe(true);
    });

    it("withdraws the claim when there is no script at all", () => {
      expect(sedWithdrawsReadClaim(literal("-n"))).toBe(true);
    });
  });

  describe("a computed argument", () => {
    it("withdraws the claim for a computed script", () => {
      expect(
        sedWithdrawsReadClaim([
          ...literal("-n"),
          computedArgWord("$range", true),
          ...literal("f.md"),
        ]),
      ).toBe(true);
    });

    it("withdraws the claim for a computed file, which could spell -i", () => {
      expect(
        sedWithdrawsReadClaim([
          ...literal("-n", "1p"),
          computedArgWord("$f", true),
        ]),
      ).toBe(true);
    });

    it("withdraws the claim for a computed word that cannot lead with a dash", () => {
      // It could still be the script itself, so no leading-character test
      // bounds what it does. The value is one the script grammar proves, so
      // only the computed flag can withdraw the claim.
      expect(
        sedWithdrawsReadClaim([
          ...literal("-n"),
          computedArgWord("p", false),
          ...literal("f.md"),
        ]),
      ).toBe(true);
    });
  });

  describe("a script command outside the grammar", () => {
    it.each([
      "w out",
      "1w out",
      "W out",
      "r other",
      "R other",
      "e rm -rf x",
      "v",
      "a text",
      "1i text",
      "c text",
      "p;w out",
      "1,2",
      "1pw",
      "}",
    ])("withdraws the claim for the script %j", (script) => {
      expect(sedWithdrawsReadClaim(literal("-n", script, "f.md"))).toBe(true);
    });

    it.each([
      "s/a/b/w out",
      "s/a/b/gw out",
      "s/a/b/e",
      "s/a/b/x",
      "s/a/b",
      "s/a/b/;w out",
      "saxayaw out",
      "s\\a\\b\\",
      "y/ab/c",
      "/x/w out",
      "/x",
      "{p",
      "b end;w out",
      "s/[]/]x/y/",
      "s,[^,]*,x,",
      "/x/{p;:done}",
    ])("withdraws the claim for the extended script %j", (script) => {
      expect(sedWithdrawsReadClaim(literal("-n", script, "f.md"))).toBe(true);
    });

    it("withdraws the claim for a delimiter inside a bracket expression", () => {
      // BSD reads `[/]` as a bracket, so the substitution ends at the third
      // `/` and `w out` is its write flag; GNU ends the regex at the first
      // `/` and rejects the rest. The two readings disagree about the flags.
      expect(sedWithdrawsReadClaim(literal("s/[/]/x/w out", "f.md"))).toBe(
        true,
      );
    });

    it("withdraws the claim where only GNU's reading writes", () => {
      // GNU ends the regex at `[`, reads `]` as the replacement, and takes
      // `w out/` as its write flag; BSD reads the bracket and sees no flag.
      expect(sedWithdrawsReadClaim(literal("s/[/]/w out/", "f.md"))).toBe(true);
    });

    it("withdraws the claim for a delimiter inside an address bracket", () => {
      expect(sedWithdrawsReadClaim(literal("-n", "/[/]/p", "f.md"))).toBe(true);
    });

    it("withdraws the claim for an unterminated bracket expression", () => {
      expect(sedWithdrawsReadClaim(literal("-n", "/[ab/p", "f.md"))).toBe(true);
    });

    it("withdraws the claim for a command glued to the next one", () => {
      // sed rejects `pd` as extra characters after a command, so the proof
      // must not read it as two commands sed never runs.
      expect(sedWithdrawsReadClaim(literal("-n", "pd", "f.md"))).toBe(true);
    });
  });
});
