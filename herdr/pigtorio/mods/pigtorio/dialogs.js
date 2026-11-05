// Popups, dialogs and windows over the farm.
import { div, image } from "gpui-kit";
import { h_flex, v_flex, InputState, Input, TextareaState, Textarea } from "gpui-base";
import { props, closeOverlay, openOverlay, run, exec, notify, setShared, shared, panes } from "herdr";
import { C, PIXEL, ART, sprite, tag, button, sectionTitle, kbd } from "./ui.js";
import { HELP_KEYS, HELP_VERBS, addRelay, addStation, removeLine, filterFind, fmtAge, fmtTokens, roleOf, spriteState, statusWord, lineBusy } from "./model.js";
import { setView } from "./nav.js";
import { renderRunt, runtInit } from "./runt.js";
import { buildHere } from "./worldview.js";

// Changes the pen's line unless crates ride it (they hold their place by stop
// number). Answers whether it changed.
function editLine(app, f, pen, change) {
  if (lineBusy(app.store.farm, f.crates, pen)) {
    notify({ title: "The belt is busy", body: "Crates are on this belt. Change it once they ship or fail.", level: "warning", timeout: 4000 });
    return false;
  }
  app.store.updateFarm(change);
  return true;
}

const stop = (_e, cx) => cx.stop_propagation();
const close = () => { const id = props()?.overlay; if (id) closeOverlay(id); };

// ---------------------------------------------------------------- openers

export function openPigMenu(address, at) {
  openOverlay({ mod: "pigtorio", kind: "popup", frame: "none", width: 230, height: 352, at: at ?? "pointer", props: { role: "menu", pig: address } });
}

export function openPenMenu(space, at) {
  openOverlay({ mod: "pigtorio", kind: "popup", frame: "none", width: 250, height: 380, at: at ?? "pointer", props: { role: "penmenu", pen: space } });
}

// Talk: the pig's live terminal in a window over the farm. Typing goes to the real session.
export function openTalk(address) {
  const id = openOverlay({
    kind: "window", key: `talk-${address}`, width: 0.62, height: 0.62,
    layout: { type: "column", children: [
      { type: "block", size: { px: 34 }, content: { mod: "pigtorio", props: { role: "talkbar", pig: address } } },
      { type: "block", content: { terminal: address } },
    ] },
  });
  setShared("pigtorio.talk", { ...(shared("pigtorio.talk") ?? {}), [address]: id });
}

// Closes every Talk window (the `closetalk` command; each window's own Close button closes one).
export function closeTalks() {
  for (const id of Object.values(shared("pigtorio.talk") ?? {})) closeOverlay(id);
  setShared("pigtorio.talk", {});
}

export function openCrate(p) {
  openOverlay({ mod: "pigtorio", kind: "dialog", width: 600, height: p.crate ? 560 : 600, props: { role: "crate", ...p } });
}

export function openPick(p) {
  openOverlay({ mod: "pigtorio", kind: "dialog", width: 380, height: 460, props: { role: "pick", ...p } });
}

export function openReview(crate) {
  openOverlay({ mod: "pigtorio", kind: "dialog", width: 0.72, height: 0.84, props: { role: "review", crate } });
}

export function openFind() {
  openOverlay({ mod: "pigtorio", kind: "dialog", width: 480, height: 440, props: { role: "find" } });
}

// The Bell goes to the most urgent thing (or the one given): a pig's
// terminal, a burnt crate's log, or a crate holding at Review.
export function openHelp() {
  openOverlay({ mod: "pigtorio", kind: "dialog", width: 560, height: 580, props: { role: "help" } });
}

export function ringBell(app, f, item) {
  const b = item ?? f.bell[0];
  if (!b) { notify({ title: "All quiet", body: "No pig needs you.", level: "success", timeout: 2500 }); return; }
  if (b.kind === "burnt") return openCrate({ crate: b.crate });
  if (b.kind === "holding" && b.crate) return openReview(b.crate);
  if (b.pig) {
    if (b.kind === "done") run("agent.focus", { terminal: b.pig });
    openTalk(b.pig);
  }
}

