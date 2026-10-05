// Runs the real `pig` binary in a tmux pane with a fake HERDR_BIN_PATH and PiG's
// hermetic test-faux provider. Needs PIG_BIN (a PiG built from the reviewed
// source), tmux and a Go toolchain: `PIG_BIN=/path/to/pig npm run test:pig`.
// It runs the generated manifest under dist/staged (the script stages first).
// With PIG_SOURCE_ROOT (a git checkout of that PiG source) it also builds each
// Piglet into a Binary with `pig piglet build --format binary` and runs the
// Binary the same way. No Node extension takes part: the reporter and the /ask
// fixture are Go extensions.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { after, before, describe, it } from "node:test";
import { fileURLToPath } from "node:url";
import { goCaches } from "../../../scripts/go-modules.mjs";
import { requirePig } from "../../../scripts/pig-bin.mjs";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const pigBin = requirePig();
execFileSync(process.execPath, [path.join(repoRoot, "scripts/stage-piglets.mjs")], { stdio: "pipe" });

// Both Piglets that select the reporter Package must behave the same, from source and as a Binary.
const piglets = ["herdr", "pig-with-batteries"];
// The manifests list every release platform; pig builds only the machine it runs on.
const hostTarget = `${{ linux: "linux", darwin: "darwin", win32: "windows" }[process.platform as string]}/${{ x64: "amd64", arm64: "arm64" }[process.arch as string]}`;
const sourceRoot = process.env.PIG_SOURCE_ROOT;
const runs = piglets.flatMap((piglet) => [
	{ piglet, binary: false },
	...(sourceRoot ? [{ piglet, binary: true }] : []),
]);
let session = "";
let dir: string;
let log: string;

const tmux = (...args: string[]) => execFileSync("tmux", args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
const type = (text: string) => tmux("send-keys", "-t", session, text, "Enter");
const key = (name: string) => tmux("send-keys", "-t", session, name);
const screen = () => tmux("capture-pane", "-p", "-t", session);
const calls = () => {
	try {
		return readFileSync(log, "utf8").split("\n").filter(Boolean);
	} catch {
		return [];
	}
};

async function until(what: string, ready: () => boolean): Promise<void> {
	const deadline = Date.now() + 30_000;
	while (!ready()) {
		assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${screen()}\n--- calls ---\n${calls().join("\n")}`);
		await new Promise((resolve) => setTimeout(resolve, 50));
	}
}

const state = (line: string) => / --state (\w+)/.exec(line)?.[1] ?? line.split(" ")[1];

for (const { piglet, binary } of runs) describe(`real pig in a herdr pane: ${piglet} ${binary ? "Binary" : "from source"}`, () => {
	before(() => {
		session = `herdr-piglet-${piglet}-${binary ? "bin" : "src"}-${process.pid}`;
		const manifest = path.join(repoRoot, "dist/staged/piglets", piglet, "piglet.yaml");
		dir = mkdtempSync(path.join(tmpdir(), "herdr-piglet-pig-"));
		log = path.join(dir, "calls.log");
		for (const sub of ["agent", "home", "work"]) mkdirSync(path.join(dir, sub));
		const fake = path.join(dir, "herdr");
		writeFileSync(fake, `#!/bin/sh\necho "$@" >> "${log}"\n`);
		chmodSync(fake, 0o755);
		const env = {
			HERDR_ENV: "1", HERDR_PANE_ID: "p_1", HERDR_BIN_PATH: fake, HERDR_SOCKET_PATH: path.join(dir, "herdr.sock"),
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
			command = `env ${exports} '${built}' --model test-faux/faux-1 -e '${ask}'`;
		} else {
			command = `env ${exports} '${pigBin}' --model test-faux/faux-1 --piglet '${manifest}' -e '${ask}'`;
		}
		tmux("new-session", "-d", "-s", session, "-x", "140", "-y", "40", "-c", path.join(dir, "work"), command);
	});

	after(() => {
		try {
			tmux("kill-session", "-t", session);
		} catch {
			// The session already ended.
		}
		rmSync(dir, { recursive: true, force: true });
	});

	it("reports idle, working, blocked on a confirmation, and releases on quit", async () => {
		await until("the startup idle report", () => calls().length >= 1);
		await until("the editor", () => screen().includes("faux-1"));

		type("TUI_LIVE_STREAM");
		await until("working then idle", () => calls().map(state).join(",") === "idle,working,idle");

		type("/ask");
		await until("the confirmation", () => screen().includes("Allow rm -rf build?"));
		await until("the blocked report", () => calls().map(state).at(-1) === "blocked");
		assert.match(calls().at(-1)!, / --message Allow rm -rf build\? /);

		key("Enter");
		await until("idle after the answer", () => calls().map(state).at(-1) === "idle");

		type("/quit");
		await until("the release", () => calls().at(-1)?.startsWith("pane release-agent p_1") === true);

		const all = calls();
		assert.deepEqual(all.map(state), ["idle", "working", "idle", "blocked", "idle", "release-agent"]);
		const seqs = all.map((line) => Number(/ --seq (\d+)/.exec(line)![1]));
		assert.ok(seqs.every((n, i) => i === 0 || n > seqs[i - 1]!), `seq not increasing: ${seqs}`);
		for (const line of all) assert.match(line, /--source custom:pig --agent pig/);
		assert.match(all[0]!, /--agent-session-path \/.*\.jsonl --agent-session-id [0-9a-f-]+/);
		// Every state report carries the resume command after `--`: pig --session <the session file>.
		for (const line of all.filter((l) => l.startsWith("pane report-agent"))) {
			const file = /--agent-session-path (\S+\.jsonl)/.exec(line)![1];
			assert.ok(line.endsWith(` -- pig --session ${file}`), `no resume command at the end of: ${line}`);
		}
		assert.doesNotMatch(all.at(-1)!, / -- /, "release must not carry a resume command");
	});
});
