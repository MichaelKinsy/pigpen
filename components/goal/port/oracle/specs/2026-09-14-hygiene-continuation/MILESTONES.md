## Superseded cooldown implementation (historical)

Reviewed #55 and the referenced fork. Selected a nonzero default to protect existing users, run-scoped classification to preserve multi-turn work, and existing message lifecycle for producer wakeups.

Implemented run-scoped cooldown and removed the earlier turn_end scheduling path. Added project/global/environment settings, menu/report support, strict bounds and cache key participation, user notifications, README and changelog.

Regression work exposed permissive legacy string parsing (new setting now validates full decimal input), an obsolete pre-settlement continuation assertion, settings menu row-count changes, and test expectations for cache refresh/provenance. Updated the tests to follow existing settings cache invalidation and source naming.

Real SDK worker now exercises repeated read-only runs, delayed automatic continuation, immediate user input and triggerTurn/followUp producer delivery without duplicate checkpoints. The worker passes with eight provider requests and no model credentials. Fake timers cover 300000 ms boundaries, deduplication, cancellation, readiness polling, explicit kickoff, no-tool stopping, run reset and the zero override. Context gate, six SDK payload checks, NAF gate, production audit and package dry run pass.

Final local validation: 974 tests pass with no skips; test-manifest self-check and 927 unit tests pass. TypeScript, ESLint and git diff whitespace checks pass. No dependency or package version change. Prepared for PR review, not merge/publication.

## Wholesale revision
User rejected tool-based classification and approved explicit ready/wait outcomes, bounded checks, and configurable allowance with no default automatic execution. Agents may configure the ceiling; edits never reset consumption. Replaced PRODUCT and TECH before implementation.


Implemented a versioned scheduler separate from goal lifecycle, with durable GoalService claims, session ownership and generation checks, configurable consumed-run accounting, explicit ready/wait declarations, one-shot repair, and finite polling/event waits. Removed tool classification from scheduling and removed the cooldown setting. Creation and explicit resume are the only allowance-period boundaries. Terminal lifecycle operations and incoming user/producer work invalidate scheduling intent.

SDK validation exposed that custom-message entry skips before_agent_start. Execution tracking now uses agent_start through agent_settled; custom runs receive current scheduling state without repeating an inherited objective block. The actual SDK fixture makes eight provider requests over an initial user execution and four extension dispatches, including mixed write/read/tool work, a waiting producer wake, and one unsuccessful repair. Replaying a historical dispatch produces no ninth request. Waiting adds no requests. Existing real SDK producer and network-recovery suites remain covered.

Race testing found that an old owner's readiness callback could otherwise claim a newer owner's decision. Readiness now carries its original generation and atomically rejects changed ownership/generation without pausing the new owner's goal. Persistence failure never returns terminate/success. Claims survive failed delivery as spent allowance and are not replayed after reload. Fake-clock tests cover early/duplicate wakes, deadlines, counters, settings edits, ownership transfer, subsequent model work, idle readiness, and active-time suspension.

Intentional context drift initially failed context:gate. Added before/after evidence and CONTEXT.md rationale, then updated the deterministic baseline without weakening any invariant. The new decision schema and instructions cost context; no progress or token-saving claim is made for them. Static historical benchmark gates are regression checks, not new end-to-end performance measurements.


Final lifecycle review preserved the admitted action in running-state context so contract-repair instructions reach the actual provider. The consumed claim remains unusable for another dispatch. Explicit resume during host work now waits for settlement and retains kickoff intent unless superseded by subsequent model work. Added regressions for both.

Added an actual SDK retry/compaction variant: ten provider requests (eight work requests, one transient failure, one summary), one native retry, four consumed extension runs. The first compaction attempt correctly rejected the fixture as too short; adding a persisted turn boundary makes it compactable. Compaction preserves the live wait and does not consume allowance.

