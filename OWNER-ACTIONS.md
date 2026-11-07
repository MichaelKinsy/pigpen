# Owner actions: the first release

> **Status on 2026-11-06: the first release was made, so this is now the reference runbook for the owner's release steps, not a to-do list.**
> Piglet Binaries (14 Piglets), Package tags and npm packages are published (see [README.md](README.md), "What is published").
> One step is still open: no verified Piglet receipt is committed under `releases/` (`npm run record -- piglet <name> <version>`), so
> `index.json` lists every Piglet as `planned`. The statements below that something was "not run" describe the state when this file was written.

Everything the repository can do for you is committed: the release workflows, the tag scheme, the checks and the index
generator. What is left needs your GitHub login, a signing key and your decision to push a tag. Run the commands in order,
from a clean checkout of `main` (`git switch main && git pull --ff-only`), with the Node and Go that CI uses. `gh` is the
GitHub CLI logged in as the repository owner. `OWNER=MichaelKinsy REPO=MichaelKinsy/pigpen` below. Run `npm ci --ignore-scripts`
once first: the `node -p "require('yaml')..."` lines and `npm run record`/`generate` need it.

When this file was written, the GitHub-side commands (`gh api`, `gh secret`) had not been run: the lane had no access to the repository. Each one is
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

## 6b. The `pig-acp` executables

`release-pig-acp.yml` attaches prebuilt `pig-acp` executables (the ACP companion of the `acp` Package) to a GitHub Release named by the
Package tag, `components/acp/v<version>`: one archive per PiG release platform (linux, darwin and windows on amd64 and arm64, and
android/arm64), `SHA256SUMS`, `SHA256SUMS.sig` (Ed25519, by the key of section 1, so Piglet users already trust it) and a Sigstore
provenance bundle per archive. The same tag also starts `release-package.yml`; neither needs the other. It needs sections 2 to 4 and one
addition: the `release` environment's tag policy must also allow this tag (`*` does not cross `/`, so `*/v*` does not cover it):

```bash
gh api -X POST "repos/$REPO/environments/release/deployment-branch-policies" -f name='components/acp/v*' -f type=tag
gh api "repos/$REPO/environments/release/deployment-branch-policies" --jq '.branch_policies[] | {name, type}'   # */v*  and components/acp/v*
```

