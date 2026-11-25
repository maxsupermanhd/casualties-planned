import type { BagJ, Cell, GameData, ItemInfo, Miss, Plan, Recipe, Settings, Snap, Step, WornJ } from "./types";

type Kind = "int" | "layer" | "room" | "cap" | "other";

class PlanError extends Error {
  constructor(
    msg: string,
    readonly kind: Kind = "other",
    readonly need = 0,
    readonly items: string[] = [],
  ) {
    super(msg);
  }
  wrap(prefix: string): PlanError {
    return new PlanError(prefix + this.message, this.kind, this.need, this.items);
  }
}

const intErr = (need: number, have: number) => new PlanError(`needs INT ${need} (have ${have})`, "int", need);

function roomErr(items: string[], load: number, cap: number): PlanError {
  if (items.length === 0) return new PlanError(`load ${fixed(load, 2)} is above the carry cap ${cap}`, "room");
  return new PlanError(`no room in hands or bags for ${items.join(", ")}`, "room", 0, items);
}

// fixed formats like Go %.Nf: exact ties round half to even.
export function fixed(x: number, n: number): string {
  const s = x.toFixed(100);
  const f = s.slice(s.indexOf(".") + 1 + n);
  if (/^50*$/.test(f)) {
    const up = x.toFixed(n);
    const down = (Math.trunc(x * 10 ** n) / 10 ** n).toFixed(n);
    const last = Number(down.charAt(down.length - 1));
    return last % 2 === 0 ? down : up;
  }
  return x.toFixed(n);
}

const goRound = (x: number) => Math.sign(x) * Math.round(Math.abs(x));
const sgn = (d: number) => (d >= 0 ? "+" : "") + d;
const add = (o: Record<string, number>, k: string, v: number) => {
  o[k] = (o[k] ?? 0) + v;
};

const xpCache: number[] = [];

// xpFor is Skills.GetExperienceForLevel from the game code.
export function xpFor(l: number): number {
  if (l <= 0) return 0;
  const c = xpCache[l];
  if (c !== undefined) return c;
  let n = 0;
  let s = 60;
  for (let i = 1; i <= l; i++) {
    if (i < 10) n += 60;
    else if (i === 10) {
      s = 100;
      n += s;
    } else {
      s *= 1.18;
      if (i > 20) {
        s *= 1.15;
        if (i >= 30) s *= 2;
      }
      n += s;
    }
  }
  xpCache[l] = goRound(n);
  return xpCache[l];
}

export function intOf(xp: number): number {
  let l = 0;
  while (xpFor(l + 1) <= xp) l++;
  return l;
}

interface Cont {
  max: number;
  per: number;
  enc: number;
  tags: string[];
}

interface It {
  uid: number;
  id: string;
  c: number;
  worn: boolean;
  res: boolean;
}

interface Entry {
  kind: string;
  name: string;
  key: string;
  n: number;
  breaks: string;
  usedUp: string;
  delta: Record<string, number>;
  got: Record<string, number>;
  note: string;
  goal: boolean;
  used: Record<string, number>;
  wear: Record<string, number>;
  mat: Record<string, number>;
  bneck: string;
  lvl: number;
  st: Snap | null;
}

interface Goal {
  id: string;
  n: number;
  done: number;
  why: string;
  err: PlanError | null;
}

const discardNote = "INT-UP: discard the result now";

class World {
  readonly byResult = new Map<string, number[]>();
  readonly conts = new Map<string, Cont>();
  readonly items: Record<string, ItemInfo>;
  readonly recipes: Recipe[];
  goalID = new Set<string>();
  constructor(
    data: GameData,
    readonly cfg: Settings,
  ) {
    this.items = data.items;
    this.recipes = data.recipes;
    data.recipes.forEach((r, i) => {
      const l = this.byResult.get(r.result);
      if (l) l.push(i);
      else this.byResult.set(r.result, [i]);
    });
    for (const id of cfg.game.bags) {
      const c = data.containers[id];
      if (c) this.conts.set(id, { max: c.capacity, per: c.perItem, enc: c.encumbrance, tags: c.tags });
    }
  }
  info(id: string): ItemInfo {
    return this.items[id] ?? { weight: 0 };
  }
  qual(id: string, q: string): number {
    for (const x of this.info(id).quals ?? []) if (x.id === q) return x.amt;
    return 0;
  }
  tag(id: string, t: string): boolean {
    return (this.info(id).tags ?? "").split(",").includes(t);
  }
  // isTool: the game tags real tools "tool". Canvas, nails and sticks are "material": a kept slot uses them up.
  isTool(id: string): boolean {
    return this.tag(id, "tool");
  }
  results(id: string): number[] {
    return this.byResult.get(id) ?? [];
  }
}

interface Bag {
  it: It;
  where: string;
  c: Cont;
  used: number;
  items: It[];
}

interface Pack {
  handR: It | null;
  handL: It | null;
  mouth: It | null;
  worn: It[];
  bags: Bag[];
  over: It[];
  load: number;
}