// ---------------------------------------------------------------- roles

export function dialogInit(app, cx) {
  const p = props() ?? {};
  app.ui.dialog = { step: null, note: "", log: null };
  if (p.role === "crate" && !p.crate) {
    app.ui.title = InputState.new({ placeholder: "What: one line" });
    app.ui.done = InputState.new({ placeholder: "Done when: the observable result" });
    app.ui.text = TextareaState.new({ placeholder: "Details, files, links (optional)", rows: 6 });
    app.ui.size = "one-shot";
    app.ui.title.focus();
    cx.timer.after(300, () => app.ui.title.focus());
    for (const s of [app.ui.title, app.ui.done]) s.on("change", (_e, cx) => cx.notify());
  }
  if (p.role === "find") {
    app.ui.query = InputState.new({ placeholder: "Find pig or pen" });
    app.ui.query.on("change", (_e, cx) => cx.notify());
    app.ui.query.on("submit", (_e, cx) => { const first = findItems(app)[0]; if (first) first.go(); });
    app.ui.query.focus();
    cx.timer.after(300, () => app.ui.query.focus());
  }
  if (p.role === "review") {
    app.ui.note = InputState.new({ placeholder: "Note for the pig (send back)" });
    cx.spawn(async (cx) => { await app.store.load(); await loadDiff(app); cx.notify(); });
  }
  if (p.role === "penmenu") {
    app.ui.test = InputState.new({ placeholder: "test command, e.g. go test ./..." });
    cx.spawn(async (cx) => { await app.store.load(); app.ui.test.set_value(app.store.farm.pens?.[p.pen]?.test ?? ""); cx.notify(); });
  }
  if (p.role === "runt") runtInit(app, cx);
  if (p.role !== "talkbar") cx.timer.after(50, () => app.handle.focus());
}

export function dialogKey(app, e, cx) {
  if (e.key === "escape") { close(); cx.stop_propagation(); }
}

export function renderDialog(app, f, cx) {
  const role = props()?.role;
  if (role === "menu") return pigMenu(app, f);
  if (role === "penmenu") return penMenu(app, f);
  if (role === "talkbar") return talkbar(app, f);
  if (role === "crate") return props().crate ? crateView(app, f) : crateForm(app, f);
  if (role === "pick") return pickView(app, f);
  if (role === "review") return reviewView(app, f);
  if (role === "find") return findView(app, f);
  if (role === "help") return helpView();
  if (role === "runt") return renderRunt(app, f);
  if (role === "buildhere") return buildHere(app, f, props(), close);
  return div().child(`unknown role ${role}`);
}

// ---------------------------------------------------------------- pig menu

function menuItem(id, label, onClick, opts = {}) {
  return div().id(id).px(12).py(7).rounded(3).cursor_pointer().text_size(13)
    .text_color(opts.danger ? "#8a3d57" : C.ink)
    .when(Boolean(opts.first), (el) => el.bg(C.danger).text_color("#ffffff"))
    .hover((s) => s.bg(C.danger).text_color("#ffffff"))
    .on_mouse_down("left", stop)
    .on_click((_e, cx) => { onClick(cx); })
    .child(label);
}

