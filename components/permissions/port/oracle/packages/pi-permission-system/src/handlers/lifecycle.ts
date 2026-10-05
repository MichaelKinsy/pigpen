import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { AskDialogRelease } from "#src/authority/ask-dialog-queue";
import type { ConfigIssueReporting } from "#src/config/config-issue-reporter";
import { PERMISSION_SYSTEM_STATUS_KEY } from "#src/config/status";
import type { DecisionSummaryWriter } from "#src/logging/decision-audit";
import type { SessionLogger } from "#src/logging/session-logger";
import type { PermissionResolver } from "#src/policy/permission-resolver";
import type { ServiceLifecycle } from "#src/service/service-lifecycle";
import type { PermissionSession } from "#src/session/permission-session";

/** Minimal subset of SessionStartEvent used by this handler. */
interface SessionStartPayload {
  reason: string;
}

/** Minimal subset of ResourcesDiscoverEvent used by this handler. */
interface ResourcesDiscoverPayload {
  reason: string;
}

/**
 * Shown when project config is skipped because the project is untrusted, so the
 * reduced-scope state is never silent (#644). Exported for assertion in tests.
 */
/**
 * What a permission request still open at shutdown is answered with.
 *
 * Reaches the agent as the block reason and the review log as the decider's
 * `reason`, so both name the same thing (#726).
 */
export const SESSION_ENDED_REASON =
  "The session ended before this permission request was answered";

export const UNTRUSTED_PROJECT_MESSAGE =
  "pi-permission-system: project is not trusted — skipping project-scoped " +
  "permission configuration. Only global policy applies. Grant project trust " +
  "to load this project's permission rules.";

/**
 * Handles session lifecycle events: start, reload, and shutdown.
 *
 * Constructor deps:
 * - `session` — encapsulates all mutable session state and lifecycle operations
 * - `resolver` — owns permission-query surface: `getConfigIssues`
 * - `serviceLifecycle` — owns the process-global service publication;
 *   `activate` publishes (skipped for registered subagent children) and emits
 *   the ready event; `teardown` unsubscribes all session listeners and unpublishes
 * - `logger` — injected directly; replaces the former `session.logger` reach-through
 * - `audit` — per-session decision counters; its summary is written on shutdown
 * - `configIssues` — reports what is wrong with the extension config, latched
 *   per issue; driven here and on every turn, so an issue already on disk when
 *   the session opens is shown rather than swallowed (#933)
 * - `dialogs` — the session's ask queue, released on shutdown so a gate waiting
 *   on a dialog nobody can answer any more is unblocked (#965)
 */
export class SessionLifecycleHandler {
  constructor(
    private readonly session: PermissionSession,
    private readonly resolver: PermissionResolver,
    private readonly serviceLifecycle: ServiceLifecycle,
    private readonly logger: SessionLogger,
    private readonly audit: DecisionSummaryWriter,
    private readonly configIssues: ConfigIssueReporting,
    private readonly dialogs: AskDialogRelease,
  ) {}

  handleSessionStart(
    event: SessionStartPayload,
    ctx: ExtensionContext,
  ): Promise<void> {
    const projectTrusted = ctx.isProjectTrusted();
    // Reset first: it activates the session, binding the context that
    // `PermissionSession.notify` — and therefore `logger.warn` — delivers
    // through. A refresh before activation has no UI to report into, which is
    // how a config warning present at session start went unseen (#933).
    this.session.resetForNewSession(ctx, projectTrusted);
    this.session.refreshConfig(ctx, projectTrusted);
    this.session.logResolvedConfigPaths();
    // The config was just re-read above, and the session is activated, so a
    // warning has a UI to reach.
    this.configIssues.report();
    if (!projectTrusted) {
      this.warnProjectUntrusted(ctx, "session_start");
    }

    const agentName = this.session.resolveAgentName(ctx);
    const policyIssues = this.resolver.getConfigIssues(agentName ?? undefined);
    for (const issue of policyIssues) {
      this.logger.warn(issue);
    }

    if (event.reason === "reload") {
      this.logger.debug("lifecycle.reload", {
        triggeredBy: "session_start",
        reason: event.reason,
        cwd: ctx.cwd,
      });
    }

    // Publish the process-global service now that a ctx (and therefore the
    // session id) is available, so an in-process subagent child can be
    // identified and excluded. Emitting ready here keeps the
    // service-resolvable-when-ready ordering contract.
    this.serviceLifecycle.activate(ctx);
    return Promise.resolve();
  }

  handleResourcesDiscover(
    event: ResourcesDiscoverPayload,
    ctx: ExtensionContext,
  ): Promise<void> {
    if (event.reason !== "reload") {
      return Promise.resolve();
    }

    const projectTrusted = ctx.isProjectTrusted();
    this.session.reload(projectTrusted);
    if (!projectTrusted) {
      this.warnProjectUntrusted(ctx, "resources_discover");
    }
    this.logger.debug("lifecycle.reload", {
      triggeredBy: "resources_discover",
      reason: event.reason,
      cwd: this.session.getRuntimeContext()?.cwd ?? null,
    });
    return Promise.resolve();
  }

  /**
   * Record the project-trust skip in the review log and surface a loud warning
   * to the user, so the reduced (global-only) scope is never silent (#644).
   */
  private warnProjectUntrusted(
    ctx: ExtensionContext,
    phase: "session_start" | "resources_discover",
  ): void {
    this.logger.review("project_trust.skipped", { cwd: ctx.cwd, phase });
    this.logger.warn(UNTRUSTED_PROJECT_MESSAGE);
  }

  handleSessionShutdown(): Promise<void> {
    const ctx = this.session.getRuntimeContext();
    if (ctx) {
      ctx.ui.setStatus(PERMISSION_SYSTEM_STATUS_KEY, undefined);
    }
    this.audit.writeSummary(this.logger);
    // Ahead of the session shutdown that stops forwarding: a drain awaiting a
    // forwarded ask can only write its response file once that ask is settled.
    this.dialogs.releaseAll(SESSION_ENDED_REASON);
    this.session.shutdown();
    this.serviceLifecycle.teardown();
    return Promise.resolve();
  }
}