class State {
  inv: It[] = [];
  liq: Record<string, number> = {};
  xp = 0;
  depth = 0;
  made = new Set<number>();
  log: Entry[] = [];
  acts = 0;
  uid = 0;
  minus = false;
  cur: Entry | null = null;
  pend: Record<string, number> = {};
  filler = false;
  bneck = "";
  bioUID = 0;
  jugUID = 0;

  constructor(readonly w: World) {}

  clone(): State {
    const c = new State(this.w);
    c.set(this);
    c.inv = this.inv.map((x) => ({ ...x }));
    c.liq = { ...this.liq };
    c.made = new Set(this.made);
    c.log = this.log.slice();
    c.cur = null;
    c.pend = { ...this.pend };
    return c;
  }

  set(o: State): void {
    this.inv = o.inv;
    this.liq = o.liq;
    this.xp = o.xp;
    this.depth = o.depth;
    this.made = o.made;
    this.log = o.log;
    this.acts = o.acts;
    this.uid = o.uid;
    this.minus = o.minus;
    this.cur = o.cur;
    this.pend = o.pend;
    this.filler = o.filler;
    this.bneck = o.bneck;
    this.bioUID = o.bioUID;
    this.jugUID = o.jugUID;
  }

  get a() {
    return this.w.cfg.assumptions;
  }

  INT(): number {
    return intOf(this.xp);
  }

  add(id: string, c: number): number {
    this.uid++;
    this.inv.push({ uid: this.uid, id, c, worn: false, res: false });
    return this.uid;
  }

  idx(uid: number): number {
    return this.inv.findIndex((it) => it.uid === uid);
  }

  at(uid: number): It {
    const i = this.idx(uid);
    if (i < 0) throw new PlanError(`item ${uid} vanished`);
    return this.inv[i]!;
  }

  remove(uid: number): void {
    const i = this.idx(uid);
    if (i < 0) throw new PlanError(`item ${uid} vanished`);
    this.inv.splice(i, 1);
  }

  delta(k: string, v: number): void {
    if (this.cur) add(this.cur.delta, k, v);
  }

  entry(): Entry {
    if (!this.cur) throw new PlanError("no open step");
    return this.cur;
  }

  begin(kind: string, name: string, key: string): void {
    this.acts++;
    const e: Entry = {
      kind, name, key, n: 1, breaks: "", usedUp: "", delta: {}, got: this.pend, note: "", goal: false,
      used: {}, wear: {}, mat: {}, bneck: this.bneck, lvl: 0, st: null,
    };
    this.pend = {};
    this.bneck = "";
    const l = this.log[this.log.length - 1];
    if (key !== "" && l && l.kind === kind && l.name === name && l.key === key) {
      e.n = l.n + 1;
      e.breaks = l.breaks;
      e.usedUp = l.usedUp;
      Object.assign(e.delta, l.delta);
      for (const [k, v] of Object.entries(l.got)) add(e.got, k, v);
      Object.assign(e.used, l.used);
      Object.assign(e.wear, l.wear);
      Object.assign(e.mat, l.mat);
      if (l.bneck !== "") e.bneck = (l.bneck + " " + e.bneck).trim();
      this.log = this.log.slice(0, -1);
    }
    this.log.push(e);
    this.cur = e;
  }

  end(): void {
    this.entry().st = this.snap();
    this.cur = null;
  }

  isCont(id: string): boolean {
    return this.w.conts.has(id);
  }

  toolish(id: string): boolean {
    for (const q of this.w.cfg.game.toolQualities) if (this.w.qual(id, q) > 0) return true;
    return this.w.info(id).category === "tool" || this.w.tag(id, "tool");
  }

  find(id: string, minC: number, noMax: boolean, slot: string): number {
    const li = this.filler ? this.light() : -1;
    for (let i = 0; i < this.inv.length; i++) {
      const it = this.inv[i]!;
      if (i === li || it.id !== id || it.res || it.uid === this.bioUID || it.uid === this.jugUID || it.c < minC || (noMax && it.c >= 0.991)) continue;
      const info = this.w.info(id);
      if (info.destroyAt0 && it.c <= 0) continue;
      if (it.worn && (info.slot ?? "") !== slot) continue;
      return i;
    }
    return -1;
  }

  gather(id: string): number {
    const r = this.w.cfg.raws[id];
    if (!r) throw new PlanError(`${id} is not gatherable`);
    if (!r.layers.includes(this.depth + 1)) {
      throw new PlanError(`${id} is not on layer ${this.depth + 1} (it is on layers [${r.layers.join(" ")}])`, "layer");
    }
    this.acts++;
    const u = this.add(id, r.cond);
    add(this.pend, id, 1);
    return u;
  }

  byINT(id: string): number[] {
    const rs = this.w.results(id).slice();
    rs.sort((a, b) => this.w.recipes[a]!.int - this.w.recipes[b]!.int);
    return rs;
  }

