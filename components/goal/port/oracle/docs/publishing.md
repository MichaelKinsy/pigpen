# Publishing releases

The `Publish npm` GitHub Actions workflow publishes through npm Trusted Publishing (OIDC). Agents need GitHub push/workflow access; no npm write token or recurring npm 2FA prompt is needed. Account 2FA stays enabled.

Bump package.json and package-lock.json together, update the changelog, and merge the release into main. Push a stable version tag matching package metadata:

```sh
git tag -a vX.Y.Z -m 'Release vX.Y.Z'
git push origin vX.Y.Z
gh run list --workflow publish.yml
```

Before publishing, the workflow records the Pi extension download ranking and commits updated light/dark badges and README text to main. It uses the same best-recorded-rank method as the website. The tagged checkout receives that release’s saved observation before packing, so the npm package includes the updated badges. Ranking updates run only for real releases, never daily or during dry runs. A ranking failure stops publication. Retries reuse the release observation instead of fetching a new rank.

The workflow checks main ancestry and version metadata, runs validation, packs once, uploads the tarball, publishes it with provenance, verifies registry integrity, and creates a GitHub release. Stable releases only; publication is serialized. An existing npm version is accepted only if the tarball integrity matches. Existing GitHub release notes/assets are preserved.

To rehearse without publishing, run the workflow on main with an existing tag:

```sh
gh workflow run publish.yml --ref main -f tag=v0.31.1 -f dry_run=true
```

To retry a release after fixing workflow infrastructure:

```sh
gh workflow run publish.yml --ref main -f tag=vX.Y.Z -f dry_run=false
```

Registry metadata and the latest tag may lag a successful npm publish. Verification polls each for up to five minutes; a timeout does not mean publication failed. Check registry availability before retrying. Failed-job reruns reuse the successful ranking job’s artifact ID, including across run attempts.

Each run attempt retains its own tarball artifact. Repacking on another platform or npm version can change archive integrity even when source files match; an integrity mismatch stops the retry for investigation.

Manual runs default to dry run. A dry run does not exercise npm OIDC authentication; only a real publish verifies the complete exchange. Do not publish an extra version solely to test authentication.

## npm configuration

The package trusts GitHub repository `tmonk/pi-goal-x`, workflow filename `publish.yml`, with direct `npm publish` permission and no approval environment. Setting up or changing npm trust requires interactive 2FA once. For setup:

```sh
npm trust github pi-goal-x --repo tmonk/pi-goal-x --file publish.yml --allow-publish --yes
```

Use GitHub-hosted runners; npm currently does not support self-hosted runners for trusted publishing. Keep `id-token: write` on the publishing job and use an OIDC-capable npm CLI. No `NPM_TOKEN` secret is required. Do not configure stage-only publishing or required environment reviewers if releases must run unattended.

Reference: https://docs.npmjs.com/trusted-publishers/
