// The Runt's brains: hints, ghost resolution and the check. Pure (no herdr
// imports) so bun can test it. See ARCHITECTURE.md "ghosts.json".
import { KINDS, addBelt, deriveLines, freeSpot, penEntity, pigTiles, placeEntity, rect } from "./farmworld.js";

export const GHOST_FILE = "ghosts.json";
const END_KINDS = new Set(["review", "barn"]);

export function RUNT_BRIEF(D) {
  const bin = `${D}/bin/pigtorio`;
  return [
    "You are the Runt, Pigtorio's foreman: the farm's guide pig. Pigtorio is Factorio for coding agents: pens hold pigs, belts carry crates between pigs and stations (Oven runs tests, Review Station holds for a person, Barn ships).",
    "You are read-only. You never edit files, never build, never push. Use read-only shell commands (cat, ls, grep, git log/status/diff) to look around.",
    `The only commands you may run that change anything are your drafting verbs: \`${bin} farm show\` (read the farm) and \`${bin} ghost ...\`:`,
    "  ghost place --kind oven|review|barn [--pen SPACE] [--near pig:ADDR|ent:ID] [--x N --y N]   draft a building (x and y together, not with --near)",
    "  ghost belt --from REF --to REF      draft a belt; REF is pig:ADDR, ent:ID or ghost:ID (a ghost placed earlier in this draft); ends are pigs, Ovens, Review Stations, Barns",
    "  ghost crate --title T --done-when W [--text X] [--size one-shot|large] [--pen SPACE]   draft a crate for the Job Board",
    "  ghost note TEXT                     say why you drafted this",
    "  ghost list [--json]                 show the current draft",
    "  ghost clear                         throw the draft away",
    "Each add prints a ghost id (g1, g2, ...). A belt stays inside one pen; every belt must end at a Review Station or the Barn, or the check fails with \"this belt dead-ends\".",
    "You draft, you never build. Draft the change, explain it in a few lines, then tell the person to press BUILD in the Runt panel (or DISCARD). Ghosts do nothing until they do.",
    "Keep answers short and concrete. Say which pig or pen you mean by name.",
  ].join("\n");
}

// ---------------------------------------------------------------- draft files

export const emptyDraft = () => ({ note: "", by: "runt", ghosts: [] });

export function parseGhosts(text) {
  if (!text) return null;
  try {
    const g = JSON.parse(text);
    if (!g || !Array.isArray(g.ghosts) || !g.ghosts.length) return null;
    return g;
  } catch { return null; }
}

const ghostNum = (id) => Number(/^g(\d+)$/.exec(id ?? "")?.[1] ?? 0);

// Appends ops (local ids, ghost:<local> refs) to a draft, renumbering to g<n>.
export function appendGhosts(draft, ops, note) {
  const d = draft && Array.isArray(draft.ghosts) ? { ...draft, ghosts: [...draft.ghosts] } : emptyDraft();
  let n = Math.max(0, ...d.ghosts.map((g) => ghostNum(g.id)));
  const map = {};
  for (const op of ops) if (op.id) map[op.id] = `g${++n}`;
  const fix = (r) => (typeof r === "string" && r.startsWith("ghost:") && map[r.slice(6)] ? `ghost:${map[r.slice(6)]}` : r);
  for (const op of ops) {
    const g = { ...op, id: op.id ? map[op.id] : `g${++n}` };
    if (g.from) g.from = fix(g.from);
    if (g.to) g.to = fix(g.to);
    d.ghosts.push(g);
  }
  if (note) d.note = d.note ? `${d.note} ${note}` : note;
  d.by = d.by || "runt";
  return d;
}

// ---------------------------------------------------------------- resolving

function pigPos(farm, pen, ref, pigs) {
  const addr = ref.slice(4);
  const inPen = (pigs ?? []).filter((p) => p.space === pen.space);
  const t = pigTiles(pen, inPen).find((t) => t.pig.address === addr);
  if (t) return { x: Math.round(t.x), y: Math.round(t.y) };
  return { x: pen.x + Math.floor(pen.w / 2), y: pen.y + Math.floor(pen.h / 2) };
}

