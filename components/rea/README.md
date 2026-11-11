# rea

REA ([Reverse Engineer Anything](https://github.com/morluto/rea), `rea-agents` by morluto, MIT) for PiG, as the
members of the [`pig-rea`](../../piglets/pig-rea/README.md) harness:

- **`rea`** unpacks the REA payload embedded in the Piglet Binary into `<agent dir>/rea/<sha256[:16]>/` on the first start
  of a REA version (about 2 s on an M-series Mac; later starts only read a sentinel) and registers REA's MCP server the way
  `rea setup --client pi` does: `{"type":"stdio","command":<node>,"args":[<rea.mjs>,"mcp"]}`, with the embedded Node in
  front of `rea.mjs` instead of the `node` on PATH. No environment, exposure or description is set. The one addition is the
  tool deadline: Pi's config has no such field, and PiG's default (60 s) cancels REA's longer analyses, so the payload
  records the `tool_timeout_sec` REA's own `rea setup` writes for clients that have one (450 s for 6.8.0, from
  `rea setup --client codex` at payload build) and the member registers with it. `/rea-runtime status`,
  `/rea-runtime verify` (every unpacked file against the embedded archive) and `/rea-runtime gc` (old versions).
- **`rea-dashboard`** is the UI; see [its README](extensions/rea-dashboard/README.md). It only observes: it never sets REA's
  environment, never changes the registration and never opens a second REA session.

## The payload

[`rea.lock.json`](rea.lock.json) pins REA (version, tag, commit, npm integrity, the sha256 of its tagged
`package-lock.json`, copied to [`locks/`](locks/)) and Node 24.22.2 (each target's archive sha256, from Node's signed
`SHASUMS256.txt`). [`scripts/payload.mjs`](scripts/payload.mjs) builds one target's payload from those pins and nothing
else:

1. `npm pack rea-agents@<version>`, checked against the pinned integrity.
2. The tagged lockfile, checked against its digest and the tarball's dependencies, then
   `npm ci --omit=dev --ignore-scripts --no-bin-links` for the target's OS and CPU, and `npm audit signatures`.
3. `bin/node` (or `node.exe`) and `LICENSE` from the pinned Node archive, checked against its sha256.
4. A deterministic tar (sorted, uid/gid 0, mtime 0) of `node/`, `rea/` and `payload.json`, compressed with
   `zstd -19 --long=27`, into `extensions/rea/payload/<os>_<arch>.tar.zst`, plus `<os>_<arch>.json` with its digests.
   Payloads for other targets are removed, so a Binary embeds only its own.

The REA code is the published package as is. darwin/arm64: 41.9 MB compressed, 8,965 files and 270.6 MB unpacked. Two
builds produce the same archive digest. The payload is not committed; build it before `pig piglet build`:

```sh
npm run payload:rea                 # this machine's target; or: node components/rea/scripts/payload.mjs linux/amd64
npm run build:piglet -- pig-rea
```

A Binary built without a payload starts, says it carries no REA payload for its target, and registers nothing.
`REA_SOURCE=/path/to/rea npm run payload:rea` packs a local REA checkout instead (it must have `dist/`); that payload
reports `REA <version> dev <commit>` and is never released.

The Package also carries the Piglet's two skills ([`skills/`](skills/)): `pigpen-derive-a-harness`, and REA's own skill as
`pigpen-reverse-engineer-anything`, which `npm run payload:rea -- --sync-skill` writes from the pinned package: the published
files byte for byte, except the frontmatter name, which Pigpen's skill rule prefixes ([`scripts/skill.mjs`](scripts/skill.mjs)).
A payload build fails when the copy has drifted.

## Gates

- `npm run test:rea` (needs the payload, network and `zstd`): REA from the payload and REA as `npx rea-agents@<pin>`
  installs it list the same tools and prompts (sha256 of the sorted lists, under both input-schema profiles);
  `rea setup --client pi` writes exactly `{type, command: rea.mjs, args: ["mcp"]}`, which is what `rea` registers with the
  Node in front; the skill copy has not drifted.
- `PIG_REA_BIN=… PIG_REA_SEALED_BIN=… npm run test:rea-harness`: the built Binaries against a scripted local model. REA's
  229 tools reach a codemode script and `binary_session` answers; a plain run loads no ambient extension or skill and the
  widening overlay brings them back; `-ns` keeps the Piglet's skills; `-ne` and `--no-mcp` drop REA and
  `-ne -e builtin:mcp -e builtin:codemode -e builtin:tool-search` restores it; an overlay can remove `rea-dashboard`; the
  sealed Binary refuses `mcp.json`, settings extensions, `-e` and `--piglet` and still connects REA. Two cases are marked
  `todo` for a PiG bug: when the project has config that needs a trust decision (for example `.pig/mcp.json`), PiG's
  pre-trust pass loads user extensions with a fixed `["user"]` scope (`coding/cli/cli_runtime_build.go:281`), ignoring the
  Piglet's `discovery` and a closed Binary.
- `go test ./...` in `extensions/rea`: unpack, reuse, a racing unpack, unsafe archive entries, digests, `verify` and `gc`,
  plus the real payload when this checkout has one.

## Licenses

The members are MIT ([LICENSE](LICENSE)). The payload redistributes REA (MIT, [licenses/rea-agents-LICENSE](licenses/rea-agents-LICENSE),
with its own `THIRD_PARTY_NOTICES.md`), its production dependencies with their own license files, and Node.js (MIT with
bundled notices, [licenses/node-LICENSE](licenses/node-LICENSE)). See [CREDITS.md](CREDITS.md).