  obtain(id: string, minC: number, noMax: boolean, slot: string, lvl: number): number {
    if (lvl > 25) throw new PlanError("recursion too deep for " + id);
    const i = this.find(id, minC, noMax, slot);
    if (i >= 0) {
      this.inv[i]!.res = true;
      return this.inv[i]!.uid;
    }
    let last = new PlanError(`no source for ${id}`);
    const r = this.w.cfg.raws[id];
    if (r && r.cond >= minC && (!noMax || r.cond < 0.991)) {
      try {
        const u = this.gather(id);
        this.at(u).res = true;
        return u;
      } catch (e) {
        last = asPlan(e);
      }
    }
    for (const ri of this.byINT(id)) {
      if (this.w.recipes[ri]!.liquid) continue;
      const c = this.clone();
      try {
        c.craft(ri, false, lvl + 1);
      } catch (e) {
        last = asPlan(e);
        continue;
      }
      const j = c.find(id, minC, noMax, slot);
      if (j >= 0) {
        this.set(c);
        this.inv[j]!.res = true;
        return this.inv[j]!.uid;
      }
      last = new PlanError(`${id} came out below ${fixed(minC * 100, 0)}%`);
    }
    throw last;
  }

  obtainQ(q: string, amt: number, minC: number, lvl: number): number {
    const p = this.w.cfg.game.qualityProviders[q] ?? "";
    let best = -1;
    this.inv.forEach((it, i) => {
      const info = this.w.info(it.id);
      if (it.res || it.worn || it.uid === this.bioUID || it.uid === this.jugUID || it.c < minC || this.w.qual(it.id, q) < amt || (info.destroyAt0 && it.c <= 0)) return;
      if (it.id !== p && (this.toolish(it.id) || (info.slot ?? "") !== "" || this.isCont(it.id))) return;
      if (best < 0 || it.id === p) best = i;
    });
    if (best >= 0) {
      this.inv[best]!.res = true;
      return this.inv[best]!.uid;
    }
    if (p === "") throw new PlanError(`no provider for quality ${q}`);
    return this.obtain(p, minC, false, "", lvl);
  }

  pickTool(q: string, amt: number, minC: number): number {
    let best = -1;
    this.inv.forEach((it, i) => {
      const info = this.w.info(it.id);
      const a = this.w.qual(it.id, q);
      if (it.res || it.worn || this.isCont(it.id) || it.uid === this.bioUID || it.uid === this.jugUID || a < amt || it.c < minC || (info.destroyAt0 && it.c <= 0)) return;
      if (best < 0) {
        best = i;
        return;
      }
      const b = this.w.qual(this.inv[best]!.id, q);
      if (a > b || (a === b && it.c > this.inv[best]!.c)) best = i;
    });
    return best;
  }

  tool(q: string, amt: number, minC: number, lvl: number): number {
    let i = this.pickTool(q, amt, minC);
    if (i >= 0) return this.inv[i]!.uid;
    let last = new PlanError(`no tool for ${q} ${fixed(amt, 1)}`);
    for (const id of this.w.cfg.game.toolCandidates[q] ?? []) {
      if (this.w.qual(id, q) < amt) continue;
      const c = this.clone();
      let u: number;
      try {
        u = c.obtain(id, minC, false, "", lvl + 1);
      } catch (e) {
        last = asPlan(e);
        continue;
      }
      c.at(u).res = false;
      this.set(c);
      i = this.pickTool(q, amt, minC);
      if (i >= 0) return this.inv[i]!.uid;
    }
    throw last;
  }

  ensureLiq(id: string, ml: number, lvl: number): void {
    if (id === "groundwater") {
      this.holdLiquid(lvl);
      if ((this.liq[id] ?? 0) < ml) {
        this.acts++;
        add(this.pend, "groundwater mL", ml - (this.liq[id] ?? 0));
        this.liq[id] = ml;
      }
      return;
    }
    const [, c] = this.bio();
    if (id === "biochem" && c > 0) {
      if (ml > c) throw new PlanError(`needs ${fixed(ml, 0)} mL biochem, holder has ${fixed(c, 0)}`);
      return;
    }
    while ((this.liq[id] ?? 0) < ml - 1e-6) {
      if (id === "biochem" && this.depth + 1 > this.a.biochemLastLayer) throw new PlanError(`no biochem source on layer ${this.depth + 1}`);
      let last = new PlanError(`no recipe for liquid ${id}`);
      let done = false;
      for (const ri of this.byINT(id)) {
        if (!this.w.recipes[ri]!.liquid) continue;
        const c2 = this.clone();
        try {
          c2.craft(ri, false, lvl + 1);
        } catch (e) {
          last = asPlan(e);
          continue;
        }
        this.set(c2);
        done = true;
        break;
      }
      if (!done) throw last;
    }
  }

  liqQ(q: string, amt: number): [string, number] {
    const src = this.w.cfg.game.liquidQualities[q];
    const wa = src?.["water"] ?? 0;
    const gw = src?.["groundwater"] ?? 0;
    if (!src || wa <= 0 || gw <= 0) throw new PlanError(`no source for liquid quality ${q}`);
    if ((this.liq["water"] ?? 0) * wa >= amt) return ["water", amt / wa];
    return ["groundwater", amt / gw];
  }

  wear(uid: number): void {
    const i = this.idx(uid);
    const me = this.inv[i]!;
    const slot = this.w.info(me.id).slot ?? "";
    const score = (id: string) => (this.w.info(id).armor ?? 0) + (this.w.conts.get(id)?.max ?? 0);
    const e = this.entry();
    for (const o of this.inv) {
      if (o.worn && (this.w.info(o.id).slot ?? "") === slot) {
        if (score(o.id) >= score(me.id)) return;
        o.worn = false;
        e.note += " (takes off " + o.id + ")";
      }
    }
    me.worn = true;
    e.note += " WEAR " + slot;
  }

