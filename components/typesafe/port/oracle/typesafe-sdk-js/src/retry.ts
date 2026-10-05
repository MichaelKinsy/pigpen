/** Retry defaults, delay calculation, and cancellable waits. */

import type { RetryPolicy } from "./types";

export const DEFAULT_TIMEOUT_MS = 10_000;

const range = (from: number, to: number): number[] =>
  Array.from({ length: to - from }, (_, i) => from + i);

/** Default SDK retry policy. */
export const DEFAULT_RETRY_POLICY: RetryPolicy = {
  maxRetries: 2,
  backoffInitialMs: 500,
  backoffMaxMs: 5_000,
  backoffJitter: 0.25,
  /** HTTP 408, 429, and 5xx responses. */
  httpStatuses: new Set([408, 429, ...range(500, 600)]),
  respectRetryAfter: true,
  /** Maximum server retry delay before falling back to backoff. */
  maxRetryAfterMs: 60_000,
  apiConnectionError: true,
  apiTimeoutError: true,
};

export const DEFAULT_MAX_RETRIES: number = DEFAULT_RETRY_POLICY.maxRetries;

/** Whether the policy retries an HTTP status code. */
export const isRetryableStatus = (
  status: number,
  policy: RetryPolicy = DEFAULT_RETRY_POLICY,
): boolean => policy.httpStatuses.has(status);

/**
 * Parse `retry-after-ms` or `Retry-After` into milliseconds, preferring `retry-after-ms`.
 *
 * Return `undefined` when neither header contains a valid delay.
 */
export const parseRetryAfter = (headers: Headers, now: number = Date.now()): number | undefined => {
  const ms = Number(headers.get("retry-after-ms"));
  if (headers.has("retry-after-ms") && Number.isFinite(ms) && ms >= 0) return ms;

  const raw = headers.get("retry-after");
  if (raw === null) return undefined;
  const seconds = Number(raw);
  if (Number.isFinite(seconds)) return seconds >= 0 ? seconds * 1000 : undefined;
  const date = Date.parse(raw);
  if (!Number.isNaN(date)) return Math.max(0, date - now);
  return undefined;
};

/**
 * Calculate the delay in milliseconds for a zero-based retry attempt.
 *
 * Use an allowed server delay; otherwise use capped exponential backoff with jitter.
 */
export const retryDelayMs = (
  attempt: number,
  headers?: Headers,
  policy: RetryPolicy = DEFAULT_RETRY_POLICY,
  random: () => number = Math.random,
): number => {
  if (policy.respectRetryAfter && headers !== undefined) {
    const retryAfter = parseRetryAfter(headers);
    if (retryAfter !== undefined && retryAfter <= policy.maxRetryAfterMs) return retryAfter;
  }
  const exponential = Math.min(policy.backoffInitialMs * 2 ** attempt, policy.backoffMaxMs);
  return Math.round(exponential * (1 - random() * policy.backoffJitter));
};

/** Wait `ms` milliseconds, rejecting with `signal.reason` on cancellation. */
export const sleep = (ms: number, signal?: AbortSignal): Promise<void> =>
  new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason);
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(signal?.reason);
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