function pigMenu(app, f) {
  const addr = props().pig;
  const pig = f.pigs.find((p) => p.address === addr);
  const d = app.ui.dialog;
  const name = pig?.name ?? addr;
  if (d.step === "retire") {
    return v_flex().size_full().bg(C.cream).rounded(8).p(10).gap(8).border_1().border_color("#d9cdb8")
      .child(div().text_size(13).text_color(C.ink).child(`Retire ${name}? Its terminal ends. Its session file stays.`))
      .child(h_flex().gap(6)
        .child(menuItem("retire-yes", "Retire", () => { close(); run("terminal.kill", { terminal: addr }); }, { danger: true }))
        .child(menuItem("retire-no", "Keep", (cx) => { d.step = null; cx.notify(); })));
  }
  const word = pig ? statusWord(pig.status, pig.crate) : "gone";
  return v_flex().size_full().bg(C.cream).rounded(8).p(6).gap(1).border_1().border_color("#d9cdb8").shadow_lg()
    .child(h_flex().px(10).py(6).gap(8).items_center()
      .child(div().font_family(PIXEL).text_size(11).text_color(C.ink).child(name.toUpperCase()))
      .child(div().text_size(11).text_color("#7a6a55").child(`${roleOf(pig?.role)?.label ?? ""} · ${word}`)))
    .child(menuItem("m-talk", "Talk", () => { close(); openTalk(addr); }, { first: true }))
    .child(menuItem("m-hand", "Hand crate…", () => { close(); openCrate({ pig: addr, pen: pig?.space }); }))
    .child(menuItem("m-relay", "Relay to…", () => { close(); openPick({ mode: "relay", from: addr, pen: pig?.space }); }))
    .child(menuItem("m-peek", "Peek", () => { close(); run("terminal.peek", { terminal: addr }); }))
    .child(menuItem("m-focus", "Focus in tab", () => { close(); run("agent.focus", { terminal: addr }); setView("terminals"); }))
    .child(menuItem("m-dress", "Dress in the Workshop", () => { close(); setShared("pigtorio.workshopTarget", addr); setView("workshop"); }))
    .child(menuItem("m-nap", "Nap", () => { close(); if (pig?.tab) run("tab.settle", { id: pig.tab }); }))
    .child(menuItem("m-retire", "Retire…", (cx) => { d.step = "retire"; cx.notify(); }, { danger: true }));
}

function penMenu(app, f) {
  const id = props().pen;
  const pen = f.pens.find((p) => p.space === id);
  const line = pen?.line;
  const has = (k) => line?.stops?.some((s) => s.kind === k);
  const save = () => app.store.updateFarm((farm) => { farm.pens = { ...(farm.pens ?? {}), [id]: { ...(farm.pens?.[id] ?? {}), test: app.ui.test.value().trim() } }; return farm; });
  return v_flex().size_full().bg(C.cream).rounded(8).p(6).gap(1).border_1().border_color("#d9cdb8")
    .child(div().px(10).py(6).font_family(PIXEL).text_size(11).text_color(C.ink).child(`${(pen?.name ?? "pen").toUpperCase()} · ${pen?.pigs.length ?? 0} PIGS`))
    .child(menuItem("p-crate", "New crate here…", () => { close(); openCrate({ pen: id }); }, { first: true }))
    .child(menuItem("p-adopt", "Adopt a pig here…", () => { close(); setShared("pigtorio.adoptPen", id); setView("adopt"); }))
    .child(menuItem("p-walk", "Walk in", () => { close(); setShared("pigtorio.walkPen", id); setView("walk"); }))
    .child(menuItem("p-open", "Open its terminals", () => { close(); run("space.select", { id }); setView("terminals"); }))
    .when(!has("oven"), (el) => el.child(menuItem("p-oven", "Build an Oven (runs tests)", () => { editLine(app, f, id, (farm) => addStation(farm, id, "oven")); close(); })))
    .when(!has("review"), (el) => el.child(menuItem("p-review", "Build a Review Station", () => { editLine(app, f, id, (farm) => addStation(farm, id, "review")); close(); })))
    .when(!has("barn"), (el) => el.child(menuItem("p-barn", "Run the belt to the Barn", () => { editLine(app, f, id, (farm) => addStation(farm, id, "barn")); close(); })))
    .when(Boolean(line), (el) => el.child(menuItem("p-clear", "Pull up the belt", () => { editLine(app, f, id, (farm) => removeLine(farm, id)); close(); }, { danger: true })))
    .child(div().px(10).pt(6).text_size(11).text_color("#7a6a55").child("Oven's test command"))
    .child(h_flex().px(8).gap(4).child(Input.new(app.ui.test).flex_1().h(28).px(6).rounded(4).border_1().border_color("#d9cdb8").bg("#ffffff").text_size(12).text_color(C.ink))
      .child(div().id("p-save").px(8).py(5).rounded(4).bg(C.ink).text_color(C.cream).text_size(11).cursor_pointer().on_mouse_down("left", stop).on_click(() => { save(); close(); }).child("Save")));
}

