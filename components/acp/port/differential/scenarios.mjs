// Scenarios for the wire differential (run.mjs). Each scenario is one adapter process driven by a scripted
// ACP client over newline-delimited JSON-RPC, with a scripted model (pigeq llm) behind it.
//
//   name     scenario name (the transcript file name)
//   quiet    quietStartup in the agent's settings.json (default true: no startup block)
//   update   "newer": the update lookup is on and finds a newer release (npm stub for the original, a local
//            registry for the port); anything else: every offline switch is set
//   turns    the scripted model's turns, one per model request
//   files    files to create before the run: "work/<path>", "agent/<path>" (text)
//   steps    the client script: { init }, { req, params, as }, { notify, params }, { permission }, { sleep }
//
// Strings in params may use ${CWD}, ${AGENT} and ${<as>.<field>} (a field of an earlier named result).
const text = (t) => [{ type: 'text', text: t }];
const sid = '${s.sessionId}';
const newSession = { req: 'session/new', params: { cwd: '${CWD}', mcpServers: [] }, as: 's' };
const prompt = (t, extra = {}) => ({ req: 'session/prompt', params: { sessionId: sid, prompt: text(t) }, ...extra });

export const scenarios = [
  { name: 'initialize', steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, { req: 'authenticate', params: { methodId: 'pi_terminal_login' } }] },
  {
    name: 'initialize-terminal-auth',
    steps: [{ init: { protocolVersion: 1, clientCapabilities: { _meta: { 'terminal-auth': true } } } }],
  },
  {
    name: 'initialize-future-protocol',
    steps: [{ init: { protocolVersion: 7, clientCapabilities: {} } }],
  },
  {
    name: 'unknown-methods',
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      { req: 'foo/bar', params: {} },
      { req: 'session/fork', params: { sessionId: 'x', cwd: '${CWD}' } },
      { req: 'session/resume', params: { sessionId: 'x', cwd: '${CWD}' } },
      { req: 'session/close', params: { sessionId: 'x' } },
    ],
  },
  {
    name: 'new-session-startup-block',
    quiet: false,
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession],
  },
  {
    name: 'new-session-quiet',
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession],
  },
  {
    name: 'new-session-update-notice-quiet',
    update: 'newer',
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession],
  },
  {
    name: 'new-session-update-notice-startup-block',
    update: 'newer',
    quiet: false,
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession],
  },
  {
    name: 'new-session-context-skills-prompts',
    quiet: false,
    files: {
      'work/AGENTS.md': 'Project rules.\n',
      'agent/prompts/review.md': '---\ndescription: Review the code\n---\nReview $1 carefully.\n',
      'work/.pi/skills/demo/SKILL.md': '---\nname: demo\ndescription: A demo skill\n---\nDemo.\n',
    },
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession],
  },
  {
    name: 'new-session-invalid-params',
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      { req: 'session/new', params: { cwd: 'relative/dir', mcpServers: [] } },
      { req: 'session/new', params: { mcpServers: [] } },
    ],
  },
  {
    name: 'prompt-text',
    turns: [{ text: 'Hello from the scripted model' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('say hello')],
  },
  {
    name: 'prompt-input-kinds',
    turns: [{ text: 'one' }, { text: 'two' }, { text: 'three' }],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      {
        req: 'session/prompt',
        params: {
          sessionId: sid,
          prompt: [
            { type: 'text', text: 'look at these' },
            { type: 'resource_link', uri: 'file:///tmp/example.txt', name: 'example.txt' },
            { type: 'resource', resource: { uri: 'file:///tmp/emb.txt', text: 'embedded body' } },
          ],
        },
      },
      {
        req: 'session/prompt',
        params: { sessionId: sid, prompt: [{ type: 'image', mimeType: 'image/png', data: 'iVBORw0KGgo=' }, { type: 'text', text: 'what is this' }] },
      },
      { req: 'session/prompt', params: { sessionId: sid, prompt: [{ type: 'audio', mimeType: 'audio/wav', data: 'AAAA' }, { type: 'text', text: 'listen' }] } },
    ],
  },
  {
    name: 'prompt-bash-tool',
    turns: [{ toolCalls: [{ name: 'bash', arguments: { command: 'echo diff-ok; sleep 0.5; echo oops 1>&2' } }] }, { text: 'all done' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('run a command')],
  },
  {
    name: 'prompt-bash-tool-fails',
    turns: [{ toolCalls: [{ name: 'bash', arguments: { command: 'echo before; exit 3' } }] }, { text: 'it failed' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('run a failing command')],
  },
  {
    name: 'prompt-write-edit-read',
    turns: [
      { toolCalls: [{ name: 'write', arguments: { path: 'notes.txt', content: 'alpha\nbeta\ngamma\n' } }] },
      { text: 'written' },
      { toolCalls: [{ name: 'edit', arguments: { path: 'notes.txt', edits: [{ oldText: 'beta', newText: 'BETA' }] } }] },
      { text: 'edited' },
      { toolCalls: [{ name: 'read', arguments: { path: 'notes.txt' } }] },
      { text: 'read' },
    ],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      prompt('create the file'),
      prompt('change beta'),
      prompt('read it back'),
    ],
  },
  {
    name: 'prompt-tool-errors',
    turns: [
      { toolCalls: [{ name: 'read', arguments: { path: 'missing.txt' } }] },
      { text: 'could not read' },
      { toolCalls: [{ name: 'edit', arguments: { path: 'notes.txt', edits: [{ oldText: 'nowhere', newText: 'x' }] } }] },
      { text: 'could not edit' },
    ],
    files: { 'work/notes.txt': 'one\ntwo\n' },
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('read the missing file'), prompt('edit the wrong text')],
  },
  {
    name: 'prompt-queue-and-idle',
    turns: [{ text: 'first' }, { text: 'second' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('one'), prompt('two')],
  },
  {
    name: 'builtin-commands',
    turns: [{ text: 'a reply so there is a session to report' }],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      prompt('hello'),
      prompt('/session'),
      prompt('/name Renamed by the client'),
      prompt('/name'),
      prompt('/steering'),
      prompt('/steering all'),
      prompt('/steering bogus'),
      prompt('/follow-up'),
      prompt('/follow-up one-at-a-time'),
      prompt('/follow-up bogus'),
      prompt('/autocompact'),
      prompt('/autocompact off'),
      prompt('/autocompact on'),
      prompt('/export'),
    ],
  },
  {
    // Compaction needs a session big enough to cut (Pi keeps the last ~20k tokens), so two long prompts come first.
    // While it runs Pi and PiG emit compaction_start/compaction_end; the original handles only the auto_compaction_* names (F1).
    name: 'builtin-compact',
    turns: [{ text: 'first long answer' }, { text: 'second long answer' }, { text: 'summary of the conversation' }, { text: 'second summary' }],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      prompt('word '.repeat(30000)),
      prompt('more '.repeat(30000)),
      prompt('/compact'),
      prompt('/compact focus on tests'),
    ],
  },
  {
    name: 'builtin-changelog',
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('/changelog')],
  },
  {
    name: 'prompt-template',
    files: { 'agent/prompts/review.md': '---\ndescription: Review the code\n---\nReview $1 and then $@ carefully.\n' },
    turns: [{ text: 'reviewed' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('/review "the parser" fast')],
  },
  {
    name: 'config-options',
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      { req: 'session/set_mode', params: { sessionId: sid, modeId: 'low' } },
      { req: 'session/set_mode', params: { sessionId: sid, modeId: 'nonsense' } },
      { req: 'session/set_config_option', params: { sessionId: sid, configId: 'thought_level', value: 'high' } },
      { req: 'session/set_config_option', params: { sessionId: sid, configId: 'model', value: 'acp-llm/acp-1' } },
      { req: 'session/set_config_option', params: { sessionId: sid, configId: 'model', value: 'acp-llm/nope' } },
      { req: 'session/set_config_option', params: { sessionId: sid, configId: 'nonsense', value: 'x' } },
      { req: 'session/set_model', params: { sessionId: sid, modelId: 'acp-llm/acp-1' } },
      { req: 'session/set_model', params: { sessionId: sid, modelId: 'nope' } },
    ],
  },
  {
    name: 'sessions-list-load-delete',
    turns: [{ text: 'first session reply' }, { text: 'second session reply' }, { text: 'after load' }],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      prompt('remember this'),
      { req: 'session/list', params: { cwd: '${CWD}' } },
      { req: 'session/list', params: {} },
      { req: 'session/list', params: { cwd: '${CWD}', cursor: 'not-a-number' } },
      { req: 'session/new', params: { cwd: '${CWD}', mcpServers: [] }, as: 's2' },
      { req: 'session/load', params: { sessionId: sid, cwd: '${CWD}', mcpServers: [] } },
      { req: 'session/prompt', params: { sessionId: sid, prompt: text('after the load') } },
      { req: 'session/load', params: { sessionId: 'no-such-session', cwd: '${CWD}', mcpServers: [] } },
      { req: 'session/delete', params: { sessionId: sid } },
      { req: 'session/delete', params: { sessionId: 'no-such-session' } },
      { req: 'session/list', params: { cwd: '${CWD}' } },
    ],
  },
  {
    name: 'load-replays-tool-history',
    turns: [{ toolCalls: [{ name: 'bash', arguments: { command: 'echo history' } }] }, { text: 'ran it' }],
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      newSession,
      prompt('run something'),
      { req: 'session/new', params: { cwd: '${CWD}', mcpServers: [] }, as: 's2' },
      { req: 'session/load', params: { sessionId: sid, cwd: '${CWD}', mcpServers: [] } },
    ],
  },
  {
    name: 'prompt-unknown-and-cancel',
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      { req: 'session/prompt', params: { sessionId: 'no-such-session', prompt: text('hello') } },
      { notify: 'session/cancel', params: { sessionId: 'no-such-session' } },
      newSession,
      { notify: 'session/cancel', params: { sessionId: sid } },
    ],
  },
  {
    name: 'no-model-auth-required',
    models: 'none',
    steps: [{ init: { protocolVersion: 1, clientCapabilities: { _meta: { 'terminal-auth': true } } } }, newSession],
  },
  {
    name: 'mcp-servers-accepted',
    steps: [
      { init: { protocolVersion: 1, clientCapabilities: {} } },
      { req: 'session/new', params: { cwd: '${CWD}', mcpServers: [{ name: 'x', command: '/bin/true', args: [], env: [] }] }, as: 's' },
    ],
  },
  // The two scenarios below are also the performance scenarios (run.mjs --bench).
  {
    name: 'bench-session',
    turns: [{ text: 'ok' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('hello'), prompt('/session'), prompt('/name bench')],
  },
  {
    name: 'bench-session-update-lookup',
    update: 'newer',
    turns: [{ text: 'ok' }],
    steps: [{ init: { protocolVersion: 1, clientCapabilities: {} } }, newSession, prompt('hello'), prompt('/session'), prompt('/name bench')],
  },
];
