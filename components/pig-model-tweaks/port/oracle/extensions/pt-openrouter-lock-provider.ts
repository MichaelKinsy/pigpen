/**
 * OpenRouter Provider Lock
 *
 * Manages per-OpenRouter-model upstream provider preferences. Each OpenRouter
 * model can carry a preferred provider slug. The lock is persisted in the
 * shared pi-tweaks settings file (section `openrouterModelProviderPref.locks`):
 *
 *   { "deepseek/deepseek-v4.1-flash": "deepseek" }
 *
 * At request time the lock is turned into OpenRouter's `provider.order` and the
 * `:<provider>` suffix that remember-model writes into pi's `defaultModel` is
 * stripped again, because OpenRouter itself does not understand `:deepseek`.
 *
 * Command:
 *   /pt-openrouter-lock-provider            - TUI picker to manage locks
 *   /pt-openrouter-lock-provider <provider> - lock provider for the current model
 *   /pt-openrouter-lock-provider clear      - clear lock for the current model
 *   /pt-openrouter-lock-provider list       - list all locks
 *   /pt-openrouter-lock-provider on|off     - enable/disable the whole feature
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { DynamicBorder } from "@earendil-works/pi-coding-agent";
import {
	Container,
	Key,
	matchesKey,
	type SelectItem,
	SelectList,
	Text,
} from "@earendil-works/pi-tui";
import {
	baseIdOf,
	clearLock,
	getLock,
	isOpenRouterLockEnabled,
	listLocks,
	makeVariantId,
	OPENROUTER_PROVIDER,
	setEnabled,
	setLock,
} from "../src/openrouter";
import { persistDefaultModel } from "../src/pi-settings";
import { loadSettings } from "../src/store";

/** TUI searchable picker over available OpenRouter models plus known locks. */
async function showLockPicker(ctx: ExtensionContext): Promise<void> {
	const available = ctx.modelRegistry
		.getAvailable()
		.filter((model) => model.provider === OPENROUTER_PROVIDER);

	const locks = await listLocks();
	const ids = new Set(available.map((model) => model.id));
	for (const id of Object.keys(locks)) ids.add(id);
	const sorted = [...ids].sort();

	if (sorted.length === 0) {
		ctx.ui.notify("No OpenRouter models available", "warning");
		return;
	}

	const items: SelectItem[] = sorted.map((id) => ({
		value: id,
		label: id,
		description: locks[id] ? `provider: ${locks[id]}` : "no provider lock",
	}));

	const selected = await ctx.ui.custom<string | null>((tui, theme, _kb, done) => {
		let searchTerm = "";

		const container = new Container();
		container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));
		container.addChild(
			new Text(theme.fg("accent", theme.bold("OpenRouter Provider Lock")), 1, 0),
		);

		// Dynamic search line (re-rendered on every frame).
		const searchLine = {
			render: () => [
				searchTerm
					? theme.fg("muted", `  search: ${searchTerm}`)
					: theme.fg("dim", "  type to search"),
			],
			invalidate: () => {},
		};
		container.addChild(searchLine);

		const selectList = new SelectList(items, Math.min(items.length, 15), {
			selectedPrefix: (text) => theme.fg("accent", text),
			selectedText: (text) => theme.fg("accent", text),
			description: (text) => theme.fg("muted", text),
			scrollInfo: (text) => theme.fg("dim", text),
			noMatch: (text) => theme.fg("warning", text),
		});
		selectList.onSelect = (item) => done(item.value);
		selectList.onCancel = () => done(null);
		container.addChild(selectList);
		container.addChild(
			new Text(theme.fg("dim", "↑↓ navigate • enter select • esc cancel"), 1, 0),
		);
		container.addChild(new DynamicBorder((str: string) => theme.fg("accent", str)));

		return {
			render: (width: number) => container.render(width),
			invalidate: () => container.invalidate(),
			handleInput: (data: string) => {
				if (matchesKey(data, Key.backspace) || data === "\x7f") {
					if (searchTerm) {
						searchTerm = searchTerm.slice(0, -1);
						selectList.setFilter(searchTerm);
						tui.requestRender();
					}
					return;
				}
				if (matchesKey(data, Key.escape) && searchTerm) {
					searchTerm = "";
					selectList.setFilter("");
					tui.requestRender();
					return;
				}
				if (data.length === 1 && data >= " " && data < "\x7f") {
					searchTerm += data;
					selectList.setFilter(searchTerm);
					tui.requestRender();
					return;
				}
				selectList.handleInput(data);
				tui.requestRender();
			},
		};
	});

	if (!selected) return;

	const current = (await getLock(selected)) ?? "";
	const input = await ctx.ui.input(
		`Provider for ${selected} (empty to clear):`,
		current || "deepseek",
	);
	if (input === undefined) return;

	const value = input.trim();
	await applyLock(selected, value || undefined);
	ctx.ui.notify(
		value
			? `OpenRouter provider lock for ${selected}: ${value}`
			: `Cleared provider lock for ${selected}`,
		"info",
	);
}

