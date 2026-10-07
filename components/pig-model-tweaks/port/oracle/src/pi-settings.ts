/**
 * Persistence of pi's own settings.json `defaultProvider` / `defaultModel`.
 *
 * For OpenRouter models with a provider lock the model id is written with a
 * `:<provider>` suffix (for example `deepseek/deepseek-v4.1-flash:deepseek`).
 * pi's default resolver does not understand that suffix, so remember-model
 * restores the underlying base model on the next session.
 */

import { readFile, writeFile } from "node:fs/promises";
import { agentPath } from "./agent-dir";
import { getLock, makeVariantId, OPENROUTER_PROVIDER } from "./openrouter";

export async function persistDefaultModel(
	provider: string,
	baseId: string,
): Promise<string | undefined> {
	try {
		const path = agentPath("settings.json");
		const raw = await readFile(path, "utf8");
		const settings = JSON.parse(raw) as Record<string, unknown>;
		const lock = provider === OPENROUTER_PROVIDER ? await getLock(baseId) : undefined;
		const modelId = lock ? makeVariantId(baseId, lock) : baseId;
		settings.defaultProvider = provider;
		settings.defaultModel = modelId;
		await writeFile(path, `${JSON.stringify(settings, null, 2)}\n`, "utf8");
		return modelId;
	} catch {
		// Missing or invalid settings.json: nothing to persist.
		return undefined;
	}
}
