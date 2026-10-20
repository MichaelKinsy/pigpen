// Records what the original's BashProgram answers (command units, external accesses, path-rule candidates) for a list of bash
// commands under six path configurations, for the Go differential
// (components/permissions/extensions/pi-permission-system/bashprogram_oracle_test.go). Copied into an installed copy of the
// original for the run by run.sh and removed after; it never lives in port/oracle.
//
//   DIFF_IN=<json array of strings> DIFF_OUT=<file> vitest run test/zz-bashprogram-probe.test.ts
//
// The filesystem the original reads is a fixture: a root (the project cwd of the "root" configurations) and an outside directory,
// both under the system temp directory, with files and symlinks created below. `@R@` and `@O@` in an input stand for them, and
// they are written back as the same placeholders in the output, so the recording does not depend on a temporary path. Outside the
// fixture the probe's fs answers as a host without symlinks and without any file: realpath is the identity and lstat finds
// nothing, so what is recorded does not depend on the recording machine's /etc or /tmp.
import { closeSync, mkdirSync, mkdtempSync, openSync, readFileSync, symlinkSync, writeFileSync, writeSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ root: "", out: "" }));
vi.mock("node:fs", async () => {
  const actual = await vi.importActual<typeof import("node:fs")>("node:fs");
  const inFixture = (p: unknown) =>
    typeof p === "string" &&
    fixture.root !== "" &&
    (p === fixture.root || p.startsWith(`${fixture.root}/`) || p === fixture.out || p.startsWith(`${fixture.out}/`));
  const realpathSync = Object.assign(
    (p: string, ...rest: unknown[]) => (inFixture(p) ? (actual.realpathSync as (...a: unknown[]) => string)(p, ...rest) : p),
    {},
  );
  const lstatSync = (p: string, ...rest: unknown[]) => {
    if (inFixture(p)) return (actual.lstatSync as (...a: unknown[]) => unknown)(p, ...rest);
    const error = new Error(`ENOENT: no such file or directory, lstat '${p}'`) as NodeJS.ErrnoException;
    error.code = "ENOENT";
    throw error;
  };
  return { ...actual, realpathSync, lstatSync, default: { ...actual, realpathSync, lstatSync } };
});

import { BashProgram } from "#src/access-intent/bash/program";
import { warmBashParser } from "#src/access-intent/bash/parser";
import { win32PathFlavor, posixPathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";

/** The fixture tree: the same layout the Go test builds (bashprogram_oracle_test.go). */
function buildFixture(real: (p: string) => string): { base: string; root: string; out: string } {
  // The temp directory itself may be a symlink (macOS); the fixture is addressed by its canonical path. The root sits 16 directories
  // below the base, so no `..` chain of the corpus climbs out of the base (whose own ancestors differ from host to host).
  const base = real(mkdtempSync(join(tmpdir(), "pbp-")));
  const deep = join(base, ...Array.from({ length: 16 }, () => "d"));
  const root = join(deep, "r");
  const out = join(deep, "o");
  mkdirSync(deep, { recursive: true });
  mkdirSync(root);
  mkdirSync(out);
  for (const f of ["id_rsa", "inside.txt", ".env", ".some.secret"]) writeFileSync(join(root, f), "x");
  for (const d of ["src", "nested", "nested/sub", "a", "a/b", "packages", "packages/sub", "docs"]) mkdirSync(join(root, d));
  for (const f of ["foo.ts", "index.ts", "main.ts", "file.txt", "secret.env"]) writeFileSync(join(root, "src", f), "x");
  writeFileSync(join(root, "nested", "inner.txt"), "x");
  writeFileSync(join(out, "secret"), "s");
  writeFileSync(join(out, "other"), "s");
  mkdirSync(join(out, "sub"));
  symlinkSync(join(root, ".some.secret"), join(root, "a_sym"));
  symlinkSync(join(out, "secret"), join(root, "outside-link"));
  symlinkSync(out, join(root, "vault"));
  symlinkSync(join(root, "gone"), join(root, "dangling"));
  symlinkSync(join(out, "secret"), join(root, "v1..v2"));
  symlinkSync(out, join(root, "link"));
  symlinkSync(join(root, "src"), join(root, "inlink"));
  return { base, root, out };
}

const FAKE_CWD = "/projects/my-app";
const WIN_CWD = "C:\\Projects\\App";

// `placed`: the configurations whose cwd is the fixture root get `@R@` and `@O@` replaced by the fixture paths; the others keep the
// placeholders as literal text (a win32 flavor lowercases and rewrites separators, so a host path could not be written back).
function scenarios(root: string) {
  return [
    { name: "root", placed: true, normalizer: new PathNormalizer(posixPathFlavor, root), workdir: undefined as string | undefined },
    { name: "fake", placed: false, normalizer: new PathNormalizer(posixPathFlavor, FAKE_CWD), workdir: undefined },
    { name: "win", placed: false, normalizer: new PathNormalizer(win32PathFlavor, WIN_CWD), workdir: undefined },
    { name: "rootwd", placed: true, normalizer: new PathNormalizer(posixPathFlavor, root), workdir: `${root}/nested` },
    { name: "fakewd", placed: false, normalizer: new PathNormalizer(posixPathFlavor, FAKE_CWD), workdir: "/etc" },
    { name: "winwd", placed: false, normalizer: new PathNormalizer(win32PathFlavor, WIN_CWD), workdir: "/c/work" },
  ];
}

it("records the original's BashProgram", { timeout: 0 }, async () => {
  const input = process.env.DIFF_IN;
  const output = process.env.DIFF_OUT;
  if (!input || !output) throw new Error("DIFF_IN and DIFF_OUT are required");
  // A home that exists on no host, so the recording is the same wherever it is checked.
  process.env.HOME = "/probe-home/user";
  const inputs: string[] = JSON.parse(readFileSync(input, "utf8"));
  const actual = await vi.importActual<typeof import("node:fs")>("node:fs");
  const { base, root, out } = buildFixture((p) => actual.realpathSync(p));
  fixture.root = root;
  fixture.out = out;
  const place = (s: string) => s.replaceAll("@R@", root).replaceAll("@O@", out);
  const unplace = (s: string) => s.replaceAll(root, "@R@").replaceAll(out, "@O@").replaceAll(base, "@B@");
  await warmBashParser();
  const configs = scenarios(root);
  const fd = openSync(output, "w");
  let buf: string[] = [];
  for (const s of inputs) {
    const rec: Record<string, unknown> = { s };
    for (const { name, placed, normalizer, workdir } of configs) {
      const command = placed ? place(s) : s;
      try {
        const program = await BashProgram.parse(command, normalizer, workdir === undefined ? undefined : { workdir });
        const view: Record<string, unknown> = {
          ext: program.externalAccesses().map(({ path, effect }) => ({
            p: path.value(),
            b: path.boundaryValue(),
            e: effect.effect,
            s: effect.source,
          })),
          rules: program.pathRuleCandidates().map(({ token, path, effect }) => ({
            t: token,
            p: path.value(),
            b: path.boundaryValue(),
            m: path.matchValues(),
            e: effect.effect,
            s: effect.source,
          })),
        };
        if (name === "root") view.cmds = program.commands();
        rec[name] = view;
      } catch (e) {
        rec[name] = { throw: (e as Error).message };
      }
    }
    buf.push(`${unplace(JSON.stringify(rec))}\n`);
    if (buf.length > 200) {
      writeSync(fd, buf.join(""));
      buf = [];
    }
  }
  if (buf.length) writeSync(fd, buf.join(""));
  closeSync(fd);
});
