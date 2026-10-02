import { describe, expect, it, vi } from "vitest";
import { APIError, APIPromise, NotFoundError, TypeSafeClient } from "../src";
import { json, mockFetch } from "./helpers";

const MODELS = [{ name: "m", description: "d", release_date: "2026" }];

const clientWith = (respond: () => Response) =>
  new TypeSafeClient({ apiKey: "k", fetch: mockFetch(respond).fetch, retry: { maxRetries: 0 } });

describe("APIPromise", () => {
  it("is returned from client methods and is a real Promise", () => {
    const p = clientWith(() => json({ models: MODELS })).models.list();
    expect(p).toBeInstanceOf(APIPromise);
    expect(p).toBeInstanceOf(Promise);
  });

  it("awaits to the parsed data", async () => {
    expect(await clientWith(() => json({ models: MODELS })).models.list()).toEqual(MODELS);
  });

  it("withResponse() returns data, response, and requestId", async () => {
    const { data, response, requestId } = await clientWith(() =>
      json({ models: MODELS }, { headers: { "x-typesafe-request-id": "req_abc" } }),
    )
      .models.list()
      .withResponse();
    expect(data).toEqual(MODELS);
    expect(response).toBeInstanceOf(Response);
    expect(response.status).toBe(200);
    expect(response.headers.get("x-typesafe-request-id")).toBe("req_abc");
    expect(requestId).toBe("req_abc");
  });

  it("withResponse() has undefined requestId when the header is absent", async () => {
    const { requestId } = await clientWith(() => json({ models: MODELS }))
      .models.list()
      .withResponse();
    expect(requestId).toBeUndefined();
  });

  it("asResponse() returns the raw Response with an unconsumed body", async () => {
    const res = await clientWith(() => json({ models: MODELS }))
      .models.list()
      .asResponse();
    expect(res).toBeInstanceOf(Response);
    expect(res.bodyUsed).toBe(false);
    expect(await res.json()).toEqual({ models: MODELS });
  });

  it("parses the body only once across multiple consumers", async () => {
    const respond = vi.fn(() => json({ models: MODELS }));
    const p = clientWith(respond).models.list();
    const [a, b, { data: c }] = await Promise.all([p, p.then((x) => x), p.withResponse()]);
    expect(a).toEqual(MODELS);
    expect(b).toEqual(MODELS);
    expect(c).toEqual(MODELS);
    expect(respond).toHaveBeenCalledTimes(1);
  });

  it("rejects with APIError on every path", async () => {
    const make = () => clientWith(() => json({ message: "nope" }, { status: 404 })).models.list();
    await expect(make()).rejects.toBeInstanceOf(NotFoundError);
    await expect(make().withResponse()).rejects.toBeInstanceOf(NotFoundError);
    await expect(make().asResponse()).rejects.toBeInstanceOf(NotFoundError);
    const caught = await make().catch((e: unknown) => e);
    expect(caught).toBeInstanceOf(APIError);
  });

  it("map() transforms the data while sharing the response and parsing once", async () => {
    const respond = vi.fn(() =>
      json({ models: MODELS }, { headers: { "x-typesafe-request-id": "req_m" } }),
    );
    const p = clientWith(respond).models.list();
    const names = p.map((models) => models.map((m) => m.name));
    expect(names).toBeInstanceOf(APIPromise);
    expect(await names).toEqual(["m"]);
    const { data, requestId, response } = await names.withResponse();
    expect(data).toEqual(["m"]);
    expect(requestId).toBe("req_m");
    expect(response.status).toBe(200);
    expect(await p).toEqual(MODELS);
    expect(respond).toHaveBeenCalledTimes(1);
  });

  it("supports then/catch/finally chaining", async () => {
    const onFinally = vi.fn();
    const names = await clientWith(() => json({ models: MODELS }))
      .models.list()
      .then((models) => models.map((m) => m.name))
      .finally(onFinally);
    expect(names).toEqual(["m"]);
    expect(onFinally).toHaveBeenCalledOnce();

    const fallback = await clientWith(() => json({}, { status: 500 }))
      .models.list()
      .catch(() => "fallback")
      .finally(onFinally);
    expect(fallback).toBe("fallback");
    expect(onFinally).toHaveBeenCalledTimes(2);
  });

  it("does not issue the request until constructed, but does not require awaiting to send", async () => {
    const respond = vi.fn(() => json({ models: MODELS }));
    clientWith(respond).models.list();
    await new Promise((r) => setTimeout(r, 0));
    expect(respond).toHaveBeenCalledTimes(1);
  });
});
