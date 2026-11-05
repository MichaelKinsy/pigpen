import { describe, expect, test } from "bun:test";
import { readFileSync, readdirSync } from "node:fs";
import {
  ALL_SKINS, COLOR_SKINS, SKIN_NAMES, addRelay, addStation, bellOrder, filterFind, flowPens, fmtAge, penSize, lineBusy,
  pigName, report, skinFor, HELP_KEYS, HELP_VERBS, VIEW_KEYS, isHelpKey, spriteState, statusWord, activeCrateFor, boardCrates,
} from "../mods/pigtorio/model.js";

const art = new URL("../mods/pigtorio/art/", import.meta.url).pathname;

describe("skins", () => {
  test("every skin has a name and every state it can show has art", () => {
    const states = new Set(["idle", "asleep", "running", "editing", "blocked", "done", "failed", "completing"]);
    for (const s of ALL_SKINS) {
      expect(SKIN_NAMES[s]).toBeTruthy();
      const files = new Set(readdirSync(`${art}sprites/${s}`));
      for (const st of states) expect(files.has(`${st}.webp`)).toBe(true);
    }
  });
  test("a pig keeps its skin and default skins are colours, not characters", () => {
    expect(skinFor("t7", {})).toBe(skinFor("t7", {}));
    for (let i = 0; i < 40; i++) expect(COLOR_SKINS).toContain(skinFor(`t${i}`, {}));
  });
  test("a chosen skin and name win; an unknown skin falls back", () => {
    const farm = { pigs: { t1: { skin: "kratos", name: "Big K" }, t2: { skin: "nope" } } };
    expect(skinFor("t1", farm)).toBe("kratos");
    expect(pigName("t1", farm)).toBe("Big K");
    expect(COLOR_SKINS).toContain(skinFor("t2", farm));
    expect(pigName("t2", farm)).toBe(SKIN_NAMES[skinFor("t2", farm)]);
  });
});

describe("what a pig shows", () => {
  test("burnt beats every status; blocked beats work", () => {
    expect(spriteState("working", { state: "burnt" })).toBe("failed");
    expect(spriteState("blocked", null)).toBe("blocked");
    expect(statusWord("blocked", { state: "working" })).toBe("blocked");
  });
  test("idle naps, working digs, a crate holding at review shows completing", () => {
    expect(spriteState("idle", null)).toBe("asleep");
    expect(spriteState("working", null)).toBe("running");
    expect(spriteState("idle", { state: "holding" })).toBe("completing");
    expect(statusWord("idle", null)).toBe("napping");
  });
});

describe("the Bell", () => {
  const now = 10 * 3600_000;
  test("blocked, burnt, holding, done, stuck: in that order, oldest first", () => {
    const pigs = [
      { address: "a", name: "A", status: "done", since: 5 },
      { address: "b", name: "B", status: "blocked", since: 9 },
      { address: "c", name: "C", status: "working", since: now - 3 * 3600_000 },
      { address: "d", name: "D", status: "blocked", since: 2 },
      { address: "e", name: "E", status: "working", since: now - 60_000 },
      { address: "f", name: "F", status: "idle", since: 1 },
    ];
    const crates = [{ id: "c1", title: "x", state: "holding", created: 1 }, { id: "c2", title: "y", state: "burnt", burns: 2, created: 3 }, { id: "c3", title: "z", state: "board", created: 1 }];
    const order = bellOrder(pigs, crates, now).map((b) => b.pig ?? b.crate);
    expect(order).toEqual(["d", "b", "c2", "c1", "a", "c"]);
  });
  test("a belt pig's done is the belt's news, not the Bell's", () => {
    const pigs = [{ address: "a", name: "A", status: "done", since: 1 }, { address: "b", name: "B", status: "done", since: 2 }];
    expect(bellOrder(pigs, [], now, new Set(["a"])).map((b) => b.pig)).toEqual(["b"]);
  });
  test("an empty farm rings nothing", () => {
    expect(bellOrder([], [], now)).toEqual([]);
  });
});

describe("pens", () => {
  test("a pen grows with its pigs and never below the minimum", () => {
    const one = penSize(1, false, 2000), four = penSize(4, false, 2000);
    expect(four.w).toBeGreaterThan(one.w);
    expect(penSize(0, false, 2000).w).toBe(one.w);
    expect(penSize(1, true, 2000).h).toBeGreaterThan(one.h);
  });
  test("pens wrap to a new row instead of leaving the region", () => {
    const pens = Array.from({ length: 5 }, (_, i) => ({ space: `s${i}`, pigs: [1, 2, 3].map((n) => ({ address: `${i}-${n}` })), line: null }));
    const { pens: out } = flowPens(pens, { x: 0, y: 0, w: 1000 });
    for (const p of out) expect(p.x + p.w).toBeLessThanOrEqual(1000);
    expect(new Set(out.map((p) => p.y)).size).toBeGreaterThan(1);
  });
});

