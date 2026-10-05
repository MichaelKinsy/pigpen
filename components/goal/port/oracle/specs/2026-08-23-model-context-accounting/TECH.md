# TECH — Complete model-context accounting

## Composed-request capture

`experiments/context/capture-context.mjs` drives the REAL extension through a
test-only harness (same discipline as tests/integration): registers tools and
handlers via the pi mock API, runs session_start + before_agent_start for each
fixture, and captures:

    {
      baseSystem: string,            // pre-extension system prompt
      extensionSystem: string,       // injected by before_agent_start
      messages: AgentMessage[],      // AFTER the context hook transformation
      tools: ToolDefinition[],       // registered goal tools incl. JSON schemas
    }

No network call, no child agent, no live provider is possible: the capture is
pure function calls over fixture data.

## Breakdown (`measure-context.mjs`)

ContextSizeBreakdown:
  baseSystemChars, extensionSystemChars, checkpointChars,
  historicalCheckpointChars, goalStateChars, toolSchemaChars, messageChars,
  totalSerializedChars, estimatedTokens (chars/4 heuristic, labeled as such)

toolSchemaChars serializes every ACTIVE tool's name+description+JSON schema;
this is the component B4 never saw. historicalCheckpointChars counts
pi-goal-event messages dropped or rewritten by the context hook.

## Semantic occurrence counts (`semantic-invariants.mjs`)

SemanticOccurrenceCounts over the serialized request: objective,
verificationContract/goalContract, currentTask, currentTaskContract,
lifecyclePolicy markers ("third consecutive identical blocker", "independent
auditor", "never edit"), auditorRejection, blockerPolicy. Every semantic field
must be classified somewhere in the breakdown (gate assertion).

## Determinism

Fixtures use fixed timestamps and ids; no Date.now() enters captured output.
The gate re-measures in-process and requires byte equality of the derived
breakdown against experiments/context/baseline-main.json. Any intentional
change to prompt composition must update the baseline WITH a spec rationale
(CONTEXT-BASELINE.md).
