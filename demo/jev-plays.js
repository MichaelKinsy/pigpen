// @options: {"timeout_ms": 180000, "max_output_tokens": 2000}
//
// Jev plays both Pigpen games, about 10 seconds each, through MCP, from a codemode script.
//
// Needs: a session with the games' MCP servers on (`/runner mcp on` and `/angry-pigs mcp on`, or
// PIG_GAMES_MCP=1 in the environment), a terminal UI to open the games in, codemode active, and
// the typesafe provider's key (TYPESAFE_API_KEY) for the jev-latest classifier.
//
// Every decision is Jev's: models.classify answers a typed choice question about the state the
// game reports, in the game's own words. The script never calls a chat model. It takes one
// decision at a time: the Runner waits at each obstacle for the answer (decide_now), and each
// Angry Pigs adjustment depends on the last.

const SECONDS_PER_GAME = 10;
// After stop, the overlay shows the final score until a key closes it. game_score waits up to
// this long for that before the script opens the next game (which otherwise opens on top).
const CLOSE_WAIT_MS = 8000;

const startedAt = Date.now();

// -- classifier --
const jev = await models.getModelOfType("classifier", "typesafe", "jev-latest");
if (!jev) throw new Error("The typesafe jev-latest classifier is not in the model catalog.");
const classify = (context) => models.classify(jev, context);
// -- end classifier --

let classifyCalls = 0;

// ask puts one choice question to Jev and returns { choice, p }: the answer and the probability
// Jev gave it. A failed classification returns undefined.
async function ask(state, instructions, criteria) {
  classifyCalls++;
  const result = await classify({ state, questions: { pick: { type: "choice", instructions, criteria } } });
  if (result.stopReason !== "stop") return undefined;
  const answer = result.answers.pick;
  if (!answer || !(answer.choice in criteria)) return undefined;
  return { choice: answer.choice, p: answer.probabilities?.[answer.choice] ?? answer.confidence };
}

// The result of an MCP tool call: its JSON text, or an Error for a tool error.
function value(result) {
  const text = result?.content?.[0]?.text;
  if (result?.isError) throw new Error(text ?? "tool error");
  return JSON.parse(text);
}

// 1. Find the games' tools. Each game is its own MCP server, mcp__<server>__<tool>.
const found = await searchTools("game", { limit: 30 });
const servers = {};
for (const { name } of found) {
  const m = /^(mcp__.+)__(games_list|game_start|game_state|game_act|game_score)$/.exec(name);
  if (!m) continue;
  servers[m[1]] = servers[m[1]] ?? {};
  servers[m[1]][m[2]] = (args) => tools[name](args ?? {});
}
const games = [];
for (const t of Object.values(servers)) {
  if (!t.games_list || !t.game_start || !t.game_state || !t.game_act || !t.game_score) continue;
  for (const game of value(await t.games_list())) games.push({ ...game, server: t });
}
if (games.length === 0) {
  throw new Error("No game tools found. Turn the servers on with `/runner mcp on` and `/angry-pigs mcp on`, then run the script again.");
}

// 2. Jev picks the game to watch first; the others follow.
const pickedGame = await ask(
  { games: games.map(({ name, description, actions }) => ({ name, description, actions })) },
  "Which game is most fun to watch an AI play first in a short clip?",
  Object.fromEntries(games.map((g) => [g.name, g.description])),
);
const first = games.find((g) => g.name === pickedGame?.choice) ?? games[0];
const order = [first, ...games.filter((g) => g !== first)];

// The questions, in the words of each game's state and instructions (games_list).
const RUNNER_QUESTION = "decide_now is true when the pig must act now. Which action avoids the next obstacle?";
const RUNNER_CHOICES = {
  jump: "Jump: decide_now is true and next_obstacle.kind is pie_small or pie_large, a pie on the road",
  duck: "Duck: decide_now is true and next_obstacle.kind is flying_pie_low, flying_pie_mid or flying_pie_high, a pie in the air",
  none: "Do nothing: decide_now is false, the next obstacle is not here yet",
};
const ANGRY_PIGS_QUESTION = "landing_vs_target says where the aimed shot ends against the nearest bird. Which action brings the shot onto the bird, or fires it?";
const ANGRY_PIGS_CHOICES = {
  fire: "Fire: landing_vs_target is \"on target\", the aimed shot ends at the bird",
  power_up: "Pull the band further: landing_vs_target is \"short by N px\" and pig.power is below 100",
  power_down: "Pull the band less: landing_vs_target is \"long by N px\"",
  aim_up: "Raise the arc: landing_vs_target is \"short by N px\" and pig.power is 100",
  none: "Wait: a pig is flying or no shot can be aimed",
};

// 3. Play one game for its window, then stop it and wait for its overlay to close.
async function play(game) {
  const t = game.server;
  value(await t.game_start({ name: game.name })); // the overlay a person gets
  const isRunner = game.actions.includes("jump");
  const act = (action, confidence) => t.game_act(confidence === undefined ? { action } : { action, confidence });
  const deadline = Date.now() + SECONDS_PER_GAME * 1000;
  let best = 0, restarts = 0, levelsCleared = 0, failed = 0;
  while (Date.now() < deadline) {
    const state = value(await t.game_state());
    best = Math.max(best, state.score);
    // Moves that need no judgment: start over after a crash, go on after a cleared level.
    if (state.game_over === true) { value(await act("restart")); restarts++; continue; }
    if (state.level_done === true) { value(await act("next_level")); levelsCleared++; continue; }
    const answer = isRunner
      ? await ask(state, RUNNER_QUESTION, RUNNER_CHOICES)
      : await ask(state, ANGRY_PIGS_QUESTION, ANGRY_PIGS_CHOICES);
    if (!answer) { failed++; continue; }
    value(await act(answer.choice, answer.p));
  }
  const stopped = value(await act("stop")); // the game freezes and shows the final score
  const score = value(await t.game_score({ wait_closed_ms: CLOSE_WAIT_MS }));
  return {
    game: game.name,
    decisions: stopped.decisions,
    score: Math.max(best, stopped.score), // the best of the window: a restart starts from zero
    final_score: stopped.score,
    high_score: score.high_score,
    restarts,
    levels_cleared: levelsCleared,
    failed,
    closed: !score.open,
  };
}

const results = [];
for (const game of order) results.push(await play(game));
return {
  order: order.map((g) => g.name),
  chosen_by_jev: pickedGame ? { choice: pickedGame.choice, p: pickedGame.p } : null,
  games: results,
  decisions: results.reduce((n, r) => n + r.decisions, 0),
  classify_calls: classifyCalls,
  total_ms: Date.now() - startedAt,
};
