// Pigtorio's pure logic: no herdr imports, so bun can test it.

export const COLOR_SKINS = ["pink", "sandy", "grey", "mint", "cloud", "lavender", "green", "blush", "pig-default"];
export const CHARACTER_SKINS = ["pigrogu", "darth-vader", "kratos", "piglet", "spider-ham", "sheriff"];
export const ALL_SKINS = [...COLOR_SKINS, ...CHARACTER_SKINS];
export const SKIN_NAMES = {
  pink: "Rosy", sandy: "Sandy", grey: "Slate", mint: "Mint", cloud: "Cloud", lavender: "Lavender",
  green: "Green", blush: "Blush", "pig-default": "Pinky", pigrogu: "PiGrogu", "darth-vader": "Vader",
  kratos: "Kratos", piglet: "Piglet", "spider-ham": "Spider-Ham", sheriff: "Sheriff",
};

// Roles are what a pig is for. For PiG, the role's brief goes in with
// --append-system-prompt; other agents get it as the first line of a crate.
export const ROLES = [
  { id: "builder", label: "Builder", about: "read, bash, edit, write", brief: "You are a Builder pig in Pigtorio. Implement the crate you are handed, run the tests, and end with a hand-off: Goal / Done / Unverified / Files touched." },
  { id: "surveyor", label: "Surveyor", about: "plans crates; reads only", brief: "You are a Surveyor pig in Pigtorio. Do not edit files. Read the code and write a short plan for the crate, with open questions, then end with a hand-off: Goal / Done / Unverified / Files touched." },
  { id: "inspector", label: "Inspector", about: "risks first; read-only reviewer", brief: "You are an Inspector pig in Pigtorio. Do not edit files. Review the change in this folder (git diff) against the crate's done-when; list risks first, then evidence, then a verdict. End with a hand-off: Goal / Done / Unverified / Files touched." },
  { id: "forager", label: "Forager", about: "reads, cites, never writes", brief: "You are a Forager pig in Pigtorio. Research the question, cite sources, never edit files. End with a hand-off: Goal / Done / Unverified / Files touched." },
  { id: "taster", label: "Taster", about: "runs tests; bash + read", brief: "You are a Taster pig in Pigtorio. Run the tests that cover the crate and report exactly what passed, failed or was skipped. Do not edit source files." },
  { id: "scribe", label: "Scribe", about: "docs paths only", brief: "You are a Scribe pig in Pigtorio. Edit documentation only. End with a hand-off: Goal / Done / Unverified / Files touched." },
  { id: "tinker", label: "Tinker", about: "CI and scripts", brief: "You are a Tinker pig in Pigtorio. Work on CI, build scripts and tooling only. End with a hand-off: Goal / Done / Unverified / Files touched." },
];
export const roleOf = (id) => ROLES.find((r) => r.id === id) ?? null;

export function hash(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}

export function skinFor(address, farm) {
  const s = farm?.pigs?.[address]?.skin;
  return ALL_SKINS.includes(s) ? s : COLOR_SKINS[hash(address) % COLOR_SKINS.length];
}

export function pigName(address, farm) {
  return farm?.pigs?.[address]?.name || SKIN_NAMES[skinFor(address, farm)];
}

// What a pig looks like doing what it does. Pigsty's sprite states:
// idle asleep thinking reading editing running blocked failed done aborted
// queued completing orphaned unknown.
export function spriteState(status, crate) {
  if (crate?.state === "burnt") return "failed";
  if (status === "blocked") return "blocked";
  if (status === "working") return crate ? "editing" : "running";
  if (status === "done") return "done";
  if (crate?.state === "holding") return "completing";
  if (status === "idle") return "asleep";
  return "idle";
}

export function statusWord(status, crate) {
  if (crate?.state === "burnt") return "burnt";
  if (status === "blocked") return "blocked";
  if (status === "working") return "working";
  if (status === "done") return "done";
  if (crate?.state === "holding") return "holding";
  if (status === "idle") return "napping";
  return "idle";
}

