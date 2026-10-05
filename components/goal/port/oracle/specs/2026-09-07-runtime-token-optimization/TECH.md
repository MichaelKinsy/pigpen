# Implementation

1. Correct SDK system/tool/message measurement and fixtures; capture an isolated before baseline before runtime changes. Measure executor and child request surfaces separately, cross-check provider payloads, and retain character estimates distinct from actual usage.
2. Incrementally index ledger activity/audit/lifecycle/Oracle state; avoid history copies and scans on hot reads. Preserve ordering/deduplication and rebuild versioned checkpoints. Cache task derivations by content and reuse read-only pool snapshots.
3. Validate ordered task batches on a clone, commit through GoalService once, preserve per-task ledger events, and check the original disk revision at flush. Audit only successfully persisted state.
4. Compact stable guidance and current state; bounded excerpts retain retrieval markers and critical policy. Add get_goal section/task/cursor paging (4000 content chars); preserve verbose/include_history and single-task calls. Tool profiles follow valid lifecycle states and preserve drafting and host tools.
5. Extend deterministic coverage and benchmarks; run checks and paired live smoke evaluation with request reservations under US$5. Update documentation and report measured gains and remaining limits.

Retrieval cursors bind goal id, selected section/task, and content fingerprint; changed source returns a restart instruction. Full content is never modified by paging. Batches execute in input order, so child completion may precede parent completion; any invalid update rejects the whole batch.

## Live evaluation steering

Use `opencode-go/deepseek-v4-flash`, the authenticated OpenCode connection (`opencode` has no configured authentication). Price worst-case requests before dispatch and only begin a pair if its maximum fits the remaining allowance after earlier attempts. Bound each run to eight requests so the complete pair fits even when reserving the entire model context window on every call. Retain model-specific results and incomplete coverage; never reset the US$5 campaign cap when switching models.
