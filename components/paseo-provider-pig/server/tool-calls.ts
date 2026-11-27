import type { AcpToolCallSnapshot, AcpTransformer } from "@getpaseo/plugin/server/acp";

type Input = AcpToolCallSnapshot["input"];

function record(value: Input): Record<string, Input> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, Input>) : undefined;
}

/**
 * Paseo's ACP shim publishes a failed tool call with the call's raw output as its error (in Paseo 0.15.x a null error
 * broke the wire contract; Paseo 0.16.0 #9467 added a generic "Tool failed" fallback). pig-acp reports a bash call's
 * output through terminal metadata and sends no raw output, so supplying a specific message keeps both 0.11-0.15
 * compatibility and a descriptive title-based error on 0.16+.
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
