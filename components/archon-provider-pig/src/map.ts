/**
 * PiG event → Archon ProviderChunk mapping.
 *
 * PiG's JSON and RPC modes write Pi's own event stream (`agent_start`, `turn_start`,
 * `message_update` with an `assistantMessageEvent`, `tool_execution_*`, `agent_end`,
 * `agent_settled`), so this is a pure translation of names and shapes into the
 * provider contract: no buffering of its own beyond coalescing text deltas into the
 * whole blocks the contract requires.
 */

export type ProviderChunk =
  | { type: 'agent_message_chunk'; text: string }
  | { type: 'agent_thought_chunk'; text: string }
  | {
      type: 'tool_call';
      toolCallId: string;
      name: string;
      title?: string;
      rawInput?: unknown;
    }
  | {
      type: 'tool_call_update';
      toolCallId: string;
      status: 'completed' | 'failed' | 'cancelled';
      output?: string;
      exitCode?: number;
    }
  | {
      type: 'result';
      sessionId?: string;
      stopReason?: 'end_turn' | 'max_tokens' | 'max_turn_requests' | 'refusal' | 'cancelled';
      tokens?: { input: number; output: number; cacheRead?: number; cacheWrite?: number };
      failure?: { class: FailureClass; evidence: string };
      isError?: boolean;
    }
  | { type: 'warning'; code: string; message: string }
  | { type: 'settled' };

/**
 * Why a turn failed. The contract forbids classifying free prose, so the class comes from the HTTP
 * status and provider error type PiG's error text carries in fixed shapes (see `httpFailureOf`)
 * and from whether the run ever started; the vendor's own words travel as `evidence`, which
 * nothing branches on.
 */
export type FailureClass =
  | 'auth'
  | 'quota_exhausted'
  | 'budget_exceeded'
  | 'misconfigured'
  | 'rate_limited'
  | 'overloaded'
  | 'transient'
  | 'unknown';

