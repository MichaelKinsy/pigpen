/**
 * Remember Last Model
 *
 * Remembers the model selected via /model (or Ctrl+P) and persists it as the
 * default in pi's settings.json (defaultProvider + defaultModel), so it is used
 * on the next session. Because pi's default resolver does not understand the
 * OpenRouter `:<provider>` lock suffix, session_start restores the selected
 * model (and its underlying base model) directly as well.
 *
 * State lives in the shared pi-tweaks settings file, section `rememberModel`.
 * Provider locks live in section `openrouterModelProviderPref` and are managed
 * by the `/pt-openrouter-lock-provider` extension.
 *
 * Command:
 *   /pt-remember-model            - show status
 *   /pt-remember-model on|off     - enable/disable remembering
 *   /pt-remember-model clear      - forget the remembered model
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { Model } from "@earendil-works/pi-ai";
import { baseIdOf, getLock, makeVariantId, OPENROUTER_PROVIDER } from "../src/openrouter";
import { persistDefaultModel } from "../src/pi-settings";
import { getSettings, loadSettings, updateSettings } from "../src/store";

/** Resolve the model to restore for a remembered `provider`/`modelId` pair. */
async function resolveModel(
	ctx: ExtensionContext,
	provider: string,
	modelId: string,
): Promise<Model<any> | undefined> {
	const baseId = await baseIdOf(modelId);
	if (provider === OPENROUTER_PROVIDER) {
		const lock = await getLock(baseId);
		if (lock) {
			const variant = ctx.modelRegistry.find(provider, makeVariantId(baseId, lock));
			if (variant) return variant;
		}
	}
	return ctx.modelRegistry.find(provider, baseId);
}

export default async function (pi: ExtensionAPI) {
	await loadSettings();

	// Track model selection changes.
	pi.on("model_select", async (event, ctx) => {
		if (!getSettings().rememberModel.enabled) return;
		const { provider, id } = event.model;
		const baseId = await baseIdOf(id);
		await updateSettings((draft) => {
			draft.rememberModel.last = { provider, modelId: baseId };
		});
		// Keep pi's own settings.json in sync for the next startup.
		const persisted = await persistDefaultModel(provider, baseId);
		if (persisted) {
			// `notify(..., "info")` maps to pi's showStatus(), which coalesces
			// consecutive status lines. The /model selector and Ctrl+P emit their
			// own "Model: ..." status right after setModel() resolves (which awaits
			// this handler), overwriting our message. Defer one tick so ours lands
			// last and stays visible.
			setTimeout(() => {
				ctx.ui.notify(
					`pt-remember-model: remembered model: ${provider}/${persisted} in settings.json`,
					"info",
				);
			}, 0);
		}
	});

	// Restore the last model on new sessions and fresh startups.
	pi.on("session_start", async (event, ctx) => {
		if (!getSettings().rememberModel.enabled) return;
		if (event.reason !== "new" && event.reason !== "startup") return;

		// A continued session already carries its own model; leave it alone.
		if (
			event.reason === "startup" &&
			ctx.sessionManager.getBranch().some((entry) => entry.type === "message")
		) {
			return;
		}

		const last = getSettings().rememberModel.last;
		if (!last) return;

		const model = await resolveModel(ctx, last.provider, last.modelId);
		if (model) await pi.setModel(model);
	});

	pi.registerCommand("pt-remember-model", {
		description: "Remember and restore the last selected model across sessions",
		handler: async (args, ctx) => {
			const sub = (args ?? "").trim().toLowerCase();

			if (sub === "on" || sub === "off") {
				const enabled = sub === "on";
				await updateSettings((draft) => {
					draft.rememberModel.enabled = enabled;
				});
				ctx.ui.notify(`remember-model ${enabled ? "enabled" : "disabled"}`, "info");
				return;
			}

			if (sub === "clear") {
				await updateSettings((draft) => {
					delete draft.rememberModel.last;
				});
				ctx.ui.notify("Cleared remembered model", "info");
				return;
			}

			const settings = getSettings();
			const last = settings.rememberModel.last;
			ctx.ui.notify(
				`remember-model: ${settings.rememberModel.enabled ? "on" : "off"}\n` +
					`last: ${last ? `${last.provider}/${last.modelId}` : "(none)"}\n` +
					"Usage: /pt-remember-model [on|off|clear]",
				"info",
			);
		},
	});
}
