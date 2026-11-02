#!/usr/bin/env bun
/**
 * The `archon-provider-pig` executable: an Archon provider plugin speaking
 * newline-delimited JSON-RPC on stdio, running each turn through `pig`.
 *
 * Capabilities are the narrow set this implementation enforces. PiG can do more than is
 * claimed here (extensions, MCP servers, skills, sub-agents), but the contract treats a
 * capability as a promise, and a promise means the plugin suppresses ambient configuration and
 * loads exactly what a node names. Until that boundary is implemented and tested, the flags say
 * so instead of letting a workflow believe its skills or MCP servers were loaded.
 */
import { spawn } from 'node:child_process';
import { createWriter, handle, readMessages, type JsonRpcRequest, PROTOCOL } from './wire';
import { pigModelRef, runPigTurn } from './pig';
import type { ProviderChunk } from './map';

const VERSION = '0.1.0';

const runSchema = {
  type: 'object',
  properties: {
    model: { type: 'string', minLength: 1 },
    tools: { type: 'array', items: { type: 'string', minLength: 1 } },
  },
  additionalProperties: false,
} as const;

export const descriptor = {
  protocol: PROTOCOL,
  id: 'pig',
  displayName: 'PiG',
  version: VERSION,
  capabilities: {
    backgroundWork: 'none',
    sessionResume: true,
    mcp: false,
    hooks: false,
    skills: false,
    plugins: false,
    agents: false,
    toolRestrictions: true,
    structuredOutput: false,
    requiresAllPropertiesRequired: false,
    envInjection: false,
    costControl: false,
    costReporting: false,
    tokenReporting: true,
    stopReasonReporting: true,
    effortControl: false,
    fallbackModel: false,
    sandbox: false,
    settingSources: false,
    nativeTools: false,
    containerExec: false,
  },
  credentials: { kind: 'static', specs: [] },
  configSchema: runSchema,
  config: { install: runSchema, run: runSchema, snapshot: { type: 'object', properties: {} }, stripUnknownKeys: true },
} as const;

interface Session {
  request: {
    prompt?: string;
    cwd?: string;
    resumeSessionId?: string;
    model?: string;
    systemPrompt?: string;
    appendSystemPrompt?: string;
    nodeConfig?: { model?: string; tools?: string[] };
  };
  abort: AbortController;
  active: boolean;
}

const sessions = new Map<string, Session>();
let nextSession = 0;
let initialized = false;

/**
 * The vendor a model ref names: `pig/openrouter/x` and `openrouter/x` both name `openrouter`.
 * Archon hands the plugin the model a node named, which may or may not carry our own prefix, so
 * the vendor is the first segment after stripping it, never the provider id itself.
 */
export function vendorOf(model: string | undefined): string | undefined {
  if (model === undefined) return undefined;
  const ref = model.startsWith('pig/') ? model.slice('pig/'.length) : model;
  const vendor = ref.split('/')[0];
  return vendor !== undefined && vendor.length > 0 && vendor !== 'pig' ? vendor : undefined;
}

/** Runs `pig auth check` for one model or vendor and reports what it answered. */
async function checkCredential(model: string | undefined, vendor: string | undefined) {
  const args = ['auth', 'check', '--json'];
  if (vendor !== undefined) args.push('--provider', vendor);
  else if (model !== undefined) args.push('--model', model);
  else return { state: 'not_checked', source: 'native' } as const;

  const child = spawn('pig', args, { stdio: ['ignore', 'pipe', 'pipe'] });
  let out = '';
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (text: string) => {
    out += text;
  });
  const code = await new Promise<number | null>(resolve => child.once('close', resolve));
  try {
    const parsed = JSON.parse(out) as { status?: string; reason?: string };
    if (parsed.status === 'ready') return { state: 'usable', source: 'native' } as const;
    if (parsed.status === 'not_ready' && parsed.reason === 'credentials_not_configured')
      return { state: 'not_connected', source: 'native' } as const;
    if (parsed.status === 'not_ready')
      return { state: 'check_failed', source: 'native', evidence: parsed.reason ?? 'not ready' } as const;
  } catch {
    // fall through: an unreadable answer is a failed check, never a silent success
  }
  return {
    state: 'check_failed',
    source: 'native',
    evidence: `pig auth check exited ${String(code)}`,
  } as const;
}

