import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  APIConnectionError,
  APIUserAbortError,
  choice,
  ENV,
  noul,
  type Questions,
  score,
  TypeSafeClient,
  type TypeSafeClientConfig,
  TypeSafeError,
  VERSION,
} from "../src";
import { DEFAULT_BASE_URL, DEFAULT_MODEL } from "../src/client";
import { DEFAULT_LOG_LEVEL, LOG_LEVELS } from "../src/logging";
import { DEFAULT_RETRY_POLICY, DEFAULT_TIMEOUT_MS } from "../src/retry";
import { describeRuntime } from "../src/runtime";
import { json, mockFetch } from "./helpers";

/** Read the API key from an outgoing request. */
const sentApiKey = async (config: TypeSafeClientConfig = {}): Promise<string | undefined> => {
  const { fetch, requests } = mockFetch(() => json({ models: [] }));
  await new TypeSafeClient({ ...config, fetch }).models.list();
  const headers = requests[0]?.init?.headers as Record<string, string> | undefined;
  return headers?.Authorization?.replace(/^Bearer /, "");
};

const SYSTEM_ONE_RESPONSE = {
  model: "m",
  answers: { q1: { type: "noul", noul: 0.5 } },
  usage: { input_tokens: 1, output_tokens: 1 },
};

describe("TypeSafeClient configuration", () => {
  const savedEnv = { ...process.env };
  beforeEach(() => {
    for (const name of Object.values(ENV)) delete process.env[name];
  });
  afterEach(() => {
    process.env = { ...savedEnv };
  });

  it("falls back to defaults when neither config nor env is set", () => {
    const client = new TypeSafeClient({ apiKey: "k" });
    expect(client.baseURL).toBe(DEFAULT_BASE_URL);
    expect(client.defaultModel).toBe(DEFAULT_MODEL);
    expect(client.logLevel).toBe(DEFAULT_LOG_LEVEL);
    expect(client.retry).toEqual(DEFAULT_RETRY_POLICY);
    expect(client.timeout).toBe(DEFAULT_TIMEOUT_MS);
  });

  it("reads every setting from the environment", async () => {
    process.env[ENV.apiKey] = "env-key";
    process.env[ENV.baseURL] = "https://env.test";
    process.env[ENV.defaultModel] = "env-model";
    process.env[ENV.logLevel] = "debug";
    const client = new TypeSafeClient();
    await expect(sentApiKey()).resolves.toBe("env-key");
    expect(client.baseURL).toBe("https://env.test");
    expect(client.defaultModel).toBe("env-model");
    expect(client.logLevel).toBe("debug");
  });

  it("prefers config over the environment", async () => {
    process.env[ENV.apiKey] = "env-key";
    process.env[ENV.baseURL] = "https://env.test";
    process.env[ENV.defaultModel] = "env-model";
    process.env[ENV.logLevel] = "debug";
    const client = new TypeSafeClient({
      apiKey: "code-key",
      baseURL: "https://code.test",
      defaultModel: "code-model",
      logLevel: "error",
    });
    await expect(sentApiKey({ apiKey: "code-key" })).resolves.toBe("code-key");
    expect(client.baseURL).toBe("https://code.test");
    expect(client.defaultModel).toBe("code-model");
    expect(client.logLevel).toBe("error");
  });

  it("keeps the API key off the instance so logging the client cannot leak it", () => {
    const client = new TypeSafeClient({ apiKey: "super-secret" });
    expect("apiKey" in client).toBe(false);
    expect(Object.values(client)).not.toContain("super-secret");
    expect(JSON.stringify(client)).not.toContain("super-secret");
  });

  it("treats empty and whitespace-only env values as unset", () => {
    process.env[ENV.apiKey] = "k";
    process.env[ENV.baseURL] = "   ";
    process.env[ENV.defaultModel] = "";
    process.env[ENV.logLevel] = "";
    const client = new TypeSafeClient();
    expect(client.baseURL).toBe(DEFAULT_BASE_URL);
    expect(client.defaultModel).toBe(DEFAULT_MODEL);
    expect(client.logLevel).toBe(DEFAULT_LOG_LEVEL);
  });

  it("throws a TypeSafeError naming the env var when no API key is available", () => {
    expect(() => new TypeSafeClient()).toThrow(TypeSafeError);
    expect(() => new TypeSafeClient()).toThrow(ENV.apiKey);
  });

  it("strips trailing slashes from baseURL from either source", () => {
    process.env[ENV.baseURL] = "https://example.test///";
    expect(new TypeSafeClient({ apiKey: "k" }).baseURL).toBe("https://example.test");
    expect(new TypeSafeClient({ apiKey: "k", baseURL: "https://x.test/" }).baseURL).toBe(
      "https://x.test",
    );
  });

  it.each(LOG_LEVELS)("accepts log level %s", (level) => {
    expect(new TypeSafeClient({ apiKey: "k", logLevel: level }).logLevel).toBe(level);
    process.env[ENV.logLevel] = level;
    expect(new TypeSafeClient({ apiKey: "k" }).logLevel).toBe(level);
  });

  it("rejects an invalid log level and names where it came from", () => {
    process.env[ENV.logLevel] = "loud";
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow(TypeSafeError);
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow(`"loud" from ${ENV.logLevel}`);
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow(LOG_LEVELS.join(", "));
    // biome-ignore lint/suspicious/noExplicitAny: deliberately bypassing the type to mimic a JS caller
    expect(() => new TypeSafeClient({ apiKey: "k", logLevel: "loud" as any })).toThrow(
      "the `logLevel` option",
    );
  });
});

