// Runs the real `pig` binary in a tmux pane against a fake herdr socket (herdr-pig's transport: one JSON line per
// connection, answered with one line) and PiG's hermetic test-faux provider. Needs PIG_BIN (a PiG built from the reviewed
// source), tmux and a Go toolchain: `PIG_BIN=/path/to/pig npm run test:pig`.
// It runs the generated manifest under dist/staged (the script stages first).
// With PIG_SOURCE_ROOT (a git checkout of that PiG source) it also builds each
// Piglet into a Binary with `pig piglet build --format binary` and runs the
// Binary the same way. No Node extension takes part: the reporter and the /ask
// fixture are Go extensions.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, before, describe, it } from "node:test";
import { fileURLToPath } from "node:url";
import { goCaches } from "../../../scripts/go-modules.mjs";
import { requirePig } from "../../../scripts/pig-bin.mjs";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const pigBin = requirePig();
execFileSync(process.execPath, [path.join(repoRoot, "scripts/stage-piglets.mjs")], { stdio: "pipe" });

// Both Piglets that select the reporter Package must behave the same as a Binary. pig-with-batteries strips the Go
// extension SDK (strip.features), so pig 0.5.0 cannot build its Go Packages when run from source: it runs as a Binary only.
const piglets = ["herdr", "pig-with-batteries"];
// The manifests list every release platform; pig builds only the machine it runs on.
const hostTarget = `${{ linux: "linux", darwin: "darwin", win32: "windows" }[process.platform as string]}/${{ x64: "amd64", arm64: "arm64" }[process.arch as string]}`;
const sourceRoot = process.env.PIG_SOURCE_ROOT;
// The prompt-based blocked state is opt-in (PIGPEN_HERDR_UI_PROMPTS=1): every run has it on, and one source run has it off.
const runs = [
	...piglets.flatMap((piglet) => [
		...(piglet === "herdr" ? [{ piglet, binary: false, prompts: true }] : []),
		...(sourceRoot ? [{ piglet, binary: true, prompts: true }] : []),
	]),
	{ piglet: "herdr", binary: false, prompts: false },
];
// pig-with-batteries strips the Go extension SDK, so `-e` cannot load the Go /ask fixture there: it gets no dialog step.
const dialogPiglets = new Set(["herdr"]);
let session = "";
let dir: string;
let server: Server;
// Open connections to the fake herdr: net.Server has no closeAllConnections (http.Server does).
const connections = new Set<Socket>();
// Every request line the fake herdr received, parsed.
let received: { method: string; params: Record<string, unknown> }[] = [];

