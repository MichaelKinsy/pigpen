// Card probe for the tmux comparison: the original's own toolCards and message renderers (port/oracle/ui.ts) behind four
// tools whose results are canned (fixtures.json, selected by the call's `fixture` argument), so a screen shows the cards
// without any note, history or budget state. The Go twin is probe.go.tmpl; both read the same fixtures.
import { readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { registerPosthorseMessages, toolCards } from "../../oracle/ui.ts";

interface Fixture {
	text?: string;
	details?: unknown;
	error?: string;
	images?: number;
	delayMs?: number;
	partial?: { text: string; details?: unknown };
}

const fixtures: Record<string, Fixture> = JSON.parse(
	readFileSync(process.env.POSTHORSE_TMUX_FIXTURES ?? "", "utf8"),
).tools;
const messages: Record<string, { customType: string; content: string; details?: unknown }> = JSON.parse(
	readFileSync(process.env.POSTHORSE_TMUX_FIXTURES ?? "", "utf8"),
).messages;
const pixel =
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

export default function probe(pi: ExtensionAPI): void {
	for (const name of ["notes", "history", "get_context_remaining", "new_context"] as const) {
		pi.registerTool({
			name,
			label: name,
			description: `${name} card probe`,
			parameters: Type.Object({}, { additionalProperties: true }),
			...toolCards(name),
			async execute(_id, params, _signal, onUpdate) {
				const fixture = fixtures[String((params as { fixture?: unknown }).fixture)];
				if (fixture === undefined) {
					throw new Error("unknown fixture");
				}
				if (fixture.partial !== undefined) {
					onUpdate?.({
						content: [{ type: "text", text: fixture.partial.text }],
						details: fixture.partial.details,
					});
				}
				if (fixture.delayMs !== undefined) {
					await new Promise((done) => setTimeout(done, fixture.delayMs));
				}
				if (fixture.error !== undefined) {
					throw new Error(fixture.error);
				}
				const images = Array.from({ length: fixture.images ?? 0 }, () => ({
					type: "image" as const,
					mimeType: "image/png",
					data: pixel,
				}));
				return {
					content: [{ type: "text", text: fixture.text ?? "" }, ...images],
					details: fixture.details,
				};
			},
		});
	}
	registerPosthorseMessages(pi);
	pi.registerCommand("pt", {
		description: "card probe: message <name> | theme <name>",
		handler: async (args, ctx) => {
			const [verb, name] = args.trim().split(/\s+/);
			if (verb === "theme") {
				ctx.ui.setTheme(name ?? "dark");
				return;
			}
			const message = messages[name ?? ""];
			if (message !== undefined) {
				pi.sendMessage(
					{ customType: message.customType, content: message.content, display: true, details: message.details },
					{ triggerTurn: false },
				);
			}
		},
	});
}
