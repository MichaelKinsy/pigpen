# Execution contract context cost

The committed baseline is intentionally updated for the explicit execution contract. CONTEXT-BEFORE.json preserves the previous main baseline; CONTEXT-AFTER.json is the new measurement from the same 24 fixtures. The gate still remeasures and checks exact breakdowns, semantic counts, single objective blocks, and filtered historical checkpoints.

| Measurement | Before | After | Increase |
| --- | ---: | ---: | ---: |
| Serialized request characters (24 fixtures) | 242,142 | 261,018 | 18,876 (7.80%) |
| Extension-attributable characters | 130,042 | 148,918 | 18,876 (14.52%) |
| Estimated tokens (characters / 4, rounded per fixture) | 60,543 | 65,266 | 4,723 |
| Child request characters | 15,225 | 15,225 | 0 |
| Active regular, 10 tasks | 12,238 | 13,379 | 1,141 |

The typical active-request increase comprises 685 tool-schema characters, 431 execution-policy/state characters, and 25 SDK tool-guidance characters. This is about 286 estimated tokens per such request in default automatic mode. Detailed scheduling policy is present for both uncapped and capped execution; explicit zero selects the shorter disabled policy. The schema remains available in every mode so settings changes require no reload. These are deterministic serialization measurements, not billed-token measurements. The schema and disabled-mode instructions were shortened following user feedback; the first implementation added 1,833 characters to this active fixture.

The extra schema and instructions make the scheduling contract available to the model; removing them would leave the new tool forms and allowance semantics undiscoverable. A scheduling declaration is itself an additional model-generated tool call and persisted tool result at a segment boundary; its dynamic output and task-dependent frequency are not represented by this static baseline. This design does not claim lower total token use per completed goal.

The separate real SDK worker measures current-state refreshes on custom-message runs (which bypass before_agent_start): 176 serialized content characters for ready state, 322 for a repair, and 301 for the fixture wait. It reuses the inherited objective/policy block when present. Its eight request sizes were 12,100; 12,370; 13,090; 11,783; 14,159; 12,852; 14,630; and 14,834 characters. Those include dynamic tool history and are not a controlled before/after savings comparison. Four extension dispatches are persisted as four used allowance units; internal tool turns are not additional units.

Run allowance bounds extension-initiated executions only. It neither proves progress nor caps a third-party extension's direct host work or an indefinitely running host tool loop.

PR review follow-up: clarify the disabled-mode instruction from “is set” to “> 0” now that zero explicitly disables inherited automation. This removes three serialized characters from each of 14 active fixtures (42 total) without changing schemas or semantic counts. Remeasured the baseline and CONTEXT-AFTER.json; the gate retains its exact comparisons. The SDK request sizes above are historical measurements from the initial implementation.

Latest user steering restores automatic continuation by default and makes the run-count limit optional. Default active requests now include ready/wait guidance; the tool description no longer claims a configured allowance is required. Relative to the previous review baseline, serialized request size increases by 2,298 characters across the same 24 fixtures, with unchanged semantic counts. Updated the baseline and CONTEXT-AFTER.json from fresh measurements; the exact-comparison gate passes. Uncapped execution may continue indefinitely when the agent repeatedly declares ready.
