# Contributing to Pigpen

Pigpen holds Michael Kinsy's official Piglet compositions. Contributions are
welcome through reviewed pull requests; adding a directory does not automatically
make a Piglet official, supported or available to install.

## Choose the right repository

- Report Piglet manifests, selected resources, defaults, documentation, generated
  catalog data and composition-specific behavior in
  [MichaelKinsy/pigpen](https://github.com/MichaelKinsy/pigpen/issues).
- Report core PiG runtime, CLI, extension-host behavior and monorepo
  publish/pull/update support in
  [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG/issues).
- For security-sensitive findings, follow [SECURITY.md](SECURITY.md). Do not
  include credentials, signing keys or private session data in public issues.

The issue templates repeat this distinction. When unsure, explain which Piglet
and PiG version are involved and whether the issue reproduces without the Piglet.

## Make a change

1. Start a branch from `main`. Keep a PR focused on one composition or concern.
2. Put each Piglet in `piglets/<name>/` with its own `piglet.yaml`, `catalog.json`,
   README, and owned `extensions/`, `skills/`, and `prompts/` directories.
3. Keep agent identity, description, resource selections, release version and
   build targets in **`piglet.yaml`**. Keep presentation-only fields (status,
   featured flag, review date, languages, caveats) in `catalog.json`.
4. Pin external resources, retain their licenses, and document provenance and
   runtime requirements. Do not copy a running session's resources implicitly or
   let a release pick up a maintainer's ambient local configuration.
5. Keep the current scaffold marked **planned**. `seed-check` is an empty
   build-pipeline fixture, not a curated battery. Do not invent download URLs,
   supported platforms, verification keys, passing checks or release claims.
6. Generate and review `index.json`; never edit generated entries by hand.

## Required checks

Use Node.js 24, Go as specified by the reviewed PiG source, and the PiG validator
commit pinned in `.github/workflows/ci.yml`. `PIG_BIN` selects that binary:

```sh
npm ci --ignore-scripts
npm run generate
npm run check
PIG_BIN=pig npm run validate
node scripts/build-matrix.mjs
```

Review the generated diff and commit source plus index together. `npm run check`
fails on drift. The YAML parser rejects duplicate keys; PiG validates each
manifest and its selected resources. Validation may compile or load extension
code, so run untrusted contributions in an isolated environment without secrets.
CI is validation-only and has a read-only token. It must not sign or publish
Piglet Binaries while [release blockers](RELEASE-BLOCKERS.md) remain unresolved.
Do not add a workaround publisher to this repository or the site.

## Sign off commits

Use your own public contributor identity and sign off each commit:

```sh
git commit -s -m "Describe the change"
```

A `Signed-off-by` line certifies that you have the right to submit the contribution
under this repository's license, as described by the
[Developer Certificate of Origin 1.1](https://developercertificate.org/).
Cryptographic signatures are welcome but separate from this sign-off.

Include the checks you ran, any limits or known failures, and the relevant
Piglet/core issue links in your pull request. Maintainers review before merging;
a green validation run is not authorization to publish a release.
