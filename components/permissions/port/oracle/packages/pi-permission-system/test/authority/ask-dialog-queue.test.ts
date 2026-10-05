import { beforeEach, describe, expect, it, vi } from "vitest";
import { AskDialogQueue } from "#src/authority/ask-dialog-queue";

// ── Helpers ─────────────────────────────────────────────────────────────────

/** The fallback every ask in this file settles as when the queue releases it. */
function released(reason: string): string {
  return `released: ${reason}`;
}

/**
 * One presentation the test drives by hand: it records when it was invoked and
 * settles only when the test says so, which is what makes "the second ask has
 * not been presented yet" observable.
 */
function makeAsk(label: string, presentations: string[]) {
  const { promise, resolve, reject } = Promise.withResolvers<string>();
  return {
    present: vi.fn(() => {
      presentations.push(label);
      return promise;
    }),
    settle: () => {
      resolve(label);
    },
    fail: (error: unknown) => {
      reject(error);
    },
  };
}

/** Drain every pending microtask, so a chained continuation has run. */
function settleMicrotasks(): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, 0);
  });
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe("AskDialogQueue", () => {
  let queue: AskDialogQueue;
  let presentations: string[];

  beforeEach(() => {
    queue = new AskDialogQueue();
    presentations = [];
  });

  describe("run", () => {
    it("presents an ask admitted to an idle queue", async () => {
      const ask = makeAsk("a", presentations);

      const decision = queue.run(ask.present, released);

      expect(ask.present).not.toHaveBeenCalled();
      ask.settle();
      await expect(decision).resolves.toBe("a");
      expect(presentations).toEqual(["a"]);
    });

    it("does not present a second ask until the first settles", async () => {
      const first = makeAsk("a", presentations);
      const second = makeAsk("b", presentations);

      const firstDecision = queue.run(first.present, released);
      const secondDecision = queue.run(second.present, released);
      await settleMicrotasks();

      expect(presentations).toEqual(["a"]);

      first.settle();
      await expect(firstDecision).resolves.toBe("a");
      await settleMicrotasks();

      expect(presentations).toEqual(["a", "b"]);

      second.settle();
      await expect(secondDecision).resolves.toBe("b");
    });

    it("presents admitted asks in admission order", async () => {
      const first = makeAsk("a", presentations);
      const second = makeAsk("b", presentations);
      const third = makeAsk("c", presentations);

      const decisions = [
        queue.run(first.present, released),
        queue.run(second.present, released),
        queue.run(third.present, released),
      ];

      await settleMicrotasks();
      expect(presentations).toEqual(["a"]);

      first.settle();
      await settleMicrotasks();
      expect(presentations).toEqual(["a", "b"]);

      second.settle();
      await settleMicrotasks();
      expect(presentations).toEqual(["a", "b", "c"]);

      third.settle();
      await expect(Promise.all(decisions)).resolves.toEqual(["a", "b", "c"]);
    });

    it("rejects only the failing ask and presents the next one", async () => {
      const failing = makeAsk("a", presentations);
      const next = makeAsk("b", presentations);

      const failingDecision = queue.run(failing.present, released);
      const nextDecision = queue.run(next.present, released);
      await settleMicrotasks();

      failing.fail(new Error("dialog exploded"));
      await expect(failingDecision).rejects.toThrow("dialog exploded");
      await settleMicrotasks();

      expect(presentations).toEqual(["a", "b"]);

      next.settle();
      await expect(nextDecision).resolves.toBe("b");
    });
  });

  describe("releaseAll", () => {
    it("settles a queued ask without ever presenting it", async () => {
      const head = makeAsk("a", presentations);
      const queued = makeAsk("b", presentations);

      const headDecision = queue.run(head.present, released);
      const queuedDecision = queue.run(queued.present, released);
      await settleMicrotasks();

      queue.releaseAll("the session ended");
      await settleMicrotasks();

      await expect(queuedDecision).resolves.toBe("released: the session ended");
      await expect(headDecision).resolves.toBe("released: the session ended");
      expect(queued.present).not.toHaveBeenCalled();
      expect(presentations).toEqual(["a"]);
    });

    it("never presents a released ask whose turn arrives afterwards", async () => {
      const head = makeAsk("a", presentations);
      const queued = makeAsk("b", presentations);

      const headDecision = queue.run(head.present, released);
      const queuedDecision = queue.run(queued.present, released);
      await settleMicrotasks();
      queue.releaseAll("the session ended");

      // The stale dialog is still on screen after a release, so answering it
      // advances the queue to an ask that has already been answered.
      head.settle();
      await settleMicrotasks();

      expect(queued.present).not.toHaveBeenCalled();
      expect(presentations).toEqual(["a"]);
      await expect(headDecision).resolves.toBe("released: the session ended");
      await expect(queuedDecision).resolves.toBe("released: the session ended");
    });

    it("discards a presentation that resolves after its ask was released", async () => {
      const head = makeAsk("a", presentations);

      const decision = queue.run(head.present, released);
      await settleMicrotasks();

      queue.releaseAll("the session ended");
      head.settle();

      await expect(decision).resolves.toBe("released: the session ended");
    });

    it("presents an ask admitted after a release", async () => {
      const abandoned = makeAsk("a", presentations);
      const abandonedDecision = queue.run(abandoned.present, released);
      await settleMicrotasks();
      queue.releaseAll("the session ended");

      const later = makeAsk("b", presentations);
      const laterDecision = queue.run(later.present, released);
      await settleMicrotasks();

      expect(presentations).toEqual(["a", "b"]);

      later.settle();
      await expect(laterDecision).resolves.toBe("b");
      await expect(abandonedDecision).resolves.toBe(
        "released: the session ended",
      );
    });
  });
});