// Applies a draft to the farm with farmworld.js. pigs (optional, {address, space}) lets
// "near pig:" aim at the pig's tile. Answers { farm, crates, errors }: crates are
// { op: "crate.add", fields: { crate } } for store.command. The farm holds whatever
// applied; callers build only when errors is empty.
export function resolveGhosts(farm, draft, pigSpace, pigs = []) {
  let f = { entities: {}, belts: [], ...farm };
  const crates = [], errors = [], placed = {};
  const refEnd = (r) => {
    if (typeof r !== "string") return null;
    if (r.startsWith("ghost:")) return placed[r.slice(6)] ? `ent:${placed[r.slice(6)]}` : null;
    return r;
  };
  for (const g of draft?.ghosts ?? []) {
    const tag = g.id ?? g.op;
    if (g.op === "place") {
      if (!KINDS[g.kind] || !KINDS[g.kind].station) { errors.push(`${tag}: cannot build a ${g.kind}`); continue; }
      let pen = g.pen ? penEntity(f, g.pen) : null;
      if (g.pen && !pen) { errors.push(`${tag}: no pen for that space`); continue; }
      let spot = null;
      if (Number.isFinite(g.x) && Number.isFinite(g.y)) spot = { x: g.x, y: g.y };
      else {
        let at = null;
        const near = refEnd(g.near);
        if (g.near && !near) { errors.push(`${tag}: ${g.near} was not placed`); continue; }
        if (near?.startsWith("ent:")) {
          const e = f.entities[near.slice(4)];
          if (!e) { errors.push(`${tag}: nothing at ${g.near}`); continue; }
          pen ??= e.pen ? penEntity(f, e.pen) : null;
          const r = rect(e);
          at = { x: r.x + r.w, y: r.y };
        } else if (near?.startsWith("pig:")) {
          pen ??= penEntity(f, pigSpace(near.slice(4)));
          if (pen) at = pigPos(f, pen, near, pigs);
        }
        if (!pen) { errors.push(`${tag}: say which pen (or x and y)`); continue; }
        at ??= { x: pen.x + Math.floor(pen.w / 2), y: pen.y + Math.floor(pen.h / 2) };
        spot = freeSpot(f, g.kind, at.x, at.y, pen);
        if (!spot) { errors.push(`${tag}: no room for a ${KINDS[g.kind].label} in that pen`); continue; }
      }
      const r = placeEntity(f, { kind: g.kind, ...spot, ...(g.pen ? { pen: g.pen } : {}) });
      if (r.error) { errors.push(`${tag}: ${r.error}`); continue; }
      f = r.farm;
      if (g.id) placed[g.id] = r.id;
    } else if (g.op === "belt") {
      const from = refEnd(g.from), to = refEnd(g.to);
      if (!from || !to) { errors.push(`${tag}: a belt end names a ghost that was not placed`); continue; }
      const r = addBelt(f, from, to, pigSpace);
      if (r.error) errors.push(`${tag}: ${r.error}`);
      else f = r.farm;
    } else if (g.op === "crate") {
      const c = g.crate ?? {};
      if (!c.title) { errors.push(`${tag}: a crate needs a title`); continue; }
      if (!c.doneWhen) { errors.push(`${tag}: crate "${c.title}" has no done-when`); continue; }
      crates.push({ op: "crate.add", fields: { crate: { text: "", ...c } } });
    } else errors.push(`${tag}: unknown draft step ${g.op}`);
  }
  return { farm: f, crates, errors, placed };
}

