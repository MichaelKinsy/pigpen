import { describe, expect, it } from "vitest";
import {
  DEFAULT_RETRY_POLICY,
  isRetryableStatus,
  parseRetryAfter,
  retryDelayMs,
  sleep,
} from "../src/retry";
import type { RetryPolicy } from "../src/types";

const h = (headers: Record<string, string>) => new Headers(headers);
const policy = (overrides: Partial<RetryPolicy>): RetryPolicy => ({
  ...DEFAULT_RETRY_POLICY,
  ...overrides,
});

describe("DEFAULT_RETRY_POLICY", () => {
  it("uses the SDK retry defaults", () => {
    expect(DEFAULT_RETRY_POLICY).toEqual({
      maxRetries: 2,
      backoffInitialMs: 500,
      backoffMaxMs: 5_000,
      backoffJitter: 0.25,
      httpStatuses: expect.any(Set),
      respectRetryAfter: true,
      maxRetryAfterMs: 60_000,
      apiConnectionError: true,
      apiTimeoutError: true,
    });
    expect([...DEFAULT_RETRY_POLICY.httpStatuses].sort((a, b) => a - b)).toEqual([
      408,
      429,
      ...Array.from({ length: 100 }, (_, i) => 500 + i),
    ]);
  });
});

describe("isRetryableStatus", () => {
  it.each([408, 429, 500, 502, 503, 504, 529, 599])("retries %i by default", (s) =>
    expect(isRetryableStatus(s)).toBe(true),
  );
  it.each([200, 400, 401, 403, 404, 409, 422, 600])("does not retry %i by default", (s) =>
    expect(isRetryableStatus(s)).toBe(false),
  );

  it("consults the policy's status set", () => {
    const p = policy({ httpStatuses: new Set([409]) });
    expect(isRetryableStatus(409, p)).toBe(true);
    expect(isRetryableStatus(503, p)).toBe(false);
    expect(isRetryableStatus(503, policy({ httpStatuses: new Set() }))).toBe(false);
  });
});

describe("parseRetryAfter", () => {
  it("reads Retry-After in seconds", () => {
    expect(parseRetryAfter(h({ "retry-after": "3" }))).toBe(3000);
    expect(parseRetryAfter(h({ "retry-after": "0" }))).toBe(0);
    expect(parseRetryAfter(h({ "retry-after": "1.5" }))).toBe(1500);
  });

  it("prefers retry-after-ms when present", () => {
    expect(parseRetryAfter(h({ "retry-after-ms": "250", "retry-after": "3" }))).toBe(250);
  });

  it("reads an HTTP date relative to now", () => {
    const now = Date.parse("Wed, 21 Oct 2026 07:28:00 GMT");
    expect(parseRetryAfter(h({ "retry-after": "Wed, 21 Oct 2026 07:28:05 GMT" }), now)).toBe(5000);
    expect(parseRetryAfter(h({ "retry-after": "Wed, 21 Oct 2026 07:27:00 GMT" }), now)).toBe(0);
  });

  it("returns undefined for missing or garbage values", () => {
    expect(parseRetryAfter(h({}))).toBeUndefined();
    expect(parseRetryAfter(h({ "retry-after": "soon" }))).toBeUndefined();
    expect(parseRetryAfter(h({ "retry-after": "-5" }))).toBeUndefined();
    expect(parseRetryAfter(h({ "retry-after-ms": "nope" }))).toBeUndefined();
  });
});

describe("retryDelayMs", () => {
  const noJitter = () => 0;
  const maxJitter = () => 1;
  const defaults = DEFAULT_RETRY_POLICY;

  it("grows exponentially from 500ms and caps at 5s", () => {
    expect([0, 1, 2, 3, 4, 5].map((a) => retryDelayMs(a, undefined, defaults, noJitter))).toEqual([
      500, 1000, 2000, 4000, 5000, 5000,
    ]);
  });

  it("shaves off at most 25% as jitter", () => {
    expect(retryDelayMs(0, undefined, defaults, maxJitter)).toBe(375);
    expect(retryDelayMs(1, undefined, defaults, () => 0.5)).toBe(875);
  });

  it("honors Retry-After exactly, with no jitter", () => {
    expect(retryDelayMs(0, h({ "retry-after": "2" }), defaults, maxJitter)).toBe(2000);
    expect(retryDelayMs(3, h({ "retry-after-ms": "10" }), defaults, maxJitter)).toBe(10);
  });

  it("falls back to backoff when Retry-After exceeds one minute", () => {
    expect(retryDelayMs(0, h({ "retry-after": "61" }), defaults, noJitter)).toBe(500);
    expect(retryDelayMs(0, h({ "retry-after": "60" }), defaults, noJitter)).toBe(60000);
    expect(retryDelayMs(0, h({ "retry-after-ms": "60000" }), defaults, noJitter)).toBe(60000);
    expect(retryDelayMs(0, h({ "retry-after-ms": "60001" }), defaults, maxJitter)).toBe(375);
  });

  it("uses the policy's initial delay, cap, and jitter", () => {
    const p = policy({ backoffInitialMs: 100, backoffMaxMs: 350, backoffJitter: 0.5 });
    expect([0, 1, 2, 3].map((a) => retryDelayMs(a, undefined, p, noJitter))).toEqual([
      100, 200, 350, 350,
    ]);
    expect(retryDelayMs(0, undefined, p, maxJitter)).toBe(50);
    expect(retryDelayMs(0, undefined, policy({ backoffJitter: 0 }), maxJitter)).toBe(500);
  });

  it("can ignore Retry-After entirely", () => {
    const p = policy({ respectRetryAfter: false });
    expect(retryDelayMs(0, h({ "retry-after": "2" }), p, noJitter)).toBe(500);
  });

  it("uses the policy's Retry-After ceiling", () => {
    const p = policy({ maxRetryAfterMs: 1000 });
    expect(retryDelayMs(0, h({ "retry-after": "1" }), p, noJitter)).toBe(1000);
    expect(retryDelayMs(0, h({ "retry-after": "2" }), p, noJitter)).toBe(500);
  });
});

describe("sleep", () => {
  it("rejects with the signal's reason when aborted mid-sleep", async () => {
    const ac = new AbortController();
    const p = sleep(10_000, ac.signal);
    ac.abort(new Error("stop"));
    await expect(p).rejects.toThrow("stop");
  });

  it("rejects immediately if the signal is already aborted", async () => {
    const ac = new AbortController();
    ac.abort(new Error("already"));
    await expect(sleep(10_000, ac.signal)).rejects.toThrow("already");
  });
});
