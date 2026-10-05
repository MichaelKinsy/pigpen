# Contributing to Pigpen

Pigpen holds PiG Piglet compositions and reusable components maintained by
Michael Kinsy. Contributions use reviewed pull requests. Adding a directory
does not make a Piglet supported or available as a published release.

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
   and README. Keep shared Resources in `components/<name>/` Packages. Add owned
   `extensions/`, `skills/`, or `prompts/` only when the Piglet needs private Resources.
3. Keep agent identity, description, Resource selections, release version and
   build targets in **`piglet.yaml`**. This is the authored composition input.
   Run `npm run stage` before selecting it in PiG. Shared local Package references
   become Piglet-local references in `dist/staged/`. Keep presentation-only fields
   in `catalog.json`.
4. Pin external resources, retain their licenses, and document provenance and
   runtime requirements. Do not copy a running session's resources implicitly or
   let a release pick up a maintainer's ambient local configuration.
5. A Piglet is **planned** until a release receipt is recorded; the index sets that, not you. `seed-check` is an empty
   build-pipeline fixture, not a curated battery. Do not invent download URLs,
   supported platforms, verification keys, passing checks or release claims.
6. Generate and review `index.json`; never edit generated entries by hand.
7. Releases are the owner's (OWNER-ACTIONS.md). A Package version is its `package.json` `version`; bump it in the change that
   needs a new release, and the tag `components/<name>/v<version>` is pushed by the owner. A Piglet's version is
   `release.version` in its `piglet.yaml`. `npm run test:packages` (real pig) and `npm run rehearse:release` check the
   release path; a change to a workflow also has to pass `npm run quality`, which enforces where the signing key may appear.

## Port and composition process

1. Pin the upstream source, Pi oracle, target PiG runtime, and applicable licenses.
2. Inventory the API and agree on observable behavior and any deliberate corrections.
3. Use the [porting Skill](components/extension-port/skills/pigpen-pi-extension-port/SKILL.md)
   (or the [`pig-extension-porter`](piglets/pig-extension-porter/README.md) Piglet): SDK gap
   check, tests first, Go implementation, differential equivalence with the original under Pi,
   mutation check, binary build, and a Package with the `pig-package` keyword, provenance and
   the original's license. Ports are Go only.
4. Validate the Package independently. Stage selecting Piglets and exercise their
   real behavior with clean config homes. A manifest-only pass is not runtime proof.
5. Review the full diff, evidence, attribution, and release requirements before
   publication. Keep unavailable evidence and failing checks visible.

A Go extension moved from another repository (for example the pig games from PiG Standard)
follows the Skill's "Relocating a Go extension" section: twins of the original tests, golden
traces from the unmodified original under PiG (`pigeq record --go-oracle`), and a `port/PORT.md`
that names every file by its upstream blob. Code shared by several Packages is its own Package
and Go module, reached from each extension directory through a `go.work` (a `replace` in
`go.mod` is ignored by `pig piglet build`).

The Skill owns the detailed procedure. This file owns contributor obligations.
Scripts and CI enforce mechanical checks. The roadmap records decisions and
remaining work, not a second copy of the porting procedure.

## Required checks

Use Node.js 24, Go as specified by the reviewed PiG source, and the PiG validator
commit pinned in `.github/workflows/ci.yml`. `PIG_BIN` selects that binary:

```sh
npm ci --ignore-scripts
npm test
npm run generate
npm run check
PIG_BIN=pig npm run validate
PIG_BIN=pig npm run test:porter
PIG_BIN=pig npm run test:moved
node scripts/build-matrix.mjs
```

