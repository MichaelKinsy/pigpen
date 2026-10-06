# Owner actions: the first release

Everything the repository can do for you is committed: the release workflows, the tag scheme, the checks and the index
generator. What is left needs your GitHub login, a signing key and your decision to push a tag. Run the commands in order,
from a clean checkout of `main` (`git switch main && git pull --ff-only`), with the Node and Go that CI uses. `gh` is the
GitHub CLI logged in as the repository owner. `OWNER=MichaelKinsy REPO=MichaelKinsy/pigpen` below. Run `npm ci --ignore-scripts`
once first: the `node -p "require('yaml')..."` lines and `npm run record`/`generate` need it.

The GitHub-side commands (`gh api`, `gh secret`) were not run: this lane has no access to the repository. Each one is
followed by a read that shows the result, so you can check it. Everything that runs locally was run against throwaway keys and
a local bare repository (`npm run rehearse:release`, `npm run test:packages`).

> **npm first.** The primary install channel is npm (`pig install npm:@pi-in-go/pigpen-<name>`, keyword `pig-package` /
> `pig-piglet`): the owner's one command and the Trusted Publisher steps are in [FIRST-NPM-PUBLISH.md](FIRST-NPM-PUBLISH.md).
> The Git tags and signed Binaries below stay available. Add `refs/tags/npm/v*` to the tag ruleset in section 3.

## What is decided

| | |
|---|---|
| Packages | Install from a Git tag with `pig install 'git:https://github.com/MichaelKinsy/pigpen.git@components/<name>/v<version>#subdirectory=components%2F<name>'`. Tag: `components/<name>/v<version>` (two slashes: it can never start the Piglet workflow). Every `components/*` is a Package; `pig-play`, `typesafe` and `pi-typesafe-api` are Go libraries and say so. No npm scope is needed. |
| Piglets | Signed per-platform Binaries only (no remote source add: RELEASE-BLOCKERS.md item 1). Tag: `<name>/v<release.version>`. `pig piglet pull 'github:MichaelKinsy/pigpen/<name>@<version>'`. |
| Build | GitHub Actions only, each target on a native runner: linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64. No local build is part of a release. |
| Signing | One Ed25519 key for all Piglets, held only as the secret `PIGLET_SIGNING_KEY` of the `release` environment. |
| Index | `index.json` is generated. A Package is `available` once its tag is recorded; a Piglet is `available` once its signed receipt, verified against the key committed in `release-keys/`, is recorded. Nothing else changes a status. |

Two limits of PiG 0.4.x shape the workflow (details at the end):

1. **The key is read in every build job and in the publish job, not once.** A Piglet Binary carries its own signature, and
   `pig piglet publish --artifacts` accepts only Binaries already signed by the same key. There is no `pig piglet sign`.
   `npm run quality` enforces that the key reaches only a step that runs `"$PIG_BIN"`, never a repository script.
2. **There is no android Binary.** PiG's native builder builds only the machine it runs on and GitHub has no android runner.
   The release runs an android/arm64 compile-and-vet job as a gate instead.

## 1. Signing key (done)

The key exists and its public half is committed as `release-keys/pigpen-piglets.pub`, key id
`ed25519:fc4fbe0a1ba0f640a360bfd5a1c98874`. The private key is the secret `PIGLET_SIGNING_KEY` of the `release` environment
(checked environment-only). Nothing to do here, except: keep a copy of the private key in your password manager, and never
commit it (`npm run quality` refuses private key material anywhere in the tree).

Check what the repository pins: `node -e 'import("./scripts/receipts.mjs").then(m=>console.log([...m.readPinnedKeys(".").keys()]))'`
prints that id. Publish the same file in the README when you announce the release. Users run
`pig piglet trust add release-keys/pigpen-piglets.pub`; without it, pig pins the signer on first pull. To rotate, add the new
`.pub` beside this one before the first release it signs; old receipts keep verifying against the old key.

## 2. The `release` environment and its secret

```bash
REPO=MichaelKinsy/pigpen
# Create the environment; only tags may deploy to it.
gh api -X PUT "repos/$REPO/environments/release" --input - <<'JSON'
{ "wait_timer": 0, "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true } }
JSON
gh api -X POST "repos/$REPO/environments/release/deployment-branch-policies" -f name='*/v*' -f type=tag
# The key is an ENVIRONMENT secret. Never a repository or organization secret.
gh secret set PIGLET_SIGNING_KEY --env release --repo "$REPO" < ./pigpen-release.key
shred -u ./pigpen-release.key 2>/dev/null || rm -P ./pigpen-release.key 2>/dev/null || rm ./pigpen-release.key
```

Check it:

```bash
gh api "repos/$REPO/environments/release" --jq '{policy: .deployment_branch_policy, protection: [.protection_rules[].type]}'
gh api "repos/$REPO/environments/release/deployment-branch-policies" --jq '.branch_policies[] | {name, type}'   # */v*  tag
gh secret list --env release --repo "$REPO"        # PIGLET_SIGNING_KEY
gh secret list --repo "$REPO"                      # must NOT list PIGLET_SIGNING_KEY
gh secret list --org MichaelKinsy                  # must NOT list it either (skip for a personal account)
```

Why environment-only: a job that names a missing environment gets a new one with no protection, and would read a repository
secret of the same name.

Required reviewers: optional. Each build job (five targets) and the publish job is a separate deployment, so one release
asks six approvals. The tag ruleset below already limits who can start a release. To require a reviewer anyway, add
`"reviewers": [{"type": "User", "id": <your numeric id>}]` to the `PUT` above (`gh api users/MichaelKinsy --jq .id`).

## 3. Who may create release tags

```bash
gh api -X POST "repos/$REPO/rulesets" --input - <<'JSON'
{ "name": "release tags", "target": "tag", "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/*/v*", "refs/tags/components/*/v*", "refs/tags/npm/v*"], "exclude": [] } },
  "rules": [ { "type": "creation" }, { "type": "update" }, { "type": "deletion" }, { "type": "non_fast_forward" } ],
  "bypass_actors": [ { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" } ] }
JSON
gh api "repos/$REPO/rulesets" --jq '.[] | {name, target, enforcement}'
```

Only a repository admin (role 5) can create, move or delete a tag matching those patterns. A tag starts a signing run, and a
released tag must never move.

## 4. Actions settings

```bash
gh api -X PUT "repos/$REPO/actions/permissions/workflow" -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
gh api "repos/$REPO/actions/permissions/workflow"
```

The workflows ask for the writes they need per job (`contents: write` only in `publish`, `id-token` and `attestations` only
in `build`), so the default stays read-only. Attestations need a public repository (it is).

## 5. Release the Packages first

Packages need no secret and no environment. `release-package.yml` checks the tag against the manifest and the history of
`main`, validates the Package, then installs it from the pushed tag the way a user does. Push **one tag at a time**: GitHub
starts no workflow when more than three tags arrive in one push.

One Package:

```bash
n=herdr
v=$(node -p "require('./components/$n/package.json').version")
git tag -a "components/$n/v$v" -m "pigpen-$n $v"          # -s to sign, if you have a signing key configured
git push origin "components/$n/v$v"
sleep 10    # let the run appear, or `gh run list --limit 1` returns the previous one
gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release-package.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

Every component, stopping at the first failure (a Package whose tag already exists, such as the one above, is skipped):

```bash
for n in $(ls components); do
  [ -f "components/$n/package.json" ] || continue
  v=$(node -p "require('./components/$n/package.json').version")
  git rev-parse -q --verify "refs/tags/components/$n/v$v" >/dev/null && continue
  git tag -a "components/$n/v$v" -m "pigpen-$n $v" && git push origin "components/$n/v$v" || break
  sleep 10
  gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release-package.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status || break
done
```

Then record what is released and let the index list it:

```bash
git fetch --tags
for n in $(ls components); do
  [ -f "components/$n/package.json" ] || continue
  npm run record -- package "$n" "$(node -p "require('./components/$n/package.json').version")"
done
npm run generate && npm run check            # index.json now lists each Package as available
git switch -c record-packages && git add releases index.json && git commit -s -m "releases: the first Package tags"
```

Merge that. The site (pi-in-go.dev) lists them at its next daily build once the site change in `docs/site-change/` is applied.

## 6. Then the Piglets

Needs sections 2 to 4 done and the key (section 1) merged. The version in the tag must equal `release.version` in `piglets/<name>/piglet.yaml`
(currently `0.1.0` for ten of them and `0.1.1` for `pig-with-batteries`, re-released for the websearch image fix), and the tagged commit must be on `main`.

```bash
git switch main && git pull --ff-only     # tags are made at HEAD, and the tagged commit must be on main
n=herdr; v=$(node -p "require('yaml').parse(require('fs').readFileSync('piglets/$n/piglet.yaml','utf8')).release.version")
git tag -a "$n/v$v" -m "$n $v" && git push origin "$n/v$v"
sleep 10
gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The run: `plan` (tag and main), `android-check`, five native `build` jobs (each builds, signs and attests its Binary), then
`publish`, which creates one release `herdr/v0.1.0` with the five Binaries, `SHA256SUMS` and `piglet-release.json`. Approve the
deployments if you required reviewers.

Check the release as a user would, with a scratch home (in a subshell, so your own `HOME`, and with it `gh`'s login,
is untouched afterwards; run it from the repository root):

