import { homedir } from "node:os";
import { describe, expect, it } from "vitest";
import { getParser, type TSNode } from "#src/access-intent/bash/parser";
import { ShellVariables } from "#src/access-intent/bash/shell-variable-expansion";
import { makeTSNode } from "#test/helpers/fake-ts-node";

/** `$NAME` as tree-sitter-bash builds it: a `$` delimiter plus the name. */
function simpleExpansion(name: string): TSNode {
  return makeTSNode("simple_expansion", `$${name}`, [
    makeTSNode("$", "$"),
    makeTSNode("variable_name", name),
  ]);
}

/** `${NAME}` as tree-sitter-bash builds it: brace delimiters plus the name. */
function bracedExpansion(name: string): TSNode {
  return makeTSNode("expansion", `\${${name}}`, [
    makeTSNode("${", "${"),
    makeTSNode("variable_name", name),
    makeTSNode("}", "}"),
  ]);
}

function findNodeOfType(node: TSNode, type: string): TSNode | null {
  if (node.type === type) return node;
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    const found = child ? findNodeOfType(child, type) : null;
    if (found) return found;
  }
  return null;
}

describe("ShellVariables.resolveReference", () => {
  const variables = ShellVariables.UNREBOUND;

  describe("resolvable variables", () => {
    it("resolves $HOME to the OS home directory", () => {
      expect(variables.resolveReference(simpleExpansion("HOME"))).toBe(
        homedir(),
      );
    });

    it("resolves ${HOME} to the OS home directory", () => {
      expect(variables.resolveReference(bracedExpansion("HOME"))).toBe(
        homedir(),
      );
    });

    it("resolves $PWD to the base-relative marker", () => {
      // The shell's working directory is the projection's effective base, so
      // the base-relative form resolves correctly after any `cd` folding
      // without threading a base into this pure function.
      expect(variables.resolveReference(simpleExpansion("PWD"))).toBe(".");
    });

    it("resolves ${PWD} to the base-relative marker", () => {
      expect(variables.resolveReference(bracedExpansion("PWD"))).toBe(".");
    });
  });

  describe("variables outside the resolvable set", () => {
    it.each(["HOMEDIR", "CURRENT", "PATH", "PWDX", "TMPDIR"])(
      "leaves $%s unresolved",
      (name) => {
        expect(variables.resolveReference(simpleExpansion(name))).toBeNull();
        expect(variables.resolveReference(bracedExpansion(name))).toBeNull();
      },
    );
  });

  describe("expansions carrying an operator", () => {
    it("leaves ${HOME:-/tmp} unresolved", () => {
      const node = makeTSNode("expansion", "${HOME:-/tmp}", [
        makeTSNode("${", "${"),
        makeTSNode("variable_name", "HOME"),
        makeTSNode(":-", ":-"),
        makeTSNode("word", "/tmp"),
        makeTSNode("}", "}"),
      ]);
      expect(variables.resolveReference(node)).toBeNull();
    });

    it("leaves ${#HOME} unresolved", () => {
      const node = makeTSNode("expansion", "${#HOME}", [
        makeTSNode("${", "${"),
        makeTSNode("#", "#"),
        makeTSNode("variable_name", "HOME"),
        makeTSNode("}", "}"),
      ]);
      expect(variables.resolveReference(node)).toBeNull();
    });
  });

  describe("nodes that are not a plain variable reference", () => {
    it("returns null for a node with no children", () => {
      expect(
        variables.resolveReference(makeTSNode("simple_expansion", "$HOME")),
      ).toBeNull();
    });

    it("returns null for a node with no variable_name child", () => {
      const node = makeTSNode("expansion", "${}", [
        makeTSNode("${", "${"),
        makeTSNode("}", "}"),
      ]);
      expect(variables.resolveReference(node)).toBeNull();
    });

    it("returns null for a variable_assignment naming a resolvable variable", () => {
      // `HOME=/tmp` binds the name; it is not a reference to its value.
      const node = makeTSNode("variable_assignment", "HOME=/tmp", [
        makeTSNode("variable_name", "HOME"),
        makeTSNode("=", "="),
        makeTSNode("word", "/tmp"),
      ]);
      expect(variables.resolveReference(node)).toBeNull();
    });
  });

  describe("fidelity to the shapes tree-sitter-bash actually produces", () => {
    it.each([
      ["ls $HOME", "simple_expansion", homedir()],
      ["ls ${HOME}", "expansion", homedir()],
      ["ls $PWD", "simple_expansion", "."],
      ["ls ${PWD}", "expansion", "."],
      ["ls ${HOME:-/tmp}", "expansion", null],
      ["ls ${#HOME}", "expansion", null],
      ["ls $HOMEDIR", "simple_expansion", null],
    ])("resolves %s to %s", async (command, nodeType, expected) => {
      const parser = await getParser();
      const tree = parser.parse(command);
      expect(tree).not.toBeNull();
      if (!tree) return;
      try {
        const node = findNodeOfType(tree.rootNode, nodeType);
        expect(node).not.toBeNull();
        if (!node) return;
        expect(variables.resolveReference(node)).toBe(expected);
      } finally {
        tree.delete();
      }
    });
  });
});

