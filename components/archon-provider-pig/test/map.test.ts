import { describe, expect, it } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { PigTurnMapper, toolOutputOf, usageOf, type PigEvent } from '../src/map';

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
});
