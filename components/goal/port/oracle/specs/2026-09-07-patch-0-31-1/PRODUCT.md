# Patch 0.31.1

Resolve all open items reviewed on 2026-09-07: amend and merge PR #46, fixing #45 and completing #47; fix audit message ordering and historical provider context (#48); isolate delegated subagents from goal automation (#49). Preserve contributor commits and publish the validated patch to npm and GitHub.

RPC and nonterminal hosts must support guided drafting through native select/input dialogs, including complete proposal context and an editable auditor setting. Missing or failed dialogs must never imply confirmation; cancellation discards partial answers and preserves the draft unless the user explicitly discards it. Preserve terminal dialogs and explicit headless/PI_GOAL_AUTO_CONFIRM behavior.

Audit transcript events must not interrupt tool/result pairing or trigger additional model turns. Keep live widget progress and ledger evidence. Existing affected histories must become usable without rewriting session files.

Delegated children inherit conversation, not goal ownership: disable all goal commands, tools, state writes, accounting and continuation in fresh, forked, resumed and nested children. Ordinary parent sessions and interactive forks remain supported. The user explicitly selected this policy and RPC auditor selection during planning.

Acceptance: deterministic regression and real-SDK/mock-provider coverage; full suite and CI checks; SDK 0.83.0, 0.84.1 and 0.84.4 compatibility; verified package import/recovery CLI; matching npm integrity and GitHub release/tag. No paid model calls or saved-data migrations.
