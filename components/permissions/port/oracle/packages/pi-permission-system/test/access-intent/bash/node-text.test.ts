import { homedir } from "node:os";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SKIP_SUBTREE_TYPES,
  WordReader,
} from "#src/access-intent/bash/node-text";
import { getParser } from "#src/access-intent/bash/parser";
import { ShellVariables } from "#src/access-intent/bash/shell-variable-expansion";
import { makeTSNode } from "#test/helpers/fake-ts-node";

/** A reader for a program that rebinds neither `HOME` nor `PWD`. */
const words = new WordReader(ShellVariables.UNREBOUND);

describe("SKIP_SUBTREE_TYPES", () => {
  it("contains the three node types that must not be descended", () => {
    expect(SKIP_SUBTREE_TYPES.has("heredoc_body")).toBe(true);
    expect(SKIP_SUBTREE_TYPES.has("heredoc_end")).toBe(true);
    expect(SKIP_SUBTREE_TYPES.has("comment")).toBe(true);
  });

  it("does not contain common argument node types", () => {
    expect(SKIP_SUBTREE_TYPES.has("word")).toBe(false);
    expect(SKIP_SUBTREE_TYPES.has("string")).toBe(false);
    expect(SKIP_SUBTREE_TYPES.has("raw_string")).toBe(false);
  });
});

