# First npm publish (owner)

Pigpen's Packages and Piglet sources are published to npm under the `@pi-in-go` scope with the keywords `pig-package` and
`pig-piglet`; pi-in-go.dev lists them by keyword. The first publish has to be you: a Trusted Publisher can only be added to a
package that exists. After that, CI publishes every release with no token.

## 1. Publish (one command)

From a clean checkout of `main` (merged, up to date), on macOS or Linux, with Node 22.18 or newer:

```bash
npm login                          # an account that can publish to the @pi-in-go scope
scripts/first-npm-publish.sh --dry-run    # checks everything, runs `npm publish --dry-run` on all 41, publishes nothing
scripts/first-npm-publish.sh              # the real thing
```

The script refuses a dirty tree or a commit that is not on `origin/main`, requires `npm whoami`, runs `npm ci --ignore-scripts`
and `npm run check` (the npm manifest contract, the quality gates, the index), then publishes the 30 Packages and after them
the 11 Piglet sources, skipping any name@version already on npm, and stops at the first failure so no Piglet is published
after a failed Package. Run it again after an interruption. If your npm account asks for a one-time password on writes,
npm will ask at each publish.

What gets published (all `0.1.0`, `access: public`): `@pi-in-go/pigpen-<dir>` for every directory in `components/` and
`@pi-in-go/pigpen-piglet-<name>` for every directory in `piglets/`. What a Piglet source contains and why it differs from the
tree is in the README ("Install").

## 2. Add the Trusted Publisher to each package

The script prints this at the end (`node scripts/npm-publish.mjs --trusted-publishers` prints it again). For each of the 40
packages, on npmjs.com open the package, then Settings, Trusted Publisher, GitHub Actions, and enter:

| Field | Value |
|---|---|
| Organization or user | `MichaelKinsy` |
| Repository | `pigpen` |
| Workflow filename | `npm-publish.yml` |
| Environment name | (leave empty) |

Optionally, in each package's Publishing access, require two-factor authentication and disallow tokens, so trusted
publishing is the only way in.

## 3. Later releases (CI)

Bump the `version` in a Package's `package.json` or a Piglet's `piglet.yaml` (`release.version`; the Piglet's npm version
follows it), merge to `main`, then either push a tag or run the workflow by hand:

```bash
git tag -a npm/v0.1.1 -m "npm release" && git push origin npm/v0.1.1
# or: GitHub, Actions, Publish to npm, Run workflow
```

`.github/workflows/npm-publish.yml` checks that the commit is on `main`, runs `npm run check`, and publishes with provenance
what is not on npm yet. The tag name is only a trigger; each package is published at its own version. A Piglet's signed
Binary is a separate release (`<name>/v<version>`, `release.yml`, [OWNER-ACTIONS.md](OWNER-ACTIONS.md)).
If you use the tag ruleset from OWNER-ACTIONS.md, add `refs/tags/npm/v*` to it.

## What was proven, and what was not

Proven here with PiG 0.4.1, against a local registry that serves the packed tarballs: `pig install npm:@pi-in-go/pigpen-<dir>`
for all 30 Packages (validate, Go extensions build and register, remove), `pig piglet add npm:...` and `pig piglet validate`
for all 11 Piglet sources, and a fused `pig-games` Binary built from the registered source. `npm publish --dry-run` ran for all
40, and `scripts/first-npm-publish.sh --dry-run` ran end to end in a clean clone. Not proven: a real `npm publish`, the scope
permissions of your npm account, trusted publishing and provenance on GitHub (no hosted run), and the pi-in-go.dev catalog's
view of the new packages (it reads npm at its next daily build).
