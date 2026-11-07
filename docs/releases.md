# Install forms, releases and the site

## What is published

This was checked against GitHub Releases, the Git tags and the npm registry on 2026-11-06.

- The public signing key is pinned in `release-keys/`.
- Signed Piglet Binaries for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64 are GitHub Releases named `<name>/v<version>`: `a2a`, `acp`, `herdr`, `jev`, `pig-essentials`, `pig-extension-porter`, `pig-games`, `pig-music`, `pig-popular`, `pig-porter`, `pig-typesafe` and `pig-warden` at 0.1.1, `pig-with-batteries` at 0.1.2 and `pig-tern` at 0.1.0. Eleven of them were first released as 0.1.0 on 2026-10-05.
- All 41 Packages (at the version in the repository) and all 21 Piglet sources are on npm.
- Package Git tags exist for the 29 Packages recorded in `releases/packages/`.
- The bench Piglets are not released as Binaries.

What is not done: the batteries-included composition is not complete, and `releases/` holds no verified Piglet receipt, so `catalog/index.json` still lists every Piglet as `planned`.

The release path (workflows, tag scheme and the checks below) was rehearsed locally with throwaway keys. [OWNER-ACTIONS.md](maintainers/OWNER-ACTIONS.md), [FIRST-NPM-PUBLISH.md](maintainers/FIRST-NPM-PUBLISH.md) and [RELEASE-BLOCKERS.md](maintainers/RELEASE-BLOCKERS.md) were written before the first release and describe the steps and what remained open then.

## Install forms

Pigpen is published to npm under the `@pi-in-go` scope, the way Pi publishes its packages. Packages carry the keyword `pig-package`, Piglet sources carry `pig-piglet`, and pi-in-go.dev lists them by keyword. These commands resolve today. The first publish is described in [FIRST-NPM-PUBLISH.md](maintainers/FIRST-NPM-PUBLISH.md).

```bash
pig install npm:@pi-in-go/pigpen-<name>                       # a Package: components/<name>, for example pigpen-herdr
pig piglet add npm:@pi-in-go/pigpen-piglet-<name>             # a Piglet's source: piglets/<name>
pig piglet pull 'github:MichaelKinsy/pigpen/<name>@0.1.0'     # the same Piglet as a signed Binary
```

`pig piglet pull` needs the full `github:owner/repo/<name>@<version>` reference. Pulling by bare Piglet name is not in PiG 0.7.0.

The Binary is the signed per-platform build from the GitHub release `<name>/v<version>` (below). The npm package is its editable source and installs its Packages from npm. Check the signing key (`release-keys/pigpen-piglets.pub`) before you trust a Binary. A signature alone does not prove who holds the key.

How the npm form differs from this tree (generated, never hand-edited):

- Each `components/<dir>/package.json` is the published manifest (`npm run check` enforces it). A Go extension that reaches `pig-play`, `typesafe` or `pi-typesafe-api` through a sibling directory depends on that library's own Package (`dependencies`), and its `go.work` names the installed sibling `node_modules/@pi-in-go/pigpen-<lib>`. Two Packages that share a library then share one directory of it, because PiG's native builder fuses a Go module from one directory only. `scripts/npm-packages.mjs` generates this into `dist/npm/packages/<dir>`.
- A Piglet source (`scripts/npm-piglets.mjs`, `dist/npm/piglets/<name>`) names its Packages as `npm:@pi-in-go/pigpen-<dir>@^<version>`, flattens `extends` (`pig-porter`) and drops the local `seed-check` build fixture, because a remote Piglet source takes no local Resource.
- `npm run test:npm-packages` and `npm run test:npm-piglets` pack everything, serve it from a local registry and run `pig install npm:...`, `pig piglet add npm:...` and `pig piglet validate` with the real pig. The second also builds a fused `pig-games` Binary from the registered source. `npm run publish:npm -- --dry-run` shows what would be published.
- `.github/workflows/npm-publish.yml` publishes every Package or Piglet whose version is not on npm yet when a merge to `main` bumps it (a tag `npm/v<x>` or a manual run retries). It uses npm trusted publishing and provenance, with no token.

## Releases

Two more channels stay available: the Git-tag channel for Packages and the signed Binaries for Piglets.

| | Tag | Install | Workflow |
|---|---|---|---|
| Package (`components/<name>`) | `components/<name>/v<version>` | `pig install 'git:https://github.com/MichaelKinsy/pigpen.git@components/<name>/v<version>#subdirectory=components%2F<name>'` | `release-package.yml`: validates and installs from the tag; no secret |
| Piglet (`piglets/<name>`) | `<name>/v<release.version>` | `pig piglet pull 'github:MichaelKinsy/pigpen/<name>@<version>'` | `release.yml`: builds every target natively, signs, publishes one GitHub Release |
| Archon plugin (`components/archon-provider-pig`) | `components/archon-provider-pig/v<version>`, released as plain `v<version>` in the generated repository | `archon plugin add MichaelKinsy/archon-provider-pig` | `release-archon-provider.yml`: generates the standalone repository, builds six `bun build --compile` binaries and `checksums.txt`, publishes the release there with `ARCHON_PROVIDER_PIG_TOKEN` (maintainers/OWNER-ACTIONS.md, section 9) |
| npm (all of the above) | a merge to `main` (or `npm/v<x>`) | `pig install npm:...`, `pig piglet add npm:...` | `npm-publish.yml`: Packages, then Piglet sources; OIDC, provenance |

The Piglet, Package and npm tag shapes cannot start each other's workflow. An Archon plugin tag has the Package shape, so it also runs `release-package.yml`, which only verifies it.

`catalog/index.json` lists a Package as `available` once its Git tag is recorded (`npm run record -- package <name> <version>`). It lists a Piglet as `available` once its signed receipt verifies against the key in `release-keys/` (`npm run record -- piglet <name> <version>`). Until then a Piglet is `planned` and a Package is not listed. `npm run test:packages` installs every Package from its Git tag with the real pig. `npm run rehearse:release` runs the Piglet workflow's own steps against a throwaway local repository.

## Site consumption

The site at [pi-in-go.dev](https://pi-in-go.dev/packages) reads this repository's generated index over HTTPS at build time, validates it, and keeps a checked-in fallback for outages. It never writes back into Pigpen. Verified upstream Pi packages remain platform-owned data. Community Piglet discovery is deferred.
