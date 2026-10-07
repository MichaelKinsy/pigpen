/**
 * Model Preference Guard
 *
 * Guards against unintended model usage by letting you select preferred
 * LLM + provider combinations. When starting a conversation, checks whether
 * the current model is in your saved preferences and prompts for confirmation
 * if not.
 *
 * State lives in the shared pi-tweaks settings file, section `modelGuard`.
 *
 * Command:
 *   /pt-model-guard-pref          - Open picker to manage preferences
 *   /pt-model-guard-pref list     - Show current allowed models
 *   /pt-model-guard-pref on|off   - Enable/disable guard
 *   /pt-model-guard-pref toggle   - Toggle guard without deleting preferences
 *   /pt-model-guard-pref add      - Open model picker to add more
 *   /pt-model-guard-pref remove   - Open model picker to remove
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Key, matchesKey, wrapTextWithAnsi } from "@earendil-works/pi-tui";
import { getSettings, loadSettings, updateSettings, type ModelGuardState } from "../src/store";

// ─── Helpers ────────────────────────────────────────────────────────────

function formatModel(pref: { provider: string; model: string }): string {
	return `${pref.provider}/${pref.model}`;
}

function isModelAllowed(
	current: { provider: string; id: string },
	config: ModelGuardState,
): boolean {
	if (!config.enabled) return true;
	if (config.allowedModels.length === 0) return true; // No preferences = no guard
	return config.allowedModels.some(
		(p) => p.provider === current.provider && p.model === current.id,
	);
}

function parseModelValue(value: string): { provider: string; model: string } {
	const [provider, ...modelParts] = value.split("/");
	return { provider, model: modelParts.join("/") };
}

// ─── Multi-Select Picker with Search ───────────────────────────────────

interface PickerItem {
	value: string; // "provider/model"
	label: string; // "provider/model"
	checked: boolean; // currently selected
	available: boolean; // has API key configured
}

async function showModelPicker(
	ctx: ExtensionContext,
	title: string,
	allModels: Array<{ provider: string; id: string; available: boolean }>,
	initialChecked: Set<string>,
): Promise<Set<string> | null> {
	// Build items: pre-checked models first, then unchecked
	const items: PickerItem[] = allModels.map((m) => ({
		value: `${m.provider}/${m.id}`,
		label: `${m.provider}/${m.id}`,
		checked: initialChecked.has(`${m.provider}/${m.id}`),
		available: m.available,
	}));

	// Sort: checked first, then available, then by value
	items.sort((a, b) => {
		if (a.checked !== b.checked) return a.checked ? -1 : 1;
		if (a.available !== b.available) return a.available ? -1 : 1;
		return a.value.localeCompare(b.value);
	});

	const result = await ctx.ui.custom<Set<string> | null>((tui, theme, _kb, done) => {
		let cursorIndex = 0;
		let scrollOffset = 0;
		let searchTerm = "";
		const maxVisible = Math.min(items.length, 20);
		let cachedLines: string[] | undefined;

		// Filter items by search term
		function getFilteredIndices(): number[] {
			if (!searchTerm) return items.map((_, i) => i);
			const term = searchTerm.toLowerCase();
			return items
				.map((item, i) => ({ item, i }))
				.filter(({ item }) => item.value.toLowerCase().includes(term))
				.map(({ i }) => i);
		}

		function refresh() {
			cachedLines = undefined;
			tui.requestRender();
		}

		function toggle(index: number) {
			const filtered = getFilteredIndices();
			const itemIdx = filtered[index];
			if (itemIdx !== undefined) items[itemIdx].checked = !items[itemIdx].checked;
			refresh();
		}

		function submit() {
			const selected = new Set<string>();
			for (const item of items) {
				if (item.checked) selected.add(item.value);
			}
			done(selected);
		}

		function handleInput(data: string) {
			if (matchesKey(data, Key.up)) {
				const filtered = getFilteredIndices();
				const maxIdx = filtered.length - 1;
				if (maxIdx < 0) return;
				cursorIndex = Math.max(0, Math.min(cursorIndex, maxIdx) - 1);
				if (cursorIndex < scrollOffset) scrollOffset = cursorIndex;
				refresh();
				return;
			}
			if (matchesKey(data, Key.down)) {
				const filtered = getFilteredIndices();
				const maxIdx = filtered.length - 1;
				if (maxIdx < 0) return;
				cursorIndex = Math.min(maxIdx, Math.max(0, cursorIndex) + 1);
				if (cursorIndex >= scrollOffset + maxVisible)
					scrollOffset = cursorIndex - maxVisible + 1;
				refresh();
				return;
			}
			if (matchesKey(data, Key.enter)) {
				submit();
				return;
			}
			if (matchesKey(data, Key.escape)) {
				if (searchTerm) {
					searchTerm = "";
					cursorIndex = 0;
					scrollOffset = 0;
					refresh();
				} else {
					done(null);
				}
				return;
			}
			// Backspace to delete search char
			if (matchesKey(data, Key.backspace) || data === "\x7f") {
				if (searchTerm.length > 0) {
					searchTerm = searchTerm.slice(0, -1);
					cursorIndex = 0;
					scrollOffset = 0;
					refresh();
				}
				return;
			}
			// Space toggles
			if (data === " ") {
				toggle(cursorIndex);
				const filtered = getFilteredIndices();
				if (cursorIndex < filtered.length - 1) {
					cursorIndex++;
					if (cursorIndex >= scrollOffset + maxVisible)
						scrollOffset = cursorIndex - maxVisible + 1;
				}
				refresh();
				return;
			}
			// 'a' = select all (filtered), 'n' = select none
			if (data === "a" && !searchTerm) {
				for (const idx of getFilteredIndices()) items[idx].checked = true;
				refresh();
				return;
			}
			if (data === "n" && !searchTerm) {
				for (const idx of getFilteredIndices()) items[idx].checked = false;
				refresh();
				return;
			}
			// Printable characters → type into search
			if (data.length === 1 && data >= " " && data < "\x7f" && data !== " ") {
				searchTerm += data;
				cursorIndex = 0;
				scrollOffset = 0;
				refresh();
				return;
			}
		}

		function render(width: number): string[] {
			if (cachedLines) return cachedLines;

			const lines: string[] = [];
			const rw = Math.max(1, width);
			const filtered = getFilteredIndices();
			const filteredItems = filtered.map((i) => items[i]);

			lines.push(theme.fg("accent", "─".repeat(rw)));
			lines.push(theme.fg("text", theme.bold(` ${title}`)));

			// Search bar
			lines.push(theme.fg("dim", ` 🔍 ${searchTerm || ""}_`));
			lines.push(
				theme.fg(
					"dim",
					` ${items.filter((i) => i.checked).length}/${items.length} selected, ${filtered.length} shown`,
				),
			);
			lines.push("");

			for (let i = 0; i < Math.min(filteredItems.length, maxVisible); i++) {
				const globalIdx = scrollOffset + i;
				const item = filteredItems[globalIdx];
				if (!item) break;
				const isSelected = globalIdx === cursorIndex;
				const checkbox = item.checked ? "☑" : "☐";
				const avail = item.available ? "" : theme.fg("warning", " ⚠ no key");

				const label = `${checkbox} ${item.label}${avail}`;

				// Wrap long lines
				const prefixWidth = 2;
				const contentWidth = Math.max(1, rw - prefixWidth);
				const wrapped = wrapTextWithAnsi(label, contentWidth);
				for (let j = 0; j < wrapped.length; j++) {
					const p = j === 0 ? (isSelected ? theme.fg("accent", "> ") : "  ") : "  ";
					const c = isSelected ? "accent" : item.checked ? "success" : "text";
					lines.push(`${p}${theme.fg(c, wrapped[j])}`);
				}
			}

			// Scroll indicator
			if (filteredItems.length > maxVisible) {
				const scrollInfo = ` ${scrollOffset + 1}-${Math.min(scrollOffset + maxVisible, filteredItems.length)} of ${filteredItems.length}`;
				lines.push(theme.fg("dim", scrollInfo));
			}

			lines.push("");
			lines.push(
				theme.fg(
					"dim",
					"  Type to search • Space toggle • a=all • n=none • Enter confirm • Esc cancel",
				),
			);
			lines.push(theme.fg("accent", "─".repeat(rw)));

			cachedLines = lines;
			return lines;
		}

		return {
			render,
			invalidate: () => {
				cachedLines = undefined;
			},
			handleInput,
		};
	});

	return result;
}

// ─── Extension ──────────────────────────────────────────────────────────

export default async function (pi: ExtensionAPI) {
	await loadSettings();

	const availableModels = (ctx: ExtensionContext) =>
		ctx.modelRegistry.getAvailable().map((m) => ({
			provider: m.provider,
			id: m.id,
			available: true,
		}));

	async function saveGuard(mutate: (draft: ModelGuardState) => void): Promise<ModelGuardState> {
		const settings = await updateSettings((draft) => mutate(draft.modelGuard));
		return settings.modelGuard;
	}

	// ── /pt-model-guard-pref command ─────────────────────────────────

	pi.registerCommand("pt-model-guard-pref", {
		description: "Manage model preferences (guard against unintended model usage)",
		handler: async (args, ctx) => {
			const sub = args?.trim().toLowerCase();

			if (sub === "list") {
				const { modelGuard } = getSettings();
				if (modelGuard.allowedModels.length === 0) {
					ctx.ui.notify("No model preferences saved. All models allowed.", "info");
				} else {
					const list = modelGuard.allowedModels.map((m) => `  • ${formatModel(m)}`).join("\n");
					ctx.ui.notify(
						`Guard: ${modelGuard.enabled ? "ON" : "OFF"}\nAllowed models:\n${list}`,
						"info",
					);
				}
				return;
			}

			if (sub === "on" || sub === "off") {
				const enabled = sub === "on";
				await saveGuard((draft) => {
					draft.enabled = enabled;
				});
				ctx.ui.notify(`Model guard ${enabled ? "enabled" : "disabled"}`, "info");
				return;
			}

			if (sub === "toggle") {
				let enabled = false;
				await saveGuard((draft) => {
					draft.enabled = !draft.enabled;
					enabled = draft.enabled;
				});
				ctx.ui.notify(`Model guard ${enabled ? "enabled" : "disabled"}`, "info");
				return;
			}

			if (sub === "add" || sub === "remove") {
				const models = availableModels(ctx);
				if (models.length === 0) {
					ctx.ui.notify("No models available. Check your API keys.", "warning");
					return;
				}

				const currentSet = new Set(getSettings().modelGuard.allowedModels.map(formatModel));
				const title =
					sub === "add"
						? "Select models to ADD to preferences"
						: "Select models to REMOVE from preferences";

				const result = await showModelPicker(ctx, title, models, currentSet);
				if (!result) {
					ctx.ui.notify("Cancelled", "info");
					return;
				}

				if (sub === "add") {
					const newModels = [...result]
						.filter((val) => !currentSet.has(val))
						.map(parseModelValue);
					if (newModels.length > 0) {
						await saveGuard((draft) => {
							draft.allowedModels.push(...newModels);
						});
						ctx.ui.notify(
							`Added ${newModels.length} model(s): ${newModels.map(formatModel).join(", ")}`,
							"info",
						);
					} else {
						ctx.ui.notify("No new models added", "info");
					}
				} else {
					const before = getSettings().modelGuard.allowedModels.length;
					const next = await saveGuard((draft) => {
						draft.allowedModels = draft.allowedModels.filter((m) => result.has(formatModel(m)));
					});
					const removed = before - next.allowedModels.length;
					ctx.ui.notify(
						removed > 0 ? `Removed ${removed} model(s)` : "No models removed",
						"info",
					);
				}
				return;
			}

			// No subcommand: manage the full set with the picker.
			const models = availableModels(ctx);
			if (models.length === 0) {
				ctx.ui.notify("No models available. Check your API keys.", "warning");
				return;
			}

			const currentSet = new Set(getSettings().modelGuard.allowedModels.map(formatModel));
			const isFirstTime = currentSet.size === 0;
			if (isFirstTime) {
				ctx.ui.notify("No preferences set yet. Pick your preferred models:", "info");
			}

			const result = await showModelPicker(
				ctx,
				isFirstTime
					? "Select your PREFERRED models"
					: "Toggle model preferences (checked = allowed)",
				models,
				currentSet,
			);
			if (!result) {
				ctx.ui.notify("Cancelled", "info");
				return;
			}

			if (isFirstTime && result.size === 0) {
				ctx.ui.notify("No models selected. All models remain allowed.", "info");
				return;
			}

			const allowedModels = [...result].map(parseModelValue);
			await saveGuard((draft) => {
				draft.allowedModels = allowedModels;
			});
			ctx.ui.notify(
				`Updated preferences: ${allowedModels.length} model(s) allowed`,
				"info",
			);
		},
	});

	// ── Session start: load config, show hint if first time ──────────

	pi.on("session_start", async (_event, ctx) => {
		const { modelGuard } = getSettings();
		if (modelGuard.enabled && modelGuard.allowedModels.length === 0) {
			ctx.ui.notify(
				"💡 No model preferences set. Run /pt-model-guard-pref to choose preferred models.",
				"info",
			);
		}
	});

	// ── Input: check model against preferences before agent runs ───

	pi.on("input", async (event, ctx) => {
		// Only check interactive typed input.
		if (event.source !== "interactive") return;
		// Skip in non-TUI mode.
		if (ctx.mode !== "tui") return;

		const { modelGuard } = getSettings();
		if (!modelGuard.enabled) return;
		if (modelGuard.allowedModels.length === 0) return;

		const model = ctx.model;
		if (!model) return;
		if (isModelAllowed({ provider: model.provider, id: model.id }, modelGuard)) return;

		const modelName = `${model.provider}/${model.id}`;
		const allowed = modelGuard.allowedModels.map(formatModel).join(", ");

		const ok = await ctx.ui.confirm(
			"⚠️  Model Not Preferred",
			`Current model: ${modelName}\n\nThis model is NOT in your preferred list:\n${allowed}\n\nContinue with ${modelName}?`,
		);

		if (!ok) {
			// User declined — block the agent entirely.
			return { action: "handled" };
		}
		// User confirmed — continue with the un-preferred model.
		return { action: "continue" };
	});

	// ── Model select: show status on change ──────────────────────────

	pi.on("model_select", async (event, ctx) => {
		if (event.source === "restore") return;
		const model = event.model;
		if (!model) return;

		const { modelGuard } = getSettings();
		const modelName = `${model.provider}/${model.id}`;

		if (modelGuard.enabled && modelGuard.allowedModels.length > 0) {
			if (isModelAllowed({ provider: model.provider, id: model.id }, modelGuard)) {
				ctx.ui.notify(`✅ ${modelName} — in preferred models`, "info");
			} else {
				ctx.ui.notify(`⚠️  ${modelName} — NOT in preferred models`, "warning");
			}
		}
	});
}
