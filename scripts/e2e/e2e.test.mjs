import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { answerLines, herdrStates } from './piglets.mjs';
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

  it('reads the reporter states from the fake herdr log, release included', () => {
    const log = [
      'pane report-agent p_1 --state idle --seq 1 --source custom:pig --agent pig',
      'pane report-agent p_1 --state working --seq 2 --source custom:pig --agent pig',
      'pane release-agent p_1 --source custom:pig --agent pig --seq 3',
    ].join('\n');
    assert.deepEqual(herdrStates(log), ['idle', 'working', 'release-agent']);
  });
});
