// The farm as a world you build on, Factorio style: Map (zoomed out, pan by
// dragging, zoom with the wheel) and Walk (zoomed in, the camera follows the
// Swineherd). Both take the same tools from the hotbar:
//   hand     click a pig or building to use it, drag a building to move it
//   pen      a new pen (a new Herdr space in a folder you pick)
//   pig      adopt a pig into the pen you click
//   belt     click one end, then the next: pig → pig → Oven → Review → Barn
//   oven / review / barn / jobboard   place the building
//   remove   click a building or belt to take it away (pens are spaces: close those in Terminals)
//   runt     talk to the Runt
// Right-click: a pig or pen opens its menu, a station or belt is removed, open
// ground opens "build here", and with a tool in hand it puts the tool away.
import { div, image } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { pickFolder, run, setShared, openOverlay } from "herdr";
import { TILE, KINDS, rect, placeEntity, moveEntity, removeEntity, removeBelt, addBelt, entityAt, placeProblem, pigTiles, penAt } from "./farmworld.js";
import { spriteState, statusWord, roleOf, fmtAge, fmtTokens, BELL_MARK, lineBusy, boardCrates } from "./model.js";
import { C, PIXEL, ART, sprite, tag, kbd, sectionTitle } from "./ui.js";
import { calm, setView } from "./nav.js";
import { openPigMenu, openPenMenu, openReview, openCrate, ringBell } from "./dialogs.js";

const stop = (_e, cx) => cx.stop_propagation();

export const TOOLS = [
  { id: "hand", label: "Hand: use and move", icon: "hand" },
  { id: "pen", label: "Pen: a new space", icon: "pen" },
  { id: "pig", label: "Pig: adopt into a pen", icon: "pig" },
  { id: "belt", label: "Belt: join two ends", icon: "belt" },
  { id: "oven", label: "Oven: runs the pen's tests", icon: "oven" },
  { id: "review", label: "Review Station: you approve", icon: "review" },
  { id: "barn", label: "Barn: commit, push, PR", icon: "barn" },
  { id: "jobboard", label: "Job Board", icon: "jobboard" },
  { id: "remove", label: "Remove a building or belt", icon: "remove" },
  { id: "runt", label: "Talk to the Runt", icon: "runt" },
];
const BUILDABLE = new Set(["pen", "oven", "review", "barn", "jobboard"]);

// The pigs' spaces, for belt checks (a pig's pen is its space's pen).
export const pigSpaceOf = (f) => { const m = new Map(f.pigs.map((p) => [p.address, p.space])); return (a) => m.get(a) ?? null; };

function world(app) {
  return app.ui.world ??= { tool: "hand", zoomMap: 0.85, cam: { x: -20, y: -10 }, hover: null, beltFrom: null, msg: null, msgAt: 0, drag: null };
}

function say(app, msg) {
  const w = world(app);
  w.msg = msg;
  w.msgAt = Date.now();
}

// ---------------------------------------------------------------- geometry

function frameOf(app, walk) {
  const w = world(app);
  const z = walk ? 1.6 : w.zoomMap;
  const t = TILE * z;
  let cam = w.cam;
  if (walk) {
    const sw = swine(app);
    cam = { x: sw.x * t - app.size.width / 2, y: sw.y * t - app.size.height / 2 };
  }
  return { z, t, cam, walk };
}
const toScreen = (fr, x, y) => ({ x: Math.round(x * fr.t - fr.cam.x), y: Math.round(y * fr.t - fr.cam.y) });
const toTile = (fr, px, py) => ({ x: Math.floor((px + fr.cam.x) / fr.t), y: Math.floor((py + fr.cam.y) / fr.t), fx: (px + fr.cam.x) / fr.t, fy: (py + fr.cam.y) / fr.t });

function swine(app) {
  return app.ui.swine ??= { x: 10, y: 4.5, dir: "down", moving: false, dur: 0 };
}

// Where a belt end sits, in tiles: a pig's feet, or a building's middle.
function endPoint(f, ref, spots) {
  if (ref.startsWith("pig:")) {
    const s = spots.get(ref.slice(4));
    return s ? { x: s.x + 1.5, y: s.y + 2.1 } : null;
  }
  const e = f.farm.entities?.[ref.slice(4)];
  return e ? { x: e.x + rect(e).w / 2, y: e.y + rect(e).h / 2 } : null;
}

// Every pig's spot (tiles), by address.
function pigSpots(f) {
  const spots = new Map();
  for (const pen of Object.values(f.farm.entities ?? {}).filter((e) => e.kind === "pen")) {
    for (const s of pigTiles(pen, f.pigs.filter((p) => p.space === pen.space))) spots.set(s.pig.address, s);
  }
  return spots;
}

