// Pigtorio's data: the farm file the mod owns, the state the engine writes,
// and the live pigs from Herdr. One Store per mod copy.
import { agents, terminals, spaces, machines, appHome, readTextFile, writeTextFile, watchFolder, hc } from "herdr";
import { pigName, skinFor, activeCrateFor } from "./model.js";
import { settle, prune, migrateLines, withLines, placeEntity, penEntity } from "./farmworld.js";

export const dataDir = () => `${appHome()}/pigtorio`;
export const engineBin = () => `${dataDir()}/bin/pigtorio`;
const ENGINE_STALE_MS = 45_000;

const EMPTY_FARM = { pens: {}, pigs: {}, lines: [], pending: {}, swineherd: { skin: "pink" }, entities: {}, belts: [] };

export class Store {
  constructor() {
    this.farm = { ...EMPTY_FARM };
    this.farmText = null;
    this.state = { crates: [], pigs: {}, acks: [], stats: {}, engine: null };
    this.since = {}; // address -> ms its status last changed (seen by this copy)
    this.lastStatus = {};
    this.engineStarting = 0;
  }

  async load() {
    await Promise.all([this.loadFarm(), this.loadState(), this.loadGhosts()]);
  }

  async loadGhosts() {
    const text = await readTextFile(`${dataDir()}/ghosts.json`).catch(() => null);
    try { this.ghosts = text ? JSON.parse(text) : { ghosts: [] }; } catch { /* mid-write */ }
  }