describe("WordReader.text", () => {
  describe("word nodes", () => {
    it("returns the node text unchanged", () => {
      expect(words.text(makeTSNode("word", "hello"))).toBe("hello");
    });
  });

  describe("raw_string nodes (single-quoted)", () => {
    it("strips surrounding single quotes", () => {
      expect(words.text(makeTSNode("raw_string", "'content'"))).toBe("content");
    });

    it("strips single quotes around a path", () => {
      expect(words.text(makeTSNode("raw_string", "'/etc/hosts'"))).toBe(
        "/etc/hosts",
      );
    });

    it("returns text as-is when not fully single-quoted", () => {
      // A raw_string node without enclosing quotes (defensive fallback)
      expect(words.text(makeTSNode("raw_string", "noquotes"))).toBe("noquotes");
    });
  });

  describe("string nodes (double-quoted)", () => {
    it("concatenates inner word children, skipping quote delimiters", () => {
      const quoteOpen = makeTSNode('"', '"');
      const content = makeTSNode("string_content", "hello world");
      const quoteClose = makeTSNode('"', '"');
      const node = makeTSNode("string", '"hello world"', [
        quoteOpen,
        content,
        quoteClose,
      ]);
      expect(words.text(node)).toBe("hello world");
    });

    it("concatenates multiple inner children", () => {
      const quoteOpen = makeTSNode('"', '"');
      const part1 = makeTSNode("string_content", "foo");
      const part2 = makeTSNode("simple_expansion", "$BAR");
      const quoteClose = makeTSNode('"', '"');
      const node = makeTSNode("string", '"foo$BAR"', [
        quoteOpen,
        part1,
        part2,
        quoteClose,
      ]);
      expect(words.text(node)).toBe("foo$BAR");
    });

    it("returns empty string for an empty double-quoted string", () => {
      const quoteOpen = makeTSNode('"', '"');
      const quoteClose = makeTSNode('"', '"');
      const node = makeTSNode("string", '""', [quoteOpen, quoteClose]);
      expect(words.text(node)).toBe("");
    });
  });

  describe("string_content, simple_expansion, and expansion nodes", () => {
    it("returns text as-is for string_content", () => {
      expect(words.text(makeTSNode("string_content", "plain text"))).toBe(
        "plain text",
      );
    });

    it("resolves a plain $HOME reference to the home directory", () => {
      // The children matter: the resolver discriminates a plain reference from
      // an operator-bearing expansion structurally, not by text prefix (#694).
      const node = makeTSNode("simple_expansion", "$HOME", [
        makeTSNode("$", "$"),
        makeTSNode("variable_name", "HOME"),
      ]);
      expect(words.text(node)).toBe(homedir());
    });

    it("resolves a plain ${HOME} reference to the home directory", () => {
      const node = makeTSNode("expansion", "${HOME}", [
        makeTSNode("${", "${"),
        makeTSNode("variable_name", "HOME"),
        makeTSNode("}", "}"),
      ]);
      expect(words.text(node)).toBe(homedir());
    });

    it("returns text as-is for a variable outside the resolvable set", () => {
      const node = makeTSNode("expansion", "${VAR}", [
        makeTSNode("${", "${"),
        makeTSNode("variable_name", "VAR"),
        makeTSNode("}", "}"),
      ]);
      expect(words.text(node)).toBe("${VAR}");
    });

    it("returns text as-is for an expansion carrying an operator", () => {
      const node = makeTSNode("expansion", "${HOME:-/tmp}", [
        makeTSNode("${", "${"),
        makeTSNode("variable_name", "HOME"),
        makeTSNode(":-", ":-"),
        makeTSNode("word", "/tmp"),
        makeTSNode("}", "}"),
      ]);
      expect(words.text(node)).toBe("${HOME:-/tmp}");
    });
  });

  describe("concatenation nodes", () => {
    it("concatenates resolved children", () => {
      const word = makeTSNode("word", "/etc/");
      const expansion = makeTSNode("simple_expansion", "$FILE", [
        makeTSNode("$", "$"),
        makeTSNode("variable_name", "FILE"),
      ]);
      const node = makeTSNode("concatenation", "/etc/$FILE", [word, expansion]);
      expect(words.text(node)).toBe("/etc/$FILE");
    });

    it("concatenates a resolved $HOME reference with its suffix", () => {
      const expansion = makeTSNode("simple_expansion", "$HOME", [
        makeTSNode("$", "$"),
        makeTSNode("variable_name", "HOME"),
      ]);
      const suffix = makeTSNode("word", "/sub");
      const node = makeTSNode("concatenation", "$HOME/sub", [
        expansion,
        suffix,
      ]);
      expect(words.text(node)).toBe(`${homedir()}/sub`);
    });

    it("handles nested concatenation-of-string", () => {
      // A concatenation whose child is a double-quoted string
      const quoteOpen = makeTSNode('"', '"');
      const content = makeTSNode("string_content", "bar");
      const quoteClose = makeTSNode('"', '"');
      const inner = makeTSNode("string", '"bar"', [
        quoteOpen,
        content,
        quoteClose,
      ]);
      const prefix = makeTSNode("word", "foo");
      const node = makeTSNode("concatenation", 'foo"bar"', [prefix, inner]);
      expect(words.text(node)).toBe("foobar");
    });
  });

  describe("default fallback", () => {
    it("returns the raw text for unknown node types", () => {
      expect(words.text(makeTSNode("unknown_type", "rawtext"))).toBe("rawtext");
    });
  });
});

describe("WordReader.isComputed", () => {
  /** Parse `echo <argument>` and ask about the command's first argument. */
  async function argumentIsComputed(argument: string): Promise<boolean> {
    const parser = await getParser();
    const tree = parser.parse(`echo ${argument}`);
    if (!tree) throw new Error("parse returned null");
    try {
      const command = tree.rootNode.child(0);
      const node = command?.child(1);
      if (!node) throw new Error(`no argument node in: echo ${argument}`);
      return words.isComputed(node);
    } finally {
      tree.delete();
    }
  }

  it.each([
    ["a bare word", "out.txt"],
    ["a single-quoted dollar sign", "'$x'"],
    ["a plain $HOME reference the resolver expands", '"$HOME/out"'],
  ])("answers false for %s (%s)", async (_label, argument) => {
    await expect(argumentIsComputed(argument)).resolves.toBe(false);
  });

  it.each([
    ["an unquoted variable", "$OUT"],
    ["a quoted variable", '"$OUT"'],
    ["a variable concatenated with a literal", '"${DIR}/x"'],
    ["a command substitution inside a word", "out-$(date).txt"],
    ["a process substitution", "<(cmd)"],
    ["an arithmetic expansion", "out-$((1+1)).txt"],
  ])("answers true for %s (%s)", async (_label, argument) => {
    await expect(argumentIsComputed(argument)).resolves.toBe(true);
  });
});