interface HttpFailure {
  status?: number;
  type?: string;
  reason?: string;
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

/** The provider error type inside a JSON error body: Anthropic's `error.type` or OpenAI's flat `type`. */
function typeOfBody(body: unknown): string | undefined {
  const record = recordOf(body);
  const inner = recordOf(record?.error);
  if (typeof inner?.type === 'string') return inner.type;
  return typeof record?.type === 'string' && record.type !== 'error' ? record.type : undefined;
}

/**
 * PiG's failed assistant message has `stopReason: "error"` and nothing structured: Go's
 * `ai.AssistantMessage` (messages.go) and Pi's carry only `errorMessage`. The HTTP status and the
 * provider's error envelope live inside that string, in the shapes PiG's own formatters produce.
 * Only those anchored shapes are read; any other text is not an HTTP failure.
 *
 *  - Anthropic (SDK form): `401 {"type":"error","error":{"type":"authentication_error",...}}`
 *  - OpenAI: `OpenAI API error (401): {"message":...,"type":"invalid_request_error",...}`
 *    (PiG ai/openai_error.go `openAIHTTPError`, prefix from the provider)
 *  - Anthropic-compatible (PiG ai/anthropic.go `anthHTTPError`): `<provider>: HTTP 401: <type>: <message>`
 *  - Pi-messages (PiG ai/pi_messages.go): `401 Unauthorized: <message>`
 *  - Google: the SDK ApiError, a bare JSON `{"error":{"message":...,"code":400,"status":"..."}}`
 */
function httpFailureOf(text: string): HttpFailure | undefined {
  const leading = /^([45]\d\d)(?:\s|:|$)/.exec(text);
  if (leading !== null) {
    return { status: Number(leading[1]), type: typeOfBody(parseJson(text.slice(leading[0].length))) };
  }
  const openai = /^OpenAI API error \(([45]\d\d)\):\s*/.exec(text);
  if (openai !== null) {
    return { status: Number(openai[1]), type: typeOfBody(parseJson(text.slice(openai[0].length))) };
  }
  const compatible = /^[\w.-]+: HTTP ([45]\d\d): ([a-z][a-z_]*): /.exec(text);
  if (compatible !== null) return { status: Number(compatible[1]), type: compatible[2] };
  const envelope = recordOf(recordOf(parseJson(text))?.error);
  if (typeof envelope?.code === 'number' && envelope.code >= 400 && envelope.code < 600) {
    // The SDK wraps the API's own body, as a string, in `message`; its ErrorInfo names the reason.
    const inner = recordOf(recordOf(parseJson(typeof envelope.message === 'string' ? envelope.message : ''))?.error);
    const details = Array.isArray(inner?.details) ? (inner.details as unknown[]) : [];
    const info = details.map(recordOf).find(detail => typeof detail?.reason === 'string');
    return {
      status: envelope.code,
      reason: typeof info?.reason === 'string' ? info.reason : undefined,
    };
  }
  return undefined;
}

/** The failure a finished assistant message reports, or undefined when it succeeded. */
export function failureOfMessage(message: unknown): { class: FailureClass; evidence: string } | undefined {
  const value = message as { stopReason?: string; errorMessage?: string } | undefined;
  if (value?.stopReason !== 'error') return undefined;
  const text = typeof value.errorMessage === 'string' ? value.errorMessage : '';
  const evidence = text.length > 0 ? text : 'PiG reported a failed message';
  const failure = httpFailureOf(text);
  const status = failure?.status;
  if (status === 401 || status === 403) return { class: 'auth', evidence };
  if (status === 402) return { class: 'quota_exhausted', evidence };
  if (status === 429) return { class: 'rate_limited', evidence };
  if (status !== undefined && status >= 500) return { class: 'overloaded', evidence };
  if (failure?.reason === 'API_KEY_INVALID') return { class: 'auth', evidence };
  if (failure?.type === 'authentication_error') return { class: 'auth', evidence };
  if (failure?.type === 'rate_limit_error') return { class: 'rate_limited', evidence };
  if (failure?.type === 'overloaded_error') return { class: 'overloaded', evidence };
  if (failure?.type === 'invalid_request_error') return { class: 'misconfigured', evidence };
  return { class: 'unknown', evidence };
}

/**
 * Why a turn ended when PiG exited without settling. A run that never wrote its Session header
 * could not start at all, which is the operator's configuration (a missing or unknown model);
 * a signal is a cancellation; anything else is unknown until a structured signal says otherwise.
 */
export function failureOfExit(options: {
  code: number | null;
  started: boolean;
  stderr: string;
}): { class: FailureClass; evidence: string } | undefined {
  const detail = options.stderr.trim().split('\n').slice(-3).join(' ').slice(0, 500);
  const evidence = detail.length > 0 ? detail : `pig exited ${String(options.code)}`;
  if (options.code === 0) return undefined;
  if (options.code === 130 || options.code === 129 || options.code === 143) {
    return { class: 'transient', evidence };
  }
  if (!options.started) return { class: 'misconfigured', evidence };
  return { class: 'unknown', evidence };
}

/** The subset of a PiG event this mapper reads. Anything else passes through untouched. */
export interface PigEvent {
  type?: string;
  id?: string;
  message?: unknown;
  aborted?: boolean;
  usage?: Record<string, unknown>;
  assistantMessageEvent?: { type?: string; delta?: string; content?: string; text?: string };
  toolCallId?: string;
  toolName?: string;
  args?: unknown;
  result?: unknown;
  isError?: boolean;
}

/** Normalized usage from a Pi event's `usage` bag, or undefined when it reports none. */
export function usageOf(usage: Record<string, unknown> | undefined) {
  if (!usage) return undefined;
  const input = typeof usage.input === 'number' ? usage.input : undefined;
  const output = typeof usage.output === 'number' ? usage.output : undefined;
  if (input === undefined && output === undefined) return undefined;
  const result: { input: number; output: number; cacheRead?: number; cacheWrite?: number } = {
    input: input ?? 0,
    output: output ?? 0,
  };
  if (typeof usage.cacheRead === 'number') result.cacheRead = usage.cacheRead;
  if (typeof usage.cacheWrite === 'number') result.cacheWrite = usage.cacheWrite;
  return result;
}

/** The text of a finished tool's result, flattened the way PiG reports it. */
export function toolOutputOf(result: unknown): string | undefined {
  if (result === undefined || result === null) return undefined;
  if (typeof result === 'string') return result;
  const content = (result as { content?: unknown }).content;
  if (Array.isArray(content)) {
    const text = content
      .map(part => (part as { text?: unknown }).text)
      .filter((part): part is string => typeof part === 'string')
      .join('');
    if (text.length > 0) return text;
  }
  const structured = (result as { structuredContent?: unknown }).structuredContent;
  if (typeof structured === 'string') return structured;
  return JSON.stringify(result);
}

/**
 * Feed PiG events in, get contract chunks out. One instance per turn.
 *
 * Text arrives as `text_start` / `text_delta` / `text_end` (and the thinking equivalents);
 * the contract wants whole blocks, so deltas accumulate and flush on the end event, and a
 * block still open when the turn ends flushes then rather than being dropped.
 */
export class PigTurnMapper {
  private text = '';
  private thought = '';
  private sessionId: string | undefined;
  private failure: { class: FailureClass; evidence: string } | undefined;
  private lastUsage: ReturnType<typeof usageOf>;