// ---------------------------------------------------------------- Talk window title bar

function talkbar(app, f) {
  const addr = props().pig;
  const pig = f.pigs.find((p) => p.address === addr);
  const c = pig?.crate;
  const word = pig ? statusWord(pig.status, c) : "gone";
  const color = word === "blocked" || word === "burnt" ? C.danger : word === "working" ? C.accent : C.warn;
  return h_flex().id("talkbar").size_full().bg(C.raised).border_b_1().border_color(C.border2).px(10).gap(10).items_center()
    .child(div().w(10).h(10).rounded(5).bg(color))
    .when(Boolean(pig), (el) => el.child(image(sprite(pig.skin, spriteState(pig.status, c))).w(32).h(24)))
    .child(div().text_size(12).text_color(C.text).truncate().child(`${pig?.name ?? addr} · ${roleOf(pig?.role)?.label ?? pig?.program?.name ?? ""} · ${word}${pig?.since ? ` ${fmtAge(Date.now() - pig.since)}` : ""}`))
    .when(Boolean(c), (el) => el.child(div().text_size(11).text_color(C.muted).truncate().child(`crate: ${c.title}`)))
    .child(div().flex_1())
    .child(div().text_size(11).text_color(C.muted).child("Typing goes to the real session"))
    .child(div().id("tb-hand").px(8).py(3).rounded(3).bg(C.raised2).text_size(11).text_color(C.text).cursor_pointer().on_mouse_down("left", stop)
      .on_click(() => openCrate({ pig: addr, pen: pig?.space })).child("Hand crate"))
    .child(div().id("tb-close").px(8).py(3).rounded(3).bg(C.raised2).text_size(11).text_color(C.text).cursor_pointer().on_mouse_down("left", stop)
      .on_click(() => close()).child("Close ✕"));
}

// ---------------------------------------------------------------- crates

function field(label, el) {
  return v_flex().gap(4).child(div().text_size(11).text_color(C.muted).child(label)).child(el);
}

const inputLook = (el) => el.h(32).px(8).rounded(4).border_1().border_color(C.border2).bg(C.deep).text_size(13).text_color(C.text);

function crateForm(app, f) {
  const p = props();
  const pig = p.pig ? f.pigs.find((x) => x.address === p.pig) : null;
  const penId = p.pen ?? pig?.space ?? f.pens.find((x) => x.active)?.space ?? f.pens[0]?.space ?? null;
  const pen = f.pens.find((x) => x.space === penId);
  const title = app.ui.title.value().trim(), doneWhen = app.ui.done.value().trim();
  const ready = title && doneWhen;
  const make = async (then) => {
    if (!ready) return;
    const id = `c${Date.now().toString(36)}`;
    await app.store.command("crate.add", { crate: { id, title, doneWhen, text: app.ui.text.value(), size: app.ui.size, pen: penId, askedBy: "you" } });
    if (then) await then(id);
    close();
  };
  const sizeChip = (s, about) => div().id(`size-${s}`).px(10).py(5).rounded(3).cursor_pointer().text_size(12)
    .bg(app.ui.size === s ? "#1d3a2e" : C.raised2).border_1().border_color(app.ui.size === s ? C.accent : C.border2).text_color(app.ui.size === s ? C.accent : C.soft)
    .on_mouse_down("left", stop).on_click((_e, cx) => { app.ui.size = s; cx.notify(); }).child(`${s} · ${about}`);
  const actions = [button("cr-pin", "PIN TO BOARD", () => make(null))];
  if (pig) actions.push(button("cr-hand", `HAND TO ${pig.name.toUpperCase()} NOW`, () => make((id) => app.store.command("crate.hand", { id, to: pig.address })), { primary: true }));
  else if (pen?.line) actions.push(button("cr-belt", "PUT ON THE BELT", () => make((id) => app.store.command("crate.run", { id, line: pen.line.id })), { primary: true }));
  return v_flex().id("crate-form").size_full().bg(C.chrome).p(18).gap(12)
    .child(h_flex().items_center().child(div().flex_1().font_family(PIXEL).text_size(14).text_color(C.text).child("NEW CRATE")).child(kbd("Esc")))
    .child(field("What", inputLook(Input.new(app.ui.title))))
    .child(field("Done when (the Mandate)", inputLook(Input.new(app.ui.done))))
    .child(field("Details", Textarea.new(app.ui.text).h(110).px(8).py(6).rounded(4).border_1().border_color(C.border2).bg(C.deep).text_size(13).text_color(C.text)))
    .child(field("Size", h_flex().gap(6).child(sizeChip("one-shot", "straight to a pig")).child(sizeChip("large", "plan first"))))
    .child(div().text_size(12).text_color(C.muted).child(`Asked by you · pen ${pen?.name ?? "none"}${pig ? ` · for ${pig.name}` : ""}${pen?.line ? ` · belt: ${beltText(pen.line, f)}` : ""}`))
    .when(!ready, (el) => el.child(div().text_size(12).text_color(C.warn).child("A crate with no done-when cannot leave the board.")))
    .child(div().flex_1())
    .child(h_flex().gap(8).justify_end().children(actions));
}

