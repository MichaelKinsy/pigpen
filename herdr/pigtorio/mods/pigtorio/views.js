// The farm's views: Map, Walk, Job Board, Adopt-a-Pig, Workshop, Report.
import { div, image } from "gpui-kit";
import { h_flex, v_flex, InputState, Input } from "gpui-base";
import { run, pickFolder, setShared, shared, terminals } from "herdr";
import { VIEW_KEYS, isHelpKey, fmtAge, fmtTokens, BELL_MARK, ALL_SKINS, COLOR_SKINS, SKIN_NAMES, ROLES, roleOf, report } from "./model.js";
import { C, PIXEL, ART, sprite, button, sectionTitle } from "./ui.js";
import { view, calm, setView } from "./nav.js";
import { dataDir, engineBin } from "./store.js";
import { openPigMenu, openTalk, openCrate, openReview, openFind, openHelp, openPick, ringBell, openPenMenu, closeTalks } from "./dialogs.js";
import { worldView, walkTo } from "./worldview.js";

const stop = (_e, cx) => cx.stop_propagation();
// Commands: `herdr-app do pigtorio.<view>` and friends. A command may carry
// one argument after a colon (`talk:t7`, `menu:t7`, `review:c12`, `walk:12,6`
// in tiles, `tool:oven`), which drives the same paths a click does.
export function farmCommand(app, c, cx) {
  const [name, arg] = c.split(":");
  if (["map", "walk", "board", "adopt", "workshop", "report", "terminals"].includes(name) && !arg) setView(name);
  else if (name === "bell") ringBell(app, app.frame());
  else if (name === "find") openFind();
  else if (name === "help") openHelp();
  else if (name === "calm") setShared("pigtorio.calm", !calm());
  else if (name === "crate") openCrate(arg ? { pig: arg } : {});
  else if (name === "talk" && arg) openTalk(arg);
  else if (name === "menu" && arg) openPigMenu(arg, { x: 400, y: 300 });
  else if (name === "penmenu" && arg) openPenMenu(arg, { x: 400, y: 300 });
  else if (name === "review" && arg) openReview(arg);
  else if (name === "relay" && arg) openPick({ mode: "relay", from: arg });
  else if (name === "walk" && arg) { setView("walk"); const [x, y] = arg.split(",").map(Number); walkTo(app, cx, x, y, null); }
  else if (name === "tool" && arg) { app.ui.world = { ...(app.ui.world ?? {}), tool: arg, beltFrom: null }; }
  else if (name === "closetalk") closeTalks();
}

// The farm copy's inputs: GPUI wants their state made outside render.
export function farmInit(app) {
  const nameInput = app.ui.adoptName = InputState.new({ placeholder: "Name" });
  const modelInput = app.ui.adoptModel = InputState.new({ placeholder: "model pattern (optional), e.g. sonnet or gpt-5", value: "" });
  nameInput.on("change", () => { if (app.ui.adopt) app.ui.adopt.name = nameInput.value(); });
  modelInput.on("change", () => { if (app.ui.adopt) app.ui.adopt.model = modelInput.value(); });
  for (const s of [nameInput, modelInput]) {
    s.on("focus", () => { app.ui.typing = true; });
    s.on("blur", () => { app.ui.typing = false; });
  }
}

export function farmKey(app, e, cx) {
  if (e.modifiers?.platform || e.modifiers?.control) return;
  // Only the Adopt view has text inputs; a flag left by an input that unmounted without a blur must not eat keys.
  if (app.ui.typing && view() === "adopt") return;
  app.ui.typing = false;
  const k = e.key;
  if (VIEW_KEYS[k]) setView(VIEW_KEYS[k]);
  else if (k === "b") ringBell(app, app.frame());
  else if (isHelpKey(e)) openHelp();
  else if (k === "/") openFind();
  else if (k === "n") openCrate({});
  else if (k === "escape") setView("map");
  else if (view() === "walk" && ["up", "down", "left", "right"].includes(k)) {
    const s = app.ui.swine ?? { x: 10, y: 4.5 };
    const d = { up: [0, -1.5], down: [0, 1.5], left: [-1.5, 0], right: [1.5, 0] }[k];
    walkTo(app, cx, s.x + d[0], s.y + d[1], null);
  } else return;
  cx.stop_propagation();
  cx.notify();
}

