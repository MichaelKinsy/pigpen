import { describe, expect, it } from "vitest";
import {
  renderToolSurfaceSections,
  type ToolSurfaceInputs,
} from "#src/exposure/tool-surface-prompt";

/** Pi's own snippets for the tools these tests use. */
const SNIPPETS: Record<string, string> = {
  read: "Read file contents",
  bash: "Execute bash commands (ls, grep, find, etc.)",
  edit: "Make precise file edits with exact text replacement",
  write: "Create or overwrite files",
  grep: "Search file contents",
  find: "Find files by name",
  ls: "List directory contents",
  powershell: "Execute PowerShell commands",
};

function inputs(overrides: Partial<ToolSurfaceInputs> = {}): ToolSurfaceInputs {
  return {
    allowedTools: ["read"],
    toolSnippets: SNIPPETS,
    guidelinesByTool: new Map(),
    promptGuidelines: [],
    ...overrides,
  };
}

function rulesOf(overrides: Partial<ToolSurfaceInputs> = {}): string[] {
  return renderToolSurfaceSections(inputs(overrides)).rules.split("\n");
}

describe("renderToolSurfaceSections", () => {
  it("states the allowed tools and their rules as untagged section contents", () => {
    expect(
      renderToolSurfaceSections(
        inputs({
          allowedTools: ["read", "edit"],
          guidelinesByTool: new Map([["read", ["Read before editing"]]]),
        }),
      ),
    ).toEqual({
      tools: [
        "- read: Read file contents",
        "- edit: Make precise file edits with exact text replacement",
      ].join("\n"),
      rules: [
        "- Read before editing",
        "- Be concise in your responses",
        "- Show file paths clearly when working with files",
      ].join("\n"),
    });
  });

  it("omits the tools section when no allowed tool has a snippet", () => {
    expect(
      renderToolSurfaceSections(inputs({ allowedTools: ["undescribed"] })),
    ).toEqual({
      rules: [
        "- Be concise in your responses",
        "- Show file paths clearly when working with files",
      ].join("\n"),
    });
  });

  describe("the tools section", () => {
    it("lists the allowed tools with Pi's own snippets, in the allowed order", () => {
      expect(
        renderToolSurfaceSections(inputs({ allowedTools: ["read", "grep"] }))
          .tools,
      ).toBe(
        ["- read: Read file contents", "- grep: Search file contents"].join(
          "\n",
        ),
      );
    });

    it("omits a tool Pi supplied no snippet for", () => {
      expect(
        renderToolSurfaceSections(
          inputs({
            allowedTools: ["read", "ask_parent"],
            toolSnippets: { read: SNIPPETS.read },
          }),
        ).tools,
      ).toBe("- read: Read file contents");
    });
  });

  describe("the rules section", () => {
    it("carries each allowed tool's own guideline bullets", () => {
      expect(
        rulesOf({
          allowedTools: ["read", "edit"],
          guidelinesByTool: new Map([
            ["read", ["Use read to examine files instead of cat or sed."]],
            [
              "edit",
              ["Use edit for precise changes (old text must match exactly)"],
            ],
          ]),
        }),
      ).toEqual([
        "- Use read to examine files instead of cat or sed.",
        "- Use edit for precise changes (old text must match exactly)",
        "- Be concise in your responses",
        "- Show file paths clearly when working with files",
      ]);
    });

    it("omits a denied tool's guideline bullets", () => {
      expect(
        rulesOf({
          allowedTools: ["read"],
          guidelinesByTool: new Map([
            ["read", ["Use read to examine files instead of cat or sed."]],
            ["write", ["Use write only for new files or complete rewrites"]],
          ]),
        }),
      ).not.toContain("- Use write only for new files or complete rewrites");
    });

    it("carries a third-party tool's guidelines", () => {
      expect(
        rulesOf({
          allowedTools: ["colgrep"],
          toolSnippets: { colgrep: "Semantic code search" },
          guidelinesByTool: new Map([
            ["colgrep", ["Prefer colgrep for intent-based searches."]],
          ]),
        }),
      ).toContain("- Prefer colgrep for intent-based searches.");
    });

    it("de-duplicates a bullet two tools both contribute", () => {
      const shared = "Do not use emojis";
      const occurrences = rulesOf({
        allowedTools: ["read", "edit"],
        guidelinesByTool: new Map([
          ["read", [shared]],
          ["edit", [shared]],
        ]),
      }).filter((line) => line === `- ${shared}`);

      expect(occurrences).toHaveLength(1);
    });

    it("always ends with Pi's two unconditional bullets", () => {
      expect(rulesOf().slice(-2)).toEqual([
        "- Be concise in your responses",
        "- Show file paths clearly when working with files",
      ]);
    });

    describe("Pi's file-exploration bullet", () => {
      it("is written first when bash is the only way to explore", () => {
        expect(rulesOf({ allowedTools: ["bash"] })[0]).toBe(
          "- Use bash for file operations like ls, rg, find",
        );
      });

      it("is withheld when a dedicated exploration tool is allowed", () => {
        expect(rulesOf({ allowedTools: ["bash", "grep"] })).not.toContain(
          "- Use bash for file operations like ls, rg, find",
        );
      });

      it("is withheld when no shell is allowed", () => {
        expect(rulesOf({ allowedTools: ["read"] })).toEqual([
          "- Be concise in your responses",
          "- Show file paths clearly when working with files",
        ]);
      });

      it("names PowerShell when it is the only shell", () => {
        expect(rulesOf({ allowedTools: ["powershell"] })[0]).toBe(
          "- Use PowerShell for file operations like listing, searching, and finding files",
        );
      });

      it("names both shells when both are allowed", () => {
        expect(rulesOf({ allowedTools: ["bash", "powershell"] })[0]).toBe(
          "- Use bash or PowerShell for file operations like listing, searching, and finding files",
        );
      });
    });

    describe("extension-contributed rules", () => {
      it("carries a rule no tool contributes, after the tools' own and before Pi's two", () => {
        expect(
          rulesOf({
            guidelinesByTool: new Map([["read", ["Read before editing"]]]),
            promptGuidelines: ["An extension's rule"],
          }),
        ).toEqual([
          "- Read before editing",
          "- An extension's rule",
          "- Be concise in your responses",
          "- Show file paths clearly when working with files",
        ]);
      });

      it("does not carry a denied tool's rule back in by that route", () => {
        expect(
          rulesOf({
            guidelinesByTool: new Map([["bash", ["  Use bash carefully"]]]),
            promptGuidelines: ["Use bash carefully "],
          }),
        ).not.toContain("- Use bash carefully");
      });
    });
  });
});
