# Milestones

- Confirmed clean main and existing CI requirements. npm 11.19.0 supports npm trust github with --allow-publish. User authorized configuration for unattended publishing; retain account 2FA.

- PR #51 merged after CI 34180264478 passed. actionlint passed; nine tag/metadata guard cases and four registry/idempotency guard cases passed.
- First rehearsal 34180410192 passed validation, context/provider checks and packaging, but npm 11.19.0 rejects an existing version even in dry-run mode. Added --force exclusively to the non-writing rehearsal to skip that registry version check. Actual publication never uses --force. Retain a separate artifact per run attempt; archive hashes can differ across operating systems, so existing-release integrity mismatches deliberately stop retries.

- Corrected rehearsal 34180571692 and exact-main CI 34180572174 passed on 30a062e. No package version was published. Started npm trust setup scoped to pi-goal-x, tmonk/pi-goal-x and publish.yml with direct publish permission; awaiting one-time Safari passkey approval. Dry run does not prove the OIDC exchange.
- npm trust approval expired before Safari was unlocked (E404 from the approval poll). Trust creation is not confirmed. Retry setup when the user is present; do not claim unattended publication is active until npm confirms the trust.

- Safari authentication succeeded on retry. npm trust exited successfully and confirmed configuration e139ac83-dc67-43f3-80ea-cb63f7bc882f for tmonk/pi-goal-x, publish.yml, with publish and stage publish permissions. Direct unattended publishing is authorized; no npm write token or approval environment is configured. The next actual release will verify the complete OIDC exchange; no extra version was published for setup.