/** Persist a lock change and keep pi's default model suffix in sync. */
async function applyLock(baseId: string, provider: string | undefined): Promise<void> {
	if (provider) await setLock(baseId, provider);
	else await clearLock(baseId);
	await persistDefaultModel(OPENROUTER_PROVIDER, baseId);
}

export default async function (pi: ExtensionAPI) {
	await loadSettings();

	// Apply / strip OpenRouter provider locks at request time.
	pi.on("before_provider_request", async (event, ctx) => {
		const model = ctx.model;
		if (!model || model.provider !== OPENROUTER_PROVIDER) return;
		if (!(await isOpenRouterLockEnabled())) return;

		const payload = event.payload;
		if (!payload || typeof payload !== "object" || Array.isArray(payload)) return;
		const body = payload as Record<string, unknown>;
		let changed = false;

		// Strip our `<model>:<provider>` suffix before it reaches OpenRouter.
		if (typeof body.model === "string") {
			const stripped = await baseIdOf(body.model);
			if (stripped !== body.model) {
				body.model = stripped;
				changed = true;
			}
		}

		// Express the lock as OpenRouter's `provider.order`.
		const lock = await getLock(await baseIdOf(model.id));
		if (lock) {
			const existingProvider =
				body.provider && typeof body.provider === "object" && !Array.isArray(body.provider)
					? { ...(body.provider as Record<string, unknown>) }
					: {};
			const order = Array.isArray(existingProvider.order)
				? (existingProvider.order as unknown[]).filter(
						(entry): entry is string => typeof entry === "string",
					)
				: [];
			if (order[0] !== lock) {
				existingProvider.order = [lock, ...order.filter((entry) => entry !== lock)];
				body.provider = existingProvider;
				changed = true;
			}
		}

		if (changed) return body;
	});

	pi.registerCommand("pt-openrouter-lock-provider", {
		description: "Manage OpenRouter provider locks (adds a :<provider> suffix)",
		handler: async (args, ctx) => {
			const current = ctx.model;
			const trimmed = (args ?? "").trim();
			const sub = trimmed.toLowerCase();

			// /pt-openrouter-lock-provider on|off
			if (sub === "on" || sub === "off") {
				const enabled = sub === "on";
				await setEnabled(enabled);
				ctx.ui.notify(
					`openrouter-lock-provider ${enabled ? "enabled" : "disabled"}`,
					"info",
				);
				return;
			}

			// /pt-openrouter-lock-provider list
			if (sub === "list") {
				const enabled = await isOpenRouterLockEnabled();
				const locks = await listLocks();
				const entries = Object.entries(locks);
				ctx.ui.notify(
					`OpenRouter provider locks (${enabled ? "ON" : "OFF"}):\n${
						entries.length
							? entries.map(([id, provider]) => `  • ${id} -> ${provider}`).join("\n")
							: "  (none)"
					}`,
					"info",
				);
				return;
			}

			// /pt-openrouter-lock-provider clear
			if (sub === "clear") {
				if (!current || current.provider !== OPENROUTER_PROVIDER) {
					ctx.ui.notify("Current model is not an OpenRouter model", "warning");
					return;
				}
				const baseId = await baseIdOf(current.id);
				if (!(await getLock(baseId))) {
					ctx.ui.notify(`No provider lock set for ${baseId}`, "info");
					return;
				}
				await applyLock(baseId, undefined);
				ctx.ui.notify(`Cleared provider lock for ${baseId}`, "info");
				return;
			}

			// /pt-openrouter-lock-provider <provider>
			if (trimmed) {
				if (!current || current.provider !== OPENROUTER_PROVIDER) {
					ctx.ui.notify("Current model is not an OpenRouter model", "warning");
					return;
				}
				const baseId = await baseIdOf(current.id);
				await applyLock(baseId, trimmed);
				ctx.ui.notify(
					`OpenRouter provider lock for ${baseId}: ${trimmed}\n` +
						`defaultModel -> ${makeVariantId(baseId, trimmed)}`,
					"info",
				);
				return;
			}

			// No args: TUI picker
			if (ctx.mode !== "tui") {
				ctx.ui.notify(
					"Usage: /pt-openrouter-lock-provider <provider> | clear | list | on | off",
					"info",
				);
				return;
			}
			await showLockPicker(ctx);
		},
	});
}
