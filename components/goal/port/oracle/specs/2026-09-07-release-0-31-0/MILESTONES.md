# Release milestones

- Confirmed npm latest and GitHub latest are both v0.30.5; fetched origin and verified main contains the six completed optimization commits.
- Selected 0.31.0 for compatible pagination/batch tool additions. Updated package.json and synchronized previously stale root lockfile version metadata without dependency changes.
- Ran direct alternating previous-release/final-source benchmarks with real SDK 0.84.1. Dashboard rendering improved 5.997×; indexed 100k-event activity reads improved 4,110×. Preserved the 8-microsecond unchanged-input prompt regression and its cache-validity explanation; did not reuse intermediate prompt speedups as release-level claims.
- Joined corrected context captures by fixture: 38% less extension-added context across 14 active workflows. No new model requests; cumulative US$5 allowance remains unchanged.
- Built the npm tarball; all 51 production modules byte-match the source. Real-SDK packed entry import and recovery CLI help passed.
- Release self-check found one newly added test missing from the discovery manifest. Regenerated the manifest; self-check and its 888 unit tests passed. Production dependency audit reports zero vulnerabilities.
- GitHub authentication succeeds. npm rejected the saved login (E401); started the ordinary browser login, which requires user sign-in. No authentication material was printed or changed manually.
- Final release validation passed: 923/923 full-suite tests, TypeScript, lint, 24 context fixtures, comprehensive performance gate and NAF gate. No production source changes since the already validated SDK compatibility runs.
- Pushed release commit `c3f282d` to main. GitHub CI run `34166666567` passed; tagged and published v0.31.0 with the reviewed notes and matching tarball asset.
- npm required one-time publishing authorization even with the new valid token. Completed the passkey flow in Safari at the user’s request, published the exact checked tarball, and verified registry latest/version/SHA-512/SHA-1. Temporary authentication files were removed; no credentials are stored in the repository. `PUBLISHED.json` records the verified release receipt.