export function beltText(line, f) {
  return line.stops.map((s) => s.kind === "pig" ? (f.pigs.find((p) => p.address === s.pig)?.name ?? s.pig) : { oven: "Oven", review: "Review", barn: "Barn" }[s.kind]).join(" → ");
}

function crateView(app, f) {
  const c = f.crates.find((x) => x.id === props().crate);
  if (!c) return v_flex().size_full().bg(C.chrome).p(18).child(div().text_color(C.muted).child("That crate is gone."));
  const pen = f.pens.find((p) => p.space === c.pen);
  const pig = c.at?.pig ? f.pigs.find((p) => p.address === c.at.pig) : null;
  return v_flex().id("crate-view").size_full().bg(C.chrome).p(18).gap(10).overflow_y_scroll()
    .child(h_flex().items_center().gap(10).child(div().font_family(PIXEL).text_size(11).text_color(C.muted).child(`#${c.id}`))
      .child(div().flex_1().text_size(15).text_color(C.text).child(c.title)).child(kbd("Esc")))
    .child(div().text_size(12).text_color(C.soft).child(`Mandate: asked by ${c.askedBy ?? "you"} · done when ${c.doneWhen}`))
    .child(div().text_size(12).text_color(c.state === "burnt" ? C.danger : C.muted).child(`${c.state}${c.burns ? ` · burnt x${c.burns}` : ""} · pen ${pen?.name ?? "?"}${pig ? ` · ${pig.name}` : ""}${c.tokens ? ` · ${fmtTokens(c.tokens)} tokens` : ""}`))
    .when(Boolean(c.text), (el) => el.child(div().text_size(12).text_color(C.soft).child(c.text)))
    .when(Boolean(c.log), (el) => el.child(sectionTitle("OVEN LOG", C.danger)).child(div().p(8).rounded(4).bg(C.deep).font_family("Menlo").text_size(11).text_color(C.soft).child(c.log)))
    .when(Boolean(c.report), (el) => el.child(sectionTitle("LAST HAND-OFF")).child(div().p(8).rounded(4).bg(C.deep).text_size(12).text_color(C.soft).child(c.report)))
    .child(sectionTitle("THIS CRATE'S LIFE", C.muted))
    .children((c.history ?? []).slice(-12).map((h) => h_flex().gap(10).text_size(12).child(div().w(50).text_color(C.muted).child(new Date(h.t).toTimeString().slice(0, 5))).child(div().text_color(C.text).child(h.what)).child(div().text_color(C.muted).truncate().child(h.detail ?? ""))))
    .child(h_flex().gap(8).pt(6)
      .when(Boolean(pig), (el) => el.child(button("cv-talk", `TALK TO ${pig.name.toUpperCase()}`, () => { close(); openTalk(pig.address); }, { primary: true })))
      .when(c.state === "burnt" && Boolean(c.line), (el) => el.child(button("cv-oven", "RUN THE OVEN AGAIN", () => { app.store.command("oven.run", { id: c.id }); close(); })))
      .when(c.state === "holding" || c.state === "barn", (el) => el.child(button("cv-review", "REVIEW", () => { close(); openReview(c.id); }, { primary: true }))));
}

