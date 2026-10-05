# Patch 0.31.4

User authorized merging PR #58 and releasing the next patch. Publish the merged explicit scheduler with automatic continuation enabled by default, optional maxAutonomousRuns (zero disables, positive caps), bounded waits, one repair and stale-dispatch protection. Include the deadline and inherited-zero fixes.

Keep README.md unchanged from the approved PR. Bump package.json and package-lock.json together, publish the changelog, and release v0.31.4 through the established GitHub Actions/npm trusted-publishing workflow. Verify npm latest, package integrity and the GitHub release before reporting success.
