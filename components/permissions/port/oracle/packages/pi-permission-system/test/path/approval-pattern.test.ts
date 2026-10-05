import { describe, expect, it } from "vitest";

import { deriveApprovalPatterns } from "#src/path/approval-pattern";
import type { PathFlavor } from "#src/path/path-flavor";
import { posixPathFlavor, win32PathFlavor } from "#src/path/path-flavor";
import { evaluate } from "#src/policy/rule";
import { SessionRules } from "#src/session/session-rules";

/** Record the derived patterns as session grants, as the gates do. */
function grantFor(
  surface: string,
  pathValue: string,
  flavor: PathFlavor,
  isDirectory = false,
): SessionRules {
  const session = new SessionRules();
  for (const pattern of deriveApprovalPatterns(
    pathValue,
    flavor,
    isDirectory,
  )) {
    session.approve(surface, pattern);
  }
  return session;
}

describe("deriveApprovalPatterns", () => {
  describe("posix flavor", () => {
    it.each([
      ["/other/project/src/foo.ts", "/other/project/src/*"],
      ["/other/project/src/", "/other/project/src/*"],
      ["/other/project/src", "/other/project/*"],
      ["/", "/*"],
      ["/foo", "/*"],
      ["/dev/null", "/dev/*"],
      ["C:/foo/bar.ts", "C:/foo/*"],
      ["src/.env", "src/*"],
    ])("derives %s -> %s", (value, expected) => {
      expect(deriveApprovalPatterns(value, posixPathFlavor, false)).toEqual([
        expected,
      ]);
    });

    it("treats a backslash as an ordinary filename character", () => {
      expect(
        deriveApprovalPatterns("C:\\foo\\bar.ts", posixPathFlavor, false),
      ).toEqual(["./*"]);
    });

    it("falls back to the current directory for a separator-free value", () => {
      expect(
        deriveApprovalPatterns("index.html", posixPathFlavor, false),
      ).toEqual(["./*"]);
      expect(deriveApprovalPatterns("", posixPathFlavor, false)).toEqual([
        "./*",
      ]);
    });
  });

  describe("win32 flavor", () => {
    it.each([
      ["C:\\foo\\bar.ts", "C:\\foo\\*"],
      ["C:\\", "C:\\*"],
      ["C:/foo/bar.ts", "C:/foo/*"],
    ])("derives a native windows path %s -> %s", (value, expected) => {
      expect(deriveApprovalPatterns(value, win32PathFlavor, false)).toEqual([
        expected,
      ]);
    });

    it.each([
      ["/dev/null", "/dev/*"],
      ["/tmp/logs/", "/tmp/logs/*"],
      ["/tmp/logs", "/tmp/*"],
      ["/foo", "/*"],
      ["/", "/*"],
    ])(
      "keeps a Git Bash token's own separator: %s -> %s",
      (value, expected) => {
        expect(deriveApprovalPatterns(value, win32PathFlavor, false)).toEqual([
          expected,
        ]);
      },
    );

    it("falls back to the windows current directory for a separator-free value", () => {
      expect(
        deriveApprovalPatterns("index.html", win32PathFlavor, false),
      ).toEqual([".\\*"]);
    });
  });

  describe("a directory", () => {
    it.each([
      [
        "/r/projects/project-a",
        ["/r/projects/project-a", "/r/projects/project-a/*"],
      ],
      ["/", ["/", "/*"]],
    ])("posix: scopes %s to itself and its contents", (value, expected) => {
      expect(deriveApprovalPatterns(value, posixPathFlavor, true)).toEqual(
        expected,
      );
    });

    it.each([
      ["C:\\r\\a", ["C:\\r\\a", "C:\\r\\a\\*"]],
      ["C:\\", ["C:\\", "C:\\*"]],
      ["C:/r/a", ["C:/r/a", "C:/r/a/*"]],
    ])("win32: scopes %s with its own separator", (value, expected) => {
      expect(deriveApprovalPatterns(value, win32PathFlavor, true)).toEqual(
        expected,
      );
    });
  });

  describe("session-grant round trip", () => {
    it("grants an approved directory and its contents, not its siblings", () => {
      const session = grantFor(
        "external_directory_read",
        "/r/projects/project-a",
        posixPathFlavor,
        true,
      );
      const actionFor = (value: string) =>
        evaluate(
          "external_directory_read",
          value,
          session.getRuleset(),
          posixPathFlavor,
        ).action;
      expect(actionFor("/r/projects/project-a")).toBe("allow");
      expect(actionFor("/r/projects/project-a/src/x.ts")).toBe("allow");
      expect(actionFor("/r/projects/project-b/sample.txt")).toBe("ask");
      expect(actionFor("/r/projects/project-a-evil/x.ts")).toBe("ask");
    });

    it("grants siblings of the approved file", () => {
      const session = grantFor(
        "external_directory_read",
        "/other/project/src/foo.ts",
        posixPathFlavor,
      );
      expect(
        evaluate(
          "external_directory_read",
          "/other/project/src/bar.ts",
          session.getRuleset(),
          posixPathFlavor,
        ).action,
      ).toBe("allow");
    });

    it("does not grant sibling directories", () => {
      const session = grantFor(
        "external_directory_read",
        "/other/project/src/foo.ts",
        posixPathFlavor,
      );
      expect(
        evaluate(
          "external_directory_read",
          "/other/project/lib/bar.ts",
          session.getRuleset(),
          posixPathFlavor,
        ).action,
      ).toBe("ask");
    });

    it("binds a current-directory file to the cwd subtree once resolved", () => {
      // Callers resolve the path to its canonical absolute form before
      // deriving; a current-directory file then yields the cwd glob and
      // excludes siblings (#438).
      const session = grantFor(
        "edit",
        "/test/project/index.html",
        posixPathFlavor,
      );
      expect(
        evaluate(
          "edit",
          "/test/project/index.html",
          session.getRuleset(),
          posixPathFlavor,
        ).action,
      ).toBe("allow");
      expect(
        evaluate("edit", "/etc/passwd", session.getRuleset(), posixPathFlavor)
          .action,
      ).toBe("ask");
    });

    it("bounds a win32 directory token to itself, not its parent", () => {
      // Deriving from win32's own `sep` produced `/tmp\*`, which the
      // `windowsSeparators` fold (#653) matches against every sibling of the
      // approved directory — the grant this pins bounded (#655).
      const session = grantFor(
        "external_directory_read",
        "/tmp/logs/",
        win32PathFlavor,
      );
      expect(
        evaluate(
          "external_directory_read",
          "/tmp/logs/app.log",
          session.getRuleset(),
          win32PathFlavor,
        ).action,
      ).toBe("allow");
      expect(
        evaluate(
          "external_directory_read",
          "/tmp/other/secrets.env",
          session.getRuleset(),
          win32PathFlavor,
        ).action,
      ).toBe("ask");
    });
  });
});
