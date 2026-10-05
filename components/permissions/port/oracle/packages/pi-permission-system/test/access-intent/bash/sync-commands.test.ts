import { homedir } from "node:os";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  resetWarmBashParser,
  warmBashParser,
} from "#src/access-intent/bash/parser";
import { parseBashCommandsSync } from "#src/access-intent/bash/sync-commands";

describe("parseBashCommandsSync", () => {
  beforeEach(() => {
    resetWarmBashParser();
  });
  afterEach(() => {
    resetWarmBashParser();
  });

  it("returns null when the parser is not warm", () => {
    expect(parseBashCommandsSync("echo hi")).toBeNull();
  });

  describe("once warm", () => {
    beforeEach(async () => {
      await warmBashParser();
    });

    it("withholds a wrapped reader's exemption once its argument's HOME is reassigned", () => {
      expect(parseBashCommandsSync('xargs find "$HOME"')).toEqual([
        {
          text: 'xargs find "$HOME"',
          wrapperKind: "indirection",
          executedUnit: 'find "$HOME"',
          floorExemption: "core-reader",
        },
      ]);
      expect(parseBashCommandsSync('HOME=-delete; xargs find "$HOME"')).toEqual(
        [
          { text: "HOME=-delete" },
          {
            text: 'xargs find "$HOME"',
            wrapperKind: "indirection",
            executedUnit: 'find "$HOME"',
          },
        ],
      );
    });

    it("returns a single unit for a lone command", () => {
      expect(parseBashCommandsSync("echo hi")).toEqual([{ text: "echo hi" }]);
    });

    it("decomposes a chained command into its units", () => {
      expect(parseBashCommandsSync("cd /repo && npm install x")).toEqual([
        { text: "cd /repo" },
        { text: "npm install x" },
      ]);
    });

    it("descends into a command substitution, tagging its context", () => {
      expect(parseBashCommandsSync("echo $(rm -rf /)")).toEqual([
        { text: "echo $(rm -rf /)" },
        { text: "rm -rf /", context: "command_substitution" },
      ]);
    });

    it("flags an opaque wrapper", () => {
      expect(parseBashCommandsSync('bash -c "rm -rf /"')).toEqual([
        {
          text: 'bash -c "rm -rf /"',
          wrapperKind: "opaque-payload",
          executedUnit: "rm -rf /",
        },
      ]);
    });

    it("returns an empty array for a comment-only command", () => {
      expect(parseBashCommandsSync("# just a comment")).toEqual([]);
    });

    it("returns an empty array for an empty command", () => {
      expect(parseBashCommandsSync("")).toEqual([]);
    });

    it("enumerates a command a partial parse dropped (#875)", () => {
      // Gate parity (#309): the advisory answer must not be weaker than the
      // gate's, and the gate salvages this command through `BashProgram`.
      expect(
        parseBashCommandsSync(
          "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG",
        ),
      ).toEqual([
        { text: "git add -A .", parseUnresolved: true },
        { text: "git commit -F", parseUnresolved: true },
        { text: "rm -rf /tmp/x", parseUnresolved: true, salvaged: true },
        { text: "git add -A .", parseUnresolved: true, salvaged: true },
        { text: "git commit -F", parseUnresolved: true, salvaged: true },
        { text: "rm -rf /tmp/x", parseUnresolved: true, salvaged: true },
      ]);
    });

    it("enumerates a command after a heredoc the grammar cannot parse", () => {
      expect(
        parseBashCommandsSync("cat <<EOF ; rm -rf /tmp/x\nb\nEOF"),
      ).toEqual([
        { text: "cat", parseUnresolved: true },
        { text: "cat", parseUnresolved: true, salvaged: true },
        { text: "rm -rf /tmp/x", parseUnresolved: true, salvaged: true },
      ]);
    });

    describe("a unit opening with a home prefix carries its home spelling", () => {
      it("spells a command named through ~", () => {
        expect(parseBashCommandsSync("~/bin/x --y")).toEqual([
          { text: "~/bin/x --y", spellings: [`${homedir()}/bin/x --y`] },
        ]);
      });

      it("spells a command named through $HOME", () => {
        expect(parseBashCommandsSync("$HOME/bin/x")).toEqual([
          { text: "$HOME/bin/x", spellings: [`${homedir()}/bin/x`] },
        ]);
      });

      it("withholds the spelling once the program rebinds HOME", () => {
        expect(parseBashCommandsSync("HOME=/tmp/evil; ~/bin/x")).toEqual([
          { text: "HOME=/tmp/evil" },
          { text: "~/bin/x" },
        ]);
      });

      it("withholds the spelling under a prefix assignment of HOME", () => {
        expect(parseBashCommandsSync("HOME=/tmp/evil ~/bin/x")).toEqual([
          { text: "~/bin/x" },
        ]);
      });

      it.each(["echo ~/x", '"~/bin/x"'])(
        "does not spell a unit whose text does not open with the prefix: %s",
        (command) => {
          expect(parseBashCommandsSync(command)).toEqual([{ text: command }]);
        },
      );

      it("does not spell a wrapper whose wrapped command opens with the prefix", () => {
        const units = parseBashCommandsSync("sudo ~/bin/x");
        expect(units?.map((unit) => unit.spellings)).toEqual([undefined]);
      });

      it("spells a nested command on its own, not its enclosing one", () => {
        expect(parseBashCommandsSync("echo $(~/bin/x)")).toEqual([
          { text: "echo $(~/bin/x)" },
          {
            text: "~/bin/x",
            context: "command_substitution",
            spellings: [`${homedir()}/bin/x`],
          },
        ]);
      });
    });
  });
});