function onRequest(writer: ReturnType<typeof createWriter>, message: JsonRpcRequest): void {
  const { id, method, params } = message;
  const isNotification = id === undefined;

  if (method === 'initialize') {
    const protocol = (params as { clientCapabilities?: { _meta?: { archon?: { protocol?: number } } } })
      ?.clientCapabilities?._meta?.archon?.protocol;
    if (protocol === PROTOCOL) initialized = true;
    void handle(writer, id ?? null, () => ({
      protocolVersion: 1,
      agentCapabilities: { _meta: { archon: descriptor } },
      authMethods: [],
    }));
    return;
  }

  if (method === 'session/cancel') {
    const sessionId = (params as { sessionId?: string })?.sessionId;
    const session = sessionId === undefined ? undefined : sessions.get(sessionId);
    session?.abort.abort();
    if (session !== undefined && !session.active && sessionId !== undefined) sessions.delete(sessionId);
    return;
  }

  void handle(writer, id ?? null, async () => {
    if (!initialized) throw new Error('initialize must complete before provider requests');

    if (method === 'session/new') {
      const parsed = params as {
        cwd?: string;
        _meta?: { archon?: { request?: Session['request'] } };
      };
      const request = { ...(parsed._meta?.archon?.request ?? {}) };
      if (parsed.cwd !== undefined) request.cwd = parsed.cwd;
      const sessionId = String(nextSession++);
      sessions.set(sessionId, { request, abort: new AbortController(), active: false });
      return { sessionId };
    }

    if (method === 'session/prompt') {
      const parsed = params as { sessionId?: string; prompt?: { text?: string }[] };
      const session = parsed.sessionId === undefined ? undefined : sessions.get(parsed.sessionId);
      if (session === undefined || session.active) throw new Error('Unknown or active provider session');
      session.active = true;
      const sessionId = parsed.sessionId as string;
      try {
        const model = session.request.nodeConfig?.model ?? session.request.model;
        const flags: string[] = [];
        if (session.request.systemPrompt !== undefined)
          flags.push('--system-prompt', session.request.systemPrompt);
        if (session.request.appendSystemPrompt !== undefined)
          flags.push('--append-system-prompt', session.request.appendSystemPrompt);
        const tools = session.request.nodeConfig?.tools;
        if (tools !== undefined && tools.length > 0) flags.push('--tools', tools.join(','));

        let stopReason: string | undefined;
        for await (const chunk of runPigTurn({
          prompt: session.request.prompt ?? parsed.prompt?.[0]?.text ?? '',
          cwd: session.request.cwd ?? process.cwd(),
          model: pigModelRef(model),
          resumeSessionId: session.request.resumeSessionId,
          flags,
          signal: session.abort.signal,
        })) {
          if (chunk.type === 'result') stopReason = chunk.stopReason;
          await writer.notify('_archon/chunk', { sessionId, chunk: chunk satisfies ProviderChunk });
        }
        return { stopReason: stopReason ?? (session.abort.signal.aborted ? 'cancelled' : 'end_turn') };
      } finally {
        sessions.delete(sessionId);
      }
    }

    if (method === '_archon/check_credential') {
      const parsed = params as { vendor?: string; model?: string; nodeConfig?: { model?: string } };
      const model = parsed.model ?? parsed.nodeConfig?.model;
      return checkCredential(model, parsed.vendor ?? vendorOf(model));
    }

    if (method === '_archon/diagnose') {
      const status = await checkCredential(undefined, 'openrouter');
      return {
        checks: [
          {
            id: 'pig.auth',
            label: 'PiG authentication',
            status: status.state === 'usable' ? 'ok' : status.state === 'not_connected' ? 'warn' : 'fail',
            message:
              status.state === 'usable'
                ? 'pig reports a usable credential'
                : `pig auth check answered ${status.state}${'evidence' in status ? `: ${status.evidence}` : ''}`,
            remedy: status.state === 'usable' ? undefined : 'Run: pig login <provider>, then pig auth check',
          },
        ],
      };
    }

    const error = new Error(`${method} is not supported by this provider`);
    (error as { rpcCode?: number }).rpcCode = -32601;
    throw error;
  });

  void isNotification;
}

async function main(): Promise<void> {
  const writer = createWriter(text => {
    process.stdout.write(text);
  });
  for await (const message of readMessages(process.stdin as AsyncIterable<Uint8Array>)) {
    if (typeof message.method !== 'string') continue;
    onRequest(writer, message);
  }
}

if (import.meta.main) await main();
