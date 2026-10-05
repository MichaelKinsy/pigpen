import { describe, expect, it } from "vitest";
import {
  createMcpPermissionTargets,
  createPiMcpToolTargets,
  McpTargetList,
  parseQualifiedMcpToolName,
} from "#src/access-intent/mcp-targets";

describe("parseQualifiedMcpToolName", () => {
  it("returns server and tool for a valid qualified name", () => {
    expect(parseQualifiedMcpToolName("exa:search")).toEqual({
      server: "exa",
      tool: "search",
    });
  });

  it("returns server and tool with surrounding whitespace trimmed", () => {
    expect(parseQualifiedMcpToolName("  exa : search  ")).toEqual({
      server: "exa",
      tool: "search",
    });
  });

  it("returns null for empty string", () => {
    expect(parseQualifiedMcpToolName("")).toBeNull();
  });

  it("returns null for whitespace-only string", () => {
    expect(parseQualifiedMcpToolName("   ")).toBeNull();
  });

  it("returns null when colon is the first character", () => {
    expect(parseQualifiedMcpToolName(":search")).toBeNull();
  });

  it("returns null when colon is the last character", () => {
    expect(parseQualifiedMcpToolName("exa:")).toBeNull();
  });

  it("returns null for a plain tool name with no colon", () => {
    expect(parseQualifiedMcpToolName("exa_search")).toBeNull();
  });

  it("returns null when server part is empty after trimming", () => {
    expect(parseQualifiedMcpToolName(" :search")).toBeNull();
  });

  it("returns null when tool part is empty after trimming", () => {
    expect(parseQualifiedMcpToolName("exa: ")).toBeNull();
  });
});

