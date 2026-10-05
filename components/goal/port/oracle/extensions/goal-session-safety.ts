import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { GOAL_AUDIT_ENTRY, goalEventMessageId } from "./goal-format.ts";

/** pi-subagents marks fresh, forked, resumed and nested worker processes. */
export function isDelegatedGoalSession(env: Record<string, string | undefined> = process.env): boolean {
	const depth = Number(env.PI_SUBAGENT_DEPTH);
	return env.PI_SUBAGENT_CHILD === "1" || (Number.isFinite(depth) && depth > 0);
}

/** Display-only audits must never become user messages between a tool and its result. */
export function filterGoalSessionContext<T>(messages: readonly T[], child = false): T[] | null {
	let filtered: T[] | null = null;
	for (let i = 0; i < messages.length; i++) {
		const message = messages[i]!;
		const entry = message as { role?: string; customType?: string; details?: unknown; content?: unknown } | null;
		const remove = entry && typeof entry === "object" && entry.role === "custom"
			&& (entry.customType === GOAL_AUDIT_ENTRY || (child && goalEventMessageId(entry) !== null));
		if (remove) filtered ??= messages.slice(0, i);
		else filtered?.push(message);
	}
	return filtered;
}

type TranscriptMessage = Parameters<ExtensionAPI["sendMessage"]>[0];
type SessionKey = string | ExtensionContext["sessionManager"];
const sessionKey = (ctx: ExtensionContext): SessionKey => ctx.sessionManager.getSessionId?.() ?? ctx.sessionManager;

/** Transcript output waits for the entire agent run, including tool results, to settle. */
export class GoalAuditMessages {
	private pending: { session: SessionKey; message: TranscriptMessage }[] = [];

	enqueue(ctx: ExtensionContext, message: TranscriptMessage): void {
		this.pending.push({ session: sessionKey(ctx), message });
	}

	clear(): void {
		this.pending = [];
	}

	flush(ctx: ExtensionContext, pi: ExtensionAPI): void {
		const session = sessionKey(ctx);
		this.pending = this.pending.filter(item => item.session === session);
		if (!ctx.isIdle()) return;
		const pending = this.pending;
		this.pending = [];
		for (const item of pending) pi.sendMessage(item.message, { triggerTurn: false });
	}
}
