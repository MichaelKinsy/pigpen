# Recorded yt-dlp output

Real output of yt-dlp 2026.08.19 (the upstream release binary), recorded on 2026-10-04 with
`--ignore-config --no-warnings --no-progress --flat-playlist -J`, no cookies, no account. They are public search and
playlist listings; nothing personal is in them.

| file | command |
| --- | --- |
| `music-songs.json` | `--playlist-end 5 -J -- "https://music.youtube.com/search?q=night+drive#songs"` |
| `music-search.json` | `--playlist-end 5 -J -- "https://music.youtube.com/search?q=night+drive"` (no section: albums and videos mixed) |
| `ytsearch.json` | `--playlist-end 3 -J -- "ytsearch3:night drive"` |
| `playlist.json` | `--playlist-end 5 -J -- "https://music.youtube.com/playlist?list=PLXIclLvfETS3AgCnZg4N6QqHu_T27XKIq"` |
| `bad-playlist.err` | stderr of the same command for a playlist ID that does not exist (exit 1) |

What they show that the hand-written fixtures in the parent directory did not: a music search entry carries only `id`,
`url` and `title` (no artist, duration or album), a search page with no section starts with `YoutubeTab` browse entries
that have no title, and playlist and `ytsearch` entries carry `channel`, `uploader` and `duration`.
