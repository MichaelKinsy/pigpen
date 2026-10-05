import {
  existsSync,
  mkdtempSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import {
  type NativeToolTarget,
  resolveNativeToolTarget,
} from "#src/path/native-tool-target";
import { posixPathFlavor, win32PathFlavor } from "#src/path/path-flavor";

// Pi's `exports` map publishes only `.`, so the parity oracle reads the pinned
// dependency's compiled module by path.
/* eslint-disable local-rules/no-parent-relative-imports -- no alias reaches a dependency's unexported module */
import {
  resolveReadPath,
  resolveToCwd,
} from "../../node_modules/@earendil-works/pi-coding-agent/dist/core/tools/path-utils.js";

/* eslint-enable local-rules/no-parent-relative-imports */

const CWD = "/projects/app";

/** Resolve with an injected set of existing absolute paths. */
function resolveWith(
  rawPath: string,
  options: { existing?: readonly string[]; readFallbacks?: boolean } = {},
): NativeToolTarget {
  const existing = new Set(options.existing ?? []);
  return resolveNativeToolTarget(rawPath, {
    cwd: CWD,
    flavor: posixPathFlavor,
    readFallbacks: options.readFallbacks ?? true,
    exists: (p) => existing.has(p),
  });
}

describe("resolveNativeToolTarget", () => {
  describe("normalization every built-in tool applies", () => {
    it.each([
      ["a no-break space", "/x/my\u00A0file.txt"],
      ["a figure space", "/x/my\u2007file.txt"],
      ["an ideographic space", "/x/my\u3000file.txt"],
      ["a narrow no-break space", "/x/my\u202Ffile.txt"],
    ])("turns %s into a space", (_label, rawPath) => {
      expect(resolveWith(rawPath).target).toBe("/x/my file.txt");
    });

    it("strips one leading @", () => {
      expect(resolveWith("@src/a.ts").target).toBe("/projects/app/src/a.ts");
      expect(resolveWith("@@a").target).toBe("/projects/app/@a");
    });

    it("expands a bare ~ and ~/", () => {
      expect(resolveWith("~").target).toBe(homedir());
      expect(resolveWith("~/notes.md").target).toBe(
        join(homedir(), "notes.md"),
      );
    });

    it("decodes a file URL, percent-escapes included", () => {
      expect(resolveWith("file:///etc/hosts").target).toBe("/etc/hosts");
      expect(resolveWith("file:///etc/h%6Fsts").target).toBe("/etc/hosts");
    });

    it.each([
      ["$HOME", "$HOME/x", "/projects/app/$HOME/x"],
      // biome-ignore lint/suspicious/noTemplateCurlyInString: a braced shell variable Pi leaves literal, not a template string
      ["${HOME}", "${HOME}/x", "/projects/app/${HOME}/x"],
      ["wrapping quotes", "'x.txt'", "/projects/app/'x.txt'"],
      ["surrounding whitespace", " x.txt ", "/projects/app/ x.txt "],
      ["~user", "~user/x", "/projects/app/~user/x"],
    ])("leaves %s literal, as Pi does", (_label, rawPath, target) => {
      expect(resolveWith(rawPath).target).toBe(target);
    });

    it("resolves a relative spelling against cwd", () => {
      expect(resolveWith("../other/x").target).toBe("/projects/other/x");
    });

    it("translates a win32 drive mount under the win32 flavor", () => {
      const native = resolveNativeToolTarget("/mnt/d/a/b.txt", {
        cwd: "C:\\proj",
        flavor: win32PathFlavor,
        readFallbacks: false,
        exists: () => false,
      });
      expect(native.target).toBe("D:\\a\\b.txt");
      expect(native.rewritten).toBe(true);
    });
  });

  describe("read fallbacks", () => {
    it("tries the AM/PM narrow no-break space variant", () => {
      const onDisk = "/x/Shot 1.02.03\u202FPM.png";
      expect(
        resolveWith("/x/Shot 1.02.03 PM.png", { existing: [onDisk] }).target,
      ).toBe(onDisk);
    });

    it("tries the NFD variant", () => {
      const onDisk = "/x/cafe\u0301.txt";
      expect(
        resolveWith("/x/caf\u00e9.txt", { existing: [onDisk] }).target,
      ).toBe(onDisk);
    });

    it("tries the curly-quote variant", () => {
      const onDisk = "/x/d\u2019x.txt";
      expect(resolveWith("/x/d'x.txt", { existing: [onDisk] }).target).toBe(
        onDisk,
      );
    });

    it("tries the NFD plus curly-quote variant", () => {
      const onDisk = "/x/d\u2019e\u0301.txt";
      expect(
        resolveWith("/x/d'\u00e9.txt", { existing: [onDisk] }).target,
      ).toBe(onDisk);
    });

    it("prefers the typed spelling when it exists", () => {
      expect(
        resolveWith("/x/d'x.txt", {
          existing: ["/x/d'x.txt", "/x/d\u2019x.txt"],
        }).target,
      ).toBe("/x/d'x.txt");
    });

    it("prefers the AM/PM variant over the curly-quote variant, in Pi's order", () => {
      expect(
        resolveWith("/x/d'x 1 PM.png", {
          existing: ["/x/d'x 1\u202FPM.png", "/x/d\u2019x 1 PM.png"],
        }).target,
      ).toBe("/x/d'x 1\u202FPM.png");
    });

    it("keeps the typed spelling when no variant exists", () => {
      expect(resolveWith("/x/d'x.txt").target).toBe("/x/d'x.txt");
    });

    it("tries no variant when the tool has no read fallbacks", () => {
      expect(
        resolveWith("/x/d'x.txt", {
          existing: ["/x/d\u2019x.txt"],
          readFallbacks: false,
        }).target,
      ).toBe("/x/d'x.txt");
    });
  });

  describe("rewritten", () => {
    it.each([
      ["a relative path", "src/a.ts"],
      ["an absolute path", "/abs/x"],
      ["a home-relative path", "~/x"],
      ["an @-prefixed path", "@x"],
      ["a missing file with a quote", "/x/d'x.txt"],
    ])("is false for %s", (_label, rawPath) => {
      expect(resolveWith(rawPath).rewritten).toBe(false);
    });

    it.each([
      ["a Unicode space", "/x/my\u00A0file.txt", []],
      ["a file URL", "file:///etc/hosts", []],
      ["a read fallback", "/x/d'x.txt", ["/x/d\u2019x.txt"]],
    ])("is true for %s", (_label, rawPath, existing) => {
      expect(resolveWith(rawPath, { existing }).rewritten).toBe(true);
    });
  });

  describe("relativeSpelling", () => {
    it("is the normalized relative spelling", () => {
      expect(resolveWith("../other/x").relativeSpelling).toBe("../other/x");
      expect(resolveWith("@src/my\u00A0a.ts").relativeSpelling).toBe(
        "src/my a.ts",
      );
    });

    it("is absent for an absolute spelling", () => {
      expect(resolveWith("/abs/x").relativeSpelling).toBeUndefined();
      expect(resolveWith("~/x").relativeSpelling).toBeUndefined();
    });

    it("is absent after a read fallback", () => {
      expect(
        resolveWith("d'x.txt", { existing: ["/projects/app/d\u2019x.txt"] })
          .relativeSpelling,
      ).toBeUndefined();
    });
  });

  describe("a file URL the platform rejects", () => {
    it("falls back to the spelling resolved against cwd", () => {
      const native = resolveWith("file://host/x");
      expect(native.target).toBe("/projects/app/file:/host/x");
      expect(native.rewritten).toBe(false);
    });
  });
});

describe("parity with Pi's resolver", () => {
  let dir: string;
  let cwd: string;
  let spellings: string[];

  beforeAll(() => {
    dir = realpathSync(mkdtempSync(join(tmpdir(), "native-target-")));
    cwd = realpathSync(mkdtempSync(join(tmpdir(), "native-target-cwd-")));
    for (const name of [
      "my file.txt",
      "d\u2019x.txt",
      "Shot 1.02.03\u202FPM.png",
      "cafe\u0301.txt",
      "secret.txt",
      "q'x.txt",
      "q\u2019x.txt",
      "dang\u2019x.txt",
    ]) {
      writeFileSync(join(dir, name), "x");
    }
    symlinkSync(join(dir, "missing-target"), join(dir, "dang'x.txt"));
    const secret = join(dir, "secret.txt");
    spellings = [
      secret,
      pathToFileURL(secret).href,
      pathToFileURL(secret).href.replace("secret", "s%65cret"),
      join(dir, "my\u00A0file.txt"),
      join(dir, "d'x.txt"),
      join(dir, "Shot 1.02.03 PM.png"),
      join(dir, "caf\u00e9.txt"),
      join(dir, "q'x.txt"),
      join(dir, "dang'x.txt"),
      join(dir, "absent'x.txt"),
      `@${secret}`,
      "relative/x.txt",
      "~/nonexistent-native-target",
      "$HOME/x",
      "'quoted'",
      "file://host/x",
    ];
  });

  afterAll(() => {
    rmSync(dir, { recursive: true, force: true });
    rmSync(cwd, { recursive: true, force: true });
  });

  it("opens the same file as Pi's read for every spelling", () => {
    for (const spelling of spellings) {
      const expected = tryResolve(() => resolveReadPath(spelling, cwd));
      const native = resolveNativeToolTarget(spelling, {
        cwd,
        flavor: posixPathFlavor,
        readFallbacks: true,
        exists: existsSync,
      });
      expect({ spelling, target: native.target }).toEqual({
        spelling,
        target: expected ?? native.target,
      });
    }
  });

  it("opens the same file as Pi's write/edit/ls/find/grep for every spelling", () => {
    for (const spelling of spellings) {
      const expected = tryResolve(() => resolveToCwd(spelling, cwd));
      const native = resolveNativeToolTarget(spelling, {
        cwd,
        flavor: posixPathFlavor,
        readFallbacks: false,
        exists: existsSync,
      });
      expect({ spelling, target: native.target }).toEqual({
        spelling,
        target: expected ?? native.target,
      });
    }
  });
});

/**
 * Pi throws for a file URL the platform rejects, and the tool then opens
 * nothing; there is no target to compare against.
 */
function tryResolve(resolve: () => string): string | undefined {
  try {
    return resolve();
  } catch {
    return undefined;
  }
}