// ---------------------------------------------------------------- pick a pig

function pickView(app, f) {
  const p = props();
  const from = p.from ? f.pigs.find((x) => x.address === p.from) : null;
  const pen = p.pen ?? from?.space;
  const pigs = f.pigs.filter((x) => x.address !== p.from).sort((a, b) => (a.space === pen ? -1 : 0) - (b.space === pen ? -1 : 0));
  const title = p.mode === "relay" ? `RELAY FROM ${(from?.name ?? "").toUpperCase()} TO…` : "HAND THE CRATE TO…";
  const choose = async (to) => {
    if (p.mode === "relay") {
      const pen = from?.space ?? to.space;
      if (!editLine(app, f, pen, (farm) => addRelay(farm, pen, p.from, to.address))) { close(); return; }
      notify({ title: "Belt built", body: `${from?.name ?? p.from} → ${to.name}: when ${from?.name ?? "it"} finishes, its hand-off rides to ${to.name}.`, level: "success", timeout: 3500 });
    } else {
      await app.store.command("crate.hand", { id: p.crate, to: to.address });
    }
    close();
  };
  return v_flex().id("pick").size_full().bg(C.chrome).p(16).gap(8)
    .child(h_flex().items_center().child(div().flex_1().font_family(PIXEL).text_size(12).text_color(C.text).child(title)).child(kbd("Esc")))
    .when(p.mode === "relay", (el) => el.child(div().text_size(12).text_color(C.muted).child("A belt carries each finished hand-off on to the next pig, as a new crate.")))
    .child(v_flex().id("pick-list").flex_1().min_h(0).gap(4).overflow_y_scroll().children(pigs.map((x) => h_flex().id(`pick-${x.address}`).gap(10).p(6).rounded(4).items_center().cursor_pointer()
      .bg(C.raised2).hover((s) => s.bg(C.raised)).on_mouse_down("left", stop).on_click(() => choose(x))
      .child(image(sprite(x.skin, "idle")).w(48).h(36))
      .child(v_flex().child(div().text_size(13).text_color(C.text).child(x.name)).child(div().text_size(11).text_color(C.muted).child(`${f.pens.find((q) => q.space === x.space)?.name ?? "no pen"} · ${statusWord(x.status, x.crate)}`))))))
    .when(!pigs.length, (el) => el.child(div().text_size(12).text_color(C.muted).child("No other pigs yet. Adopt one first (A).")));
}

// ---------------------------------------------------------------- Review Station

function crateFolder(app, f, c) {
  if (c.folder) return c.folder;
  const pig = c.at?.pig ? panes().find((p) => p.terminal === c.at.pig) : null;
  return pig?.cwd ?? f.pens.find((p) => p.space === c.pen)?.folder ?? null;
}

async function loadDiff(app) {
  const f = app.frame();
  const c = f.crates.find((x) => x.id === props().crate);
  if (!c) return;
  const dir = crateFolder(app, f, c);
  if (!dir) { app.ui.diff = { error: "no folder for this crate" }; return; }
  const stat = await exec("git", ["-C", dir, "status", "--short"]).catch((e) => ({ code: 1, stdout: "", stderr: String(e) }));
  const diff = await exec("git", ["-C", dir, "diff", "HEAD", "--stat"]).catch(() => ({ stdout: "" }));
  const body = await exec("git", ["-C", dir, "diff", "HEAD"]).catch(() => ({ stdout: "" }));
  // New files are part of the change too: git diff leaves them out, so diff each against nothing.
  const added = stat.stdout.split("\n").filter((l) => l.startsWith("?? ")).map((l) => l.slice(3).trim()).filter((p) => !p.endsWith("/")).slice(0, 8);
  let fresh = "";
  for (const p of added) {
    const r = await exec("git", ["-C", dir, "diff", "--no-index", "--", "/dev/null", p]).catch(() => ({ stdout: "" }));
    fresh += r.stdout;
  }
  const all = `${body.stdout}${fresh}`.split("\n");
  app.ui.diff = { dir, status: stat.stdout, stat: diff.stdout, body: all.slice(0, 220).join("\n") + (all.length > 220 ? `\n… ${all.length - 220} more lines (git diff in the pen shows the rest)` : ""), error: stat.code ? stat.stderr : null };
}

