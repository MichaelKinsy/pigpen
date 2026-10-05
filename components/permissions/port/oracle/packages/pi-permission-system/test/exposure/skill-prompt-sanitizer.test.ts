import { resolve } from "node:path";
import { afterEach, describe, expect, test, vi } from "vitest";
import {
  findSkillPathMatch,
  parseAllSkillPromptSections,
  type SkillPermissionChecker,
  visibleSkillPromptEntries,
  withoutDeniedSkills,
} from "#src/exposure/skill-prompt-sanitizer";
import { posixPathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";
import type { ScopedPermissionManager } from "#src/policy/permission-manager";
import type { PermissionCheckResult } from "#src/types";
import { createManager } from "#test/helpers/manager-harness";

/**
 * Adapt a real `PermissionManager` to the raw `SkillPermissionChecker`
 * contract, mirroring how `PermissionResolver.checkPermission` delegates to
 * `manager.check` with a tool intent (#478).
 */
function asChecker(manager: ScopedPermissionManager): SkillPermissionChecker {
  return {
    checkPermission: (surface, input, agentName) =>
      manager.check({ kind: "tool", surface, input, agentName }),
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

// ── Helpers ────────────────────────────────────────────────────────────────

const CWD = "/projects/my-app";

// `findSkillPathMatch` only uses the normalizer's platform (it compares two
// already-absolute paths via `isWithinDirectory`), so this CWD-baked instance
// serves every call regardless of the entries' cwd.
const normalizer = new PathNormalizer(posixPathFlavor, CWD);

function makeManager(
  defaultState: "allow" | "deny" | "ask" = "allow",
  overrides: Record<string, "allow" | "deny" | "ask"> = {},
): SkillPermissionChecker {
  return {
    checkPermission: vi.fn(
      (_surface: string, input: unknown): PermissionCheckResult => {
        const name = (input as { name?: string }).name ?? "";
        const state = overrides[name] ?? defaultState;
        return { toolName: "skill", state, source: "tool", origin: "builtin" };
      },
    ),
  };
}

function skillBlock(
  name: string,
  location = `/skills/${name}/SKILL.md`,
): string {
  return [
    "  <skill>",
    `    <name>${name}</name>`,
    `    <description>Description of ${name}</description>`,
    `    <location>${location}</location>`,
    "  </skill>",
  ].join("\n");
}

function availableSkillsSection(...names: string[]): string {
  return [
    "<available_skills>",
    ...names.map((n) => skillBlock(n)),
    "</available_skills>",
  ].join("\n");
}

// ── visibleSkillPromptEntries ──────────────────────────────────────────────

describe("visibleSkillPromptEntries", () => {
  test("returns nothing and checks nothing when no skills section is present", () => {
    const manager = makeManager("deny");
    const entries = visibleSkillPromptEntries(
      "You are a helpful assistant.",
      manager,
      null,
      normalizer,
    );
    expect(entries).toEqual([]);
    expect(manager.checkPermission).not.toHaveBeenCalled();
  });

  test("keeps all skills visible when all are allowed", () => {
    const input = availableSkillsSection("librarian", "ask-user");
    const manager = makeManager("allow");
    const entries = visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(entries.map((e) => e.name)).toEqual(["librarian", "ask-user"]);
  });

  test("keeps a denied skill out of the visible entries", () => {
    const input = availableSkillsSection("alpha", "beta");
    const manager = makeManager("allow", { beta: "deny" });
    const entries = visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(entries.map((e) => e.name)).toEqual(["alpha"]);
  });

  test("keeps an ask-state skill visible", () => {
    const input = availableSkillsSection("alpha", "beta");
    const manager = makeManager("allow", { beta: "ask" });
    const entries = visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(entries.map((e) => [e.name, e.state])).toEqual([
      ["alpha", "allow"],
      ["beta", "ask"],
    ]);
  });

  test("classifies every catalogue in the prompt", () => {
    const input = `${availableSkillsSection("alpha")}\n${availableSkillsSection("beta")}`;
    const manager = makeManager("allow", { beta: "deny" });
    const entries = visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(entries.map((e) => e.name)).toEqual(["alpha"]);
  });

  test("delegates permission check to permissionManager for each skill", () => {
    const input = availableSkillsSection("alpha", "beta");
    const manager = makeManager("allow");
    visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(manager.checkPermission).toHaveBeenCalledWith(
      "skill",
      { name: "alpha" },
      undefined,
    );
    expect(manager.checkPermission).toHaveBeenCalledWith(
      "skill",
      { name: "beta" },
      undefined,
    );
  });

  test("passes agentName to permissionManager", () => {
    const input = availableSkillsSection("librarian");
    const manager = makeManager("allow");
    visibleSkillPromptEntries(input, manager, "my-agent", normalizer);
    expect(manager.checkPermission).toHaveBeenCalledWith(
      "skill",
      { name: "librarian" },
      "my-agent",
    );
  });

  test("caches permission result: checkPermission called once per unique skill name", () => {
    // Same skill appears in two separate sections.
    const input = [
      availableSkillsSection("librarian"),
      availableSkillsSection("librarian"),
    ].join("\n");
    const manager = makeManager("allow");
    visibleSkillPromptEntries(input, manager, null, normalizer);
    // Should only be called once despite appearing twice.
    expect(manager.checkPermission).toHaveBeenCalledTimes(1);
  });

  test("resolves entry normalizedLocation relative to cwd", () => {
    const location = "/skills/librarian/SKILL.md";
    const input = availableSkillsSection("librarian");
    const manager = makeManager("allow");
    const entries = visibleSkillPromptEntries(input, manager, null, normalizer);
    expect(entries[0].normalizedLocation).toBe(location);
    expect(entries[0].normalizedBaseDir).toBe("/skills/librarian");
  });
});

// ── findSkillPathMatch ──────────────────────────────────────────────────────

describe("withoutDeniedSkills", () => {
  test("drops a denied skill and keeps the rest in order", () => {
    const manager = makeManager("allow", { beta: "deny" });
    expect(
      withoutDeniedSkills(
        [{ name: "alpha" }, { name: "beta" }, { name: "gamma" }],
        manager,
        null,
      ),
    ).toEqual([{ name: "alpha" }, { name: "gamma" }]);
  });

  test("keeps an ask-state skill", () => {
    const manager = makeManager("allow", { beta: "ask" });
    expect(
      withoutDeniedSkills([{ name: "alpha" }, { name: "beta" }], manager, null),
    ).toEqual([{ name: "alpha" }, { name: "beta" }]);
  });

  test("passes agentName to permissionManager", () => {
    const manager = makeManager("allow");
    withoutDeniedSkills([{ name: "librarian" }], manager, "my-agent");
    expect(manager.checkPermission).toHaveBeenCalledWith(
      "skill",
      { name: "librarian" },
      "my-agent",
    );
  });
});

describe("findSkillPathMatch", () => {
  const entries = [
    {
      name: "librarian",
      description: "desc",
      location: "/skills/librarian/SKILL.md",
      state: "allow" as const,
      normalizedLocation: "/skills/librarian/SKILL.md",
      normalizedBaseDir: "/skills/librarian",
    },
    {
      name: "ask-user",
      description: "desc",
      location: "/skills/ask-user/SKILL.md",
      state: "allow" as const,
      normalizedLocation: "/skills/ask-user/SKILL.md",
      normalizedBaseDir: "/skills/ask-user",
    },
  ];

  test("returns null for empty normalized path", () => {
    expect(findSkillPathMatch("", entries, normalizer)).toBeNull();
  });

  test("returns null for empty entries array", () => {
    expect(
      findSkillPathMatch("/skills/librarian/SKILL.md", [], normalizer),
    ).toBeNull();
  });

  test("matches exact location path", () => {
    const match = findSkillPathMatch(
      "/skills/librarian/SKILL.md",
      entries,
      normalizer,
    );
    expect(match?.name).toBe("librarian");
  });

  test("matches path within skill base directory", () => {
    const match = findSkillPathMatch(
      "/skills/librarian/extra/helper.md",
      entries,
      normalizer,
    );
    expect(match?.name).toBe("librarian");
  });

  test("returns null for path not within any skill directory", () => {
    const match = findSkillPathMatch(
      "/other/path/file.md",
      entries,
      normalizer,
    );
    expect(match).toBeNull();
  });

  test("returns null for sibling path that shares a prefix", () => {
    // "/skills/librarian-extra" should not match "/skills/librarian"
    const match = findSkillPathMatch(
      "/skills/librarian-extra/SKILL.md",
      entries,
      normalizer,
    );
    expect(match).toBeNull();
  });

  test("prefers longer matching base directory (most specific skill wins)", () => {
    const nestedEntries = [
      {
        name: "parent",
        description: "desc",
        location: "/skills/parent/SKILL.md",
        state: "allow" as const,
        normalizedLocation: "/skills/parent/SKILL.md",
        normalizedBaseDir: "/skills/parent",
      },
      {
        name: "child",
        description: "desc",
        location: "/skills/parent/child/SKILL.md",
        state: "allow" as const,
        normalizedLocation: "/skills/parent/child/SKILL.md",
        normalizedBaseDir: "/skills/parent/child",
      },
    ];
    const match = findSkillPathMatch(
      "/skills/parent/child/helper.md",
      nestedEntries,
      normalizer,
    );
    expect(match?.name).toBe("child");
  });
});

// ---------------------------------------------------------------------------
// Moved from permission-system.test.ts catch-all (#342)
// ---------------------------------------------------------------------------

test("parseAllSkillPromptSections finds every available_skills block", () => {
  const prompt = [
    "Some preamble",
    "<available_skills>",
    "  <skill>",
    "    <name>skill-one</name>",
    "    <description>First skill</description>",
    "    <location>/path/to/one</location>",
    "  </skill>",
    "</available_skills>",
    "Some content between",
    "<available_skills>",
    "  <skill>",
    "    <name>skill-two</name>",
    "    <description>Second skill</description>",
    "    <location>/path/to/two</location>",
    "  </skill>",
    "</available_skills>",
    "Footer",
  ].join("\n");

  const sections = parseAllSkillPromptSections(prompt);

  expect(sections.length).toBe(2);
  expect(sections[0].entries[0]?.name).toBe("skill-one");
  expect(sections[1].entries[0]?.name).toBe("skill-two");
});

test("REGRESSION: visibleSkillPromptEntries excludes a denied skill from every available_skills block", () => {
  const { manager, cleanup } = createManager({
    permission: {
      "*": "ask",
      skill: { "denied-skill": "deny" },
    },
  });

  try {
    const prompt = [
      "System prompt start",
      "<available_skills>",
      "  <skill>",
      "    <name>visible-skill</name>",
      "    <description>Allowed skill</description>",
      "    <location>/skills/visible/index.ts</location>",
      "  </skill>",
      "  <skill>",
      "    <name>denied-skill</name>",
      "    <description>Denied in first block</description>",
      "    <location>/skills/blocked/one.ts</location>",
      "  </skill>",
      "</available_skills>",
      "Agent identity section",
      "<available_skills>",
      "  <skill>",
      "    <name>denied-skill</name>",
      "    <description>Denied in second block</description>",
      "    <location>/skills/blocked/two.ts</location>",
      "  </skill>",
      "</available_skills>",
      "System prompt end",
    ].join("\n");

    const entries = visibleSkillPromptEntries(
      prompt,
      asChecker(manager),
      null,
      new PathNormalizer(posixPathFlavor, "/cwd"),
    );

    expect(entries.map((entry) => entry.name)).toEqual(["visible-skill"]);
  } finally {
    cleanup();
  }
});

test("REGRESSION: visibleSkillPromptEntries keeps only visible skills available for path matching", () => {
  const { manager, cleanup } = createManager({
    permission: {
      "*": "ask",
      skill: { "blocked-skill": "deny" },
    },
  });

  try {
    const prompt = [
      "System prompt start",
      "<available_skills>",
      "  <skill>",
      "    <name>blocked-skill</name>",
      "    <description>Blocked skill</description>",
      "    <location>@./skills/blocked/entry.ts</location>",
      "  </skill>",
      "</available_skills>",
      "Middle section",
      "<available_skills>",
      "  <skill>",
      "    <name>visible-skill</name>",
      "    <description>Visible skill</description>",
      "    <location>@./skills/visible/entry.ts</location>",
      "  </skill>",
      "</available_skills>",
      "System prompt end",
    ].join("\n");

    const entries = visibleSkillPromptEntries(
      prompt,
      asChecker(manager),
      null,
      new PathNormalizer(posixPathFlavor, "/cwd"),
    );
    const visiblePath = resolve("/cwd", "./skills/visible/file.ts");
    const blockedPath = resolve("/cwd", "./skills/blocked/file.ts");
    const matchedVisibleSkill = findSkillPathMatch(
      process.platform === "win32" ? visiblePath.toLowerCase() : visiblePath,
      entries,
      normalizer,
    );
    const matchedBlockedSkill = findSkillPathMatch(
      process.platform === "win32" ? blockedPath.toLowerCase() : blockedPath,
      entries,
      normalizer,
    );

    expect(matchedVisibleSkill?.name).toBe("visible-skill");
    expect(matchedBlockedSkill).toBe(null);
  } finally {
    cleanup();
  }
});
