// The Runt: Pigtorio's foreman. A panel with scripted hints (free), the current
// draft of ghosts with the check and Build / Discard, and a door to the real
// PiG agent that drafts for you. Pure logic lives in runtlogic.js.
import { div, image } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { props, closeOverlay, openOverlay, exec, hc, notify, setShared, readTextFile, writeTextFile, watchFolder, appHome } from "herdr";
import { C, PIXEL, ART, tag, button, sectionTitle, kbd } from "./ui.js";
import { dataDir } from "./store.js";
import { setView } from "./nav.js";
import { openCrate, openReview, openTalk, openPenMenu } from "./dialogs.js";
import { GHOST_FILE, RUNT_BRIEF, appendGhosts, checkDraft, emptyDraft, parseGhosts, resolveGhosts, runtHints } from "./runtlogic.js";

const RUNT_NAME = "pigtorio-runt";
const ghostPath = () => `${dataDir()}/${GHOST_FILE}`;
const close = () => { const id = props()?.overlay; if (id) closeOverlay(id); };

export function openRunt() {
  openOverlay({ mod: "pigtorio", kind: "window", key: "runt", width: 460, height: 560, props: { role: "runt" } });
}

// ---------------------------------------------------------------- the draft file

async function loadGhosts(app) {
  const text = await readTextFile(ghostPath());
  app.ui.runt.text = text;
  app.ui.runt.ghosts = parseGhosts(text);
}

// Writes the draft with an expected snapshot; on a stale snapshot re-reads and tries again.
async function changeDraft(app, change) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const text = await readTextFile(ghostPath());
    const next = change(parseGhosts(text));
    try {
      await writeTextFile(ghostPath(), JSON.stringify(next, null, 2), text);
      return true;
    } catch { /* someone wrote first: read again */ }
  }
  return false;
}

const draftAction = (app, a) => changeDraft(app, (g) => appendGhosts(g, a.ops, a.note)).then(async (ok) => {
  if (!ok) notify({ title: "The Runt could not draft that", body: "The draft file changed under it; try again.", level: "warning", timeout: 4000 });
  await loadGhosts(app);
});

const discard = async (app) => {
  await changeDraft(app, () => emptyDraft());
  await loadGhosts(app);
};

// ---------------------------------------------------------------- init

export function runtInit(app, cx) {
  app.ui.runt = { ghosts: null, text: null, art: false };
  if (props()?.mode === "bar") return;
  cx.spawn(async (cx) => {
    await app.store.load();
    await loadGhosts(app);
    const probe = await exec("test", ["-f", `${appHome()}/${ART}/runt/talk.webp`]).catch(() => null);
    app.ui.runt.art = probe?.code === 0;
    cx.notify();
  });
  // The Store watches farm.json and state.json only; ghosts.json is ours.
  cx.spawn(async (cx) => {
    for (;;) {
      const paths = await watchFolder(dataDir());
      if (paths.some((p) => p === dataDir() || p.endsWith(GHOST_FILE))) {
        await loadGhosts(app);
        cx.notify();
      }
    }
  });
}

// ---------------------------------------------------------------- the agent

async function liveRunt() {
  const r = await hc(["ls", "--json"]).catch(() => null);
  try { return (JSON.parse(r?.stdout ?? "{}").terminals ?? []).find((t) => t.name === RUNT_NAME && t.state === "live") ?? null; } catch { return null; }
}

// Starts the Runt's PiG agent once (a live core terminal named pigtorio-runt) and opens it.
async function askRunt() {
  let t = await liveRunt();
  if (!t) {
    const D = dataDir();
    await hc(["run", "--bg", "--no-parent", "--name", RUNT_NAME, "--cwd", D, "--", "pig", "--tools", "read,bash,grep,find,ls", "--session-dir", `${D}/sessions/runt`, "--append-system-prompt", RUNT_BRIEF(D)]).catch((e) => {
      notify({ title: "The Runt did not start", body: String(e?.message ?? e), level: "error", timeout: 6000 });
    });
    for (let i = 0; i < 10 && !t; i++) t = await liveRunt();
  }
  if (!t) return;
  openOverlay({
    kind: "window", key: "runt-talk", width: 0.62, height: 0.62,
    layout: { type: "column", children: [
      { type: "block", size: { px: 34 }, content: { mod: "pigtorio", props: { role: "runt", mode: "bar" } } },
      { type: "block", content: { terminal: t.address } },
    ] },
  });
}

// ---------------------------------------------------------------- actions

function runHint(app, f, a) {
  if (a.type === "ghost") return draftAction(app, a);
  if (a.type === "putcrate") return app.store.command("crate.run", { id: a.crate, line: a.line });
  close();
  if (a.type === "view") return setView(a.view);
  if (a.type === "adopt") { if (a.pen) setShared("pigtorio.adoptPen", a.pen); return setView("adopt"); }
  if (a.type === "talk") return openTalk(a.pig);
  if (a.type === "penmenu") return openPenMenu(a.pen, { x: 300, y: 200 });
  if (a.type === "crate") return openCrate({ crate: a.crate });
  if (a.type === "review") return openReview(a.crate);
}