User requested low context overhead and off-by-default operation. Updated PRODUCT then TECH, shortened duplicate policy/schema descriptions, and made detailed guidance conditional on configured allowance. Default mode remains discoverable by agents through a short settings instruction. Typical default overhead fell from 1,833 to 966 characters (about 242 estimated tokens); no tool-schema hiding or reload is needed to enable it. A cache regression verifies settings changes select the right policy.

Final validation: 982 tests pass, zero skips/failures; manifest self-check and 934 unit tests pass. TypeScript, ESLint, context gate (24 fixtures), provider cross-check (six real SDK payloads), NAF/runtime-token/comprehensive benchmark gates, ranking tests, production audit (zero vulnerabilities), package dry run and whitespace checks pass. No package version or dependency changes. PR remains open; no merge or publication.

## PR review fixes

User approved the review findings. Updated PRODUCT then TECH to require outstanding wait deadlines for recovery/repair dispatches and an explicit project-level zero allowance. The review reproduced a network retry after a polling wait expired and a project zero falling back to global 20.

Moved retained-wait deadline checks outside the waiting-phase branches in scheduling and the atomic dispatch gate. Recovery and repair cannot bypass expiry by transitioning to ready, including when the host delays delivery. Added four scheduling/delivery deadline regressions and a real runtime-backoff regression; denied dispatches leave consumption unchanged.

Parsing, persistence and the settings menu now accept zero, with disabled labels in settings reports and scheduler summaries. Added regressions for the menu, project-over-global zero, pending-dispatch cancellation, denied resume, restored inheritance and retained consumption. Disabled prompt guidance also remains selected for zero. The product spec explains that an agent-editable allowance is not a hard spending cap.

The first context gate run detected the intentional three-character prompt clarification from “is set” to “> 0”. Remeasured all 24 fixtures, updated the baseline/CONTEXT-AFTER and documented the 42-character aggregate reduction in CONTEXT.md; no gate invariant was relaxed.

Validation: all 989 tests pass (including both real SDK scheduler fixtures), with zero failures/skips. TypeScript, ESLint, manifest self-check (941 unit tests), context gate, six real SDK provider-payload cross-checks and git diff whitespace checks pass. No package version or dependency change.

User subsequently requested no README changes. Updated PRODUCT then TECH and restored README.md exactly to the pre-review PR version. Code and tests are unchanged by this documentation-only correction.

## Automatic continuation by default

User approved restoring automatic continuation by default while retaining the scheduler in every mode. Updated PRODUCT then TECH: absent at both settings scopes now means unlimited automatic runs, zero disables them, and a positive value caps them. The README remains unchanged as requested.

Kept the resolved limit optional throughout dispatch availability, kickoff and resume; only explicit zero disables execution. Uncapped runs still increment durable consumption, so adding/removing a ceiling never renews usage. Settings, status and tool summaries show unlimited explicitly. Default and capped prompts both include ready/wait guidance; zero keeps the shorter disabled guidance. Removed the tool description's obsolete requirement for a configured allowance.

Added default creation/resume, uncapped consumption and dynamic-limit tests, plus an actual SDK no-settings variant covering ready continuation, event waiting, one repair, and stale checkpoint rejection. The first full suite found one historical empty-run expectation that assumed absent settings disabled scheduling. Updated it to exercise admission of exactly one repair, then verify pause without further dispatches. Network-backoff deadline coverage also runs without a cap.

Remeasured context after changing the default policy and shortening the tool description. All 24 fixtures retain their semantic counts; the active 10-task fixture adds 1,141 characters versus pre-PR main, and total serialized size is 261,018 characters. Updated CONTEXT.md, CONTEXT-AFTER.json and the exact baseline without relaxing invariants.

Validation: all 993 tests pass, zero failures/skips; TypeScript, ESLint, manifest self-check (944 unit tests), context gate, six real SDK payload cross-checks and whitespace checks pass. README.md matches the pre-review PR exactly. No version/dependency change, merge or release.