function reviewView(app, f) {
  const c = f.crates.find((x) => x.id === props().crate);
  if (!c) return v_flex().size_full().bg(C.chrome).p(18).child(div().text_color(C.muted).child("That crate is gone."));
  const pen = f.pens.find((p) => p.space === c.pen);
  const d = app.ui.diff;
  const ovenGreen = (c.history ?? []).some((h) => /oven: green/i.test(h.what));
  const ovenBurnt = (c.history ?? []).filter((h) => /oven: burnt/i.test(h.what)).length;
  const hasOven = Boolean(pen?.line?.stops?.some((s) => s.kind === "oven"));
  const gate = (name, ok, text) => h_flex().gap(8).text_size(12).child(div().w(14).font_family(PIXEL).text_color(ok === true ? C.accent : ok === false ? C.danger : C.muted).child(ok === true ? "✓" : ok === false ? "x" : "–")).child(div().w(90).text_color(C.text).child(name)).child(div().text_color(C.muted).child(text));
  const atBarn = c.state === "barn";
  return v_flex().id("review").size_full().bg(C.chrome).p(18).gap(10)
    .child(h_flex().items_center().gap(10).child(div().font_family(PIXEL).text_size(14).text_color(C.reviewText).child(atBarn ? "THE BARN" : "REVIEW STATION"))
      .child(div().text_size(12).text_color(C.muted).child(`${pen?.name ?? ""} · ${c.state} ${fmtAge(Date.now() - (c.updated ?? c.created))}`)).child(div().flex_1()).child(kbd("Esc")))
    .child(h_flex().flex_1().min_h(0).gap(14)
      .child(v_flex().w("42%").min_w(320).h_full().gap(10).overflow_y_scroll()
        .child(div().text_size(15).text_color(C.text).child(c.title))
        .child(div().text_size(12).text_color(C.soft).child(`Mandate: asked by ${c.askedBy ?? "you"} · done when ${c.doneWhen}${c.tokens ? ` · ${fmtTokens(c.tokens)} tokens` : ""}`))
        .child(sectionTitle("GATES PASSED"))
        .child(gate("Oven", hasOven ? (ovenGreen ? true : false) : null, hasOven ? (ovenGreen ? `green${ovenBurnt ? ` after ${ovenBurnt} burn${ovenBurnt > 1 ? "s" : ""}` : ""}` : "not run: a skipped check is burnt") : "no Oven on this belt"))
        .child(gate("Hand-off", Boolean(c.report), c.report ? "written" : "missing"))
        .child(sectionTitle("EVIDENCE · THE LAST HAND-OFF"))
        .child(div().p(8).rounded(4).bg(C.deep).text_size(12).text_color(C.soft).child(c.report || "No report."))
        .when(Boolean(c.log), (el) => el.child(sectionTitle("OVEN LOG")).child(div().p(8).rounded(4).bg(C.deep).font_family("Menlo").text_size(11).text_color(C.soft).child(c.log))))
      .child(v_flex().flex_1().min_w(0).h_full().gap(6)
        .child(sectionTitle(`DIFF · READ LAST${d?.dir ? ` · ${d.dir}` : ""}`))
        .child(div().id("diff").flex_1().min_h(0).p(8).rounded(4).bg(C.deep).overflow_y_scroll().font_family("Menlo").text_size(11).text_color(C.soft)
          .child(d ? (d.error ? `git: ${d.error}` : [d.status && `status:\n${d.status}`, d.stat, d.body].filter(Boolean).join("\n") || "No changes.") : "Reading the diff…"))))
    .child(h_flex().gap(8).items_center()
      .child(Input.new(app.ui.note).flex_1().h(32).px(8).rounded(4).border_1().border_color(C.border2).bg(C.deep).text_size(12).text_color(C.text))
      .when(!atBarn, (el) => el.child(button("rv-back", "SEND BACK WITH THE NOTE", () => { app.store.command("review.sendback", { id: c.id, note: app.ui.note.value() }); close(); })))
      .child(atBarn
        ? button("rv-ship", "SHIP: COMMIT, PUSH, PR", () => { app.store.command("barn.ship", { id: c.id }); close(); }, { primary: true })
        : d?.error || (d && !d.dir)
          ? div().text_size(12).text_color(C.warn).child("No diff to read: cannot approve")
          : button("rv-ok", c.line ? "APPROVE · TO THE BARN" : "ACCEPT THE WORK", () => { app.store.command("review.approve", { id: c.id }); close(); }, { primary: true })))
    .child(div().text_size(11).text_color(C.muted).child("Approve sends the crate on to the Barn, which commits and, when the repo has a remote, pushes and opens a pull request. Send back returns it to the last pig with your note."));
}

