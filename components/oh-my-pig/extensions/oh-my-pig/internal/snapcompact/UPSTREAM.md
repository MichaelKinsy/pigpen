# Upstream Provenance — `internal/snapcompact`

Ported from **omp v19.1.1** (`github.com/can1357/oh-my-pi`, MIT).

## Ported Source Files

| Upstream file | Lines | Go target | Notes |
| --- | --- | --- | --- |
| `crates/natives/src/snapcompact.rs` | 55–1240 | `fonts.go`, `render.go` | X.org BDF (`5x8`, `6x12`, `8x13`) & `unscii-8.hex` parsers, 10-color palette, `Layout` state machine (row-major grid + 2-column doc layout, `U+000E`/`U+000F` dim spans, `U+2588` full-block newline fill, 6-hue sentence ink, `lineRepeat` highlight bands), Lanczos3 stretch resampling, and pure-stdlib indexed/RGBA PNG encoding. |
| `crates/natives/src/fonts/{5x8,6x12,8x13}.bdf`, `unscii-8.hex` | — | `fonts/*.gz` | Embedded via `//go:embed` (deterministic gzip, ~104 KB total). Public-domain X.org misc-fixed BDF fonts and unscii-8 bitmap font (digit `0` already disambiguated with slash/bar). |
| `packages/snapcompact/src/snapcompact.ts` | 80–2721 | `shapes.go`, `normalize.go`, `compact.go` | `ShapeVariants` (all 17 variants), `IdealShapeVariant`, `ResolveShape`, `DenseCompanion`, `ComputeGeometry`, `ProviderImageBudget`, `FrameDataBytesBudget`, `MaxFramesForDataBudget`, `ReaderIncludesThinking`, `SerializeConversation`, `ElideDataUrls`, `Normalize`, `ScanRenderability`, `DimStopwords`, `Wrap`, `paginateCells`, `planArchive` (foveated HQ/LQ/HQ + verbatim text edges), `HistoryBlocks` (newest-first byte cap + gap notices), `ViewWithoutFrames`, and `Compact`. |
| `packages/snapcompact/src/prompts/snapcompact-summary.md` | 1–27 | `compact.go` (`renderSnapcompactSummary`) | Exact reading guide, scope legend (thinking-aware), grid notes, `FILES` and `HISTORY` sections. |
| `packages/coding-agent/src/session/compact-modes.ts` | 16–107 | `register.go` (`ParseCompactArgs`, `onInput`) | `/compact snapcompact`, `/compact soft [focus]`, `/compact handoff [focus]`, `/compact remote [focus]`, and focus-rejection on `/compact snapcompact`. |
| `packages/coding-agent/src/session/compaction-methods.ts` | 45–51 | `register.go` (`DefaultMethodOrder`, `wantsSnapcompactForMethodOrder`) | Honors `compaction.methodOrder` (default `["remote", "snapcompact", "handoff", "shake", "soft"]`) and `snapcompact.shape`. |
| `packages/coding-agent/src/session/session-maintenance.ts` | 1554–1665, 4287–4467 | `register.go`, `compact.go` (`ComputeMaxFrames`, `onSessionBeforeCompact`) | Local vision-model gating, 5% renderability preflight, standing payload byte cap with 2-pass re-render shrink, and fallback to PiG's default LLM compaction (`soft`) when unavailable. |
| `packages/agent/src/compaction/messages.ts` | 248–268 | `register.go` (`onContext`) | Re-attaches persisted snapcompact PNG frames + verbatim text edges on `EventContext` by rewriting `compactionSummary` into a multimodal `user` message (`[{type:"text", text:summary}, ...HistoryBlocks]`), with automatic `ViewWithoutFrames` text-only fallback for non-vision or thinking-scrubbed readers. |

## Deliberate Omissions & Host-SDK Notes

| Upstream feature | Reason |
| --- | --- |
| Embedded `Silver.ttf` (3.7 MB, CC BY 4.0) TrueType CJK fallback | Zero third-party Go deps (stdlib only) and compact binary size. `8x13.bdf` (3,703 glyphs) and `6x12.bdf` (4,531 glyphs) cover ASCII, Latin, Greek, Cyrillic, box-drawing, and math; CJK-heavy transcripts exceeding the 5% unrenderable ceiling cleanly fall back to PiG's LLM summarization (`soft`). |
| Externalized `blob:sha256:` session blob store | PiG has no session blob store; frames persist directly inside `CompactionEntry.Details["snapcompact"]` (capped by `FrameDataBytesBudget`). Unresolved `blob:sha256:` references degrade cleanly to omp's unavailable-frame marker. |
| Inline imaging (`snapcompact.systemPrompt`, `snapcompact.toolResults`) | Separate experimental opt-in features (both default off in omp `v19.1.1`). |
