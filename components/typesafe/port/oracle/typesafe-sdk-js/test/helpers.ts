import type { Fetch } from "../src";

export interface RecordedRequest {
  url: string;
  init: RequestInit | undefined;
  body: unknown;
}

/** Create a fetch mock that records requests and returns supplied responses. */
export const mockFetch = (
  respond: (req: RecordedRequest) => Response | Promise<Response>,
): { fetch: Fetch; requests: RecordedRequest[] } => {
  const requests: RecordedRequest[] = [];
  const fetch: Fetch = async (url, init) => {
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    const req = { url, init, body };
    requests.push(req);
    return respond(req);
  };
  return { fetch, requests };
};

export const json = (data: unknown, init: ResponseInit = {}): Response =>
  new Response(JSON.stringify(data), {
    ...init,
    headers: { "content-type": "application/json", ...init.headers },
  });
