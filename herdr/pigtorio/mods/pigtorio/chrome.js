// The top bar and the footer.
import { div, image } from "gpui-kit";
import { h_flex } from "gpui-base";
import { window as appWindow, shared, setShared } from "herdr";
import { C, PIXEL, logo, kbd } from "./ui.js";
import { view, calm, setView } from "./nav.js";
import { openFind, openHelp, ringBell } from "./dialogs.js";
import { BELL_MARK } from "./model.js";

const TABS = [
  { id: "map", label: "Map", key: "M" },
  { id: "walk", label: "Walk", key: "W" },
  { id: "terminals", label: "Terminals", key: "T" },
  { id: "board", label: "Job Board", key: "J" },
  { id: "adopt", label: "Adopt-a-Pig", key: "A" },
  { id: "workshop", label: "Workshop", key: "K" },
  { id: "report", label: "Report", key: "R" },
];

export function renderBar(app, f) {
  const current = shared("pigtorio.terminals") === true ? "terminals" : view();
  const mac = appWindow()?.platform === "macos";
  const first = f.bell[0];
  const bellText = first ? `Bell: ${first.label}` : "Bell: all quiet";
  const stop = (_e, cx) => cx.stop_propagation();
  return h_flex().id("bar").size_full().bg(C.chrome).border_b_1().border_color(C.border).items_center().gap(18)
    .pl(mac ? 86 : 16).pr(16).text_size(13).text_color(C.soft)
    .child(h_flex().items_center().gap(8).child(logo(22))
      .child(div().font_family(PIXEL).text_size(13).text_color(C.text).child("PIGTORIO")))
    .child(h_flex().gap(4).children(TABS.map((t) => h_flex().id(`tab-${t.id}`).px(10).py(6).rounded(4)
      .when(current === t.id, (el) => el.bg(C.raised).text_color(C.text))
      .cursor_pointer().hover((s) => s.text_color(C.text))
      .on_mouse_down("left", stop)
      .on_click(() => setView(t.id))
      .child(t.label))))
    .child(div().flex_1())
    .child(h_flex().id("bell").items_center().gap(6).cursor_pointer().on_mouse_down("left", stop)
      .on_click((_e, cx) => ringBell(app, f))
      .child(kbd("⌘⇧B"))
      .child(div().text_color(first?.kind === "blocked" || first?.kind === "burnt" ? C.danger : first ? C.warn : C.soft)
        .child(first ? `${BELL_MARK[first.kind]} ${bellText}` : bellText)))
    .child(h_flex().id("find").items_center().gap(6).cursor_pointer().on_mouse_down("left", stop)
      .on_click(() => openFind()).child(kbd("⌘⇧F")).child("Find pig or pen"))
    .child(h_flex().id("help").items_center().gap(6).cursor_pointer().on_mouse_down("left", stop)
      .on_click(() => openHelp()).child(kbd("⌘⇧H")).child("Help"))
    .child(div().id("calm").cursor_pointer().on_mouse_down("left", stop)
      .on_click(() => setShared("pigtorio.calm", !calm()))
      .text_color(calm() ? C.accent : C.muted).child(`Calm: ${calm() ? "on" : "off"}`));
}

export function renderFooter(app, f) {
  const machineCount = new Set(f.pens.map((p) => p.machine ?? "")).size || 1;
  const strays = f.pigs.filter((p) => p.stray).length;
  const working = f.pigs.filter((p) => p.status === "working").length;
  const blocked = f.pigs.filter((p) => p.status === "blocked").length;
  const sep = () => div().text_color(C.border2).child("·");
  return h_flex().id("footer").size_full().bg(C.chrome).border_t_1().border_color(C.border).items_center().gap(12).px(16)
    .text_size(12).text_color(C.muted)
    .child(div().child(`${f.pens.length} pens · ${machineCount} machine${machineCount === 1 ? "" : "s"}`))
    .child(sep())
    .child(div().child(`${f.pigs.length - strays} pigs${strays ? ` + ${strays} stray` : ""} · ${working} working${blocked ? ` · ${blocked} blocked` : ""}`))
    .child(sep())
    .child(div().text_color(f.engine ? C.accent : C.warn).child(f.engine ? "engine running" : "engine starting…"))
    .child(div().flex_1())
    .child(div().child("Click a pig: Talk · Hand crate · Relay to · Peek · Nap · Retire"));
}
