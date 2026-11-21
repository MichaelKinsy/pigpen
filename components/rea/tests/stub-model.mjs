// A scripted OpenAI-compatible chat-completions server for the harness gates. Each request is recorded; the reply is what the
// `reply(request, index)` callback returns: { text } or { toolCall: { name, arguments } }. Streams when the request asks to.
import { createServer } from 'node:http';

function chunks({ text, toolCall }) {
  const base = { id: 'stub', object: 'chat.completion.chunk', created: 0, model: 'stub' };
  const out = [];
  if (toolCall) {
    out.push({ ...base, choices: [{ index: 0, delta: { role: 'assistant', tool_calls: [{ index: 0, id: toolCall.id ?? 'call_1', type: 'function', function: { name: toolCall.name, arguments: JSON.stringify(toolCall.arguments) } }] }, finish_reason: null }] });
    out.push({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } });
  } else {
    out.push({ ...base, choices: [{ index: 0, delta: { role: 'assistant', content: text ?? 'done' }, finish_reason: null }] });
    out.push({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } });
  }
  return out;
}

/** Starts the server; resolves to { url, requests, close }. */
export async function startStubModel(reply = () => ({ text: 'done' })) {
  const requests = [];
  const server = createServer((req, res) => {
    let body = '';
    req.setEncoding('utf8');
    req.on('data', (c) => { body += c; });
    req.on('end', () => {
      if (req.method !== 'POST' || !req.url.endsWith('/chat/completions')) {
        res.writeHead(404).end();
        return;
      }
      const request = JSON.parse(body);
      requests.push(request);
      const answer = reply(request, requests.length - 1);
      if (request.stream) {
        res.writeHead(200, { 'content-type': 'text/event-stream' });
        for (const chunk of chunks(answer)) res.write(`data: ${JSON.stringify(chunk)}\n\n`);
        res.end('data: [DONE]\n\n');
        return;
      }
      const message = answer.toolCall
        ? { role: 'assistant', content: null, tool_calls: [{ id: answer.toolCall.id ?? 'call_1', type: 'function', function: { name: answer.toolCall.name, arguments: JSON.stringify(answer.toolCall.arguments) } }] }
        : { role: 'assistant', content: answer.text ?? 'done' };
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ id: 'stub', object: 'chat.completion', created: 0, model: 'stub', choices: [{ index: 0, message, finish_reason: answer.toolCall ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
    });
  });
  await new Promise((resolveListen) => server.listen(0, '127.0.0.1', resolveListen));
  const { port } = server.address();
  return { url: `http://127.0.0.1:${port}/v1`, requests, close: () => new Promise((r) => server.close(r)) };
}

/** models.json content that points the provider `stub` at url. */
export const stubModels = (url) => ({ providers: { stub: { baseUrl: url, api: 'openai-completions', apiKey: 'stub', models: [{ id: 'stub', name: 'Stub', contextWindow: 128000, maxTokens: 4096 }] } } });
