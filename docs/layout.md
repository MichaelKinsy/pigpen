# Repository layout

This is the tree from the repository root. Paths marked GENERATED are never edited by hand.

```text
catalog.config.json                 MichaelKinsy/pigpen, branch main
components/extension-port/          directly installable shared Skill Package (the porting
                                    workflow, TS-to-Go pattern catalog, fake-host template)
components/extension-equivalence/   directly installable Go extension Package: the
                                    differential harness (pigeq) and SDK gap scanner
demo/                               codemode demo scripts (jev-plays.js: Jev plays both pig games over MCP)
ports/                              the ports list: ports.json (the dispatch list), ports.schema.json,
                                    README.md (GENERATED table; npm run generate)
components/dirty-repo-guard/        a Go port (Pi's dirty-repo-guard example) as a Package,
                                    with its evidence in port/
components/typesafe/                shared Go client for the TypeSafe API, plus an own-model
                                    backend (libraries only, no extension)
components/hardening/               shared Go library of the opt-in enterprise profile (profile, credential file,
                                    egress, audit, headless, policy, resource server; libraries only, no extension)
components/warden/                  a Go port of pi-warden as a Package, with its evidence in port/
components/websearch/                a Go port (first slice) of pi-web-access as a Package, with its evidence in port/
components/ahp/                     a Go port of pi-ahp as a Package (Agent Host Protocol host), with its evidence in proof/
components/pi-typesafe/             a Go port of pi-typesafe as a Package (typesafe_evaluate, /typesafe), with its evidence in port/
components/pi-typesafe-api/         the typed Go API half of the pi-typesafe port (library, no extension)
components/pig-doctor/              /doctor, the read-only pig_doctor tool and the pig-doctor command line: find and fix cruft
components/herdr/                   directly installable herdr reporter Package
                                    (Go extension and its tests, and the pigpen-herdr-desktop Skill)
components/ollama-native/            directly installable Ollama provider Package (native /api/chat;
                                    started by Peder Munksgaard, see its CREDITS.md)
components/pig-play/                Go library Package: pig sprite, pixel, arcade, termgame, gamemcp (no extension)
components/pig-runner/              PiG Runner, /runner (Go, from PiG Standard)
components/angry-pigs/              Angry Pigs, /angry-pigs (Go, from PiG Standard)
components/pig-snake/               directly installable Go game Package: a snake whose head is
                                    the PiG pig and whose every apple adds another pig to the
                                    herd (explicit /pig-snake command; evidence in port/)
components/pig-music/               Pig Music, /music: a full-screen music player for PiG (Go, SDK v0.4.0;
                                    milestone 6: the player screen, library, footer status, shuffle and repeat, /music setup and the doctor, plus the pigmusic CLI; selected by the pig-music Piglet and pig-with-batteries)
components/acp/                     Agent Client Protocol for PiG: the pig-acp companion and
                                    the /acp extension, a Go port of pi-acp with its evidence in port/
components/jev/                     a Go port of pi-jev as a Package (typed judge, off until /jev on),
                                    with its evidence in port/
components/session-ingest/          directly installable Go extension Package: read_session
components/context-info/            directly installable Go extension Package: context and
                                    cost inspection commands, opt-in footer
components/dev-skills/              directly installable Skill Package (adapted, credited)
components/a2a/                     directly installable Go Package: A2A (Agent2Agent) server and client on
                                    a2a-go v2.6.0, listener off unless configured (evidence in port/)
piglets/
  a2a/                              Go Piglet selecting components/a2a (also selected by pig-with-batteries)
  pig-extension-porter/             Go-only focused porter Piglet (signed Binary `pig-extension-porter/v0.1.1`)
  pig-porter/                       umbrella; extends pig-extension-porter (signed Binary `pig-porter/v0.1.1`)
  pig-games/                        PiG Runner and Angry Pigs (signed Binary `pig-games/v0.1.1`)
  pig-music/                        Go-only: PiG with the /music player, nothing starts until you type it (signed Binary `pig-music/v0.1.1`)
  jev/                              Go Piglet selecting components/jev (also selected by pig-with-batteries)
  pig-warden/                       Go-only: PiG with the opt-in warden guardrails (signed Binary `pig-warden/v0.1.1`)
  pig-typesafe/                     Go-only: PiG with the opt-in typesafe_evaluate tool and /typesafe (signed Binary `pig-typesafe/v0.1.1`)
  pig-with-batteries/
    piglet.yaml                     authored composition, input to staging
    catalog.json                    presentation metadata, not a PiG manifest
                                    (selects herdr, the games, Pig Snake, session-ingest, context-info, pig-doctor, jev, a2a, websearch, plus the fixture)
    extensions/                     empty build fixture, not curated capability
    skills/                         owned source, initially empty
    prompts/                        owned source, initially empty
    README.md
  acp/                              PiG for ACP editors (selects components/acp; signed Binary `acp/v0.1.1`)
  herdr/                            PiG plus the herdr agent-state reporter
    piglet.yaml, catalog.json, README.md, tests/   (tests/ runs real pig: npm run test:pig)
  pig-tern/                         PiG as Tern's agent, drawn natively in Tern (signed Binary `pig-tern/v0.1.0`)
scripts/stage-piglets.mjs            materializes local shared Packages
dist/staged/                        GENERATED, gitignored local Piglet trees
scripts/generate-index.mjs           derives catalog data from authored compositions
scripts/validate-manifests.mjs       validates Packages and staged manifests with PiG
scripts/build-matrix.mjs             release tag -> Piglet and native runners; target metadata
scripts/packages.mjs                Package tags, install command, release records, index entries
scripts/receipts.mjs                verifies a signed piglet-release.json against the pinned key
scripts/record-release.mjs          npm run record: writes releases/ after a release
scripts/rehearse-release.mjs        runs the release workflow's steps against a local repository
releases/                           committed release records (Package tags, verified Piglet receipts)
release-keys/                       the public signing key (never a private key)
OWNER-ACTIONS.md                    the owner's first-release commands
scripts/ports.mjs                    validates ports/ports.json, renders ports/README.md, and reads and
                                    updates rows (show, list, set-status, provenance, verify-pins)
index.json                          GENERATED; never hand-edit
index.schema.json                   machine-readable catalog format
.github/workflows/ci.yml             validation, platform build matrix and smoke; no publishing or signing
.github/workflows/release.yml        a Piglet tag: native builds, signing, one GitHub Release
.github/workflows/release-package.yml a Package tag: validate and install from the tag
.github/workflows/release-archon-provider.yml an Archon plugin tag: generate its repository, build, release it there
scripts/archon-provider-repo.mjs     the standalone repository's layout, release tag, asset names, checksums and descriptor check
RELEASE-BLOCKERS.md                  exact core PiG work needed for releases
CONTRIBUTING.md                     changes, checks, sign-off and issue routing
docs/                               this documentation: components.md, development.md, releases.md, layout.md; plan/ holds the roadmap and progress records
SECURITY.md                         private security reporting guidance
```
