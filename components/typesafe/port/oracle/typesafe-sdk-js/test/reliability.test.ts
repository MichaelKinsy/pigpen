import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  APIConnectionError,
  APIError,
  APITimeoutError,
  APIUserAbortError,
  BadRequestError,
  InternalServerError,
  type Logger,
  RateLimitError,
  type RetryPolicy,
  TypeSafeClient,
  TypeSafeError,
} from "../src";
import { DEFAULT_RETRY_POLICY } from "../src/retry";
import { json, mockFetch } from "./helpers";

const MODELS = [{ name: "m", description: "d", release_date: "2026" }];

const infoLogger = (): Logger & { lines: string[] } => {
  const lines: string[] = [];
  const at = (m: string) => {
    lines.push(m);
  };
  return { lines, debug: () => {}, info: at, warn: at, error: at };
};

/** Settle a promise by draining pending fake timers. */
const settle = async <T>(p: Promise<T>): Promise<T> => {
  // Attach a handler first so a rejection is never left unobserved while timers run.
  const guarded = p.then(
    (v) => ({ ok: true as const, v }),
    (e: unknown) => ({ ok: false as const, e }),
  );
  await vi.runAllTimersAsync();
  const r = await guarded;
  if (r.ok) return r.v;
  throw r.e;
};

describe("retries", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0); // no jitter: delays are exactly 500, 1000, 2000...
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("retries a 429 and then succeeds, honoring Retry-After", async () => {
    const logger = infoLogger();
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls === 1
        ? json({}, { status: 429, headers: { "retry-after": "2" } })
        : json({ models: MODELS }),
    );
    const client = new TypeSafeClient({ apiKey: "k", fetch, logger, logLevel: "info" });

    const p = client.models.list();
    await vi.advanceTimersByTimeAsync(1999);
    expect(requests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(await settle(p)).toEqual(MODELS);

    expect(requests).toHaveLength(2);
    expect(logger.lines).toEqual([
      expect.stringMatching(/^#1 GET \/v1\/models <- 429 in \d+ms$/),
      "#1 GET /v1/models retrying in 2000ms (retry 1/2) after 429",
      expect.stringMatching(/^#1 GET \/v1\/models <- 200 in \d+ms$/),
    ]);
  });

  it("uses exponential backoff when there is no Retry-After", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls <= 2 ? json({}, { status: 503 }) : json({ models: MODELS }),
    );
    const client = new TypeSafeClient({ apiKey: "k", fetch });

    const p = client.models.list();
    await vi.advanceTimersByTimeAsync(499);
    expect(requests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(requests).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(999);
    expect(requests).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(requests).toHaveLength(3);
    expect(await settle(p)).toEqual(MODELS);
  });

  it("gives up after maxRetries and throws the last error", async () => {
    const { fetch, requests } = mockFetch(() => json({ message: "down" }, { status: 500 }));
    const client = new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetries: 3 } });
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(InternalServerError);
    expect(requests).toHaveLength(4);
  });

  it("does not retry non-retryable statuses", async () => {
    const { fetch, requests } = mockFetch(() => json({}, { status: 400 }));
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(BadRequestError);
    expect(requests).toHaveLength(1);
  });

  it("retries connection errors", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() => {
      if (++calls === 1) throw new TypeError("fetch failed");
      return json({ models: MODELS });
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    expect(await settle(client.models.list())).toEqual(MODELS);
    expect(requests).toHaveLength(2);
  });

  it("never retries a caller abort", async () => {
    const ac = new AbortController();
    const { fetch, requests } = mockFetch(() => {
      ac.abort();
      throw new DOMException("aborted", "AbortError");
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    await expect(settle(client.models.list({ signal: ac.signal }))).rejects.toBeInstanceOf(
      APIUserAbortError,
    );
    expect(requests).toHaveLength(1);
  });

  it("aborting during the backoff wait throws APIUserAbortError", async () => {
    const ac = new AbortController();
    const { fetch, requests } = mockFetch(() => json({}, { status: 503 }));
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    const p = client.models.list({ signal: ac.signal });
    const guarded = p.catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(100);
    ac.abort();
    expect(await guarded).toBeInstanceOf(APIUserAbortError);
    expect(requests).toHaveLength(1);
  });

  it("per-call maxRetries overrides the client, and 0 disables retries", async () => {
    const { fetch, requests } = mockFetch(() => json({}, { status: 503 }));
    const client = new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetries: 5 } });
    await expect(settle(client.models.list({ retry: { maxRetries: 0 } }))).rejects.toBeInstanceOf(
      InternalServerError,
    );
    expect(requests).toHaveLength(1);
  });

  it("tells the server which retry this is", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls <= 2 ? json({}, { status: 503 }) : json({ models: MODELS }),
    );
    await settle(new TypeSafeClient({ apiKey: "k", fetch }).models.list());
    const retryHeader = (i: number) =>
      (requests[i]?.init?.headers as Record<string, string> | undefined)?.[
        "X-TypeSafe-Retry-Count"
      ];
    expect(retryHeader(0)).toBeUndefined();
    expect(retryHeader(1)).toBe("1");
    expect(retryHeader(2)).toBe("2");
  });

  it("rejects invalid maxRetries from config or per call", () => {
    expect(() => new TypeSafeClient({ apiKey: "k", retry: { maxRetries: -1 } })).toThrow(
      TypeSafeError,
    );
    expect(() => new TypeSafeClient({ apiKey: "k", retry: { maxRetries: 1.5 } })).toThrow(
      "retry.maxRetries",
    );
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ models: [] })).fetch,
    });
    expect(() => client.models.list({ retry: { maxRetries: -1 } })).toThrow("retry.maxRetries");
  });
});