  craft(ri: number, top: boolean, lvl: number): void {
    const r = this.w.recipes[ri]!;
    const o = this.w.cfg.options;
    if (o.disabled.includes(r.result)) throw new PlanError(`the recipe for ${r.result} is turned off`);
    if (o.intCap > 0 && r.int > o.intCap) throw new PlanError(`${r.result} needs INT ${r.int}, above the INT cap ${o.intCap}`, "cap");
    const inINT = this.INT();
    const dlt = inINT - r.int;
    if (dlt < 0 && (o.safeInt || !(top && this.minus && dlt === -1))) throw intErr(r.int, inINT);
    const slot = this.w.info(r.result).slot ?? "";
    const drop = lvl === 0 && this.filler;
    const wrap = (e: unknown) => asPlan(e).wrap(r.result + " <- ");
    const used: number[] = [];
    for (const g of r.items) {
      if (g.kept || g.liquid) continue;
      try {
        if (g.id) used.push(this.obtain(g.id, g.minCond, !!g.noMax, slot, lvl));
        else used.push(this.obtainQ(g.q ?? "", g.amt ?? 0, g.minCond, lvl));
      } catch (e) {
        throw wrap(e);
      }
    }
    const tools: { uid: number; w: number }[] = [];
    for (const g of r.items) {
      if (!g.kept) continue;
      try {
        const u = this.tool(g.q ?? "", g.amt ?? 0, g.minCond, lvl);
        tools.push({ uid: u, w: (g.amt ?? 0) / this.w.qual(this.at(u).id, g.q ?? "") });
      } catch (e) {
        throw wrap(e);
      }
    }
    const need: Record<string, number> = {};
    try {
      for (const g of r.items) {
        if (!g.liquid) continue;
        if (g.id) {
          add(need, g.id, g.minCond);
          continue;
        }
        const [id, ml] = this.liqQ(g.q ?? "", g.amt ?? 0);
        add(need, id, ml);
      }
      for (const id of ["water", "groundwater", "biochem"]) {
        const v = need[id];
        if (v !== undefined && v > 0) this.ensureLiq(id, v, lvl);
      }
      for (const [id, ml] of Object.entries(need)) this.ensureLiq(id, ml, lvl);
      if (r.liquid && r.result !== "biochem" && !drop) this.holdLiquid(lvl);
    } catch (e) {
      throw wrap(e);
    }
    const key = !this.made.has(ri) || top ? "" : "c";
    const name = r.repair ? "repair " + r.result : r.result;
    this.begin("craft", name, key);
    const e = this.entry();
    e.lvl = lvl;
    const [, full] = this.bio();
    for (const u of used) {
      const i = this.idx(u);
      if (i < 0) throw new PlanError(`${r.result}: ingredient broke before use`);
      const id = this.inv[i]!.id;
      this.delta(id, -1);
      add(e.used, id, 1);
      this.remove(u);
    }
    for (const [id, ml] of Object.entries(need)) {
      add(e.used, id + " mL", ml);
      if (id === "biochem" && full > 0) continue;
      this.liq[id] = (this.liq[id] ?? 0) - ml;
      this.delta(id + " mL", -ml);
    }
    for (const t of tools) {
      const i = this.idx(t.uid);
      if (i < 0) throw new PlanError(`${r.result}: kept item gone before use`);
      const it = this.inv[i]!;
      const tool = this.w.isTool(it.id);
      it.c -= t.w;
      add(tool ? e.wear : e.mat, it.id, t.w);
      if (it.c <= 1e-9 && this.w.info(it.id).destroyAt0) {
        if (tool) e.breaks += " " + it.id + " BREAKS";
        else e.usedUp += " " + it.id;
        this.delta(it.id, -1);
        this.remove(t.uid);
      }
    }
    let cond = r.cond;
    if (dlt === -1) {
      cond *= this.a.gambleCondition;
      e.note += ` d=-1 GAMBLE: 50% full, 50% at 20-90% (sim assumes worst ${fixed(cond * 100, 0)}%)`;
    }
    if (r.liquid) {
      this.delta(r.result + " mL", r.cond * r.amount);
      if (drop) e.note += " " + discardNote;
      else add(this.liq, r.result, r.cond * r.amount);
    } else {
      const holders = this.a.biochemHolders;
      for (let k = 0; k < r.amount; k++) {
        this.delta(r.result, 1);
        if (drop && !this.toolish(r.result)) {
          if (k === 0) e.note += " " + discardNote;
          continue;
        }
        const u = this.add(r.result, cond);
        if (slot !== "" && k === 0) this.wear(u);
        const [b, c] = this.bio();
        const h = holders[r.result] ?? 0;
        if (k === 0 && h > c && this.w.cfg.options.biochemFull) {
          if (b !== "") e.note += ` BIOCHEM moves from ${b} to ${r.result} (${fixed(h, 0)} mL, kept full)`;
          else e.note += ` BIOCHEM holder (${fixed(h, 0)} mL, kept full)`;
          this.bioUID = u;
        }
      }
    }
    this.releaseJug();
    if (!this.made.has(ri)) {
      this.made.add(ri);
      this.xp += this.a.firstCraftXP;
      e.note += ` FIRST +${this.a.firstCraftXP}xp`;
    }
    e.note = `[INT${r.int} d${sgn(dlt)}]${e.note}`.trim();
    this.end();
  }