export function fmtAge(ms) {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return "";
  const m = Math.floor(ms / 60000);
  if (m < 1) return "now";
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ${String(m % 60).padStart(2, "0")}`;
  return `${Math.floor(h / 24)} d`;
}

export function fmtTokens(n) {
  if (!n) return "0";
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

const BELL_RANK = { blocked: 0, burnt: 1, holding: 2, done: 3, stuck: 4, idle: 5 };
export const BELL_MARK = { blocked: "!", burnt: "x", holding: "o", done: "✓", stuck: "~", idle: "z" };
const STUCK_MS = 2 * 60 * 60 * 1000;

// The Bell's fixed order: blocked pigs, burnt crates, crates holding for
// review, pigs done while you weren't looking, pigs working over two hours,
// then idle pigs with a crate. Oldest first within a kind.
// A pig on a belt that finished has handed its work on: its "done" is the
// belt's news, not yours, so it stays out of the Bell (beltPigs: addresses).
export function bellOrder(pigs, crates, now, beltPigs = new Set()) {
  const items = [];
  for (const p of pigs) {
    if (p.status === "blocked") items.push({ kind: "blocked", pig: p.address, label: `${p.name} · blocked`, since: p.since });
    else if (p.status === "done" && !beltPigs.has(p.address)) items.push({ kind: "done", pig: p.address, label: `${p.name} · done`, since: p.since });
    else if (p.status === "working" && p.since != null && now - p.since > STUCK_MS) items.push({ kind: "stuck", pig: p.address, label: `${p.name} · working ${fmtAge(now - p.since)}`, since: p.since });
  }
  for (const c of crates ?? []) {
    if (c.state === "burnt") items.push({ kind: "burnt", crate: c.id, label: `${c.title} · burnt x${c.burns ?? 1}`, since: c.updated ?? c.created });
    else if (c.state === "holding") items.push({ kind: "holding", crate: c.id, pig: c.at?.pig, label: `${c.title} · holding`, since: c.updated ?? c.created });
    else if (c.state === "barn") items.push({ kind: "holding", crate: c.id, label: `${c.title} · at the Barn`, since: c.updated ?? c.created });
  }
  return items.sort((a, b) => BELL_RANK[a.kind] - BELL_RANK[b.kind] || (a.since ?? 0) - (b.since ?? 0));
}

// Pens flow left to right in rows inside a region, each as wide as its pigs.
export const PEN = { pigW: 112, pigH: 84, slot: 150, pad: 24, top: 14, labelH: 30, beltH: 70, minW: 250 };

export function penSize(pigCount, hasLine, maxW) {
  const w = Math.min(Math.max(PEN.minW, PEN.pad * 2 + Math.max(1, pigCount) * PEN.slot), Math.max(PEN.minW, maxW));
  const perRow = Math.max(1, Math.floor((w - PEN.pad * 2) / PEN.slot));
  const rows = Math.max(1, Math.ceil(pigCount / perRow));
  const h = PEN.top + rows * (PEN.pigH + PEN.labelH + 10) + (hasLine ? PEN.beltH : 8);
  return { w, h, perRow };
}

export function flowPens(pens, region) {
  const out = [];
  const gapX = 36, gapY = 52;
  let x = region.x, y = region.y, rowH = 0;
  for (const p of pens) {
    const s = penSize(p.pigs.length, Boolean(p.line), region.w);
    if (x > region.x && x + s.w > region.x + region.w) { x = region.x; y += rowH + gapY; rowH = 0; }
    out.push({ ...p, x, y, w: s.w, h: s.h, perRow: s.perRow });
    x += s.w + gapX;
    rowH = Math.max(rowH, s.h);
  }
  return { pens: out, bottom: y + rowH };
}

// Where each pig stands inside its pen (relative to the pen's top left).
export function pigSpots(pen) {
  return pen.pigs.map((pig, i) => {
    const col = i % pen.perRow, row = Math.floor(i / pen.perRow);
    return { pig, x: PEN.pad + col * PEN.slot + (PEN.slot - PEN.pigW) / 2 - 18, y: PEN.top + row * (PEN.pigH + PEN.labelH + 10) };
  });
}

// The machines: this one (null) first, then others by name.
export function machineOrder(spaces) {
  const names = [...new Set(spaces.map((s) => s.machine).filter((m) => m != null))].sort();
  return [null, ...names];
}

export function lineForPen(farm, spaceId) {
  return (farm?.lines ?? []).find((l) => l.pen === spaceId) ?? null;
}

// Crates remember their place on a line by stop index, so a line with crates
// on it must not change under them.
export function lineBusy(farm, crates, spaceId) {
  const line = (farm?.lines ?? []).find((l) => l.pen === spaceId);
  return Boolean(line) && (crates ?? []).some((c) => c.line === line.id && !["shipped", "failed", "board"].includes(c.state));
}

// Adds a relay from one pig to another to the pen's line, creating the line
// when the pen has none. A pig appears once per line.
export function addRelay(farm, spaceId, from, to) {
  const lines = [...(farm.lines ?? [])];
  let i = lines.findIndex((l) => l.pen === spaceId);
  if (i < 0) { lines.push({ id: `l${Date.now().toString(36)}`, pen: spaceId, stops: [] }); i = lines.length - 1; }
  const stops = [...lines[i].stops];
  const has = (a) => stops.some((s) => s.kind === "pig" && s.pig === a);
  // New pigs go before the first station: a pig after the Barn would never get a crate.
  const firstStation = () => { const k = stops.findIndex((s) => s.kind !== "pig"); return k < 0 ? stops.length : k; };
  if (!has(from)) stops.splice(firstStation(), 0, { kind: "pig", pig: from });
  if (!has(to)) {
    const at = stops.findIndex((s) => s.kind === "pig" && s.pig === from);
    stops.splice(at + 1, 0, { kind: "pig", pig: to });
  }
  lines[i] = { ...lines[i], stops };
  return { ...farm, lines };
}

const STATION_ORDER = { pig: 0, oven: 1, review: 2, barn: 3 };
// Adds a station to the pen's line; stations keep the order pigs, Oven, Review, Barn.
export function addStation(farm, spaceId, kind) {
  const lines = [...(farm.lines ?? [])];
  let i = lines.findIndex((l) => l.pen === spaceId);
  if (i < 0) { lines.push({ id: `l${Date.now().toString(36)}`, pen: spaceId, stops: [] }); i = lines.length - 1; }
  if (lines[i].stops.some((s) => s.kind === kind)) return farm;
  const stops = [...lines[i].stops, { kind }];
  stops.sort((a, b) => STATION_ORDER[a.kind] - STATION_ORDER[b.kind]);
  lines[i] = { ...lines[i], stops };
  return { ...farm, lines };
}

export function removeLine(farm, spaceId) {
  return { ...farm, lines: (farm.lines ?? []).filter((l) => l.pen !== spaceId) };
}

// The crates a pen's Job Board shows: on the board, oldest first.
export function boardCrates(crates) {
  return (crates ?? []).filter((c) => c.state === "board").sort((a, b) => (a.created ?? 0) - (b.created ?? 0));
}

export function activeCrateFor(address, crates) {
  return (crates ?? []).find((c) => c.at?.kind === "pig" && c.at.pig === address && !["shipped", "board", "failed"].includes(c.state)) ?? null;
}

// The morning report's numbers over a window ending now.
export function report(crates, pigs, now, windowMs) {
  const since = now - windowMs;
  const inWin = (t) => t != null && t >= since;
  const ev = (c, what) => (c.history ?? []).some((h) => h.what === what && inWin(h.t));
  const shipped = (crates ?? []).filter((c) => c.state === "shipped" && inWin(c.updated ?? c.created));
  const burnt = (crates ?? []).filter((c) => c.state === "burnt");
  const holding = (crates ?? []).filter((c) => c.state === "holding" || c.state === "barn");
  const blocked = pigs.filter((p) => p.status === "blocked");
  const tokens = (crates ?? []).filter((c) => inWin(c.updated ?? c.created)).reduce((n, c) => n + (c.tokens ?? 0), 0);
  const cost = (crates ?? []).filter((c) => inWin(c.updated ?? c.created)).reduce((n, c) => n + (c.cost ?? 0), 0);
  return { shipped, burnt, holding, blocked, tokens, cost, added: (crates ?? []).filter((c) => inWin(c.created)).length, moved: (crates ?? []).filter((c) => ev(c, "moved")).length };
}

export function filterFind(items, query) {
  const q = query.trim().toLowerCase();
  if (!q) return items;
  return items.filter((it) => it.label.toLowerCase().includes(q)).sort((a, b) => a.label.toLowerCase().indexOf(q) - b.label.toLowerCase().indexOf(q));
}

// ---------------------------------------------------------------- help (Pigpedia)

export const VIEW_KEYS = { m: "map", w: "walk", j: "board", a: "adopt", k: "workshop", r: "report", t: "terminals" };

// Rows of the Help dialog's key list. `keys` are the chips shown.
export const HELP_KEYS = [
  { keys: ["M", "W", "J", "A", "K", "R", "T"], does: "Map, Walk, Job Board, Adopt, Workshop, Report, Terminals" },
  { keys: ["B"], does: "Ring the Bell: go to the next pig or crate that needs you" },
  { keys: ["/"], does: "Find a pig or pen" },
  { keys: ["N"], does: "New crate" },
  { keys: ["?", "H"], does: "This Pigpedia" },
  { keys: ["Esc"], does: "Back to the Map" },
  { keys: ["←", "↑", "↓", "→"], does: "Walk the Swineherd (Walk view only)" },
];

// What each game verb runs underneath.
export const HELP_VERBS = [
  { verb: "Pen", runs: "A Herdr space, with its working folder" },
  { verb: "Pig", runs: "An agent in a terminal, reporting its state by OSC 7501" },
  { verb: "Crate", runs: "hc send of a brief to the pig" },
  { verb: "Belt", runs: "The engine relays a finished hand-off to the next pig" },
  { verb: "Oven", runs: "The pen's test command" },
  { verb: "Review", runs: "You read the diff, then approve" },
  { verb: "Barn", runs: "git commit, push, and a pull request, after you approve" },
];

// Shift-/ arrives as key "/" with shift (the key as printed, not typed); some layouts send "?".
export const isHelpKey = (e) => e.key === "?" || e.key_char === "?" || e.key === "h" || (e.key === "/" && Boolean(e.modifiers?.shift));