describe("requests", () => {
  it("sends auth and identifying headers", async () => {
    const { fetch, requests } = mockFetch(() => json({ models: [] }));
    const client = new TypeSafeClient({ apiKey: "secret", baseURL: "https://x.test", fetch });
    await client.models.list();

    expect(requests).toHaveLength(1);
    expect(requests[0]?.url).toBe("https://x.test/v1/models");
    expect(requests[0]?.init?.method).toBe("GET");
    const headers = requests[0]?.init?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer secret");
    expect(headers["User-Agent"]).toBe(`typesafe-sdk/${VERSION}`);
    expect(headers["X-TypeSafe-SDK"]).toBe(`typesafe-sdk/${VERSION}`);
    expect(headers["X-TypeSafe-Runtime"]).toBe(describeRuntime());
    expect(headers["X-TypeSafe-Runtime"]).toMatch(/^node\/\d+\.\d+\.\d+ \(\w+; \w+\)$/);
    expect(headers["Content-Type"]).toBeUndefined();
  });

  it("merges defaultHeaders and per-call headers, per-call winning, never clobbering auth", async () => {
    const { fetch, requests } = mockFetch(() => json({ models: [] }));
    const client = new TypeSafeClient({
      apiKey: "secret",
      fetch,
      defaultHeaders: { "X-Trace": "client", "X-Only-Default": "yes", Authorization: "nope" },
    });
    await client.models.list({ headers: { "X-Trace": "call", "X-Only-Call": "yes" } });
    const headers = requests[0]?.init?.headers as Record<string, string>;
    expect(headers["X-Trace"]).toBe("call");
    expect(headers["X-Only-Default"]).toBe("yes");
    expect(headers["X-Only-Call"]).toBe("yes");
    expect(headers.Authorization).toBe("Bearer secret");
  });

  it.each([{ cards: [] }, { cards: [{ name: "m", description: "d", release_date: "2026" }] }])(
    "models.list() unwraps the documented response: %j",
    async ({ cards }) => {
      const { fetch } = mockFetch(() => json({ models: cards }));
      const client = new TypeSafeClient({ apiKey: "k", fetch });
      expect(await client.models.list()).toEqual(cards);
      const { data, response } = await client.models.list().withResponse();
      expect(data).toEqual(cards);
      expect(response.status).toBe(200);
    },
  );

  it.each(
    [null, [], { models: { models: [] } }, { models: null }, { models: "bad" }, { ok: true }].map(
      (wire) => ({ wire }),
    ),
  )("models.list() fails clearly on an unrecognized shape: %j", async ({ wire }) => {
    const { fetch } = mockFetch(() => json(wire));
    await expect(new TypeSafeClient({ apiKey: "k", fetch }).models.list()).rejects.toThrow(
      "Unexpected response shape from GET /v1/models",
    );
  });

  it("preserves employee-only model fields through raw access", async () => {
    const wire = {
      models: [{ name: "m", description: "d", release_date: "2026", tags: ["internal"] }],
    };
    const { fetch } = mockFetch(() => json(wire));
    const raw = await new TypeSafeClient({ apiKey: "k", fetch }).models.list().asResponse();
    expect(raw.bodyUsed).toBe(false);
    expect(await raw.json()).toEqual(wire);
  });

  it("posts the systemOne payload with the default model", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const client = new TypeSafeClient({ apiKey: "k", baseURL: "https://x.test", fetch });
    const result = await client.systemOne({ state: { a: 1 }, questions: { q1: noul("x") } });

    expect(requests[0]?.url).toBe("https://x.test/v1/systemone");
    expect(requests[0]?.init?.method).toBe("POST");
    expect(requests[0]?.body).toEqual({
      state: { a: 1 },
      model: DEFAULT_MODEL,
      questions: { q1: { type: "noul", instructions: "x", criteria: undefined } },
    });
    expect(result.answers.q1.noul).toBe(0.5);
  });

  it("sends the request object as the payload, filling in the default model", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    const request = { state: { a: 1 }, questions: { q1: noul("x") } };
    await client.systemOne(request);
    expect(requests[0]?.body).toEqual({ ...request, model: DEFAULT_MODEL });
    // Passing the same object with `model` set sends it byte-for-byte.
    await client.systemOne({ ...request, model: "explicit" });
    expect(requests[1]?.body).toEqual({ ...request, model: "explicit" });
  });

  it("honors per-call model and client defaultModel", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const client = new TypeSafeClient({ apiKey: "k", fetch, defaultModel: "client-default" });
    await client.systemOne({ state: "s", questions: { q: choice("c", { a: null }) } });
    await client.systemOne({
      state: "s",
      questions: { q: choice("c", { a: null }) },
      model: "per-call",
    });
    expect(requests[0]?.body).toMatchObject({ model: "client-default" });
    expect(requests[1]?.body).toMatchObject({ model: "per-call" });
  });

  it("aborting the caller's signal aborts the signal handed to fetch, and wraps the error", async () => {
    const ac = new AbortController();
    const { fetch, requests } = mockFetch(() => {
      ac.abort();
      throw new DOMException("aborted", "AbortError");
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    await expect(client.models.list({ signal: ac.signal })).rejects.toBeInstanceOf(
      APIUserAbortError,
    );
    expect(requests[0]?.init?.signal?.aborted).toBe(true);
  });

  it("wraps network failures in APIConnectionError with the cause", async () => {
    const boom = new TypeError("fetch failed");
    const { fetch } = mockFetch(() => {
      throw boom;
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetries: 0 } });
    const err = await client.models.list().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(APIConnectionError);
    expect((err as APIConnectionError).cause).toBe(boom);
    expect((err as Error).message).toContain("fetch failed");
  });
});

