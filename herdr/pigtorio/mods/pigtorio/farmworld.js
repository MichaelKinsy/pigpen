// The farm as a Factorio-style world: entities on a tile grid, belts between
// pigs and stations. Pure: no herdr imports, so bun can test it.
//
// farm.json (mod-owned) holds:
//   entities: { <id>: { id, kind, x, y, w, h, space?, pen?, ghost? } }
//   belts:    [ { id, from, to } ]   endpoints are "pig:<address>" or "ent:<entity id>"
//   lines:    derived from belts for the engine (see ARCHITECTURE.md); never edited by hand.
// A pen entity carries its Herdr space id in `space`. A station's `pen` is the
// space of the pen it stands in.

export const TILE = 32; // logical px per tile at zoom 1

export const KINDS = {
  pen: { w: 16, h: 9, label: "Pen" },
  oven: { w: 2, h: 2, label: "Oven", station: true },
  review: { w: 2, h: 2, label: "Review Station", station: true },
  barn: { w: 3, h: 3, label: "Barn", station: true },
  jobboard: { w: 4, h: 3, label: "Job Board" },
  runt: { w: 1, h: 1, label: "The Runt" },
};
export const BELT_KINDS = new Set(["oven", "review", "barn"]);

export const rect = (e) => ({ x: e.x, y: e.y, w: e.w ?? KINDS[e.kind].w, h: e.h ?? KINDS[e.kind].h });
export const overlaps = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
export const inside = (a, b) => a.x >= b.x && a.y >= b.y && a.x + a.w <= b.x + b.w && a.y + a.h <= b.y + b.h;
export const contains = (r, x, y) => x >= r.x && y >= r.y && x < r.x + r.w && y < r.y + r.h;

const entities = (farm) => Object.values(farm.entities ?? {});
const pens = (farm) => entities(farm).filter((e) => e.kind === "pen");

export function penEntity(farm, space) {
  return pens(farm).find((e) => e.space === space) ?? null;
}

export function penAt(farm, x, y) {
  return pens(farm).find((e) => contains(rect(e), x, y)) ?? null;
}

// What stands on a tile, topmost first: a station or sign before the pen under it.
export function entityAt(farm, x, y) {
  const hits = entities(farm).filter((e) => !e.ghost && contains(rect(e), x, y));
  return hits.find((e) => e.kind !== "pen") ?? hits[0] ?? null;
}

// Why an entity cannot stand there, or null. Pens may not touch other pens;
// stations stand on open ground or inside a pen, never on another building.
export function placeProblem(farm, ent) {
  const r = rect(ent);
  if (r.x < 0 || r.y < 0) return "off the edge of the farm";
  for (const o of entities(farm)) {
    if (o.id === ent.id || o.ghost) continue;
    const ro = rect(o);
    if (ent.kind === "pen") {
      if (o.kind === "pen" && overlaps({ x: r.x - 1, y: r.y - 1, w: r.w + 2, h: r.h + 2 }, ro)) return "too close to another pen";
      if (o.kind !== "pen" && overlaps(r, ro) && !inside(ro, r)) return `on top of the ${KINDS[o.kind].label}`;
    } else if (o.kind === "pen") {
      if (overlaps(r, ro) && !inside(r, ro)) return "half in a pen: inside or outside, not across the fence";
    } else if (overlaps(r, ro)) return `on top of the ${KINDS[o.kind].label}`;
  }
  return null;
}

let seq = 0;
export const newId = (prefix) => `${prefix}${Date.now().toString(36)}${(seq++ % 1296).toString(36).padStart(2, "0")}`;

// Places an entity (a copy of `ent` with kind, x, y). Stations inside a pen
// belong to it. Answers { farm, id } or { error }.
export function placeEntity(farm, ent) {
  const k = KINDS[ent.kind];
  if (!k) return { error: `unknown building ${ent.kind}` };
  const e = { w: k.w, h: k.h, ...ent, id: ent.id ?? newId(ent.kind[0]) };
  const problem = placeProblem(farm, e);
  if (problem) return { error: problem };
  if (k.station) {
    const pen = pens(farm).find((p) => inside(rect(e), rect(p)));
    e.pen = pen?.space ?? e.pen ?? null;
  }
  const next = { ...farm, entities: { ...(farm.entities ?? {}), [e.id]: e } };
  return { farm: withLines(next), id: e.id };
}