export function renderFarm(app, f, cx) {
  const v = view();
  if (v === "walk") return worldView(app, f, true);
  if (v === "board") return boardView(app, f);
  if (v === "adopt") return adoptView(app, f);
  if (v === "workshop") return workshopView(app, f);
  if (v === "report") return reportView(app, f);
  return worldView(app, f, false);
}


// ---------------------------------------------------------------- Job Board

const STATE_LABEL = { board: "on the board", working: "with a pig", blocked: "blocked", oven: "in the Oven", burnt: "burnt", holding: "holding at Review", barn: "at the Barn", shipped: "shipped", failed: "failed" };

function crateCard(app, f, c) {
  const pen = f.pens.find((p) => p.space === c.pen);
  const pig = c.at?.pig ? f.pigs.find((p) => p.address === c.at.pig) : null;
  const line = pen?.line;
  const meta = [c.size ?? "one-shot", pen?.name, pig ? `${pig.name}` : null, STATE_LABEL[c.state] ?? c.state, c.burns ? `burnt x${c.burns}` : null, c.tokens ? `${fmtTokens(c.tokens)} tokens` : null].filter(Boolean).join(" · ");
  const color = c.state === "burnt" || c.state === "blocked" ? C.danger : c.state === "holding" || c.state === "barn" ? C.warn : c.state === "shipped" ? C.accent : C.soft;
  const actions = [];
  if (c.state === "board") {
    actions.push(button(`hand-${c.id}`, "HAND TO A PIG", () => openPick({ mode: "hand", crate: c.id, pen: c.pen }), { h: 26, size: 10 }));
    if (line) actions.push(button(`run-${c.id}`, "PUT ON THE BELT", () => app.store.command("crate.run", { id: c.id, line: line.id }), { h: 26, size: 10, primary: true }));
    actions.push(button(`rm-${c.id}`, "REMOVE", () => app.store.command("crate.remove", { id: c.id }), { h: 26, size: 10 }));
  } else if (c.state === "holding" || c.state === "barn") {
    actions.push(button(`rev-${c.id}`, "REVIEW", () => openReview(c.id), { h: 26, size: 10, primary: true }));
  } else if (c.state === "burnt") {
    actions.push(button(`log-${c.id}`, "LOG", () => openCrate({ crate: c.id }), { h: 26, size: 10 }));
    if (c.line) actions.push(button(`oven-${c.id}`, "RUN THE OVEN AGAIN", () => app.store.command("oven.run", { id: c.id }), { h: 26, size: 10 }));
    if (c.at?.pig) actions.push(button(`talk-${c.id}`, "TALK", () => openTalk(c.at.pig), { h: 26, size: 10 }));
  } else if (c.state === "failed" && c.at?.kind === "barn") {
    actions.push(button(`ship-${c.id}`, "SHIP AGAIN", () => app.store.command("barn.ship", { id: c.id }), { h: 26, size: 10, primary: true }));
    if (c.note) actions.push(div().text_size(11).text_color(C.danger).child(c.note));
  } else if (c.state === "shipped") {
    actions.push(div().text_size(11).text_color(C.accent).child(c.pr ?? (c.commit ? `commit ${c.commit}` : "accepted")));
  } else if (pig) {
    actions.push(button(`talk-${c.id}`, `TALK TO ${pig.name.toUpperCase()}`, () => openTalk(pig.address), { h: 26, size: 10 }));
  }
  return v_flex().id(`card-${c.id}`).p(10).gap(4).rounded(4).bg(C.raised2).border_1().border_color(C.border2)
    .child(h_flex().gap(8).child(div().font_family(PIXEL).text_size(10).text_color(C.muted).child(`#${c.id}`))
      .child(div().flex_1().text_size(13).text_color(C.text).truncate().child(c.title)))
    .child(div().text_size(11).text_color(color).child(meta))
    .child(div().text_size(11).text_color(C.soft).child(`Done when: ${c.doneWhen}`))
    .when(actions.length > 0, (el) => el.child(h_flex().flex_wrap().gap(6).pt(4).children(actions)));
}