  itemW(it: It): number {
    const info = this.w.info(it.id);
    if (info.scaleW) return 0.1 + (info.weight - 0.1) * Math.max(0, it.c);
    return info.weight;
  }

  descend(): void {
    this.begin("descend", `to layer ${this.depth + 2}`, "");
    for (const [k, v] of Object.entries(this.liq)) {
      if (v > 0.01 && !(k === "biochem" && this.bioUID !== 0)) this.delta(k + " mL", -v);
      this.liq[k] = 0;
    }
    this.jugUID = 0;
    this.depth++;
    this.xp += this.a.descendXP;
    this.end();
  }

  holdLiquid(lvl: number): void {
    if (this.jugUID !== 0 && this.idx(this.jugUID) >= 0) return;
    const h = this.a.liquidHolder;
    let i = this.find(h, 0, false, "");
    if (i < 0) {
      let u: number;
      try {
        u = this.obtain(h, 0, false, "", lvl + 1);
      } catch (e) {
        throw asPlan(e).wrap(`liquid needs a free ${h}: `);
      }
      i = this.idx(u);
    }
    this.inv[i]!.res = false;
    this.jugUID = this.inv[i]!.uid;
  }

  releaseJug(): void {
    for (const [k, v] of Object.entries(this.liq)) if (k !== "biochem" && v > 0.01) return;
    this.jugUID = 0;
  }

  light(): number {
    for (const id of this.a.lights) {
      const i = this.inv.findIndex((it) => it.id === id);
      if (i >= 0) return i;
    }
    return -1;
  }

  accepts(b: Bag, it: It): boolean {
    const w = this.itemW(it);
    if (w > b.c.per + 0.001 || b.used + w > b.c.max + 0.001 || this.isCont(it.id)) return false;
    if (b.c.tags.length === 0) return true;
    return b.c.tags.some((t) => this.w.tag(it.id, t));
  }

  pack(): Pack {
    const p: Pack = { handR: null, handL: null, mouth: null, worn: [], bags: [], over: [], load: 0 };
    const li = this.light();
    if (li >= 0) p.handR = { ...this.inv[li]! };
    const mag: Record<string, number> = {};
    for (const it of this.inv) {
      const m = this.w.cfg.game.magazines[it.id];
      if (m) add(mag, m.round, m.capacity);
    }
    const loose: It[] = [];
    this.inv.forEach((it, i) => {
      if (i === li) return;
      if ((mag[it.id] ?? 0) > 0) {
        mag[it.id]!--;
        return;
      }
      if (it.worn) {
        p.worn.push(it);
        const c = this.w.conts.get(it.id);
        if (c) p.bags.push({ it, where: "worn " + (this.w.info(it.id).slot ?? ""), c, used: 0, items: [] });
        return;
      }
      loose.push(it);
    });
    let held = 0;
    const rest: It[] = [];
    for (const it of loose) {
      const c = this.w.conts.get(it.id);
      if (c && held < this.a.backSlots) {
        held++;
        p.bags.push({ it, where: `back slot ${held}`, c, used: 0, items: [] });
        continue;
      }
      rest.push(it);
    }
    p.bags.sort((a, b) => a.c.enc - b.c.enc);
    rest.sort((a, b) => this.itemW(b) - this.itemW(a));
    const handsOnly = this.w.cfg.game.handsOnly;
    for (const it of rest) {
      const b = p.bags.find((b) => this.accepts(b, it));
      if (b) {
        b.used += this.itemW(it);
        b.items.push(it);
        continue;
      }
      if (!p.handL) p.handL = { ...it };
      else if (!p.mouth && !handsOnly.includes(it.id)) p.mouth = { ...it };
      else p.over.push(it);
    }
    for (const x of [p.handR, p.handL, p.mouth]) if (x) p.load += this.itemW(x);
    for (const it of p.worn) p.load += this.itemW(it);
    for (const b of p.bags) {
      if (!b.it.worn) p.load += this.itemW(b.it);
      p.load += b.used * b.c.enc;
    }
    for (const it of p.over) p.load += this.itemW(it);
    return p;
  }

  cells(its: It[]): Cell[] {
    const out: Cell[] = [];
    const at = new Map<string, number>();
    for (const it of its) {
      let i = at.get(it.id);
      if (i === undefined) {
        i = out.length;
        at.set(it.id, i);
        const c: Cell = { id: it.id, n: 0, conds: [] };
        if (this.toolish(it.id)) c.tool = true;
        out.push(c);
      }
      const c = out[i]!;
      c.n++;
      c.conds.push(goRound(it.c * 1000) / 1000);
    }
    return out;
  }

  cell(it: It | null): Cell | null {
    return it ? this.cells([it])[0]! : null;
  }

