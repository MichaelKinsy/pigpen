import { afterEach, describe, expect, it, vi } from "vitest";
import {
  APIConnectionError,
  APITimeoutError,
  APIUserAbortError,
  noul,
  score,
  TypeSafeClient,
} from "../src";
import { json, mockFetch } from "./helpers";

describe("release regressions", () => {
  afterEach(() => vi.useRealTimers());

  it("replaces mixed-case defaults and protects every SDK header on every attempt", async () => {
    let calls = 0;
    const { fetch, requests } = mockFetch(() =>
      ++calls === 1 ? json({}, { status: 503 }) : json({}),
    );
    const client = new TypeSafeClient({
      apiKey: "secret",
      fetch,
      defaultHeaders: {
        "X-Team": "default",
        authorization: "bad",
        "content-type": "text/plain",
        "x-typesafe-retry-count": "99",
      },
      retry: { maxRetries: 1, backoffInitialMs: 0 },
    });
    await client.systemOne(
      { state: "s", questions: { q: noul("?") } },
      {
        headers: {
          "x-team": "call",
          AUTHORIZATION: "bad-again",
          ACCEPT: "text/plain",
          "USER-AGENT": "bad",
          "X-TYPESAFE-SDK": "bad",
          "X-TYPESAFE-RUNTIME": "bad",
          "CONTENT-TYPE": "text/html",
          "X-TYPESAFE-RETRY-COUNT": "88",
        },
      },
    );
    expect(requests).toHaveLength(2);
    for (const [index, request] of requests.entries()) {
      const headers = new Headers(request.init?.headers);
      expect(headers.get("authorization")).toBe("Bearer secret");
      expect(headers.get("x-team")).toBe("call");
      expect(headers.get("content-type")).toBe("application/json");
      expect(headers.get("accept")).toBe("application/json");
      expect(headers.get("user-agent")).toMatch(/^typesafe-sdk\//);
      expect(headers.get("x-typesafe-sdk")).toMatch(/^typesafe-sdk\//);
      expect(headers.get("x-typesafe-runtime")).not.toContain("bad");
      expect(headers.get("x-typesafe-retry-count")).toBe(index === 0 ? null : "1");
    }
  });

  it("does not send a caller-supplied content type or retry count on GET", async () => {
    const { fetch, requests } = mockFetch(() => json({ models: [] }));
    await new TypeSafeClient({
      apiKey: "k",
      fetch,
      defaultHeaders: { "content-type": "bad", "x-typesafe-retry-count": "99" },
    }).models.list();
    const headers = new Headers(requests[0]?.init?.headers);
    expect(headers.has("content-type")).toBe(false);
    expect(headers.has("x-typesafe-retry-count")).toBe(false);
  });

  it("preserves own __proto__ questions without mutation", async () => {
    const questions = JSON.parse('{"__proto__":{"type":"noul","instructions":"?"}}');
    questions.score = score("?", ["no", "yes"]);
    const original = JSON.stringify(questions);
    const { fetch, requests } = mockFetch(() => json({}));
    await new TypeSafeClient({ apiKey: "k", fetch }).systemOne({ state: "s", questions });
    const body = JSON.parse(String(requests[0]?.init?.body));
    expect(Object.hasOwn(body.questions, "__proto__")).toBe(true);
    expect(Object.getOwnPropertyDescriptor(body.questions, "__proto__")?.value).toEqual(noul("?"));
    expect(body.questions.score.criteria).toEqual(["no", "yes"]);
    expect(JSON.stringify(questions)).toBe(original);
  });

  it.each([200, 503])(
    "times out a stalled %s body even with a custom fetch ignoring signals",
    async (status) => {
      vi.useFakeTimers();
      const cancel = vi.fn();
      const { fetch, requests } = mockFetch(
        () => new Response(new ReadableStream({ cancel }), { status }),
      );
      const client = new TypeSafeClient({
        apiKey: "k",
        fetch,
        timeout: 50,
        retry: { maxRetries: 1, backoffInitialMs: 0 },
      });
      const guarded = client.models.list().catch((e: unknown) => e);
      await vi.runAllTimersAsync();
      expect(await guarded).toBeInstanceOf(APITimeoutError);
      expect(requests).toHaveLength(2);
      expect(cancel).toHaveBeenCalledTimes(2);
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it.each([200, 503])(
    "wraps %s body failures with their cause and honors disabled connection retries",
    async (status) => {
      const cause = new Error("socket dropped");
      const { fetch, requests } = mockFetch(
        () =>
          new Response(
            new ReadableStream({
              start(c) {
                c.error(cause);
              },
            }),
            { status },
          ),
      );
      const client = new TypeSafeClient({
        apiKey: "k",
        fetch,
        retry: { apiConnectionError: false },
      });
      const error = await client.models.list().catch((e: unknown) => e);
      expect(error).toBeInstanceOf(APIConnectionError);
      expect((error as APIConnectionError).cause).toBe(cause);
      expect(requests).toHaveLength(1);
    },
  );

  it("cancels a body when the signal was aborted just before fetch returned headers", async () => {
    const ac = new AbortController();
    const { fetch, requests } = mockFetch(() => {
      ac.abort();
      return new Response(new ReadableStream());
    });
    const client = new TypeSafeClient({ apiKey: "k", fetch });
    await expect(client.models.list({ signal: ac.signal })).rejects.toBeInstanceOf(
      APIUserAbortError,
    );
    expect(requests).toHaveLength(1);
  });

  it("returns a null-body response without trying to read a stream", async () => {
    const { fetch } = mockFetch(() => new Response(null, { status: 204 }));
    const response = await new TypeSafeClient({ apiKey: "k", fetch }).models.list().asResponse();
    expect(response.status).toBe(204);
    expect(response.body).toBeNull();
  });
});
