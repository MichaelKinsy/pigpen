Subset of https://github.com/gotgenes/pi-packages at commit c5bc74712cd2fe808d70246d93d26a092f736676, byte for byte: the root files
`package.json`, `pnpm-workspace.yaml`, `pnpm-lock.yaml`, `tsconfig.base.json`, `vitest.config.mjs`, and the directory
`packages/pi-permission-system` (`@gotgenes/pi-permission-system` 39.0.3, MIT; its LICENSE names MasuRii and Christopher D. Lasher).
The other packages of the repository are omitted. Added here, not part of the original: this file.
Install the dev dependencies (Pi 1.0.0 among them) with
`corepack pnpm install --frozen-lockfile --ignore-scripts --filter @gotgenes/pi-permission-system...` in this directory; the
original's own suite then passes (`npx vitest run` in `packages/pi-permission-system`: 177 files, 5418 tests).
