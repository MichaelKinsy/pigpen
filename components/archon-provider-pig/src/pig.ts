/**
 * Runs one PiG turn as a child process and yields contract chunks.
 *
 * `pig --mode json` writes Pi's event stream as JSON Lines for exactly one prompt, which is the
 * whole of what an Archon turn needs. `--no-stdin` matters: PiG reads piped stdin until it ends,
 * and a caller that leaves the pipe open makes startup wait (PiG documents this divergence).
 */
import { spawn } from 'node:child_process';
import { PigTurnMapper, type PigEvent, type ProviderChunk } from './map';
import { readMessages } from './wire';

export interface PigTurnOptions {
  prompt: string;
  cwd: string;
  /** PiG's own model ref, without the Archon provider prefix. */
  model?: string;
  /** PiG session id to continue, from an earlier turn's Session header. */
  resumeSessionId?: string;
  /** Extra PiG flags, such as a tool allowlist. */
  flags?: readonly string[];
  signal?: AbortSignal;
  /** The `pig` executable; `pig` on PATH by default. */
  binary?: string;
}

/** Strips this plugin's own prefix from an Archon model ref: `pig/openrouter/x` → `openrouter/x`. */
export function pigModelRef(model: string | undefined): string | undefined {
  if (model === undefined) return undefined;
  return model.startsWith('pig/') ? model.slice('pig/'.length) : model;
}

export function pigArgs(options: PigTurnOptions): string[] {
  const args = ['--mode', 'json', '--no-stdin'];
  const model = pigModelRef(options.model);
  if (model !== undefined) args.push('--model', model);
  if (options.resumeSessionId !== undefined) args.push('--session-id', options.resumeSessionId);
  args.push(...(options.flags ?? []));
  args.push(options.prompt);
  return args;
}

/**
 * One turn's chunks. A cancelled turn yields its `result` with `stopReason: 'cancelled'` and then
 * ends: the contract forbids manufacturing `settled` for work that was stopped.
 */
export async function* runPigTurn(options: PigTurnOptions): AsyncGenerator<ProviderChunk> {
  if (options.signal?.aborted === true) {
    yield { type: 'result', stopReason: 'cancelled' };
    return;
  }
  const child = spawn(options.binary ?? 'pig', pigArgs(options), {
    cwd: options.cwd,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const mapper = new PigTurnMapper();
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (text: string) => {
    stderr = (stderr + text).slice(-4000);
  });

  const onAbort = (): void => {
    child.kill('SIGTERM');
  };
  options.signal?.addEventListener('abort', onAbort, { once: true });

  const exit = new Promise<number | null>(resolve => {
    child.once('close', code => resolve(code));
    child.once('error', () => resolve(null));
  });

  let settled = false;
  try {
    for await (const message of readMessages(child.stdout as AsyncIterable<Uint8Array>)) {
      const event = message as unknown as PigEvent;
      for (const chunk of mapper.push(event)) {
        if (chunk.type === 'settled') settled = true;
        yield chunk;
      }
    }
  } finally {
    options.signal?.removeEventListener('abort', onAbort);
  }

  const code = await exit;
  if (options.signal?.aborted === true) {
    yield { type: 'result', stopReason: 'cancelled' };
    return;
  }
  if (!settled) {
    const detail = stderr.trim().split('\n').slice(-3).join(' ').slice(0, 500);
    yield {
      type: 'warning',
      code: 'pig.exit_without_settle',
      message: `pig exited (${String(code)}) without settling${detail ? `: ${detail}` : ''}`,
    };
    yield { type: 'result', stopReason: code === 0 ? 'end_turn' : 'refusal' };
  }
}
