// Which view the farm shows, shared by every copy (bar, farm, footer, popups).
import { shared, setShared, run } from "herdr";

export const VIEWS = ["map", "walk", "board", "adopt", "workshop", "report"];
export const view = () => (VIEWS.includes(shared("pigtorio.view")) ? shared("pigtorio.view") : "map");
export const calm = () => shared("pigtorio.calm") === true;

// Terminals is the layout's other stack child: Herdr's own tab and panes.
export function setView(v) {
  if (v === "terminals") {
    run("layout.select", { stack: "main", child: "terminals" });
    setShared("pigtorio.terminals", true);
    return;
  }
  run("layout.select", { stack: "main", child: "farm" });
  if (shared("pigtorio.terminals") === true) setShared("pigtorio.terminals", false);
  if (shared("pigtorio.view") !== v) setShared("pigtorio.view", v);
}
