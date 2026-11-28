# Upstream Reference: `internal/memory`

Port of omp's memory backends (`local`, `mnemopi`, `hindsight`), memory tools (`retain`, `recall`, `reflect`, `memory_edit`, `learn`), and `memory://` internal URL handler (`https://github.com/can1357/oh-my-pi`, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/memory-backend/settings.ts:12-40` + `global-scope.ts:23-32` → `settings.go` (`BackendFor`, `RetainRecallReflectActive`, `MemoryEditActive`, `LearnActive`, `IsGlobalMemoryScopeAvailable`).
- `packages/coding-agent/src/memory-backend/redact.ts:1-149` + `memories/index.ts:1469-1492` → `redact.go` (`RedactMemorySecrets`, `NeutralizeInjection`, `NormalizeLearnedText`).
- `packages/coding-agent/src/memories/index.ts:1444-1588` + `memory-backend/local-backend.ts:19-47` → `local.go` (`SaveLearnedLesson` → `<agentDir>/memories/<encodedCwd>/learned.md`, newest-first bullet list, deduplication, 100-entry cap, per-path mutex + atomic temp-file rename, injection neutralization + secret redaction on write and read, `buildLocalInstructions`).
- `packages/coding-agent/src/mnemopi/{settings,config,state}.ts` → `mnemopi.go` (`LoadMnemopiConfig`, per-bank atomic JSON store under `<dbDir>/banks/<bank>.json`, `working`/`episodic`/`fact` stores, `MnemopiRemember`, `MnemopiRecall`, `MnemopiGetScopedMemory`, `MnemopiEditScopedMemory`, `FormatMnemopiRecallWithIDs`, `FormatMnemopiReflectContext`, optional OpenAI-compatible HTTP embeddings via `mnemopi.embeddingApiUrl`).
- `packages/coding-agent/src/hindsight/{settings,config,bank,client,content,outbox,state}.ts` → `hindsight.go` (`LoadHindsightConfig`, `ComputeHindsightBankScope`, pure `net/http` `HindsightClient`, `FormatHindsightMemories`, durable JSONL outbox under `<agentDir>/memories/hindsight/` with tag-grouped batch flush).
- `packages/coding-agent/src/tools/memory-{retain,recall,reflect,edit}.ts` + `tools/learn.ts` → `tools.go` (`Register`, `ExecuteRetain`, `ExecuteRecall`, `ExecuteReflect`, `ExecuteMemoryEdit`, `ExecuteLearn`, `BuildMemoryPromptInstructions`, `DefaultActive: false` + `toolcmd.RegisterAutoActivate` + `intent.Tool` + `intent.RegisterSync`).
- `packages/coding-agent/src/internal-urls/memory-protocol.ts:1-491` → `protocol.go` (`MemoryHandler` for `memory://root[/<subpath>]` under `local` and `memory://<id>` under `mnemopi`; `PromptDoc` returns `""` unless `host.MemoryBackend == "local"`).
- `packages/coding-agent/src/prompts/tools/{retain,recall,reflect,memory-edit,learn}.md`, `prompts/internal-urls/memory.md`, `prompts/system/{mnemopi,hindsight}-instructions.md`, `prompts/memories/read-path.md` → `prompts/*.md` (embedded verbatim).

## Stdlib-Only Adaptations (Zero Third-Party Deps / Zero cgo)

| Area | Upstream implementation | Port adaptation |
|---|---|---|
| Mnemopi persistence & search | `bun:sqlite` FTS5 + local ONNX/fastembed embeddings | Pure-Go per-bank atomic JSON store (`<dbDir>/banks/<bank>.json`) with per-bank mutex, deterministic BM25 + token-overlap + recency + importance scoring, plus optional `net/http` embedding calls when `mnemopi.embeddingApiUrl` is configured (graceful fallback to lexical scoring). |
| Hindsight outbox | One JSON file per event under `<agentDir>/hindsight-outbox/<dest>/` with POSIX flock | Atomic JSONL outbox under `<agentDir>/memories/hindsight/outbox-<destKey>.jsonl` with per-path mutex, secret/token redaction, deterministic deduplicating IDs, and tag-grouped batch flush via `net/http`. |
| Local rollout consolidation | Background LLM stage-1/phase-2 SQLite pipeline (`memories/storage.ts`) | Not ported; `local` backend provides `learned.md` lesson capture/reading, `memory_summary.md` prompt rendering, and `memory://root[/<subpath>]` resolution without SQLite or background LLM calls. |

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `memory.backend` | `"off"` | Yes | `"off" \| "local" \| "mnemopi" \| "hindsight"`; default `"off"` keeps all 5 tools inactive and `memory://` prompt doc empty. |
| `mnemopi.{dbPath,bank,bankId,scoping,recallLimit,noEmbeddings,embeddingModel,embeddingApiUrl,embeddingApiKey,embeddingQueryPrefix,injectionTokenLimit}` | upstream defaults | Yes | `mnemopi.scoping` defaults to `"per-project"`. |
| `hindsight.{apiUrl,apiToken,apiKey,bankId,bankIdPrefix,scoping,globalScope,bankMission,retainMission,retainContext,recallBudget,recallMaxTokens,recallTypes,requestTimeoutMs,recallTimeoutMs,reflectTimeoutMs,retainTimeoutMs}` | upstream defaults | Yes | `hindsight.apiUrl` must be non-empty for Hindsight tool activation. |
| `autolearn.enabled` | `false` | Yes | Required alongside a live memory backend to activate `learn`. |
