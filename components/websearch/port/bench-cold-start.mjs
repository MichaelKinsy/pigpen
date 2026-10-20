// Time from import to registration of the original extension: load index.ts and call its default export
// with a stub `pi` until it has registered everything. For the cold-start comparison of the port PR.
//   node port/bench-cold-start.mjs
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const dir = await mkdtemp(join(tmpdir(), "pigpen-cold-"));
await writeFile(join(dir, "web-search.json"), "{}");
process.env.PI_CODING_AGENT_DIR = dir;
const start = process.hrtime.bigint();
const registered = [];
const pi = {
	registerTool(tool) { registered.push(tool.name); },
	registerShortcut() {},
	registerCommand() {},
	on() {},
	appendEntry() {},
	sendMessage() {},
};
const extension = (await import(new URL("./oracle/index.ts", import.meta.url).href)).default;
extension(pi);
const ms = Number(process.hrtime.bigint() - start) / 1e6;
console.log(JSON.stringify({ registrationMs: ms, tools: registered.length }));
