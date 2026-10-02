export const REQUEST_ID_HEADER = "x-typesafe-request-id";

export const requestIdFrom = (headers: Headers): string | undefined =>
  headers.get(REQUEST_ID_HEADER) ?? undefined;

/** Parsed data with its HTTP response and request ID. */
export interface WithResponse<T> {
  /** The parsed response body. */
  data: T;
  /** The HTTP response, with its body consumed by parsing. */
  response: Response;
  /** Request ID from `x-typesafe-request-id`, or `undefined` when absent. */
  requestId: string | undefined;
}

/**
 * A promise for the parsed result with access to the HTTP response.
 *
 * Non-2xx responses reject with an `APIError`, including through `asResponse()`.
 */
export class APIPromise<T> extends Promise<T> {
  readonly #responsePromise: Promise<Response>;
  readonly #parseResponse: (response: Response) => Promise<T>;
  #parsed: Promise<T> | undefined;

  constructor(
    responsePromise: Promise<Response>,
    parseResponse: (response: Response) => Promise<T>,
  ) {
    // The inherited promise is never used; all consumers go through `parse()`.
    super((resolve) => resolve(undefined as T));
    this.#responsePromise = responsePromise;
    this.#parseResponse = parseResponse;
  }

  /**
   * Resolves to the raw `Response` without parsing the body. SDK requests buffer the full
   * body under the request timeout before handoff; reading it afterwards is caller-owned.
   * The caller owns the body; don't also `await` the parsed result on the same promise.
   */
  asResponse(): Promise<Response> {
    return this.#responsePromise;
  }

  /** Return the parsed result, HTTP response, and request ID. */
  async withResponse(): Promise<WithResponse<T>> {
    const [data, response] = await Promise.all([this.#parse(), this.#responsePromise]);
    return { data, response, requestId: requestIdFrom(response.headers) };
  }

  /** Transform the parsed result, sharing the HTTP response and a single body parse. */
  map<U>(fn: (data: T) => U): APIPromise<U> {
    return new APIPromise<U>(this.#responsePromise, () => this.#parse().then(fn));
  }

  #parse(): Promise<T> {
    this.#parsed ??= this.#responsePromise.then(this.#parseResponse);
    return this.#parsed;
  }

  // biome-ignore lint/suspicious/noThenProperty: this is a Promise subclass; overriding then is the point
  override then<TResult1 = T, TResult2 = never>(
    onfulfilled?: ((value: T) => TResult1 | PromiseLike<TResult1>) | null,
    onrejected?: ((reason: unknown) => TResult2 | PromiseLike<TResult2>) | null,
  ): Promise<TResult1 | TResult2> {
    return this.#parse().then(onfulfilled, onrejected);
  }

  override catch<TResult = never>(
    onrejected?: ((reason: unknown) => TResult | PromiseLike<TResult>) | null,
  ): Promise<T | TResult> {
    return this.#parse().catch(onrejected);
  }

  override finally(onfinally?: (() => void) | null): Promise<T> {
    return this.#parse().finally(onfinally);
  }
}