```bash
( export HOME="$(mktemp -d)"; export PIG_HOME="$HOME/.pig"
  pig piglet trust add release-keys/pigpen-piglets.pub
  pig piglet pull 'github:MichaelKinsy/pigpen/herdr@0.1.0'
  pig piglet list )
# Optional Sigstore provenance check of a downloaded Binary (uses your gh login, contacts GitHub):
d="$(mktemp -d)"
gh release download herdr/v0.1.0 --repo MichaelKinsy/pigpen --pattern 'pig-herdr-linux-amd64' --dir "$d"
pig verify --provenance --repo MichaelKinsy/pigpen --signer-workflow .github/workflows/release.yml "$d/pig-herdr-linux-amd64"
```

Record the receipt (downloaded with `gh`, verified against `release-keys/`, written byte for byte), regenerate, commit:

```bash
npm run record -- piglet herdr 0.1.0
npm run generate && npm run check
git switch -c record-herdr && git add releases index.json && git commit -s -m "releases: herdr 0.1.0"
```

Merge `record-herdr` first. All eleven, one at a time (`pig-with-batteries` last: it selects the most Packages; a Piglet whose tag already exists,
such as herdr above, is skipped):

```bash
git switch main && git pull --ff-only
for n in $(ls piglets | grep -v '^pig-with-batteries$') pig-with-batteries; do
  v=$(node -p "require('yaml').parse(require('fs').readFileSync('piglets/$n/piglet.yaml','utf8')).release.version")
  git rev-parse -q --verify "refs/tags/$n/v$v" >/dev/null && continue
  git tag -a "$n/v$v" -m "$n $v" && git push origin "$n/v$v" || break
  sleep 10
  gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status || break
  npm run record -- piglet "$n" "$v" || break
done
npm run generate && npm run check
git switch -c record-piglets && git add releases index.json && git commit -s -m "releases: the first Piglets"
```

Two Piglets with the same version (`herdr/v0.1.0`, `a2a/v0.1.0`) pull and update independently by design (`--tag-prefix`).

## 7. If a run fails

A release is immutable: `pig piglet publish` refuses an existing release, and the receipt is recorded byte for byte. A run that
fails before `publish` created the release can be re-run from the Actions page. After the release exists, fix forward with a
new version (`release.version` in the manifest, a new tag). Only if nothing was recorded and nobody pulled it:
`gh release delete herdr/v0.1.0 --cleanup-tag --repo "$REPO" --yes` (the tag ruleset lets admins delete it), then tag again.

## 8. Still unproven, and follow-ups

Proven locally with PiG v0.4.1, throwaway keys and a stub `gh`: the workflow's own steps for `plan`, `android-check`, one
native `build` and `publish` (a Binary altered after signing, a second publish of the same tag and a tag that is not the
manifest version are all refused; the key file is gone after each step and the key is in no log); a receipt written by pig
verifies in `scripts/receipts.mjs`; every Package installs from their tags with the production command.

Not proven until the first hosted run: GitHub environments and secrets, attestations, artifact upload and download, and the
four other targets (linux/arm64, darwin/arm64, darwin/amd64, windows/amd64 have never run: a Binary starting and finishing a
faux turn there is the first evidence, via the platform matrix in `ci.yml` on `workflow_dispatch`). Run that matrix once
before the first tag.

Follow-ups for PiG (none blocks a release):

- **Sign once.** A `pig piglet sign <binary>` (or a detached signature that `publish --artifacts` accepts) would let the
  build jobs run with no key and one job sign every Binary and the index. Today the key is in six steps.
- **Keyless signing.** PiG could accept a GitHub OIDC identity instead of a stored key: the workflow's Fulcio certificate
  (repository, workflow, ref) as the signer, with the Rekor entry as proof. The workflow already attaches a Sigstore build
  provenance attestation (`actions/attest-build-provenance`), and `pig verify --provenance --repo MichaelKinsy/pigpen
  --signer-workflow .github/workflows/release.yml <binary>` checks it with `gh`, online and optional. What PiG would need:
  a Binary signature block that can carry a certificate chain and a Rekor inclusion proof, a trust store keyed by
  identity (repository and workflow) as well as by key id, and pull and update pinning by identity. Offline startup
  verification would then rest on the certificate chain, not on a key you must keep. That removes the secret, the
  environment and the key-rotation story. Until PiG has it, the stored key and the attestation are complementary.
- **android/arm64 Binary.** A GitHub android runner does not exist; the native builder would need a cross target (a fused
  Go Binary is a plain cross-compile with cgo off, which `scripts/cross-check.mjs` already proves compiles).
- **Source add.** `pig piglet add` from this repository still fails for composed Piglets (RELEASE-BLOCKERS.md item 1).