function boardView(app, f) {
  const cols = [
    { title: "ON THE BOARD", crates: f.crates.filter((c) => c.state === "board") },
    { title: "MOVING", crates: f.crates.filter((c) => ["working", "blocked", "oven"].includes(c.state)) },
    { title: "NEEDS YOU", crates: f.crates.filter((c) => ["burnt", "holding", "barn"].includes(c.state)) },
    { title: "SHIPPED", crates: f.crates.filter((c) => ["shipped", "failed"].includes(c.state)).slice(-12).reverse() },
  ];
  return v_flex().id("board").size_full().bg(C.chrome).p(20).gap(14)
    .child(h_flex().items_center().gap(12)
      .child(div().font_family(PIXEL).text_size(16).text_color(C.text).child("JOB BOARD"))
      .child(div().text_size(12).text_color(C.muted).child(`${cols[0].crates.length} queued · ${cols[1].crates.length} moving · ${cols[2].crates.length} need you`))
      .child(div().flex_1())
      .child(button("new-crate", "N  NEW CRATE", () => openCrate({}), { primary: true })))
    .child(div().text_size(12).text_color(C.muted).child("A crate carries a Mandate: who asked, and when it is done. A crate with no done-when cannot leave the board. Put it on a pen's belt, or hand it to one pig."))
    .when(!f.engine, (el) => el.child(div().text_size(12).text_color(C.warn).child("The engine is starting; crates move when it runs.")))
    .child(h_flex().flex_1().min_h(0).gap(12).items_start().children(cols.map((col, i) => v_flex().id(`col-${i}`).flex_1().min_w(0).h_full().gap(8)
      .child(sectionTitle(`${col.title} · ${col.crates.length}`, i === 2 && col.crates.length ? C.warn : C.muted))
      .child(v_flex().id(`colbody-${i}`).flex_1().min_h(0).gap(8).overflow_y_scroll().children(col.crates.map((c) => crateCard(app, f, c)))))));
}

// ---------------------------------------------------------------- Adopt-a-Pig

const MOCK = { key: "mock", name: "Mock pig", run: null, installed: true, about: "tutorial pig: answers instantly, 0 tokens" };

