# Pigpen porter

`pig-porter` **extends** [`pig-extension-porter`](../pig-extension-porter/README.md) with
PiG's `extends` field. Today it adds nothing to it: same Skill, same Go extension, no copy.
It is the umbrella that further porting work (providers, themes, Package conversion) would
be added to. If you port Pi extensions, use `pig-extension-porter` directly.

This is distinct from PiG's core parity-maintenance porter. It does not import that
workbench or operate its parity database.

## Why it extends instead of copying

The task was to make the focused Piglet the base and have the full porter include it.
Checked against a PiG 0.3.0 pre-release build (`63c6ba456`):

- `extends.source: local:../pig-extension-porter/piglet.yaml` validates
  (`1 extension(s), 1 skill(s)` inherited) and **builds a Binary**
  (`pig piglet build ... --format binary`, "Preparing fused Go members —
  extension-equivalence").
- The derived Piglet must repeat `release` and `build`; PiG does not inherit them.
- Limits, recorded in [RELEASE-BLOCKERS.md](../../RELEASE-BLOCKERS.md): `pig piglet add`
  cannot copy an `extends` closure, `extends` with `agentEnv` is not implemented, and a
  remote add of an authored manifest cannot select shared `components/` by sibling path.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-porter/piglet.yaml
```

Staging copies the shared Packages into the base Piglet's generated tree and leaves the
`extends` path (`../pig-extension-porter/piglet.yaml`) valid, because both Piglets are
staged as siblings. Do not run the authored manifests directly, edit staged copies, or
commit `dist/`.

## Status

Source only, no published release. Original composition: [MIT](../../LICENSE).
