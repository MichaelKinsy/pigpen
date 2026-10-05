# Port record: pig-play (relocation of PiG Standard's shared art and terminal code)

This is a **Go-to-Go relocation**, not a Pi TypeScript port. There is no Pi oracle; the
original is MichaelKinsy/PiG `piglets/standard` at commit
`d86eb93f217e64b655e9ee6f48c93a9afd107697` (MIT, Copyright (c) 2026 Michael Kinsy, written by Michael Kinsy, who asked for it to ship in Pigpen). The
upstream Go files are not vendored: the table names each upstream file by git blob id at
that commit, and the diff is reproducible with

```sh
git -C <PiG> show d86eb93f217e64b655e9ee6f48c93a9afd107697:piglets/standard/<path> | diff - <file here>
```

| Upstream (`piglets/standard/`, blob) | Here | Differences from upstream |
|---|---|---|
| `internal/pixel/pixel.go` (`c3fe91df`) | `libraries/pixel/pixel.go` | none |
| `internal/pixel/text.go` (`97098f87`) | `libraries/pixel/text.go` | none |
| `internal/arcade/arcade.go` (`260448c0`) | `libraries/arcade/arcade.go` | import path of `pixel`; package doc says "the pig games" |
| `internal/termgame/termgame.go` (`53c43e30`) | `libraries/termgame/termgame.go` | package doc wording (SDK import unchanged) |
| `extensions/piglogin/art.go` (`73307e4f`) | `libraries/sprite/art.go` | `package sprite`; `logoRows` exported as `LogoRows` |
| `extensions/piglogin/variants.go` (`9fae5733`) | `libraries/sprite/variants.go` | `package sprite`; `defaultVariant` exported as `Default`; new `ByID` (was the unexported `variantByID` in `extension.go`); `logoRows` -> `LogoRows`; review: the `pig-default` comment names no website source repository, and names `assets/pig/website-art.txt` (was `testdata/`) |
| `extensions/piglogin/extension.go` (state functions only) | `libraries/sprite/state.go` | `loadVariant` kept, `saveVariant` -> `SaveVariant`, `statePath` -> `StatePath`, `defaultVariantID` -> `DefaultID` |
| `extensions/piglogin/definition.go` (`heroFor`, `rgbaHex`, hero constants) | `libraries/sprite/hero.go` | `heroFor` -> `Hero`, `rgbaHex` -> `RGBAHex`, exported hero constants; no SDK import |
| `extensions/piglogin/testdata/website-art.txt` (`f0039a56`) | `assets/pig/website-art.txt` | review: comment lines only (no website source repository named; source paths shortened); colours and hashes unchanged |
| `extensions/piglogin/LICENSE` (`9abb2ff4`) | `LICENSE` | none |

`extensions/piglogin/definition.go` `LoginDefinitionFor` and `extension.go` (`/sprite`)
stayed SDK-bound and moved to the `pig-login` Package. That Package was removed later: PiG 0.4.0
has the sprite login and `/sprite` built in (`coding/piglogin`, with every sprite listed here and
the same state file), so Pigpen no longer carries a login of its own.

## Tests

| Upstream test file | Twins here |
|---|---|
| `internal/pixel/pixel_test.go` (6) | `libraries/pixel/pixel_test.go` (6, byte-identical) |
| `internal/arcade/arcade_test.go` (3) | `libraries/arcade/arcade_test.go` (3, import path only) |
| `internal/termgame/termgame_test.go` (3) | `libraries/termgame/termgame_test.go` (3, identical) |
| `extensions/piglogin/extension_test.go`, `login_art_test.go` (11) | 7 twins in `libraries/sprite/sprite_test.go` (state persistence and mode 0600, unknown saved variant, default is the website pig, pig-default by ID and saved state, mascot matches website pig, logo matches website wordmark, hero draws "PiG." with period). The `spriteList` assertion of `TestPigDefaultResolvesByIDAndFromSavedState` and the four login-definition and golden-render tests need the SDK; they were in `pig-login` and left with it (PiG's built-in login carries its own). Renamed identifiers only (`loadVariant` -> `ActiveVariant`, `saveVariant` -> `SaveVariant`, `statePath` -> `StatePath`, ...). |

Added here, not upstream: `TestEveryVariantSpriteIsTheSharedGrid`,
`TestFindVariantFallsBackToDefault`, `TestEveryVariantPaletteCoversItsSprite`, and
`TestGamesReadTheSelectionPiGsBuiltInLoginSaves` (the games read the file PiG 0.4.0's built-in
`/sprite` writes; an id only PiG knows is drawn as `pig-default`).

**Counts: 19 exact or renamed twins, 0 skipped, 4 added.**

## Checks run

`go vet ./...` on linux, windows, darwin; `go test -race -count=1 ./...` (see the lane
report for the exact log). The tests were written first (commit `test(pig-play): RED`)
and failed to build; the implementation commit made them pass.
