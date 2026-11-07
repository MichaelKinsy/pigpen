import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const S = "/home/kinsy/ph-work/scratch/notesstore";
const s = await import(pathToFileURL(`${S}/oracle/src/note-scopes.ts`).href);
const segs = ["a", ".", "..", "...", ".forks", ".forksx", "", " ", "é", "😀", "a b", "x.md", "\\", "a\\b", ".hidden", "notes", "\t", "%2e%2e"];
const paths = new Set(["", "/", "//", "/a", "a/", "./", ".", "..", "a\0b"]);
for (const a of segs) for (const b of segs) { paths.add(`${a}/${b}`); paths.add(`${a}${b}`); for (const c of ["x.md", ".forks", ".."]) paths.add(`${a}/${b}/${c}`); }
const rel = [...paths].map((p) => { try { return { p, ok: s.noteRelative(p) }; } catch (e) { return { p, err: e.message }; } });
const dirs = ["/p/notes", "/p/notes/", "/p/notes/deep", "/p", "/q//", "C:\\r\\notes", "/p/notes/.forks/x/notes", ""];
const targets = ["/p/notes/a.md", "/p/notes/.forks/x/notes/a.md", "/p/notes/.forks", "/p/notes/.forks/", "/p/notes", "/p/notes/", "/p/notesx/a", "/q/a", "/q//a", "/q///a", "C:/r/notes/a.md", "C:\\r\\notes\\.forks\\a", "/p/notes/deep/a", "/p/x", "/p/notes//a.md", "/p/notes/./a"];
const match = [];
for (const t of targets) for (let i = 0; i < dirs.length; i++) { const d = [dirs[i], ...dirs.filter((_, j) => j !== i)]; match.push({ t, d, r: s.matchNoteDirectory(t, d) ?? null }); }
const ins = [];
for (const d of ["/p", "/p/", "/p/notes", "/"]) for (const t of ["/p", "/p/", "/p/notes", "/p/notes/x", "/px", "/p/../q", "/p/..x", "/p/..", "/"]) ins.push({ d, t, r: s.inside(d, t) });
const ids = ["s1", "a b/c", "é😀", "!'()*-._~", "%", "+=&?#", "\u2028", "", "x".repeat(40)];
const stamps = ["2026-01-02T03:04:05.678Z", "1970-01-01T00:00:00.000Z", "0000-01-01T00:00:00.000Z", "9999-12-31T23:59:59.999Z", "2024-02-29T12:00:00.000Z"];
const fork = [];
for (const id of ids) for (const timestamp of stamps) { try { fork.push({ id, timestamp, r: s.forkScope({ id, timestamp }) }); } catch (e) { fork.push({ id, timestamp, err: e.message }); } }
writeFileSync(`${S}/paths-cases.json`, JSON.stringify({ rel, match, ins, fork }));
console.log(rel.length, match.length, ins.length, fork.length);
