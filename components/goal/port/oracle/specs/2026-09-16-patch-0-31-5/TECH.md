# Release procedure

Merge green PR #64 at its verified head. Bump package and lock metadata together, move Unreleased notes into 0.31.5, and record release ranking. Merge a release preparation PR after CI, tag its main commit, and let publish.yml validate, pack, publish with OIDC and create the GitHub release. Verify registry latest and integrity against the workflow artifact.