describe("ShellVariables.scan", () => {
  /** Which of HOME and PWD `command` rebinds, read through a plain reference. */
  async function reboundIn(command: string): Promise<string[]> {
    const parser = await getParser();
    const tree = parser.parse(command);
    if (!tree) throw new Error("parser.parse returned null");
    try {
      const variables = ShellVariables.scan([tree.rootNode]);
      return ["HOME", "PWD"].filter(
        (name) => variables.resolveReference(simpleExpansion(name)) === null,
      );
    } finally {
      tree.delete();
    }
  }

  describe("a variable_name outside a plain reference rebinds it", () => {
    it.each([
      ["HOME=/etc", ["HOME"]],
      ["HOME+=/x", ["HOME"]],
      ["HOME=/etc cat x", ["HOME"]],
      ["export HOME=/etc", ["HOME"]],
      ["local HOME", ["HOME"]],
      ["readonly PWD=/etc", ["PWD"]],
      ["for HOME in /etc; do :; done", ["HOME"]],
      ["unset HOME", ["HOME"]],
      ["(( HOME = 1 ))", ["HOME"]],
      ["echo ${HOME:=/etc}", ["HOME"]],
      ["f() { PWD=/; }; HOME=/etc", ["HOME", "PWD"]],
    ])("%s rebinds %j", async (command, expected) => {
      expect(await reboundIn(command)).toEqual(expected);
    });
  });

  describe("a builtin that binds a name it is given as a word rebinds it", () => {
    it.each([
      ["read HOME", ["HOME"]],
      ["printf -v PWD x", ["PWD"]],
      ["declare -n r=HOME; r=/etc", ["HOME"]],
      ['read "HOME"', ["HOME"]],
      ["read -r HOME", ["HOME"]],
      ["mapfile HOME", ["HOME"]],
      ["getopts ab HOME", ["HOME"]],
      ['export "HOME=/etc"', ["HOME"]],
      ["declare 'HOME=/etc'", ["HOME"]],
      ["let HOME=1", ["HOME"]],
      ["let 'PWD=1'", ["PWD"]],
      ["let HOME++", ["HOME"]],
    ])("%s rebinds %j", async (command, expected) => {
      expect(await reboundIn(command)).toEqual(expected);
    });
  });

  describe("a command that runs code it cannot see rebinds both", () => {
    it.each([
      "eval x",
      "source f",
      ". f",
      '"eval" x',
      "e\\val x",
      "trap 'HOME=/etc' DEBUG",
    ])("%s", async (command) => {
      expect(await reboundIn(command)).toEqual(["HOME", "PWD"]);
    });
  });

  describe("a program that only reads them rebinds nothing", () => {
    it.each([
      'cat "$HOME/x" $PWD',
      "echo ${HOME} ${PWD}",
      'env -i HOME="$HOME" cmd',
      "HOMEDIR=/etc MY_PWD=/x cmd",
      "echo HOMEDIR",
      "echo HOME",
      "grep HOME ~/.bashrc",
      "printf HOME",
      "git log -- PWD",
      "find . -name x",
      "echo eval source",
    ])("%s", async (command) => {
      expect(await reboundIn(command)).toEqual([]);
    });
  });

  describe("a path token spelled from a rebound HOME", () => {
    async function spellsReboundHome(
      command: string,
      token: string,
    ): Promise<boolean> {
      const parser = await getParser();
      const tree = parser.parse(command);
      if (!tree) throw new Error("parser.parse returned null");
      try {
        return ShellVariables.scan([tree.rootNode]).spellsReboundHome(token);
      } finally {
        tree.delete();
      }
    }

    it.each(["$HOME", "$HOME/x", "${HOME}", "${HOME}/x", "~", "~/x"])(
      "%s is, once HOME is rebound",
      async (token) => {
        expect(await spellsReboundHome("HOME=/etc", token)).toBe(true);
        expect(await spellsReboundHome("PWD=/etc", token)).toBe(false);
      },
    );

    it.each(["$HOMEDIR/x", "~user/x", "/etc/x", "x/$HOME"])(
      "%s is not, even once HOME is rebound",
      async (token) => {
        expect(await spellsReboundHome("HOME=/etc", token)).toBe(false);
      },
    );
  });

  it("reads every root it is given", async () => {
    const parser = await getParser();
    const first = parser.parse("cat x");
    const second = parser.parse("HOME=/etc");
    if (!first || !second) throw new Error("parser.parse returned null");
    try {
      const variables = ShellVariables.scan([first.rootNode, second.rootNode]);
      expect(variables.resolveReference(simpleExpansion("HOME"))).toBeNull();
    } finally {
      first.delete();
      second.delete();
    }
  });
});

