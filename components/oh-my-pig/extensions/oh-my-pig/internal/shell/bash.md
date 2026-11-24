Persistent shell: one fact command/pipeline; dependencies use `&&`.
Scripts/heredocs/`$(…)`/complex flow → dedicated tool or checked-in script.
`cwd`, not `cd`; `pty` only interactive.
Internal URIs work as paths for builtins/coreutils, redirects, globs.
`async` defers finite results but keeps the deadline (default 300s); `timeout: 0` for watchers and long jobs.
No `head`/`tail`/redirection; output trunc by default, full result at `artifact://<id>`.
Long-lived services: unique name; ready requires name; no async/timeout; pty defaults true. ready needs log regex or port (both if given); host defaults 127.0.0.1, ready.timeout 30s.
Non-`pty` calls still running after 60s (or 1s before `timeout`, if sooner) usually move to background job `bg_N` and keep running to `timeout` (not when the client terminal runs the command or the job cap is reached); the result arrives automatically as a follow-up message. NEVER poll.
