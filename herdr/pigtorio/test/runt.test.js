import { describe, expect, test } from "bun:test";
import { addBelt, placeEntity } from "../mods/pigtorio/farmworld.js";
import { RUNT_BRIEF, appendGhosts, checkDraft, parseGhosts, resolveGhosts, runtHints } from "../mods/pigtorio/runtlogic.js";

const space = { t1: "sA", t2: "sA" };
const pigSpace = (a) => space[a] ?? null;
const pigs = [{ address: "t1", space: "sA", name: "Slate", status: "idle" }, { address: "t2", space: "sA", name: "Sandy", status: "idle" }];
const pens = [{ space: "sA", name: "pigpen", pigs }];

function base() {
  return placeEntity({ entities: {}, belts: [] }, { kind: "pen", x: 2, y: 6, space: "sA", id: "pA" }).farm;
}
const withBelt = () => addBelt(base(), "pig:t1", "pig:t2", pigSpace).farm;
const ids = (h) => h.map((x) => x.id);

describe("hints", () => {
  test("no pens: place a pen", () => {
    expect(ids(runtHints({ entities: {}, belts: [] }, [], [], [], null))).toEqual(["no-pen"]);
  });
  test("a pen without pigs: adopt one", () => {
    const h = runtHints(base(), [], [{ space: "sA", name: "pigpen", pigs: [] }], [], null);
    expect(h[0].id).toBe("no-pigs");
    expect(h[0].action.type).toBe("adopt");
  });
  test("pigs but no belt: a ghost belt between two pigs", () => {
    const h = runtHints(base(), pigs, pens, [], null).find((x) => x.id === "no-belt");
    expect(h.action.ops).toEqual([{ op: "belt", from: "pig:t1", to: "pig:t2" }]);
  });
  test("a belt without an Oven: ghost Oven and Review, drafted inside the pen", () => {
    const farm = withBelt();
    const h = runtHints(farm, pigs, pens, [], null).find((x) => x.id === "no-oven:sA");
    expect(h.action.ops.map((o) => o.op)).toEqual(["place", "belt", "place", "belt"]);
    const draft = appendGhosts(null, h.action.ops, h.action.note);
    const c = checkDraft(farm, draft, pigSpace, pigs);
    expect(c.problems).toEqual([]);
    expect(c.ok).toBe(true);
  });
  test("an Oven with no test command", () => {
    const farm = placeEntity(withBelt(), { kind: "oven", x: 6, y: 10 }).farm;
    expect(ids(runtHints(farm, pigs, pens, [], null))).toContain("no-test:sA");
    farm.pens = { sA: { test: "go test ./..." } };
    expect(ids(runtHints(farm, pigs, pens, [], null))).not.toContain("no-test:sA");
  });
  test("a line that ends at an Oven dead-ends", () => {
    let farm = withBelt();
    const o = placeEntity(farm, { kind: "oven", x: 6, y: 10 });
    farm = addBelt(o.farm, "pig:t2", `ent:${o.id}`, pigSpace).farm;
    const h = runtHints(farm, pigs, pens, [], null).find((x) => x.id.startsWith("dead-end"));
    expect(h.text).toMatch(/dead-ends/);
    expect(h.action.ops[0].kind).toBe("review");
  });
  test("a line ending at Review is fine", () => {
    let farm = withBelt();
    const o = placeEntity(farm, { kind: "review", x: 6, y: 10 });
    farm = addBelt(o.farm, "pig:t2", `ent:${o.id}`, pigSpace).farm;
    expect(ids(runtHints(farm, pigs, pens, [], null)).some((i) => i.startsWith("dead-end") || i.startsWith("no-oven"))).toBe(false);
  });
  test("crates on the board go on the belt", () => {
    const h = runtHints(withBelt(), pigs, pens, [{ id: "c1", title: "Fix", state: "board", pen: "sA" }], null).find((x) => x.id === "put:c1");
    expect(h.action).toMatchObject({ type: "putcrate", crate: "c1" });
    expect(h.action.line).toMatch(/^l-/);
    expect(ids(runtHints(base(), pigs, pens, [{ id: "c1", title: "Fix", state: "board" }], null))).not.toContain("put:c1");
  });
  test("blocked, burnt and holding", () => {
    const blocked = [{ ...pigs[0], status: "blocked" }, pigs[1]];
    const h = runtHints(withBelt(), blocked, pens, [{ id: "c2", title: "A", state: "burnt" }, { id: "c3", title: "B", state: "holding" }], null);
    expect(h.find((x) => x.id === "blocked:t1").action.type).toBe("talk");
    expect(h.find((x) => x.id === "burnt:c2").action.type).toBe("crate");
    expect(h.find((x) => x.id === "holding:c3").action.type).toBe("review");
  });
  test("a waiting draft replaces ghost hints", () => {
    const d = appendGhosts(null, [{ op: "belt", from: "pig:t1", to: "pig:t2" }]);
    const h = ids(runtHints(base(), pigs, pens, [], d));
    expect(h).toContain("draft");
    expect(h).not.toContain("no-belt");
  });
});