describe("createMcpPermissionTargets", () => {
  describe("tool call (input.tool)", () => {
    it("produces targets for a bare tool name with no configured servers", () => {
      const targets = createMcpPermissionTargets({ tool: "exa_search" }, []);
      expect(targets).toContain("exa_search");
      expect(targets).toContain("mcp_call");
    });

    it("produces targets for a qualified tool name (server:tool)", () => {
      const targets = createMcpPermissionTargets({ tool: "exa:search" }, []);
      expect(targets).toContain("exa_search");
      expect(targets).toContain("exa:search");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_call");
    });

    it("produces targets for a tool call with explicit server field", () => {
      const targets = createMcpPermissionTargets(
        { tool: "search", server: "exa" },
        [],
      );
      expect(targets).toContain("exa_search");
      expect(targets).toContain("exa:search");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_call");
    });

    it("derives server targets from configured server names when tool name ends with _<server>", () => {
      const targets = createMcpPermissionTargets({ tool: "search_code_exa" }, [
        "exa",
      ]);
      expect(targets).toContain("exa_search_code_exa");
      expect(targets).toContain("exa:search_code_exa");
      expect(targets).toContain("exa");
      expect(targets).toContain("search_code_exa");
    });

    describe("prefix-named tools (<server>_<tool>)", () => {
      // The shape the mcp() proxy and aggregators such as mcp-combiner both
      // produce. Before #928 a leading `<server>_` segment derived nothing, so
      // an exact-server rule never fired for these names.
      it("derives the bare server for a configured leading segment", () => {
        const targets = createMcpPermissionTargets(
          { tool: "github_search_code" },
          ["github", "todoist"],
        );
        expect(targets).toContain("github");
      });

      it("reports the tool name ahead of the derived server", () => {
        // Order decides which name a winning rule is reported under, so the
        // specific tool name comes first and the prompt says what is running.
        const targets = createMcpPermissionTargets(
          { tool: "github_search_code" },
          ["github"],
        );
        expect(targets[0]).toBe("github_search_code");
        expect(targets[1]).toBe("github");
      });

      it("derives nothing when the leading segment names no configured server", () => {
        const targets = createMcpPermissionTargets(
          { tool: "github_search_code" },
          ["todoist"],
        );
        expect(targets).not.toContain("github");
        expect(targets).toContain("github_search_code");
      });

      it("picks the longest matching server whatever order the list arrives in", () => {
        // The production loader sorts longest-first, but the invariant belongs
        // to the derivation rather than to its caller: `foo_bar_baz` belongs to
        // `foo_bar`, never also to `foo`.
        const targets = createMcpPermissionTargets({ tool: "foo_bar_baz" }, [
          "foo",
          "foo_bar",
        ]);
        expect(targets).toContain("foo_bar");
        expect(targets).not.toContain("foo");
      });

      it("omits re-prefixed candidates when an explicit server repeats the prefix", () => {
        // `github_github_search_code` names nothing a rule can usefully match,
        // and it used to lead the list -- so it also became the reported
        // target whenever no rule matched.
        const targets = createMcpPermissionTargets(
          { tool: "github_search_code", server: "github" },
          [],
        );
        expect(targets).not.toContain("github_github_search_code");
        expect(targets).not.toContain("github:github_search_code");
        expect(targets[0]).toBe("github_search_code");
        expect(targets).toContain("github");
      });

      it("keeps the qualified candidates when the explicit server is not the prefix", () => {
        const targets = createMcpPermissionTargets(
          { tool: "search_code", server: "github" },
          [],
        );
        expect(targets).toContain("github_search_code");
        expect(targets).toContain("github:search_code");
        expect(targets).toContain("github");
      });

      it("suppresses a suffix coincidence once a prefix matches", () => {
        // One naming convention per name: `foo_bar_baz_github` is a foo_bar
        // tool that happens to end in a configured server's name.
        const targets = createMcpPermissionTargets(
          { tool: "foo_bar_baz_github" },
          ["foo_bar", "github"],
        );
        expect(targets).toContain("foo_bar");
        expect(targets).not.toContain("github");
      });
    });

    it("does not include duplicate entries", () => {
      const targets = createMcpPermissionTargets({ tool: "exa:search" }, [
        "exa",
      ]);
      const unique = [...new Set(targets)];
      expect(targets).toEqual(unique);
    });
  });

  describe("connect call (input.connect)", () => {
    it("produces targets for a connect operation", () => {
      const targets = createMcpPermissionTargets({ connect: "exa" }, []);
      expect(targets).toContain("mcp_connect_exa");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_connect");
    });

    it("does not include mcp_call for connect operations", () => {
      const targets = createMcpPermissionTargets({ connect: "exa" }, []);
      expect(targets).not.toContain("mcp_call");
    });
  });

  describe("describe operation (input.describe)", () => {
    it("produces targets for a describe operation on a qualified tool", () => {
      const targets = createMcpPermissionTargets(
        { describe: "exa:search" },
        [],
      );
      expect(targets).toContain("exa_search");
      expect(targets).toContain("exa:search");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_describe");
    });
  });

  describe("search operation (input.search)", () => {
    it("produces mcp_search and the search string as targets", () => {
      const targets = createMcpPermissionTargets({ search: "weather" }, []);
      expect(targets).toContain("weather");
      expect(targets).toContain("mcp_search");
    });

    it("includes server targets when server is provided alongside search", () => {
      const targets = createMcpPermissionTargets(
        { search: "weather", server: "exa" },
        [],
      );
      expect(targets).toContain("mcp_server_exa");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_search");
    });
  });

  describe("server listing (input.server only)", () => {
    it("produces mcp_list and server-specific targets", () => {
      const targets = createMcpPermissionTargets({ server: "exa" }, []);
      expect(targets).toContain("mcp_server_exa");
      expect(targets).toContain("exa");
      expect(targets).toContain("mcp_list");
    });
  });

  describe("status (no meaningful input)", () => {
    it("produces mcp_status for empty input", () => {
      const targets = createMcpPermissionTargets({}, []);
      expect(targets).toContain("mcp_status");
    });

    it("produces mcp_status for null input", () => {
      const targets = createMcpPermissionTargets(null, []);
      expect(targets).toContain("mcp_status");
    });

    it("produces mcp_status when no server/tool/connect/describe/search present", () => {
      const targets = createMcpPermissionTargets({ unrelated: "value" }, [
        "exa",
      ]);
      expect(targets).toContain("mcp_status");
    });
  });

  describe("the derivation table published in docs/configuration.md", () => {
    // `docs/configuration.md` § `mcp` Surface prints these rows so a rule author
    // can see what their rule has to match. Asserting the full array keeps the
    // doc honest -- a derivation change that does not update it fails here.
    it.each([
      [
        "a prefix-named tool",
        { tool: "github_search_code" },
        ["github"],
        ["github_search_code", "github", "mcp_call"],
      ],
      [
        "a suffix-named tool",
        { tool: "search_code_github" },
        ["github"],
        [
          "github_search_code_github",
          "github:search_code_github",
          "github",
          "search_code_github",
          "mcp_call",
        ],
      ],
      [
        "a qualified tool name",
        { tool: "github:search_code" },
        [],
        [
          "github_search_code",
          "github:search_code",
          "github",
          "search_code",
          "mcp_call",
        ],
      ],
      [
        "an explicit server argument",
        { tool: "search_code", server: "github" },
        [],
        [
          "github_search_code",
          "github:search_code",
          "github",
          "search_code",
          "mcp_call",
        ],
      ],
    ])("%s", (_label, input, servers, expected) => {
      expect(createMcpPermissionTargets(input, servers)).toEqual(expected);
    });
  });

  describe("priority ordering", () => {
    it("tool targets appear before mcp_call", () => {
      const targets = createMcpPermissionTargets({ tool: "exa:search" }, []);
      const mcpCallIdx = targets.indexOf("mcp_call");
      const exaSearchIdx = targets.indexOf("exa_search");
      expect(exaSearchIdx).toBeGreaterThanOrEqual(0);
      expect(mcpCallIdx).toBeGreaterThan(exaSearchIdx);
    });
  });
});