describe("ShellVariables.spellHomeAtStart", () => {
  /** The spelling `text` gets inside the program `command`. */
  async function spellingIn(
    command: string,
    text: string,
  ): Promise<string | undefined> {
    const parser = await getParser();
    const tree = parser.parse(command);
    if (!tree) throw new Error("parser.parse returned null");
    try {
      return ShellVariables.scan([tree.rootNode]).spellHomeAtStart(text);
    } finally {
      tree.delete();
    }
  }

  describe("a leading home prefix is spelled as the startup home", () => {
    const variables = ShellVariables.UNREBOUND;

    it.each([
      ["~/bin/x", "/bin/x"],
      ["~", ""],
      ["$HOME/bin/x --y", "/bin/x --y"],
      ["${HOME}/bin/x", "/bin/x"],
      ["$HOME", ""],
    ])("%s", (text, rest) => {
      expect(variables.spellHomeAtStart(text)).toBe(`${homedir()}${rest}`);
    });

    it("keeps the rest of the text verbatim rather than normalizing it as a path", () => {
      expect(variables.spellHomeAtStart("~/evil /x/../../safe")).toBe(
        `${homedir()}/evil /x/../../safe`,
      );
    });
  });

  describe("text bash does not expand from HOME gets no spelling", () => {
    const variables = ShellVariables.UNREBOUND;

    it.each([
      "~\\bin\\x",
      "~user/x",
      "$HOMEDIR/x",
      "${HOME:-/tmp}/x",
      "echo ~/x",
      '"~/bin/x"',
      "/abs/bin/x",
    ])("%s", (text) => {
      expect(variables.spellHomeAtStart(text)).toBeUndefined();
    });
  });

  describe("a program that rebinds HOME gets no spelling", () => {
    it.each(["HOME=/tmp; ~/x", "export HOME=/tmp", "HOME=/tmp ~/x"])(
      "%s",
      async (command) => {
        expect(await spellingIn(command, "~/x")).toBeUndefined();
        expect(await spellingIn(command, "$HOME/x")).toBeUndefined();
      },
    );

    it("spells it when the program rebinds only PWD", async () => {
      expect(await spellingIn("PWD=/tmp; ~/x", "~/x")).toBe(`${homedir()}/x`);
    });
  });
});
