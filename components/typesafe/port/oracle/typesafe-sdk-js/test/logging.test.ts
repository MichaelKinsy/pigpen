import { afterEach, describe, expect, it, vi } from "vitest";
import { type Logger, TypeSafeClient, VERSION } from "../src";
import { consoleLogger, redactHeaders } from "../src/logging";
import { json, mockFetch } from "./helpers";

/** Create a logger that records calls for assertions. */
const recordingLogger = (): Logger & {
  calls: [level: string, message: string, ...args: unknown[]][];
} => {
  const calls: [string, string, ...unknown[]][] = [];
  const at =
    (level: string) =>
    (message: string, ...args: unknown[]) => {
      calls.push([level, message, ...args]);
    };
  return { calls, debug: at("debug"), info: at("info"), warn: at("warn"), error: at("error") };
};

const messages = (logger: ReturnType<typeof recordingLogger>, level?: string): string[] =>
  logger.calls.filter((c) => level === undefined || c[0] === level).map((c) => c[1]);

describe("default console logger", () => {
  afterEach(() => vi.restoreAllMocks());

  it("is silent at the default level for a successful request", async () => {
    const spies = ["debug", "info", "warn", "error"].map((m) =>
      vi.spyOn(console, m as "debug").mockImplementation(() => {}),
    );
    await new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ models: [] })).fetch,
    }).models.list();
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
  });

  it("writes prefixed lines to console at debug level", async () => {
    const debug = vi.spyOn(console, "debug").mockImplementation(() => {});
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ models: [] })).fetch,
      logLevel: "debug",
    });
    await client.models.list();
    expect(debug).toHaveBeenCalled();
    expect(info).toHaveBeenCalled();
    for (const call of [...debug.mock.calls, ...info.mock.calls]) {
      expect(call[0]).toMatch(/^\[typesafe-sdk\] #1 GET \/v1\/models/);
    }
  });

  it("routes each level to the matching console method", () => {
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {});
    consoleLogger.warn("careful", { a: 1 });
    expect(spy).toHaveBeenCalledWith("[typesafe-sdk] careful", { a: 1 });
  });
});

describe("custom logger and level filtering", () => {
  const clientWith = (logger: Logger, logLevel: "debug" | "info" | "warn" | "error" | "off") =>
    new TypeSafeClient({
      apiKey: "sk_live_0123456789abcdef",
      baseURL: "https://x.test",
      fetch: mockFetch(() =>
        json({ models: [] }, { headers: { "x-typesafe-request-id": "req_9" } }),
      ).fetch,
      logger,
      logLevel,
    });

  it("exposes the level-filtered logger on the client", () => {
    const logger = recordingLogger();
    const client = clientWith(logger, "warn");
    client.logger.info("dropped");
    client.logger.warn("kept");
    expect(messages(logger)).toEqual(["kept"]);
  });

  it("drops everything at level off", async () => {
    const logger = recordingLogger();
    await clientWith(logger, "off").models.list();
    expect(logger.calls).toEqual([]);
  });

  it("logs a one-line summary at info and nothing at debug", async () => {
    const logger = recordingLogger();
    await clientWith(logger, "info").models.list();
    expect(messages(logger, "debug")).toEqual([]);
    expect(messages(logger, "info")).toEqual([
      expect.stringMatching(/^#1 GET \/v1\/models <- 200 in \d+ms \(request req_9\)$/),
    ]);
  });

  it("logs request headers, request body, and response body at debug", async () => {
    const logger = recordingLogger();
    const client = clientWith(logger, "debug");
    await client.models.list();

    const [level, message, detail] = logger.calls[0] ?? [];
    expect(level).toBe("debug");
    expect(message).toBe("#1 GET /v1/models -> https://x.test/v1/models");
    expect(detail).toEqual({
      headers: {
        Authorization: "Bearer ***cdef",
        Accept: "application/json",
        "User-Agent": `typesafe-sdk/${VERSION}`,
        "X-TypeSafe-SDK": `typesafe-sdk/${VERSION}`,
        "X-TypeSafe-Runtime": expect.stringMatching(/^node\//),
      },
      body: undefined,
    });

    expect(logger.calls.at(-1)).toEqual(["debug", "#1 GET /v1/models <- body", { models: [] }]);
  });

  it("never logs the raw API key", async () => {
    const logger = recordingLogger();
    await clientWith(logger, "debug").models.list();
    expect(JSON.stringify(logger.calls)).not.toContain("sk_live_0123456789abcdef");
  });

  it("numbers requests so concurrent calls can be told apart", async () => {
    const logger = recordingLogger();
    const client = clientWith(logger, "info");
    await Promise.all([client.models.list(), client.models.list()]);
    expect(
      messages(logger, "info")
        .map((m) => m.slice(0, 2))
        .sort(),
    ).toEqual(["#1", "#2"]);
  });

  it("logs error responses as a summary plus the body at debug, without a warn", async () => {
    const logger = recordingLogger();
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => json({ message: "nope" }, { status: 404 })).fetch,
      logger,
      logLevel: "debug",
    });
    await expect(client.models.list()).rejects.toThrow("404 nope");
    expect(messages(logger, "info")).toEqual([
      expect.stringMatching(/^#1 GET \/v1\/models <- 404 in \d+ms$/),
    ]);
    expect(logger.calls.at(-1)).toEqual([
      "debug",
      "#1 GET /v1/models <- error body",
      { message: "nope" },
    ]);
    expect(messages(logger, "warn")).toEqual([]);
    expect(messages(logger, "error")).toEqual([]);
  });

  it("logs connection failures with the underlying error", async () => {
    const logger = recordingLogger();
    const boom = new TypeError("fetch failed");
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => {
        throw boom;
      }).fetch,
      logger,
      logLevel: "info",
      retry: { maxRetries: 0 },
    });
    await expect(client.models.list()).rejects.toThrow("fetch failed");
    expect(logger.calls).toEqual([
      ["info", expect.stringMatching(/^#1 GET \/v1\/models connection error after \d+ms$/), boom],
    ]);
  });

  it("logs caller aborts", async () => {
    const logger = recordingLogger();
    const ac = new AbortController();
    const client = new TypeSafeClient({
      apiKey: "k",
      fetch: mockFetch(() => {
        ac.abort();
        throw new DOMException("aborted", "AbortError");
      }).fetch,
      logger,
      logLevel: "info",
    });
    await expect(client.models.list({ signal: ac.signal })).rejects.toThrow("aborted");
    expect(messages(logger, "info")).toEqual([
      expect.stringMatching(/^#1 GET \/v1\/models aborted by caller after \d+ms$/),
    ]);
  });
});

describe("redactHeaders", () => {
  it("masks credentials, keeps the scheme and the last four characters", () => {
    expect(
      redactHeaders({
        Authorization: "Bearer sk_live_0123456789abcdef",
        "x-api-key": "0123456789abcdef",
        Cookie: "session=abc",
        Accept: "application/json",
      }),
    ).toEqual({
      Authorization: "Bearer ***cdef",
      "x-api-key": "***cdef",
      Cookie: "***",
      Accept: "application/json",
    });
  });

  it("does not leak a tail from short secrets", () => {
    expect(redactHeaders({ authorization: "Bearer abc" })).toEqual({ authorization: "Bearer ***" });
  });

  it("does not mutate its input", () => {
    const headers = { Authorization: "Bearer x" };
    redactHeaders(headers);
    expect(headers.Authorization).toBe("Bearer x");
  });
});
