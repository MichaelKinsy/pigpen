# TECH — program-level notes

Delivery graph followed plan §5 exactly:
preflight → A(#30) → B(#27) → C(#29)+0.28.0 → D(measurement) → E(dedup) →
F(separation) → G(Oracle #26)+0.29.0 → final report.

Non-negotiables honored throughout (plan §3): maintainer-owned histories only
(provenance gate script), separate PRs per concern, no correctness-for-token
trade (protected semantics list enforced by tests), no destructive
active-session rewriting (offline dry-run-first repair only), no silent model
selection (Oracle/auditor config refusals tested).

Every merge used expected_head_sha compared against the locally validated
head; every merge was a merge commit preserving commit separation.
