Copy of https://github.com/juicesharp/rpiv-mono at commit 7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f (release
v2.12.0, MIT, Copyright (c) 2026 juicesharp): `packages/rpiv-web-tools` (package `@juicesharp/rpiv-web-tools` 2.12.0),
byte for byte, except that the binary images under `docs/` (`cover.png`, `config.jpg`, `vertical-cover.*`) are
omitted. Added here, not part of the original: this file, `.npmrc` (`legacy-peer-deps=true`, so `npm install` fetches
only the package's own dependency and not a second Pi) and `package-lock.json` (generated from the original's
`package.json`). `npm ci --omit=dev --ignore-scripts` in this directory installs `@juicesharp/rpiv-config` 2.12.0 and
`typebox`; `@earendil-works/pi-ai`, `pi-tui` and `pi-coding-agent` are provided by Pi itself.