function adoptView(app, f) {
  const ui = app.ui.adopt ??= { kind: null, role: "builder", pen: null, skin: COLOR_SKINS[(Date.now() / 1000 | 0) % COLOR_SKINS.length], model: "", agents: null, loading: false, name: null };
  const known = app.ui.agentsList;
  const kinds = [...(known ?? []).filter((a) => a.installed), MOCK];
  if (!ui.kind) ui.kind = kinds.find(isPiG)?.key ?? kinds[0]?.key ?? "mock";
  const kind = kinds.find((k) => k.key === ui.kind) ?? MOCK;
  const pens = f.pens;
  const asked = shared("pigtorio.adoptPen");
  if (asked && asked !== app.ui.adoptPenSeen) { app.ui.adoptPenSeen = asked; ui.pen = asked; }
  if (!ui.pen && pens.length) ui.pen = (pens.find((p) => p.active) ?? pens[0]).space;
  const name = ui.name ?? SKIN_NAMES[ui.skin];
  const choice = (id, on, label, sub, onClick) => v_flex().id(id).p(10).gap(2).rounded(4).cursor_pointer().min_w(150)
    .bg(on ? "#1d3a2e" : C.raised2).border_1().border_color(on ? C.accent : C.border2)
    .on_mouse_down("left", stop).on_click((_e, cx) => { onClick(); cx.notify(); })
    .child(div().text_size(13).text_color(C.text).child(label)).when(Boolean(sub), (el) => el.child(div().text_size(11).text_color(C.muted).child(sub)));
  const nameInput = app.ui.adoptName, modelInput = app.ui.adoptModel;
  return v_flex().id("adopt").size_full().bg(C.chrome).p(20).gap(14).overflow_y_scroll()
    .child(h_flex().items_center().gap(12).child(div().font_family(PIXEL).text_size(16).text_color(C.text).child("ADOPT-A-PIG"))
      .child(div().text_size(12).text_color(C.muted).child("knownAgents(): the agents installed here · any agent that reports OSC 7501 can be a pig")))
    .child(sectionTitle("1 · PICK A KIND"))
    .child(h_flex().gap(8).flex_wrap().children(known == null ? [div().text_size(12).text_color(C.muted).child("Looking for installed agents…")] : kinds.map((k) =>
      choice(`kind-${k.key}`, ui.kind === k.key, k.name, k.key === "mock" ? k.about : k.run?.join(" "), () => { ui.kind = k.key; }))))
    .when(isPiG(kind), (el) => el.child(h_flex().gap(8).items_center().child(div().text_size(12).text_color(C.muted).child("Model"))
      .child(Input.new(modelInput).w(380).h(30).px(8).rounded(4).border_1().border_color(C.border2).bg(C.deep).text_size(12).text_color(C.text))))
    .child(sectionTitle("2 · PICK A ROLE"))
    .child(h_flex().gap(8).flex_wrap().children(ROLES.map((r) => choice(`role-${r.id}`, ui.role === r.id, r.label, r.about, () => { ui.role = r.id; }))))
    .child(sectionTitle("3 · DRESS AND NAME"))
    .child(h_flex().gap(6).flex_wrap().children(ALL_SKINS.map((s) => div().id(`skin-${s}`).w(72).h(54).rounded(4).cursor_pointer()
      .bg(ui.skin === s ? "#1d3a2e" : C.raised2).border_1().border_color(ui.skin === s ? C.accent : C.border2)
      .on_mouse_down("left", stop).on_click((_e, cx) => { ui.skin = s; if (!ui.name || Object.values(SKIN_NAMES).includes(ui.name)) { ui.name = SKIN_NAMES[s]; nameInput.set_value(ui.name); } cx.notify(); })
      .child(image(sprite(s, "idle")).w(72).h(54)))))
    .child(h_flex().gap(8).items_center().child(div().text_size(12).text_color(C.muted).child("Name"))
      .child(Input.new(nameInput).w(220).h(30).px(8).rounded(4).border_1().border_color(C.border2).bg(C.deep).text_size(12).text_color(C.text)))
    .child(sectionTitle("4 · PICK A PEN"))
    .child(h_flex().gap(8).flex_wrap()
      .children(pens.map((p) => choice(`pen-${p.space}`, ui.pen === p.space, p.name, `${p.machine ?? "this mac"} · ${p.pigs.length} pig${p.pigs.length === 1 ? "" : "s"}`, () => { ui.pen = p.space; })))
      .child(choice("pen-new", ui.pen === "new", "+ New pen…", "choose a folder", () => { ui.pen = "new"; })))
    .child(h_flex().gap(12).items_center().pt(6)
      .child(button("adopt-go", `ADOPT ${(name || "PIG").toUpperCase()}`, () => adopt(app, f, kind), { primary: true, h: 36 }))
      .child(div().text_size(11).text_color(C.muted).child(`runs: ${adoptArgv(app, kind, "<id>").join(" ")}`)));
}

// PiG takes --session-dir, --model and --append-system-prompt; its program key varies by install.
const isPiG = (k) => k.key === "pig" || k.run?.[0] === "pig" || /(^|\/)pig$/.test(k.run?.[0] ?? "");