  bio(): [string, number] {
    const i = this.idx(this.bioUID);
    if (this.bioUID === 0 || i < 0) return ["", 0];
    const id = this.inv[i]!.id;
    return [id, this.a.biochemHolders[id] ?? 0];
  }

  snap(): Snap {
    const p = this.pack();
    const n = this.INT();
    const j: Snap = {
      int: n, xp: this.xp, nextXp: xpFor(n + 1), load: p.load, cap: this.a.carryCap, layer: this.depth + 1,
      handR: this.cell(p.handR), handL: this.cell(p.handL), mouth: this.cell(p.mouth), worn: [], bags: [], over: this.cells(p.over), liq: {},
    };
    for (const it of p.worn) j.worn.push({ slot: this.w.info(it.id).slot ?? "", id: it.id, cond: goRound(it.c * 1000) / 1000 } satisfies WornJ);
    for (const b of p.bags) {
      const bj: BagJ = { id: b.it.id, where: b.where, max: b.c.max, used: goRound(b.used * 100) / 100, enc: b.c.enc, items: this.cells(b.items) };
      if (b.c.tags.length) bj.tags = b.c.tags.join(",");
      j.bags.push(bj);
    }
    const [bi, bml] = this.bio();
    if (bi !== "") {
      j.bioIn = bi;
      j.bioMl = bml;
    }
    const ji = this.idx(this.jugUID);
    if (this.jugUID !== 0 && ji >= 0) j.jug = this.inv[ji]!.id;
    for (const [k, v] of Object.entries(this.liq)) if (v > 0.01 && !(k === "biochem" && bi !== "")) j.liq[k] = v;
    return j;
  }

  // settle keeps the best light in hand and makes the kit fit with the mouth empty.
  settle(): PlanError | null {
    const best = this.a.lights[0] ?? "";
    const ri = this.recipeFor(best);
    const li = this.light();
    if (ri >= 0 && this.made.has(ri) && (li < 0 || this.inv[li]!.id !== best)) {
      const c = this.clone();
      try {
        c.craft(ri, true, 0);
      } catch (e) {
        return asPlan(e).wrap(`replace ${best}: `);
      }
      c.log[c.log.length - 1]!.note += " REPLACES the " + best + " (light hand)";
      this.set(c);
    }
    for (;;) {
      const p = this.pack();
      const over = p.over.slice();
      if (p.mouth) over.push(p.mouth);
      if (over.length === 0 && p.load <= this.a.carryCap + 1e-6) return null;
      if (over.length > 0 && this.storageFirst()) continue;
      return roomErr(over.map((it) => it.id), p.load, this.a.carryCap);
    }
  }

  storageFirst(): boolean {
    let held = 0;
    for (const b of this.pack().bags) if (!b.it.worn) held++;
    const ri = this.recipeFor(this.a.storageCraft);
    if (ri < 0 || held >= this.a.backSlots) return false;
    const c = this.clone();
    try {
      c.craft(ri, true, 0);
    } catch {
      return false;
    }
    c.log[c.log.length - 1]!.note += " STORAGE (back slot)";
    this.set(c);
    return true;
  }

  recipeFor(id: string): number {
    for (const ri of this.w.results(id)) {
      const r = this.w.recipes[ri]!;
      if (!r.repair && !r.liquid) return ri;
    }
    return -1;
  }

  gambleOK(id: string): boolean {
    return this.w.cfg.options.gambleMode === "all" || this.w.cfg.upChain.gamble.includes(id);
  }

  goalCraft(g: Goal): void {
    for (const ri of this.w.results(g.id)) {
      const r = this.w.recipes[ri]!;
      if (r.repair || r.liquid) continue;
      if (this.minus && !this.gambleOK(g.id) && this.INT() < r.int) throw intErr(r.int, this.INT());
      for (const ing of r.items) {
        const l = this.light();
        if (ing.id && !this.a.lights.includes(g.id) && l >= 0 && ing.id === this.inv[l]!.id && this.INT() >= r.int) {
          const fr = Math.max(0, this.recipeFor(ing.id));
          try {
            this.craft(fr, true, 0);
          } catch (e) {
            throw asPlan(e).wrap(`spare ${ing.id}: `);
          }
          this.log[this.log.length - 1]!.note += " SPARE light: the next craft eats one";
        }
      }
      this.craft(ri, true, 0);
      return;
    }
    throw new PlanError("no recipe for " + g.id);
  }

  fit(c: State, g: Goal): [State, PlanError | null] {
    const p = c.pack();
    if (p.mouth || p.over.length > 0) {
      const c2 = this.clone();
      if (c2.storageFirst() && tryDo(() => c2.goalCraft(g)) === null) {
        c2.log[c2.log.length - 1]!.goal = true;
        if (c2.settle() === null) return [c2, null];
      }
    }
    return [c, c.settle()];
  }
}

function asPlan(e: unknown): PlanError {
  if (e instanceof PlanError) return e;
  throw e;
}

function tryDo(f: () => void): PlanError | null {
  try {
    f();
    return null;
  } catch (e) {
    return asPlan(e);
  }
}

