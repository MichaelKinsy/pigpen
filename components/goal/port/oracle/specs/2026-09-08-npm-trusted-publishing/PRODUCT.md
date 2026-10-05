# Unattended npm publishing

Agents release pi-goal-x through GitHub Actions without recurring npm 2FA or stored npm write tokens. Keep account 2FA enabled. Configure npm trust for tmonk/pi-goal-x, publish.yml, with direct publish permission.

Stable vX.Y.Z tag pushes publish after validation. A manual workflow accepts an existing tag and defaults to dry run for safe rehearsal. Release tags must match package metadata and point to main history. Serialize publication, retain the tested tarball, verify registry integrity, and create a GitHub release. Do not publish another version just to test configuration.
