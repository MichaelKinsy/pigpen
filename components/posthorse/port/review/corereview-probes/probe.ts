import { mkdtempSync, mkdirSync, realpathSync, writeFileSync } from "node:fs";
import { execSync } from "node:child_process";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { createServer } from "node:net";
import { relative } from "node:path";
const base = realpathSync(mkdtempSync(join(tmpdir(), "rvcore-")));
process.env.PI_CODING_AGENT_DIR = join(base, "agent");
const { getAgentDir } = await import("@earendil-works/pi-coding-agent");
const { notesDir, publishFile, readText, isBinaryFile } = await import("./src/files.ts");
const { sessionLines } = await import("./src/session-lines.ts");
console.log("agentDir", getAgentDir() === join(base, "agent"));
console.log("REL", JSON.stringify(relative("/a/b", "/a/b")));
const installs = join(base, "agent", "git");
mkdirSync(join(installs, ".pi", "notes"), { recursive: true });
execSync("git init -q", { cwd: installs });
console.log("A", notesDir(installs).replace(base, "<base>"));
const pg = join(base, "proj", ".pi", "git");
mkdirSync(join(pg, ".pi", "notes"), { recursive: true });
execSync("git init -q", { cwd: pg });
console.log("A2", notesDir(pg).replace(base, "<base>"));
const c = new AbortController(); c.abort();
try { await publishFile(join(base, "n.md"), "x", c.signal); } catch (e: any) { console.log("B", JSON.stringify(e.message)); }
const sf = join(base, "s.jsonl");
writeFileSync(sf, Buffer.concat([Buffer.from('{"type":"x"}\nx'), Buffer.from([0xe0, 0x80]), Buffer.from("\n")]));
const c2 = new AbortController();
for await (const line of sessionLines(sf, c2.signal)) { c2.abort(); try { await line.text(); } catch (e: any) { console.log("C", JSON.stringify(e.message)); } break; }
for await (const line of sessionLines(sf, undefined, { test: (t) => { console.log("W", JSON.stringify(t)); return false; }, encoding: "utf8", overlap: 8 })) { void line; }
try { await readText(base); } catch (e: any) { console.log("D", JSON.stringify(e.message.replace(base, "<base>"))); }
const sock = join(base, "sock");
const server = createServer(); await new Promise<void>((r) => server.listen(sock, r));
try { await isBinaryFile(sock); } catch (e: any) { console.log("E", JSON.stringify(e.message.replace(base, "<base>"))); }
server.close();
execSync(`rm -rf ${base}`);
