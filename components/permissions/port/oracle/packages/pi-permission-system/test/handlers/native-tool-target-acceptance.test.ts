/**
 * Acceptance: a path rule applies to the file a built-in Pi tool opens.
 *
 * Pi's `read`/`write`/`edit`/`ls`/`find`/`grep` resolve their `path`
 * argument through Pi's own resolver, which rewrites some spellings (Unicode
 * spaces, `file://` URLs) and, for `read`, tries variant spellings that exist
 * (macOS AM/PM, curly quote). Exercised end-to-end with real files and the
 * real `PermissionManager` + `PermissionResolver`, so a deny keyed on the file
 * on disk must fire however the model spelled it.
 */

import {
  mkdirSync,
  mkdtempSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  type GateDescriptor,
  isGateDescriptor,
} from "#src/handlers/gates/descriptor";
import { describeExternalDirectoryGate } from "#src/handlers/gates/external-directory";
import { describePathGate } from "#src/handlers/gates/path";
import { ToolCallGatePipeline } from "#src/handlers/gates/tool-call-gate-pipeline";
import type { ToolCallContext } from "#src/handlers/gates/types";
import { pathFlavorForPlatform } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";
import { PermissionResolver } from "#src/policy/permission-resolver";
import { SessionRules } from "#src/session/session-rules";
import type { ScopeConfig } from "#src/types";
import { makeGateInputs, makeGateRunner } from "#test/helpers/gate-fixtures";
import { createManager } from "#test/helpers/manager-harness";

let cwd: string;
let normalizer: PathNormalizer;
const cleanups: Array<() => void> = [];

beforeEach(() => {
  cwd = realpathSync(mkdtempSync(join(tmpdir(), "native-target-acc-")));
  normalizer = new PathNormalizer(pathFlavorForPlatform(process.platform), cwd);
  cleanups.push(() => {
    rmSync(cwd, { recursive: true, force: true });
  });
});

afterEach(() => {
  while (cleanups.length > 0) cleanups.pop()?.();
});

function onDisk(name: string): string {
  const file = join(cwd, name);
  writeFileSync(file, "x");
  return file;
}

function makeResolver(config: ScopeConfig): PermissionResolver {
  const { manager, cleanup } = createManager(config);
  cleanups.push(cleanup);
  manager.configureForCwd(cwd);
  return new PermissionResolver(manager, new SessionRules());
}

function readCall(path: string): ToolCallContext {
  return {
    toolName: "read",
    agentName: null,
    input: { path },
    toolCallId: "tc-1",
    cwd,
  };
}

/** Each spelling a built-in `read` resolves to a different file on disk than it names. */
const REWRITTEN_SPELLINGS: Array<[string, string, (file: string) => string]> = [
  ["a file URL", "secret.txt", (file) => pathToFileURL(file).href],
  [
    "a percent-encoded file URL",
    "secret.txt",
    (file) => pathToFileURL(file).href.replace("secret", "s%65cret"),
  ],
  [
    "a no-break space for a space",
    "my file.txt",
    (file) => file.replace(" ", "\u00A0"),
  ],
  [
    "a straight quote for a curly one",
    "d\u2019x.txt",
    (file) => file.replace("\u2019", "'"),
  ],
  [
    "a space for the AM/PM narrow no-break space",
    "Shot 1.02.03\u202FPM.png",
    (file) => file.replace("\u202F", " "),
  ],
];

