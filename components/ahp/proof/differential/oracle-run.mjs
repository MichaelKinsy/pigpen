// The oracle side of the protocol differential: pi-ahp's own AhpHost and ChangesetService (src/,
// unmodified) driven by a scripted client over an in-memory transport, with a scripted git backend
// and watcher (the shape of its changeset-lifecycle tests). Prints the host's wire transcript as
// JSON. The Go side is extensions/ahp/internal/changeset/differential_test.go; both run the same
// steps (see PORT.md "Protocol differential").
//   node oracle-run.mjs <oracle directory with its dependencies installed> <workspace directory>
import { EventEmitter } from "node:events";
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const [oracle, workspace] = process.argv.slice(2);
const load = (p) => import(pathToFileURL(join(oracle, p)).href);
const { AhpHost } = await load("src/core/host.ts");
const { installRootChannel } = await load("src/channels/root.ts");
const { initialChatState } = await load("src/channels/chat.ts");
const { ChangesetService } = await load("src/pi/changeset-service.ts");
const { piChangesetUri } = await load("src/pi/changeset-uri.ts");

const id = "diff-session";
const sessionChannel = `ahp-session:/${id}`;
const chatChannel = `ahp-chat:/${id}`;
const session = { uri: sessionChannel, sessionId: id, workingDirectory: workspace };
const host = new AhpHost();
installRootChannel(host, []);
host.store.create(sessionChannel, {
	provider: "pi", title: "Differential", status: 1, lifecycle: "ready", // SessionStatus.Idle is the number 1
	workingDirectories: [pathToFileURL(workspace).toString()], activeClients: [], chats: [],
}, "session");
host.store.create(chatChannel, initialChatState(chatChannel, "Differential"), "chat");

let computeCalls = 0;
const git = {
	async inspect() {
		return { cwd: workspace, repositoryRoot: workspace, gitDirectory: join(workspace, ".git"), commonGitDirectory: join(workspace, ".git"), pathspec: ".", head: "a".repeat(40), parent: "b".repeat(40) };
	},
	async compute() {
		computeCalls += 1;
		const uri = pathToFileURL(join(workspace, "result.txt")).toString();
		return [{ id: uri, edit: { after: { uri, content: { uri } }, diff: { added: computeCalls, removed: 0 } } }];
	},
	async ignoredDirectories() { return []; },
	pathForBlob() { return undefined; },
	async readBlob() { throw new Error("not used"); },
};
class Watcher extends EventEmitter { async close() {} }
const watchers = [];
const watchFactory = () => { const w = new Watcher(); watchers.push(w); queueMicrotask(() => w.emit("ready")); return w; };
const service = new ChangesetService(host, {
	getSession: (s) => (s === id ? session : undefined),
	getSessionByChat: (c) => (c === chatChannel ? session : undefined),
	hydrateSession: async (s) => (s === id ? session : undefined),
	git, watchFactory, debounceMs: 5, stateGraceMs: 0,
});
await service.attach(session);
host.serve({ hydrator: service });

const frames = [];
const handlers = [];
const transport = {
	send: (m) => frames.push(structuredClone(m)),
	close() {},
	onMessage: (h) => handlers.push(h),
	onClose() {},
};
host.accept(transport);
let nextId = 0;
const rpc = (method, params) => { nextId += 1; for (const h of handlers) h({ jsonrpc: "2.0", id: nextId, method, params }); };
const notify = (method, params) => { for (const h of handlers) h({ jsonrpc: "2.0", method, params }); };
const until = async (what, pred) => {
	const end = Date.now() + 10_000;
	while (!pred()) { if (Date.now() > end) throw new Error(`timed out waiting for ${what}`); await new Promise((r) => setTimeout(r, 5)); }
};
const settle = () => new Promise((r) => setTimeout(r, 50));
const channel = piChangesetUri(id, "uncommitted");
const status = () => host.store.get(channel)?.status;

rpc("initialize", { channel: "ahp-root://", clientId: "diff-client", protocolVersions: ["0.9.0"] });
await settle();
rpc("subscribe", { channel: sessionChannel });
await settle();
rpc("subscribe", { channel });
await until("the first scan", () => status() === "ready");
await settle();
watchers.at(-1).emit("change", join(workspace, "during-turn.ts"));
await until("the refresh", () => computeCalls >= 2 && status() === "ready");
await settle();
notify("dispatchAction", { channel, clientSeq: 1, action: { type: "changeset/filesReviewChanged", files: ["x"], reviewed: true } });
await settle();
notify("unsubscribe", { channel });
await until("eviction", () => !host.store.has(channel));
await settle();
rpc("subscribe", { channel: `ahp-changeset:/${id}/unknown` });
await settle();
rpc("ping", { channel: "ahp-root://" });
await settle();
await service.dispose();
console.log(JSON.stringify(frames));