async function build(app, f, draft) {
  const pigSpace = (a) => f.pigs.find((p) => p.address === a)?.space ?? null;
  const check = checkDraft(app.store.farm, draft, pigSpace, f.pigs);
  if (!check.ok) return;
  let failed = null;
  const ok = await app.store.updateFarm((farm) => {
    const r = resolveGhosts(farm, draft, pigSpace, f.pigs);
    if (r.errors.length) { failed = r.errors; return farm; }
    return r.farm;
  });
  if (!ok || failed) {
    notify({ title: "The Runt could not build that", body: failed?.join("; ") ?? "The farm changed; check the draft again.", level: "warning", timeout: 6000 });
    return;
  }
  for (const c of check.result.crates) await app.store.command(c.op, c.fields);
  await discard(app);
  notify({ title: "Built", body: "The draft is on the farm.", level: "success", timeout: 3000 });
}

// ---------------------------------------------------------------- views

function title(extra) {
  return h_flex().h(40).flex_none().px(12).gap(10).items_center().bg(C.raised).border_b_1().border_color(C.border2)
    .child(div().font_family(PIXEL).text_size(12).text_color(C.accent).child("THE RUNT"))
    .child(div().flex_1().text_size(11).text_color(C.muted).truncate().child(extra))
    .child(kbd("Esc"))
    .child(button("runt-close", "CLOSE ✕", () => close(), { h: 26, px: 10, size: 10 }));
}

export function renderRunt(app, f) {
  if (props()?.mode === "bar") {
    return h_flex().id("runt-bar").size_full().bg(C.raised).border_b_1().border_color(C.border2).px(10).gap(10).items_center()
      .child(image(`${ART}/sprites/piglet/idle.webp`).w(32).h(24))
      .child(div().text_size(12).text_color(C.text).child("The Runt · read-only · drafts only"))
      .child(div().flex_1())
      .child(div().text_size(11).text_color(C.muted).child("Press BUILD in the Runt panel to build a draft"));
  }
  const r = app.ui.runt ?? {};
  const pigSpace = (a) => f.pigs.find((p) => p.address === a)?.space ?? null;
  const draft = r.ghosts;
  const hints = runtHints(f.farm, f.pigs, f.pens, f.crates, draft);
  const check = draft ? checkDraft(f.farm, draft, pigSpace, f.pigs) : null;
  const pigName = (ref) => f.pigs.find((p) => `pig:${p.address}` === ref)?.name ?? ref;
  const describe = (g) => {
    if (g.op === "place") return `${g.id} · ${g.kind}${Number.isFinite(g.x) ? ` at ${g.x},${g.y}` : g.near ? ` near ${pigName(g.near)}` : ""}`;
    if (g.op === "belt") return `${g.id} · belt ${pigName(g.from)} → ${pigName(g.to)}`;
    if (g.op === "crate") return `${g.id} · crate "${g.crate?.title ?? ""}"`;
    return `${g.id ?? ""} · ${g.op}`;
  };
  const sprite = r.art ? `${ART}/runt/talk.webp` : `${ART}/sprites/piglet/idle.webp`;

  const hintRow = (h) => v_flex().gap(4).p(10).rounded(6).bg(C.raised).border_1().border_color(C.border2)
    .child(div().text_size(13).text_color(C.text).child(h.text))
    .child(div().text_size(11).text_color(C.muted).child(h.why))
    .when(Boolean(h.action), (el) => el.child(h_flex().pt(4).child(button(`hint-${h.id}`, h.action.label ?? "DO IT", () => runHint(app, f, h.action), { primary: true, h: 28, size: 10 }))));

  return v_flex().id("runt").size_full().bg(C.chrome)
    .child(title(draft ? "ghosts drafted" : "read-only · drafts only"))
    .child(v_flex().id("runt-body").flex_1().min_h(0).p(14).gap(12).overflow_y_scroll()
      .child(h_flex().gap(12).items_center()
        .child(image(sprite).w(64).h(48).flex_none())
        .child(v_flex().gap(3)
          .child(div().text_size(14).text_color(C.text).child(hints.length ? "Here is what I would do next." : "The farm looks tidy."))
          .child(div().text_size(11).text_color(C.muted).child("These hints are free. Ask me when you want a plan."))))
      .when(hints.length > 0, (el) => el.child(sectionTitle("NEXT")).children(hints.map(hintRow)))
      .when(Boolean(draft), (el) => el
        .child(sectionTitle("THE DRAFT"))
        .when(Boolean(draft.note), (e) => e.child(div().text_size(12).text_color(C.soft).child(draft.note)))
        .child(v_flex().gap(3).p(10).rounded(6).bg(C.deep).children(draft.ghosts.map((g) => div().text_size(12).text_color(C.soft).child(describe(g)))))
        .child(check?.ok
          ? tag("Check passed: every belt reaches a Review Station or the Barn", { color: C.accentSoft, pixel: false, size: 12 })
          : v_flex().gap(3).children((check?.problems ?? []).map((p) => div().text_size(12).text_color(C.danger).child(`Check failed: ${p}`))))
        .child(h_flex().gap(8)
          .child(button("runt-build", "BUILD", () => build(app, f, draft), { primary: check?.ok, h: 32 }))
          .child(button("runt-discard", "DISCARD", () => discard(app), { danger: true, h: 32 }))))
      .child(div().flex_1())
      .child(h_flex().gap(8).items_center()
        .child(button("runt-ask", "ASK THE RUNT", () => askRunt(), { primary: true, h: 34 }))
        .child(div().flex_1().text_size(11).text_color(C.muted).child("A PiG agent that can read your repo and draft ghosts. It cannot build."))));
}
