import { describe, expect, test } from "bun:test";
import { addBelt, deriveLines, entityAt, freeSpot, migrateLines, placeEntity, placeProblem, prune, removeEntity, settle } from "../mods/pigtorio/farmworld.js";

const space = { t1: "sA", t2: "sA", t3: "sA", t9: "sB" };
const pigSpace = (a) => space[a] ?? null;

function farmWithPens() {
  let f = { entities: {}, belts: [] };
  f = placeEntity(f, { kind: "pen", x: 2, y: 6, space: "sA", id: "pA" }).farm;
  f = placeEntity(f, { kind: "pen", x: 2, y: 20, space: "sB", id: "pB" }).farm;
  return f;
}

describe("placing", () => {
  test("a station inside a pen belongs to it; one outside belongs to none", () => {
    let f = farmWithPens();
    const a = placeEntity(f, { kind: "oven", x: 4, y: 10 });
    expect(a.farm.entities[a.id].pen).toBe("sA");
    const b = placeEntity(a.farm, { kind: "oven", x: 30, y: 2 });
    expect(b.farm.entities[b.id].pen).toBeNull();
  });
  test("buildings don't stack, pens keep a gap, and nothing straddles a fence", () => {
    const f = farmWithPens();
    const r = placeEntity(f, { kind: "oven", x: 4, y: 10 });
    expect(placeProblem(r.farm, { kind: "review", x: 5, y: 11, w: 2, h: 2 })).toMatch(/on top of/);
    expect(placeProblem(f, { kind: "pen", x: 16, y: 6, w: 14, h: 8 })).toMatch(/too close/);
    expect(placeProblem(f, { kind: "oven", x: 17, y: 10, w: 2, h: 2 })).toMatch(/fence/);
    expect(placeProblem(f, { kind: "oven", x: -1, y: 0, w: 2, h: 2 })).toMatch(/edge/);
  });
  test("a click finds the station before the pen under it", () => {
    const r = placeEntity(farmWithPens(), { kind: "barn", x: 10, y: 9 });
    expect(entityAt(r.farm, 11, 10).kind).toBe("barn");
    expect(entityAt(r.farm, 3, 7).kind).toBe("pen");
    expect(entityAt(r.farm, 40, 40)).toBeNull();
  });
  test("freeSpot steps around what is in the way", () => {
    const r = placeEntity(farmWithPens(), { kind: "oven", x: 4, y: 10 });
    const s = freeSpot(r.farm, "oven", 4, 10, r.farm.entities.pA);
    expect(s).not.toBeNull();
    expect(placeProblem(r.farm, { kind: "oven", ...s, w: 2, h: 2 })).toBeNull();
  });
});

describe("belts", () => {
  function line() {
    let f = farmWithPens();
    const o = placeEntity(f, { kind: "oven", x: 4, y: 11, id: "ov" }); f = o.farm;
    const b = placeEntity(f, { kind: "barn", x: 10, y: 10, id: "bn" }); f = b.farm;
    for (const [a, c] of [["pig:t1", "pig:t2"], ["pig:t2", "ent:ov"], ["ent:ov", "ent:bn"]]) {
      const r = addBelt(f, a, c, pigSpace);
      expect(r.error).toBeUndefined();
      f = r.farm;
    }
    return f;
  }
  test("belts make the engine's line, in order, with the station kinds", () => {
    const f = line();
    expect(f.lines).toHaveLength(1);
    expect(f.lines[0].pen).toBe("sA");
    expect(f.lines[0].stops.map((s) => s.pig ?? s.kind)).toEqual(["t1", "t2", "oven", "barn"]);
  });
  test("one belt out and one in per end, same pen, no loops, nothing after the Barn", () => {
    const f = line();
    expect(addBelt(f, "pig:t1", "pig:t3", pigSpace).error).toMatch(/going out/);
    expect(addBelt(f, "pig:t3", "pig:t2", pigSpace).error).toMatch(/coming in/);
    expect(addBelt(f, "pig:t3", "pig:t9", pigSpace).error).toMatch(/one pen/);
    expect(addBelt(f, "ent:bn", "pig:t3", pigSpace).error).toMatch(/end of the line/);
    const g = addBelt(farmWithPens(), "pig:t1", "pig:t2", pigSpace).farm;
    expect(addBelt(g, "pig:t2", "pig:t1", pigSpace).error).toMatch(/loop/);
  });
  test("removing a station takes its belts and splits the line", () => {
    const f = removeEntity(line(), "ov");
    expect(f.belts.map((b) => `${b.from}>${b.to}`)).toEqual(["pig:t1>pig:t2"]);
    expect(f.lines[0].stops.map((s) => s.pig)).toEqual(["t1", "t2"]);
  });
  test("a line keeps its id when its tail changes", () => {
    const f = line();
    const id = f.lines[0].id;
    expect(removeEntity(f, "bn").lines[0].id).toBe(id);
  });
});

describe("the farm grows by itself", () => {
  test("settle adds the Job Board, Barn, Runt and one pen per space, once", () => {
    const f = settle({}, [{ id: "s1" }, { id: "s2" }]);
    const kinds = Object.values(f.entities).map((e) => e.kind).sort();
    expect(kinds).toEqual(["barn", "jobboard", "pen", "pen", "runt"]);
    expect(Object.keys(settle(f, [{ id: "s1" }, { id: "s2" }]).entities)).toHaveLength(5);
  });
  test("prune pulls up a pen whose space closed, with its stations and belts", () => {
    let f = farmWithPens();
    f = placeEntity(f, { kind: "oven", x: 4, y: 24, id: "ovB" }).farm;
    f = addBelt(f, "pig:t9", "ent:ovB", pigSpace).farm;
    f = prune(f, new Set(["sA"]));
    expect(Object.keys(f.entities).sort()).toEqual(["pA"]);
    expect(f.belts).toHaveLength(0);
  });
  test("an old line of stops becomes stations and belts", () => {
    const f = migrateLines({ ...farmWithPens(), lines: [{ id: "l1", pen: "sA", stops: [{ kind: "pig", pig: "t1" }, { kind: "pig", pig: "t2" }, { kind: "oven" }, { kind: "review" }] }] }, pigSpace);
    expect(f.belts).toHaveLength(3);
    expect(f.lines[0].stops.map((s) => s.pig ?? s.kind)).toEqual(["t1", "t2", "oven", "review"]);
    expect(deriveLines(f, pigSpace)).toHaveLength(1);
  });
});