function pigAt(f, spots, fx, fy) {
  for (const [addr, s] of spots) if (fx >= s.x && fx < s.x + 3 && fy >= s.y && fy < s.y + 2.6) return f.pigs.find((p) => p.address === addr);
  return null;
}

function beltAt(f, spots, fx, fy) {
  const near = (P, Q) => (P.y === Q.y
    ? Math.abs(fy - P.y) < 0.5 && fx >= Math.min(P.x, Q.x) - 0.3 && fx <= Math.max(P.x, Q.x) + 0.3
    : Math.abs(fx - P.x) < 0.5 && fy >= Math.min(P.y, Q.y) - 0.3 && fy <= Math.max(P.y, Q.y) + 0.3);
  for (const b of f.farm.belts ?? []) {
    const a = endPoint(f, b.from, spots), c = endPoint(f, b.to, spots);
    if (!a || !c) continue;
    const k = beltCorner(a, c);
    if (near(a, k) || near(k, c)) return b;
  }
  return null;
}

// ---------------------------------------------------------------- actions

async function place(app, f, kind, tx, ty) {
  const k = KINDS[kind];
  const ent = { kind, x: tx - Math.floor(k.w / 2), y: ty - Math.floor(k.h / 2) };
  if (kind === "pen") {
    const problem = placeProblem(f.farm, { ...ent, w: k.w, h: k.h });
    if (problem) return say(app, `Can't build here: ${problem}`);
    const folder = await pickFolder({ prompt: "Make a pen for this folder" });
    if (!folder) return;
    // The space comes back through spaces(); its pen is placed where it was asked for.
    await app.store.updateFarm((farm) => ({ ...farm, wantPen: { x: ent.x, y: ent.y, folder, at: Date.now() } }));
    run("space.new", { folder });
    return say(app, "Pen staked out: the space is opening");
  }
  if (KINDS[kind].station && lineBusyAt(app, f, ent)) return say(app, "Crates are riding this pen's belt: build once they ship");
  let error = null;
  await app.store.updateFarm((farm) => { const r = placeEntity(farm, ent); error = r.error; return r.farm ?? farm; });
  say(app, error ? `Can't build here: ${error}` : `${k.label} built`);
}

function lineBusyAt(app, f, ent) {
  const pen = Object.values(f.farm.entities ?? {}).find((p) => p.kind === "pen" && ent.x >= p.x && ent.y >= p.y && ent.x < p.x + p.w && ent.y < p.y + p.h);
  return pen ? lineBusy(f.farm, f.crates, pen.space) : false;
}

async function remove(app, f, ent, belt) {
  if (belt) {
    const pen = pigSpaceOf(f)(belt.from.slice(4)) ?? f.farm.entities?.[belt.from.slice(4)]?.pen;
    if (pen && lineBusy(f.farm, f.crates, pen)) return say(app, "Crates are riding this belt: take it up once they ship");
    await app.store.updateFarm((farm) => removeBelt(farm, belt.id));
    return say(app, "Belt taken up");
  }
  if (!ent) return say(app, "Nothing to remove there");
  if (ent.kind === "pen") return say(app, "A pen is a Herdr space: close the space in Terminals to take it away");
  if (ent.kind === "runt") return say(app, "The Runt stays: it runs the farm");
  if (ent.pen && lineBusy(f.farm, f.crates, ent.pen)) return say(app, "Crates are riding this pen's belt: take it down once they ship");
  await app.store.updateFarm((farm) => removeEntity(farm, ent.id));
  say(app, `${KINDS[ent.kind].label} taken down`);
}

async function beltClick(app, f, ref) {
  const w = world(app);
  if (!ref) { w.beltFrom = null; return say(app, "Belts join pigs, Ovens, Review Stations and Barns"); }
  if (!w.beltFrom) { w.beltFrom = ref; return say(app, "Now click where the hand-off should go"); }
  const from = w.beltFrom;
  w.beltFrom = null;
  if (from === ref) return say(app, "Belt cancelled");
  const pen = pigSpaceOf(f)(from.slice(4)) ?? f.farm.entities?.[from.slice(4)]?.pen;
  if (pen && lineBusy(f.farm, f.crates, pen)) return say(app, "Crates are riding this pen's belt: extend it once they ship");
  let error = null;
  await app.store.updateFarm((farm) => { const r = addBelt(farm, from, ref, pigSpaceOf(f)); error = r.error; return r.farm ?? farm; });
  say(app, error ? `No belt: ${error}` : "Belt laid: finished hand-offs ride it to the next stop");
}