function blockOf(e: PlanError | null): string {
  switch (e?.kind) {
    case "room":
      return e.items.length === 0 ? "carry cap" : "storage";
    case "layer":
      return "layer";
    case "int":
      return "INT";
    case "cap":
      return "INT cap";
  }
  return "recipe";
}

export function goals(targets: string[]): Goal[] {
  if (targets.length === 0) throw new Error("the target list is empty");
  return targets.map((s) => {
    let id = s;
    let n = 1;
    const at = s.indexOf("*");
    if (at >= 0) {
      id = s.slice(0, at);
      const b = s.slice(at + 1);
      n = /^\d+$/.test(b) ? Number(b) : 0;
      if (n < 1) throw new Error(`bad target "${s}"`);
    }
    if (id === "") throw new Error(`bad target "${s}"`);
    return { id, n, done: 0, why: "", err: null };
  });
}

// check returns the first problem with the settings, or "".
export function check(data: GameData, cfg: Settings): string {
  const a = cfg.assumptions;
  if (!(a.carryCap > 0)) return "carry cap must be more than 0";
  if (!(a.layers >= 1)) return "layers must be 1 or more";
  if (!(a.maxLayer >= 1 && a.maxLayer <= a.layers)) return `layer limit must be 1 to ${a.layers}`;
  if (a.startINT < 0 || a.backSlots < 0) return "start INT and back slots must not be negative";
  if (a.lights.length === 0) return "the light list needs at least one item";
  try {
    for (const g of goals(cfg.targets)) if (!data.items[g.id]) return `unknown target item ${g.id}`;
  } catch (e) {
    return (e as Error).message;
  }
  const ids = [
    ...a.startItems, ...a.lights, a.liquidHolder, a.storageCraft, ...cfg.upChain.gunRecipes, ...cfg.upChain.gamble,
    ...cfg.upChain.neverFiller, ...cfg.game.handsOnly, ...Object.keys(a.biochemHolders), ...Object.keys(cfg.raws), ...cfg.game.bags,
    ...Object.values(cfg.game.qualityProviders), ...a.extraGathers.map((e) => e.item), ...Object.values(cfg.game.toolCandidates).flat(),
  ];
  for (const [k, m] of Object.entries(cfg.game.magazines)) ids.push(k, m.round);
  for (const id of ids) if (!data.items[id]) return `the settings name item "${id}", the game data has no such item`;
  for (const id of cfg.game.bags) if (!data.containers[id]) return `${id} is not a container in the game data`;
  return "";
}

function simulate(w: World): { s: State; gs: Goal[]; stopped: string } {
  const cfg = w.cfg;
  const gs = goals(cfg.targets);
  let s = new State(w);
  s.xp = xpFor(cfg.assumptions.startINT);
  for (const id of cfg.assumptions.startItems) s.add(id, 1);
  let guns = cfg.options.guns === "always";
  for (const g of gs) {
    w.goalID.add(g.id);
    if (cfg.options.guns === "auto") guns = guns || cfg.upChain.gunRecipes.includes(g.id);
  }
  let stopped = "";
  loop: for (let iter = 0; iter < 5000; iter++) {
    let progressed = false;
    for (const g of gs) {
      if (g.done >= g.n) continue;
      const c = s.clone();
      const err = tryDo(() => c.goalCraft(g));
      if (err) {
        g.why = err.message;
        g.err = err;
        continue;
      }
      c.log[c.log.length - 1]!.goal = true;
      const [c2, ferr] = s.fit(c, g);
      if (ferr) {
        g.why = ferr.message;
        g.err = ferr;
        continue;
      }
      g.done++;
      g.why = "";
      g.err = null;
      s = c2;
      progressed = true;
      break;
    }
    if (progressed) continue;
    let best: State | null = null;
    let acts = 0;
    for (let ri = 0; cfg.options.fillers && ri < w.recipes.length; ri++) {
      const r = w.recipes[ri]!;
      if (s.made.has(ri) || cfg.upChain.neverFiller.includes(r.result) || w.goalID.has(r.result) || (!guns && cfg.upChain.gunRecipes.includes(r.result))) continue;
      const c = s.clone();
      c.filler = true;
      if (tryDo(() => c.craft(ri, false, 0))) continue;
      c.filler = false;
      if (best && c.acts >= acts) continue;
      const n = c.acts;
      c.log[c.log.length - 1]!.note += " XP filler";
      if (c.settle() === null) {
        best = c;
        acts = n;
      }
    }
    if (best) {
      s = best;
      continue;
    }
    let pending = false;
    let gate = Infinity;
    let who: string[] = [];
    for (const g of gs) {
      if (g.done >= g.n) continue;
      pending = true;
      if (g.err?.kind === "int") {
        if (g.err.need < gate) {
          gate = g.err.need;
          who = [];
        }
        if (g.err.need === gate) who.push(g.id);
      }
    }
    if (!pending) break;
    let bn = "";
    if (gate < Infinity) bn = `INT ${gate} gate: ${fixed(xpFor(gate) - s.xp, 0)} xp short (${who.join(", ")})`;
    if (s.depth + 1 < cfg.assumptions.maxLayer) {
      for (const e of cfg.assumptions.extraGathers) {
        if (e.layer !== s.depth + 1) continue;
        const c = s.clone();
        for (let k = 0; k < e.count; k++) {
          const ge = tryDo(() => c.gather(e.item));
          if (ge) {
            stopped = ge.message;
            break loop;
          }
        }
        const err = c.settle();
        if (err) {
          bn = `${bn} no room to take ${e.count} ${e.item} down: ${err.message}`.trim();
          continue;
        }
        s = c;
      }
      s.bneck = bn;
      s.descend();
      const err = s.settle();
      if (err) {
        stopped = `can not carry the kit to layer ${s.depth + 1}: ${err.message}`;
        break;
      }
      continue;
    }
    if (!s.minus && !cfg.options.safeInt && cfg.options.gambleMode !== "off") {
      s.minus = true;
      s.bneck = `layer limit ${cfg.assumptions.maxLayer}: d=-1 armor gamble allowed`;
      continue;
    }
    break;
  }
  return { s, gs, stopped };
}