describe("McpTargetList", () => {
  describe("add", () => {
    it("ignores null", () => {
      const list = new McpTargetList();
      list.add(null);
      expect(list.toArray()).toEqual([]);
    });

    it("ignores empty string", () => {
      const list = new McpTargetList();
      list.add("");
      expect(list.toArray()).toEqual([]);
    });

    it("appends a new value", () => {
      const list = new McpTargetList();
      list.add("exa");
      expect(list.toArray()).toEqual(["exa"]);
    });

    it("dedups repeated values", () => {
      const list = new McpTargetList();
      list.add("exa");
      list.add("exa");
      expect(list.toArray()).toEqual(["exa"]);
    });

    it("preserves first-insertion order across a mix of values", () => {
      const list = new McpTargetList();
      list.add("exa_search");
      list.add("exa:search");
      list.add("exa");
      list.add("exa_search"); // duplicate — must not change order
      list.add("mcp_call");
      expect(list.toArray()).toEqual([
        "exa_search",
        "exa:search",
        "exa",
        "mcp_call",
      ]);
    });
  });

  describe("toArray", () => {
    it("returns an independent copy that does not mutate the list", () => {
      const list = new McpTargetList();
      list.add("exa");
      const first = list.toArray();
      first.push("mutated");
      expect(list.toArray()).toEqual(["exa"]);
    });
  });
});

describe("createPiMcpToolTargets", () => {
  it("emits the configured and sanitized server spellings, most specific first", () => {
    expect(
      createPiMcpToolTargets("mcp__danger_srv__wipe", ["danger-srv"]),
    ).toEqual([
      "danger-srv_wipe",
      "danger-srv:wipe",
      "danger-srv",
      "danger_srv_wipe",
      "danger_srv:wipe",
      "danger_srv",
      "wipe",
      "mcp__danger_srv__wipe",
      "mcp_call",
    ]);
  });

  it("emits one spelling when the configured name needs no sanitizing", () => {
    expect(createPiMcpToolTargets("mcp__github__search", ["github"])).toEqual([
      "github_search",
      "github:search",
      "github",
      "search",
      "mcp__github__search",
      "mcp_call",
    ]);
  });

  it("splits an unconfigured server at the first separator", () => {
    expect(createPiMcpToolTargets("mcp__srv__get__x", [])).toEqual([
      "srv_get__x",
      "srv:get__x",
      "srv",
      "get__x",
      "mcp__srv__get__x",
      "mcp_call",
    ]);
  });

  it("resolves the longest configured server whose sanitized name prefixes the tool", () => {
    // Both `a` and `a--b` (sanitized `a__b`) prefix `mcp__a__b__x`; the
    // longer one owns it, whatever the configured order.
    const expected = [
      "a--b_x",
      "a--b:x",
      "a--b",
      "a__b_x",
      "a__b:x",
      "a__b",
      "x",
      "mcp__a__b__x",
      "mcp_call",
    ];
    expect(createPiMcpToolTargets("mcp__a__b__x", ["a", "a--b"])).toEqual(
      expected,
    );
    expect(createPiMcpToolTargets("mcp__a__b__x", ["a--b", "a"])).toEqual(
      expected,
    );
  });

  it("resolves a configured server whose name holds a separator Pi rewrote", () => {
    expect(createPiMcpToolTargets("mcp__my_srv__x", ["my.srv"])).toEqual([
      "my.srv_x",
      "my.srv:x",
      "my.srv",
      "my_srv_x",
      "my_srv:x",
      "my_srv",
      "x",
      "mcp__my_srv__x",
      "mcp_call",
    ]);
  });

  it("resolves a configured server whose sanitized name contains the separator", () => {
    expect(createPiMcpToolTargets("mcp__a__b__x", ["a--b"])).toEqual([
      "a--b_x",
      "a--b:x",
      "a--b",
      "a__b_x",
      "a__b:x",
      "a__b",
      "x",
      "mcp__a__b__x",
      "mcp_call",
    ]);
  });

  it("still qualifies a tool whose own name starts with its server", () => {
    expect(createPiMcpToolTargets("mcp__github__github_x", ["github"])).toEqual(
      [
        "github_github_x",
        "github:github_x",
        "github",
        "github_x",
        "mcp__github__github_x",
        "mcp_call",
      ],
    );
  });

  it("keeps every configured name that sanitizes to the same server", () => {
    expect(createPiMcpToolTargets("mcp__a_b__x", ["a.b", "a_b"])).toEqual([
      "a.b_x",
      "a.b:x",
      "a.b",
      "a_b_x",
      "a_b:x",
      "a_b",
      "x",
      "mcp__a_b__x",
      "mcp_call",
    ]);
  });
});
