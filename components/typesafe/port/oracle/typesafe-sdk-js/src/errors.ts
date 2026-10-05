import { requestIdFrom } from "./api-promise";
import { parseRetryAfter } from "./retry";

/** Base class for SDK errors. */
export class TypeSafeError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = new.target.name;
  }
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null;

/** Extract a message from a text, error, or validation response body. */
const extractMessage = (body: unknown): string | undefined => {
  if (typeof body === "string") return body || undefined;
  if (!isRecord(body)) return undefined;
  const { error, message, detail } = body;
  if (typeof error === "string") return error;
  if (isRecord(error) && typeof error.message === "string") return error.message;
  if (typeof message === "string") return message;
  if (typeof detail === "string") return detail;
  if (isRecord(detail) && typeof detail.message === "string") return detail.message;
  if (Array.isArray(detail)) return describeValidationErrors(detail);
  return undefined;
};

/** Format validation errors as semicolon-separated `path: message` entries. */
const describeValidationErrors = (errors: unknown[]): string | undefined => {
  const parts = errors.flatMap((e) => {
    if (!isRecord(e) || typeof e.msg !== "string") return [];
    const loc = Array.isArray(e.loc) ? e.loc.filter((x) => x !== "body").join(".") : "";
    return [loc ? `${loc}: ${e.msg}` : e.msg];
  });
  return parts.length > 0 ? parts.join("; ") : undefined;
};

const MAX_RAW_BODY_IN_MESSAGE = 200;

/** An unsuccessful HTTP response from the API. */
export class APIError extends TypeSafeError {
  /** HTTP response status code. */
  readonly status: number;
  /** HTTP response headers. */
  readonly headers: Headers;
  /** Parsed JSON, response text, or `undefined` for an empty body. */
  readonly body: unknown;
  /** Request ID from `x-typesafe-request-id`, or `undefined` when absent. */
  readonly requestId: string | undefined;

  constructor(status: number, body: unknown, headers: Headers, message?: string) {
    super(message ?? APIError.describe(status, body));
    this.status = status;
    this.body = body;
    this.headers = headers;
    this.requestId = requestIdFrom(headers);
  }

  private static describe(status: number, body: unknown): string {
    const detail = extractMessage(body);
    if (detail) return `${status} ${detail}`;
    if (body === undefined) return `${status} status code (no body)`;
    const raw = typeof body === "string" ? body : JSON.stringify(body);
    return `${status} ${raw.length > MAX_RAW_BODY_IN_MESSAGE ? `${raw.slice(0, MAX_RAW_BODY_IN_MESSAGE)}…` : raw}`;
  }

  /** Create the error subclass for an HTTP status code. */
  static fromResponse(status: number, body: unknown, headers: Headers): APIError {
    if (status === 400) return new BadRequestError(status, body, headers);
    if (status === 401) return new AuthenticationError(status, body, headers);
    if (status === 403) return new PermissionDeniedError(status, body, headers);
    if (status === 404) return new NotFoundError(status, body, headers);
    if (status === 422) return new UnprocessableEntityError(status, body, headers);
    if (status === 429) return new RateLimitError(status, body, headers);
    if (status >= 500) return new InternalServerError(status, body, headers);
    return new APIError(status, body, headers);
  }
}

/** HTTP 400: the request is invalid. */
export class BadRequestError extends APIError {}
/** HTTP 401: authentication failed. */
export class AuthenticationError extends APIError {}
/** HTTP 403: access is denied. */
export class PermissionDeniedError extends APIError {}
/** HTTP 404: the resource was not found. */
export class NotFoundError extends APIError {}
/** HTTP 422: request validation failed. */
export class UnprocessableEntityError extends APIError {}
/** HTTP 429: the rate limit was exceeded. */
export class RateLimitError extends APIError {
  /** Server retry delay in milliseconds, or `undefined` when absent or invalid. */
  readonly retryAfterMs: number | undefined = parseRetryAfter(this.headers);
}
/** HTTP 5xx: the server failed to handle the request. */
export class InternalServerError extends APIError {}

/** The request or response-body delivery failed (DNS, TLS, connection closed, etc.). */
export class APIConnectionError extends TypeSafeError {
  constructor(message = "Connection error.", options?: ErrorOptions) {
    super(message, options);
  }
}

/** The full response did not arrive within the timeout. A kind of `APIConnectionError`. */
export class APITimeoutError extends APIConnectionError {
  /** Configured timeout in milliseconds. */
  readonly timeoutMs: number;

  constructor(timeoutMs: number, options?: ErrorOptions) {
    super(`Request timed out after ${timeoutMs}ms.`, options);
    this.timeoutMs = timeoutMs;
  }
}

/** The caller cancelled the request through an `AbortSignal`. */
export class APIUserAbortError extends TypeSafeError {
  constructor(message = "Request was aborted.", options?: ErrorOptions) {
    super(message, options);
  }
}
