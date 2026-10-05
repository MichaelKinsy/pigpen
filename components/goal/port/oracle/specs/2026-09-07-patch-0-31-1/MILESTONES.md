# Milestones

- Planning inspection: main e0411fb is clean, npm/GitHub latest 0.31.0; TypeScript, lint and 923 tests pass. Four open issues (#45, #47, #48, #49), one open PR (#46, e594a64e); PR is mergeable and maintainers may edit it.
- Reproduced undefined questionnaire result and proposal TypeError. Reproduced the reported audit history through SDK 0.84.1 conversion: it synthesizes a second result for the same call; removing the audit display message restores one paired result.
- Verified pi-subagents 0.60.0 launch source sets PI_SUBAGENT_CHILD=1 and increments PI_SUBAGENT_DEPTH. Current goal startup restores inherited focus and arms continuation without a child guard.
- Began implementation on codex/pr-46-drafting and merged origin/main into the original contributor commits without conflicts.

- Hardened PR #46 with direct RPC dialogs, safe unknown-host factory fallback, unambiguous recommendations/custom choices, auditor selection, and explicit failure handling. Corrected proposal summaries to show the selected auditor setting. Original contributor commits retained.
- Drafting validation: 947/947 full-suite tests, TypeScript, lint, discovery self-check, 24 context fixtures and six real-SDK provider-payload checks pass. Added 24 tests since main; no baseline drift.

- Setback: contributor-fork pushes rejected over both HTTPS and SSH despite maintainerCanModify=true. Preserved amendments in commit 1749760 and continued on codex/release-0.31.1; TECH records the checked maintainer-PR integration path.
- Installed isolated SDK 0.83.0 and 0.84.4 dependencies for final compatibility validation.

- Implemented session-scoped audit transcript queue and historical audit context filtering, plus early delegated-child isolation. Updated the focus-race regression to switch focus during the auditor itself rather than relying on unsafe in-tool transcript dispatch.
- Real-SDK lifecycle tests pass for approval/rejection/skip/abort/error and fresh/fork/resume/nested children, including a 6.5-second initial fork prompt. Initial history fixtures counted both the capture hook and local HTTP dispatch because the SDK catches hook exceptions; corrected the harness to use real local Completions and Responses SSE responses and verify completed historical sessions.
- Release date crossed midnight in Europe/London; changelog uses 2026-09-08 and the original 2026-09-07 spec directory remains the implementation record.

- Final production implementation on pinned SDK 0.84.1 passes TypeScript, lint and all 961 tests (including 11 real-SDK session/protocol subprocess scenarios); no tests skipped. Production dependency audit reports zero vulnerabilities; NAF CI gate and package dry run pass. Context/provider validation remains unchanged.

- SDK compatibility complete: 0.83.0, 0.84.1 and 0.84.4 each pass TypeScript, 915 serial unit tests, 11 real-SDK session scenarios and six provider-payload checks. Root full suite passes 961/961; discovery self-check passes 915 units; context baseline remains unchanged.
- Maintainer release PR #50 passed CI run 34169124742 on ce48088. Updated #46 with the fork-permission explanation and companion link, then merged exact original head e594a64e as 906b78a. Merged main into the release branch; production files, tests and package inputs are byte-unchanged from the validated candidate.

- Final-branch CI run 34169322174 passed the full suite but exposed a pre-existing flaky self-check test: a random goal id can contain the task-id substring t1. Made the fixture deliberately include t1 and assert the exact bounded marker rather than banning that substring. Production/package bytes remain unchanged.
- Package smoke harness initially used native Node type stripping, which deliberately rejects TypeScript under node_modules. Switched the check to Pi's actual extension loader; no package change is needed.

- Deterministic checkpoint test passes; PR #50 final-head CI 34169518627 passed and it was merged as cb282e2. Exact merged-commit CI 34169616964 also passed. All four issues and both PRs are closed.
- Built one tarball and verified all 62 files (52 extension modules) byte-match the final merged source. A clean npm install with peer SDK 0.84.4 loads through Pi's real extension loader; recovery CLI help succeeds. PACKAGE.json records the integrity and source commit.
- Pushed v0.31.1 at cb282e2. npm publish requires the maintainer account's security-key authentication; opened the official approval page and requested the user complete two-factor authentication. No credentials or authentication URLs are stored in the repository. GitHub release 384357558 is a prepared draft with the matching tarball asset; registry latest remains 0.31.0 until authentication succeeds.

- Publication complete on 2026-09-08: after expired approval attempts, Safari passkey authentication succeeded and the original verified tarball published as pi-goal-x@0.31.1. npm latest, SHA-512 integrity and SHA-1 match PACKAGE.json. Published the GitHub release with the matching SHA-256 asset; remote v0.31.1 resolves to tested merge cb282e2. PUBLISHED.json records registry and GitHub evidence.