function adoptArgv(app, kind, key) {
  const role = roleOf(app.ui.adopt?.role);
  const sessions = `${dataDir()}/sessions/${key}`;
  if (kind.key === "mock") return [engineBin(), "fakepig", "--session-dir", sessions, "--name", app.ui.adopt?.name || SKIN_NAMES[app.ui.adopt?.skin] || "pig"];
  const argv = [...(kind.run ?? [kind.key])];
  if (isPiG(kind)) {
    argv.push("--session-dir", sessions);
    if (app.ui.adopt?.model) argv.push("--model", app.ui.adopt.model);
    if (role) argv.push("--append-system-prompt", role.brief);
  }
  return argv;
}

async function adopt(app, f, kind) {
  const ui = app.ui.adopt;
  const key = `p${Date.now().toString(36)}`;
  const argv = adoptArgv(app, kind, key);
  const name = ui.name || SKIN_NAMES[ui.skin];
  const pig = isPiG(kind) || kind.key === "mock";
  // Agents other than PiG carry no session key in their argv: remember which terminals
  // existed, and claim the new one running this program; their role goes in as the first message.
  const before = pig ? undefined : terminals().map((t) => t.address);
  const space = ui.pen === "new" ? null : ui.pen;
  await app.store.updateFarm((farm) => { farm.pending = { ...(farm.pending ?? {}), [key]: { name, skin: ui.skin, role: ui.role, at: Date.now(), space, program: argv[0], before } }; return farm; });
  const task = pig ? undefined : roleOf(ui.role)?.brief;
  if (ui.pen === "new" || !ui.pen) {
    const folder = await pickFolder({ prompt: "Make a pen here" });
    if (!folder) return;
    run("space.new", { folder, argv, ...(task ? { task } : {}) });
  } else {
    run("tab.new", { space: ui.pen, name, argv, ...(task ? { task } : {}) });
  }
  app.ui.adopt = { ...ui, name: null };
  setView("map");
}

// Adoptions in flight: a new terminal whose argv carries the pending key gets its name, skin and role.
export async function claimPending(app) {
  const pending = app.store.farm.pending ?? {};
  const keys = Object.keys(pending);
  if (!keys.length) return;
  const found = {};
  for (const t of terminals()) {
    const argv = (t.argv ?? []).join(" ");
    for (const k of keys) {
      const p = pending[k];
      if (argv.includes(`/sessions/${k}`)) found[k] = t.address;
      else if (p.before && !p.before.includes(t.address) && argv.includes(p.program) && (!p.space || t.labels?.["herdr.space"] === p.space) && !Object.values(found).includes(t.address)) found[k] = t.address;
    }
  }
  const stale = keys.filter((k) => Date.now() - (pending[k].at ?? 0) > 10 * 60_000);
  if (!Object.keys(found).length && !stale.length) return;
  await app.store.updateFarm((farm) => {
    for (const [k, addr] of Object.entries(found)) {
      const { at, space, program, before, ...meta } = farm.pending[k] ?? {};
      farm.pigs = { ...(farm.pigs ?? {}), [addr]: { ...(farm.pigs?.[addr] ?? {}), ...meta } };
      delete farm.pending[k];
    }
    for (const k of stale) delete farm.pending[k];
    return farm;
  });
}

// ---------------------------------------------------------------- Workshop

