/**
 * Single consolidated settings store for @liyu1981/pi-tweaks.
 *
 * Everything the package persists lives in one file:
 *
 *   ~/.pi/agent/pi-tweaks-settings.json
 *
 * Shape:
 *
 *   {
 *     "version": 1,
 *     "rememberModel": { "enabled": true, "last": { "provider": "...", "modelId": "..." } },
 *     "modelGuard":    { "enabled": true, "allowedModels": [{ "provider": "...", "model": "..." }] },
 *     "openrouterModelProviderPref": { "enabled": true, "locks": { "<baseModelId>": "<providerSlug>" } }
 *   }
 *
 * Reads go through {@link loadSettings} (cached), writes through
 * {@link updateSettings}, which serializes read-modify-write cycles across
 * extensions and persists atomically (temp file + rename).
 */

import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { agentPath } from "./agent-dir";

export const SETTINGS_FILENAME = "pi-tweaks-settings.json";
export const SETTINGS_VERSION = 1 as const;

export interface RememberModelState {
	enabled: boolean;
	last?: { provider: string; modelId: string };
}

export interface AllowedModel {
	provider: string;
	model: string;
}

export interface ModelGuardState {
	enabled: boolean;
	allowedModels: AllowedModel[];
}

export interface OpenRouterProviderPrefState {
	enabled: boolean;
	/** base model id -> preferred upstream provider slug */
	locks: Record<string, string>;
}

export interface PiTweaksSettings {
	version: typeof SETTINGS_VERSION;
	rememberModel: RememberModelState;
	modelGuard: ModelGuardState;
	openrouterModelProviderPref: OpenRouterProviderPrefState;
}

export function settingsPath(): string {
	return agentPath(SETTINGS_FILENAME);
}

function defaults(): PiTweaksSettings {
	return {
		version: SETTINGS_VERSION,
		rememberModel: { enabled: true },
		modelGuard: { enabled: true, allowedModels: [] },
		openrouterModelProviderPref: { enabled: true, locks: {} },
	};
}

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function normalizeAllowedModels(value: unknown): AllowedModel[] {
	if (!Array.isArray(value)) return [];
	const out: AllowedModel[] = [];
	for (const entry of value) {
		if (!isObject(entry)) continue;
		const provider = asString(entry.provider);
		const model = asString(entry.model);
		if (provider && model) out.push({ provider, model });
	}
	return out;
}

function normalizeLocks(value: unknown): Record<string, string> {
	const out: Record<string, string> = {};
	if (!isObject(value)) return out;
	for (const [key, raw] of Object.entries(value)) {
		const provider = asString(raw);
		if (key && provider) out[key] = provider;
	}
	return out;
}

/** Coerce arbitrary parsed JSON into a valid settings object, filling defaults. */
function normalize(raw: unknown): PiTweaksSettings {
	const out = defaults();
	if (!isObject(raw)) return out;

	if (isObject(raw.rememberModel)) {
		const rm = raw.rememberModel;
		const last = isObject(rm.last) ? rm.last : undefined;
		const provider = asString(last?.provider);
		const modelId = asString(last?.modelId);
		out.rememberModel = {
			enabled: rm.enabled !== false,
			...(provider && modelId ? { last: { provider, modelId } } : {}),
		};
	}

	if (isObject(raw.modelGuard)) {
		const mg = raw.modelGuard;
		out.modelGuard = {
			enabled: mg.enabled !== false,
			allowedModels: normalizeAllowedModels(mg.allowedModels),
		};
	}

	if (isObject(raw.openrouterModelProviderPref)) {
		const or = raw.openrouterModelProviderPref;
		out.openrouterModelProviderPref = {
			enabled: or.enabled !== false,
			locks: normalizeLocks(or.locks),
		};
	}

	return out;
}

async function readJson(path: string): Promise<unknown | undefined> {
	try {
		return JSON.parse(await readFile(path, "utf8"));
	} catch {
		return undefined;
	}
}

/**
 * Seed the consolidated file from the legacy per-feature files that earlier
 * versions of these extensions used. Returns undefined when none exist.
 *
 *   last-model.json                 -> rememberModel.last
 *   openrouter-provider-prefs.json  -> openrouterModelProviderPref.locks
 *   model-preferences.json          -> modelGuard
 */
async function migrateLegacy(): Promise<PiTweaksSettings | undefined> {
	const [lastRaw, locksRaw, guardRaw] = await Promise.all([
		readJson(agentPath("last-model.json")),
		readJson(agentPath("openrouter-provider-prefs.json")),
		readJson(agentPath("model-preferences.json")),
	]);
	if (lastRaw === undefined && locksRaw === undefined && guardRaw === undefined) {
		return undefined;
	}

	const out = defaults();

	if (isObject(lastRaw)) {
		const provider = asString(lastRaw.provider);
		const modelId = asString(lastRaw.modelId);
		if (provider && modelId) out.rememberModel.last = { provider, modelId };
	}

	if (isObject(locksRaw)) {
		out.openrouterModelProviderPref = {
			enabled: true,
			locks: normalizeLocks(locksRaw),
		};
	}

	if (isObject(guardRaw)) {
		out.modelGuard = {
			enabled: guardRaw.enabled !== false,
			allowedModels: normalizeAllowedModels(guardRaw.allowedModels),
		};
	}

	return out;
}

async function writeAtomic(data: PiTweaksSettings): Promise<void> {
	const path = settingsPath();
	await mkdir(dirname(path), { recursive: true });
	const tmp = `${path}.${process.pid}.${Date.now()}.tmp`;
	await writeFile(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
	await rename(tmp, path);
}

let cache: PiTweaksSettings | undefined;
let queue: Promise<unknown> = Promise.resolve();

/** Load settings once, migrating legacy files if the consolidated file is absent. */
export async function loadSettings(): Promise<PiTweaksSettings> {
	if (cache) return cache;
	const raw = await readJson(settingsPath());
	if (raw === undefined) {
		const migrated = await migrateLegacy();
		if (migrated) {
			await writeAtomic(migrated);
			cache = migrated;
			return cache;
		}
	}
	cache = normalize(raw);
	return cache;
}

/** Cached snapshot. Call {@link loadSettings} first; falls back to defaults. */
export function getSettings(): PiTweaksSettings {
	return cache ?? defaults();
}

/**
 * Serialized read-modify-write against the shared settings file.
 * Mutate the provided draft and it is persisted atomically.
 */
export async function updateSettings(
	mutate: (draft: PiTweaksSettings) => void | Promise<void>,
): Promise<PiTweaksSettings> {
	const run = queue.then(async () => {
		const current = await loadSettings();
		const draft = structuredClone(current);
		await mutate(draft);
		await writeAtomic(draft);
		cache = draft;
		return draft;
	});
	queue = run.then(
		() => undefined,
		() => undefined,
	);
	return run;
}
