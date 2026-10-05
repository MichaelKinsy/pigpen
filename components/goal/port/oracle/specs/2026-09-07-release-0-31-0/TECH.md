# Release procedure

The previous npm latest and GitHub latest release are both v0.30.5 (`59826ec`). Compare that source directly with the final optimized source (`18323b8`) using identical fixtures, dependencies and benchmark code. Alternate baseline/candidate/candidate/baseline runs in separate processes using real SDK 0.84.1. Report averages of per-run medians, with repeated operations for short paths. Reuse the corrected context captures for the previous release and final source; name the active fixtures and record their exact character counts.

The release does not change production code. Existing 923-test, TypeScript, lint, context, performance and SDK compatibility evidence applies to the production source hash. Verify the version bump, package contents, clean installation/import of the packed artifact and current CI checks. Synchronize root lockfile version metadata.

Commit the release notes, comparison evidence, changelog and version bump. Push main, confirm CI, tag the reviewed commit, publish the exact checked npm tarball, and create the GitHub release with the measured notes. Verify registry latest/version/integrity and the published GitHub tag. Never expose authentication material in logs.