function workshopView(app, f) {
  const ui = app.ui.workshop ??= { target: "swineherd", state: "idle" };
  const asked = shared("pigtorio.workshopTarget");
  if (asked && asked !== ui.askedSeen) { ui.askedSeen = asked; ui.target = asked; }
  const targets = [{ id: "swineherd", label: "The Swineherd (you)" }, ...f.pigs.map((p) => ({ id: p.address, label: `${p.name} · ${p.address}` }))];
  const target = targets.find((t) => t.id === ui.target) ?? targets[0];
  const skin = target.id === "swineherd" ? (f.farm.swineherd?.skin ?? "pink") : (f.pigs.find((p) => p.address === target.id)?.skin ?? "pink");
  const states = ["idle", "asleep", "thinking", "reading", "editing", "running", "blocked", "failed", "done", "completing", "queued", "aborted", "orphaned", "unknown"];
  const setSkin = (s) => app.store.updateFarm((farm) => {
    if (target.id === "swineherd") farm.swineherd = { ...(farm.swineherd ?? {}), skin: s };
    else farm.pigs = { ...(farm.pigs ?? {}), [target.id]: { ...(farm.pigs?.[target.id] ?? {}), skin: s } };
    return farm;
  });
  const setRole = (r) => app.store.updateFarm((farm) => { farm.pigs = { ...(farm.pigs ?? {}), [target.id]: { ...(farm.pigs?.[target.id] ?? {}), role: r } }; return farm; });
  const role = f.farm.pigs?.[target.id]?.role;
  return h_flex().id("workshop").size_full().bg(C.chrome).p(20).gap(20).items_start()
    .child(v_flex().w(240).gap(6).h_full().overflow_y_scroll()
      .child(sectionTitle("WHO"))
      .children(targets.map((t) => div().id(`ws-${t.id}`).px(10).py(6).rounded(3).cursor_pointer().text_size(12)
        .bg(t.id === target.id ? C.raised : C.chrome).text_color(t.id === target.id ? C.text : C.soft)
        .on_mouse_down("left", stop).on_click((_e, cx) => { ui.target = t.id; cx.notify(); }).child(t.label))))
    .child(v_flex().flex_1().gap(14)
      .child(h_flex().gap(20).items_center()
        .child(div().w(256).h(192).rounded(6).bg(C.grass).border_1().border_color(C.border2)
          .child(target.id === "swineherd"
            ? div().pl(100).pt(50).child(image(`${ART}/swineherd/walk-down.webp`).w(72).h(88))
            : image(sprite(skin, ui.state)).w(256).h(192)))
        .child(v_flex().gap(6).child(div().font_family(PIXEL).text_size(16).text_color(C.text).child(target.id === "swineherd" ? "SWINEHERD" : (f.pigs.find((p) => p.address === target.id)?.name ?? "").toUpperCase()))
          .child(div().text_size(12).text_color(C.muted).child(`skin: ${skin}`))
          .when(target.id !== "swineherd", (el) => el.child(h_flex().gap(4).flex_wrap().children(ROLES.map((r) => div().id(`wr-${r.id}`).px(8).py(4).rounded(3).cursor_pointer().text_size(11)
            .bg(role === r.id ? "#1d3a2e" : C.raised2).text_color(role === r.id ? C.accent : C.soft)
            .on_mouse_down("left", stop).on_click(() => setRole(r.id)).child(r.label)))))))
      .when(target.id !== "swineherd", (el) => el.child(sectionTitle("STATES")).child(h_flex().gap(4).flex_wrap().children(states.map((s) => div().id(`st-${s}`).px(8).py(4).rounded(3).cursor_pointer().text_size(11)
        .bg(ui.state === s ? C.raised : C.raised2).text_color(ui.state === s ? C.text : C.soft)
        .on_mouse_down("left", stop).on_click((_e, cx) => { ui.state = s; cx.notify(); }).child(s)))))
      .child(sectionTitle(target.id === "swineherd" ? "PRESETS · the Swineherd's look is fixed in this version" : "PRESETS · 9 colours and 6 characters"))
      .when(target.id !== "swineherd", (el) => el.child(h_flex().gap(8).flex_wrap().children(ALL_SKINS.map((s) => v_flex().id(`preset-${s}`).items_center().gap(2).cursor_pointer()
        .on_mouse_down("left", stop).on_click(() => setSkin(s))
        .child(div().w(96).h(72).rounded(4).bg(s === skin ? "#1d3a2e" : C.raised2).border_1().border_color(s === skin ? C.accent : C.border2).child(image(sprite(s, "idle")).w(96).h(72)))
        .child(div().text_size(11).text_color(s === skin ? C.accent : C.muted).child(SKIN_NAMES[s])))))));
}

