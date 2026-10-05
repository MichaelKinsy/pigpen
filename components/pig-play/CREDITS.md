# Credits

The pig sprite, its colour variants, the "PiG." wordmark, the half-block pixel
rasterizer, the arcade scenery and the terminal game plumbing in this Package come
from **PiG**, https://github.com/MichaelKinsy/PiG, written by **Michael Kinsy**
(Copyright (c) 2026 Michael Kinsy, MIT; see [LICENSE](LICENSE)).

Source, at commit `d86eb93f217e64b655e9ee6f48c93a9afd107697`:

| Upstream (`piglets/standard/`) | Here |
|---|---|
| `extensions/piglogin/art.go`, `variants.go` and the state functions of `extension.go` | `libraries/sprite` |
| `extensions/piglogin/definition.go` (`heroFor`, `rgbaHex`) | `libraries/sprite` (`Hero`, `RGBAHex`) |
| `extensions/piglogin/testdata/website-art.txt` | `assets/pig/website-art.txt` |
| `internal/pixel` | `libraries/pixel` |
| `internal/arcade` | `libraries/arcade` |
| `internal/termgame` | `libraries/termgame` |

The pig artwork is original PiG artwork. Its default variant, `pig-default`,
reproduces the green pixel pig and "PiG." wordmark of the PiG website; the sampled
colours and the hashes of the website files they came from are in
[`assets/pig/website-art.txt`](assets/pig/website-art.txt).

## What changed

The code moved into this Package's module, and exported names replace the
unexported ones that another package needs (`sprite.DefaultID`, `sprite.ByID`,
`sprite.SaveVariant`, `sprite.StatePath`, `sprite.RGBAHex`, `sprite.Hero`,
`sprite.HeroWidth`, `sprite.HeroHeight`, `sprite.HeroPeriodKey`, `sprite.LogoRows`,
`sprite.Default`). The SDK-dependent login definition and `/sprite` went to the
`pig-login` Package, which was later removed because PiG 0.4.0 has the sprite login
built in; the sprite library imports no SDK. Behaviour, artwork, palettes and
persisted state paths are unchanged. [`port/PORT.md`](port/PORT.md) records every
file, and the exact differences from the upstream.
