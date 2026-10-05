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
		description: "harness: fork from the first user message",
		handler: async (_args: string, ctx: any) => {
			const entry = ctx.sessionManager.getEntries().find((e: any) => e.type === "message" && e.message?.role === "user");
			if (!entry) {
				ctx.ui.notify("eq-driver fork no-user-message", "error");
				return;
			}
			const result = await ctx.fork(entry.id);
			if (result.cancelled) ctx.ui.notify("eq-driver fork cancelled", "info");
		},
	});
}
