Copy of https://github.com/juicesharp/rpiv-mono at commit 7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f (release
v2.12.0, MIT, Copyright (c) 2026 juicesharp): `packages/rpiv-web-tools` (package `@juicesharp/rpiv-web-tools`
2.12.0), byte for byte, except that the binary images under `docs/` (`config.jpg`, `cover.png`, `cover.svg`,
`vertical-cover.png`, `vertical-cover.svg`) are omitted. Added here, not part of the original: this file,
`.npmrc` (`legacy-peer-deps=true`, so `npm ci` installs only the package's own dependencies and not a second
Pi) and `package-lock.json` (generated from the original's `package.json`). `npm ci --ignore-scripts` in this
directory installs `@juicesharp/rpiv-config` 2.12.0; `@earendil-works/pi-ai`, `pi-tui`, `pi-coding-agent`
and `typebox` are provided by the host.

All 36 files were verified byte for byte against the pinned commit: each file's git blob hash matches the
tree at that revision (SHA-1 over `blob <len>\0<content>`).
