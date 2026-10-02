import { describe, expect, it } from "vitest";
import {
  APIError,
  AuthenticationError,
  BadRequestError,
  InternalServerError,
  NotFoundError,
  PermissionDeniedError,
  RateLimitError,
  TypeSafeClient,
  TypeSafeError,
  UnprocessableEntityError,
} from "../src";
import { json, mockFetch } from "./helpers";

// Retries are off here: these tests reuse one Response, and a retry would find its body consumed.
const clientReturning = (res: Response) =>
  new TypeSafeClient({ apiKey: "k", fetch: mockFetch(() => res).fetch, retry: { maxRetries: 0 } });

describe("APIError.fromResponse", () => {
  it.each([
    [400, BadRequestError],
    [401, AuthenticationError],
    [403, PermissionDeniedError],
    [404, NotFoundError],
    [422, UnprocessableEntityError],
    [429, RateLimitError],
    [500, InternalServerError],
    [503, InternalServerError],
    [418, APIError],
  ])("maps %i to %o", (status, cls) => {
    const err = APIError.fromResponse(status, undefined, new Headers());
    expect(err).toBeInstanceOf(cls);
    expect(err).toBeInstanceOf(APIError);
    expect(err).toBeInstanceOf(TypeSafeError);
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe(cls.name);
    expect(err.status).toBe(status);
  });
});

describe("error messages and bodies", () => {
  it("uses error.message from a JSON body and exposes the request id", async () => {
    const res = json(
      { error: { message: "invalid api key" } },
      { status: 401, headers: { "x-typesafe-request-id": "req_123" } },
    );
    const err = await clientReturning(res)
      .models.list()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AuthenticationError);
    const apiErr = err as APIError;
    expect(apiErr.message).toBe("401 invalid api key");
    expect(apiErr.requestId).toBe("req_123");
    expect(apiErr.body).toEqual({ error: { message: "invalid api key" } });
    expect(apiErr.headers.get("x-typesafe-request-id")).toBe("req_123");
  });

  it.each([
    [{ error: "plain string" }, "plain string"],
    [{ message: "top-level message" }, "top-level message"],
    [{ detail: "fastapi style" }, "fastapi style"],
    [
      { detail: { error_type: "api_usage_error", message: "Unknown model: x" } },
      "Unknown model: x",
    ],
    [
      {
        detail: [
          {
            type: "list_type",
            loc: ["body", "questions", "q", "score", "criteria"],
            msg: "Input should be a valid list",
          },
          {
            type: "too_short",
            loc: ["body", "questions"],
            msg: "Dictionary should have at least 1 item",
          },
        ],
      },
      "questions.q.score.criteria: Input should be a valid list; questions: Dictionary should have at least 1 item",
    ],
  ])("extracts a message from %o", async (body, expected) => {
    const err = await clientReturning(json(body, { status: 400 }))
      .models.list()
      .catch((e: unknown) => e);
    expect((err as Error).message).toBe(`400 ${expected}`);
  });

  it("falls back to the raw body when no message can be extracted, truncated", async () => {
    const short = await clientReturning(json({ code: 7 }, { status: 400 }))
      .models.list()
      .catch((e: unknown) => e);
    expect((short as Error).message).toBe('400 {"code":7}');

    const long = await clientReturning(json({ blob: "x".repeat(500) }, { status: 400 }))
      .models.list()
      .catch((e: unknown) => e);
    expect((long as Error).message).toHaveLength("400 ".length + 200 + 1);
    expect((long as Error).message.endsWith("…")).toBe(true);
  });

  it("keeps non-JSON bodies as text", async () => {
    const res = new Response("<h1>bad gateway</h1>", {
      status: 502,
      headers: { "content-type": "text/html" },
    });
    const err = await clientReturning(res)
      .models.list()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(InternalServerError);
    expect((err as APIError).body).toBe("<h1>bad gateway</h1>");
    expect((err as Error).message).toBe("502 <h1>bad gateway</h1>");
  });

  it("handles empty bodies", async () => {
    const res = new Response(null, { status: 429 });
    const err = await clientReturning(res)
      .models.list()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RateLimitError);
    expect((err as APIError).body).toBeUndefined();
    expect((err as Error).message).toBe("429 status code (no body)");
  });

  it("parses JSON even when content-type is missing", async () => {
    const res = new Response(JSON.stringify({ message: "no content type" }), { status: 400 });
    const err = await clientReturning(res)
      .models.list()
      .catch((e: unknown) => e);
    expect((err as APIError).body).toEqual({ message: "no content type" });
  });
});