Review the generated diff and commit source plus index together. `npm run check`
fails on drift and runs the source quality gates. `npm run validate` also runs
those gates, rebuilds the local stage, and invokes PiG on Packages and staged
manifests. `npm run test:porter` exercises independent installation and two staged
compositions. The scenario needs a PiG build with Package Skill expansion and the
print-mode tool-scope fix; PiG 0.4.1 has both, and CI runs it with the pig it builds from
PiG's `v0.4.1` commit. Set `PIG_BIN` to a 0.4.1 pig (PiG 0.3.x fails it). `npm run test:moved` installs `session-ingest` and
`context-info` into a scratch home and drives a real `pig` against a scripted local
provider (needs `PIG_BIN` and a Go toolchain). Run `npm run quality` to check source alone.

Use `npm run stage` for the edit/run loop. The script owns `dist/staged/` and
replaces that generated tree. Never put authored Resources there. Local Package
references outside a Piglet must select exactly `components/<name>/` inside this
repository. Do not use absolute paths, symlinks, or copies in another owner.
All Package build inputs and licenses must travel with the Package.
For publication, select approved immutable Package references instead of sibling paths.

- Give every `SKILL.md` valid YAML frontmatter, a nonempty description, and a
  unique `pigpen-<name>` identifier. Match its directory to that identifier.
  Quote descriptions that contain `: `, or use a YAML block scalar.
- Keep each component in one owner directory. Select shared components by Package
  origin, not by copying their source into another Piglet.
- Use `components/extension-port/skills/pigpen-pi-extension-port/SKILL.md` for
  extension ports. Preserve the approved Go target, upstream behavior, and credits.
- Add `provenance.json` to every Piglet and shared component Package. Declare
  original authors, license, license-file path, and upstream sources for ports.
  Retain upstream license texts and readable author credits.
- In Markdown, cite PiG and upstream revisions by tag or commit, not by a working branch: the quality gate refuses
  `team/<who>/<branch>` and `staging/<...>/<branch>` outside vendored originals.
- Use the schema and checks in `scripts/quality-gates.mjs`. The CLI scenario in
  `scripts/quality-gates.test.mjs` includes a complete port-provenance example.
  Source checks cannot establish legal permission or detect every modified copy.
  Review the dependency closure and license obligations before release.

## The ports list

[`ports/ports.json`](ports/README.md) is the dispatch list for every port and move: one row each, with
the upstream URL, the **full commit it is pinned to**, its license and author, the target Package, the port
lane, the status and the credit text. Edit the JSON, then run `npm run generate` (it rewrites the generated
`ports/README.md`); `npm run check` fails on drift, on a malformed row, on a credit text that omits the upstream
name, URL or author, on a Package whose `provenance.json` pins another commit or license than its row, and on
a port or move whose Package `provenance.json` does not record the row's upstream.

- **Credit the copyright holder** the upstream's LICENSE names as `author` (for example `Ryan Gapac (DevMortimer)`,
  not only the GitHub account), and name the Package the lane actually builds as `targetPackage`.
- **Add a row** when a port is approved: status `queued`, a `priority` if it has a place in the queue, the
  pinned commit (the commit of the latest npm release when the registry records one, else the default branch
  head) and the license read from the upstream's own license file at that commit. `node scripts/ports.mjs verify-pins`
  fetches every pin from its upstream (network; not run in CI; git credential helpers and askpass are disabled for it).
- **Status** moves one stage at a time: `queued`, `porting` (a lane started), `review` (the completion checklist of
  the porting Skill is met), `done` (a reviewer accepted). `node scripts/ports.mjs set-status <id> <status> [--note TEXT]`
  updates the JSON and the README together. A `done` row's Package must exist with its `provenance.json`.
- **Port a row:** `/skill:pigpen-pi-extension-port port row <id>`. The Skill reads the row with
  `node scripts/ports.mjs show <id>`, uses its pin, target, license and credit, and sets `porting`, then `review`.
  Never `done`: that is the reviewer's. A re-pin needs approval and a row edit.
- Kinds: `port` translates an upstream program, `adapter` is original code on an upstream SDK or protocol,
  `move` brings existing code in from another repository, `original` is new work (it may credit an upstream asset).
  MCP is built into PiG and has no row.

The YAML parser rejects duplicate keys; PiG validates each
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