function use(app, f, ent) {
  if (!ent) return;
  if (ent.kind === "jobboard") return setView("board");
  if (ent.kind === "barn") {
    const c = f.crates.find((c) => c.state === "barn");
    return c ? openReview(c.id) : setView("report");
  }
  if (ent.kind === "review") {
    const c = f.crates.find((c) => c.state === "holding" && f.farm.lines?.some((l) => l.id === c.line && l.pen === ent.pen));
    return c ? openReview(c.id) : say(app, "Nothing waiting at this Review Station");
  }
  if (ent.kind === "oven") {
    const c = f.crates.find((c) => c.state === "burnt" && f.farm.lines?.some((l) => l.id === c.line && l.pen === ent.pen));
    return c ? openCrate({ crate: c.id }) : openPenMenu(ent.pen, null);
  }
  if (ent.kind === "runt") return openRuntPanel();
  if (ent.kind === "pen") return openPenMenu(ent.space, null);
}

function openRuntPanel() {
  // The Runt's own panel (runt.js) when present; the Pigpedia until then.
  openOverlay({ mod: "pigtorio", kind: "window", key: "runt", width: 460, height: 560, props: { role: "runt" } });
}

export function walkTo(app, cx, tx, ty, then) {
  const sw = swine(app);
  const dx = tx - sw.x, dy = ty - sw.y;
  const dur = Math.max(120, Math.round(Math.hypot(dx, dy) * TILE * 1.6 / 0.24));
  const dir = Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? "right" : "left") : dy > 0 ? "down" : "up";
  const step = (app.ui.walkStep ?? 0) + 1;
  app.ui.walkStep = step;
  app.ui.swine = { x: tx, y: ty, dir, moving: true, dur };
  cx.timer.after(dur, (cx) => {
    if (app.ui.walkStep !== step) return;
    app.ui.swine = { ...app.ui.swine, moving: false };
    if (then) then(cx);
    cx.notify();
  });
}

// A pointer position relative to the world's top left. GPUI's local_position
// is relative to whichever element is under the pointer (a pig, a building),
// so this goes from the window position and the world's own origin, learnt
// from any event whose bounds are the world's.
function localPoint(app, e) {
  const b = e.bounds;
  if (b && Math.abs(b.width - app.size.width) < 2 && Math.abs(b.height - app.size.height) < 2) app.ui.origin = { x: b.x, y: b.y };
  const o = app.ui.origin ?? { x: 0, y: 44 };
  return { x: e.position.x - o.x, y: e.position.y - o.y };
}

// One press on the world: what is under it, and what the tool in hand does.
function press(app, f, fr, e, cx, button) {
  const w = world(app);
  const p = localPoint(app, e);
  const t = toTile(fr, p.x, p.y);
  const spots = pigSpots(f);
  const pig = pigAt(f, spots, t.fx, t.fy);
  const ent = entityAt(f.farm, t.x, t.y);
  const belt = !pig && (!ent || ent.kind === "pen") ? beltAt(f, spots, t.fx, t.fy) : null;
  app.handle.focus();
  if (button === "right") {
    if (w.tool !== "hand" || w.beltFrom) { w.tool = "hand"; w.beltFrom = null; return say(app, "Tool put away"); }
    if (pig) return openPigMenu(pig.address, e.position);
    if (ent && ent.kind === "pen") return openPenMenu(ent.space, e.position);
    if (ent && KINDS[ent.kind].station) return remove(app, f, ent, null);
    if (belt) return remove(app, f, null, belt);
    if (ent) return use(app, f, ent);
    return openOverlay({ mod: "pigtorio", kind: "popup", frame: "none", width: 220, height: 300, at: e.position, props: { role: "buildhere", x: t.x, y: t.y, walk: fr.walk } });
  }
  const tool = w.tool;
  if (tool === "runt") return openRuntPanel();
  if (tool === "remove") return remove(app, f, ent, belt);
  if (tool === "belt") {
    const ref = pig ? `pig:${pig.address}` : ent && KINDS[ent.kind].station ? `ent:${ent.id}` : null;
    return beltClick(app, f, ref);
  }
  if (tool === "pig") {
    const pen = penAt(f.farm, t.x, t.y);
    if (!pen) return say(app, "Click inside a pen to adopt a pig there");
    setShared("pigtorio.adoptPen", pen.space);
    return setView("adopt");
  }
  if (BUILDABLE.has(tool)) {
    if (fr.walk) walkTo(app, cx, t.fx, t.fy + 1, null);
    return place(app, f, tool, t.x, t.y);
  }
  // The hand: use what is there; drag a building to move it; drag ground (Map) to look around; walk (Walk).
  if (pig) {
    if (fr.walk) { const at = e.position; return walkTo(app, cx, spots.get(pig.address).x + 1.5, spots.get(pig.address).y + 3, () => openPigMenu(pig.address, at)); }
    return openPigMenu(pig.address, e.position);
  }
  if (ent && ent.kind !== "pen") {
    w.drag = { kind: "move", id: ent.id, from: { x: p.x, y: p.y }, start: { x: ent.x, y: ent.y }, moved: false, ent };
    return;
  }
  if (ent && ent.kind === "pen" && !fr.walk) {
    w.drag = { kind: "move", id: ent.id, from: { x: p.x, y: p.y }, start: { x: ent.x, y: ent.y }, moved: false, ent, pen: true };
    return;
  }
  if (fr.walk) return walkTo(app, cx, t.fx, t.fy, null);
  w.drag = { kind: "pan", from: { x: p.x, y: p.y }, cam: { ...w.cam } };
}

