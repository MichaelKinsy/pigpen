# pi-in-go.dev change: list released Pigpen Packages and Piglets

`0001-site-list-released-Pigpen-Packages-and-Piglets-on-pa.patch` is a `git format-patch` for the PiG platform repository
(the pi-in-go.dev site), made on `main` at `91553f7`. It applies with `git am` (checked on a clean checkout of that commit).
Nothing was pushed.

It adds two sources to `/packages`, **Pigpen Packages** and **Pigpen Piglets**, shown first once they have entries:

- `scripts/build-pigpen-catalog.mjs` (runs in `pnpm generate` after the npm catalog) reads Pigpen's generated `index.json`
  from the repository and ref in `catalog.config.json`, validates it against `data/pigpen-index.schema.json` (a copy of this
  repository's `index.schema.json`), lists only `available` entries and re-derives every command and URL from the entry's own
  name, version and repository, refusing the entry when the index says otherwise. A failed fetch or invalid index keeps the
  committed `data/pigpen-index.snapshot.json`.
- Package cards show `pig install 'git:https://github.com/MichaelKinsy/pigpen.git@components/<name>/v<version>#subdirectory=components%2F<name>'`.
  Piglet cards show `pig piglet pull 'github:MichaelKinsy/pigpen/<name>@<version>'`, the signed platforms and the signing key id
  with a link to the public key. The site hosts no downloads and verifies no signature: Pigpen does that when it records a receipt.
- Before the first release the lists are empty and say so; the snapshot is today's index (all Piglets `planned`).
  After the first release run `pnpm catalog:snapshot` on the site and commit the diff.

Tests: `node --test test/pigpen-catalog.test.mjs` (8 pass). The rest of `test/*.test.mjs` is unchanged (93 pass; 3 need a PiG
checkout and fail here the same way without the patch). `app/` type-checks apart from the generated data files that need
`pnpm generate`. Not run: `next build` and a visual check of the new cards.