  /** The PiG session id, once the Session header has been seen. */
  get piSessionId(): string | undefined {
    return this.sessionId;
  }

  /** Events for one PiG record, in contract order. */
  push(event: PigEvent): ProviderChunk[] {
    const chunks: ProviderChunk[] = [];
    const kind = event.type;

    if (kind === 'session' && typeof event.id === 'string') {
      this.sessionId = event.id;
      return chunks;
    }

    if (event.usage) this.lastUsage = usageOf(event.usage) ?? this.lastUsage;

    if (kind === 'message_update') {
      const update = event.assistantMessageEvent ?? {};
      const eventType = update.type ?? '';
      // Only text and thinking blocks are reply content. Tool-call arguments stream as
      // `toolcall_*` (the call itself comes from the tool execution events), and `start`, `done`,
      // `error` and any type this mapper does not know carry no text, so they are ignored.
      // Deltas accumulate; an end event repeats the whole block in `content`, so appending it
      // would double the text. An end that arrives with nothing buffered (a runtime that sends
      // only start/end) still contributes its block.
      const isThought = eventType.startsWith('thinking_');
      if (eventType === 'text_delta' || eventType === 'thinking_delta') {
        const delta = update.delta ?? update.content ?? update.text ?? '';
        if (isThought) this.thought += delta;
        else this.text += delta;
      } else if (eventType === 'text_end' || eventType === 'thinking_end') {
        const buffered = isThought ? this.thought : this.text;
        if (buffered.length === 0) {
          const whole = update.content ?? update.text ?? '';
          if (isThought) this.thought = whole;
          else this.text = whole;
        }
        chunks.push(...(isThought ? this.flushThought() : this.flushText()));
      }
      return chunks;
    }

    if (kind === 'tool_execution_start' && typeof event.toolCallId === 'string') {
      chunks.push(...this.flushText());
      chunks.push({
        type: 'tool_call',
        toolCallId: event.toolCallId,
        name: event.toolName ?? 'tool',
        title: event.toolName,
        rawInput: event.args,
      });
      return chunks;
    }

    if (kind === 'tool_execution_end' && typeof event.toolCallId === 'string') {
      const update: ProviderChunk = {
        type: 'tool_call_update',
        toolCallId: event.toolCallId,
        status: event.isError === true ? 'failed' : 'completed',
      };
      const output = toolOutputOf(event.result);
      if (output !== undefined) update.output = output;
      chunks.push(update);
      return chunks;
    }

    if (kind === 'message_end') {
      const failure = failureOfMessage((event as { message?: unknown }).message);
      if (failure !== undefined) this.failure = failure;
      return chunks;
    }

    if (kind === 'auto_retry_start') {
      chunks.push({ type: 'warning', code: 'pig.auto_retry', message: 'PiG is retrying the turn' });
      return chunks;
    }

    if (kind === 'agent_settled') {
      chunks.push(...this.flushText());
      chunks.push(...this.flushThought());
      if (this.failure !== undefined) {
        // A failed message settles like any other: the class, never the exit status, says so.
        chunks.push(
          { type: 'result', failure: this.failure, isError: true },
          { type: 'settled' }
        );
        return chunks;
      }
      const result: ProviderChunk = {
        type: 'result',
        stopReason: event.aborted === true ? 'cancelled' : 'end_turn',
      };
      if (this.sessionId !== undefined) result.sessionId = this.sessionId;
      if (this.lastUsage !== undefined) result.tokens = this.lastUsage;
      chunks.push(result, { type: 'settled' });
    }

    return chunks;
  }

  /** Flush an open text block, if any: the contract's `text` must be non-empty. */
  flushText(): ProviderChunk[] {
    if (this.text.length === 0) return [];
    const chunk: ProviderChunk = { type: 'agent_message_chunk', text: this.text };
    this.text = '';
    return [chunk];
  }

  /** Flush an open reasoning block, if any. */
  flushThought(): ProviderChunk[] {
    if (this.thought.length === 0) return [];
    const chunk: ProviderChunk = { type: 'agent_thought_chunk', text: this.thought };
    this.thought = '';
    return [chunk];
  }
}
