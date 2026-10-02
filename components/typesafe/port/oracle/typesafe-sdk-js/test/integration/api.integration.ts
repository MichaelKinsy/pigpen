import { describe, expect, it } from "vitest";
import {
  type APIError,
  AuthenticationError,
  BadRequestError,
  choice,
  ENV,
  noul,
  score,
  TypeSafeClient,
  TypeSafeError,
  UnprocessableEntityError,
} from "../../src";

/** Live API tests with raw response output; skipped unless `TYPESAFE_API_KEY` is set. */
const describeLive = process.env[ENV.apiKey] ? describe : describe.skip;

const show = (label: string, value: unknown): void => {
  console.log(`\n--- ${label} ---\n${JSON.stringify(value, null, 2)}`);
};

const sum = (values: Record<string, number>): number =>
  Object.values(values).reduce((a, b) => a + b, 0);

describeLive("live API", () => {
  const client = new TypeSafeClient({ logLevel: "info", timeout: 120_000 });

  const ticket = {
    subject: "Charged twice this month",
    body: "I see two charges of $49 on my card for August. I only have one account. Please fix this ASAP.",
  };

  it("lists models", async () => {
    const { data: models, requestId } = await client.models.list().withResponse();
    show("GET /v1/models", { requestId, count: models.length, first: models[0] });
    expect(requestId).toMatch(/^req_/);

    expect(Array.isArray(models)).toBe(true);
    expect(models.length).toBeGreaterThan(0);
    for (const m of models) {
      expect(typeof m.name).toBe("string");
      expect(typeof m.description).toBe("string");
      expect(typeof m.release_date).toBe("string");
    }
  });

  it("answers noul, choice, and score-as-list questions", async () => {
    const { data, requestId } = await client
      .systemOne({
        state: ticket,
        questions: {
          isBilling: noul("Is this ticket about billing?"),
          sentiment: choice("What is the customer's tone?", {
            calm: null,
            frustrated: null,
            angry: null,
          }),
          urgency: score("How urgent is this ticket?", ["can wait", "this week", "today"]),
        },
      })
      .withResponse();
    show("POST /v1/systemone", { requestId, data });

    expect(typeof data.model).toBe("string");
    expect(data.usage.input_tokens).toBeGreaterThan(0);
    expect(data.usage.output_tokens).toBeGreaterThanOrEqual(0);

    const { isBilling, sentiment, urgency } = data.answers;
    expect(isBilling.type).toBe("noul");
    expect(isBilling.noul).toBeGreaterThanOrEqual(0);
    expect(isBilling.noul).toBeLessThanOrEqual(1);

    expect(sentiment.type).toBe("choice");
    expect(["angry", "calm", "frustrated"]).toContain(sentiment.choice);
    expect(Object.keys(sentiment.probabilities).sort()).toEqual(["angry", "calm", "frustrated"]);
    expect(sum(sentiment.probabilities)).toBeCloseTo(1, 1);
    expect(sentiment.confidence).toBeGreaterThanOrEqual(0);

    expect(urgency.type).toBe("score");
    expect(urgency.score).toBeGreaterThanOrEqual(0);
    expect(urgency.score).toBeLessThanOrEqual(2);
    expect(urgency.legend).toEqual({ 0: "can wait", 1: "this week", 2: "today" });
    expect(Object.keys(urgency.probabilities).sort()).toEqual(["0", "1", "2"]);
    expect(sum(urgency.probabilities)).toBeCloseTo(1, 1);
  });

  it("accepts rich descriptions and one-sided noul criteria", async () => {
    const data = await client.systemOne({
      state: ticket,
      questions: {
        duplicate: noul("Is the customer reporting a duplicate charge?", {
          true: { meaning: "the same amount charged more than once", examples: ["billed twice"] },
        }),
        tone: choice("Tone?", {
          calm: { summary: "measured", examples: ["please look into this"] },
          upset: null,
        }),
      },
    });
    show("rich descriptions", data.answers);
    expect(data.answers.duplicate.noul).toBeGreaterThanOrEqual(0);
    expect(Object.keys(data.answers.tone.probabilities).sort()).toEqual(["calm", "upset"]);
  });

  it("rejects a bad API key with AuthenticationError", async () => {
    const bad = new TypeSafeClient({
      apiKey: "not-a-real-key",
      retry: { maxRetries: 0 },
      logLevel: "off",
    });
    const err = await bad.models.list().catch((e: unknown) => e);
    show("bad key", { name: (err as Error).name, message: (err as Error).message });
    expect(err).toBeInstanceOf(AuthenticationError);
  });

  it("rejects an unknown model with a readable BadRequestError", async () => {
    const err = await client
      .systemOne(
        { state: "hello", questions: { q: noul("Is this a greeting?") }, model: "no-such-model" },
        { retry: { maxRetries: 0 } },
      )
      .catch((e: unknown) => e);
    show("unknown model", { name: (err as Error).name, message: (err as Error).message });
    expect(err).toBeInstanceOf(BadRequestError);
    expect((err as APIError).message).toBe("400 Unknown model: no-such-model");
    expect((err as APIError).requestId).toMatch(/^req_/);
  });

  it("surfaces server-side validation errors readably", async () => {
    // A description type the SDK does not validate but the API rejects, to see how a 422 renders.
    const malformed = { q: score("?", [123 as unknown as string, "ok"]) };
    const err = await client
      .systemOne({ state: "x", questions: malformed }, { retry: { maxRetries: 0 } })
      .catch((e: unknown) => e);
    show("422", { name: (err as Error).name, message: (err as Error).message });
    expect(err).toBeInstanceOf(UnprocessableEntityError);
    expect((err as Error).message).toMatch(/^422 questions\.q\.score\.criteria\.0/);
  });

  it("catches shapes the API would reject before sending", () => {
    expect(() => client.systemOne({ state: "x", questions: {} })).toThrow(TypeSafeError);
    // biome-ignore lint/suspicious/noExplicitAny: deliberately malformed, as a JS caller might send
    const empty: any = [];
    expect(() => client.systemOne({ state: "x", questions: { q: score("?", empty) } })).toThrow(
      TypeSafeError,
    );
  });
});
