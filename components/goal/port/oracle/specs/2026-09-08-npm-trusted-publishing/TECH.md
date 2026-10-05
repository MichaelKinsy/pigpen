# Design

Use a GitHub-hosted Ubuntu runner, Node 24, npm 11.19.0 and job-scoped id-token:write for npm OIDC. No npm secret or approval environment. Checkout the requested immutable tag, verify stable version and main ancestry, run existing CI gates plus context/provider checks, pack once, and publish that exact tarball with provenance. Verify npm integrity before creating the GitHub release. Reruns accept an already published version only if its integrity matches; they never silently replace a release.

Use workflow_dispatch on main with tag and dry_run inputs. Dry runs perform validation and npm publish --dry-run, which does not prove OIDC authentication. Configure trust only after the workflow is committed and validated. Record that first real publication is the end-to-end authentication test.

Sources: https://docs.npmjs.com/trusted-publishers/ and installed npm 11.19.0 npm-trust manual.
