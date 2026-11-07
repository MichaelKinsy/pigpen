// Harness driver extension, loaded next to the extension under test in every
// lane. RPC has no command for session operations that only an extension
// command context can start (and PiG's RPC loop handles new_session inline, so a
// dialog raised while it runs cannot be answered). A cancelled operation is
// reported through ctx.ui.notify so the result is part of the trace. After a
// replacement that went ahead the old ctx is stale (Pi rejects it) and a fresh
// ctx's notifications are not delivered by PiG's RPC UI, so nothing is reported
// then: "no cancelled notice" is the observable.
export default function (pi: any) {
	pi.registerCommand("eq-new", {
		description: "harness: start a new session",
		handler: async (_args: string, ctx: any) => {
			const result = await ctx.newSession();
			if (result.cancelled) ctx.ui.notify("eq-driver new cancelled", "info");
		},
	});
	pi.registerCommand("eq-fork", {
		description: "harness: fork from the first user message, or from an entry id (second word `at` forks after it)",
		handler: async (args: string, ctx: any) => {
			const [id, position] = args.trim().split(/\s+/).filter(Boolean);
			const entry = id
				? ctx.sessionManager.getEntries().find((e: any) => e.id === id)
				: ctx.sessionManager.getEntries().find((e: any) => e.type === "message" && e.message?.role === "user");
			if (!entry) {
				ctx.ui.notify(id ? "eq-driver fork no-such-entry" : "eq-driver fork no-user-message", "error");
				return;
			}
			const result = await ctx.fork(entry.id, position === "at" ? { position: "at" } : undefined);
			if (result.cancelled) ctx.ui.notify("eq-driver fork cancelled", "info");
		},
	});
	pi.registerCommand("eq-tree", {
		description: "harness: navigate the session tree to an entry id (no summary)",
		handler: async (args: string, ctx: any) => {
			const id = args.trim();
			if (!id) {
				ctx.ui.notify("eq-driver tree needs an entry id", "error");
				return;
			}
			const result = await ctx.navigateTree(id, { summarize: false });
			if (result.cancelled) ctx.ui.notify("eq-driver tree cancelled", "info");
		},
	});
	pi.registerCommand("eq-reload", {
		description: "harness: reload extensions, skills, prompts and themes",
		handler: async (_args: string, ctx: any) => {
			await ctx.reload();
		},
	});
}
