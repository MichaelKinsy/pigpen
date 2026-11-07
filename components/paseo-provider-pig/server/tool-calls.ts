import type { AcpToolCallSnapshot, AcpTransformer } from "@getpaseo/plugin/server/acp";

type Input = AcpToolCallSnapshot["input"];

function record(value: Input): Record<string, Input> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, Input>) : undefined;
}

/**
 * Paseo's ACP shim publishes a failed tool call with the call's raw output as its error, and the wire contract refuses a
 * null error. pig-acp reports a bash call's output through terminal metadata and sends no raw output, so a failed bash call
 * (a command that exits non-zero, or one the user denied) arrived with a null error and made the agent's timeline unreadable.
 */
function withFailureReason(toolCall: AcpToolCallSnapshot): AcpToolCallSnapshot {
  if (toolCall.status !== "failed" || toolCall.output !== null) return toolCall;
  return { ...toolCall, output: { message: `${toolCall.title} failed` } };
}

/**
 * PiG's edit tool takes `{ path, edits: [{ oldText, newText }] }` and its write tool `{ path, content }`. Paseo's ACP
 * shim draws an edit row from `filePath`, `oldString` and `newString`, so this fills them in from those payloads.
 * Anything else, including a payload of another shape, passes through unchanged.
 */
export const pigToolCalls: AcpTransformer = {
  toolCall(original) {
    const toolCall = withFailureReason(original);
    if (toolCall.kind !== "edit") return toolCall;
    const input = record(toolCall.input);
    if (!input) return toolCall;
    const filePath = typeof input.path === "string" ? input.path : toolCall.locations[0];
    if (!filePath) return toolCall;

    const edits = Array.isArray(input.edits) ? input.edits : [];
    const only = edits.length === 1 ? record(edits[0]) : undefined;
    if (only && typeof only.oldText === "string" && typeof only.newText === "string") {
      return { ...toolCall, input: { ...input, filePath, oldString: only.oldText, newString: only.newText } };
    }
    if (typeof input.content === "string") {
      return { ...toolCall, input: { ...input, filePath, newString: input.content } };
    }
    return { ...toolCall, input: { ...input, filePath } };
  },
};
