import { describe, expect, it } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
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

  it('classifies a failed message from its status code, never from prose', () => {
    expect(failureOfMessage({ stopReason: 'error', error: { statusCode: 401 }, errorMessage: 'bad key' })).toEqual({
      class: 'auth',
      evidence: 'bad key',
    });
    expect(failureOfMessage({ stopReason: 'error', error: { statusCode: 429 } })?.class).toBe(
      'rate_limited'
    );
    expect(failureOfMessage({ stopReason: 'error', error: { statusCode: 503 } })?.class).toBe(
      'overloaded'
    );
    expect(failureOfMessage({ stopReason: 'error', error: { type: 'invalid_request_error' } })?.class).toBe(
      'misconfigured'
    );
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
      message: { stopReason: 'error', error: { statusCode: 403 }, errorMessage: 'forbidden' },
    });
    const chunks = mapper.push({ type: 'agent_settled', aborted: false });
    expect(chunks[0]).toMatchObject({
      type: 'result',
      isError: true,
      failure: { class: 'auth', evidence: 'forbidden' },
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
});
