import { writeFileSync, mkdirSync, symlinkSync, rmSync, statSync, realpathSync, utimesSync } from "node:fs";
import { copyFile, stat, utimes, open } from "node:fs/promises";
import { constants } from "node:fs";
import { execFileSync } from "node:child_process";
const dir = "/home/kinsy/ph-work/scratch/notesstore/p1";
rmSync(dir, { recursive: true, force: true });
mkdirSync(dir, { recursive: true });
const out = {};
// utimes: copy the exact sequence of note-snapshots.ts copyNote
const src = `${dir}/src.md`, dst = `${dir}/dst.md`;
writeFileSync(src, "hello");
execFileSync("touch", ["-m", "-d", "@1762430000.123456789", src]);
const before = await stat(src);
await copyFile(src, dst, constants.COPYFILE_EXCL);
await utimes(dst, before.atimeMs / 1000, before.mtimeMs / 1000);
const after = await stat(dst);
const srcNs = statSync(src, { bigint: true }).mtimeNs, dstNs = statSync(dst, { bigint: true }).mtimeNs;
out.utimes = { srcMtimeMs: before.mtimeMs, dstMtimeMs: after.mtimeMs, srcNs: String(srcNs), dstNs: String(dstNs), json: JSON.stringify({ m: after.mtimeMs }) };
// realpath ELOOP via a non-final component
symlinkSync("loop", `${dir}/loop`);
for (const p of [`${dir}/loop`, `${dir}/loop/file.md`, `${dir}/loop/a/b.md`]) {
  try { realpathSync(p); out[p] = "ok"; } catch (e) { out[p] = `${e.code} ${e.syscall} ${e.path} | ${e.message}`; }
}
// fileDigest open error text
try { await open(`${dir}/missing.md`, "r"); } catch (e) { out.open = e.message; }
// stat error text
try { await stat(`${dir}/missing.md`); } catch (e) { out.stat = e.message; }
// JSON.parse lone surrogate
const parsed = JSON.parse('{"absent":["\\ud800.md"]}');
out.lone = [parsed.absent[0].length, parsed.absent[0].charCodeAt(0).toString(16), JSON.stringify(parsed)];
console.log(JSON.stringify(out, null, 1));
rmSync(dir, { recursive: true, force: true });
