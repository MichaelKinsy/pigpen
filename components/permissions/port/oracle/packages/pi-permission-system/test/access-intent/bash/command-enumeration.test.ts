import { describe, expect, it } from "vitest";
import { inlineShellPayloadNode } from "#src/access-intent/bash/command-enumeration";
import { WordReader } from "#src/access-intent/bash/node-text";
import { getParser, type TSNode } from "#src/access-intent/bash/parser";
import { ShellVariables } from "#src/access-intent/bash/shell-variable-expansion";

/**
 * Parse a bash snippet, find its first `command` node, and ask what inline-shell
 * payload it carries — as `{ text, startIndex }`, the two facts the log's
 * command masker reads off the node.
 */
async function payloadOf(
  command: string,
): Promise<{ text: string; startIndex: number } | null> {
  const parser = await getParser();
  const tree = parser.parse(command);
  if (!tree) throw new Error("parser.parse returned null");
  try {
    const commandNode = findFirst(tree.rootNode, "command");
    if (!commandNode) throw new Error(`no command node in ${command}`);
    const payload = inlineShellPayloadNode(
      commandNode,
      new WordReader(ShellVariables.UNREBOUND),
    );
    return payload === null
      ? null
      : { text: payload.text, startIndex: payload.startIndex };
  } finally {
    tree.delete();
  }
}

function findFirst(node: TSNode, type: string): TSNode | null {
  if (node.type === type) return node;
  for (let i = 0; i < node.childCount; i++) {
    const child = node.child(i);
    const found = child ? findFirst(child, type) : null;
    if (found) return found;
  }
  return null;
}

describe("inlineShellPayloadNode", () => {
  describe("a shell or eval running an inline program", () => {
    it.each([
      [`bash -c 'rm -rf /'`, `'rm -rf /'`, 8],
      [`sh -c "ls -la"`, `"ls -la"`, 6],
      [`eval "rm x"`, `"rm x"`, 5],
      [`bash -ec 'make build'`, `'make build'`, 9],
      [`/bin/bash -c 'ls'`, `'ls'`, 13],
      [`bash -c TOKEN=abc`, `TOKEN=abc`, 8],
    ])("names the payload node of %s", async (command, text, startIndex) => {
      await expect(payloadOf(command)).resolves.toEqual({ text, startIndex });
    });

    it("skips a leading assignment prefix, so the index is not shifted by it", async () => {
      await expect(
        payloadOf(`TOKEN=outer bash -c 'API_KEY=inner x'`),
      ).resolves.toEqual({ text: `'API_KEY=inner x'`, startIndex: 20 });
    });
  });

  describe("a command carrying no inline program", () => {
    it.each([
      [`ls -la`, "an ordinary command"],
      [`sudo ls`, "an indirection wrapper hides no payload"],
      [`python3 -c 'print(1)'`, "an interpreter is not a shell"],
      [`node -e 'x'`, "an interpreter is not a shell"],
      [`bash script.sh`, "a shell running a script file"],
      [`bash --help`, "a long option is not a -c cluster"],
      [`bash -c`, "the payload position is vacant"],
      [`eval`, "the payload position is vacant"],
    ])("answers null for %s (%s)", async (command) => {
      await expect(payloadOf(command)).resolves.toBeNull();
    });
  });
});
