# Credits

The `pigpen-pi-extension-port` Skill adapts the PiG Porter procedure and
TypeScript-to-Go translation rules from PiG.

Authors and copyright attribution: Michael Kinsy.
Source repository: https://github.com/MichaelKinsy/PiG.

Reviewed revision: `d86eb93f217e64b655e9ee6f48c93a9afd107697`.

- [PiG Porter Skill](https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/piglets/porter/skills/pig-porter/SKILL.md)
- [TypeScript-to-Go porting](https://github.com/MichaelKinsy/PiG/blob/d86eb93f217e64b655e9ee6f48c93a9afd107697/docs/typescript-to-go-porting.md)

License: [MIT](LICENSE). Pigpen's adaptation removes core-repository assumptions
and adds Package distribution and extension-specific completion checks. It does
not copy or replace PiG's core parity engine.

The L-rules in the Skill are those of PiG's 0.99 port progress record
(`docs/plan/upgrade-0.99.1-progress.md` in PiG `v0.4.0`, same authors and license).
The fake-host and fake-CLI test patterns come from Pigpen's herdr Go port
(`pigpen-herdr-go`), and the equivalence workflow from Pigpen's `extension-equivalence` Package.