export function moveEntity(farm, id, x, y) {
  const e = farm.entities?.[id];
  if (!e) return { error: "nothing there" };
  const moved = { ...e, x, y };
  const problem = placeProblem(farm, moved);
  if (problem) return { error: problem };
  if (KINDS[e.kind].station) moved.pen = pens(farm).find((p) => inside(rect(moved), rect(p)))?.space ?? null;
  return { farm: withLines({ ...farm, entities: { ...farm.entities, [id]: moved } }) };
}

// Removes an entity and every belt that touches it.
export function removeEntity(farm, id) {
  const rest = { ...(farm.entities ?? {}) };
  delete rest[id];
  const belts = (farm.belts ?? []).filter((b) => b.from !== `ent:${id}` && b.to !== `ent:${id}`);
  return withLines({ ...farm, entities: rest, belts });
}

// Which pen (space id) a belt endpoint belongs to. pigSpace maps a pig address to its space.
export function refPen(farm, ref, pigSpace) {
  if (ref.startsWith("pig:")) return pigSpace(ref.slice(4)) ?? null;
  const e = farm.entities?.[ref.slice(4)];
  return e?.pen ?? null;
}

// Adds a belt from one pig or station to the next. A belt carries crates one
// way along one line, so each end has at most one belt out and one belt in,
// both ends sit in the same pen, and the line never loops.
export function addBelt(farm, from, to, pigSpace) {
  if (from === to) return { error: "a belt needs two ends" };
  for (const r of [from, to]) {
    if (r.startsWith("ent:")) {
      const e = farm.entities?.[r.slice(4)];
      if (!e || !BELT_KINDS.has(e.kind)) return { error: "belts join pigs, Ovens, Review Stations and Barns" };
    } else if (!r.startsWith("pig:")) return { error: `bad belt end ${r}` };
  }
  const pa = refPen(farm, from, pigSpace), pb = refPen(farm, to, pigSpace);
  if (!pa || !pb) return { error: "both ends must stand in a pen" };
  if (pa !== pb) return { error: "a belt stays inside one pen" };
  const belts = farm.belts ?? [];
  if (belts.some((b) => b.from === from)) return { error: "that end already has a belt going out (Splitters come later)" };
  if (belts.some((b) => b.to === to)) return { error: "that end already has a belt coming in" };
  if (from.startsWith("ent:") && farm.entities[from.slice(4)].kind === "barn") return { error: "the Barn is the end of the line" };
  for (let at = to, n = 0; n <= belts.length; n++) {
    if (at === from) return { error: "that belt would loop" };
    const nxt = belts.find((b) => b.from === at);
    if (!nxt) break;
    at = nxt.to;
  }
  const next = { ...farm, belts: [...belts, { id: newId("b"), from, to }] };
  return { farm: withLines(next, pigSpace) };
}

export function removeBelt(farm, id) {
  return withLines({ ...farm, belts: (farm.belts ?? []).filter((b) => b.id !== id) });
}

// The engine's lines, read off the belts: every chain from a start (an end
// with a belt out and none in) to its last stop. A line's id comes from its
// first stop, so it survives edits further down.
export function deriveLines(farm, pigSpace = () => null) {
  const belts = farm.belts ?? [];
  const lines = [];
  for (const start of belts.map((b) => b.from).filter((f) => !belts.some((b) => b.to === f))) {
    const stops = [];
    const seen = new Set();
    for (let at = start; at && !seen.has(at); at = belts.find((b) => b.from === at)?.to) {
      seen.add(at);
      if (at.startsWith("pig:")) stops.push({ kind: "pig", pig: at.slice(4) });
      else {
        const e = farm.entities?.[at.slice(4)];
        if (e) stops.push({ kind: e.kind, entity: e.id });
      }
    }
    const pen = refPen(farm, start, pigSpace) ?? farm.lines?.find((l) => l.id === `l-${start}`)?.pen ?? null;
    if (stops.length > 1) lines.push({ id: `l-${start}`, pen, stops });
  }
  return lines;
}