function moveMouse(app, f, fr, e, cx) {
  const w = world(app);
  const p = localPoint(app, e);
  const t = toTile(fr, p.x, p.y);
  if (w.drag) {
    const dx = p.x - w.drag.from.x, dy = p.y - w.drag.from.y;
    if (w.drag.kind === "pan") { w.cam = { x: w.drag.cam.x - dx, y: w.drag.cam.y - dy }; cx.notify(); return; }
    if (Math.abs(dx) + Math.abs(dy) > 6) w.drag.moved = true;
    const nx = w.drag.start.x + Math.round(dx / fr.t), ny = w.drag.start.y + Math.round(dy / fr.t);
    if (w.drag.moved && (nx !== w.drag.at?.x || ny !== w.drag.at?.y)) { w.drag.at = { x: nx, y: ny }; cx.notify(); }
    return;
  }
  const changed = !w.hover || w.hover.x !== t.x || w.hover.y !== t.y;
  w.hover = { x: t.x, y: t.y, fx: t.fx, fy: t.fy };
  if (changed && (w.tool !== "hand" || w.beltFrom)) cx.notify();
}

async function release(app, f, cx) {
  const w = world(app);
  const d = w.drag;
  w.drag = null;
  if (!d) return;
  if (d.kind === "move") {
    if (!d.moved) { use(app, f, d.ent); cx.notify(); return; }
    if (!d.at) return;
    if (d.ent.pen && lineBusy(f.farm, f.crates, d.ent.pen)) { say(app, "Crates are riding this belt: move it once they ship"); cx.notify(); return; }
    let error = null;
    await app.store.updateFarm((farm) => { const r = moveEntity(farm, d.id, d.at.x, d.at.y); error = r.error; return r.farm ?? farm; });
    if (error) say(app, `Can't move it there: ${error}`);
  }
  cx.notify();
}

// ---------------------------------------------------------------- drawing

function grid(fr, W, H) {
  const out = [];
  const ox = ((-fr.cam.x % fr.t) + fr.t) % fr.t, oy = ((-fr.cam.y % fr.t) + fr.t) % fr.t;
  for (let x = ox; x < W; x += fr.t) out.push(div().absolute().left(Math.round(x)).top(0).w(1).h(H).bg("#0000001a"));
  for (let y = oy; y < H; y += fr.t) out.push(div().absolute().left(0).top(Math.round(y)).w(W).h(1).bg("#0000001a"));
  return out;
}

function penEl(app, f, fr, pen, ghost) {
  const s = toScreen(fr, pen.x, pen.y);
  const info = f.pens.find((p) => p.space === pen.space);
  const pigs = info?.pigs ?? [];
  const blocked = pigs.some((p) => p.status === "blocked");
  const b = Math.max(3, Math.round(5 * fr.z));
  const label = [info?.name ?? "new pen", info?.branch, `${pigs.length} pig${pigs.length === 1 ? "" : "s"}`, info?.machine ? `on ${info.machine}` : null, blocked ? "fence shaking" : null].filter(Boolean).join(" · ");
  return div().absolute().left(s.x).top(s.y).w(Math.round(pen.w * fr.t)).h(Math.round(pen.h * fr.t))
    .when(Boolean(ghost), (el) => el.opacity(0.45))
    .child(div().size_full().bg(C.penFill).border(b).border_color(blocked ? C.danger : C.penWood).rounded(2)
      .when(blocked, (el) => el.loop("opacity", { from: 0.85, to: 1, period: 900 }))
      .child(div().size_full().border(Math.max(2, Math.round(2 * fr.z))).border_color(C.penDark)))
    .child(div().absolute().left(8).top(-26).child(tag(label, { color: blocked ? C.danger : C.text, size: fr.walk ? 12 : 11 })));
}

