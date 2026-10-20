// Paths probe: runs the original's own path, access-intent and MCP-target functions over a corpus of inputs and writes what they
// return, for the Go differential test (extensions/pi-permission-system/testdata/paths-oracle.json). It is copied into an
// installed copy of the original by port/record-paths-probe.sh and never runs from here; the port itself has no Node.
import { mkdtempSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { homedir } from "node:os";
import { posix, win32 } from "node:path";
import { it } from "vitest";
import { AccessPath } from "#src/access-intent/access-path";
import {
  buildAccessIntentForSurface,
  buildResolvedIntentFromMatchValues,
  normalizeBashCommand,
  normalizeInput,
} from "#src/access-intent/input-normalizer";
import {
  createMcpPermissionTargets,
  createPiMcpToolTargets,
  parseQualifiedMcpToolName,
} from "#src/access-intent/mcp-targets";
import { mergeTokenEffects } from "#src/access-intent/effect";
import {
  canonicalNormalizePathForComparison,
  cwdRelativePolicyValues,
  getPathPolicyValues,
  normalizePathForComparison,
  normalizePathPolicyLiteral,
} from "#src/access-intent/path-normalization";
import {
  capabilityDirectionOf,
  capabilitySurfaceForEffect,
  capabilitySurfaceForTool,
  PATH_SURFACES,
  surfaceFamilyMembers,
  surfaceFamilyOf,
} from "#src/access-intent/path-surfaces";
import { getToolInputPath } from "#src/access-intent/tool-input-path";
import {
  classifyToolKind,
  isMcpCheck,
  resolveShellInvocation,
} from "#src/access-intent/tool-kind";
import { deriveApprovalPatterns } from "#src/path/approval-pattern";
import { resolveNativeToolTarget } from "#src/path/native-tool-target";
import { isPathOutsideWorkingDirectory } from "#src/path/path-containment";
import { posixPathFlavor, win32PathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";
import { isPiInfrastructureRead } from "#src/path/pi-infrastructure-read";
import { expandHomePath } from "#src/path/expand-home";
import { isSafeSystemPath } from "#src/path/safe-system-paths";
// eslint-disable-next-line local-rules/no-parent-relative-imports -- the parity oracle reads Pi's own compiled module
import { normalizeWindowsShellPath } from "../node_modules/@earendil-works/pi-coding-agent/dist/utils/paths.js";
// eslint-disable-next-line local-rules/no-parent-relative-imports -- the parity oracle reads Pi's own compiled module
import { resolveReadPath, resolveToCwd } from "../node_modules/@earendil-works/pi-coding-agent/dist/core/tools/path-utils.js";

const out = (v: string | undefined, name: string): string => {
  if (!v) throw new Error(`${name} is required`);
  return v;
};

/** The result of fn, or {throws:true} when it throws. */
function tryIt<T>(fn: () => T): { v: T } | { throws: true } {
  try {
    return { v: fn() };
  } catch {
    return { throws: true };
  }
}

const PATHS = [
  "", "/", "//", "///", "/a", "/a/", "/a/b/c", "/a//b", "/a/./b", "/a/../b", "/..", "/../..", "a", "a/", "a/b", "./a", "../a",
  "../../a/../b", ".", "..", "...", "a/.../b", "~", "~/x", "~\\x", "$HOME", "$HOME/x", "${HOME}/x", "${HOME:-/t}", "@/x", "@x", "@@x",
  "'q'", '"q"', "'q", "q'", " sp ", "a b/c d", "/dev/null", "/dev/null/x", "/dev/stdin", "/c/Users/x", "/c", "/c/", "/C/x", "/cc/x",
  "/mnt/d/x", "/cygdrive/e/x", "//server/share", "//server/share/x", "//server", "///x", "file:///x",
  "C:", "C:\\", "C:\\a", "C:\\a\\", "C:a", "c:\\A\\B", "C:/a/b", "C:\\a\\..\\b", "C:\\..\\..", "\\", "\\\\", "\\\\server\\share",
  "\\\\server\\share\\x", "\\\\server\\share\\", "\\\\?\\C:\\x", "\\\\.\\pipe\\x", "\\a", "a\\b", "a\\..\\b", "a:b", "a:\\b", "a:/b",
  ".\\a", "..\\a", "C:\\a/b\\c", "D:\\", "d:", "1:\\x", "CON", "a:", "a::b", "x:y:z", "\\\\server\\share\\..\\..\\x", "//server/share/../..",
  "\\\\?\\COM1:\\x", "\\\\?\\COM1:", "\\\\.\\COM1:\\x", "\\\\.\\C:\\x", "\\\\?\\UNC\\server\\share\\x", "COM1:", "com1:\\x",
  "NUL:foo", "LPT\u00b9:x", "COM\u00b9:", "CONa", "NULa", "AUX:", "a\\CON:\\b", "\\\\.\\", "\\\\?\\", "//?/C:/x", "//./pipe/x", "~//x", "~/../x",
  "~/./x/", "/c/a\\b", "/mnt/c/a\\b", "/c/a\nb", "/c/\u2028x", "C:CON:", "x/CON:/y", "CON:/..",
  "/İ/ı", "C:\\İSTANBUL\\Σ", "/Σ", "ΑΣ", "ΑΣ.", "Σ", "/é/e\u0301", "/😀/x", "C:\\😀\\İ", "a\nb", "/a\tb", "/a/b/", "/a/b//", "C:\\a\\b\\\\",
];

const FILE_URLS = [
  "file:///tmp/x", "file:///tmp/x/../y", "file:///tmp/./x", "file:///", "file://", "file:", "file:/x", "file:x", "FILE:///x",
  "file:///C:/x", "file:///C:/", "file:///C|/x", "file:///c:/x/../..", "file://C:/x", "file:///C:", "file:///C:x", "file://localhost/x",
  "file://LOCALHOST/C:/x", "file://host/x", "file://host/C:/x", "file://host", "file://HOST.Example.com/Share/x", "file://127.0.0.1/x",
  "file://[::1]/x", "file://a b/x", "file://%61/x", "file:///a%2fb", "file:///a%2Fb", "file:///a%5cb", "file:///a%5Cb", "file:///a%zz",
  "file:///a%", "file:///a%e2%82%ac", "file:///a%EF%BF%BDb", "file:///a%ef%bf%be", "file:///a%f4%8f%bf%bf", "file:///a%f4%90%80%80", "file:///a%ed%9f%bf", "file:///a%f0%9f%98%80", "file:///a%e2%82", "file:///a%c0%80", "file:///a%ed%a0%80", "file:///a%00b", "file:///a b",
  "file:///a\\b", "file:\\\\\\a", "file:////a", "file://///a", "file:///a?x", "file:///a#x", "file:///a?x#y/z", "file:///%2e%2e/x",
  "file:///.%2E/x", "file:///%2e/x", "file:///a/%2e%2e/b", "file:///a/%2E%2E", "file:///a/..", "file:///a/.", "file:///a/", "file:///a//b",
  "file:///é", "file:///😀", "file:///\u00A0", "file:///a\tb", "file:///a\nb", "  file:///a  ", "file:///a^b", "file:///a`b", "file:///a{b}",
  "file:///a|b", "file:///a<b>", 'file:///a"b', "file:///a%20b", "file:///C:/a%20b", "file:///C:/a%5Cb", "file:///C%3A/x", "file:///%43:/x",
  "file:///1:/x", "file:///ab:/x", "file:///C:\\x", "file:///C:/x\\y", "http://x/y", "not-a-url", "", "/tmp/x", "file:/etc/passwd",
  "file:etc/passwd", "file:///etc/passwd", "file:///tmp/s%65cret", "file:///C:/x/y%20z", "file:///a/b/../../..", "file:///a/b/../../../c",
  "file:///C:/a/../../b", "file:///C:/../x", "file://c:/x", "file://c|/x", "file://xn--bcher-kva.example/x", "file://bücher.example/x",
  "file://ex%41mple/x", "file://exa%2fmple/x", "file://1.2.3.4/x", "file://0x7f.1/x", "file://256.1.1.1/x", "file://a..b/x", "file://-/x",
  "file://_a/x", "file://a_b.com/x", "file://[::ffff:1.2.3.4]/x", "file://[xyz]/x", "file://[::1/x", "file://%6cocalhost/x",
  "file://ＬＯＣＡＬＨＯＳＴ/x", "file://local\u00ADhost/x", "file://localhost./x", "file://localhost:80/x", "file://user@host/x", "file://host:80/x",
  "file://localhost", "file://LocalHost/etc/passwd", "file:///C:/a/b/../c", "file:///C:/a/./b", "file:////server/share/x", "file:///%5C%5Cserver/x",
  "file:/C:/x", "file:C:/x", "file:///c:", "file:///c:/", "file:///c:x/y", "file:///\\C:/x", "file:\\\\C:\\x", "file://1.2.3.256/x", "file://1.2.65536/x", "file://16777216/x", "file://4294967296/x",
  "file://4294967295/x", "file://[1:0:0:2:3:4:5:6]/x", "file://[0:0:0:0:0:0:0:1]/x", "file://[1:0:0:0:0:0:0:0]/x", "file://[::]/x", "file://[1::]/x",
  "file://[2001:db8:0:0:1:0:0:1]/x",
];

const SERVER_LISTS: string[][] = [[], ["exa"], ["github", "todoist"], ["foo", "foo_bar"], ["foo_bar", "foo"], ["danger-srv"], ["a", "a--b"], ["a--b", "a"], ["a.b", "a_b"], ["my.srv"], ["github", "  "], [" exa "], ["😀"], ["git"], ["a_b", "a.b"], ["My_Srv"], ["exa", "github"]];

const MCP_INPUTS: unknown[] = [
  {}, null, "x", [], { tool: "exa_search" }, { tool: "exa:search" }, { tool: "search", server: "exa" }, { tool: "search_code_exa" },
  { tool: "github_search_code" }, { tool: "github_search_code", server: "github" }, { tool: "search_code", server: "github" },
  { tool: "foo_bar_baz" }, { tool: "foo_bar_baz_github" }, { tool: "  exa : search  " }, { tool: ":x" }, { tool: "x:" }, { tool: " " },
  { connect: "exa" }, { connect: " exa " }, { describe: "exa:search" }, { describe: "x", server: "s" }, { search: "weather" },
  { search: "w", server: "exa" }, { server: "exa" }, { server: " " }, { unrelated: "v" }, { tool: 5 }, { tool: "t", connect: "c" },
  { connect: "c", describe: "d" }, { describe: "d", search: "s" }, { search: "s", server: "v" }, { tool: "a:b:c" }, { tool: "a_b", server: "a" },
  { tool: "😀_x" }, { tool: "x_😀" }, { tool: "exa:search", server: "other" },
];

const PI_MCP_NAMES = [
  "mcp__a__b", "mcp__danger_srv__wipe", "mcp__a__b__c", "mcp__srv__get_x_1a2b3c4d", "mcp__github__github_x", "mcp__a_b__x", "mcp__a__b__x",
  "mcp__my_srv__x", "mcp__srv__get__x", " mcp__a__b ", "mcp__", "mcp__a", "mcp__a__", "mcp____b", "mcp__😀__x", "mcp____", "mcp__x__y__",
  "mcp__a__😀", "mcp__My_Srv__x", "mcp_a__b", "mcp", "bash",
];

const TOOL_NAMES = [
  "bash", " bash ", "mcp", "\tmcp\n", "skill", "read", "  read  ", "write", "edit", "find", "grep", "ls", "path", "external_directory", "task",
  "third_party_tool", "mcp__a__b", "mcp__a", "xmcp__a__b", "Bash", "", "path_read", "external_directory_write", "my_tool_read", "_read",
  "exec_command", "constructor", "__proto__", "toString", "hasOwnProperty", "valueOf", "other",
];

const SURFACES = [
  "path", "path_read", "path_write", "external_directory", "external_directory_read", "external_directory_write", "read", "write", "edit",
  "find", "grep", "ls", "bash", "mcp", "skill", "my_tool_read", "_read", "", "task",
];

const BASH_COMMANDS = [
  "", "ls", "# c\nls", "#c", "  # c\n  ls", "# a\n# b\nls -la", "ls\n# c", "\u00a0# nbsp\nls", "\ufeff# bom\nls", "\u2028# ls", "ls #x", "\n\nls\n",
  "# only", "#", " \t ", "git status\r\n# x\r\nls", "echo '# not a comment'",
];

it("records the original's path-layer results", () => {
  const output = out(process.env.PROBE_OUT, "PROBE_OUT");
  const home = homedir();
  const result: Record<string, unknown> = { home, cwd: process.cwd(), paths: PATHS, fileUrls: FILE_URLS, serverLists: SERVER_LISTS, mcpInputs: MCP_INPUTS };
  const flavors = { posix: posixPathFlavor, win32: win32PathFlavor };
  const impls = { posix, win32 };

  // --- node:path (the strategy objects the flavors expose) ---
  const nodepath: Record<string, unknown> = {};
  for (const [name, p] of Object.entries(impls)) {
    const absolute = PATHS.filter((s) => p.isAbsolute(s));
    nodepath[name] = {
      normalize: PATHS.map((s) => p.normalize(s)),
      isAbsolute: PATHS.map((s) => p.isAbsolute(s)),
      dirname: PATHS.map((s) => p.dirname(s)),
      basename: PATHS.map((s) => p.basename(s)),
      root: PATHS.map((s) => p.parse(s).root),
      joinPairs: PATHS.flatMap((a) => PATHS.map((b) => p.join(a, b))),
      relativePairs: absolute.flatMap((a) => absolute.map((b) => p.relative(a, b))),
      // resolve with an absolute base only: a relative base reads the process's own cwd.
      resolvePairs: absolute.flatMap((a) => PATHS.map((b) => p.resolve(a, b))),
      absoluteBases: absolute,
    };
  }
  result.nodepath = nodepath;

  // --- toLowerCase: every code point whose lower-case differs from itself, plus word-final sigma strings ---
  const lowers: [number, number[]][] = [];
  for (let c = 0; c <= 0x10ffff; c++) {
    if (c >= 0xd800 && c <= 0xdfff) continue;
    const s = String.fromCodePoint(c);
    const l = s.toLowerCase();
    if (l !== s) lowers.push([c, [...l].map((ch) => ch.codePointAt(0) as number)]);
  }
  result.lowers = lowers;
  const sigmaStrings = ["Σ", "ΑΣ", "ΑΣ.", "Α.Σ", "ΑΣΑ", "Α'Σ", "ΑΣ'", "Σ.Α", "ΑΣ\u0301", "ΑΣ\u0301Α", "aΣ", "aΣb", "a Σ", "ΑΣΣ", "ΑΣ Α", "I\u0130", "\u0130", "İSTANBUL", "ǅ", "ᾈΣ"];
  result.sigma = sigmaStrings.map((s) => ({ in: s, out: s.toLowerCase() }));

  // --- file URLs ---
  result.fileUrlResults = FILE_URLS.map((u) => ({
    in: u,
    posix: tryIt(() => posixPathFlavor.fileUrlToPath(u)),
    win32: tryIt(() => win32PathFlavor.fileUrlToPath(u)),
  }));

  // --- flavor ---
  result.toolShell = PATHS.map((s) => ({ in: s, flavor: win32PathFlavor.toolShellPath(s), pi: normalizeWindowsShellPath(s) }));
  result.bashTokenShape = PATHS.map((s) => ({ in: s, win32: win32PathFlavor.bashTokenShape(s), posix: posixPathFlavor.bashTokenShape(s) }));
  result.separators = PATHS.map((s) => ({
    in: s,
    posix: posixPathFlavor.lastSeparatorIndex(s),
    win32: win32PathFlavor.lastSeparatorIndex(s),
  }));
  result.flavorPairs = Object.fromEntries(
    Object.entries(flavors).map(([name, fl]) => [
      name,
      {
        fold: PATHS.map((s) => fl.fold(s)),
        withinPairs: PATHS.flatMap((a) => PATHS.map((b) => fl.isWithin(a, b))),
        outsidePairs: PATHS.flatMap((a) => PATHS.map((b) => isPathOutsideWorkingDirectory(a, b, fl))),
        comparablePairs: PATHS.flatMap((a) => ["/base", "C:\\Base", "/", "C:\\", "\\\\srv\\sh\\d"].map((b) => fl.comparable(a, b))),
      },
    ]),
  );
  result.safe = PATHS.map((s) => isSafeSystemPath(s));

  // --- approval patterns ---
  result.approval = Object.fromEntries(
    Object.entries(flavors).map(([name, fl]) => [
      name,
      PATHS.map((s) => [deriveApprovalPatterns(s, fl, false), deriveApprovalPatterns(s, fl, true)]),
    ]),
  );

  // --- path normalization (a base that does not exist, so realpath is the identity) ---
  const BASES = { posix: ["/nonexistent-pp/proj", "/nonexistent-pp/proj/sub"], win32: ["C:\\Nonexistent-pp\\Proj", "C:\\Nonexistent-pp\\Proj\\Sub"] };
  const norm: Record<string, unknown> = {};
  for (const [name, fl] of Object.entries(flavors)) {
    const [cwd, sub] = BASES[name as "posix" | "win32"];
    norm[name] = {
      cwd,
      sub,
      literal: PATHS.map((s) => normalizePathPolicyLiteral(s)),
      forComparison: PATHS.map((s) => normalizePathForComparison(s, cwd, fl)),
      canonical: PATHS.map((s) => canonicalNormalizePathForComparison(s, cwd, fl)),
      policyValues: PATHS.map((s) => [
        getPathPolicyValues(s, {}, fl),
        getPathPolicyValues(s, { cwd }, fl),
        getPathPolicyValues(s, { cwd, resolveBase: sub }, fl),
        getPathPolicyValues(s, { resolveBase: sub }, fl),
      ]),
      cwdRelative: PATHS.map((s) => cwdRelativePolicyValues(normalizePathForComparison(s, cwd, fl), cwd, fl)),
      accessPath: PATHS.map((s) => {
        const ap = AccessPath.forPath(s, { cwd, flavor: fl });
        const ap2 = AccessPath.forPath(s, { cwd, resolveBase: sub, flavor: fl });
        const show = (a: AccessPath) => ({ value: a.value(), match: a.matchValues(), boundary: a.boundaryValue(), alias: a.resolvedAlias() ?? null });
        return [show(ap), show(ap2)];
      }),
      bashToken: PATHS.map((s) => {
        const n = new PathNormalizer(fl, cwd);
        const a = n.forBashToken(s);
        return { value: a.value(), match: a.matchValues(), boundary: a.boundaryValue(), cd: n.interpretBashCdTarget(s), approval: n.approvalPatternsFor(a) };
      }),
    };
  }
  result.norm = norm;

  // --- native tool targets (an injected existence set) ---
  const natives: unknown[] = [];
  const spellings = [
    ...PATHS,
    "/x/my\u00A0file.txt", "/x/my\u2007file.txt", "/x/my\u3000file.txt", "/x/my\u202Ffile.txt", "/x/Shot 1.02.03 PM.png", "/x/Shot 1.02.03 am.png",
    "/x/caf\u00e9.txt", "/x/cafe\u0301.txt", "/x/d'x.txt", "/x/d\u2019x.txt", "/x/d'\u00e9.txt", "/x/d'x 1 PM.png", "d'x.txt", "@d'x.txt",
    "@@a", "@~/x", "~/\u00e9", "/mnt/d/a/b.txt", "/c/x/y", "/C", "/c/", "/cygdrive/e/b/c", "//server/share", "/c\\x", "file:///etc/hosts",
    "file:///etc/h%6Fsts", "file://host/x", "file:///C:/x/y%20z", "~user/x", "$HOME/x", "'x.txt'", " x.txt ", "../other/x", "\u00A0", "@",
    "/x/caf\u00e9 1 PM.png", "/x/a\u205Fb", "/x/\u1e9b\u0323.txt", "/x/\ufb01.txt", "/x/\u212b.txt",
  ];
  const variantsOf = (resolved: string): string[] => {
    const nfd = resolved.normalize("NFD");
    const curly = (v: string) => v.replace(/'/g, "\u2019");
    return [resolved.replace(/ (AM|PM)\./gi, "\u202F$1."), nfd, curly(resolved), curly(nfd)];
  };
  for (const [name, fl, cwd] of [["posix", posixPathFlavor, "/projects/app"], ["win32", win32PathFlavor, "C:\\proj"]] as const) {
    for (const raw of spellings) {
      const base = resolveNativeToolTarget(raw, { cwd, flavor: fl, readFallbacks: false, exists: () => false });
      const variants = variantsOf(base.target);
      const existSets: string[][] = [[], variants, ...variants.map((v) => [v]), [base.target], [base.target, ...variants], [variants[3], variants[0]], [variants[1], variants[2]]];
      for (const existing of existSets) {
        for (const readFallbacks of [false, true]) {
          const set = new Set(existing);
          const r = resolveNativeToolTarget(raw, { cwd, flavor: fl, readFallbacks, exists: (p) => set.has(p) });
          natives.push({ flavor: name, cwd, raw, existing, readFallbacks, target: r.target, rewritten: r.rewritten, relativeSpelling: r.relativeSpelling ?? null });
        }
      }
    }
  }
  result.natives = natives;

  // --- Pi's own resolver on the real filesystem is recorded by the parity test (testdata/native-target.json) ---

  // --- infrastructure read ---
  const infraDirs = ["/a", "/a/x", "~/.pi/agent", "/opt/*/pi", "/o?t", "C:\\Users\\Foo\\.pi\\agent", "C:\\Users\\Foo\\**\\pi\\**", "/", ""];
  const infra: unknown[] = [];
  const infraPaths = ["/a/x/y", "/a/x/logs/z", "/a/b", "/opt/q/pi/f", "/ot/q", "/oat", home + "/.pi/agent/c.json", "/p/.pi/npm/d", "/p/.pi/git/d", "/p", "/p/.pi/npmx/d", "c:\\users\\foo\\.pi\\agent\\c", "c:\\users\\foo\\n\\pi\\x", "c:\\p\\.pi\\npm\\x", "/etc/passwd", ""];
  for (const [name, fl, cwd] of [["posix", posixPathFlavor, "/p"], ["win32", win32PathFlavor, "C:\\p"]] as const) {
    for (const tool of ["read", "ls", "grep", "find", "write", "edit", "bash", "mcp"]) {
      for (const path of infraPaths) {
        for (const excluded of [[], ["/a/x/logs"], ["~/.pi/agent/logs", "C:\\Users\\Foo\\.pi\\agent\\Logs"]]) {
          const dirs = infraDirs;
          infra.push({ flavor: name, cwd, tool, path, excluded, dirs, r: isPiInfrastructureRead(tool, path, dirs, cwd, fl, excluded) });
        }
      }
    }
  }
  result.infra = infra;
  result.expandHome = ["~", "~/x", "~\\x", "$HOME", "$HOME/x", "${HOME}", "${HOME}\\x", "${HOME:-/t}/x", "~user", "$HOMEDIR", "~/", "~//x", "~/../x", "~/./x/", "x"].map((s) => ({ in: s, out: expandHomePath(s) }));

  // --- tool kind, surfaces, effects ---
  result.toolKinds = TOOL_NAMES.map((n) => classifyToolKind(n));
  const aliases = { exec_command: { commandArgument: "cmd", workdirArgument: "workdir" }, bare: { commandArgument: "c" } };
  const shellInputs: unknown[] = [{}, { command: "ls" }, { command: "  ls  " }, { command: 5 }, { cmd: " x ", workdir: " /w ", undefined: " u ", c: " cc " }, null, { workdir: "/e" }];
  result.shell = { aliases, inputs: shellInputs, results: TOOL_NAMES.flatMap((n) => shellInputs.map((i) => [resolveShellInvocation(n, i, aliases) ?? null, resolveShellInvocation(n, i, undefined) ?? null])) };
  result.surfaces = SURFACES.map((s) => ({
    in: s,
    family: surfaceFamilyOf(s),
    direction: capabilityDirectionOf(s),
    members: surfaceFamilyMembers(s),
    isPath: PATH_SURFACES.has(s),
    forTool: TOOL_NAMES.map((n) => capabilitySurfaceForTool(s, n)),
    forEffect: ["read", "write", "unproven"].map((e) => capabilitySurfaceForEffect(s, e as "read")),
  }));
  const effects = [["read", "syntax"], ["read", "core"], ["write", "syntax"], ["write", "core"], ["unproven", "unproven"], ["unproven", "retracted"], ["read", "retracted"]];
  result.effects = { list: effects, merged: effects.flatMap((a) => effects.map((b) => mergeTokenEffects({ effect: a[0], source: a[1] } as never, { effect: b[0], source: b[1] } as never))) };
  result.isMcpCheck = TOOL_NAMES.flatMap((n) => ["tool", "mcp", "bash", "default"].map((s) => isMcpCheck({ toolName: n, source: s })));

  // --- MCP targets and input normalization ---
  result.mcp = {
    targets: MCP_INPUTS.flatMap((i) => SERVER_LISTS.map((s) => createMcpPermissionTargets(i, s))),
    pi: PI_MCP_NAMES.flatMap((n) => SERVER_LISTS.map((s) => createPiMcpToolTargets(n, s))),
    piNames: PI_MCP_NAMES,
    qualified: ["exa:search", "  exa : search  ", "", "   ", ":search", "exa:", "exa_search", " :search", "exa: ", "a:b:c", "😀:x", "a :b"].map((s) => ({ in: s, out: parseQualifiedMcpToolName(s) })),
  };
  const NORMALIZE_INPUTS: unknown[] = [{}, null, { command: "ls" }, { command: 5 }, { name: "librarian" }, { name: 99 }, { name: "" }, { path: ".env" }, ...MCP_INPUTS.slice(4, 14)];
  result.normalizeInput = {
    inputs: NORMALIZE_INPUTS,
    results: TOOL_NAMES.flatMap((n) => NORMALIZE_INPUTS.map((i) => normalizeInput(n, i, ["exa", "github", "danger-srv"]))),
    bash: BASH_COMMANDS.map((c) => normalizeBashCommand(c, ["x", c])),
    bashCommands: BASH_COMMANDS,
  };

  // --- intents ---
  const surfaceValues: (string | undefined)[] = [undefined, "", "   ", "src/a.ts", "/etc/hosts", "file:///etc/hosts", "~/x", "'q'", "danger", "*", "C:\\x", "@y"];
  const intents: unknown[] = [];
  for (const [name, fl, cwd] of [["posix", posixPathFlavor, "/nonexistent-pp/proj"], ["win32", win32PathFlavor, "C:\\Nonexistent-pp\\Proj"]] as const) {
    const n = new PathNormalizer(fl, cwd);
    for (const surface of SURFACES) {
      for (const value of surfaceValues) {
        for (const agent of [undefined, "Explore"]) {
          const i = buildAccessIntentForSurface(surface, value, n, agent);
          intents.push({
            flavor: name, cwd, surface, value: value ?? null, agent: agent ?? null, kind: i.kind,
            input: i.kind === "tool" ? i.input : null, values: i.kind === "path-values" ? i.values : null,
            path: i.kind === "access-path" ? { value: i.path.value(), match: i.path.matchValues(), boundary: i.path.boundaryValue(), alias: i.path.resolvedAlias() ?? null } : null,
            toolPath: ["read", "write", "bash", "task"].map((t) => { const a = n.forToolPath(t, value ?? ""); return { value: a.value(), match: a.matchValues(), boundary: a.boundaryValue(), alias: a.resolvedAlias() ?? null }; }),
          });
        }
      }
    }
  }
  result.intents = intents;
  result.resolved = SURFACES.flatMap((s) => [[], ["a"], ["a", "b"]].map((vals) => ({ surface: s, vals, r: buildResolvedIntentFromMatchValues(s, vals, "Agent") })));

  // --- tool input path ---
  const tipInputs: unknown[] = [{}, null, { path: "/p" }, { path: "" }, { path: "  /p  " }, { arguments: { path: "/a" } }, { arguments: "x" }, { path: 5 }, { arguments: { path: "  " } }];
  result.toolInputPath = TOOL_NAMES.flatMap((n) => tipInputs.map((i) => getToolInputPath(n, i)));
  result.toolInputPathInputs = tipInputs;
  result.toolNames = TOOL_NAMES;

  writeFileSync(output, JSON.stringify(result));
});

it("records Pi's own resolver on a real filesystem layout", () => {
  const output = out(process.env.PROBE_NATIVE_OUT, "PROBE_NATIVE_OUT");
  const dir = realpathSync(mkdtempSync(join(tmpdir(), "native-target-")));
  const cwd = realpathSync(mkdtempSync(join(tmpdir(), "native-target-cwd-")));
  try {
    const files = ["my file.txt", "d\u2019x.txt", "Shot 1.02.03\u202FPM.png", "cafe\u0301.txt", "secret.txt", "q'x.txt", "q\u2019x.txt", "dang\u2019x.txt"];
    for (const name of files) writeFileSync(join(dir, name), "x");
    symlinkSync(join(dir, "missing-target"), join(dir, "dang'x.txt"));
    const secret = join(dir, "secret.txt");
    const pathToFileURL = (p: string) => new URL(`file://${p}`).href;
    const spellings = [
      secret, pathToFileURL(secret), pathToFileURL(secret).replace("secret", "s%65cret"), join(dir, "my\u00A0file.txt"), join(dir, "d'x.txt"),
      join(dir, "Shot 1.02.03 PM.png"), join(dir, "caf\u00e9.txt"), join(dir, "q'x.txt"), join(dir, "dang'x.txt"), join(dir, "absent'x.txt"),
      `@${secret}`, "relative/x.txt", "~/nonexistent-native-target", "$HOME/x", "'quoted'", "file://host/x",
    ];
    const tmpl = (s: string) => s.split(dir).join("<DIR>").split(cwd).join("<CWD>").split(homedir()).join("<HOME>");
    const tryResolve = (fn: () => string) => { try { return tmpl(fn()); } catch { return null; } };
    const recorded = {
      files,
      spellings: spellings.map(tmpl),
      fsDependent: Object.fromEntries(spellings.map((s) => [tmpl(s), s.includes("caf")])),
      read: Object.fromEntries(spellings.map((s) => [tmpl(s), tryResolve(() => resolveReadPath(s, cwd))])),
      resolve: Object.fromEntries(spellings.map((s) => [tmpl(s), tryResolve(() => resolveToCwd(s, cwd))])),
    };
    writeFileSync(output, JSON.stringify(recorded));
  } finally {
    rmSync(dir, { recursive: true, force: true });
    rmSync(cwd, { recursive: true, force: true });
  }
});