const tmux = (...args: string[]) => execFileSync("tmux", args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
const type = (text: string) => tmux("send-keys", "-t", session, text, "Enter");
const key = (name: string) => tmux("send-keys", "-t", session, name);
const screen = () => tmux("capture-pane", "-p", "-t", session);
const calls = () => received;

async function until(what: string, ready: () => boolean): Promise<void> {
	const deadline = Date.now() + 30_000;
	while (!ready()) {
		assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${screen()}\n--- calls ---\n${JSON.stringify(calls())}`);
		await new Promise((resolve) => setTimeout(resolve, 50));
	}
}

const state = (request: { method: string; params: Record<string, unknown> }) => (request.method === "pane.release_agent" ? "release" : String(request.params.state));

for (const { piglet, binary, prompts } of runs) describe(`real pig in a herdr pane: ${piglet} ${binary ? "Binary" : "from source"}${prompts ? "" : ", prompts off"}`, () => {
	const dialog = dialogPiglets.has(piglet);
	before(async () => {
		session = `herdr-piglet-${piglet}-${binary ? "bin" : "src"}${prompts ? "" : "-noprompts"}-${process.pid}`;
		const manifest = path.join(repoRoot, "dist/staged/piglets", piglet, "piglet.yaml");
		dir = mkdtempSync(path.join(tmpdir(), "herdr-piglet-pig-"));
		received = [];
		for (const sub of ["agent", "home", "work"]) mkdirSync(path.join(dir, sub));
		// A unix socket path is limited to about 104 bytes: keep it short.
		const socket = path.join(mkdtempSync(path.join(tmpdir(), "hd")), "s");
		server = createServer((conn) => {
			connections.add(conn);
			conn.on("close", () => connections.delete(conn));
			let buffer = "";
			conn.on("error", () => {});
			conn.on("data", (chunk) => {
				buffer += chunk.toString();
				for (let newline = buffer.indexOf("\n"); newline >= 0; newline = buffer.indexOf("\n")) {
					received.push(JSON.parse(buffer.slice(0, newline)));
					buffer = buffer.slice(newline + 1);
					conn.write('{"result":{}}\n');
				}
			});
		});
		await new Promise<void>((resolve) => server.listen(socket, resolve));
		const env = {
			HERDR_ENV: "1", HERDR_PANE_ID: "p_1", HERDR_SOCKET_PATH: socket,
			...(prompts ? { PIGPEN_HERDR_UI_PROMPTS: "1" } : {}),
			HOME: path.join(dir, "home"), PIG_HOME: path.join(dir, "pighome"),
			PIG_CODING_AGENT_DIR: path.join(dir, "agent"), PI_CODING_AGENT_DIR: path.join(dir, "agent"),
			PIG_TEST_FAUX: "1", PIG_TEST_FAUX_SCENARIO: "parity-basic", TERM: "xterm-256color",
			PATH: process.env.PATH!,
			// The pane runs pig, which builds the Go extensions: it must not depend on what a long-running tmux
			// server inherited (a server started without the module cache leaves pig no third-party modules).
			...goCaches(),
		};
		const exports = Object.entries(env).map(([name, value]) => `${name}='${value}'`).join(" ");
		const ask = path.join(repoRoot, "piglets/herdr/tests/fixtures/ask");
		let command: string;
		if (binary) {
			const built = path.join(dir, "piglet-binary");
			execFileSync(pigBin, ["piglet", "build", manifest, "--format", "binary", "--out", built, "--targets", hostTarget], {
				stdio: "pipe",
				env: {
					...process.env, ...goCaches(),
					HOME: path.join(dir, "home"), PIG_HOME: path.join(dir, "pighome"),
					PIG_CODING_AGENT_DIR: path.join(dir, "agent"), PI_CODING_AGENT_DIR: path.join(dir, "agent"),
					PIG_SOURCE_ROOT: sourceRoot!, GIT_TERMINAL_PROMPT: "0",
				},
			});
			command = `env ${exports} '${built}' --model test-faux/faux-1${dialog ? ` -e '${ask}'` : ""}`;
		} else {
			command = `env ${exports} '${pigBin}' --model test-faux/faux-1 --piglet '${manifest}' -e '${ask}'`;
		}
		tmux("new-session", "-d", "-s", session, "-x", "140", "-y", "40", "-c", path.join(dir, "work"), command);
	});

	after(async () => {
		await new Promise<void>((resolve) => { for (const conn of connections) conn.destroy(); server.close(() => resolve()); });
		try {
			tmux("kill-session", "-t", session);
		} catch {
			// The session already ended.
		}
		rmSync(dir, { recursive: true, force: true });
	});

	it(prompts ? "reports idle, working, blocked on a confirmation, and releases on quit" : "reports idle, working, no blocked state without the opt-in, and releases on quit", async () => {
		await until("the startup idle report", () => calls().length >= 1);
		await until("the editor", () => screen().includes("faux-1"));

		type("TUI_LIVE_STREAM");
		await until("working then idle", () => calls().map(state).join(",") === "idle,working,idle");

		if (dialog) {
			type("/ask");
			await until("the confirmation", () => screen().includes("Allow rm -rf build?"));
			if (prompts) {
				await until("the blocked report", () => calls().map(state).at(-1) === "blocked");
				assert.equal(calls().at(-1)!.params.message, "Allow rm -rf build?");
			} else {
				await new Promise((resolve) => setTimeout(resolve, 1000));
				assert.equal(calls().map(state).join(","), "idle,working,idle", "a dialog must not report blocked without the opt-in");
			}

			key("Enter");
			if (prompts) await until("idle after the answer", () => calls().map(state).at(-1) === "idle");
		}

		type("/quit");
		await until("the release", () => calls().at(-1)?.method === "pane.release_agent");

		const all = calls();
		assert.deepEqual(all.map(state), prompts && dialog ? ["idle", "working", "idle", "blocked", "idle", "release"] : ["idle", "working", "idle", "release"]);
		const seqs = all.map((request) => Number(request.params.seq));
		assert.ok(seqs.every((n, i) => i === 0 || n > seqs[i - 1]!), `seq not increasing: ${seqs}`);
		for (const request of all) {
			assert.equal(request.params.pane_id, "p_1");
			assert.equal(request.params.source, "pig");
			assert.equal(request.params.agent, "pig");
		}
		// Every state report carries the resume command, as herdr-pig sends it: pig --session <the session file>.
		for (const request of all.filter((r) => r.method === "pane.report_agent")) {
			const resume = request.params.resume_argv as string[];
			assert.match(resume[2]!, /^\/.*\.jsonl$/);
			assert.deepEqual(resume, ["pig", "--session", resume[2]!]);
			assert.equal(request.params.agent_session_path, undefined);
		}
		assert.equal(all.at(-1)!.params.resume_argv, undefined, "release must not carry a resume command");
	});
});
