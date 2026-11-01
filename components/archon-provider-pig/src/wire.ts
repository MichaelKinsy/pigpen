/**
 * The Archon provider-plugin wire: newline-delimited JSON-RPC 2.0 over stdio.
 *
 * Archon's contributor guide publishes this wire as language-neutral, for authors who do not
 * import its contract package, so it is implemented here rather than vendored. The shapes are
 * the ones its own `serveProvider` uses: `initialize` answers with this plugin's descriptor,
 * `session/new` and `session/prompt` run a turn, chunks travel as `_archon/chunk`
 * notifications, and `session/cancel` stops one.
 */

export const PROTOCOL = 3;
export const MAX_MESSAGE_BYTES = 16 * 1024 * 1024;

export interface JsonRpcRequest {
  jsonrpc: '2.0';
  id?: string | number;
  method: string;
  params?: unknown;
}

export type Reply = { ok: true; result: unknown } | { ok: false; code: number; message: string };

/** Reads `\n`-delimited JSON-RPC lines from a stream of bytes. */
export async function* readMessages(
  stream: AsyncIterable<Uint8Array>
): AsyncGenerator<JsonRpcRequest> {
  const decoder = new TextDecoder();
  let buffer = '';
  for await (const chunk of stream) {
    buffer += decoder.decode(chunk, { stream: true });
    let newline = buffer.indexOf('\n');
    while (newline >= 0) {
      const line = buffer.slice(0, newline).replace(/\r$/, '');
      buffer = buffer.slice(newline + 1);
      newline = buffer.indexOf('\n');
      if (line.trim().length === 0) continue;
      const parsed: unknown = JSON.parse(line);
      if (typeof parsed !== 'object' || parsed === null) throw new Error('message is not an object');
      yield parsed as JsonRpcRequest;
    }
  }
  if (buffer.trim().length > 0) yield JSON.parse(buffer) as JsonRpcRequest;
}

/** One writer for replies and notifications: stdout carries nothing else. */
export function createWriter(write: (text: string) => void) {
  let queue = Promise.resolve();
  const send = (message: unknown): Promise<void> => {
    const line = `${JSON.stringify(message)}\n`;
    if (line.length > MAX_MESSAGE_BYTES) throw new Error('message exceeds the wire limit');
    queue = queue.then(() => {
      write(line);
    });
    return queue;
  };
  return {
    reply(id: string | number, result: unknown): Promise<void> {
      return send({ jsonrpc: '2.0', id, result });
    },
    fail(id: string | number | null, code: number, message: string): Promise<void> {
      return send({ jsonrpc: '2.0', id, error: { code, message } });
    },
    notify(method: string, params: unknown): Promise<void> {
      return send({ jsonrpc: '2.0', method, params });
    },
  };
}

export type Writer = ReturnType<typeof createWriter>;

/** Wraps a handler so one failing request never ends the session. */
export async function handle(
  writer: Writer,
  id: string | number | null,
  run: () => unknown | Promise<unknown>
): Promise<void> {
  try {
    const result = run();
    await writer.reply(id as string | number, result instanceof Promise ? await result : result);
  } catch (error) {
    const code = (error as { rpcCode?: number }).rpcCode ?? -32603;
    await writer.fail(id, code, error instanceof Error ? error.message : 'request failed');
  }
}
