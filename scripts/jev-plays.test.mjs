// Runs demo/jev-plays.js, the codemode script that has Jev play both games, against stub tools
// and a stub classifier, on a simulated clock: the play windows take no real time. It checks what
// the script promises: it finds the tools with searchTools("game"), asks the classifier which game
// to play first, plays each for its window one decision at a time, stops it, waits for its
// overlay to close, makes no other model call, and returns { order, games, decisions, total_ms }.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const source = readFileSync(fileURLToPath(new URL('../demo/jev-plays.js', import.meta.url)), 'utf8');
const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor;

const result = (value) => ({ content: [{ type: 'text', text: JSON.stringify(value) }], isError: false });
const toolError = (message) => ({ content: [{ type: 'text', text: message }], isError: true });

/** A game server stub: its own state, the calls made to it, and a scripted state per read. */
function gameServer(namespace, game, nextState) {
  const calls = [];
  const scores = [];
  let stopped = false;
  const tools = {
    games_list: () => result([{ name: game.name, description: game.description, instructions: 'How to play.', actions: game.actions }]),
    game_start: (args) => (args.name === game.name ? result({ ok: true, game: game.name }) : toolError(`unknown game ${args.name}`)),
    game_state: () => result(nextState()),
    game_act: (args) => {
      if (stopped) return toolError('the game is stopped');
      calls.push(args);
      if (args.action === 'stop') {
        stopped = true;
        return result({ ok: true, stopped: true, score: 40, high_score: 50, decisions: calls.length - 1 });
      }
      return result({ ok: true, decisions: calls.length });
    },
    game_score: (args) => {
      scores.push(args);
      return result({ score: 40, high_score: 55, decisions: calls.length, game_over: false, open: !stopped || !(args.wait_closed_ms > 0), stopped });
    },
  };
  return { namespace, tools, acted: calls, scores, game };
}
const RUNNER = { name: 'pig-runner', description: 'PiG Runner: a pig runs; jump pies, duck flying pies.', actions: ['jump', 'duck', 'restart', 'none', 'stop'] };
const ANGRY = { name: 'angry-pigs', description: 'Angry Pigs: slingshot.', actions: ['aim_up', 'aim_down', 'power_up', 'power_down', 'fire', 'next_level', 'restart', 'none', 'stop'] };

/**
 * Runs the script. `decide(question, context)` is the stub classifier's answer for every question;
 * `pickGame` is its answer to the game choice. Game tools advance the clock by 30 ms, as a round trip.
 */
async function runScript({ servers, pickGame, extraSetup }) {
  let now = 1_000_000;
  const log = { searches: [], started: [], classifyContexts: [], maxInFlight: 0, modelCalls: [], chatCalls: 0 };
  let inFlight = 0;

  const tools = {};
  for (const server of servers) {
    for (const [name, fn] of Object.entries(server.tools)) {
      tools[`mcp__${server.namespace}__${name}`] = async (args) => {
        now += 30;
        if (name === 'game_start') log.started.push({ server: server.namespace, name: args.name });
        await null;
        return fn(args ?? {});
      };
    }
  }
  const searchTools = async (query) => {
    log.searches.push(query);
    return Object.keys(tools).map((name) => ({ name, description: name }));
  };
  const jev = { provider: 'typesafe', id: 'jev-latest' };
  const models = new Proxy({}, {
    get(_, key) {
      if (key === 'getModelOfType') return async (type, provider, id) => { log.modelCalls.push(`getModelOfType ${type} ${provider}/${id}`); return jev; };
      if (key === 'classify') {
        return async (model, context) => {
          assert.deepEqual(model, jev, 'the classifier is the typesafe jev-latest model');
          inFlight++;
          log.maxInFlight = Math.max(log.maxInFlight, inFlight);
          log.classifyContexts.push(context);
          // Latency: 0.7 s of the simulated clock, as a Jev classification takes.
          now += 700;
          for (let i = 0; i < 40; i++) await null;
          inFlight--;
          const question = context.questions.pick;
          const answer = context.state.games ? pickGame(context) : extraSetup.decide(context);
          const choice = answer in question.criteria ? answer : Object.keys(question.criteria)[0];
          const probabilities = Object.fromEntries(Object.keys(question.criteria).map((k) => [k, k === choice ? 0.9 : 0.02]));
          return { stopReason: 'stop', answers: { pick: { type: 'choice', choice, probabilities, confidence: 0.9 } } };
        };
      }
      // Any other model call (a chat model, an image model) would break "Jev only".
      return (...args) => { log.modelCalls.push(String(key)); throw new Error(`models.${String(key)} must not be called`); };
    },
  });
  const clock = { now: () => now };
  const run = new AsyncFunction('tools', 'searchTools', 'models', 'Date', source);
  const value = await run(tools, searchTools, models, clock);
  return { value, log, elapsed: now - 1_000_000 };
}