Release (the version is the Package's, which main.go and the agent also carry; the commit must be on `main`):

```bash
git switch main && git pull --ff-only
v=$(node -p "require('./components/acp/package.json').version")
git tag -a "components/acp/v$v" -m "pigpen-acp $v" && git push origin "components/acp/v$v"
sleep 10
gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release-pig-acp.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The run plans (tag, version, main), builds the seven archives twice each (they must be identical), attests them, runs the five that have a
native runner on it, then `publish` (environment `release`) signs `SHA256SUMS`, verifies the signature, the checksums, the archive contents
and every attestation, and only then creates the release. Check it as a user would:

```bash
d="$(mktemp -d)"; cd "$d"
gh release download "components/acp/v$v" --repo "$REPO"
openssl pkeyutl -verify -rawin -pubin -inkey ~/pigpen/release-keys/pigpen-piglets.pub -in SHA256SUMS -sigfile SHA256SUMS.sig
sha256sum -c SHA256SUMS
pig verify --provenance --repo "$REPO" --signer-workflow .github/workflows/release-pig-acp.yml --checksums SHA256SUMS "pig-acp-$v-linux-amd64.tar.gz"
```

Rehearse without GitHub, on a Linux machine with Go (the version in PiG's `.go-version`), Node and OpenSSL: `npm run rehearse:pig-acp -- <dir>` builds
every target twice, checks they are identical, signs with a throwaway key and verifies (android/arm64 needs an NDK and PiG's
`automation/release/android-binary.sh` in `PIG_ANDROID_SCRIPT`). It cannot make the attestations: those need the hosted run.
A release is immutable like a Piglet's (section 7); fix forward with a new Package version.

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

Not proven when this was written, before the first hosted run (which has since succeeded, see the status at the top): GitHub environments and secrets, attestations, artifact upload and download, and the
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

## 8. Discord notifications (optional)

`.github/workflows/discord-notify.yml` announces merged pull requests and published releases in one Discord channel. A
webhook URL is a write credential for that channel, so it is an ENVIRONMENT secret of a `notifications` environment that
only `main` may deploy to, never a repository secret.

```bash
REPO=MichaelKinsy/pigpen
# Create the environment; only main may deploy to it.
gh api -X PUT "repos/$REPO/environments/notifications" --input - <<'JSON'
{ "wait_timer": 0, "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true } }
JSON
gh api -X POST "repos/$REPO/environments/notifications/deployment-branch-policies" -f name=main -f type=branch
# The Discord channel's webhook URL, as an ENVIRONMENT secret.
gh secret set DISCORD_WEBHOOK_URL --env notifications --repo "$REPO"
```

Check it:

```bash
gh api "repos/$REPO/environments/notifications/deployment-branch-policies" --jq '.branch_policies[] | {name, type}'   # main  branch
gh secret list --env notifications --repo "$REPO"   # DISCORD_WEBHOOK_URL
gh secret list --repo "$REPO"                       # must NOT list DISCORD_WEBHOOK_URL
```

Without the secret the workflow posts nothing and the run passes. `scripts/check-workflows.mjs` refuses a
`DISCORD_WEBHOOK_*` secret that is not the value of a step env in a job of this environment, and refuses any trigger other
than a push to `main` and a published release.

## 9. The Archon provider repository

`archon plugin add MichaelKinsy/archon-provider-pig` installs the Archon provider plugin from its own repository, which
`.github/workflows/release-archon-provider.yml` generates from `components/archon-provider-pig` and releases. Archon's tag
grammar has no `/`, so `components/archon-provider-pig/v0.1.0` can never be named in an install, and an untagged install takes a
repository's latest release, which in this monorepo is some other component's. A workflow's own token cannot write to another
repository, so the release job holds a personal access token, as the environment secret `ARCHON_PROVIDER_PIG_TOKEN` of the
`archon-provider-release` environment (never a repository secret: see section 2).

`gh` on this machine may be logged into another host. `gh auth login --hostname github.com` first, as `MichaelKinsy`.

**a. Create the empty public repository.** No README, license or `.gitignore` (the workflow's first push is its first commit,
to `main`), and the `archon-plugin` topic, which lists it in Archon's plugin index:

```bash
gh repo create MichaelKinsy/archon-provider-pig --public \
  --description "Archon agent provider that runs sessions through PiG. Generated from MichaelKinsy/pigpen; change it there."
gh repo edit MichaelKinsy/archon-provider-pig --add-topic archon-plugin
gh repo view MichaelKinsy/archon-provider-pig --json visibility,isEmpty,repositoryTopics --jq '{visibility, isEmpty, topics: [.repositoryTopics[].name]}'
```

The first push creates `main`, and in an empty repository the first branch pushed becomes the default branch, which is
what Archon reads. A repository that already has a commit must have `main` as its default: the workflow refuses any other.

**b. Create the token.** Only the web UI creates a fine-grained token (GitHub, Settings, Developer settings, Personal access
tokens, Fine-grained tokens, Generate new token):

| Field | Value |
|---|---|
| Token name | `pigpen archon-provider-pig release` |
| Resource owner | `MichaelKinsy` |
| Expiration | the longest you will renew (a year); put the date in your calendar, a release fails with 401 after it |
| Repository access | Only select repositories: `MichaelKinsy/archon-provider-pig` (create it first, step a) |
| Repository permissions | **Contents: Read and write** (push `main` and the tag, create the release and upload its assets). Metadata: Read-only is added by GitHub. Nothing else. |
| Account and organization permissions | none |

A deploy key is not enough: it can push but cannot create a release. A classic token with `repo` scope works but reaches
every repository you own; use the fine-grained one.

**c. Add it as a pigpen secret, in its own environment** that only the plugin's tag may deploy to:

```bash
REPO=MichaelKinsy/pigpen
gh api -X PUT "repos/$REPO/environments/archon-provider-release" --input - <<'JSON'
{ "wait_timer": 0, "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true } }
JSON
gh api -X POST "repos/$REPO/environments/archon-provider-release/deployment-branch-policies" -f name='components/archon-provider-pig/v*' -f type=tag
gh secret set ARCHON_PROVIDER_PIG_TOKEN --env archon-provider-release --repo "$REPO"      # paste the token when asked
```

Check it:

```bash
gh api "repos/$REPO/environments/archon-provider-release/deployment-branch-policies" --jq '.branch_policies[] | {name, type}'   # components/archon-provider-pig/v*  tag
gh secret list --env archon-provider-release --repo "$REPO"   # ARCHON_PROVIDER_PIG_TOKEN
gh secret list --repo "$REPO"                                 # must NOT list ARCHON_PROVIDER_PIG_TOKEN
```

**d. Tag.** The tag `components/archon-provider-pig/v0.1.0` already exists on the commit before this workflow was merged,
so a run of it would use no workflow and nothing was released from it. Once the pull request that adds the workflow is merged,
move the tag to the merged `main` (the tag ruleset in section 3 lets an admin delete it), or bump `VERSION` in
`components/archon-provider-pig/src/main.ts` to a new version and tag that:

```bash
git switch main && git pull --ff-only
t=components/archon-provider-pig/v0.1.0
git push origin ":refs/tags/$t" && git tag -d "$t"
git tag -a "$t" -m "archon-provider-pig 0.1.0" && git push origin "$t"
gh run watch --repo "$REPO" "$(gh run list --repo "$REPO" --workflow release-archon-provider.yml --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The run checks the tag against the version the plugin answers, runs Archon's own naming and tag rules against Archon's source,
tests the generated repository, builds the six binaries (`archon-provider-pig-<os>-<arch>[.exe]`, linux, darwin and windows on x64
and arm64), writes `checksums.txt`, makes the linux-x64 binary answer Archon's descriptor check, then commits the repository
to `main` of `MichaelKinsy/archon-provider-pig`, tags it `v0.1.0` and publishes the release. Commits there are made by
`github-actions[bot]`, unsigned; the tag is annotated. A published release is never replaced: a re-run after a failure removes
only a draft left behind, and fails on a tag whose content differs. Release a fix as a new version.

Check it as a user would, with a scratch Archon home:

```bash
gh release view v0.1.0 --repo MichaelKinsy/archon-provider-pig --json assets --jq '.assets[].name'   # six binaries and checksums.txt
( export ARCHON_HOME="$(mktemp -d)"
  archon plugin add MichaelKinsy/archon-provider-pig
  archon doctor providers )      # lists pig (0.1.0)
```