describe("question builders", () => {
  it("choice accepts a criteria object as-is", () => {
    expect(choice("q", { a: "desc", b: null })).toEqual({
      type: "choice",
      instructions: "q",
      criteria: { a: "desc", b: null },
    });
  });

  it("choice rejects the removed label-list shorthand in JavaScript too", () => {
    expect(() => {
      // @ts-expect-error exercise plain JavaScript input
      choice("q", ["a", "b"]);
    }).toThrow(TypeSafeError);
  });

  it("noul allows describing one side, both, or neither", () => {
    expect(noul("q")).toEqual({ type: "noul", instructions: "q", criteria: undefined });
    expect(noul("q", { true: "yes means this" })).toEqual({
      type: "noul",
      instructions: "q",
      criteria: { true: "yes means this" },
    });
    expect(noul("q", { false: "no means this" }).criteria).toEqual({ false: "no means this" });
    expect(noul("q", { true: "a", false: "b" }).criteria).toEqual({ true: "a", false: "b" });
  });

  it("descriptions can be JSON objects, not only strings", () => {
    const rich = { summary: "warm", examples: ["hi!", "welcome"] };
    expect(choice("q", { friendly: rich, hostile: null }).criteria.friendly).toEqual(rich);
    expect(score("q", [rich, "meh"]).criteria[0]).toEqual(rich);
    expect(noul("q", { true: rich }).criteria?.true).toEqual(rich);
  });

  it("choice answers carry the winning label", async () => {
    const { fetch } = mockFetch(() =>
      json({
        model: "m",
        answers: {
          q: { type: "choice", choice: "b", confidence: 0.9, probabilities: { a: 0.1, b: 0.9 } },
        },
        usage: { input_tokens: 1, output_tokens: 1 },
      }),
    );
    const r = await new TypeSafeClient({ apiKey: "k", fetch }).systemOne({
      state: "s",
      questions: {
        q: choice("q", { a: null, b: null }),
      },
    });
    expect(r.answers.q.choice).toBe("b");
  });

  it("score keeps the list it was given and rejects maps", () => {
    expect(score("q", ["bad", "good"]).criteria).toEqual(["bad", "good"]);
    // biome-ignore lint/suspicious/noExplicitAny: deliberately malformed, as a JS caller might send
    expect(() => score("q", { 0: "bad", 1: "good" } as any)).toThrow(
      "Score criteria must be a list of descriptions indexed by score from zero, not a map.",
    );
  });
});

