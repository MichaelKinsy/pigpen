# Scheduler implementation

Use a dedicated persisted scheduler with the GoalService atomic mutation boundary; finish buffered turns before authorizing declarations or dispatch. All extension-generated messages pass a single authorize/claim/send gate. Persist generation-tagged claim before delivery, consume it before execution, and fail closed after ambiguous recovery. Runtime timers supply readiness polling/network delay only; they must not authorize work.

Enforce any retained wait deadline independently of scheduler phase in both scheduling and the atomic claim gate. Recovery and repair retain the wait, so neither can bypass expiry by changing phase to ready. Test expiry before recovery/repair scheduling and expiry while a ready dispatch waits for host readiness.

Track actual agent_start through final settlement (not before_agent_start). Preserve a logical execution across internal retries, invalidate a declared disposition if another model turn occurs, and keep checkpoint admission separate from normal user/producer takeover. A matching wait signal can be recorded before settlement, but delivery occurs only afterward. Wait checks retain the same wait identity and finite allowance. Resume explicitly resets epoch and consumption.

Keep lifecycle GoalStatus separate from a versioned scheduler field. Strictly validate persisted scheduler data; corrupt scheduling state requires resume rather than resetting counters. Read legacy records without scheduling them. Render bounded scheduler summaries through existing surfaces. Add maxAutonomousRuns to layered settings and remove the unreleased cooldown entirely.

Accept maxAutonomousRuns=0 in strict parsing and the settings menu. Preserve zero through persistence and resolution so a project can override an enabled global allowance; unsetting the project value restores inheritance without resetting consumption. Summaries explicitly label zero as disabled. Verify parsing, persistence, settings UI, inheritance and dispatch cancellation with used allowance retained.

Keep an absent resolved limit as undefined, meaning unlimited execution; do not coerce it to zero or persist Infinity. Dispatch availability accepts undefined or an unspent numeric limit, while kickoff/resume only refuse explicit zero. Continue counting uncapped dispatches so adding a limit does not renew usage. Render absent limits as unlimited in all summaries/settings. Default execution still uses the same claim gate, repair limit and bounded waits; extend real SDK coverage to the no-settings path.

Keep README.md at its pre-review version per user steering. Record these follow-up semantics in specs, changelog and the PR description.

Validation: pure scheduler fake-clock tests plus real GoalService and real SDK cases for tool independence, claim/cancellation races, missing decisions, owned restoration, external events, dynamic allowance, retries and compaction. Run full checks and measure extra model-context cost, updating baseline only for reviewed intentional contract overhead.

Use a compact disabled-mode policy and conditional enabled guidance, with maxAutonomousRuns !== 0 in the prompt cache key. Omitted and positive limits both receive ready/wait guidance; zero does not. Keep the scheduling schema discoverable without reload; avoid dynamic tool-registration churn. Deduplicate tool descriptions and system instructions, retain validation at the mutation boundary, and remeasure context.
