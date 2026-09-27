# PiG monorepo release blockers

**Publication is intentionally blocked, not implemented by a site workaround.**
Pigpen CI is **validation-only**: it checks the generated index and all manifests.
It does not build/sign Piglet Binaries, handle signing keys, or upload artifacts.
A `pig-with-batteries/v0.1.0` tag is recognized, then refused by the release guard.
The workflow has no signing secrets or write permissions.

## Source inspection

Checked public PiG commit
[`35280defec22d384545be37a7f297e1a98f1ace1`](https://github.com/MichaelKinsy/PiG/commit/35280defec22d384545be37a7f297e1a98f1ace1),
also pinned as the CI validator. The Pigpen scaffold passes validation with a
binary built from this commit.

- [`coding/pigletbuild/publish_github.go`](https://github.com/MichaelKinsy/PiG/blob/35280defec22d384545be37a7f297e1a98f1ace1/coding/pigletbuild/publish_github.go):
  `publishRelease.tag()` is hard-coded to `"v" + version`. No tag-prefix option
  exists. Release existence checks, URLs, and upload all use this value.
- [`coding/piglet/release/pull.go`](https://github.com/MichaelKinsy/PiG/blob/35280defec22d384545be37a7f297e1a98f1ace1/coding/piglet/release/pull.go):
  `github:owner/repo@version` accepts a SemVer and constructs
  `/releases/download/v<version>/piglet-release.json`. A prefixed version fails
  SemVer validation. A **direct HTTPS signed-index URL does work** without this
  shorthand, including a URL with a per-Piglet prefix; signature/asset/identity
  checks still apply. This does not fix publishing or updates.
- The public [Piglets documentation](https://github.com/MichaelKinsy/PiG/blob/35280defec22d384545be37a7f297e1a98f1ace1/internal/pigdocs/content/piglets.md)
  marks named Piglet updates as planned. There is no namespace-aware update
  command in this inspected tree. Any future update implementation must select
  the named Piglet's releases, not repository-wide `releases/latest`.
- Remote source add selects a single Piglet from a package/repository root
  (`pig.piglet` or `piglet.yaml`, per that documentation). A root
  `piglet.yaml` would pick one agent, not select `piglets/<name>/` independently.
  Package Git subdirectory support alone is not evidence that Piglet add uses
  it. Pigpen offers **no remote add command** until that path is verified.

## Required PiG feature task (not platform code)

1. Add a reviewed per-Piglet release tag namespace, e.g. a **proposed**
   `--tag-prefix <name>/` publish option. Preserve unprefixed default behavior.
   Use it consistently in dry-run, existing-release refusal, asset URLs,
   source refs and upload. Keep release SemVer independent of Git tag strings.
2. Extend GitHub pull references with unambiguous Piglet/tag selection (syntax
   is a PiG API decision). Record that identity in signed indexes and receipts;
   reject an index whose Piglet name, version or repository does not match.
3. Make update discovery namespace-aware. Never use repository-wide "latest"
   for a named Piglet in a shared repository; never update one Piglet to another.
   Preserve signer pinning/revocation and rollback/version checks.
4. Verify or implement `pig piglet add` from an explicit repository subdirectory
   at a pinned commit. Keep resource/prompt closure portable, path-safe and
   attributable to the selected Piglet; do not silently select the root agent.
5. Add end-to-end tests publishing two Piglets with the **same** release version,
   then pulling/updating each independently. Cover escaped slash tags,
   mismatched names, existing releases, tampered bytes and signer changes.

## Complete publication after that task

- Replace the pinned PiG build-tool commit in CI with the reviewed feature commit.
- Finish curation, keep all sources pinned, and validate the portable closure.
- Adapt PiG's native-runner
  [release workflow](https://github.com/MichaelKinsy/PiG/blob/35280defec22d384545be37a7f297e1a98f1ace1/docs/examples/piglet-release.yml)
  using the target matrix derived by `scripts/build-matrix.mjs`. Add per-Piglet
  protected environments and Ed25519 secrets;
  publish only on `<name>/v<release.version>` after tag/manifest equality checks.
  Use native builders, provenance attestations and prebuilt `--artifacts` exactly
  as that workflow does; do not upload disposable test-key artifacts.
- Use PiG's new native prefixed publisher, **not** `gh release create` around its
  index and not a site-created signature. Only the publisher owns release bytes.
- Extend `generate-index.mjs` to consume verified publisher receipts for each
  manifest. Populate actual release version, direct signed-index URL, target
  platforms and public key ID/URL. Do not infer release availability from
  `build.targets` or from a tag existing. The generator currently permits only
  planned entries so an unverified release cannot become installable by mistake.
- Generate and review `index.json`; the site's next authorized rebuild consumes
  it. No site-to-monorepo synchronization is needed.

Until then, a working prefix publisher and live binaries cannot honestly be
claimed. The catalog displays a planned composition and no download buttons.
