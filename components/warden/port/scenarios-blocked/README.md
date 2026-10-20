Scenarios that cannot be recorded yet, with the reason.

- `git-*.json` (real git in a workspace repository): `pigeq run` fails its host check, because the host's own `git rev-parse --is-inside-work-tree` and the first `message_end` arrive in a different order under Pi and under PiG's Node runtime (host check: pig-ts differs from pi-ts at event 3). That is a host ordering difference, not normalized away here. The behaviour is covered by `gitstate_test.go` with real git.
- Path rules, arming rules and escalation configs: the port reads `<PIG_HOME>/pigpen-warden/config.json`, a scenario can only write under `<root>/agent` (`agentFiles`) and rejects `PIG_*` in `env`. Harness gap routed to Main; covered by `extension_guardcore_test.go`.