// Runner stub: an obstacle approaches; at the decision point the run waits (decide_now).
function runnerWorld() {
  let reads = 0;
  const server = gameServer('games_runner', RUNNER, () => {
    reads++;
    const kinds = ['pie_small', 'flying_pie_mid', 'none', 'pie_large'];
    const kind = kinds[Math.floor(reads / 3) % kinds.length];
    const decideNow = kind !== 'none' && reads % 3 === 2;
    return {
      running: true, game_over: false, stopped: false, score: reads, speed: 115, decision_window_ms: 150, decide_now: decideNow,
      next_obstacle: { kind, distance_px: decideNow ? 17 : 60, time_to_impact_ms: decideNow ? 148 : 520, height: 2, width_px: 10 },
      pig: { jumping: false, ducking: false },
    };
  });
  const decide = ({ state }) => {
    if (!state.decide_now) return 'none';
    return state.next_obstacle.kind.startsWith('pie_') ? 'jump' : 'duck';
  };
  return { server, decide };
}

function angryWorld() {
  let reads = 0;
  const server = gameServer('games_angry_pigs', ANGRY, () => {
    reads++;
    const onTarget = reads % 4 === 0;
    return {
      shots_left: 4, score: reads * 5, level: 1, level_done: false, game_over: false, flying: false,
      targets: [{ x: 189, y: 21, material: 'bird' }, { x: 183, y: 15, material: 'wood' }],
      pig: { power: 70, angle: 45 }, aim_landing: { x: onTarget ? 189 : 152, y: 3, hit: onTarget ? 'bird' : 'ground' },
      landing_vs_target: onTarget ? 'on target' : 'short by 37 px', landing_vs_target_px: onTarget ? 0 : -37,
      last_shot_result: { outcome: 'none', birds_knocked: 0 },
    };
  });
  const decide = ({ state }) => (state.landing_vs_target === 'on target' ? 'fire' : 'power_up');
  return { server, decide };
}

/** Runs the script with a classifier that answers each game with its own world's rule. */
function playBoth(first) {
  const runner = runnerWorld();
  const angry = angryWorld();
  const decide = (context) => ('decide_now' in context.state ? runner.decide(context) : angry.decide(context));
  return { runner, angry, run: runScript({ servers: [angry.server, runner.server], pickGame: () => first, extraSetup: { decide } }) };
}