// ---------------------------------------------------------------- find

function findItems(app) {
  const f = app.frame();
  const items = [
    ...f.pigs.map((p) => ({ label: `${p.name} · ${f.pens.find((q) => q.space === p.space)?.name ?? "stray"} · ${statusWord(p.status, p.crate)}`, kind: "pig", go: () => { close(); openTalk(p.address); } })),
    ...f.pens.map((p) => ({ label: `${p.name} · pen · ${p.pigs.length} pigs`, kind: "pen", go: () => { close(); setShared("pigtorio.walkPen", p.space); setView("walk"); } })),
  ];
  return filterFind(items, app.ui.query?.value() ?? "");
}

function helpView() {
  return v_flex().id("help").size_full().bg(C.chrome).p(16).gap(8)
    .child(h_flex().items_center().child(div().flex_1().font_family(PIXEL).text_size(14).text_color(C.text).child("PIGPEDIA")).child(kbd("Esc")))
    .child(sectionTitle("KEYS"))
    .child(div().text_size(11).text_color(C.muted).child("Hold ⌘⇧ with a letter from anywhere in this layout (installed with pigtorio install --keys; Walk is ⌘⇧G). The bare letters work while the farm holds the keyboard."))
    .child(v_flex().gap(4).children(HELP_KEYS.map((r) => h_flex().items_center().gap(10)
      .child(h_flex().w(170).gap(3).children(r.keys.map((k) => kbd(k))))
      .child(div().flex_1().text_size(12).text_color(C.soft).child(r.does)))))
    .child(sectionTitle("WHAT EACH VERB RUNS"))
    .child(v_flex().gap(4).children(HELP_VERBS.map((r) => h_flex().gap(10)
      .child(div().w(170).font_family(PIXEL).text_size(11).text_color(C.accent).child(r.verb.toUpperCase()))
      .child(div().flex_1().text_size(12).text_color(C.soft).child(r.runs)))));
}

function findView(app, f) {
  const items = findItems(app).slice(0, 12);
  return v_flex().id("find").size_full().bg(C.chrome).p(16).gap(8)
    .child(inputLook(Input.new(app.ui.query)))
    .child(v_flex().gap(2).children(items.map((it, i) => h_flex().id(`find-${i}`).px(8).py(6).rounded(3).gap(8).cursor_pointer()
      .when(i === 0, (el) => el.bg(C.raised)).hover((s) => s.bg(C.raised))
      .on_mouse_down("left", stop).on_click(() => it.go())
      .child(div().w(30).font_family(PIXEL).text_size(9).text_color(it.kind === "pig" ? C.accent : C.warn).child(it.kind.toUpperCase()))
      .child(div().text_size(13).text_color(C.text).child(it.label)))))
    .child(div().text_size(11).text_color(C.muted).child("Enter goes to the first · Esc closes"));
}
