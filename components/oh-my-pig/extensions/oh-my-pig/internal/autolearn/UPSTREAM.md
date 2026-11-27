# Upstream Reference: `internal/autolearn`

Port of omp's managed-skills primitives and `manage_skill` tool (`https://github.com/can1357/oh-my-pi`, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/autolearn/managed-skills.ts:1-266` → `managed_skills.go`, `open_unix.go`, `open_windows.go`, `open_other.go` (`ManagedSkillsProviderID`, `MaxManagedSkillBytes`, `ManagedSkillsDir`, `SanitizeSkillName`, `IsValidManagedSkillName`, `SanitizeManagedDescription`, `ToSkillFrontmatter`, `WriteManagedSkill`, `DeleteManagedSkill`, per-skill mutex serialization, symlink/hardlink/broken-root safety checks).
- `packages/coding-agent/src/autolearn/settings.ts:10-38` → `SettingEnabled` (`autolearn.enabled`, default `false`), `SettingAutoContinue` (`autolearn.autoContinue`, default `false`), `SettingMinToolCalls` (`autolearn.minToolCalls`, default `5`) in `managed_skills.go` + `internal/settings/schema.json`.
- `packages/coding-agent/src/autolearn/controller.ts:1-153` → `controller.go` (`BuildInstructions`, `AutoLearnController`, per-session `toolCalls`/`turnStartedInGoalMode`/`captureInFlight`/`capturePending` state, `lifecycle.OnEnd("autolearn", ...)`, `EventAgentStart`/`EventToolResult`/`EventAgentEnd`/`OnSessionShutdown` hooks, live settings re-read, abort/plan/goal/subagent/re-entrancy guards).
- `packages/coding-agent/src/prompts/system/autolearn-guidance.md` → `prompts/autolearn-guidance.md` (embedded verbatim).
- `packages/coding-agent/src/prompts/system/autolearn-nudge-autocontinue.md` → `prompts/autolearn-nudge-autocontinue.md` (embedded verbatim).
- `packages/coding-agent/src/tools/manage-skill.ts:1-104` + `packages/coding-agent/src/tools/index.ts:788-789,846-850` → `tool.go` (`Register`, `ExecuteManageSkill`, `ManageSkillParams`, authored-skill collision check, `intent.Tool` + `intent.RegisterSync` + `schemaorder.Apply` + `toolcmd.RegisterAutoActivate`, `approval.TierWrite` already mapped in `internal/approval/approval.go:1368`).
- `packages/coding-agent/src/discovery/builtin.ts:343-366` → `tool.go` (`EventResourcesDiscover` handler contributing `<agentDir>/managed-skills` in `skillPaths` when the directory exists, using `settings.AgentDirForContext`).
- `packages/coding-agent/src/prompts/tools/manage-skill.md` → `prompts/manage-skill.md` (embedded verbatim).

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `autolearn.enabled` | `false` | Yes (via `Enabled`) | Gates `manage_skill` activation, `BuildInstructions` standing guidance, and post-stop capture; default-off keeps the default system prompt and tool surface unchanged. |
| `autolearn.autoContinue` | `false` | Yes (via `Enabled`) | When `true` alongside `autolearn.enabled`, triggers a post-stop hidden `followUp` capture turn after eligible turns. |
| `autolearn.minToolCalls` | `5` | Yes (via `Setting`) | Minimum `tool_result` count in a turn before post-stop capture triggers. |

## Not Ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| `learn` tool and `autolearn-guidance-learn.md` addendum | `packages/coding-agent/src/tools/learn.ts:1-160`, `prompts/system/autolearn-guidance-learn.md` | Requires omp's `memory.backend` (`mnemopi` / `hindsight` / `local` memory store), which is not ported. |
| Kernel-atomic `O_NOFOLLOW` on Windows | `packages/coding-agent/src/autolearn/managed-skills.ts:138-160` | Windows lacks POSIX `O_NOFOLLOW`; `open_windows.go` checks `os.Lstat` (`os.ModeSymlink \| os.ModeIrregular` for symlinks/junctions) before open and verifies `syscall.GetFileInformationByHandle` `NumberOfLinks > 1` on the opened handle. Unix builds (`open_unix.go`) enforce both `O_NOFOLLOW` and `st.Nlink > 1`. |
| Automatic mid-session host skill registry refresh after mutation | `packages/coding-agent/src/tools/manage-skill.ts:50,60,96` | PiG's extension SDK does not expose a `refreshSkills()` host RPC; `RefreshSkills` hook is exported for future host wiring, and `<agentDir>/managed-skills` is discovered automatically via `resources_discover` on session startup and `/reload`. |
