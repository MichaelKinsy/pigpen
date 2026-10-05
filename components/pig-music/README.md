# pig-music

A full-screen music player for PiG that keeps playing while it is hidden. The target design, from the owner's brief:

- `/music` opens the player over the whole terminal; Esc or `q` hides it and music keeps playing; `/music` again shows
  it as it was (same queue, same track, same position).
- YouTube Music as the source (search, queue, next and previous, seek, volume, pause), played by a detached `mpv`
  with `yt-dlp` resolving tracks, so the queue survives hiding the player and `/reload`.
- The look and key bindings of [Orpheus](https://github.com/Cabritto-Corps/orpheus), reused as far as practical.

**Status: milestones 1 to 7 are built.** `/music` opens the player (search, library, queue, transport, volume, shuffle, repeat,
settings, cover art) over mpv and yt-dlp; the track shows in PiG's footer (or a one-line widget) while it is hidden; `/music
settings` and the quick commands (`/music pause`, `/music vol 50`, ...) work without opening it. The Package is `private`, and no Piglet
selects it. A Piglet Binary that selects it builds only with `PIG_SOURCE_ROOT` set to a PiG source checkout: pig 0.4.0's build
from fetched source fails for Packages with third-party Go modules.

## Polish (milestone 6)

`alt+m` or `/music` opens the player; `/music stop` ends the music; the track shows in PiG's footer while the player is hidden
(`footerStatus`); the music survives `/reload` and is found again with no `/music`; when PiG quits the music stops unless
`stopOnExit` is false (`/reload` and session switches never stop it). Only the PiG that opened the player last stops it: another
PiG, or a `pig -p` / JSON / RPC run, leaves it playing when it exits. A PiG killed by a signal stops nothing (no shutdown event
reaches the extension); `/music stop` or `pigmusic stop` ends that music. Keys added: `s` shuffle (the playing track stays first
and keeps playing; the rest of the queue follows in random order), `l` repeat (off, all, one; with all, `n` on the last track
goes to the first; turning shuffle off needs mpv 0.37), `o` the settings screen (`stopOnExit`, `footerStatus`, saved at once to `settings.json`). `/music`
opens on Search when nothing is playing and the library cannot list anything; the error stays on the Library tab.

## Library and liked songs (cookies, with your yes)

The Library tab lists your liked songs and playlists. That needs your signed-in YouTube session, so pig-music asks first, once
per browser: it uses the system's default browser (detected, never guessed) or the `cookieBrowser` setting (`BROWSER[:PROFILE]`,
yt-dlp's syntax), and yt-dlp reads that browser's cookies **for library listings only**: search, playback and mpv never carry them.
Cookies stay in yt-dlp's memory for each call; pig-music saves none and never sees one. Your answer is stored as the browser name in
`<agent dir>/pig-music/cookie-consent.json`; delete it to withdraw, and sign out of Google in the browser to invalidate the session.
No browser (Termux, headless, an unsupported default) disables the library with the reason. The default browser is asked of the
system: macOS through `osascript` (LaunchServices; a Mac whose default was never changed is Safari), Linux through
`xdg-settings`, Windows through the https UserChoice. Only builds whose cookies yt-dlp reads under that name are mapped; a beta or
developer channel, Opera GX, or a Flatpak or Snap Chromium build is reported with the setting to use (`BROWSER:PROFILE`, where
PROFILE may be the profile's directory). A library listing may take up to 2 minutes (it pages through the whole list).
`pigmusic library-counts` prints how many playlists and liked songs you have, counts only. The library's yt-dlp pages have not yet
been run against a real account.

## Milestone 4a: `/music setup` and `pigmusic doctor`

```text
/music doctor          check this machine (mpv, yt-dlp version and age, JavaScript runtime, audio, one resolve and stream read)
/music setup           the same, then offer to download yt-dlp and Deno into pig-music's own directory, asking each time
pigmusic doctor [--probe] | pigmusic setup      the same from a shell
```

Each problem gets one message and one copy-paste fix for the operating system. The doctor also runs before the first
`/music` and before `pigmusic search`/`play`/`add`; a failure refuses to start, with the fix. Downloads come from the official
yt-dlp and Deno release pages, are checked against the published SHA-256 checksums, go in `<agent dir>/pig-music/bin`, need no
sudo and never happen without a yes; nothing is bundled in this repository. A yt-dlp that pig-music downloaded is kept current
with `yt-dlp -U` (daily, and after a 403). Settings (`settings.json`): `mpvPath`, `ytdlpPath`, `jsRuntime` (yt-dlp's
`--js-runtimes` syntax). `PIG_MUSIC_JS_RUNTIME` overrides `jsRuntime`.

## Milestone 4: the player screen

```text
/music          open the player; q or Esc hides it and the music keeps playing; /music again shows it as it was
/music hello    the milestone 2 diagnostic screen
```

Screens: Search, Library, Player (tab / shift+tab). Keys: `space` play/pause, `n` `p` next and previous, `left` `right` seek 5 s,
`+` `-` volume, `/` search, `enter` play or jump, `a` queue, `x` remove, `[` `]` reorder, `j` `k` `up` `down` move, `?` help,
`q` `esc` `ctrl+c` hide. Built with Bubble Tea v2 and Lip Gloss v2 (MIT; their modules are MIT apart from `golang.org/x/sys` and `x/sync`, BSD-3-Clause) hosted by `teahost`; layout and keys follow Orpheus from reading
only. Needs `mpv` and a current `yt-dlp` (a missing one is named). Every search result, queue row and library track is filled in with its artist and length (and the playing track with its
album) in the background, three yt-dlp runs at a time, never with cookies (a songs search lists titles only; about 3 s per track). The Library tab lists liked songs and playlists after your yes
(above; `y` allow, `n` not now, `r` ask or list again, `enter` open or play, `esc` or `backspace` back). Real-terminal check: `PIG_BIN=pig npm run test:pig-music-player` (tmux, mpv, yt-dlp,
network; sets `PIG_MUSIC_SMOKE=1`).

## Milestone 3: the player core and `pigmusic`

`music` (types, paths, settings, dependency check), `mpv` (JSON IPC client and a Player on mpv's own playlist) and
`ytdlp` (a Source over `yt-dlp --flat-playlist -J`), with a command line to drive them:

```sh
cd components/pig-music/extensions/pig-music/cmd/pigmusic && go build -o pigmusic .   # needs the SDK in a go.work
pigmusic search night drive          # numbered results, remembered for `play <n>`
pigmusic play 2                      # queue the results, start at the second; mpv keeps playing after this exits
pigmusic status | queue | pause | resume | next | prev | seek 30 | volume 50
pigmusic stop                        # the only thing that ends mpv
```

mpv runs detached (its own session) and owns the queue; every command reattaches to it. Needs `mpv` and a current
`yt-dlp` on PATH (or `PIG_MUSIC_MPV` / `PIG_MUSIC_YTDLP`, or `mpvPath` / `ytdlpPath` in `pig-music/settings.json` under
PiG's agent directory); a missing one is named, with how to install it, before anything starts. `PIG_MUSIC_MPV_ARGS=--ao=null`
runs mpv without sound output (it is also the place for an audio device: pig-music's mpv loads no `mpv.conf`, so that an
`ytdl-raw-options=cookies-from-browser=...` line there cannot reach playback). These commands never use browser cookies; only
the library does, see below. Every yt-dlp run (searches, the doctor, `-U` and mpv's hook) passes `--ignore-config`, so cookie, proxy or format options in your own yt-dlp config do not apply to pig-music. Checked against mpv 0.37.0 and yt-dlp 2026.08.19: no JavaScript
runtime is needed today (yt-dlp warns that one will be), see `docs/plan/progress/pig-music.md`. mpv's warnings and
errors (a track that failed to resolve, for example) go to `mpv.log` beside the socket. A songs search lists titles only; the
player fills in artist and length for every row (the command line does not).

Real-program checks: `PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvSmoke -v` (mpv and ffmpeg) and
`PIG_MUSIC_SMOKE=1 PIG_SDK_DIR=<PiG>/extensions/sdk node components/pig-music/tests/player-core.smoke.mjs` (mpv, yt-dlp, network).

## Milestone 2: the hello overlay

```text
/music
```

Opens a full-terminal component that shows the terminal size, echoes every key it receives (decoded name and raw
bytes), redraws once a second from a timer and closes on `q`, Esc or ctrl+c. It exists to prove, in the Go SDK, the
overlay, input and timer paths the player will stand on (see `docs/plan/progress/pig-music.md` for the findings).
Closing it and running `/music` again shows the same key log: the state lives in the extension, not in the overlay.

Layout rules the player inherits: the component returns one line per terminal row, each exactly the render width; the
height comes from `ctx.Height()`, which the host does not push as a redraw, so the component polls it; input arrives as
raw terminal chunks and `internal/keys` names them (legacy, application-cursor, Kitty CSI-u, modifyOtherKeys).

## Requirements

- `mpv` and a current `yt-dlp` on PATH (only `/music hello` works without them).
- An interactive PiG session (TUI), fullscreen or regular. In print, JSON and RPC mode the command only says so.
- PiG 0.4.0 or later; the extension requires the Go SDK `github.com/MichaelKinsy/PiG/extensions/sdk` at `v0.4.0` and
  builds from source on first use (Go toolchain).

## Licence

MIT. This Package contains no Orpheus code. Orpheus is GPL-3.0; the owner chose a fresh Bubble Tea model in this
Package that follows Orpheus's layout and key bindings from reading only, with no Orpheus code, themes or assets copied.
The licences of the Go modules built into `pigmusic` and the extension are in `third_party/licenses`, with WaxTap's and
WaxFlow's own third-party notices (read WaxFlow's about its WMA tables before shipping a binary).

## Tests

```sh
cd components/pig-music/extensions/pig-music
go test -race ./...            # needs the PiG SDK (see scripts/test-go.mjs for the go.work it uses)
PIG_BIN=<pig 0.4.0> npm run test:pig-music   # from the repository root: real pig in tmux, both TUI modes
pig package validate components/pig-music
```

## Speed (milestone 9c)

Search asks YouTube Music directly (one request, with artist, album and length, no cookies: 0.5 s where yt-dlp took 4 s) and falls
back to yt-dlp when that fails; `PIG_MUSIC_SEARCH=ytdlp` forces yt-dlp. A track's missing details come from one small request
(0.2 s, was 2.8 s). The Library remembers what your account answered for the session, lists a long collection 50 rows at a time (the
next page when the cursor nears the end), and `r` asks the account again. `pigmusic doctor --timings` shows where the time goes on
your machine.

## Instant library and no gap (milestone 10a)

The Library asks YouTube Music's own `browse` endpoint, signed like the website signs it (SAPISIDHASH), instead of a yt-dlp run
that opens the whole browser cookie store: the first rows come in one request. The cookies are read once per session, with the
consent and browser you already chose (yt-dlp exports them into a file that has no name, handed over as a file descriptor: on
Linux a memory-only file, on macOS a file unlinked from the per-user temporary directory before yt-dlp writes to it), only the
cookies a browser sends to music.youtube.com are kept, in memory, and they are never written anywhere else. While it reads the
browser's store, yt-dlp copies it into a private temporary directory that is removed afterwards. Choosing another browser or
withdrawing the consent drops them. If the account does not accept them, or YouTube answers in a layout this
version does not know, yt-dlp does the listing as before (`PIG_MUSIC_LIBRARY=ytdlp` forces it). What the last session saw (titles,
artists, album, length, cover URL: no cookie, no browser name) is kept in the data directory, so the Library opens with it at
once, marked "updating...", and the live answer replaces it behind the rows. Withdrawing the consent deletes it.
While a track plays, the stream of the next one is resolved ahead (4 s after it becomes next), and mpv uses that answer when the
track starts: the silence between two tracks went from 3.5-4.3 s to 0.57-0.97 s here (`PIG_MUSIC_PREFETCH=off` turns it off;
Windows and the native engine do not prefetch). A prefetched stream that does not open (the stream URLs are bound to the network
address that resolved them) is resolved afresh the next time. Details and numbers: the progress note, "Milestone 10a".

## Albums, artists and wide listings (milestone 10b)

Track lists show title, artist, album and length, and the album column drops first on a narrow screen (under 90 cells), then the
artist (under 50). Explicit tracks carry an `E`. A search lists albums and artists too: `1` songs, `2` albums, `3` artists, enter
opens one (its tracks, or an artist's top songs), enter there plays, backspace goes back.

## The native engine (M4b)

A second engine with no external program on a desktop: WaxTap resolves the stream, WaxFlow decodes it, oto plays it (all
MIT/Apache, `CGO_ENABLED=0` for macOS, Linux and Windows). `engine` in `settings.json` (or `PIG_MUSIC_ENGINE`) is `auto` (mpv with
yt-dlp when healthy, else native), `mpv` or `native`; Termux is mpv only. The player runs as `pigmusic serve`, a detached daemon on
a local socket (named pipe on Windows), so playback survives `/reload`:

```sh
PIG_MUSIC_ENGINE=native pigmusic play night drive   # starts `pigmusic serve` by itself; PIG_MUSIC_SERVE names the program
pigmusic check                                      # the engine in use and a health report with fixes
PIG_MUSIC_NATIVE_OUTPUT=null ...                    # no sound hardware: a paced software output
pigmusic serve --opener synthetic --output null     # offline test tone through the whole daemon
```

Library and liked songs, PO tokens and age-restricted tracks are not supported by the native engine (they report why).

The engine choice is wired into `pigmusic` only. The `/music` player inside PiG, and its quick commands, still drive mpv whatever
`engine` says (`/music settings` saves it for `pigmusic`), and
`/music doctor` does not show the native engine's health yet (`pigmusic check` does); connecting both is a follow-up. The program
that runs the native player is found through `PIG_MUSIC_SERVE`, then `nativePath`, then a `pigmusic` next to the running program,
then PATH.
**Where the engine lives (milestone 9b).** WaxTap (with its JavaScript engine), WaxFlow and oto are linked into the `pigmusic`
program only (`cmd/pigmusic`, package `native/wax`). The extension, and the `/music` quick commands that run the command line
in-process, link none of it. Inside PiG they drive mpv only (see above), so a PiG session never starts or asks `pigmusic`. What the
native client keeps (the `pigmusic serve` socket client, and `pigmusic native playlist|probe <id>`, a one-shot helper that prints one
versioned JSON document for what needs WaxTap besides playing) is used by the `pigmusic` program itself. So a PiG session that
selects pig-music pays about 1 MB of Binary and no measurable memory, and the native engine needs the `pigmusic` program: build it
from this checkout (`cd components/pig-music/extensions/pig-music/cmd/pigmusic && go build -o ~/.local/bin/pigmusic .`, Go 1.26 or
later) and put it on PATH, or set `PIG_MUSIC_SERVE` or `nativePath` to an absolute path (a relative path is refused: it would
resolve against the working directory). No doctor inside PiG says this yet; `pigmusic check` does when it cannot find a
`pigmusic serve`. A test keeps the engine out of the extension's dependencies.
The audio device (oto) is part of that program only: oto's purego would make the PiG Binary that the extension is fused into (pig-with-batteries) a dynamically linked executable that needs glibc to start.
See `docs/plan/progress/pig-music-native.md`.

## Cover art and the disc (milestone 7a)

The Player screen shows the track's cover, drawn as half blocks (24-bit colour, 256 colours, or a shade ramp without colour;
`NO_COLOR` is honoured), or a braille disc that turns while the music plays and stands still when paused or hidden. The
extension cannot draw terminal images (Kitty, iTerm2, sixel), so none is attempted. Covers are public thumbnails fetched over
HTTPS without cookies and cached under the extension's data directory (20 MiB cap); set `"coverArt": false` in `settings.json`
for the disc only. The footer status has a one-character record spinner while playing.

## Settings from the status line (milestone 7b)

A status item cannot take keys, so `/music settings` opens a small box: volume, shuffle, repeat, engine, library browser, where
the now-playing line shows, and cover art. If another extension replaces PiG's footer (which hides every extension's status), set
`"nowPlaying": "widget"` in `settings.json` (or choose it in that box): the same line, with the spinner, shows above the editor
while the player is hidden. `auto` is the footer for now.

## Quick commands (milestone 7c)

`/music play <query|number>`, `pause`, `resume`, `toggle`, `next`, `prev`, `vol <0-100>`, `now`, `queue`, `shuffle on|off` and
`repeat off|one|all` work without opening the player and answer in one line. They are the `pigmusic` command line's commands run in
the extension, so both behave the same (`pigmusic vol`, `now`, `shuffle`, `repeat` are new too). `play <number>` takes the result
number from your last search, in the player or on the command line. Liking a song is not offered: it would write to your account
(`/music like` says so). Any other word after `/music` gets one line naming the commands; `/music` alone opens the player.
Inside PiG they always use mpv, like the player. Answers are notifications, which are not sent to the model.
number from your last search, in the player or on the command line. Liking a song is not offered: it would write to your account.

## Vibes: a palette from the cover (milestone 8a)

The player is painted with colours taken from the track's cover: a dark gradient background, an accent for the title, disc and
progress bar, and text that always has at least 7:1 contrast against the background (dim text and the accent 4.5:1). Without a
cover the colours come from the artist and title. A change of track cross-fades over about 0.6 s. Colours are drawn only inside the
overlay's own cells; the terminal's background is never touched. `NO_COLOR` or a terminal without colour turns it off, and so does
`"palette": false` in `settings.json`.

## A pulse that follows the music (milestone 8b)

While the player is shown and the music plays, the background brightens a little on each beat, the disc turns faster and a slim
`Level` meter shows the loudness. The levels are real: mpv's `astats` filter (added only while the pulse runs) or, for the native
engine, the PCM it decodes. It runs at most about 15 frames a second and stops completely when paused, hidden or stopped.
`"pulse": false` or `"calm": true` in `settings.json` switch it off (calm also stops the spinning disc and the cross-fade).

## Safety and settings for the colours and motion (milestone 8c)

`/music settings` (or `settings.json`) has `palette`, `pulse`, `calm` (no motion at all) and `lowPower` (slower pulse and disc, no
fade); they apply at once. Colours are drawn only inside the player's own cells, with 24-bit, 256-colour or 16-colour fallbacks;
`NO_COLOR` (or `TERM=dumb`) turns every colour off, the accents included, which become bold. The terminal's background is never
changed: an extension cannot write to the terminal, so there is no "tint the terminal" option. Calm also stills the record spinner
of the footer status and the widget.

In 256 and 16 colours the text keeps its contrast in the colours the terminal draws: text takes the nearest colour that is no
darker, backgrounds the nearest that is no lighter. The 16-colour check assumes xterm's default colours; a theme that changes them
can change the result.