describe("belts", () => {
  test("a relay builds a line once and keeps pig order", () => {
    let farm = addRelay({ lines: [] }, "sp", "t1", "t2");
    farm = addRelay(farm, "sp", "t2", "t3");
    farm = addRelay(farm, "sp", "t1", "t2");
    expect(farm.lines).toHaveLength(1);
    expect(farm.lines[0].stops.map((s) => s.pig)).toEqual(["t1", "t2", "t3"]);
  });
  test("a pig relayed onto a line with stations lands before the Oven", () => {
    let farm = addStation(addStation({ lines: [] }, "sp", "oven"), "sp", "barn");
    farm = addRelay(farm, "sp", "t1", "t2");
    expect(farm.lines[0].stops.map((s) => s.pig ?? s.kind)).toEqual(["t1", "t2", "oven", "barn"]);
  });
  test("a line with a crate on it is busy; shipped and board crates don't count", () => {
    const farm = { lines: [{ id: "l1", pen: "sp", stops: [] }] };
    expect(lineBusy(farm, [{ line: "l1", state: "holding" }], "sp")).toBe(true);
    expect(lineBusy(farm, [{ line: "l1", state: "shipped" }, { line: "l1", state: "board" }], "sp")).toBe(false);
    expect(lineBusy(farm, [{ line: "l1", state: "working" }], "other")).toBe(false);
  });
  test("stations sort after pigs: Oven, Review, Barn; a station is added once", () => {
    let farm = addStation({ lines: [] }, "sp", "barn");
    farm = addStation(farm, "sp", "oven");
    farm = addRelay(farm, "sp", "t1", "t2");
    farm = addStation(farm, "sp", "review");
    farm = addStation(farm, "sp", "oven");
    expect(farm.lines[0].stops.map((s) => s.kind)).toEqual(["pig", "pig", "oven", "review", "barn"]);
  });
});

describe("crates", () => {
  test("a pig's active crate ignores shipped and board crates", () => {
    const crates = [{ id: "1", state: "shipped", at: { kind: "pig", pig: "t1" } }, { id: "2", state: "working", at: { kind: "pig", pig: "t1" } }];
    expect(activeCrateFor("t1", crates).id).toBe("2");
    expect(activeCrateFor("t2", crates)).toBeNull();
  });
  test("the board lists only board crates, oldest first", () => {
    expect(boardCrates([{ id: "b", state: "board", created: 2 }, { id: "a", state: "board", created: 1 }, { id: "c", state: "working" }]).map((c) => c.id)).toEqual(["a", "b"]);
  });
  test("the report counts shipped crates inside the window only", () => {
    const now = 100_000_000;
    const r = report([{ state: "shipped", updated: now - 1000, tokens: 5 }, { state: "shipped", updated: now - 10_000_000, tokens: 7 }, { state: "burnt" }], [{ status: "blocked" }], now, 5_000_000);
    expect(r.shipped).toHaveLength(1);
    expect(r.burnt).toHaveLength(1);
    expect(r.blocked).toHaveLength(1);
    expect(r.tokens).toBe(5);
  });
});

test("ages read like the storyboard", () => {
  expect(fmtAge(30_000)).toBe("now");
  expect(fmtAge(41 * 60_000)).toBe("41 min");
  expect(fmtAge((6 * 60 + 2) * 60_000)).toBe("6 h 02");
  expect(fmtAge(3 * 24 * 3600_000)).toBe("3 d");
});

test("find ranks earlier matches first", () => {
  const items = [{ label: "pigpen · pen" }, { label: "Rosy · pigpen" }, { label: "docs" }];
  expect(filterFind(items, "pig").map((i) => i.label)).toEqual(["pigpen · pen", "Rosy · pigpen"]);
});

// The Terminals view borrows Herdr's own sidebar and tab bar; everything else is Pigtorio's.
test("the layout uses Pigtorio's roles and only Herdr's shipped workspace mods besides", () => {
  const layout = JSON.parse(readFileSync(new URL("../layouts/pigtorio.json", import.meta.url), "utf8"));
  const shipped = new Set(["spaces", "agents", "tabs", "pane"]);
  const roles = [];
  const walk = (n) => {
    for (const m of [n.content?.mod, n.content?.pane?.header?.content?.mod].filter(Boolean)) {
      if (m === "pigtorio") roles.push(n.content.props?.role);
      else expect(shipped.has(m)).toBe(true);
    }
    (n.children ?? []).forEach(walk);
  };
  walk(layout);
  expect(roles.sort()).toEqual(["bar", "farm", "footer"]);
});

describe("help", () => {
  test("lists exactly the seven verbs, all with text", () => {
    expect(HELP_VERBS.map((v) => v.verb)).toEqual(["Pen", "Pig", "Crate", "Belt", "Oven", "Review", "Barn"]);
    for (const v of HELP_VERBS) expect(v.runs.length).toBeGreaterThan(0);
    for (const r of HELP_KEYS) { expect(r.does.length).toBeGreaterThan(0); expect(r.keys.length).toBeGreaterThan(0); }
  });
  test("every view key is listed", () => {
    const listed = HELP_KEYS.flatMap((r) => r.keys).map((k) => k.toLowerCase());
    for (const k of Object.keys(VIEW_KEYS)) expect(listed).toContain(k);
  });
  test("? and h open help; plain / does not", () => {
    expect(isHelpKey({ key: "?" })).toBe(true);
    expect(isHelpKey({ key: "/", modifiers: { shift: true } })).toBe(true);
    expect(isHelpKey({ key: "h" })).toBe(true);
    expect(isHelpKey({ key: "/", modifiers: {} })).toBe(false);
  });
});