describe("retry policy", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  const always = (status: number) => mockFetch(() => json({}, { status }));

  it("defaults to the SDK policy and exposes the resolved policy on the client", () => {
    expect(new TypeSafeClient({ apiKey: "k" }).retry).toEqual(DEFAULT_RETRY_POLICY);
    const client = new TypeSafeClient({ apiKey: "k", retry: { maxRetries: 7, backoffJitter: 0 } });
    expect(client.retry).toEqual({ ...DEFAULT_RETRY_POLICY, maxRetries: 7, backoffJitter: 0 });
  });

  it("isolates the policy and status set between default clients", () => {
    const first = new TypeSafeClient({ apiKey: "k" });
    const second = new TypeSafeClient({ apiKey: "k" });
    expect(first.retry).not.toBe(second.retry);
    expect(first.retry.httpStatuses).not.toBe(second.retry.httpStatuses);
    // JavaScript callers can mutate these despite TypeScript's readonly annotations.
    Reflect.set(first.retry, "maxRetries", 0);
    (first.retry.httpStatuses as Set<number>).clear();
    for (const client of [second, new TypeSafeClient({ apiKey: "k" })]) {
      expect(client.retry.maxRetries).toBe(2);
      expect(client.retry.httpStatuses.has(503)).toBe(true);
    }
  });

  it("copies caller-owned status sets when constructing a client", async () => {
    const statuses = new Set([503]);
    const { fetch, requests } = always(503);
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      retry: { httpStatuses: statuses, maxRetries: 1 },
    });
    statuses.clear();
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(InternalServerError);
    expect(requests).toHaveLength(2);
  });

  it.each(["client", "call"] as const)(
    "snapshots the %s policy for an in-flight request",
    async (source) => {
      const statuses = new Set([503]);
      const { fetch, requests } = always(503);
      const client = new TypeSafeClient({
        apiKey: "k",
        fetch,
        retry: { maxRetries: 1, httpStatuses: new Set([503]) },
      });
      const pending = client.models.list(
        source === "call" ? { retry: { httpStatuses: statuses } } : {},
      );
      Reflect.set(client.retry, "maxRetries", 0);
      (client.retry.httpStatuses as Set<number>).clear();
      statuses.clear();
      await expect(settle(pending)).rejects.toBeInstanceOf(InternalServerError);
      expect(requests).toHaveLength(2);
    },
  );

  it("can extend the default statuses by spreading the client's policy", () => {
    const client = new TypeSafeClient({ apiKey: "k" });
    const extended = new TypeSafeClient({
      apiKey: "k",
      retry: { httpStatuses: new Set([...client.retry.httpStatuses, 409]) },
    });
    expect(extended.retry.httpStatuses.has(409)).toBe(true);
    expect(extended.retry.httpStatuses.has(503)).toBe(true);
    expect(client.retry.httpStatuses.has(409)).toBe(false);
  });

  it("retries only the statuses in httpStatuses", async () => {
    const { fetch, requests } = always(409);
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      retry: { httpStatuses: new Set([409]) },
    });
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(APIError);
    expect(requests).toHaveLength(3);

    const server = always(503);
    const noServerRetries = new TypeSafeClient({
      apiKey: "k",
      fetch: server.fetch,
      retry: { httpStatuses: new Set([]) },
    });
    await expect(settle(noServerRetries.models.list())).rejects.toBeInstanceOf(InternalServerError);
    expect(server.requests).toHaveLength(1);
  });

  it("can stop retrying connection errors while still retrying timeouts", async () => {
    const dropped = mockFetch(() => {
      throw new TypeError("fetch failed");
    });
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: dropped.fetch,
      retry: { apiConnectionError: false },
    });
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(APIConnectionError);
    expect(dropped.requests).toHaveLength(1);

    const hung = mockFetch(
      ({ init }) =>
        new Promise<Response>((_, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        }),
    );
    const timeouts = new TypeSafeClient({
      apiKey: "k",
      fetch: hung.fetch,
      timeout: 10,
      retry: { apiConnectionError: false, maxRetries: 1 },
    });
    await expect(settle(timeouts.models.list())).rejects.toBeInstanceOf(APITimeoutError);
    expect(hung.requests).toHaveLength(2);
  });

  it("can stop retrying timeouts while still retrying connection errors", async () => {
    const hung = mockFetch(
      ({ init }) =>
        new Promise<Response>((_, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        }),
    );
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: hung.fetch,
      timeout: 10,
      retry: { apiTimeoutError: false },
    });
    await expect(settle(client.models.list())).rejects.toBeInstanceOf(APITimeoutError);
    expect(hung.requests).toHaveLength(1);

    const dropped = mockFetch(() => {
      throw new TypeError("fetch failed");
    });
    const connections = new TypeSafeClient({
      apiKey: "k",
      fetch: dropped.fetch,
      retry: { apiTimeoutError: false, maxRetries: 1 },
    });
    await expect(settle(connections.models.list())).rejects.toBeInstanceOf(APIConnectionError);
    expect(dropped.requests).toHaveLength(2);
  });

  it("backs off from the configured initial delay and cap", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls <= 3 ? json({}, { status: 503 }) : json({ models: MODELS }),
    );
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      retry: { maxRetries: 3, backoffInitialMs: 100, backoffMaxMs: 150 },
    });
    const p = client.models.list();
    await vi.advanceTimersByTimeAsync(99);
    expect(requests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(requests).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(150);
    expect(requests).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(150);
    expect(requests).toHaveLength(4);
    expect(await settle(p)).toEqual(MODELS);
  });

  it("can ignore Retry-After, and caps how long a Retry-After may be", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls === 1
        ? json({}, { status: 429, headers: { "retry-after": "2" } })
        : json({ models: MODELS }),
    );
    const client = new TypeSafeClient({ apiKey: "k", fetch, retry: { respectRetryAfter: false } });
    const p = client.models.list();
    await vi.advanceTimersByTimeAsync(500);
    expect(requests).toHaveLength(2);
    expect(await settle(p)).toEqual(MODELS);

    calls = 0;
    const capped = new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetryAfterMs: 1000 } });
    const q = capped.models.list();
    await vi.advanceTimersByTimeAsync(500);
    expect(requests).toHaveLength(4);
    expect(await settle(q)).toEqual(MODELS);
  });

  it("per-call retry overrides field by field and leaves the client's policy alone", async () => {
    const logger = infoLogger();
    const { fetch, requests } = always(503);
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      logger,
      logLevel: "info",
      retry: { maxRetries: 5, backoffInitialMs: 100 },
    });
    await expect(settle(client.models.list({ retry: { maxRetries: 1 } }))).rejects.toBeInstanceOf(
      InternalServerError,
    );
    expect(requests).toHaveLength(2);
    // backoffInitialMs came from the client, maxRetries from the call.
    expect(logger.lines).toContain("#1 GET /v1/models retrying in 100ms (retry 1/1) after 503");
    expect(client.retry.maxRetries).toBe(5);
  });

  it("validates every numeric field and names it", () => {
    const bad = (retry: Partial<RetryPolicy>) => () => new TypeSafeClient({ apiKey: "k", retry });
    expect(bad({ backoffInitialMs: -1 })).toThrow("retry.backoffInitialMs");
    expect(bad({ backoffMaxMs: Number.NaN })).toThrow("retry.backoffMaxMs");
    expect(bad({ backoffJitter: 1.5 })).toThrow("retry.backoffJitter");
    expect(bad({ backoffJitter: -0.1 })).toThrow("retry.backoffJitter");
    expect(bad({ maxRetryAfterMs: Number.NEGATIVE_INFINITY })).toThrow("retry.maxRetryAfterMs");
    expect(bad({ httpStatuses: new Set([503, 42]) })).toThrow("retry.httpStatuses");
    expect(bad({ httpStatuses: new Set([500.5]) })).toThrow("retry.httpStatuses");
    expect(bad({ backoffInitialMs: 0, backoffMaxMs: 0, backoffJitter: 0 })).not.toThrow();
    expect(bad({ backoffJitter: 1, maxRetryAfterMs: 0 })).not.toThrow();
    expect(bad({ maxRetryAfterMs: Number.POSITIVE_INFINITY })).toThrow("retry.maxRetryAfterMs");
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ models: [] })).fetch,
    });
    expect(() => client.models.list({ retry: { backoffJitter: 2 } })).toThrow(
      "retry.backoffJitter",
    );
  });
});