const IMG = { oven: ["ui/oven.webp", 88, 80], review: ["ui/review.png", 88, 80], barn: ["ui/barn.png", 144, 136], jobboard: ["ui/jobboard.png", 152, 96] };

function stationEl(app, f, fr, e, ghost) {
  const s = toScreen(fr, e.x, e.y);
  const W = Math.round(rect(e).w * fr.t), H = Math.round(rect(e).h * fr.t);
  if (e.kind === "runt") {
    const busy = (app.store.ghosts?.ghosts ?? []).length > 0;
    return div().absolute().left(s.x - Math.round(fr.t * 0.3)).top(s.y - Math.round(fr.t * 0.6)).w(Math.round(fr.t * 1.6)).h(Math.round(fr.t * 1.8))
      .child(image(runtArt(busy)).size_full())
      .child(div().absolute().left(-20).top(Math.round(fr.t * 1.8)).child(tag(busy ? "Runt · draft ready" : "Runt", { size: 9, px: 5, color: busy ? C.warn : C.text })));
  }
  const [src] = IMG[e.kind];
  const label = { oven: "OVEN", review: "REVIEW", barn: "BARN", jobboard: "JOB BOARD" }[e.kind];
  const color = { oven: C.ovenText, review: C.reviewText, barn: C.signText, jobboard: C.signText }[e.kind];
  const bg = { oven: C.oven, review: C.review, barn: C.barnDark, jobboard: C.sign }[e.kind];
  const extra = e.kind === "jobboard" ? ` · ${boardCrates(f.crates).length}` : e.kind === "barn" && !e.pen ? ` · ${f.crates.filter((c) => c.state === "shipped").length}` : "";
  return div().absolute().left(s.x).top(s.y).w(W).h(H)
    .when(Boolean(ghost), (el) => el.opacity(0.45))
    .child(image(`${ART}/${src}`).size_full())
    .child(div().absolute().left(-30).top(H + 2).w(W + 60).child(h_flex().justify_center().child(tag(`${label}${extra}`, { size: 9, px: 4, color, bg }))));
}

function runtArt(talking) {
  return `${ART}/runt/${talking ? "talk" : "idle"}.webp`;
}

function pigEl(app, f, fr, p, spot) {
  const s = toScreen(fr, spot.x, spot.y);
  const w = Math.round(3 * fr.t), h = Math.round(2.25 * fr.t);
  const word = statusWord(p.status, p.crate);
  // The Map shows name and state; Walk, closer up, adds the role.
  const label = [p.name, fr.walk ? roleOf(p.role)?.label : null, word === "blocked" && p.since ? `blocked ${fmtAge(Date.now() - p.since)}` : word].filter(Boolean).join(" · ");
  return div().absolute().left(s.x).top(s.y).w(w).h(h + 18)
    .child(image(sprite(p.skin, spriteState(p.status, p.crate))).w(w).h(h))
    .child(div().absolute().left(-30).top(h - 4).w(w + 60).child(h_flex().justify_center()
      .child(div().px(5).py(1).rounded(3).bg(word === "blocked" || word === "burnt" ? C.danger : C.pigTag).text_size(fr.walk ? 12 : 10).text_color(C.text).whitespace_nowrap().child(label))))
    .when(Boolean(p.crate), (el) => el.child(image(`${ART}/ui/${p.crate.state === "burnt" ? "crate-burnt" : "crate"}.png`).absolute().left(w - 10).top(h - 22).w(Math.round(fr.t * 0.5)).h(Math.round(fr.t * 0.5))));
}

// A belt is an L through one corner: down (or up) first when the next stop is
// more than a tile lower or higher, so it leaves a pig's feet instead of
// running back along the row of pigs; across first otherwise.
export const beltCorner = (a, c) => (Math.abs(c.y - a.y) > 1 ? { x: a.x, y: c.y } : { x: c.x, y: a.y });