describe("resolveGhosts", () => {
  test("x/y, near a pig, near an entity, and the pen centre", () => {
    const farm = base();
    const draft = { ghosts: [
      { id: "g1", op: "place", kind: "oven", pen: "sA", x: 4, y: 10 },
      { id: "g2", op: "place", kind: "review", near: "pig:t1" },
      { id: "g3", op: "place", kind: "review", pen: "sA", near: "ghost:g1" },
      { id: "g4", op: "place", kind: "barn", pen: "sA" },
    ] };
    const r = resolveGhosts(farm, draft, pigSpace, pigs);
    expect(r.errors).toEqual([]);
    const e = (id) => r.farm.entities[r.placed[id]];
    expect(e("g1")).toMatchObject({ x: 4, y: 10, pen: "sA" });
    expect(e("g2").pen).toBe("sA");
    expect(e("g3").x).toBeGreaterThanOrEqual(6);
    expect(e("g4").pen).toBe("sA");
  });
  test("ghost belt ends map to the placed entities", () => {
    const draft = { ghosts: [
      { id: "g1", op: "place", kind: "oven", pen: "sA", x: 4, y: 10 },
      { id: "g2", op: "belt", from: "pig:t1", to: "ghost:g1" },
      { id: "g3", op: "belt", from: "pig:t1", to: "ghost:g9" },
    ] };
    const r = resolveGhosts(base(), draft, pigSpace);
    expect(r.farm.belts[0].to).toBe(`ent:${r.placed.g1}`);
    expect(r.errors.length).toBe(1);
  });
  test("crates come back as crate.add commands", () => {
    const r = resolveGhosts(base(), { ghosts: [{ id: "g1", op: "crate", crate: { title: "T", doneWhen: "W", text: "x", pen: "sA" } }, { id: "g2", op: "crate", crate: { title: "T" } }] }, pigSpace);
    expect(r.crates).toEqual([{ op: "crate.add", fields: { crate: { title: "T", doneWhen: "W", text: "x", pen: "sA" } } }]);
    expect(r.errors.length).toBe(1);
  });
});

describe("checkDraft", () => {
  test("a belt that ends at an Oven dead-ends", () => {
    const draft = { ghosts: [{ id: "g1", op: "place", kind: "oven", pen: "sA", x: 4, y: 10 }, { id: "g2", op: "belt", from: "pig:t1", to: "ghost:g1" }] };
    const c = checkDraft(base(), draft, pigSpace);
    expect(c.ok).toBe(false);
    expect(c.problems.join()).toMatch(/dead-ends/);
  });
  test("ending at the Barn passes", () => {
    const draft = { ghosts: [{ id: "g1", op: "place", kind: "barn", pen: "sA", x: 4, y: 10 }, { id: "g2", op: "belt", from: "pig:t1", to: "ghost:g1" }] };
    expect(checkDraft(base(), draft, pigSpace).ok).toBe(true);
  });
  test("overlapping ghosts are reported", () => {
    const draft = { ghosts: [{ id: "g1", op: "place", kind: "oven", x: 4, y: 10 }, { id: "g2", op: "place", kind: "review", x: 5, y: 11 }] };
    const c = checkDraft(base(), draft, pigSpace);
    expect(c.ok).toBe(false);
    expect(c.problems.join()).toMatch(/on top of/);
  });
  test("invalid belts and an empty draft", () => {
    const c = checkDraft(base(), { ghosts: [{ id: "g1", op: "belt", from: "pig:t1", to: "pig:t1" }] }, pigSpace);
    expect(c.ok).toBe(false);
    expect(checkDraft(base(), { ghosts: [] }, pigSpace).ok).toBe(false);
  });
});

describe("draft files", () => {
  test("appendGhosts renumbers and maps ghost refs; parseGhosts ignores junk", () => {
    const d1 = appendGhosts(null, [{ id: "o", op: "place", kind: "oven" }, { op: "belt", from: "pig:t1", to: "ghost:o" }], "why");
    const d2 = appendGhosts(d1, [{ id: "o", op: "place", kind: "review" }, { op: "belt", from: "ghost:o", to: "pig:t2" }]);
    expect(d2.ghosts.map((g) => g.id)).toEqual(["g1", "g2", "g3", "g4"]);
    expect(d2.ghosts[1].to).toBe("ghost:g1");
    expect(d2.ghosts[3].from).toBe("ghost:g3");
    expect(d2.note).toBe("why");
    expect(parseGhosts("nope")).toBeNull();
    expect(parseGhosts('{"ghosts":[]}')).toBeNull();
    expect(RUNT_BRIEF("/d")).toContain("/d/bin/pigtorio ghost");
  });
});