describe("timeouts", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  /** Create a fetch mock that waits for cancellation. */
  const hangingFetch = () =>
    mockFetch(
      ({ init }) =>
        new Promise<Response>((_, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        }),
    );

  it("throws APITimeoutError, which is an APIConnectionError", async () => {
    const { fetch } = hangingFetch();
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      timeout: 1000,
      retry: { maxRetries: 0 },
    });
    const err = await settle(client.models.list().catch((e: unknown) => e));
    expect(err).toBeInstanceOf(APITimeoutError);
    expect(err).toBeInstanceOf(APIConnectionError);
    expect((err as APITimeoutError).timeoutMs).toBe(1000);
    expect((err as Error).message).toBe("Request timed out after 1000ms.");
  });

  it("fires at exactly the configured timeout", async () => {
    const { fetch, requests } = hangingFetch();
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      timeout: 1000,
      retry: { maxRetries: 0 },
    });
    const guarded = client.models.list().catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(999);
    expect(requests[0]?.init?.signal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(requests[0]?.init?.signal?.aborted).toBe(true);
    expect(await guarded).toBeInstanceOf(APITimeoutError);
  });

  it("retries after a timeout, each attempt getting its own timeout", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(({ init }) => {
      if (++calls === 1) {
        return new Promise<Response>((_, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        });
      }
      return json({ models: MODELS });
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch, timeout: 1000 });
    expect(await settle(client.models.list())).toEqual(MODELS);
    expect(requests).toHaveLength(2);
  });

  it("per-call timeout overrides the client", async () => {
    const { fetch, requests } = hangingFetch();
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch,
      timeout: 60_000,
      retry: { maxRetries: 0 },
    });
    const guarded = client.models.list({ timeout: 50 }).catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(50);
    expect(requests[0]?.init?.signal?.aborted).toBe(true);
    expect(await guarded).toBeInstanceOf(APITimeoutError);
  });

  it("a caller abort during a hung request is reported as an abort, not a timeout", async () => {
    const ac = new AbortController();
    const { fetch } = hangingFetch();
    const client = new TypeSafeClient({ apiKey: "k", fetch, timeout: 60_000 });
    const guarded = client.models.list({ signal: ac.signal }).catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(10);
    ac.abort();
    expect(await guarded).toBeInstanceOf(APIUserAbortError);
  });

  it("clears its timer after a fast response", async () => {
    const { fetch } = mockFetch(() => json({ models: MODELS }));
    const client = new TypeSafeClient({ apiKey: "k", fetch, timeout: 1000 });
    await client.models.list();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("rejects invalid timeouts from config or per call", () => {
    expect(() => new TypeSafeClient({ apiKey: "k", timeout: 0 })).toThrow("timeout");
    expect(() => new TypeSafeClient({ apiKey: "k", timeout: Number.POSITIVE_INFINITY })).toThrow(
      TypeSafeError,
    );
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ models: [] })).fetch,
    });
    expect(() => client.models.list({ timeout: -5 })).toThrow("timeout");
  });
});

