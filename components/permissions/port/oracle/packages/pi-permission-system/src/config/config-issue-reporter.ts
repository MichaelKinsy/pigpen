/**
 * config-issue-reporter.ts — Tell the operator what is wrong with their config,
 * once per issue, for as long as it is wrong.
 *
 * `ConfigStore` loads and answers; this decides whether the operator has
 * already heard it. Splitting the two is what fixes #933: the store was primed
 * at factory time, with no context to notify through, and recorded the warning
 * as delivered anyway — so the identical warning at `session_start` was deduped
 * away and every config issue reached the debug log alone.
 *
 * Delivery goes through the injected `warn` seam rather than a ctx parameter.
 * That seam is `SessionLogger.warn`, which reaches the operator through
 * `PermissionSession.notify` and the context the session owns — the same path
 * every other session-lifecycle warning in this package takes. A reporter
 * cannot be handed a context that does not exist, which is the whole point.
 *
 * The latch is per issue and the delivery is per report: a config with three
 * detector hits produces one notification, and a later report announces only
 * what is new. `reported` is *replaced* on every report rather than added to,
 * so an issue the operator fixed and then reintroduced is announced again —
 * which preserves the clearing behavior the store's single-string dedupe had,
 * at per-issue granularity.
 *
 * Driven at both moments the operator is reachable: `session_start`, and every
 * `before_agent_start` (the config is re-read there, so an issue created
 * mid-session is caught on the next turn).
 */

/** The config seam this reads (ISP): the issues current as of the last load. */
export interface ConfigIssueSource {
  getConfigIssues(): readonly string[];
}

/** The log seam this writes (ISP): one operator-facing warning. */
export interface ConfigIssueWarner {
  warn(message: string): void;
}

/** The seam the session-start and turn-prep handlers drive. */
export interface ConfigIssueReporting {
  report(): void;
}

export class ConfigIssueReporter implements ConfigIssueReporting {
  private reported: ReadonlySet<string> = new Set();

  constructor(
    private readonly source: ConfigIssueSource,
    private readonly log: ConfigIssueWarner,
  ) {}

  report(): void {
    const current = this.source.getConfigIssues();
    const unreported = current.filter((issue) => !this.reported.has(issue));
    if (unreported.length > 0) {
      this.log.warn(unreported.join("\n"));
    }
    this.reported = new Set(current);
  }
}