function beltEls(fr, a, c, still, ghost) {
  const out = [];
  const th = Math.max(5, Math.round(fr.t * 0.28));
  const k = beltCorner(a, c);
  const A = toScreen(fr, a.x, a.y), K = toScreen(fr, k.x, k.y), D = toScreen(fr, c.x, c.y);
  const seg = (x, y, w, h, img) => {
    const el = still || ghost ? div().absolute().left(x).top(y).w(w).h(h).bg(ghost ? "#ffffff66" : "#4a4a4a").border(1).border_color("#222222")
      : image(`${ART}/tiles/${img}`).absolute().left(x).top(y).w(w).h(h);
    out.push(el);
  };
  const run = (P, Q) => {
    if (P.y === Q.y) { const x0 = Math.min(P.x, Q.x), x1 = Math.max(P.x, Q.x); for (let x = x0; x < x1; x += fr.t) seg(x, P.y - th / 2, Math.min(fr.t, x1 - x) + 1, th, "belt-h.webp"); }
    else { const y0 = Math.min(P.y, Q.y), y1 = Math.max(P.y, Q.y); for (let y = y0; y < y1; y += fr.t) seg(P.x - th / 2, y, th, Math.min(fr.t, y1 - y) + 1, "belt-v.webp"); }
  };
  run(A, K);
  run(K, D);
  // The arrow sits on the longer leg, pointing the way crates ride.
  const [P, Q] = Math.hypot(K.x - A.x, K.y - A.y) >= Math.hypot(D.x - K.x, D.y - K.y) ? [A, K] : [K, D];
  const dir = P.y === Q.y ? (Q.x >= P.x ? "right" : "left") : Q.y > P.y ? "down" : "up";
  out.push(image(`${ART}/ui/belt-arrow${dir === "right" ? "" : `-${dir}`}.png`).absolute().left(Math.round((P.x + Q.x) / 2 - 6)).top(Math.round((P.y + Q.y) / 2 - 6)).w(12).h(12));
  return out;
}

function hotbar(app) {
  const w = world(app);
  return h_flex().id("hotbar").absolute().bottom(14).left(0).right(0).justify_center()
    .child(h_flex().gap(4).p(6).rounded(6).bg("#0f1512e6").border_1().border_color(C.border2)
      .on_mouse_down("left", stop).on_mouse_down("right", stop)
      .children(TOOLS.map((t, i) => {
        const on = w.tool === t.id;
        return div().id(`tool-${t.id}`).w(48).h(48).cursor_pointer()
          .on_mouse_down("left", (_e, cx) => { cx.stop_propagation(); w.tool = t.id; w.beltFrom = null; say(app, t.label); cx.notify(); })
          .child(image(`${ART}/hotbar/${on ? "slot-on" : "slot"}.png`).absolute().left(0).top(0).w(48).h(48))
          .child(image(`${ART}/hotbar/${t.icon}.png`).absolute().left(8).top(8).w(32).h(32))
          .child(div().absolute().left(3).top(1).font_family(PIXEL).text_size(9).text_color(on ? C.accent : C.muted).child(String((i + 1) % 10)));
      })));
}