  // The world follows Herdr: a pen for every space (where one was staked out by
  // hand, else in a free row), pens of closed spaces pulled up, an old farm's
  // lines turned into belts. Writes only when something changed.
  async settleWorld(pigs) {
    const list = spaces();
    const ready = machines().every((m) => m.layout === "ready");
    const where = new Map(pigs.map((p) => [p.address, p.space]));
    const pigSpace = (a) => where.get(a) ?? null;
    const shape = (f) => JSON.stringify([f.entities, f.belts, f.lines, f.wantPen ?? null]);
    const grow = (farm) => {
      let f = farm;
      if (f.wantPen) {
        // macOS reports /tmp and /var folders under /private.
        const same = (a, b) => (a ?? "").replace(/^\/private\//, "/") === (b ?? "").replace(/^\/private\//, "/");
        const s = list.find((s) => !penEntity(f, s.id) && same(s.folder, f.wantPen.folder));
        if (s) { f = placeEntity(f, { kind: "pen", x: f.wantPen.x, y: f.wantPen.y, space: s.id }).farm ?? f; f = { ...f }; delete f.wantPen; }
        else if (Date.now() - (f.wantPen.at ?? 0) > 120_000) { f = { ...f }; delete f.wantPen; }
      }
      f = settle(f, list);
      if (ready) f = prune(f, new Set(list.map((s) => s.id)));
      f = migrateLines(f, pigSpace);
      return withLines(f, pigSpace);
    };
    if (shape(grow(JSON.parse(JSON.stringify(this.farm)))) === shape(this.farm)) return false;
    return this.updateFarm(grow);
  }

  async loadFarm() {
    const text = await readTextFile(`${dataDir()}/farm.json`);
    this.farmText = text;
    if (text) {
      try { this.farm = { ...EMPTY_FARM, ...JSON.parse(text) }; } catch { /* keep the last good farm */ }
    } else this.farm = { ...EMPTY_FARM };
  }

  async loadState() {
    const text = await readTextFile(`${dataDir()}/state/state.json`);
    if (!text) return;
    try { this.state = { crates: [], pigs: {}, acks: [], stats: {}, ...JSON.parse(text) }; } catch { /* mid-write; the next change re-reads */ }
  }

  // Commands the engine refused since this copy last looked; the first call only
  // remembers what is already there.
  freshRefusals() {
    const acks = this.state.acks ?? [];
    if (!this.seenAcks) { this.seenAcks = new Set(acks.map((a) => a.id)); return []; }
    const out = acks.filter((a) => !this.seenAcks.has(a.id) && a.ok === false);
    for (const a of acks) this.seenAcks.add(a.id);
    return out;
  }

  // Applies change(farm) and writes it, re-reading once if another copy wrote first.
  async updateFarm(change) {
    for (let attempt = 0; attempt < 3; attempt++) {
      const next = change(JSON.parse(JSON.stringify(this.farm)));
      const text = JSON.stringify(next, null, 2);
      try {
        await writeTextFile(`${dataDir()}/farm.json`, text, this.farmText);
        this.farm = next;
        this.farmText = text;
        return true;
      } catch {
        await this.loadFarm();
      }
    }
    return false;
  }

  // One command for the engine; it answers in state.json acks.
  async command(op, fields) {
    this.seq = ((this.seq ?? 0) + 1) % 10000;
    const id = `${Date.now()}-${String(this.seq).padStart(4, "0")}${Math.random().toString(36).slice(2, 5)}`;
    await writeTextFile(`${dataDir()}/inbox/${id}.json`, JSON.stringify({ op, ...fields }), null);
    return id;
  }

  engineAlive(now = Date.now()) {
    const beat = this.state?.engine?.beat;
    return beat != null && now - beat < ENGINE_STALE_MS;
  }

  async ensureEngine() {
    if (this.engineAlive() || Date.now() - this.engineStarting < 60_000) return;
    this.engineStarting = Date.now();
    const live = await hc(["ls", "--json"]).catch(() => null);
    try {
      const list = JSON.parse(live?.stdout ?? "{}").terminals ?? [];
      // The engine is the live terminal named pigtorio-engine running this home's binary
      // (a lost terminal comes back as a shell under the same name).
      const named = list.find((t) => t.name === "pigtorio-engine" && t.state === "live");
      if (named && String(named.agent ?? "").includes("pigtorio")) return;
      if (named) await hc(["kill", named.address]).catch(() => null);
    } catch { /* start it */ }
    await hc(["run", "--bg", "--no-parent", "--name", "pigtorio-engine", "--cwd", dataDir(), "--", engineBin(), "engine", "--home", appHome()]).catch(() => null);
  }

  // Follows the engine's state folder and the farm file for this copy's life.
  watch(cx, onChange) {
    cx.spawn(async (cx) => {
      for (;;) {
        const paths = await watchFolder(dataDir());
        // Sessions and the inbox change often; only the state and the farm file are drawn.
        const all = paths.some((p) => p === dataDir());
        const state = all || paths.some((p) => p.endsWith("state.json"));
        const farm = all || paths.some((p) => p.endsWith("farm.json"));
        const ghosts = all || paths.some((p) => p.endsWith("ghosts.json"));
        if (state) await this.loadState();
        if (farm) await this.loadFarm();
        if (ghosts) await this.loadGhosts();
        if (state || farm || ghosts) onChange(cx);
      }
    });
  }

  // Every pig: Herdr's agents plus live core terminals that report a status
  // without a pane (strays and sub-agents). Each carries its pen, name, skin,
  // role and crate.
  pigs(now = Date.now()) {
    const crates = this.state.crates ?? [];
    const byAddr = new Map();
    for (const a of agents()) {
      byAddr.set(a.address, { address: a.address, space: a.space, tab: a.tab, status: a.status, app: a.app, title: a.title, program: a.program, pane: a.pane, focused: a.focused, stray: false });
    }
    for (const t of terminals()) if (t.name === "pigtorio-runt") byAddr.delete(t.address);
    const statusAt = new Map();
    for (const t of terminals()) {
      if (t.status_ms != null) statusAt.set(t.address, t.status_ms);
      if (byAddr.has(t.address) || t.state !== "live" || !t.reported) continue;
      if (t.name === "pigtorio-engine" || t.name === "pigtorio-runt") continue;
      const status = typeof t.status === "string" ? t.status : t.status?.state;
      if (!status) continue;
      byAddr.set(t.address, { address: t.address, space: t.labels?.["herdr.space"] ?? null, tab: t.labels?.["herdr.tab"] ?? null, status: status === "error" ? "blocked" : status, app: t.status?.app ?? null, title: t.title, program: t.program, pane: null, focused: false, stray: true });
    }
    const out = [];
    for (const p of byAddr.values()) {
      const at = this.state.pigs?.[p.address]?.statusSince ?? statusAt.get(p.address);
      if (at != null) this.since[p.address] = at;
      else if (this.lastStatus[p.address] !== p.status) this.since[p.address] = now;
      this.lastStatus[p.address] = p.status;
      const crate = activeCrateFor(p.address, crates);
      const meta = this.farm.pigs?.[p.address] ?? {};
      out.push({ ...p, name: pigName(p.address, this.farm), skin: skinFor(p.address, this.farm), role: meta.role ?? null, crate, since: this.since[p.address] });
    }
    return out;
  }

  // Pens: one per space, its pigs inside, its machine and line.
  pens(pigs) {
    const list = spaces().map((s) => ({ space: s.id, name: s.name, machine: s.machine, folder: s.folder, branch: s.branch, status: s.status, active: s.active, tabs: s.tabs, pigs: pigs.filter((p) => p.space === s.id), line: (this.farm.lines ?? []).find((l) => l.pen === s.id) ?? null }));
    return list;
  }
}
