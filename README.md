# Pigpen

[Pigpen](https://github.com/MichaelKinsy/pigpen) is the official PiG Piglets
monorepo maintained by Michael Kinsy. It holds named agent compositions built on
[PiG](https://github.com/MichaelKinsy/PiG), not a fork of the core runtime or a
community package registry. `pig-with-batteries` is first; `pig-core` and other
official compositions can follow.

## Current status

**Planned scaffold, not an installable release.** The `pig-with-batteries`
directory reserves the shape of default PiG plus curated extensions, with
protocol bridges planned for later. Curation is not finished. Its only extension
is an empty build-pipeline fixture; it adds no tools, commands or hooks.

No Piglet Binary, release signing key, supported binary platform, or completed
batteries-included composition is published here. The manifest's development
version and build target are not release claims. Publishing waits on **PiG's
monorepo-release support**: prefixed tags, unambiguous Piglet selection and safe
namespace-aware updates. See [RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md).

## Pull or add a Piglet — after support and releases are ready

These are future instructions, **not commands to install this scaffold today**.
Choose a published Piglet/version, review its source and requirements, and use
only the commands recorded in its generated catalog entry and release notes.

- **Signed binary:** `pig piglet pull <signed-release-index-url>` verifies the
  signed index, checksums and Binary before installation. Direct HTTPS index
  URLs already work in PiG; Pigpen has no published index to pull yet. A future
  release's URL will use its own tag namespace, for example
  `pig-with-batteries/v0.1.0`, rather than a repository-wide `v0.1.0`.
  Confirm the public signing key with the publisher; a signature alone does not
  prove who holds the key.
- **Editable source:** `pig piglet add <version-pinned-source-reference>` adds a
  named Piglet; select it with `pig --piglet pig-with-batteries`. The reference
  must select `piglets/pig-with-batteries/` in this repository, not an arbitrary
  root manifest. PiG's monorepo source-selection contract still needs verification
  or implementation, so we intentionally do not invent a remote add syntax.
- Do **not** use `pig piglet pull github:MichaelKinsy/pigpen@0.1.0` as a shortcut:
  the currently inspected PiG implementation assumes unprefixed tags and cannot
  distinguish multiple Piglets with that version. Follow the reviewed command
  published after the core feature lands.

For now, maintainers can inspect the source locally with a compatible PiG:

```sh
pig piglet validate piglets/pig-with-batteries/piglet.yaml
```

## Layout

```text
catalog.config.json                 MichaelKinsy/pigpen, branch main
piglets/
  pig-with-batteries/
    piglet.yaml                     authoritative agent manifest
    catalog.json                    presentation metadata, not a PiG manifest
    extensions/                     empty build fixture, not curated capability
    skills/                         owned source, initially empty
    prompts/                        owned source, initially empty
    README.md
scripts/generate-index.mjs           derives catalog data from manifests
scripts/validate-manifests.mjs       validates every manifest with PiG
scripts/build-matrix.mjs             target metadata and closed release guard
index.json                          GENERATED; never hand-edit
index.schema.json                   machine-readable catalog format
.github/workflows/ci.yml             validation only; no publishing or signing
RELEASE-BLOCKERS.md                  exact core PiG work needed for releases
CONTRIBUTING.md                     changes, checks, sign-off and issue routing
SECURITY.md                         private security reporting guidance
```

## Maintain and validate

Use Node.js 24 and the reviewed PiG build-tool commit pinned in CI. PiG is the
authority for the closed Piglet manifest and resource contract.

```sh
npm ci --ignore-scripts
npm run generate
npm run check
PIG_BIN=pig npm run validate
```

The generator checks duplicate YAML keys and presentation metadata. Names and
descriptions come from the manifests, and the index records their SHA-256
identities. `index.json` is committed alongside source changes for predictable
static-site consumption. A manifest's intended targets never become binary
availability claims. The generator permits only planned entries until verified
publisher receipts can supply real release metadata.

CI checks generated data and validates all manifests. It builds the pinned PiG
validator itself, but **does not build or sign Piglet Binaries**, upload artifacts,
or publish releases. It requires no signing secrets or write permissions. The
release guard refuses prefixed release tags until the core feature is ready.

## Where to report issues

- Piglet manifests, selected resources, defaults, Pigpen documentation or index:
  [Pigpen issues](https://github.com/MichaelKinsy/pigpen/issues).
- Core PiG runtime, CLI, extension host, or monorepo publish/pull/update support:
  [PiG issues](https://github.com/MichaelKinsy/PiG/issues).
- Suspected vulnerabilities: follow [SECURITY.md](SECURITY.md), not a public bug
  report. Never post credentials, private keys or unredacted session files.

See [CONTRIBUTING.md](CONTRIBUTING.md) before proposing a change.

## Site consumption

The site at [pi-in-go.dev](https://pi-in-go.dev/packages) reads this repository's
generated index over HTTPS at build time, validates it, and retains a checked-in
fallback for outages. It never writes back into Pigpen. Verified upstream Pi
packages remain platform-owned data; community Piglet discovery is deferred.

## License

Original code and documentation: [MIT](LICENSE). Curated third-party resources
must retain their own licenses and provenance; inclusion does not grant new
redistribution rights.