function statusLine(app) {
  const w = world(app);
  const tool = TOOLS.find((t) => t.id === w.tool);
  const fresh = w.msg && Date.now() - w.msgAt < 6000;
  const text = w.beltFrom ? "Belt: now click the next stop (right-click to cancel)" : fresh ? w.msg : tool.id === "hand" ? "Hand: click to use · drag to move · right-click for more" : `${tool.label} · right-click to put it away`;
  return h_flex().absolute().bottom(80).left(0).right(0).justify_center()
    .child(div().px(10).py(4).rounded(4).bg("#0f1512cc").text_size(12).text_color(fresh && /can't|no belt|nothing|crates are/i.test(w.msg) ? C.warn : C.soft).child(text));
}

function cursorEls(app, f, fr, spots) {
  const w = world(app);
  const h = w.hover;
  if (!h) return [];
  const out = [];
  if (BUILDABLE.has(w.tool)) {
    const k = KINDS[w.tool];
    const ent = { kind: w.tool, x: h.x - Math.floor(k.w / 2), y: h.y - Math.floor(k.h / 2), w: k.w, h: k.h };
    const problem = placeProblem(f.farm, ent);
    out.push(w.tool === "pen" ? penEl(app, f, fr, { ...ent, space: null }, true) : stationEl(app, f, fr, ent, true));
    const s = toScreen(fr, ent.x, ent.y);
    out.push(div().absolute().left(s.x - 3).top(s.y - 3).w(Math.round(k.w * fr.t) + 6).h(Math.round(k.h * fr.t) + 6).border(2).border_color(problem ? C.danger : C.accent).rounded(3));
    if (problem) out.push(div().absolute().left(s.x).top(s.y - 22).child(tag(problem, { size: 10, color: C.danger, pixel: false })));
  }
  if (w.tool === "belt" && w.beltFrom) {
    const a = endPoint(f, w.beltFrom, spots);
    if (a) out.push(...beltEls(fr, a, { x: h.fx, y: h.fy }, true, true));
  }
  return out;
}

function drawWorld(app, f, fr) {
  const W = Math.round(app.size.width), H = Math.round(app.size.height);
  const w = world(app);
  const still = calm();
  const els = [...grid(fr, W, H)];
  const ents = Object.values(f.farm.entities ?? {});
  const spots = pigSpots(f);
  const moving = w.drag?.kind === "move" && w.drag.at ? { ...w.drag.ent, ...w.drag.at } : null;
  for (const pen of ents.filter((e) => e.kind === "pen")) els.push(penEl(app, f, fr, moving?.id === pen.id ? moving : pen, moving?.id === pen.id));
  for (const b of f.farm.belts ?? []) {
    const a = endPoint(f, b.from, spots), c = endPoint(f, b.to, spots);
    if (a && c) els.push(...beltEls(fr, a, c, still, false));
  }
  for (const e of ents.filter((e) => e.kind !== "pen")) els.push(stationEl(app, f, fr, moving?.id === e.id ? moving : e, moving?.id === e.id));
  for (const g of (app.store.ghosts?.ghosts ?? []).filter((g) => g.op === "place" && g.x != null && KINDS[g.kind])) {
    const ge = { ...g, w: KINDS[g.kind].w, h: KINDS[g.kind].h };
    els.push(g.kind === "pen" ? penEl(app, f, fr, { ...ge, space: null }, true) : stationEl(app, f, fr, ge, true));
  }
  for (const [addr, s] of spots) els.push(pigEl(app, f, fr, f.pigs.find((p) => p.address === addr), s));
  // Crates riding belts sit at their stop.
  for (const c of f.crates.filter((c) => c.line && c.stop != null && !["shipped", "board", "failed"].includes(c.state))) {
    const line = (f.farm.lines ?? []).find((l) => l.id === c.line);
    const st = line?.stops[Math.min(c.stop, line.stops.length - 1)];
    const ref = st ? (st.kind === "pig" ? `pig:${st.pig}` : `ent:${st.entity}`) : null;
    const pt = ref ? endPoint(f, ref, spots) : null;
    if (!pt) continue;
    const s = toScreen(fr, pt.x, pt.y);
    const size = Math.round(fr.t * 0.6);
    els.push(div().id(`crate-${c.id}`).absolute().left(s.x - size / 2).top(s.y - size / 2).w(size).h(size)
      .transition("left", { duration: 900, easing: "ease-in-out" }).transition("top", { duration: 900, easing: "ease-in-out" })
      .child(image(`${ART}/ui/${c.state === "burnt" ? "crate-burnt" : "crate"}.png`).size_full()));
  }
  // Pigs with no pen yet (strays) stand by the road.
  const strays = f.pigs.filter((p) => !spots.has(p.address));
  strays.forEach((p, i) => els.push(pigEl(app, f, fr, p, { x: 2 + i * 4, y: -3.5 })));
  // The Swineherd.
  const sw = swine(app);
  const ss = toScreen(fr, sw.x, sw.y);
  const sh = Math.round(fr.t * 1.9), swd = Math.round(sh * 72 / 88);
  els.push(div().id("swineherd").absolute().left(ss.x - swd / 2).top(ss.y - sh).w(swd).h(sh)
    .transition("left", { duration: sw.dur || 1, easing: "linear" }).transition("top", { duration: sw.dur || 1, easing: "linear" })
    .child(image(`${ART}/swineherd/${sw.moving ? "walk" : "idle"}-${sw.dir}.webp`).size_full()));
  els.push(...cursorEls(app, f, fr, spots));
  return els;
}

export function worldView(app, f, walk) {
  const fr = frameOf(app, walk);
  const W = Math.round(app.size.width), H = Math.round(app.size.height);
  const still = calm();
  return div().id(walk ? "walk" : "map").size_full().relative().overflow_hidden().bg(C.grass)
    .on_mouse_down("left", (e, cx) => { press(app, f, fr, e, cx, "left"); cx.notify(); })
    .on_mouse_down("right", (e, cx) => { press(app, f, fr, e, cx, "right"); cx.notify(); })
    .on_mouse_move((e, cx) => moveMouse(app, f, fr, e, cx))
    .on_mouse_up("left", (_e, cx) => { release(app, f, cx); })
    .on_scroll_wheel((e, cx) => {
      if (walk) return;
      const w = world(app);
      const d = e.delta?.y ?? e.delta_y ?? 0;
      const z = Math.min(1.6, Math.max(0.45, w.zoomMap * (d > 0 ? 0.92 : 1.08)));
      const p = e.position ? localPoint(app, e) : { x: W / 2, y: H / 2 };
      w.cam = { x: (w.cam.x + p.x) * (z / w.zoomMap) - p.x, y: (w.cam.y + p.y) * (z / w.zoomMap) - p.y };
      w.zoomMap = z;
      cx.notify();
    })
    .children(drawWorld(app, f, fr))
    .when(!still && !walk, (el) => el.child(bellPanel(app, f)).child(productionPanel(app, f)))
    .child(statusLine(app))
    .child(hotbar(app));
}

function bellPanel(app, f) {
  const items = f.bell.slice(0, 5);
  const row = (b, i) => h_flex().id(`bell-${i}`).gap(8).px(8).py(5).rounded(3).cursor_pointer()
    .when(i === 0 && (b.kind === "blocked" || b.kind === "burnt"), (el) => el.bg("#3a1a18").border_1().border_color("#5a2e2a"))
    .hover((s) => s.bg(C.raised))
    .on_mouse_down("left", (_e, cx) => { cx.stop_propagation(); ringBell(app, f, b); })
    .child(div().w(12).font_family(PIXEL).text_size(11).text_color(b.kind === "blocked" || b.kind === "burnt" ? C.danger : b.kind === "holding" ? C.warn : C.accent).child(BELL_MARK[b.kind]))
    .child(div().flex_1().text_size(12).text_color(C.text).truncate().child(b.label))
    .when(i === 0, (el) => el.child(kbd("⌘⇧B")));
  return v_flex().id("bell-panel").absolute().right(14).top(14).w(260).p(10).gap(3).rounded(4).bg("#0f1512eb").border_1().border_color(C.border2)
    .on_mouse_down("left", stop).on_mouse_down("right", stop)
    .child(sectionTitle("BELL · IN ORDER", C.muted))
    .children(items.length ? items.map(row) : [div().text_size(12).text_color(C.muted).py(4).child("All quiet. Nobody needs you.")]);
}

function productionPanel(app, f) {
  const s = f.engine ? (app.store.state.stats ?? {}) : {};
  const cell = (label, value) => v_flex().w(105).gap(1).child(div().text_size(11).text_color(C.muted).child(label)).child(div().font_family(PIXEL).text_size(14).text_color(C.text).child(String(value)));
  return v_flex().id("prod-panel").absolute().right(14).top(Math.max(14, 30 + 32 * Math.min(5, Math.max(1, f.bell.length)) + 26)).w(260).p(10).gap(6).rounded(4).bg("#0f1512eb").border_1().border_color(C.border2)
    .on_mouse_down("left", stop).on_mouse_down("right", stop)
    .child(sectionTitle("PRODUCTION · LAST 24 H", C.muted))
    .child(h_flex().gap(8).child(cell("Crates in", s.cratesIn24h ?? 0)).child(cell("Truffles out", s.trufflesOut24h ?? 0)))
    .child(h_flex().gap(8).child(cell("Tokens", fmtTokens(s.tokens24h ?? 0))).child(cell("Burnt", s.burnt24h ?? 0)))
    .when(!f.engine, (el) => el.child(div().text_size(11).text_color(C.warn).child("Engine not running: belts are stopped.")));
}

// "Build here": the right-click menu on open ground.
export function buildHere(app, f, props, close) {
  const opts = ["oven", "review", "barn", "jobboard", "pen"];
  return v_flex().size_full().bg(C.cream).rounded(8).p(6).gap(1).border_1().border_color("#d9cdb8")
    .child(div().px(10).py(6).font_family(PIXEL).text_size(11).text_color(C.ink).child("BUILD HERE"))
    .children(opts.map((k) => h_flex().id(`bh-${k}`).px(10).py(6).gap(8).items_center().rounded(3).cursor_pointer().text_size(13).text_color(C.ink)
      .hover((s) => s.bg(C.danger).text_color("#ffffff"))
      .on_mouse_down("left", stop)
      .on_click(async () => { close(); await place(app, f, k, props.x, props.y); })
      .child(image(`${ART}/hotbar/${k}.png`).w(20).h(20)).child(KINDS[k].label)))
    .child(h_flex().id("bh-runt").px(10).py(6).gap(8).items_center().rounded(3).cursor_pointer().text_size(13).text_color(C.ink)
      .hover((s) => s.bg(C.danger).text_color("#ffffff")).on_mouse_down("left", stop)
      .on_click(() => { close(); openRuntPanel(); }).child(image(`${ART}/hotbar/runt.png`).w(20).h(20)).child("Ask the Runt"));
}

