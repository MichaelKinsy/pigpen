Scenarios that cannot be recorded yet, with the reason.

- `git-*.json` (real git in a workspace repository): `pigeq run` fails its host check, because the host's own `git rev-parse --is-inside-work-tree` and the first `message_end` arrive in a different order under Pi and under PiG's Node runtime (host check: pig-ts differs from pi-ts at event 3). That is a host ordering difference, not normalized away here. The behaviour is covered by `gitstate_test.go` with real git.
- Escalation configs need a judge; they are covered by the fake-judge tests. Path-rule and arming scenarios moved to `port/scenarios` once pigeq had `homeFiles`.
