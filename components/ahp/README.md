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
  token, if it is missing. Clients connect to `ws://127.0.0.1:<port>/?token=<token>`; the listener also
  takes the token as `?tkn=`, the spelling Visual Studio Code uses (see below).
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

## Visual Studio Code

**Not verified with a live VS Code.** The listener is tested with the messages VS Code's client sends, as read from VS Code's
source for 1.140.0 and main (the handshake, the `tkn` token, text frames, a dash-free provider id;
`internal/ws/vscode_test.go`), not against VS Code itself. Until the owner has tried it, treat the steps below as the intended path.

VS Code's Agents window (**Chat: Open Agents Window**) can connect to any WebSocket AHP host:

1. Start the listener in the session you want to serve: `pig --ahp`, or `/ahp start`. The notice (and `/ahp status`) starts with
   the address in the form VS Code takes, `ws://127.0.0.1:<port>?tkn=<token>`, then gives the `/?token=` form for other clients.
2. In the Agents window's Command Palette run **Agents: Add Remote Agent Host...** (`sessions.remoteAgentHost.add`), paste that
   address, and name the host. VS Code moves the token out of the address into the entry's `connectionToken`. Or add it to your
   user `settings.json` by hand:

   ```json
   "chat.remoteAgentHosts": [
     { "name": "pig", "address": "ws://127.0.0.1:41234", "connectionToken": "<token>" }
   ]
   ```

3. `chat.remoteAgentHostsEnabled` must be on (it is by default). If you are not signed in to GitHub in VS Code, also set
   `"chat.agentHost.allowSignedOutWhenUsable": true`.

Reaching a host on another machine:

- **SSH port forward** (simplest): `ssh -L 41234:127.0.0.1:41234 <host>`, start the listener there, and use the forwarded
  `ws://127.0.0.1:41234?tkn=<token>` locally. The host keeps its loopback listener, so no token ever crosses the network unprotected
  beyond the tunnel.
- **VS Code's SSH remote agent host**: `chat.sshRemoteAgentHostCommand` (a development setting) replaces the command VS Code runs
  on the remote. VS Code reads the first `ws://127.0.0.1:<port>?tkn=<token>` in the command's output, which is how the notice
  starts. The command has to keep pig running without a terminal: `pig --mode rpc --ahp` prints the notice on standard output at
  once and runs while its input stays open (checked with a plain shell, not yet with VS Code).
- **A dev tunnel** is not bundled (the pi-ahp tunnel feature is not ported), and VS Code's own tunnel support looks for VS Code agent
  hosts, not this one. A tunnel you start yourself can forward the port and give a `wss://` address that **Add Remote Agent Host...**
  accepts, but VS Code sends no tunnel credentials on that connection, so the tunnel has to admit it anonymously and the token is
  the only protection. Not tried.

Debugging: set `"chat.agentHost.ahpJsonlLoggingEnabled": true` to have VS Code log every AHP message as JSON lines under the
window's log directory (it is off by default in stable builds).

What to expect, and the limits that apply to this port:

- VS Code 1.140 offers protocol 0.9.0 first, then 0.7.0 down to 0.5.1; later builds offer 0.10.0 first and still 0.9.0. This host
  speaks only 0.9.0 and negotiates it; a VS Code that stops offering 0.9.0 will report the host as incompatible. A wrong
  or missing token is refused at the upgrade with HTTP 403, as VS Code's own agent host refuses one (pi-ahp answers 401; see
  [proof/PORT.md](proof/PORT.md), deviation 15). A browser `Origin` that is not allowed also gets 403.
- The host serves **the one running session** as one agent (`pi`). Other sessions are readable from their files; they are not
  running agents. If the session is replaced (new, resume, fork, reload) clients are dropped (see Commands and flags).
- No git changeset channels: VS Code's change views will have nothing to show (the changeset service of pi-ahp is not ported).
- The remote filesystem, watches, terminals and session deletion are off until you opt in (see Safe by default).
- No dev tunnel is bundled, and project trust cannot be gated from an extension (see [proof/PORT.md](proof/PORT.md)).

## Other AHP clients

Any client that speaks AHP 0.9.0 over WebSocket text frames can connect with `ws://127.0.0.1:<port>/?token=<token>` (or `?tkn=`).
Two others are named for trying; **neither has been tried against this host**, and what they support has not been checked here:

- **agent-host-protocol-ui**
- **copilot-remote-host**

Two things decide whether a client works: it must offer protocol 0.9.0 (this host does not down-convert from another version), and,
if it runs in a browser, its `Origin` must be listed in `allowedOrigins` in the settings file, because the host refuses a browser
`Origin` it does not know.

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
