// SPDX-License-Identifier: GPL-3.0-or-later
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
// pi's extension loader aliases the pi-ai root to the /compat entrypoint and
// only whitelists exact subpaths (/compat, /oauth, /providers/all), so deeper
// subpath imports like .../api/openai-completions fail to resolve at load time.
import { openAICompletionsApi, openAIResponsesApi } from "@earendil-works/pi-ai/compat";
import { discoverModels, type OpenCodeModelInfo } from "./discovery.js";

const nativeOpenAICompletionsStream = openAICompletionsApi().streamSimple;
const nativeOpenAIResponsesStream = openAIResponsesApi().streamSimple;

const PROVIDER_ID = "opencode-free";
const BASE_URL = "https://opencode.ai/zen/v1";

// Describes Zen's upstream quirks to Pi's native openai-completions engine
// (max_tokens field name, reasoning_content replay) — no custom streaming code.
const OPENCODE_COMPAT = {
  supportsStore: false,
  supportsDeveloperRole: false,
  // Zen often ends streams without finish_reason; let pi infer stop/toolUse
  // instead of throwing "Stream ended without finish_reason".
  supportsFinishReason: false,
  maxTokensField: "max_tokens" as const,
  requiresReasoningContentOnAssistantMessages: true,
};

/** Maps a discovered model to the provider config shape. */
function toProviderModel(m: OpenCodeModelInfo) {
  return {
    id: m.id.replace(/^opencode\//, ""),
    name: m.name,
    // Per-model API override: models routed via @ai-sdk/openai speak the
    // Responses API (e.g. muse-spark), everything else uses chat completions.
    api: m.api ?? "openai-completions",
    reasoning: m.reasoning ?? false,
    thinkingLevelMap: m.thinkingLevelMap,
    input: (m.input?.includes("image") ? ["text", "image"] : ["text"]) as ("text" | "image")[],
    contextWindow: m.contextWindow ?? 128_000,
    maxTokens: m.maxTokens ?? 16_384,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    compat: OPENCODE_COMPAT,
  };
}

function toStoredModel(config: ReturnType<typeof toProviderModel>) {
  return { ...config, provider: PROVIDER_ID, baseUrl: BASE_URL };
}

function fromStoredModel<M extends { provider?: unknown; baseUrl?: unknown }>(m: M) {
  const { provider: _provider, baseUrl: _baseUrl, ...config } = m;
  return config as unknown as ReturnType<typeof toProviderModel>;
}

export default function opencodeDirectExtension(pi: ExtensionAPI): void {
  // Registered once with an empty list; models come exclusively through
  // refreshModels (snapshot restore on session init, live discovery on
  // explicit refresh) — pi's native model lifecycle.
  pi.registerProvider(PROVIDER_ID, {
    name: "OpenCode Direct (Free)",
    baseUrl: BASE_URL,
    // Placeholder: Pi's auth composition requires a key; real requests go
    // keyless via the streamSimple shim below.
    apiKey: "none",
    api: "openai-completions",
    headers: {
      "x-opencode-client": "cli",
      "x-opencode-project": "global",
      "User-Agent": "opencode/0.0.0-dev",
      // Zen's free tier rejects every Bearer token with 401. Provider-level
      // headers reach the OpenAI SDK's defaultHeaders even on pi's native
      // request path (ModelsRuntime.prepareRequest → composeModelProvider),
      // and `Authorization: null` is the SDK's supported way to omit the
      // header entirely. This covers Responses-API models (e.g. muse-spark),
      // which bypass the streamSimple shim below because pi only routes
      // through it when model.api === provider api ("openai-completions").
      Authorization: null as unknown as string,
    },
    models: [],
    async refreshModels(ctx) {
      if (!ctx.allowNetwork) {
        return ctx.stored?.models
          .filter(m => m.provider === PROVIDER_ID && (m.api === "openai-completions" || m.api === "openai-responses"))
          .map(fromStoredModel) ?? [];
      }
      if (ctx.signal.aborted) return [];
      const discovered = await discoverModels({ signal: ctx.signal });
      if (discovered.length === 0) return []; // keep previous snapshot; retry on next refresh
      const configs = discovered.map(toProviderModel);
      await ctx.publish({ persist: { models: configs.map(toStoredModel), checkedAt: Date.now() } });
      return configs;
    },
    // Keyless shim over pi's native engines, dispatched by model API:
    // Zen's free tier rejects every Bearer token with 401, and
    // `Authorization: null` is the OpenAI SDK's supported omission.
    // Streaming, tools, reasoning, and cost handling stay fully native.
    streamSimple: (model, context, options) => {
      const api = (model as { api?: string }).api;
      const native =
        api === "openai-responses"
          ? (nativeOpenAIResponsesStream as typeof nativeOpenAICompletionsStream)
          : nativeOpenAICompletionsStream;
      return native(model as Parameters<typeof nativeOpenAICompletionsStream>[0], context, {
        ...options,
        headers: { ...options?.headers, Authorization: null },
      });
    },
  });
}
