// Pigtorio: Factorio, but every worker is a coding agent.
//
// One mod, many roles (props().role):
//   bar      the top bar: views, the Bell, find, calm
//   farm     the play area: Map, Walk, Job Board, Adopt-a-Pig, Workshop, Report
//   footer   the status line
//   menu     a pig's speech-bubble menu (popup)
//   talkbar  the title bar of a Talk window (a pig's live terminal over the farm)
//   crate    write a crate (dialog)
//   pick     pick a pig: relay to, hand a crate to (popup)
//   review   the Review Station card for one crate (dialog)
//   find     find a pig or pen (dialog)
// The engine (`pigtorio engine`) owns crates and belts; see ARCHITECTURE.md.
import { View, div } from "gpui-kit";

import { exec, props, nextCommand, setAgentOrder, knownAgents, shared, notify } from "herdr";
import { Store, dataDir } from "./store.js";
import { bellOrder } from "./model.js";
import { C } from "./ui.js";
import { renderBar, renderFooter } from "./chrome.js";
import { renderFarm, farmKey, farmCommand, claimPending, farmInit } from "./views.js";
import { renderDialog, dialogInit, dialogKey } from "./dialogs.js";

export default class Pigtorio extends View {
  init(_props, cx) {
    this.role = props()?.role ?? "farm";
    this.store = new Store();
    this.size = { width: 1200, height: 760 };
    this.handle = cx.focus_handle();
    this.ui = {}; // per-view state (selection, walking, forms)
    cx.spawn(async (cx) => {
      if (this.role === "farm") await exec("mkdir", ["-p", `${dataDir()}/inbox`, `${dataDir()}/state`, `${dataDir()}/sessions`]).catch(() => null);
      await this.store.load();
      this.store.freshRefusals(); // old refusals are not news
      cx.notify();
      if (this.role === "farm") {
        await this.store.ensureEngine();
        cx.timer.every(30_000, (cx) => { this.store.ensureEngine(); cx.notify(); });
      }
    });
    this.store.watch(cx, (cx) => {
      // The farm copy tells the person when the engine refused something they asked for.
      if (this.role === "farm") for (const a of this.store.freshRefusals()) notify({ title: "Pigtorio could not do that", body: a.error ?? a.op, level: "warning", timeout: 6000 });
      cx.notify();
    });
    if (this.role === "farm") {
      farmInit(this);
      cx.spawn(async (cx) => {
        for (;;) {
          const c = await nextCommand();
          if (c === "focus") { this.handle.focus(); continue; }
          farmCommand(this, c, cx);
          cx.notify();
        }
      });
      // Ages in tags and the Bell's "stuck" need the clock, once a minute.
      cx.timer.every(60_000, (cx) => cx.notify());
      cx.spawn(async (cx) => { this.ui.agentsList = await knownAgents().catch(() => []); cx.notify(); });
      // A pig adopted a moment ago gets its name, skin and role once its terminal shows up;
      // new spaces get pens and closed ones lose theirs (cheap when nothing changed).
      cx.timer.every(2000, (cx) => {
        cx.spawn(async (cx) => {
          if (Object.keys(this.store.farm.pending ?? {}).length) await claimPending(this);
          if (await this.store.settleWorld(this.store.pigs())) cx.notify();
        });
      });
    }
    // While Terminals shows, the farm copy is hidden and gets no commands; the bar takes them.
    if (this.role === "bar") {
      cx.spawn(async (cx) => {
        for (;;) {
          const c = await nextCommand();
          if (shared("pigtorio.terminals") === true && c !== "focus") farmCommand(this, c, cx);
          cx.notify();
        }
      });
    }
    if (!["bar", "farm", "footer"].includes(this.role)) dialogInit(this, cx);
  }

  // Everything a frame needs, computed once per render.
  frame() {
    const now = Date.now();
    const pigs = this.store.pigs(now);
    const pens = this.store.pens(pigs);
    const crates = this.store.state.crates ?? [];
    const beltPigs = new Set((this.store.farm.lines ?? []).flatMap((l) => l.stops.filter((s) => s.kind === "pig").map((s) => s.pig)));
    const bell = bellOrder(pigs, crates, now, beltPigs);
    return { now, pigs, pens, crates, bell, farm: this.store.farm, engine: this.store.engineAlive(now) };
  }

  render(cx) {
    const f = this.frame();
    if (this.role === "bar") return renderBar(this, f);
    if (this.role === "footer") return renderFooter(this, f);
    if (this.role === "farm") {
      setAgentOrder([...new Set(f.bell.filter((b) => b.pig).map((b) => b.pig).concat(f.pigs.map((p) => p.address)))]);
      return div().id("root").size_full().bg(C.grass).track_focus(this.handle)
        .on_key_down((e, cx) => farmKey(this, e, cx))
        .on_size_change((s, cx) => { this.size = s; cx.notify(); })
        .child(renderFarm(this, f, cx));
    }
    return div().id("dialog-root").size_full().track_focus(this.handle)
      .on_key_down((e, cx) => dialogKey(this, e, cx))
      .child(renderDialog(this, f, cx));
  }
}