// ---------------------------------------------------------------- Morning report

function reportView(app, f) {
  const lastSeen = shared("pigtorio.lastReport") ?? (f.now - 12 * 3600_000);
  const away = Math.max(0, f.now - lastSeen);
  const r = report(f.crates, f.pigs, f.now, Math.max(away, 3600_000));
  const tile = (label, value, sub, color = C.text) => v_flex().flex_1().p(14).gap(4).rounded(4).bg(C.raised2).border_1().border_color(C.border2)
    .child(div().text_size(11).text_color(C.muted).child(label)).child(div().font_family(PIXEL).text_size(28).text_color(color).child(String(value))).child(div().text_size(11).text_color(C.soft).child(sub));
  const machines = new Set(f.pens.map((p) => p.machine ?? "")).size || 1;
  const napping = f.pigs.filter((p) => p.status === "idle").length;
  return v_flex().id("report").size_full().bg(C.chrome).p(24).gap(16).overflow_y_scroll()
    .child(h_flex().items_baseline().gap(14).child(div().font_family(PIXEL).text_size(20).text_color(C.text).child("MORNING REPORT"))
      .child(div().text_size(13).text_color(C.muted).child(`While you were away: ${fmtAge(away)} · ${new Date(f.now).toDateString()}`)))
    .child(h_flex().gap(12)
      .child(tile("Truffles shipped", r.shipped.length, "crates through the Barn", C.accent))
      .child(tile("Burnt", r.burnt.length, "crates the Oven burnt twice", r.burnt.length ? C.danger : C.text))
      .child(tile("Waiting on you", `${r.blocked.length} + ${r.holding.length}`, "blocked · holding for review", r.blocked.length ? C.warn : C.text))
      .child(tile("Trough", fmtTokens(r.tokens), r.cost ? `tokens · $${r.cost.toFixed(2)}` : "tokens")))
    .child(sectionTitle("SHIPPED"))
    .children(r.shipped.length ? r.shipped.slice(-8).map((c) => h_flex().gap(10).text_size(13).child(div().text_color(C.text).child(`${f.pens.find((p) => p.space === c.pen)?.name ?? ""} · ${c.title}`)).child(div().text_color(C.accent).child(c.pr ?? c.commit ?? "committed")))
      : [div().text_size(13).text_color(C.muted).child("Nothing shipped yet.")])
    .child(sectionTitle("NEEDS YOU, IN BELL ORDER"))
    .children(f.bell.length ? f.bell.slice(0, 8).map((b, i) => h_flex().id(`rb-${i}`).gap(10).text_size(13).cursor_pointer().hover((s) => s.text_color(C.text))
      .on_mouse_down("left", stop).on_click(() => ringBell(app, f, b))
      .child(div().w(14).font_family(PIXEL).text_color(b.kind === "blocked" || b.kind === "burnt" ? C.danger : C.warn).child(BELL_MARK[b.kind]))
      .child(div().text_color(C.soft).child(b.label)).child(div().text_color(C.muted).child(b.since ? fmtAge(f.now - b.since) : "")))
      : [div().text_size(13).text_color(C.muted).child("Nothing. Every pig is fine.")])
    .child(h_flex().gap(8).pt(8)
      .child(button("rep-bell", "GO TO THE BELL (⌘⇧B)", () => ringBell(app, f), { primary: true }))
      .child(button("rep-map", "OPEN THE FARM (⌘⇧M)", () => { setShared("pigtorio.lastReport", Date.now()); setView("map"); })))
    .child(div().text_size(12).text_color(C.muted).child(`${f.pens.length} pens · ${machines} machine${machines === 1 ? "" : "s"} · ${f.pigs.length} pigs: ${napping} napping · ${r.blocked.length} blocked · ${r.holding.length} holding`));
}
