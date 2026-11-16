# Getting a capture

REA reads captures; it does not intercept traffic and does not ship a proxy. Three ways to get one, by app kind:

| The app is | Capture with | Notes |
|---|---|---|
| a page in a browser | the browser's network panel, "save all as HAR with content"; or REA's own browser capture | REA's passive capture also records what started each request; see the REA skill's `references/runtime-observation.md` |
| a program you can start with your own environment (a CLI, most desktop apps started from a terminal) | a private mitmproxy, below | sees what honors the proxy setting and trusts the certificate |
| traffic already saved by Fiddler | the `.saz` file as it is | REA reads SAZ natively |

mitmproxy's own flow files are read by REA only with mitmdump 12.2.3 on Linux. Always export a HAR.

## A private mitmproxy for one process

Ask before installing anything. If `mitmdump` is not on `PATH`, say so and let the user choose how to get it (their
package manager, or a throwaway run such as `uvx --from mitmproxy mitmdump`).

Start it with its own directory, on the loopback address, on a free port:

```sh
run=evidence/traffic/run1; mkdir -p "$run/conf"
mitmdump --set confdir="$run/conf" --listen-host 127.0.0.1 --listen-port 8899 \
  --set hardump="$run/session.har" -w "$run/session.flows" \
  --set stream_large_bodies=256k > "$run/mitmdump.log" 2>&1 &
```

- `confdir` makes mitmproxy create its certificate authority there (`conf/mitmproxy-ca-cert.pem`) on first start. It
  is this run's own; nothing else on the machine trusts it.
- `hardump` is written when mitmdump exits. Stop it with an interrupt (`kill -INT`), not a kill.
- `-w` keeps mitmproxy's own flow file, to export parts from later.
- `stream_large_bodies` keeps bodies above the size out of the capture, **requests and responses alike**. Without it one
  large download makes a HAR of hundreds of megabytes, and REA reads at most 33,554,432 bytes. Write the threshold into
  the report: a large upload is then seen as a request without its body.

Start the app in a scratch home where it allows that, with the proxy and the certificate in its environment only:

```sh
ca="$PWD/$run/conf/mitmproxy-ca-cert.pem"
env HTTPS_PROXY=http://127.0.0.1:8899 HTTP_PROXY=http://127.0.0.1:8899 \
    NODE_EXTRA_CA_CERTS="$ca" SSL_CERT_FILE="$ca" CURL_CA_BUNDLE="$ca" GIT_SSL_CAINFO="$ca" \
    the-app its-arguments
```

Which variable a program reads for the certificate depends on what it is built with:

| Runtime or tool | Trusts the certificate through | Uses the proxy setting |
|---|---|---|
| Node.js | `NODE_EXTRA_CA_CERTS` | not by itself: only if the app adds a proxy agent, or with `NODE_USE_ENV_PROXY=1` (Node 24 and later) |
| curl | `CURL_CA_BUNDLE` or `SSL_CERT_FILE` | yes |
| Python | `SSL_CERT_FILE`; the `requests` library reads `REQUESTS_CA_BUNDLE` | yes |
| git | `GIT_SSL_CAINFO` | yes |
| programs that use only the operating system's trust store (many Go and Swift programs on macOS, most mobile apps) | nothing per process | varies |

Tried with mitmproxy 12.3.1: Node 24 (the built-in `fetch` went around the proxy until `NODE_USE_ENV_PROXY=1` was set),
curl, Python's `urllib` and git. Other runtimes have their own variable or none; look it up rather than guess. A
program that starts other programs (an app that runs `git`, a helper binary) needs the variable of each of them, which
is why the command above sets several.

## Then find out what the capture missed

Do both before you read the capture, and put the results into the report.

1. **Refused certificates.** In `mitmdump.log`, a line that starts `Client TLS handshake failed` and names a host
   means something contacted that host and the proxy saw no content. Find which program it was (its certificate
   variable is missing above), add the variable and capture again. If no variable exists for it, the host goes into
   the report as "contacted, content not observed".
2. **Connections around the proxy.** While the app runs, list the sockets of the app and its child processes, and
   compare the remote addresses with the proxy's:

   ```sh
   tree() { echo "$1"; for c in $(pgrep -P "$1"); do tree "$c"; done; }
   lsof -nP -iTCP -a -p "$(tree "$app_pid" | paste -sd, -)"
   ```

   A connection to anything but `127.0.0.1:8899` did not go through the proxy. This check is a sample: a connection
   that opens and closes between two looks is missed. "No direct connection seen in N looks" is what it supports, not
   "none exist".

## One run answers one question

- Capture one named thing per run: starting the app and quitting, signing in, one feature. A run that does everything
  cannot say which action sent what.
- Note the mode: signed in or not, which settings, online or offline. A request that is absent in one mode may exist
  in another; write "not captured in mode X".
- Capture the same run twice before drawing conclusions from one request more or less. What differs between the two
  is timing.
- To tie a setting to a request, change the one setting (a telemetry switch, an offline mode, an update check) and
  capture again. The request that disappears is `observed` to depend on it.

## When the HAR is too large

REA refuses a capture above 33,554,432 bytes and reads nothing of it. Export a part from the flow file instead of
editing the HAR:

```sh
mitmdump -n -r "$run/session.flows" --set confdir="$run/conf" --set hardump="$run/host.har" '~d api.example.com'
```

`~d` selects a domain; mitmproxy's filter expressions also select by method (`~m POST`) or path (`~u /v1/`). Read each
part, and say in the report which parts were read. A part that was not read supports no claim.
