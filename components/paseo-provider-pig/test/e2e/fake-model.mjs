// A scripted OpenAI-compatible chat completions server for the e2e run: no network, no key.
// The latest user message picks the reply:
//   RUNBASH   one bash tool call (echo paseo-ok), then a text reply quoting the result
//   RUNEDIT   one edit tool call on README.md, then a text reply quoting the result
//   README    one read tool call on README.md, then a text reply quoting the first line
//   otherwise a short text reply
import { createServer } from "node:http";

const text = (content) => (typeof content === "string" ? content : Array.isArray(content) ? content.map((part) => part.text ?? "").join("") : "");
const chunk = (delta, finish, usage) => `data: ${JSON.stringify({ id: "fake", object: "chat.completion.chunk", model: "fake-model", choices: [{ index: 0, delta: delta ?? {}, finish_reason: finish ?? null }], ...(usage ? { usage } : {}) })}\n\n`;

function toolCall(name, args) {
  const json = JSON.stringify(args);
  const pieces = [chunk({ role: "assistant", content: "" }), chunk({ content: "Let me check. " }), chunk({ tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name, arguments: "" } }] })];
  for (let i = 0; i < json.length; i += 16) pieces.push(chunk({ tool_calls: [{ index: 0, function: { arguments: json.slice(i, i + 16) } }] }));
  pieces.push(chunk({}, "tool_calls", { prompt_tokens: 30, completion_tokens: 10, total_tokens: 40 }));
  return pieces;
}

function reply(messages) {
  const last = messages.at(-1);
  if (last?.role === "tool") {
    const first = text(last.content).trim().split("\n")[0] ?? "";
    return [chunk({ role: "assistant", content: "" }), chunk({ content: `The tool returned: ${first.slice(0, 80)}` }), chunk({}, "stop", { prompt_tokens: 40, completion_tokens: 12, total_tokens: 52 })];
  }
  const user = messages.filter((message) => message.role === "user").map((message) => text(message.content)).at(-1) ?? "";
  if (user.includes("RUNEDIT")) return toolCall("edit", { path: "README.md", edits: [{ oldText: "# Demo project", newText: "# Demo project (edited)" }] });
  if (user.includes("RUNBASH")) return toolCall("bash", { command: "echo paseo-ok" });
  if (user.includes("README")) return toolCall("read", { path: "README.md" });
  return [chunk({ role: "assistant", content: "" }), ...["Hello! ", "This reply ", "comes from ", "a local fake model."].map((word) => chunk({ content: word })), chunk({}, "stop", { prompt_tokens: 20, completion_tokens: 8, total_tokens: 28 })];
}

/** Starts the server on a free port; resolves to { port, close }. */
export function startFakeModel() {
  const server = createServer((request, response) => {
    if (request.method === "GET") {
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ data: [{ id: "fake-model", object: "model" }] }));
      return;
    }
    let body = "";
    request.on("data", (part) => (body += part));
    request.on("end", () => {
      const { messages = [] } = JSON.parse(body || "{}");
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(`${reply(messages).join("")}data: [DONE]\n\n`);
    });
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve({ port: server.address().port, close: () => new Promise((done) => server.close(done)) }));
  });
}
