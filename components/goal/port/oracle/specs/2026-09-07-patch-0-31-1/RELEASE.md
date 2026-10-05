## Fixes

- Guided drafting now works on RPC and browser hosts through native dialogs, with complete proposal context and auditor selection. Missing or cancelled dialogs never confirm an unseen goal. Resolves #45 and #47; includes the original contribution in #46 and its hardened follow-up in #50.
- Audit transcript events no longer interrupt tool/result pairing or enter model context. Existing affected histories can resume after loading the updated extension, without editing session files. Resolves #48.
- Delegated subagents inherit conversation without inheriting the parent goal loop, tools, accounting, or state writes. Covers fresh, forked, resumed and nested children. Resolves #49.

961 tests pass, with no skips. SDK 0.83.0, 0.84.1 and 0.84.4 each pass 915 serial unit tests, 11 real-SDK session scenarios, and six provider-payload checks. TypeScript, lint, context, package and production dependency checks pass. No dependency-range or saved-data format changes.

Restart Pi sessions to load the updated extension.