describe("wire format", () => {
  it("preserves null state, instructions, and criteria values", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const questions = {
      noul: noul(null, { true: null, false: null }),
      noCriteria: noul(null, null),
      choice: choice(null, { yes: null, no: null }),
      score: score(null, [null, "high"]),
    };
    await new TypeSafeClient({ apiKey: "k", fetch }).systemOne({ state: null, questions });
    expect(requests[0]?.body).toEqual({ model: DEFAULT_MODEL, state: null, questions });
  });

  it("allows omitted instructions and preserves JSON arrays", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const questions = {
      noul: { type: "noul", criteria: null },
      choice: { type: "choice", criteria: { yes: [null, { example: true }] } },
      score: { type: "score", criteria: [[null, "low"], null] },
      arrayInstructions: noul([null, { examples: [1, false] }], { true: ["yes", null] }),
      defaultInstructions: noul(),
    } satisfies Questions;
    const state = [null, { messages: ["hello"] }];
    await new TypeSafeClient({ apiKey: "k", fetch }).systemOne({ state, questions });
    expect(requests[0]?.body).toEqual({
      model: DEFAULT_MODEL,
      state,
      questions: {
        ...questions,
        defaultInstructions: { type: "noul", instructions: null },
      },
    });
  });

  const send = async (questions: Questions) => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    await new TypeSafeClient({ apiKey: "k", fetch }).systemOne({ state: "s", questions });
    const body = requests[0]?.body as
      | { questions: Record<string, { criteria: unknown }> }
      | undefined;
    return body?.questions ?? {};
  };

  it("sends choice criteria without rewriting", async () => {
    const wire = await send({ q: choice("which?", { a: null, b: null }) });
    expect(wire.q?.criteria).toEqual({ a: null, b: null });
  });

  it("sends a score list untouched", async () => {
    const wire = await send({ q: score("q", ["bad", "ok", "great"]) });
    expect(wire.q?.criteria).toEqual(["bad", "ok", "great"]);
  });

  it("rejects non-list score criteria, fewer than two criteria, and empty question sets before sending", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    // biome-ignore lint/suspicious/noExplicitAny: deliberately malformed, as a JS caller might send
    const bad = (criteria: any) =>
      client.systemOne({ state: "s", questions: { q: { type: "score", criteria } } });
    expect(() => bad({ 0: "bad", 1: "ok" })).toThrow(TypeSafeError);
    expect(() => bad({ 0: "bad", 1: "ok" })).toThrow(
      'Score question "q" has criteria that are not a list',
    );
    expect(() => bad([])).toThrow(
      'Score question "q" has 0 criteria; at least two scores are required.',
    );
    expect(() => bad(["only"])).toThrow("at least two scores");
    expect(() => client.systemOne({ state: "s", questions: {} })).toThrow(
      "At least one question is required",
    );
    expect(requests).toHaveLength(0);
  });
});

describe("1.0 primitive contract", () => {
  it("uses a ten-second default and keeps raw metadata access", async () => {
    const { fetch, requests } = mockFetch(() =>
      json(SYSTEM_ONE_RESPONSE, { headers: { "x-typesafe-request-id": "req_1" } }),
    );
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    expect(client.timeout).toBe(10000);
    const result = await client
      .systemOne({ state: "s", questions: { q1: noul("?") } })
      .withResponse();
    expect(result.data).toEqual(SYSTEM_ONE_RESPONSE);
    expect(result.requestId).toBe("req_1");
    expect(requests).toHaveLength(1);
  });

  it("forwards extra fields and null", async () => {
    const { fetch, requests } = mockFetch(() => json(SYSTEM_ONE_RESPONSE));
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    const request = {
      state: "s",
      questions: { q: score("?", ["low", "high"]) },
      future_option: null,
      nested: { enabled: true },
    };
    await client.systemOne(request);
    expect(requests[0]?.body).toEqual({ ...request, model: "jev-latest" });
    await client.systemOne({ state: "s", questions: { q: noul("?") } });
    expect(requests[1]?.body).not.toHaveProperty("future_option");
  });
});
