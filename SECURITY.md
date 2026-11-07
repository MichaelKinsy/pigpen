# Security policy

## Scope and current status

Pigpen holds Piglet compositions, reusable components, and generated catalog
metadata. Signed Piglet Binaries are published as GitHub Releases `<name>/v<version>`
(see the README, "What is published"). Each Binary carries an Ed25519 signature;
the public key is pinned in [`release-keys/pigpen-piglets.pub`](release-keys/pigpen-piglets.pub).
Check that key before you trust a Binary: a signature alone does not prove who holds
the key. No supported binary-version range is advertised. This policy does not claim
a security audit or guaranteed response time.

Report problems with Pigpen's manifests, selected resources, generated metadata,
or future release integrity here. Problems in the core PiG runtime, CLI,
extension host or verification implementation belong to
[MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG); use that project's security
reporting guidance rather than a public core issue for sensitive findings.

## Report privately

Do **not** post credentials, private signing keys, private source, unredacted
sessions or exploit details in a public issue or pull request.

If GitHub's private vulnerability reporting is enabled, use
[Report a vulnerability](https://github.com/MichaelKinsy/pigpen/security/advisories/new).
If that option is unavailable, contact the maintainer at **mrkinsy5@gmail.com**
with a brief description and ask for an appropriate private exchange before
sending sensitive material. Do not assume an email attachment is encrypted.

Provide, without secrets:

- affected Piglet, source commit and PiG version;
- affected platform and whether any release artifact is involved;
- a minimal reproduction and the security impact;
- any safe mitigation you have identified.

Ordinary non-sensitive bugs can use the Pigpen issue templates. Coordinate public
disclosure with the maintainer so a fix or mitigation can be prepared first.

## Safe handling

Review resource provenance and permissions before validating or running a Piglet;
validation may execute extension code. Never place private keys in source,
issues, CI logs, test fixtures or catalog metadata. Future production signing
requires protected keys and PiG's verified monorepo publication path. Current CI
is validation-only and has no signing or publication permissions. Catalog
inclusion, a checksum or a passing validation check is not proof of safety.

Every Go module is scanned with `govulncheck` (version pinned in `scripts/govulncheck.mjs`) on every CI run, with the Go toolchain
of the pinned PiG release (`.go-version`) and the staged PiG SDK. A vulnerable function that a module's code can call fails the run; a
vulnerable dependency that is only imported is reported and does not. The vulnerability database is live, so a new advisory can fail
a run with no change in this repository: raise the toolchain or the dependency, not the gate.
