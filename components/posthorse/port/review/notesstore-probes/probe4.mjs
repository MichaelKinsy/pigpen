import { mkdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const S = "/home/kinsy/ph-work/scratch/notesstore";
const { canonicalPath } = await import(pathToFileURL(`${S}/oracle/src/note-scopes.ts`).href);
const { fileDigest, readManifest } = await import(pathToFileURL(`${S}/oracle/src/note-snapshots.ts`).href);
const dir = `${S}/p4`;
rmSync(dir, { recursive: true, force: true });
mkdirSync(`${dir}/root/x`, { recursive: true });
mkdirSync(`${dir}/elsewhere/deep`, { recursive: true });
mkdirSync(`${dir}/elsewhere/x`, { recursive: true });
symlinkSync(`${dir}/elsewhere/deep`, `${dir}/root/link`);
symlinkSync("loop", `${dir}/root/loop`);
const out = {};
out.dotdot = canonicalPath(`${dir}/root/link/../x`);
out.dotdotMissing = canonicalPath(`${dir}/root/link/../x/new.md`);
for (const p of [`${dir}/root/loop/file.md`, `${dir}/root/loop`]) {
  try { canonicalPath(p); } catch (e) { out[p] = e.message; }
}
try { await fileDigest(`${dir}/missing.md`); } catch (e) { out.digest = e.message; }
// manifest with a lone surrogate absent path round trip through readManifest
mkdirSync(`${dir}/m/.forks/2026-01-02T03-04-05-678Z_s1/notes`, { recursive: true });
const binding = { version: 1, kind: "fork", owner: { id: "s1", timestamp: "2026-01-02T03:04:05.678Z" }, root: `${dir}/m`, scope: ".forks/2026-01-02T03-04-05-678Z_s1/notes", source: null, seeds: [], absent: ["\ud800.md"] };
writeFileSync(`${dir}/m/.forks/2026-01-02T03-04-05-678Z_s1/manifest.json`, JSON.stringify(binding));
const m = await readManifest(`${dir}/m/.forks/2026-01-02T03-04-05-678Z_s1/notes`);
out.manifestAbsent = m === undefined ? "undefined" : [m.absent[0].length, m.absent[0].charCodeAt(0).toString(16), JSON.stringify(m.absent)];
console.log(JSON.stringify(out, null, 1));
rmSync(dir, { recursive: true, force: true });
