# yt-dlp fixtures

These files are **hand-written to the documented shape** of `yt-dlp --flat-playlist -J` output (a playlist
object with `entries`, each a `_type: "url"` object), to cover shapes a short recording does not (private
and duplicate entries, playlist and channel hits, `track`/`artists`/`album` fields, a missing duration). They are not recordings. The titles, channels and IDs are invented. The parser's tests run against both.

| file | what it stands for |
| --- | --- |
| `search-music.json` | `yt-dlp --flat-playlist -J "https://music.youtube.com/search?q=..."`: videos plus a playlist and a channel hit, a private video, a duplicate, a missing duration |
| `search-ytsearch.json` | `yt-dlp --flat-playlist -J ytsearch5:...`: the fallback, channel names with the `- Topic` suffix |
| `playlist.json` | `yt-dlp --flat-playlist -J "https://music.youtube.com/playlist?list=..."` |
| `video.json` | `yt-dlp -J <video URL>` (no `--flat-playlist`): one video with `track`, `artists`, `album` |
| `error-sign-in.txt` | stderr of a failed run |
