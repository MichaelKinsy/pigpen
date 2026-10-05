# Third-party licences

The licence texts of every Go module compiled into pig-music: the `pigmusic` program (which also runs the native
engine's `pigmusic serve` player) and the `/music` extension. Each directory holds the module's own licence file(s),
copied unchanged from the pinned version in `extensions/pig-music/go.mod` (hashes in `go.sum`). No module is
licensed GPL, LGPL or AGPL (`go-licenses report` for linux/amd64, darwin/arm64 and windows/amd64 with `CGO_ENABLED=0`).
WaxTap and WaxFlow also ship a `THIRD-PARTY-NOTICES.md` for code they ported, which a binary that embeds them owes in
turn; both are kept here. Read WaxFlow's before shipping a binary: its `codec/wma`, `codec/wmapro` and `codec/wmavoice`
parameter tables are data extracted from FFmpeg (LGPL-2.1-or-later), and WaxTap links those packages (through
`waxflow/format`), so `pigmusic` contains them although it only plays Opus and AAC. Not legal advice.

| module | version | licence | built into |
|---|---|---|---|
| github.com/colespringer/waxtap/v3 | v3.6.0 | MIT | pigmusic |
| github.com/colespringer/waxflow | v0.0.0-20260923050513-446ca3124d89 | MIT | pigmusic |
| github.com/colespringer/waxlabel | v1.8.0 | MIT | pigmusic |
| github.com/dop251/goja | v0.0.0-20260723142020-b4aef50fa347 | MIT; `ftoa` Apache-2.0 (`LICENSE_LUCENE`); `ftoa/internal/fast` BSD-3-Clause (`LICENSE_V8`) | pigmusic |
| github.com/dlclark/regexp2/v2 | v2.5.2 | MIT | pigmusic |
| github.com/go-sourcemap/sourcemap | v2.1.4+incompatible | BSD-2-Clause | pigmusic |
| github.com/google/pprof (`profile` only) | v0.0.0-20230207041349-798e818bf904 | Apache-2.0 | pigmusic |
| github.com/ebitengine/oto/v3 | v3.5.1 | Apache-2.0 | pigmusic |
| github.com/ebitengine/purego | v0.11.0 | Apache-2.0 | pigmusic (macOS, Linux) |
| github.com/jfreymuth/pulse | v0.1.3 | MIT | pigmusic (Linux) |
| github.com/Microsoft/go-winio | v0.6.2 | MIT | pigmusic (Windows) |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause | pigmusic (Windows), extension |
| golang.org/x/text | v0.40.0 | BSD-3-Clause | pigmusic |
| google.golang.org/protobuf | v1.36.11 | BSD-3-Clause | pigmusic |
| charm.land/bubbletea/v2 | v2.0.10 | MIT | extension |
| charm.land/lipgloss/v2 | v2.0.6 | MIT | extension |
| github.com/charmbracelet/colorprofile | v0.4.3 | MIT | extension |
| github.com/charmbracelet/ultraviolet | v0.0.0-20260811164956-006e29f97886 | MIT | extension |
| github.com/charmbracelet/x/ansi | v0.11.8 | MIT | extension |
| github.com/charmbracelet/x/term | v0.2.2 | MIT | extension |
| github.com/charmbracelet/x/termios | v0.1.1 | MIT | extension |
| github.com/charmbracelet/x/windows | v0.2.2 | MIT | extension |
| github.com/clipperhouse/displaywidth | v0.11.0 | MIT | extension |
| github.com/clipperhouse/uax29/v2 | v2.7.0 | MIT | extension |
| github.com/lucasb-eyer/go-colorful | v1.4.1 | MIT | extension |
| github.com/mattn/go-runewidth | v0.0.24 | MIT | extension |
| github.com/muesli/cancelreader | v0.2.2 | MIT | extension |
| github.com/rivo/uniseg | v0.4.7 | MIT | extension |
| github.com/xo/terminfo | v0.0.0-20220910002029-abceb7e1c41e | MIT | extension |
| golang.org/x/sync | v0.22.0 | BSD-3-Clause | extension |

The PiG Go SDK (`github.com/MichaelKinsy/PiG/extensions/sdk`, MIT, its `json` package BSD-3-Clause) is PiG's own and
ships with PiG. When a pin changes, run `go-licenses report` again for the three targets and update this directory.
