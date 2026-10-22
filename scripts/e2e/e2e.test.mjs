import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { connect } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { answerLines, fakeHerdr, herdrStates } from './piglets.mjs';
import { e2eEnv, isolatedPath, promptReady, shellQuote } from './tmux.mjs';

describe('piglet e2e helpers', () => {
  it('quotes a shell word, including a single quote', () => {
    assert.equal(shellQuote("it's"), `'it'\\''s'`);
    assert.equal(shellQuote('a b'), "'a b'");
  });

  it('gives the Binary system directories only, and a home that holds all state', () => {
    const env = e2eEnv('/tmp/h', { X: '1' });
    assert.equal(env.PATH, isolatedPath('/tmp/h'));
    assert.equal(env.PATH, '/tmp/h/sysbin');
    assert.equal(env.HOME, '/tmp/h');
    assert.equal(env.PIG_HOME, '/tmp/h/pig');
    assert.equal(env.PIG_CODING_AGENT_DIR, '/tmp/h/agent');
    assert.equal(env.PI_CODING_AGENT_DIR, '/tmp/h/pi');
    for (const name of ['XDG_RUNTIME_DIR', 'XDG_CACHE_HOME', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME']) assert.ok(env[name].startsWith('/tmp/h/'), `${name} stays under the home: a player's socket or cache must not be shared between runs`);
    assert.equal(env.X, '1');
    assert.equal(env.PIG_TEST_FAUX, '1');
  });

  it('lets a Piglet scenario put its own tools first on PATH', () => {
    assert.equal(e2eEnv('/tmp/h', { PATH: '/tmp/h/fakebin:/usr/bin:/bin' }).PATH, '/tmp/h/fakebin:/usr/bin:/bin');
  });

  it('knows the editor is ready only when the input box and the model footer are drawn', () => {
    const rule = '─'.repeat(40);
    assert.equal(promptReady(`banner\n${rule}\n\n${rule}\n~/work\n0.0%/128k (auto)   faux-1\n`), true);
    assert.equal(promptReady('banner only, still starting'), false);
    assert.equal(promptReady(`${rule}\n${rule}\nno model yet`), false);
  });

  it('finds the faux answer as a line of its own, not as part of other text', () => {
    assert.equal(answerLines('reply with exactly: ok\n\n ok\n').length, 1);
    assert.equal(answerLines('reply with exactly: ok\nlook\n').length, 0);
  });

  it('reads the reporter states from the requests a fake herdr took, release included', () => {
    const params = { pane_id: 'p_1', source: 'pig', agent: 'pig' };
    const requests = [
      { id: 'pig:report:1', method: 'pane.report_agent', params: { ...params, state: 'idle', seq: 1 } },
      { id: 'pig:report:2', method: 'pane.report_agent', params: { ...params, state: 'working', seq: 2 } },
      { id: 'pig:release:3', method: 'pane.release_agent', params: { ...params, seq: 3 } },
      { unparsed: 'not json' },
    ];
    assert.deepEqual(herdrStates(requests), ['idle', 'working', 'release-agent', 'unparsed']);
  });

  it('listens before it returns, and answers every request line of a connection, in order', { skip: process.platform === 'win32' && 'unix sockets' }, async () => {
    const dir = mkdtempSync(join(tmpdir(), 'pigpen-fake-herdr-'));
    const path = join(dir, 'herdr.sock');
    const herdr = fakeHerdr(path);
    try {
      assert.ok(existsSync(path), 'the socket exists as soon as fakeHerdr returns: the Binary is started after that');
      const replies = await new Promise((resolve, reject) => {
        let got = '';
        const client = connect(path, () => {
          // one line split across two writes, then a second line: the server must frame on the newline, not on the write
          client.write('{"id":"a","method":"pane.report_agent","par');
          setTimeout(() => client.write('ams":{"state":"idle","seq":1}}\n{"id":"b","method":"pane.release_agent","params":{"seq":2}}\nnot json\n'), 20);
        });
        client.setEncoding('utf8');
        client.on('data', (chunk) => { got += chunk; if (got.split('\n').filter(Boolean).length === 3) { client.end(); resolve(got); } });
        client.on('error', reject);
      });
      assert.equal(replies, '{"result":{}}\n'.repeat(3));
      assert.deepEqual(herdrStates(herdr.requests), ['idle', 'release-agent', 'unparsed']);
      assert.equal(herdr.requests[0].id, 'a');
    } finally {
      herdr.close();
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
