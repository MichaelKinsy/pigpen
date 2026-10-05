import { describe, expect, it } from "vitest";

import {
  type ApprovalGrant,
  grantTargets,
  provenDirectionOf,
  widenGrant,
} from "#src/session/approval-grant";

describe("widenGrant", () => {
  it("folds a directional surface to its bare family, keeping the pattern", () => {
    expect(
      widenGrant({ surface: "external_directory_write", pattern: "/tmp/*" }),
    ).toEqual({ surface: "external_directory", pattern: "/tmp/*" });
  });

  it("folds the other direction the same way", () => {
    expect(widenGrant({ surface: "path_read", pattern: "/tmp/*" })).toEqual({
      surface: "path",
      pattern: "/tmp/*",
    });
  });

  it("leaves a grant that already names a family alone", () => {
    const grant: ApprovalGrant = {
      surface: "external_directory",
      pattern: "/tmp/*",
    };
    expect(widenGrant(grant)).toBe(grant);
  });

  it("leaves a non-directional surface alone", () => {
    const grant: ApprovalGrant = { surface: "bash", pattern: "git *" };
    expect(widenGrant(grant)).toBe(grant);
  });
});

describe("provenDirectionOf", () => {
  it("names the direction a single directional grant proves", () => {
    expect(
      provenDirectionOf([
        { surface: "external_directory_write", pattern: "/tmp/*" },
      ]),
    ).toBe("write");
  });

  it("names the shared direction when every grant agrees", () => {
    expect(
      provenDirectionOf([
        { surface: "external_directory_read", pattern: "/tmp/a/*" },
        { surface: "external_directory_read", pattern: "/tmp/b/*" },
        { surface: "external_directory_read", pattern: "/tmp/c/*" },
      ]),
    ).toBe("read");
  });

  it("answers null when the grants disagree", () => {
    expect(
      provenDirectionOf([
        { surface: "external_directory_read", pattern: "/tmp/a/*" },
        { surface: "external_directory_write", pattern: "/tmp/b/*" },
      ]),
    ).toBeNull();
  });

  it("answers null when a later grant is non-directional", () => {
    expect(
      provenDirectionOf([
        { surface: "external_directory_read", pattern: "/tmp/a/*" },
        { surface: "external_directory", pattern: "/tmp/b/*" },
      ]),
    ).toBeNull();
  });

  it("answers null when the only grant is non-directional", () => {
    expect(
      provenDirectionOf([{ surface: "bash", pattern: "git *" }]),
    ).toBeNull();
  });

  it("answers null for no grants at all", () => {
    expect(provenDirectionOf([])).toBeNull();
  });
});

describe("grantTargets", () => {
  it("names one target per distinct pattern", () => {
    expect(
      grantTargets([
        { surface: "external_directory_read", pattern: "/r/*" },
        { surface: "external_directory_read", pattern: "/r/*" },
      ]),
    ).toEqual(["/r/*"]);
  });

  it("folds a directory's exact grant into its contents grant", () => {
    expect(
      grantTargets([
        { surface: "external_directory_read", pattern: "/r/a" },
        { surface: "external_directory_read", pattern: "/r/a/*" },
      ]),
    ).toEqual(["/r/a/*"]);
  });

  it("folds a win32 directory pair spelled with backslashes", () => {
    expect(
      grantTargets([
        { surface: "path", pattern: "C:\\r\\a" },
        { surface: "path", pattern: "C:\\r\\a\\*" },
      ]),
    ).toEqual(["C:\\r\\a\\*"]);
  });

  it("keeps a pair apart when the two grants name different surfaces", () => {
    expect(
      grantTargets([
        { surface: "path_read", pattern: "/r/a" },
        { surface: "path_write", pattern: "/r/a/*" },
      ]),
    ).toEqual(["/r/a", "/r/a/*"]);
  });

  it("keeps an exact grant whose sibling is not its own contents", () => {
    expect(
      grantTargets([
        { surface: "path_read", pattern: "/r/a" },
        { surface: "path_read", pattern: "/r/ab/*" },
      ]),
    ).toEqual(["/r/a", "/r/ab/*"]);
  });
});