// The check before Build: the draft applies cleanly (no overlaps, valid belts),
// and every line it touches ends at a Review Station or the Barn.
export function checkDraft(farm, draft, pigSpace, pigs = []) {
  const problems = [];
  if (!draft?.ghosts?.length) return { ok: false, problems: ["the draft is empty"], result: null };
  const result = resolveGhosts(farm, draft, pigSpace, pigs);
  problems.push(...result.errors);
  const old = new Set((farm.belts ?? []).map((b) => b.id));
  const added = (result.farm.belts ?? []).filter((b) => !old.has(b.id));
  const ends = new Set(added.flatMap((b) => [b.from, b.to]));
  const kinds = result.farm.entities ?? {};
  const label = (s) => (s.kind === "pig" ? `pig ${s.pig}` : KINDS[s.kind]?.label ?? s.kind);
  for (const line of deriveLines(result.farm, pigSpace)) {
    const refs = line.stops.map((s) => (s.kind === "pig" ? `pig:${s.pig}` : `ent:${s.entity}`));
    if (!refs.some((r) => ends.has(r))) continue;
    const last = line.stops[line.stops.length - 1];
    if (!END_KINDS.has(last.kind) || (last.entity && !kinds[last.entity])) problems.push(`this belt dead-ends: it stops at ${label(last)}; route it to a Review Station or the Barn`);
  }
  return { ok: problems.length === 0, problems, result };
}

// ---------------------------------------------------------------- hints

const lastRef = (stop) => (stop.kind === "pig" ? `pig:${stop.pig}` : `ent:${stop.entity}`);

// A ghost spot (x, y) for a building next to a ref, inside the pen.
function spotNear(farm, kind, ref, penSpace, pigs) {
  const pen = penEntity(farm, penSpace);
  if (!pen) return null;
  let at;
  if (ref.startsWith("ent:") && farm.entities?.[ref.slice(4)]) {
    const r = rect(farm.entities[ref.slice(4)]);
    at = { x: r.x + r.w, y: r.y };
  } else at = pigPos(farm, pen, ref, pigs);
  return freeSpot(farm, kind, at.x, at.y, pen);
}