function newStep(e: Entry): Step {
  const st: Step = {
    step: 0, kind: e.kind, name: e.name, times: e.n, note: e.note, gathered: { ...e.got }, used: { ...e.used }, toolWear: { ...e.wear }, matWear: { ...e.mat },
    delta: { ...e.delta }, incl: {}, goal: e.goal, after: e.st!,
  };
  const b = e.breaks.trim();
  if (b) st.breaks = b;
  const u = e.usedUp.trim();
  if (u) st.usedUp = u;
  if (e.bneck) st.bottleneck = e.bneck;
  return st;
}

function absorb(a: Step, b: Step): void {
  for (const [k, v] of Object.entries(b.gathered)) add(a.gathered, k, v);
  for (const [k, v] of Object.entries(b.used)) add(a.used, k, v);
  for (const [k, v] of Object.entries(b.toolWear)) add(a.toolWear, k, v);
  for (const [k, v] of Object.entries(b.matWear)) add(a.matWear, k, v);
  for (const [k, v] of Object.entries(b.delta)) add(a.delta, k, v);
  a.incl ??= {};
  for (const [k, v] of Object.entries(b.incl ?? {})) add(a.incl, k, v);
  const x = (a.inclXp ?? 0) + (b.inclXp ?? 0);
  if (x) a.inclXp = x;
  const br = ((a.breaks ?? "") + " " + (b.breaks ?? "")).trim();
  if (br) a.breaks = br;
  const uu = ((a.usedUp ?? "") + " " + (b.usedUp ?? "")).trim();
  if (uu) a.usedUp = uu;
  if (b.bottleneck) a.bottleneck = ((a.bottleneck ?? "") + " " + b.bottleneck).trim();
}

function same(a: Step, b: Step): boolean {
  return a.kind === "craft" && a.kind === b.kind && a.name === b.name && !b.note.includes("FIRST") && !b.note.includes("BIOCHEM") && a.goal === b.goal;
}

// steps folds sub-crafts (lvl > 0, not a carried goal item) into the craft they feed and merges repeats as xN.
function steps(s: State): Step[] {
  const out: Step[] = [];
  let buf: Step | null = null;
  let made: Record<string, number> = {};
  for (const e of s.log) {
    const st = newStep(e);
    if (e.kind === "craft" && e.lvl > 0 && !s.w.goalID.has(e.name)) {
      for (const [k, v] of Object.entries(e.delta)) if (v > 0) add(made, k, v);
      add(st.incl!, e.name, e.n);
      if (e.note.includes("FIRST")) st.inclXp = (st.inclXp ?? 0) + s.a.firstCraftXP;
      if (!buf) buf = { ...st, times: 0, note: "", name: "" };
      else absorb(buf, st);
      buf.after = e.st!;
      continue;
    }
    if (buf) {
      if (e.kind !== "craft") {
        out.push({ ...buf, kind: "craft", name: "prep", times: 1, note: "" });
      } else {
        absorb(st, buf);
        for (const [k, v] of Object.entries(made)) {
          const u = Math.min(st.used[k] ?? 0, v);
          const left = (st.used[k] ?? 0) - u;
          if (left < 1e-6) delete st.used[k];
          else st.used[k] = left;
        }
      }
      buf = null;
      made = {};
    }
    const last = out[out.length - 1];
    if (last && same(last, st)) {
      const times = last.times + st.times;
      absorb(last, st);
      last.times = times;
      last.after = st.after;
      continue;
    }
    out.push(st);
  }
  out.forEach((x, i) => {
    x.step = i + 1;
    if (x.incl && Object.keys(x.incl).length === 0) delete x.incl;
  });
  return out;
}

export function plan(data: GameData, cfg: Settings): Plan {
  const bad = check(data, cfg);
  if (bad) throw new Error(bad);
  const w = new World(data, cfg);
  const { s, gs, stopped } = simulate(w);
  const left: Miss[] = gs.filter((g) => g.done < g.n).map((g) => ({ id: g.id, done: g.done, n: g.n, block: stopped && !g.err ? "stopped" : blockOf(g.err), why: g.why || stopped }));
  const p: Plan = { maxLayer: cfg.assumptions.maxLayer, steps: steps(s), notReached: left };
  if (stopped) p.stopped = stopped;
  return p;
}