test('jev-plays.js', async (t) => {
  await t.test('finds the tools, lets Jev pick the first game, plays both for their windows, stops each and waits for it to close', async () => {
    const { runner, angry, run } = playBoth('pig-runner');
    const { value, log, elapsed } = await run;

    assert.deepEqual(log.searches, ['game']);
    assert.deepEqual(log.started, [{ server: 'games_runner', name: 'pig-runner' }, { server: 'games_angry_pigs', name: 'angry-pigs' }]);
    assert.deepEqual(log.modelCalls, ['getModelOfType classifier typesafe/jev-latest']);

    const pick = log.classifyContexts[0];
    assert.equal(pick.questions.pick.type, 'choice');
    assert.match(pick.questions.pick.instructions, /most fun to watch an AI play first/);
    assert.deepEqual(Object.keys(pick.questions.pick.criteria).sort(), ['angry-pigs', 'pig-runner']);

    // The Runner question speaks the state's words: decide_now and the pie kinds.
    const question = log.classifyContexts[1].questions.pick;
    assert.match(question.instructions, /decide_now/);
    assert.deepEqual(Object.keys(question.criteria), ['jump', 'duck', 'none']);
    assert.match(question.criteria.jump, /pie_small or pie_large/);
    assert.match(question.criteria.duck, /flying_pie_/);
    assert.doesNotMatch(JSON.stringify(question), /cactus|bird/);
    assert.equal(log.classifyContexts[1].state.next_obstacle.kind.startsWith('pie') || log.classifyContexts[1].state.next_obstacle.kind.startsWith('flying') || log.classifyContexts[1].state.next_obstacle.kind === 'none', true);

    // Angry Pigs: the question names landing_vs_target, and the shot is fired only on target.
    const apContext = log.classifyContexts.find((c) => 'landing_vs_target' in c.state);
    assert.match(apContext.questions.pick.instructions, /landing_vs_target/);
    assert.deepEqual(Object.keys(apContext.questions.pick.criteria), ['fire', 'power_up', 'power_down', 'aim_up', 'none']);
    assert.match(apContext.questions.pick.criteria.power_up, /short by/);

    assert.equal(log.maxInFlight, 1, 'one decision at a time');
    for (const server of [runner.server, angry.server]) {
      const actions = server.acted.map((a) => a.action);
      assert.equal(actions.at(-1), 'stop', `${server.game.name} ends with stop`);
      assert.equal(actions.filter((a) => a === 'stop').length, 1);
      assert.ok(server.scores.some((a) => a.wait_closed_ms > 0), `${server.game.name}: game_score waits for the overlay to close`);
    }
    assert.ok(runner.server.acted.some((a) => a.action === 'jump') && runner.server.acted.some((a) => a.action === 'duck'));
    assert.ok(runner.server.acted.filter((a) => a.action === 'jump' || a.action === 'duck').every((a) => a.confidence === 0.9));
    assert.ok(angry.server.acted.some((a) => a.action === 'fire') && angry.server.acted.some((a) => a.action === 'power_up'));
    // Fire comes only after a state that said "on target".
    assert.ok(log.classifyContexts.filter((c) => 'landing_vs_target' in c.state).length > 3);

    assert.ok(elapsed >= 20_000 && elapsed < 23_000, `the two windows ran ${elapsed} ms`);
    assert.deepEqual(value.order, ['pig-runner', 'angry-pigs']);
    assert.deepEqual(value.games.map((g) => g.game), ['pig-runner', 'angry-pigs']);
    for (const g of value.games) {
      assert.equal(g.final_score, 40);
      assert.equal(g.high_score, 55);
      assert.equal(g.closed, true);
      assert.ok(g.decisions > 5, `${g.game}: ${g.decisions} decisions`);
    }
    assert.equal(value.decisions, value.games[0].decisions + value.games[1].decisions);
    assert.ok(value.total_ms >= 20_000);
  });

  await t.test('plays the game Jev picked first', async () => {
    const { run } = playBoth('angry-pigs');
    const { value, log } = await run;
    assert.deepEqual(value.order, ['angry-pigs', 'pig-runner']);
    assert.deepEqual(log.started.map((s) => s.name), ['angry-pigs', 'pig-runner']);
  });

  await t.test('restarts after a crash without asking Jev', async () => {
    let reads = 0;
    const server = gameServer('games_runner', RUNNER, () => {
      reads++;
      const over = reads % 5 === 0;
      return { running: !over, game_over: over, score: reads, speed: 115, decide_now: false, decision_window_ms: 150, next_obstacle: { kind: 'none', distance_px: 0, time_to_impact_ms: 0, height: 0, width_px: 0 }, pig: { jumping: false, ducking: false } };
    });
    const { value, log } = await runScript({ servers: [server], pickGame: () => 'pig-runner', extraSetup: { decide: () => 'none' } });
    const restarts = server.acted.filter((a) => a.action === 'restart');
    assert.ok(restarts.length >= 3);
    assert.ok(restarts.every((a) => a.confidence === undefined), 'a restart is not a classification');
    assert.equal(value.games[0].restarts, restarts.length);
    assert.ok(log.classifyContexts.every((c) => !c.state.game_over), 'Jev was never asked about a crashed game');
  });

  await t.test('goes to the next level when one is cleared, without asking Jev', async () => {
    let reads = 0;
    const server = gameServer('games_angry_pigs', ANGRY, () => {
      reads++;
      const done = reads % 4 === 0;
      return {
        shots_left: 3, score: reads, level: 1, level_done: done, game_over: false, flying: false,
        targets: [{ x: 189, y: 21, material: 'bird' }], pig: { power: 70, angle: 45 },
        aim_landing: { x: 152, y: 3, hit: 'ground' }, landing_vs_target: 'short by 37 px', landing_vs_target_px: -37,
        last_shot_result: { outcome: 'none', birds_knocked: 0 },
      };
    });
    const { value, log } = await runScript({ servers: [server], pickGame: () => 'angry-pigs', extraSetup: { decide: () => 'power_up' } });
    const next = server.acted.filter((a) => a.action === 'next_level');
    assert.ok(next.length >= 3);
    assert.ok(next.every((a) => a.confidence === undefined));
    assert.equal(value.games[0].levels_cleared, next.length);
    assert.ok(log.classifyContexts.every((c) => !c.state.level_done));
  });

  await t.test('says what to do when no game tool exists', async () => {
    await assert.rejects(runScript({ servers: [], pickGame: () => '', extraSetup: { decide: () => 'none' } }), /No game tools found/);
  });

  await t.test('a tool error reaches the script as an Error', async () => {
    const server = gameServer('games_runner', RUNNER, () => { throw new Error('unused'); });
    server.tools.game_start = () => toolError('this session has no terminal UI to open the game in');
    await assert.rejects(runScript({ servers: [server], pickGame: () => 'pig-runner', extraSetup: { decide: () => 'none' } }), /no terminal UI/);
  });

  await t.test('the script calls no model but the classifier', () => {
    assert.doesNotMatch(source, /models\.(getAvailableOfType|getModelsOfType)/);
    assert.equal(source.match(/models\.[a-zA-Z]+/g).filter((m, i, all) => all.indexOf(m) === i).sort().join(), 'models.classify,models.getModelOfType');
  });
});
