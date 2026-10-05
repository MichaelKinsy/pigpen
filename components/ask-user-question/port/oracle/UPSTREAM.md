Copy of https://github.com/juicesharp/rpiv-mono at commit 61904e69e1a50e12585bdf15f0310e633a62ba36 (release
v2.11.0, MIT, Copyright (c) 2026 juicesharp): `packages/rpiv-ask-user-question` (package
`@juicesharp/rpiv-ask-user-question` 2.11.0), byte for byte, except that the binary and vector images under `docs/`
(`*.png`, `*.jpg`, `*.svg`) are omitted. Added here, not part of the original: this file, `.npmrc`
(`legacy-peer-deps=true`, so `npm ci` installs only the package's own dependencies and not a second Pi) and
`package-lock.json` (generated from the original's `package.json`). `npm ci --ignore-scripts` in this directory
installs `@juicesharp/rpiv-config` 2.12.0 and `typebox`; `@earendil-works/pi-ai`, `pi-tui` and `pi-coding-agent` are
provided by Pi itself.
