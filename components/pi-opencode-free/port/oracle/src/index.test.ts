// SPDX-License-Identifier: GPL-3.0-or-later
import assert from "node:assert/strict";
import test from "node:test";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { readFileSync } from "node:fs";
import opencodeDirectExtension from "./index.js";
import { discoverModels } from "./discovery.js";

test("registers provider once with empty initial models and no boot fetch", async () => {
  const originalFetch = globalThis.fetch;
  let fetchCount = 0;
  globalThis.fetch = (async () => { fetchCount++; throw new Error("must not fetch"); }) as typeof fetch;
  try {
    let registeredId = ""; let registeredConfig: any = null;
    const fakePi = {
      registerProvider(id: string, config: any) { registeredId = id; registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);
    assert.equal(registeredId, "opencode-free");
    assert.equal(fetchCount, 0);                    // no fetch during extension load
    assert.deepEqual(registeredConfig.models, []);  // populated by refreshModels
    assert.equal(registeredConfig.api, "openai-completions");
    assert.equal(registeredConfig.baseUrl, "https://opencode.ai/zen/v1");
    assert.equal(registeredConfig.name, "OpenCode Direct (Free)");
    assert.equal(registeredConfig.apiKey, "none");
    assert.equal(registeredConfig.headers["x-opencode-client"], "cli");
    assert.equal(registeredConfig.headers["x-opencode-project"], "global");
    assert.equal(typeof registeredConfig.refreshModels, "function");
    // Thin keyless shim over the native engine.
    assert.equal(typeof registeredConfig.streamSimple, "function");
  } finally { globalThis.fetch = originalFetch; }
});

test("module exports are defined and functions", () => {
  assert.equal(typeof opencodeDirectExtension, "function");
  assert.equal(typeof discoverModels, "function");
});

test("package.json is configured for public release and distribution", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  assert.equal(pkg.private, undefined, "package must not be private");
  assert.equal(pkg.license, "GPL-3.0-or-later");
  assert.deepEqual(pkg.pi?.extensions, ["./dist/index.js"]);
  assert.ok(pkg.files?.includes("dist"));
  assert.ok(pkg.scripts?.prepack);
});

test("cache-only refresh restores persisted snapshot without network", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => { throw new Error("must not fetch"); }) as typeof fetch;
  try {
    let registeredConfig: any = null;
    const fakePi = {
      registerProvider(_id: string, config: any) { registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);

    const completionsModel = {
      id: "hy3-free", name: "Hy3 (Free)", api: "openai-completions",
      provider: "opencode-free", baseUrl: "https://opencode.ai/zen/v1",
      reasoning: true, input: ["text"], contextWindow: 256_000, maxTokens: 64_000,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      compat: { supportsStore: false, supportsDeveloperRole: false, maxTokensField: "max_tokens" },
    };
    const responsesModel = {
      ...completionsModel,
      id: "muse-spark-1.2-contributor-free", name: "Muse Spark 1.2 Free",
      api: "openai-responses",
    };
    const restored = await registeredConfig.refreshModels({
      allowNetwork: false,
      signal: new AbortController().signal,
      stored: { models: [completionsModel, responsesModel] },
      publish: async () => true,
    });
    assert.equal(restored.length, 2);
    assert.equal(restored[0].id, "hy3-free");            // stripped back to config shape
    assert.equal(restored[0].contextWindow, 256_000);
    assert.equal((restored[0] as any).provider, undefined); // provider/baseUrl removed
    assert.equal((restored[1] as any).api, "openai-responses"); // per-model API survives restore
  } finally { globalThis.fetch = originalFetch; }
});

test("cache-only refresh with no stored snapshot yields empty list", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => { throw new Error("must not fetch"); }) as typeof fetch;
  try {
    let registeredConfig: any = null;
    const fakePi = {
      registerProvider(_id: string, config: any) { registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);
    const restored = await registeredConfig.refreshModels({
      allowNetwork: false, signal: new AbortController().signal,
      stored: undefined, publish: async () => true,
    });
    assert.deepEqual(restored, []);
  } finally { globalThis.fetch = originalFetch; }
});

test("network refresh discovers models and persists a stamped snapshot", async () => {
  const originalFetch = globalThis.fetch;
  const zenResponse = { ok: true, json: async () => ({ data: [{ id: "hy3-free", name: "Hy3" }] }) };
  let published: any = null;
  globalThis.fetch = (async () => zenResponse) as unknown as typeof fetch;
  try {
    let registeredConfig: any = null;
    const fakePi = {
      registerProvider(_id: string, config: any) { registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);

    const refreshed = await registeredConfig.refreshModels({
      allowNetwork: true,
      signal: new AbortController().signal,
      stored: undefined,
      publish: async (publication: any) => { published = publication; return true; },
    });

    assert.equal(refreshed[0].id, "hy3-free");
    assert.equal(refreshed[0].compat.maxTokensField, "max_tokens"); // config shape
    assert.ok(published, "persist must be called on successful discovery");
    assert.equal(published.persist.models[0].provider, "opencode-free"); // stamped
    assert.equal(published.persist.models[0].api, "openai-completions");
    assert.equal(typeof published.persist.checkedAt, "number");
  } finally { globalThis.fetch = originalFetch; }
});

test("network refresh never wipes a good snapshot when discovery comes back empty", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => { throw new Error("Offline"); }) as typeof fetch;
  try {
    let registeredConfig: any = null; let publishCalled = false;
    const fakePi = {
      registerProvider(_id: string, config: any) { registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);
    const refreshed = await registeredConfig.refreshModels({
      allowNetwork: true, signal: new AbortController().signal,
      stored: { models: [{ id: "old", provider: "opencode-free", api: "openai-completions" }] },
      publish: async () => { publishCalled = true; return true; },
    });
    assert.deepEqual(refreshed, []);
    assert.equal(publishCalled, false);
  } finally { globalThis.fetch = originalFetch; }
});

test("network refresh respects abort signal before fetching", async () => {
  const originalFetch = globalThis.fetch;
  let fetchCount = 0;
  globalThis.fetch = (async () => { fetchCount++; return { ok: true }; }) as unknown as typeof fetch;
  try {
    let registeredConfig: any = null;
    const fakePi = {
      registerProvider(_id: string, config: any) { registeredConfig = config; },
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);
    const controller = new AbortController();
    controller.abort();
    const refreshed = await registeredConfig.refreshModels({
      allowNetwork: true, signal: controller.signal,
      stored: undefined, publish: async () => true,
    });
    assert.deepEqual(refreshed, []);
    assert.equal(fetchCount, 0);
  } finally { globalThis.fetch = originalFetch; }
});

test("registers no slash commands", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => ({ ok: true, json: async () => ({ data: [] }) })) as unknown as typeof fetch;
  try {
    const commands: string[] = [];
    const fakePi = {
      registerProvider() {},
    } as unknown as ExtensionAPI;
    await opencodeDirectExtension(fakePi);
    assert.deepEqual(commands, []);
  } finally { globalThis.fetch = originalFetch; }
});