describe("RateLimitError", () => {
  it("exposes the parsed Retry-After in milliseconds", async () => {
    const { fetch } = mockFetch(() => json({}, { status: 429, headers: { "retry-after": "7" } }));
    const err = await new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetries: 0 } }).models
      .list()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RateLimitError);
    expect((err as RateLimitError).retryAfterMs).toBe(7000);
  });

  it("is undefined when the server sent no Retry-After", async () => {
    const { fetch } = mockFetch(() => json({}, { status: 429 }));
    const err = await new TypeSafeClient({ apiKey: "k", fetch, retry: { maxRetries: 0 } }).models
      .list()
      .catch((e: unknown) => e);
    expect((err as RateLimitError).retryAfterMs).toBeUndefined();
  });
});

describe("browsers", () => {
  afterEach(() => vi.unstubAllGlobals());

  const pretendBrowser = () => {
    vi.stubGlobal("window", { document: {} });
    vi.stubGlobal("navigator", { userAgent: "test" });
  };

  it("refuses to construct in a browser by default", () => {
    pretendBrowser();
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow(TypeSafeError);
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow("dangerouslyAllowBrowser");
  });

  it("constructs in a browser when explicitly allowed", () => {
    pretendBrowser();
    expect(() => new TypeSafeClient({ apiKey: "k", dangerouslyAllowBrowser: true })).not.toThrow();
  });

  it("does not mistake Node for a browser", () => {
    expect(() => new TypeSafeClient({ apiKey: "k" })).not.toThrow();
  });

  it("calls the global fetch with a receiver it accepts", async () => {
    // Browser fetch throws "Illegal invocation" unless `this` is the global object (or undefined).
    const picky = vi.fn(function (this: unknown) {
      if (this !== undefined && this !== globalThis) throw new TypeError("Illegal invocation");
      return Promise.resolve(json({ models: MODELS }));
    });
    vi.stubGlobal("fetch", picky);
    const client = new TypeSafeClient({ apiKey: "k" });
    expect(await client.models.list()).toEqual(MODELS);
    expect(picky).toHaveBeenCalledOnce();
  });

  it("fails clearly when no fetch exists and none was provided", () => {
    vi.stubGlobal("fetch", undefined);
    expect(() => new TypeSafeClient({ apiKey: "k" })).toThrow("No global `fetch`");
    expect(
      () => new TypeSafeClient({ apiKey: "k", fetch: mockFetch(() => json({ models: [] })).fetch }),
    ).not.toThrow();
  });
});
