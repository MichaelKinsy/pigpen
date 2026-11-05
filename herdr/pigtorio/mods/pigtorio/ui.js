// Pigtorio's look: the storyboard's palette and small parts every view uses.
import { div, image } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";

export const ART = "mods/pigtorio/art";
export const PIXEL = "Silkscreen";

export const C = {
  chrome: "#0f1512", raised: "#1a2620", raised2: "#182420", deep: "#141c18", border: "#223129", border2: "#2b3d33",
  text: "#e6ece8", muted: "#8fa39a", soft: "#aab8b0", accent: "#4ed1a1", accentSoft: "#8fe8c4", warn: "#e3a63b", danger: "#e05a4f",
  grass: "#2f6b45", grassLine: "#00000010", penFill: "#35804f", penWood: "#9a6a3a", penDark: "#5e3f22",
  river: "#2c5f86", bridge: "#8b5a2b", tagBg: "#0f1512", pigTag: "#0f15128c",
  barn: "#8f2f2a", barnDark: "#4f1a17", roof: "#5e3f22", sign: "#8b5a2b", signText: "#f3e3c3",
  oven: "#5a2a1a", ovenDark: "#2e1510", ovenText: "#ffb347", review: "#1f3d5a", reviewDark: "#10202f", reviewText: "#9ec9ee",
  crate: "#8a5a3a", cream: "#f6f0e6", ink: "#2a2318",
};

export const STATUS_COLOR = { blocked: C.danger, burnt: C.danger, working: C.accent, done: C.warn, holding: C.warn, napping: C.muted, idle: C.muted };

export const sprite = (skin, state) => `${ART}/sprites/${skin}/${state}.webp`;

export function pixelText(text, size = 11, color = C.text) {
  return div().font_family(PIXEL).text_size(size).text_color(color).whitespace_nowrap().child(text);
}

export function tag(text, opts = {}) {
  return div().px(opts.px ?? 8).py(3).rounded(3).bg(opts.bg ?? C.tagBg).when(opts.pixel !== false, (el) => el.font_family(PIXEL))
    .text_size(opts.size ?? 11).text_color(opts.color ?? C.text).whitespace_nowrap().child(text);
}

export function kbd(key) {
  return div().px(6).py(1).rounded(3).bg(C.raised).font_family(PIXEL).text_size(11).text_color(C.text).child(key);
}

export function button(id, label, onClick, opts = {}) {
  const primary = opts.primary, danger = opts.danger;
  const bg = primary ? C.accent : danger ? C.danger : C.raised;
  const fg = primary ? C.chrome : C.text;
  return h_flex().id(id).h(opts.h ?? 32).px(opts.px ?? 14).items_center().justify_center().rounded(4)
    .border_1().border_color(primary ? C.accent : danger ? C.danger : C.border2).bg(bg)
    .font_family(PIXEL).text_size(opts.size ?? 11).text_color(fg).cursor_pointer()
    .hover((s) => s.opacity(0.85))
    .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
    .on_click((e, cx) => { cx.stop_propagation(); onClick(e, cx); })
    .child(label);
}

export function panel(title) {
  return v_flex().bg(C.chrome).border_1().border_color(C.border2).rounded(6).p(14).gap(8)
    .child(div().font_family(PIXEL).text_size(11).text_color(C.accent).child(title));
}

export function sectionTitle(text, color = C.accent) {
  return div().font_family(PIXEL).text_size(11).text_color(color).child(text);
}

export function logo(size = 22) {
  return image(`${ART}/ui/logo.png`).w(size).h(Math.round(size * 13 / 14)).flex_none();
}