describe("a path rule applies to the file a built-in tool opens", () => {
  describe("the path gate", () => {
    it.each(REWRITTEN_SPELLINGS)(
      "denies %s of a denied file",
      (_label, name, spell) => {
        const file = onDisk(name);
        const resolver = makeResolver({
          permission: { "*": "allow", path: { "*": "allow", [file]: "deny" } },
        });

        const result = describePathGate(
          readCall(spell(file)),
          resolver,
          normalizer,
        );

        expect(isGateDescriptor(result)).toBe(true);
        expect((result as GateDescriptor).preCheck?.state).toBe("deny");
      },
    );

    it.each(REWRITTEN_SPELLINGS)(
      "lets %s through when the allow names the file it opens",
      (_label, name, spell) => {
        const file = onDisk(name);
        const resolver = makeResolver({
          permission: { "*": "allow", path: { "*": "deny", [file]: "allow" } },
        });

        expect(
          describePathGate(readCall(spell(file)), resolver, normalizer),
        ).toBeNull();
      },
    );

    it("judges a `..` escape as the file it opens, not its as-typed parent", () => {
      const file = onDisk("secret.txt");
      const resolver = makeResolver({
        permission: {
          "*": "allow",
          path: { "*": "allow", [file]: "deny", [`${cwd}/public/*`]: "allow" },
        },
      });
      const dotDotSpelling = `${cwd}/public/../secret.txt`;

      const result = describePathGate(
        readCall(dotDotSpelling),
        resolver,
        normalizer,
      ) as GateDescriptor;

      expect(result.preCheck?.state).toBe("deny");
      expect(
        normalizer.forToolPath("read", dotDotSpelling).matchValues(),
      ).not.toContain(dotDotSpelling);
    });

    it("discloses the file a fallback opens in the ask", () => {
      const file = onDisk("d\u2019x.txt");
      const resolver = makeResolver({
        permission: { "*": "allow", path: { "*": "ask" } },
      });

      const result = describePathGate(
        readCall(file.replace("\u2019", "'")),
        resolver,
        normalizer,
      ) as GateDescriptor;

      expect(result.payload.evidence).toEqual([
        { label: "resolves to", text: file, detail: null },
      ]);
    });

    it("approves the directory of the file a fallback opens for the session", () => {
      const curlyDir = join(cwd, "q\u2019s");
      mkdirSync(curlyDir);
      writeFileSync(join(curlyDir, "a.txt"), "x");
      const resolver = makeResolver({
        permission: { "*": "allow", path: { "*": "ask" } },
      });

      const result = describePathGate(
        readCall(join(cwd, "q's", "a.txt")),
        resolver,
        normalizer,
      ) as GateDescriptor;

      expect(result.sessionApproval?.grants).toEqual([
        { surface: "path_read", pattern: `${curlyDir}/*` },
      ]);
    });
  });

  describe("the external_directory gate", () => {
    function outsideFile(name: string): string {
      const dir = realpathSync(mkdtempSync(join(tmpdir(), "native-outside-")));
      cleanups.push(() => {
        rmSync(dir, { recursive: true, force: true });
      });
      const file = join(dir, name);
      writeFileSync(file, "x");
      return file;
    }

    it("asks for an outside file spelled as a file URL", () => {
      const file = outsideFile("secret.txt");
      const resolver = makeResolver({
        permission: { "*": "allow", external_directory: { "*": "ask" } },
      });

      const result = describeExternalDirectoryGate(
        readCall(pathToFileURL(file).href),
        { dirs: [], excludedDirs: [] },
        resolver,
        normalizer,
      );

      expect(isGateDescriptor(result)).toBe(true);
      expect((result as GateDescriptor).preCheck?.state).toBe("ask");
    });

    it("discloses the outside file a file URL names in the ask", () => {
      const file = outsideFile("secret.txt");
      const resolver = makeResolver({
        permission: { "*": "allow", external_directory: { "*": "ask" } },
      });

      const result = describeExternalDirectoryGate(
        readCall(pathToFileURL(file).href),
        { dirs: [], excludedDirs: [] },
        resolver,
        normalizer,
      ) as GateDescriptor;

      expect(result.payload.evidence).toEqual([
        { label: "resolves to", text: file, detail: null },
        { label: "working directory", text: cwd, detail: null },
      ]);
    });

    it("allows a rewritten spelling of an outside file the allow names", () => {
      const file = outsideFile("my file.txt");
      const resolver = makeResolver({
        permission: {
          "*": "allow",
          external_directory: { "*": "ask", [file]: "allow" },
        },
      });

      const result = describeExternalDirectoryGate(
        readCall(file.replace(" ", "\u00A0")),
        { dirs: [], excludedDirs: [] },
        resolver,
        normalizer,
      );

      expect((result as GateDescriptor).preCheck?.state).toBe("allow");
    });
  });

  describe("the per-tool gate", () => {
    it.each(REWRITTEN_SPELLINGS)(
      "blocks %s of a file the read rule denies",
      async (_label, name, spell) => {
        const file = onDisk(name);
        const resolver = makeResolver({
          permission: { "*": "allow", read: { "*": "allow", [file]: "deny" } },
        });
        const pipeline = new ToolCallGatePipeline(
          resolver,
          makeGateInputs({ getPathNormalizer: () => normalizer }),
        );
        const { runner } = makeGateRunner();

        const outcome = await pipeline.evaluate(readCall(spell(file)), runner);

        expect(outcome.action).toBe("block");
      },
    );
  });
});
