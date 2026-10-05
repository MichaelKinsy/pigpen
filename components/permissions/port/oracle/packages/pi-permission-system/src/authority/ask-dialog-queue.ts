/**
 * The shutdown-facing slice of {@link AskDialogQueue}: settle everything this
 * session still owes an answer for.
 *
 * Narrow on purpose (ISP) — the lifecycle handler releases asks and never
 * admits one.
 */
export interface AskDialogRelease {
  releaseAll(reason: string): void;
}

/**
 * The admission-facing slice of {@link AskDialogQueue}: present this ask once
 * every earlier one has settled.
 *
 * The terminal that shows a dialog admits asks and never releases them, and
 * depending on the slice rather than the class keeps the queue's private state
 * out of its dependency type.
 */
export interface AskDialogAdmission {
  run<T>(
    present: () => Promise<T>,
    released: (reason: string) => T,
  ): Promise<T>;
}

/**
 * Serializes the human-facing presentations one session owns.
 *
 * Pi's inline `ctx.ui.custom` slot holds one component: a second presentation
 * clears the editor container and mounts over the first, whose promise then
 * never settles and whose component is never disposed. This extension raises
 * asks from two independent tasks — the gate's local ask and the forwarded ask
 * a poll tick drains — so without a queue whichever arrives second silently
 * strands the first, and a forwarded ask stranded that way holds the
 * forwarding inbox closed behind it (#965).
 *
 * The queue cannot see the host's slot, so its contract is only that it never
 * lets two of *its own* presentations overlap. A presentation another
 * extension mounts is outside its evidence; upstream declined to arbitrate
 * globally (earendil-works/pi#7007), so that case stays open by design.
 *
 * Logger-free, following `transient-fs-retry.ts`: `PermissionPrompter` already
 * brackets each ask with review entries, so a queue wait reads as the gap
 * between them and a released ask reads as its terminal entry.
 */
export class AskDialogQueue implements AskDialogAdmission, AskDialogRelease {
  /** Settles once every ask admitted so far has been presented or released. */
  private tail: Promise<void> = Promise.resolve();
  /**
   * The asks that still owe their caller a value, queued and in-flight alike.
   * Typed as the release capability rather than `AdmittedAsk<unknown>` so the
   * set does not have to name a value type it never reads.
   */
  private readonly outstanding = new Set<{ release(reason: string): void }>();

  /**
   * Admit an ask, presenting it only once every earlier one has settled.
   *
   * `released` is the caller's own fallback, supplied at admission: the queue
   * never constructs a decision, because a decision is stamped at the site
   * that decides and the queue does not know what it is presenting.
   */
  run<T>(
    present: () => Promise<T>,
    released: (reason: string) => T,
  ): Promise<T> {
    const ask = new AdmittedAsk(released);
    this.outstanding.add(ask);
    this.tail = this.tail.then(async () => {
      // A release while this ask waited its turn means nothing may render:
      // presenting it now would put a dialog on screen for a decision that
      // has already been answered.
      if (ask.isSettled) {
        return;
      }
      try {
        ask.settle(await present());
      } catch (error) {
        ask.fail(error);
      } finally {
        this.outstanding.delete(ask);
      }
    });
    return ask.decision;
  }

  /**
   * Settle every outstanding ask with its own released value.
   *
   * The tail is reset rather than chained on, because the in-flight ask's
   * presentation is still pending in the host and nothing would ever settle
   * it — a session that keeps running must not queue behind a dialog nobody
   * can answer.
   */
  releaseAll(reason: string): void {
    const releasing = [...this.outstanding];
    this.outstanding.clear();
    this.tail = Promise.resolve();
    for (const ask of releasing) {
      ask.release(reason);
    }
  }
}

/**
 * One admitted ask and the promise its caller is holding.
 *
 * A release while a presentation is in flight leaves the host's own promise
 * pending, and it may resolve later; that late answer is discarded because the
 * caller's promise has already settled. `settled` is therefore read rather
 * than enforced — it is what lets the queue skip presenting an ask that was
 * released while it waited its turn.
 */
class AdmittedAsk<T> {
  readonly decision: Promise<T>;
  private settled = false;
  private readonly resolve: (value: T) => void;
  private readonly reject: (error: unknown) => void;

  constructor(private readonly released: (reason: string) => T) {
    const { promise, resolve, reject } = Promise.withResolvers<T>();
    this.decision = promise;
    this.resolve = resolve;
    this.reject = reject;
  }

  get isSettled(): boolean {
    return this.settled;
  }

  settle(value: T): void {
    this.settled = true;
    this.resolve(value);
  }

  fail(error: unknown): void {
    this.settled = true;
    this.reject(error);
  }

  release(reason: string): void {
    this.settle(this.released(reason));
  }
}
