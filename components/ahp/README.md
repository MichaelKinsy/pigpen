# ahp (Go port of pi-ahp)

A Go extension for PiG that serves the **running session** to remote clients over Microsoft's
[Agent Host Protocol](https://github.com/microsoft/agent-host-protocol) (AHP, protocol 0.9.0,
pinned): a WebSocket host with multi-client state, reconnect with replay or fresh snapshots,
token authentication, an origin check, model and session-config discovery, `@`-mentions, the
session catalogue with history paging, and live mirroring of the agent's turns, including prompts
typed locally.

It is a port of **pi-ahp** by Bang Lee (see [CREDITS.md](CREDITS.md)). pi-ahp is a standalone
host that embeds Pi and runs many sessions; a PiG extension lives inside one session, so this port
serves that session (and reads other sessions from their files). See [proof/PORT.md](proof/PORT.md)
for every deviation and gap.

## Safe by default

- **No listener** exists unless you start one: `pig --ahp`, or `/ahp start`. Installing the
  extension opens nothing and creates no file.
- Starting creates `<pig home>/ahp/settings.json` (mode 0600) with a free loopback port and a random
  token, if it is missing. Clients connect to `ws://127.0.0.1:<port>/?token=<token>`.
  A non-loopback `host` without a token is refused; a browser `Origin` is refused unless listed in
  `allowedOrigins`.
- **Remote filesystem, watches, terminals and session deletion are separate opt-ins** in the same
  file, all off unless set:

```json
{
  "port": 41234, "token": "…", "host": "127.0.0.1",
  "allowedOrigins": [],
  "filesystem": { "enabled": true, "roots": [], "unrestricted": false },
  "terminals": { "enabled": false },
  "allowSessionDeletion": false
}
```

With `filesystem.enabled` and no `roots`, access is confined to the session's working directory;
`unrestricted: true` must be said explicitly. A terminal is a shell as you, claimed by the client
that started it. The dev tunnel of pi-ahp is not bundled.

**What a connected client can always do.** The opt-ins above cover the host's own filesystem,
watch, terminal and deletion services. They do not limit the agent. Anyone who holds the token
can do all of the following, so treat the token like a shell login:

- Send prompts to the running session. The agent answers with the session's tools (for example
  `bash`, `read`, `write`, `edit`), as you, in its working directory. Slash commands typed by a
  client are sent to the model as text and are not run.
- Switch the session's model and thinking level.
- List and read the stored history of **every** PiG session under the session directory, from all
  projects, not only the running one.
- Get `@` completions, which list file names under the working directory.

The token is shown in the `/ahp` status and in the "listening" notice.

## Commands and flags

| | |
|---|---|
| `--ahp` | start the listener when the session starts |
| `--ahp-settings <file>` | settings file (default `<pig home>/ahp/settings.json`) |
| `/ahp [status]` `/ahp start` `/ahp stop` | inspect, start or stop the listener |

If the PiG session is replaced (new, resume, fork, reload), the listener closes with it and connected
clients are dropped. With `--ahp` it opens again for the new session on the same port. After
`/ahp start` it stays closed, a warning says so, and `/ahp start` serves the new session.

## Install

```sh
pig install ./components/ahp
```

It builds from source on first use (Go toolchain required), or fuses into a Piglet Binary
(`packages: {ahp: local:../../components/ahp}` in a Piglet). It needs no Node runtime.

## Layout

`extensions/ahp/internal`: `wire` (JSON-RPC, channel URIs, version negotiation), `host` (channel
state, sequencing, replay, connections), `ws` (RFC 6455 server), `mapper` (Pi events to AHP
actions), `pi` (chat driver, session registry, catalogue, hydration, history, models, completions),
`svc` (resources, watches, terminals, PTY), `live` (the running PiG session behind the SDK),
`compose` (wiring), `settings`. The Microsoft AHP Go types and reducers are vendored byte for byte
under `third_party/`. Tests are twins of pi-ahp's 430 test cases (`internal/twin`); `twins_test.go`
fails if one has neither a Go test nor a named skip.