// Ordered hints: { id, text, why, action? }. action.type is one of
// view (view), adopt (pen), ghost (ops, note), penmenu (pen), talk (pig),
// putcrate (crate, line), crate (crate), review (crate); label is the button text.
export function runtHints(farm, pigs, pens, crates, ghosts) {
  const hints = [];
  const entities = Object.values(farm.entities ?? {});
  const belts = farm.belts ?? [];
  const lines = farm.lines ?? [];
  const name = (a) => pigs.find((p) => p.address === a)?.name ?? a;
  const penName = (space) => pens.find((p) => p.space === space)?.name ?? space;
  const drafted = Boolean(ghosts?.ghosts?.length);

  // Things that need a person now.
  for (const p of pigs) if (p.status === "blocked") hints.push({ id: `blocked:${p.address}`, text: `${p.name ?? p.address} is blocked.`, why: "A pig stops when it needs an answer from you; its crate waits until you reply.", action: { type: "talk", pig: p.address, label: "TALK TO IT" } });
  for (const c of crates) if (c.state === "burnt") hints.push({ id: `burnt:${c.id}`, text: `"${c.title}" burnt in the Oven.`, why: "The tests failed twice. The log says why.", action: { type: "crate", crate: c.id, label: "READ THE LOG" } });
  for (const c of crates) if (c.state === "holding") hints.push({ id: `holding:${c.id}`, text: `"${c.title}" is waiting at Review.`, why: "A crate stays at a Review Station until you approve it or send it back.", action: { type: "review", crate: c.id, label: "REVIEW IT" } });

  // Setup, in the order a farm grows.
  const penEnts = entities.filter((e) => e.kind === "pen");
  if (!pens.length && !penEnts.length) {
    hints.push({ id: "no-pen", text: "Place a pen: open a Herdr space for your project.", why: "A pen is one project folder; every Herdr space becomes one.", action: { type: "view", view: "map", label: "OPEN THE MAP" } });
    return hints;
  }
  const emptyPen = pens.find((p) => !p.pigs.length);
  if (emptyPen && !pigs.length) hints.push({ id: "no-pigs", text: `${penName(emptyPen.space)} has no pigs. Adopt one.`, why: "A pig is a coding agent. Without one nothing gets done.", action: { type: "adopt", pen: emptyPen.space, label: "ADOPT A PIG" } });
  else if (!pigs.length) hints.push({ id: "no-pigs", text: "Adopt a pig.", why: "A pig is a coding agent.", action: { type: "adopt", pen: pens[0]?.space, label: "ADOPT A PIG" } });

  if (pigs.length && !belts.length) {
    const pen = pens.find((p) => p.pigs.length >= 2);
    if (pen && !drafted) {
      const [a, b] = pen.pigs;
      hints.push({ id: "no-belt", text: `Drag a belt between ${name(a.address)} and ${name(b.address)}.`, why: "A belt carries a crate's hand-off from one pig to the next.",
        action: { type: "ghost", label: "DRAFT IT", note: "A belt so one pig hands work to the next.", ops: [{ op: "belt", from: `pig:${a.address}`, to: `pig:${b.address}` }] } });
    } else if (!pen) hints.push({ id: "no-belt", text: "Adopt a second pig in the same pen, then drag a belt between them.", why: "A belt joins two pigs; one pig alone has nothing to hand to.", action: { type: "adopt", pen: pens.find((p) => p.pigs.length)?.space, label: "ADOPT A PIG" } });
  }

  if (belts.length && !drafted) {
    for (const line of lines) {
      const last = line.stops[line.stops.length - 1];
      const inPen = entities.filter((e) => e.pen === line.pen);
      const hasOven = inPen.some((e) => e.kind === "oven");
      const ref = lastRef(last);
      if (!hasOven && last.kind === "pig") {
        const op = spotNear(farm, "oven", ref, line.pen, pigs);
        let review = null;
        if (op) {
          const placed = placeEntity(farm, { kind: "oven", ...op });
          if (placed.farm) review = freeSpot(placed.farm, "review", op.x + 2, op.y, penEntity(farm, line.pen));
        }
        const ops = [{ id: "o", op: "place", kind: "oven", pen: line.pen, ...(op ?? { near: ref }) }, { op: "belt", from: ref, to: "ghost:o" }];
        if (review) ops.push({ id: "r", op: "place", kind: "review", pen: line.pen, ...review }, { op: "belt", from: "ghost:o", to: "ghost:r" });
        hints.push({ id: `no-oven:${line.pen}`, text: `${penName(line.pen)} has a belt but no Oven.`, why: "The Oven runs your tests before a crate moves on; a Review Station after it lets you check the result.",
          action: { type: "ghost", label: "DRAFT AN OVEN", note: "An Oven after the last pig, then a Review Station.", ops } });
      } else if (hasOven && !END_KINDS.has(last.kind)) {
        const op = spotNear(farm, "review", ref, line.pen, pigs);
        hints.push({ id: `dead-end:${line.id}`, text: `This belt dead-ends in ${penName(line.pen)}: ${last.kind === "pig" ? name(last.pig) : "the Oven"} sends crates nowhere.`, why: "Every line should end at a Review Station or the Barn so finished work lands somewhere a person sees it.",
          action: { type: "ghost", label: "DRAFT A REVIEW", note: "A Review Station at the end of the belt.", ops: [{ id: "r", op: "place", kind: "review", pen: line.pen, ...(op ?? { near: ref }) }, { op: "belt", from: ref, to: "ghost:r" }] } });
      }
    }
  }
  if (drafted) hints.push({ id: "draft", text: "A draft is waiting below.", why: "Ghosts change nothing until you press BUILD." });

  for (const e of entities) if (e.kind === "oven" && e.pen && !farm.pens?.[e.pen]?.test?.trim()) {
    hints.push({ id: `no-test:${e.pen}`, text: `The Oven in ${penName(e.pen)} has no test command.`, why: "Without one every crate burns at the Oven. Set it in the pen menu.", action: { type: "penmenu", pen: e.pen, label: "SET IT" } });
  }

  // Work.
  const board = crates.filter((c) => c.state === "board");
  if (board.length && lines.length) {
    const c = board.find((c) => lines.some((l) => l.pen === c.pen)) ?? board[0];
    const line = lines.find((l) => l.pen === c.pen) ?? lines[0];
    hints.push({ id: `put:${c.id}`, text: `Put "${c.title}" on the belt.`, why: "A crate on the Job Board does nothing until a pig takes it or it rides a belt.", action: { type: "putcrate", crate: c.id, line: line.id, label: "PUT IT ON" } });
  }
  return hints;
}
