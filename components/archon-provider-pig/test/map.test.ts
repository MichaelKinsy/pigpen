import { describe, expect, it } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { vendorOf } from '../src/main';
import {
  PigTurnMapper,
  failureOfExit,
  failureOfMessage,
  toolOutputOf,
  usageOf,
  type PigEvent,
} from '../src/map';

function replay(file: string) {
  const mapper = new PigTurnMapper();
  const chunks = readFileSync(join(import.meta.dir, '..', 'fixtures', file), 'utf8')
    .split('\n')
    .filter(line => line.trim().length > 0)
    .flatMap(line => mapper.push(JSON.parse(line) as PigEvent));
  return { chunks, mapper };
}

describe('PigTurnMapper over a recorded PiG turn', () => {
  it('turns a text turn into one block, one result and one settled', () => {
    const { chunks, mapper } = replay('text-turn.ndjson');
    const text = chunks.filter(chunk => chunk.type === 'agent_message_chunk');
    expect(text).toHaveLength(1);
    expect(text[0]).toMatchObject({ type: 'agent_message_chunk' });
    expect((text[0] as { text: string }).text.trim()).toBe('ok');
    expect(mapper.piSessionId).toBeTruthy();

    const tail = chunks.slice(-2);
    expect(tail[0]).toMatchObject({ type: 'result', stopReason: 'end_turn' });
    expect(tail[0]).toHaveProperty('sessionId');
    expect(tail[1]).toEqual({ type: 'settled' });
    expect(chunks.filter(chunk => chunk.type === 'result')).toHaveLength(1);
  });

  it('turns a tool turn into a closed tool call', () => {
    const { chunks } = replay('tool-turn.ndjson');
    const call = chunks.find(chunk => chunk.type === 'tool_call');
    expect(call).toMatchObject({ type: 'tool_call', name: 'read' });
    expect((call as { rawInput: { path: string } }).rawInput.path).toContain('note.txt');

    const update = chunks.find(chunk => chunk.type === 'tool_call_update');
    expect(update).toMatchObject({ type: 'tool_call_update', status: 'completed' });
    expect((update as { output: string }).output).toContain('hello');
    expect(chunks.findIndex(chunk => chunk.type === 'tool_call')).toBeLessThan(
      chunks.findIndex(chunk => chunk.type === 'tool_call_update')
    );
  });

  it('orders a tool call after the text that preceded it, and before the result', () => {
    const { chunks } = replay('tool-turn.ndjson');
    const resultIndex = chunks.findIndex(chunk => chunk.type === 'result');
    const callIndex = chunks.findIndex(chunk => chunk.type === 'tool_call');
    expect(callIndex).toBeGreaterThanOrEqual(0);
    expect(resultIndex).toBeGreaterThan(callIndex);
  });

  it('keeps streamed tool-call arguments out of the assistant text', () => {
    // Captured from `pig --mode json` (github-copilot/claude-haiku-5.5) reading note.txt: the
    // toolcall_delta events stream `{"path": "note.txt"}`, which must never become reply text.
    const { chunks } = replay('tool-turn-args.ndjson');
    const text = chunks.flatMap(chunk => (chunk.type === 'agent_message_chunk' ? [chunk.text] : []));
    expect(text.join('')).not.toContain('path');
    expect(text.join('')).toContain('PIG_E2E_FILE_CONTENT');
    expect(chunks.find(chunk => chunk.type === 'tool_call')).toMatchObject({ name: 'read' });
  });

  it('accumulates only text and thinking deltas and ignores every other event type', () => {
    const mapper = new PigTurnMapper();
    const update = (assistantMessageEvent: Record<string, unknown>) =>
      mapper.push({ type: 'message_update', assistantMessageEvent });
    const chunks = [
      ...update({ type: 'start' }),
      ...update({ type: 'toolcall_start', contentIndex: 0 }),
      ...update({ type: 'toolcall_delta', contentIndex: 0, delta: '{"path": "a"}' }),
      ...update({ type: 'toolcall_checkpoint', contentIndex: 0, delta: 'x' }),
      ...update({ type: 'toolcall_end', contentIndex: 0, content: 'x' }),
      ...update({ type: 'mystery_delta', delta: 'leak' }),
      ...update({ type: 'text_delta', delta: 'ok' }),
      ...update({ type: 'text_end' }),
      ...update({ type: 'done' }),
      ...update({ type: 'error' }),
    ];
    expect(JSON.stringify(chunks)).not.toContain('path');
    expect(JSON.stringify(chunks)).not.toContain('leak');
    expect(JSON.stringify(chunks)).toContain('ok');
  });

  it('reports a cancelled turn when PiG settled as aborted', () => {
    const mapper = new PigTurnMapper();
    const chunks = mapper.push({ type: 'agent_settled', aborted: true });
    expect(chunks[0]).toMatchObject({ type: 'result', stopReason: 'cancelled' });
  });

  it('coalesces deltas into one block and never emits empty text', () => {
    const mapper = new PigTurnMapper();
    const chunks = [
      ...mapper.push({ type: 'message_update', assistantMessageEvent: { type: 'text_start' } }),
      ...mapper.push({
        type: 'message_update',
        assistantMessageEvent: { type: 'text_delta', delta: 'he' },
      }),
      ...mapper.push({
        type: 'message_update',
        assistantMessageEvent: { type: 'text_delta', delta: 'llo' },
      }),
      ...mapper.push({ type: 'message_update', assistantMessageEvent: { type: 'text_end' } }),
    ];
    expect(chunks).toEqual([{ type: 'agent_message_chunk', text: 'hello' }]);
  });

  it('flattens tool output and reports no usage when PiG gives none', () => {
    expect(toolOutputOf({ content: [{ type: 'text', text: 'a' }, { type: 'text', text: 'b' }] })).toBe(
      'ab'
    );
    expect(toolOutputOf(undefined)).toBeUndefined();
    expect(usageOf({})).toBeUndefined();
    expect(usageOf({ input: 5, output: 7, cacheRead: 1 })).toEqual({
      input: 5,
      output: 7,
      cacheRead: 1,
    });
  });

  // errorMessage strings captured from `pig --mode json` (0.5.3) against a bad key; PiG's failed
  // assistant message has stopReason "error" and carries no structured error object.
  const failed = (errorMessage: string) => failureOfMessage({ stopReason: 'error', errorMessage });
  const ANTHROPIC_401 =
    '401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_011CgchPmY9rvs5rAvNwxDyX"}';
  const OPENAI_401 =
    'OpenAI API error (401): {"message":"Incorrect API key provided: sk-bad-k**-000. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error","code":"invalid_api_key","param":null}';
  const GOOGLE_BAD_KEY = JSON.stringify({
    error: {
      message: JSON.stringify(
        {
          error: {
            code: 400,
            message: 'API key not valid. Please pass a valid API key.',
            status: 'INVALID_ARGUMENT',
            details: [{ '@type': 'type.googleapis.com/google.rpc.ErrorInfo', reason: 'API_KEY_INVALID' }],
          },
        },
        null,
        2
      ),
      code: 400,
      status: 'Bad Request',
    },
  });

  it('classifies a real Anthropic 401 as auth, keeping the message as evidence', () => {
    expect(failed(ANTHROPIC_401)).toEqual({ class: 'auth', evidence: ANTHROPIC_401 });
  });

  it('classifies a real OpenAI 401 as auth although its body type is invalid_request_error', () => {
    expect(failed(OPENAI_401)).toEqual({ class: 'auth', evidence: OPENAI_401 });
  });

  it('classifies a real Google bad-key failure as auth from the ErrorInfo reason', () => {
    expect(failed(GOOGLE_BAD_KEY)?.class).toBe('auth');
  });

  it('reads 403, 429 and 5xx from the leading status of each provider shape', () => {
    const anthropic = (status: number, type: string) =>
      `${status} {"type":"error","error":{"type":"${type}","message":"x"}}`;
    expect(failed(anthropic(403, 'permission_error'))?.class).toBe('auth');
    expect(failed(anthropic(429, 'rate_limit_error'))?.class).toBe('rate_limited');
    expect(failed(anthropic(529, 'overloaded_error'))?.class).toBe('overloaded');
    expect(failed(anthropic(500, 'api_error'))?.class).toBe('overloaded');
    expect(failed('OpenAI API error (429): {"message":"slow down","type":"requests"}')?.class).toBe(
      'rate_limited'
    );
    expect(failed('OpenAI API error (503): upstream down')?.class).toBe('overloaded');
    expect(failed('anthropic: HTTP 403: permission_error: nope')?.class).toBe('auth');
    expect(failed('402 Payment Required: out of credit')?.class).toBe('quota_exhausted');
    expect(
      failed(JSON.stringify({ error: { message: 'quota', code: 429, status: 'Too Many Requests' } }))?.class
    ).toBe('rate_limited');
  });

  it('falls back to the provider error type when the status is not mapped', () => {
    expect(failed('400 {"type":"error","error":{"type":"invalid_request_error","message":"x"}}')?.class).toBe(
      'misconfigured'
    );
    expect(failed('409 {"type":"error","error":{"type":"overloaded_error","message":"x"}}')?.class).toBe(
      'overloaded'
    );
  });

  it('leaves a failure with no HTTP status or provider type as unknown, never guessed from prose', () => {
    for (const text of [
      'Request aborted',
      'Anthropic stream ended without a stop reason',
      'connection reset by peer',
      'the server returned 401 unauthorized',
      'got status: UNAVAILABLE. {"error":{"code":503}}',
      '{"error":{"message":"plain"}}',
    ]) {
      expect(failed(text)).toEqual({ class: 'unknown', evidence: text });
    }
    expect(failureOfMessage({ stopReason: 'error' })).toEqual({
      class: 'unknown',
      evidence: 'PiG reported a failed message',
    });
    expect(failureOfMessage({ stopReason: 'stop' })).toBeUndefined();
  });

  it('reports a turn that never started as misconfigured and a signal as transient', () => {
    expect(failureOfExit({ code: 1, started: false, stderr: 'no model configured' })).toEqual({
      class: 'misconfigured',
      evidence: 'no model configured',
    });
    expect(failureOfExit({ code: 143, started: true, stderr: '' })?.class).toBe('transient');
    expect(failureOfExit({ code: 0, started: true, stderr: '' })).toBeUndefined();
    expect(failureOfExit({ code: 1, started: true, stderr: 'boom' })?.class).toBe('unknown');
  });

  it('carries a failed message into the settled result as a typed failure', () => {
    const mapper = new PigTurnMapper();
    mapper.push({ type: 'session', id: 'abc' });
    mapper.push({
      type: 'message_end',
      message: { stopReason: 'error', errorMessage: '403 {"type":"error","error":{"type":"permission_error","message":"forbidden"}}' },
    });
    const chunks = mapper.push({ type: 'agent_settled', aborted: false });
    expect(chunks[0]).toMatchObject({
      type: 'result',
      isError: true,
      failure: {
        class: 'auth',
        evidence: '403 {"type":"error","error":{"type":"permission_error","message":"forbidden"}}',
      },
    });
    expect(chunks[1]).toEqual({ type: 'settled' });
  });

  it('names token usage `tokens`, the field the contract reads', () => {
    const mapper = new PigTurnMapper();
    mapper.push({ type: 'message_end', message: undefined, usage: { input: 3, output: 4 } });
    const chunks = mapper.push({ type: 'agent_settled' });
    expect(chunks[0]).toMatchObject({ type: 'result', tokens: { input: 3, output: 4 } });
    expect(chunks[0]).not.toHaveProperty('usage');
  });

  it('reads the vendor from a model ref, with or without the pig prefix', () => {
    expect(vendorOf('pig/openrouter/anthropic/claude-haiku-5.5')).toBe('openrouter');
    expect(vendorOf('openrouter/anthropic/claude-haiku-5.5')).toBe('openrouter');
    expect(vendorOf('pig')).toBeUndefined();
    expect(vendorOf(undefined)).toBeUndefined();
  });
});