export function withLines(farm, pigSpace) {
  const known = new Map((farm.lines ?? []).flatMap((l) => l.stops.filter((s) => s.kind === "pig").map((s) => [s.pig, l.pen])));
  const space = (a) => pigSpace?.(a) ?? known.get(a) ?? null;
  return { ...farm, lines: deriveLines(farm, space) };
}

// The nearest free spot for a building around (x, y), searching outwards.
export function freeSpot(farm, kind, x, y, within = null) {
  const k = KINDS[kind];
  for (let d = 0; d < 60; d++) {
    for (let dy = -d; dy <= d; dy++) {
      for (let dx = -d; dx <= d; dx++) {
        if (Math.max(Math.abs(dx), Math.abs(dy)) !== d) continue;
        const e = { kind, x: x + dx, y: y + dy, w: k.w, h: k.h };
        if (within && !inside(rect(e), rect(within))) continue;
        if (!placeProblem(farm, e)) return { x: e.x, y: e.y };
      }
    }
  }
  return null;
}

// Every Herdr space gets a pen the first time it is seen (in a row below the
// others), and the farm gets its Job Board, Barn and Runt once.
export function settle(farm, spaces) {
  let f = { entities: {}, belts: [], ...farm };
  const fixed = [["jobboard", 2, 1], ["barn", 24, 1], ["runt", 7, 2]];
  for (const [kind, x, y] of fixed) {
    if (!entities(f).some((e) => e.kind === kind)) {
      const spot = freeSpot(f, kind, x, y);
      if (spot) f = placeEntity(f, { kind, ...spot, id: kind }).farm ?? f;
    }
  }
  for (const s of spaces) {
    if (penEntity(f, s.id)) continue;
    const bottom = Math.max(5, ...pens(f).map((p) => p.y + p.h + 2));
    const spot = freeSpot(f, "pen", 2, bottom) ?? freeSpot(f, "pen", 2, 6);
    if (spot) f = placeEntity(f, { kind: "pen", ...spot, space: s.id }).farm ?? f;
  }
  return f;
}

// Pens whose space is gone are pulled up, with their stations and belts.
export function prune(farm, spaceIds) {
  let f = farm;
  for (const p of pens(f)) if (!spaceIds.has(p.space)) {
    for (const e of entities(f).filter((e) => e.pen === p.space)) f = removeEntity(f, e.id);
    f = removeEntity(f, p.id);
  }
  return f;
}

// An old farm (lines of stops, no belts) becomes stations and belts in its pens.
export function migrateLines(farm, pigSpace) {
  if ((farm.belts ?? []).length || !(farm.lines ?? []).length) return farm;
  let f = { ...farm, belts: [] };
  for (const line of farm.lines) {
    const pen = penEntity(f, line.pen);
    if (!pen) continue;
    const refs = [];
    for (const s of line.stops) {
      if (s.kind === "pig") refs.push(`pig:${s.pig}`);
      else {
        const spot = freeSpot(f, s.kind, pen.x + pen.w - 4, pen.y + pen.h - 3, pen);
        if (!spot) continue;
        const r = placeEntity(f, { kind: s.kind, ...spot });
        if (r.farm) { f = r.farm; refs.push(`ent:${r.id}`); }
      }
    }
    for (let i = 0; i + 1 < refs.length; i++) {
      const r = addBelt(f, refs[i], refs[i + 1], (a) => pigSpace(a) ?? line.pen);
      if (r.farm) f = r.farm;
    }
  }
  return f;
}

// Where each pig stands in its pen: a grid along the top of the pen, in tiles
// (fractional), so belts can be drawn to them.
export function pigTiles(pen, pigs) {
  const cols = Math.max(1, Math.floor((pen.w - 1) / 4.6));
  return pigs.map((p, i) => ({ pig: p, x: pen.x + 1.2 + (i % cols) * 4.6 + 0.4, y: pen.y + 1.4 + Math.floor(i / cols) * 3.4 }));
}
