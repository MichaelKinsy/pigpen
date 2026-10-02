import { createServer, type RequestListener } from "node:http";
import { describe, expect, it } from "vitest";
import { APITimeoutError, APIUserAbortError, noul, TypeSafeClient } from "../src";

/** Real fetch/socket coverage: mocks do not reproduce the headers/body lifecycle. */
const withServer = async (
  handle: RequestListener,
  run: (baseURL: string) => Promise<void>,
): Promise<void> => {
  const server = createServer(handle);
  try {
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("No server address");
    await run(`http://127.0.0.1:${address.port}`);
  } finally {
    server.closeAllConnections();
    if (server.listening) {
      await new Promise<void>((resolve, reject) => {
        server.close((error) => (error ? reject(error) : resolve()));
      });
    }
  }
};

describe("native response transport", () => {
  it("sends one authorization, content type and overriding custom header on the wire", async () => {
    let observed: unknown;
    await withServer(
      (req, res) => {
        observed = req.headers;
        res.end("{}");
      },
      async (baseURL) => {
        await new TypeSafeClient({
          apiKey: "test",
          baseURL,
          defaultHeaders: { authorization: "bad", "X-Team": "default", "content-type": "bad" },
        }).systemOne(
          { state: "s", questions: { q: noul("?") } },
          { headers: { "x-team": "call" } },
        );
      },
    );
    expect(observed).toMatchObject({
      authorization: "Bearer test",
      "x-team": "call",
      "content-type": "application/json",
    });
  });

  it.each([200, 503])(
    "times out a stalled %s body on parsed, withResponse and raw paths",
    async (status) => {
      let headersReceived = 0;
      await withServer(
        (_req, res) => {
          res.writeHead(status, { "content-type": "application/json" });
          res.flushHeaders();
        },
        async (baseURL) => {
          const client = new TypeSafeClient({
            apiKey: "k",
            baseURL,
            timeout: 200,
            retry: { maxRetries: 0 },
            fetch: async (url, init) => {
              const response = await fetch(url, init);
              headersReceived++;
              return response;
            },
          });
          await expect(client.models.list()).rejects.toBeInstanceOf(APITimeoutError);
          await expect(client.models.list().withResponse()).rejects.toBeInstanceOf(APITimeoutError);
          await expect(client.models.list().asResponse()).rejects.toBeInstanceOf(APITimeoutError);
          expect(headersReceived).toBe(3);
        },
      );
    },
  );

  it.each([200, 503])(
    "honors caller cancellation after %s headers and never retries",
    async (status) => {
      let attempts = 0;
      await withServer(
        (_req, res) => {
          attempts++;
          res.writeHead(status, { "content-type": "application/json" });
          res.flushHeaders();
        },
        async (baseURL) => {
          const ac = new AbortController();
          const client = new TypeSafeClient({
            apiKey: "k",
            baseURL,
            fetch: async (url, init) => {
              const response = await fetch(url, init);
              setTimeout(() => ac.abort(), 10);
              return response;
            },
          });
          await expect(client.models.list({ signal: ac.signal })).rejects.toBeInstanceOf(
            APIUserAbortError,
          );
          expect(attempts).toBe(1);
        },
      );
    },
  );

  it.each([200, 503])(
    "retries a broken %s body and exposes the successful response metadata",
    async (status) => {
      let attempts = 0;
      await withServer(
        (_req, res) => {
          attempts++;
          res.writeHead(attempts === 1 ? status : 200, {
            "content-type": "application/json",
            "x-typesafe-request-id": `req_${attempts}`,
          });
          if (attempts === 1) {
            res.write("[");
            setTimeout(() => res.destroy(), 10);
          } else res.end('{"models":[]}');
        },
        async (baseURL) => {
          const client = new TypeSafeClient({
            apiKey: "k",
            baseURL,
            retry: { maxRetries: 1, backoffInitialMs: 0 },
          });
          const { data, response, requestId } = await client.models.list().withResponse();
          expect(data).toEqual([]);
          expect(attempts).toBe(2);
          expect(requestId).toBe("req_2");
          expect(response.url).toBe(`${baseURL}/v1/models`);
          expect(response.bodyUsed).toBe(true);
        },
      );
    },
  );

  it("buffers delayed chunks before raw handoff and preserves a readable body after cancellation", async () => {
    let completed = false;
    await withServer(
      (_req, res) => {
        res.writeHead(200, { "content-type": "application/json" });
        res.write("[");
        setTimeout(() => {
          completed = true;
          res.end("]");
        }, 20);
      },
      async (baseURL) => {
        const ac = new AbortController();
        const response = await new TypeSafeClient({ apiKey: "k", baseURL }).models
          .list({ signal: ac.signal })
          .asResponse();
        expect(completed).toBe(true);
        expect(response.bodyUsed).toBe(false);
        expect(response.url).toBe(`${baseURL}/v1/models`);
        ac.abort();
        expect(await response.json()).toEqual([]);
      },
    );
  });
});