describe("WordReader.argWord", () => {
  /** Parse `echo <argument>` and read the command's first argument. */
  async function argWordOf(argument: string) {
    const parser = await getParser();
    const tree = parser.parse(`echo ${argument}`);
    if (!tree) throw new Error("parse returned null");
    try {
      const node = tree.rootNode.child(0)?.child(1);
      if (!node) throw new Error(`no argument node in: echo ${argument}`);
      return words.argWord(node);
    } finally {
      tree.delete();
    }
  }

  describe("a value the source spells exactly", () => {
    it.each([
      ["a bare word", "-n", "-n", true],
      ["a single-quoted word", "'-i'", "-i", true],
      ["a double-quoted word", '"1,80p"', "1,80p", false],
      ["a single-quoted backslash", "'s/\\./x/'", "s/\\./x/", false],
      ["a tilde path", "~/notes.md", "~/notes.md", false],
      ["a concatenation of quoted parts", "-'i'\"\"", "-i", true],
      ["an empty brace pair, which bash leaves alone", "{}", "{}", false],
      ["a number", "2", "2", false],
      ["a negative number", "-20", "-20", true],
    ])(
      "reads %s as exact (%s)",
      async (_label, argument, value, mayLeadWithDash) => {
        await expect(argWordOf(argument)).resolves.toEqual({
          value,
          computed: false,
          mayLeadWithDash,
        });
      },
    );
  });

  describe("a value only the shell decides", () => {
    // The shell removes an escape, expands a glob, and decodes an ANSI-C
    // string before the program sees the word, so the source spelling is not
    // the value a capability proof must read.
    it.each([
      ["an unquoted escape", "-\\i"],
      ["an escape inside double quotes", '"-\\i"'],
      ["an unquoted glob", "-*"],
      ["a bracket glob", "-[i]"],
      ["a comma brace expansion", "{-i,-n}"],
      ["a brace expansion glued to an option", "-n{,i}"],
      ["an ANSI-C string", "$'-i'"],
      ["a variable", "$OPT"],
      ["a command substitution", "$(echo -i)"],
      ["a based number with an expansion", "10#$x"],
    ])("marks %s computed (%s)", async (_label, argument) => {
      await expect(argWordOf(argument)).resolves.toMatchObject({
        computed: true,
      });
    });
  });

  describe("a leading tilde, read in a program that may reassign HOME", () => {
    /** Parse `<program>; echo <argument>` and read that last argument. */
    async function argWordAfter(program: string, argument: string) {
      const parser = await getParser();
      const tree = parser.parse(`${program}; echo ${argument}`);
      if (!tree) throw new Error("parse returned null");
      try {
        const root = tree.rootNode;
        const node = root.child(root.childCount - 1)?.child(1);
        if (!node) throw new Error(`no argument node in: echo ${argument}`);
        return new WordReader(ShellVariables.scan([root])).argWord(node);
      } finally {
        tree.delete();
      }
    }

    it.each([
      ["a bare tilde", "~"],
      ["a tilde path", "~/x"],
      ["a tilde path concatenated with a quoted part", '~/x"y"'],
    ])(
      "marks %s computed and maybe an option once HOME is reassigned (%s)",
      async (_label, argument) => {
        await expect(argWordAfter("HOME=-x", argument)).resolves.toMatchObject({
          computed: true,
          mayLeadWithDash: true,
        });
      },
    );

    it.each([
      ["a tilde after a literal, which bash leaves alone", "a~/x"],
      ["a quoted tilde", '"~/x"'],
      ["a named user's tilde, which reads no HOME", "~root/x"],
    ])(
      "leaves %s exact once HOME is reassigned (%s)",
      async (_label, argument) => {
        await expect(argWordAfter("HOME=-x", argument)).resolves.toMatchObject({
          computed: false,
          mayLeadWithDash: false,
        });
      },
    );

    describe("with an inherited HOME that begins with a dash", () => {
      beforeEach(() => {
        vi.stubEnv("HOME", "-h");
      });
      afterEach(() => {
        vi.unstubAllEnvs();
      });

      it.each([
        ["a bare tilde", "~"],
        ["a tilde path", "~/x"],
        ["a glob under a tilde path", "~/*.ts"],
      ])(
        "reads %s as computed and maybe an option (%s)",
        async (_label, argument) => {
          await expect(argWordAfter("PWD=/x", argument)).resolves.toMatchObject(
            { computed: true, mayLeadWithDash: true },
          );
        },
      );
    });

    it("leaves a tilde path exact when only PWD is reassigned", async () => {
      await expect(argWordAfter("PWD=-x", "~/x")).resolves.toEqual({
        value: "~/x",
        computed: false,
        mayLeadWithDash: false,
      });
    });
  });

  describe("whether a computed word may lead with a dash", () => {
    /** The two facts a guard reads, without the unresolved source spelling. */
    async function shapeOf(argument: string) {
      const { computed, mayLeadWithDash } = await argWordOf(argument);
      return { computed, mayLeadWithDash };
    }

    describe("a word that may reach the program beginning with `-`", () => {
      it.each([
        ["a bare variable", "$A"],
        ["a quoted variable", '"$O"'],
        ["a command substitution", "$(echo -o)"],
        ["a backtick substitution", "`echo -o`"],
        ["a quoted variable leading a path", '"$pkg/src"'],
        ["an unquoted variable after a literal, which splits", "x$y"],
        ["an unquoted variable after an empty string", '""$x'],
        ["an unquoted variable after a single-quoted literal", "'x'$y"],
        // A quoted `$@` still expands to one word per element, and only the
        // first carries the literal prefix.
        ["quoted positional parameters after a literal", '"x$@"'],
        ["a quoted array expansion after a literal", '"x${arr[@]}"'],
        ["a quoted positional slice after a literal", '"x${@:2}"'],
        // An indirect expansion names its target at run time, and `a='arr[@]'`
        // expands per element with no `@` in the source.
        ["a quoted indirect expansion after a literal", '"x${!a}"'],
        ["an indirect expansion nested in a default", '"x${y:-${!b}}"'],
        // Any variable may be a nameref (`declare -n s='arr[@]'`), which
        // expands per element with nothing in its own spelling to show it.
        ["a quoted variable after a literal", '"x$y"'],
        ["a quoted braced variable after a literal", '"x${y}"'],
        // `$*` and `${arr[*]}` join into one word inside quotes; answering
        // true for them is the conservative over-count of the rule above.
        ["quoted joined positional parameters after a literal", '"x$*"'],
        ["a quoted joined array after a literal", '"x${arr[*]}"'],
        ["a leading glob", "*"],
        ["a dash before a glob", "-*"],
        ["an escaped dash", "\\-delete"],
        ["a leading brace expansion", "{-delete,}"],
        ["an ANSI-C string", "$'-o'"],
        ["an option glued to an ANSI-C string", "-t$'\\t'"],
      ])("answers true for %s (%s)", async (_label, argument) => {
        await expect(shapeOf(argument)).resolves.toEqual({
          computed: true,
          mayLeadWithDash: true,
        });
      });
    });

    describe("a word whose leading literal survives every rewrite", () => {
      it.each([
        ["a quoted command substitution after a literal", '"x$(echo -o)"'],
        ["a quoted arithmetic expansion after a literal", '"x$((1))"'],
        ["a glob after a literal", "packages/*/docs"],
        ["an escaped parenthesis", "\\("],
        ["a brace expansion after a literal", "x{a,-b}"],
        ["a glob under a tilde path", "~/*.ts"],
        ["a process substitution", "<(cmd)"],
        ["a process substitution whose body splits", "<(cmd $a)"],
      ])("answers false for %s (%s)", async (_label, argument) => {
        await expect(shapeOf(argument)).resolves.toEqual({
          computed: true,
          mayLeadWithDash: false,
        });
      });
    });
  });
});
