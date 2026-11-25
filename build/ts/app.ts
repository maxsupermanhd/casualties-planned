import { check, choices, field, limits, plan as runPlan } from "./planner";
import type { Cell, GameData, PageData, Plan, PlanReply, Settings, Snap, Step } from "./types";

const $ = (id: string): HTMLElement => {
  const e = document.getElementById(id);
  if (!e) throw new Error("missing element " + id);
  return e;
};
const D = JSON.parse($("data").textContent ?? "{}") as PageData;
const GAME: GameData = { recipes: D.recipes, items: D.items, containers: D.containers };
const LS = { settings: "cuplan-settings", step: "cuplan-step", view: "cuplan-view" };

function esc(s: unknown): string {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] ?? c);
}
const bid = (k: string) => k.replace(/ mL$/, "").replace(/^repair /, "");
function nm(k: string): string {
  const b = bid(k);
  const n = D.names[b] ?? D.rules.liquidNames[b] ?? b;
  return (k.startsWith("repair ") ? "Repair " : "") + n;
}
const isLiq = (k: string) => / mL$/.test(k) || (!D.atlas[k] && k in D.rules.liquidNames);
const fmt = (v: number) => (Math.abs(v - Math.round(v)) < 1e-6 ? String(Math.round(v)) : String(Math.round(v * 100) / 100));
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

function icon(k: string, box: number): string {
  const r = D.atlas[bid(k)];
  const wrap = (inner: string) => `<span class="ib" style="width:${box}px;height:${box}px">${inner}</span>`;
  if (!r) return wrap(isLiq(k) ? '<span class="drop"></span>' : '<span class="q">?</span>');
  const [x, y, w, h] = r;
  let s = box / Math.max(w, h);
  if (s >= 1) s = Math.floor(s);
  const [aw, ah] = D.atlasSize;
  return wrap(`<span class="ic" style="width:${w * s}px;height:${h * s}px;background-size:${aw * s}px ${ah * s}px;background-position:${-x * s}px ${-y * s}px"></span>`);
}

function chip(k: string, v: number, cls: string): string {
  const amt = / mL$/.test(k) ? fmt(v) + " mL" : "×" + fmt(v);
  return `<span class="chip ${cls}">${icon(k, 24)}<span class="n">${esc(nm(k))}</span><b>${amt}</b></span>`;
}

function chips(o: Record<string, number> | undefined, cls = "", filt?: (v: number) => boolean): string {
  const ks = Object.keys(o ?? {}).filter((k) => !filt || filt(o![k]!));
  if (!ks.length) return "";
  return '<div class="chips">' + ks.map((k) => chip(k, Math.abs(o![k]!), cls)).join("") + "</div>";
}

const ccol = (c: number) => (c >= 0.6 ? "var(--ok)" : c >= 0.3 ? "var(--warn)" : "var(--bad)");

function conds(cs: number[], tool: boolean): string {
  if (!cs.length) return "";
  const low = cs.some((c) => c < 0.999);
  if (!tool && !low) return "";
  let h = '<div class="conds">' + cs.slice(0, 6).map((c) => `<span class="cb"><i style="width:${Math.max(2, c * 100)}%;background:${ccol(c)}"></i></span>`).join("") + "</div>";
  if (cs.length === 1) h += `<span class="pct">${Math.round(cs[0]! * 100)}%</span>`;
  else if (low) h += `<span class="pct">min ${Math.round(Math.min(...cs) * 100)}%</span>`;
  return h;
}

interface TileOpt {
  slot?: string;
  tool?: boolean;
  isNew?: boolean;
}

function tile(c: Cell | null, o: TileOpt = {}): string {
  let h = `<div class="tile${o.isNew ? " new" : ""}">`;
  if (o.slot) h += `<span class="sl">${esc(o.slot)}</span>`;
  if (c) {
    if (c.n > 1) h += `<span class="cnt">×${c.n}</span>`;
    h += icon(c.id, 32) + `<span class="tn">${esc(nm(c.id))}</span>` + conds(c.conds, !!(c.tool || o.tool));
    if (c.ammo) h += `<span class="pct">${esc(c.ammo.n)} × ${esc(nm(c.ammo.id))} loaded</span>`;
  } else h += '<span class="ib" style="width:32px;height:32px"></span><span class="tn" style="color:var(--mut)">empty</span>';
  return h + "</div>";
}

function meter(v: number, max: number, cls = ""): string {
  const p = max > 0 ? Math.min(100, (v / max) * 100) : 0;
  return `<div class="meter ${cls}"><i style="width:${p}%"></i></div>`;
}

interface Note {
  int: number | null;
  d: number;
  flags: string[];
  wear: string;
  rest: string;
}

function parseNote(n: string): Note {
  const r: Note = { int: null, d: 0, flags: [], wear: "", rest: n };
  const m = /\[INT(\d+) d([+-]?\d+)\]/.exec(r.rest);
  if (m) {
    r.int = Number(m[1]);
    r.d = Number(m[2]);
    r.rest = r.rest.replace(m[0], "");
  }
  const first = /FIRST \+[\d.]+xp/.exec(r.rest);
  if (first) {
    r.flags.push("first");
    r.rest = r.rest.replace(first[0], "");
  }
  for (const [t, f] of [["XP filler", "filler"], ["INT-UP: discard the result now", "discard"], ["INT-UP: keep the result (tool)", "keep"]] as const) {
    if (r.rest.includes(t)) {
      r.flags.push(f);
      r.rest = r.rest.replace(t, "");
    }
  }
  const w = /WEAR (\w+)/.exec(r.rest);
  if (w) {
    r.wear = w[1] ?? "";
    r.rest = r.rest.replace(w[0], "");
  }
  r.rest = r.rest.replace(/\s+/g, " ").trim();
  return r;
}

const slotName = (s: string) => ({ torsofront: "torso", thighback: "thigh", hat: "head", wraps: "limbs" })[s] ?? s;
const gateText = (t: string) => t.replace(/\(([^)]*)\)/, (_, l: string) => "(" + l.split(", ").map(nm).join(", ") + ")");
const isBN = (x: Step) => !!(x.bottleneck || x.breaks);

// ---------------------------------------------------------------- settings

// The settings objects that map an item id to a value. All other objects have fixed keys.
const records = new Set(["biochemHolders", "raws", "toolCandidates", "qualityProviders", "liquidQualities", "magazines"]);
const elemOf: Record<string, unknown> = { extraGathers: { layer: 1, item: "", count: 1 } };

const sameKind = (t: unknown, v: unknown) => typeof t === typeof v && v !== null && Array.isArray(t) === Array.isArray(v);

// fitTo returns v in the shape of the template t: a value of the wrong type becomes the template value, a number
// text becomes a number, and keys that t does not have go away.
function fitTo(t: unknown, v: unknown, key: string): unknown {
  if (typeof t === "number") {
    const n = typeof v === "string" && v.trim() !== "" ? Number(v) : v;
    return typeof n === "number" && Number.isFinite(n) ? n : t;
  }
  if (typeof t !== "object" || t === null) return sameKind(t, v) ? v : t;
  if (Array.isArray(t)) {
    if (!Array.isArray(v)) return clone(t);
    const et = elemOf[key] ?? t[0] ?? "";
    return v.filter((e) => sameKind(et, e)).map((e) => fitTo(et, e, ""));
  }
  if (!sameKind(t, v)) return clone(t);
  const tt = t as Record<string, unknown>;
  const vv = v as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  if (records.has(key)) {
    const et = Object.values(tt)[0];
    if (et === undefined) return clone(t);
    for (const [k, e] of Object.entries(vv)) if (sameKind(et, e)) out[k] = fitTo(et, e, k);
    return out;
  }
  for (const k of Object.keys(tt)) out[k] = k in vv ? fitTo(tt[k], vv[k], k) : clone(tt[k]);
  return out;
}

const clampTo = (v: number, lo: number, hi: number, int = true) => Math.min(hi, Math.max(lo, int ? Math.round(v) : v));

// merge makes settings from base and the values in v. Each value gets the type of base and the planner limits.
function merge(base: Settings, v: unknown): Settings {
  const s = fitTo(base, v, "") as Settings;
  const a = s.assumptions;
  for (const [path, , lo, top, int] of limits) {
    const [o, k] = field(s, path);
    o[k] = clampTo(o[k] as number, lo, top === "layers" ? a.layers : top, int);
  }
  for (const [k, vs] of Object.entries(choices)) {
    const o = s.options as unknown as Record<string, string>;
    if (!vs.includes(o[k]!)) o[k] = vs[0]!;
  }
  for (const r of Object.values(s.raws)) {
    r.cond = clampTo(r.cond, 0.01, 1, false);
    r.layers = r.layers.filter((l) => Number.isInteger(l) && l >= 1 && l <= a.layers);
  }
  for (const e of a.extraGathers) {
    e.layer = clampTo(e.layer, 1, a.layers);
    e.count = clampTo(e.count, 1, 99);
  }
  for (const k of Object.keys(a.biochemHolders)) a.biochemHolders[k] = clampTo(a.biochemHolders[k]!, 0, 100000, false);
  for (const m of Object.values(s.game.magazines)) m.capacity = clampTo(m.capacity, 1, 1000);
  return s;
}

function loadSettings(): Settings {
  try {
    const s = localStorage.getItem(LS.settings);
    if (s) return merge(D.defaults, JSON.parse(s));
  } catch {
    /* no storage or bad JSON */
  }
  return clone(D.defaults);
}

let S: Settings = loadSettings();

function saveSettings(): void {
  try {
    localStorage.setItem(LS.settings, JSON.stringify(S));
  } catch {
    /* no storage */
  }
}

const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const unb64 = (s: string) => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));

async function pipe(b: Uint8Array<ArrayBuffer>, t: CompressionStream | DecompressionStream): Promise<Uint8Array<ArrayBuffer>> {
  const out = new Response(new Blob([b]).stream().pipeThrough(t));
  return new Uint8Array(await out.arrayBuffer());
}

async function encodeShare(s: Settings): Promise<string> {
  const raw = new TextEncoder().encode(JSON.stringify(s));
  if (typeof CompressionStream === "function") return "z." + b64(await pipe(raw, new CompressionStream("deflate-raw")));
  return "j." + b64(raw);
}

async function decodeShare(v: string): Promise<unknown> {
  let b = unb64(v.slice(2));
  if (v.startsWith("z.")) b = await pipe(b, new DecompressionStream("deflate-raw"));
  return JSON.parse(new TextDecoder().decode(b));
}

function parseLayers(t: string): number[] {
  const out = new Set<number>();
  for (const part of t.split(/[,\s]+/)) {
    const m = /^(\d+)(?:-(\d+))?$/.exec(part);
    if (!m) continue;
    const a = Number(m[1]);
    const b = Number(m[2] ?? m[1]);
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) out.add(i);
  }
  return [...out].sort((a, b) => a - b);
}

function layerText(ls: number[]): string {
  const parts: string[] = [];
  for (let i = 0; i < ls.length; i++) {
    let j = i;
    while (j + 1 < ls.length && ls[j + 1] === ls[j]! + 1) j++;
    parts.push(j > i ? `${ls[i]}-${ls[j]}` : String(ls[i]));
    i = j;
  }
  return parts.join(",");
}

// ---------------------------------------------------------------- planning

let P: Plan | null = null;
let cur = 0;
let view: "step" | "list" = "step";
let mode: "plan" | "setup" = "plan";
let reqID = 0;
let worker: Worker | null = null;

function status(t: string, err = false): void {
  const e = $("status");
  e.textContent = t;
  e.classList.toggle("err", err);
}

function getWorker(): Worker | null {
  if (worker) return worker;
  try {
    const url = URL.createObjectURL(new Blob([$("worker-src").textContent ?? ""], { type: "text/javascript" }));
    worker = new Worker(url);
  } catch {
    worker = null;
  }
  return worker;
}

let toPlan = false;
// notice is a problem to show with the next plan status.
let notice = "";

function onPlan(r: PlanReply): void {
  if (!r.ok) {
    status("Plan failed: " + r.error, true);
    return;
  }
  P = r.plan;
  const n = P.steps.length;
  status((notice ? notice + " " : "") + `Planned ${n} steps in ${Math.round(r.ms)} ms.` + (P.stopped ? " Stopped: " + P.stopped : ""), !!P.stopped || !!notice);
  notice = "";
  if (cur < 0 || cur >= n) cur = 0;
  setupPlanView();
  if (toPlan) mode = "plan";
  show(toPlan);
}

function planNow(switchView = true): void {
  toPlan = switchView;
  const bad = check(GAME, S);
  if (bad) {
    status((notice ? notice + " " : "") + "Settings problem: " + bad, true);
    return;
  }
  saveSettings();
  const id = ++reqID;
  status("Planning...");
  const w = getWorker();
  const fallback = () =>
    setTimeout(() => {
      const t = performance.now();
      try {
        onPlan({ ok: true, plan: runPlan(GAME, S), ms: performance.now() - t });
      } catch (e) {
        onPlan({ ok: false, error: e instanceof Error ? e.message : String(e) });
      }
    }, 20);
  if (!w) {
    fallback();
    return;
  }
  w.onmessage = (ev: MessageEvent<PlanReply>) => {
    if (id === reqID) onPlan(ev.data);
  };
  w.onerror = (ev) => {
    ev.preventDefault();
    worker = null;
    if (id === reqID) fallback();
  };
  w.postMessage({ data: GAME, settings: S });
}

// ---------------------------------------------------------------- plan view

let PN: Note[] = [];
let bnIdx: number[] = [];

function xpGain(i: number): number {
  return P!.steps[i]!.after.xp - (i > 0 ? P!.steps[i - 1]!.after.xp : P!.startXp);
}

function bnHTML(x: Step): string {
  let h = "";
  const ox = D.rules.otherXP;
  if (x.bottleneck) {
    const m = /INT (\d+) gate: (\d+) xp short(?: \((.*)\))?/.exec(x.bottleneck);
    h += `<div class="bn"><b>Bottleneck: ${esc(m ? `INT ${m[1]} gate, ${m[2]} xp short` : x.bottleneck)}</b>`;
    if (m) {
      const need = Number(m[2]);
      h += `<p>Blocked: ${esc((m[3] ?? "").split(", ").map(nm).join(", "))}.</p>` +
        `<p>How to clear: no safe first-craft is left on this layer, so the plan descends (+${esc(S.assumptions.descendXP)} xp each layer, to layer ${esc(P!.maxLayer)} at most) until INT ${esc(m[1])}. ` +
        `You can clear it sooner with XP the planner does not count: ePDA +${esc(ox["ePDA"])} (about ${Math.ceil(need / (ox["ePDA"] ?? 35))}), blueprint +${esc(ox["blueprint"])} (about ${Math.ceil(need / (ox["blueprint"] ?? 25))}), ` +
        `or first-crafts of biome foods/items +${esc(ox["firstCraftFood"])} each (about ${Math.ceil(need / (ox["firstCraftFood"] ?? 10))}).</p>`;
    }
    h += "</div>";
  }
  if (x.breaks) {
    const cnt: Record<string, number> = {};
    for (const mm of x.breaks.matchAll(/(\S+) BREAKS/g)) cnt[mm[1]!] = (cnt[mm[1]!] ?? 0) + 1;
    const ks = Object.keys(cnt);
    h += `<div class="bn"><b>Tool breaks: ${ks.map((k) => esc(nm(k)) + " ×" + cnt[k]).join(", ")}</b>` +
      `<p>How to clear: each one is destroyed while used as a tool in this craft or its sub-crafts. Have ${ks.map((k) => cnt[k] + " extra " + esc(nm(k))).join(", ")} ready before you start.</p></div>`;
  }
  return h;
}

function liqText(a: Snap): string {
  const p: string[] = [];
  if (a.bioIn) p.push(`${fmt(a.bioMl ?? 0)} mL ${esc(nm("biochem"))} in ${esc(nm(a.bioIn))}`);
  for (const [k, v] of Object.entries(a.liq)) p.push(`${fmt(v)} mL ${esc(nm(k))}${a.jug ? " in " + esc(nm(a.jug)) : ""}`);
  return p.length ? p.join("<br>") : '<span style="color:var(--mut);font-weight:400">none</span>';
}

function invHTML(i: number): string {
  const x = P!.steps[i]!;
  const a = x.after;
  const pos: Record<string, boolean> = {};
  for (const [k, v] of Object.entries(x.delta)) if (v > 0) pos[bid(k)] = true;
  const lc = a.load / a.cap;
  let h = "<h3>Inventory after this step</h3>";
  h += '<div class="stat">' +
    `<div class="sbox"><div class="l">Carry</div><div class="v">${fmt(Math.round(a.load * 100) / 100)} / ${fmt(a.cap)}u</div>${meter(a.load, a.cap, lc > 0.95 ? "b" : lc > 0.8 ? "w" : "ok")}</div>` +
    `<div class="sbox"><div class="l">INT / XP</div><div class="v">INT ${a.int} <span style="color:var(--mut);font-weight:400">${fmt(a.xp)}/${fmt(a.nextXp)}</span></div>${meter(a.xp, a.nextXp)}</div>` +
    `<div class="sbox"><div class="l">Layer</div><div class="v">L${a.layer}</div></div>` +
    `<div class="sbox"><div class="l">Liquids</div><div class="v" style="font-size:.85rem">${liqText(a)}</div></div></div>`;
  h += '<h3>Hands &amp; mouth</h3><div class="slots">' + tile(a.handR, { slot: "right hand", tool: true, isNew: !!a.handR && !!pos[a.handR.id] }) +
    tile(a.handL, { slot: "left hand", isNew: !!a.handL && !!pos[a.handL.id] }) + tile(a.mouth, { slot: "mouth" }) + "</div>";
  h += "<h3>Worn gear</h3>";
  h += a.worn.length ? '<div class="grid">' + a.worn.map((w) => tile({ id: w.id, n: 1, conds: [w.cond] }, { slot: slotName(w.slot), tool: true, isNew: !!pos[w.id] })).join("") + "</div>" : '<div class="empty">nothing worn yet</div>';
  h += "<h3>Bags</h3>";
  for (const b of a.bags) {
    const f = b.used / b.max;
    h += `<div class="bag"><div class="bagh">${icon(b.id, 28)}<div class="bn2">${esc(nm(b.id))}<small>${esc(b.where)} · weight x${fmt(b.enc)}${b.tags ? " · " + esc(b.tags.replace(/,/g, ", ")) : ""}</small></div><div class="f">${fmt(b.used)} / ${fmt(b.max)}u</div></div>` +
      meter(b.used, b.max, f > 0.95 ? "b" : f > 0.8 ? "w" : "ok");
    h += b.items.length ? '<div class="grid" style="margin-top:8px">' + b.items.map((c) => tile(c, { isNew: !!pos[c.id] })).join("") + "</div>" : '<div class="empty">empty</div>';
    h += "</div>";
  }
  if (!a.bags.length) h += '<div class="empty">no bags</div>';
  if (a.over.length) {
    h += `<div class="warnbox"><b style="color:var(--bad)">No room for:</b><div class="grid" style="margin-top:6px">${a.over.map((c) => tile(c)).join("")}</div></div>`;
  }
  return h;
}

function stepHTML(i: number): string {
  const x = P!.steps[i]!;
  const p = PN[i]!;
  const a = x.after;
  const desc = x.kind === "descend";
  let h = '<article class="card" id="card">';
  h += `<div class="top"><div class="num">${x.step}</div><div class="act"><div class="verb">${desc ? "Descend" : Object.keys(x.gathered).length ? "Gather, then craft" : "Craft"} · layer ${a.layer}</div>`;
  if (desc) h += `<div class="what">${esc(x.name.replace(/^to /, "To "))}</div>`;
  else h += `<div class="what">${icon(x.name, 32)}<span>${esc(nm(x.name))}</span>${x.times > 1 ? `<span class="x">×${x.times}</span>` : ""}</div>`;
  h += '</div></div><div class="tags">';
  if (p.int !== null) h += `<span class="tag ${p.d >= 2 ? "ok" : p.d >= 0 ? "w" : "b"}">Recipe INT ${p.int} · d${p.d >= 0 ? "+" : ""}${p.d}</span>`;
  const g = xpGain(i);
  if (g) h += `<span class="tag a">+${fmt(g)} xp</span>`;
  if (p.flags.includes("first")) h += '<span class="tag">first craft</span>';
  if (p.flags.includes("filler")) h += '<span class="tag">XP filler</span>';
  if (p.flags.includes("discard")) h += '<span class="tag w">INT-up craft: discard result</span>';
  if (p.flags.includes("keep")) h += '<span class="tag">INT-up craft: keep the tool</span>';
  if (p.wear) h += `<span class="tag ok">wear: ${esc(p.wear)}</span>`;
  if (x.usedUp) {
    const cnt: Record<string, number> = {};
    for (const k of x.usedUp.split(" ")) cnt[k] = (cnt[k] ?? 0) + 1;
    h += `<span class="tag">used up: ${Object.entries(cnt).map(([k, n]) => esc(nm(k)) + (n > 1 ? " ×" + n : "")).join(", ")}</span>`;
  }
  if (isBN(x)) h += '<span class="tag b">bottleneck</span>';
  if (!x.goal && !desc) h += '<span class="tag">extra</span>';
  h += "</div>";
  if (p.rest) h += `<div class="note">${esc(p.rest)}</div>`;
  h += bnHTML(x);
  if (desc) {
    const em = chips(x.delta, "neg", (v) => v < 0);
    h += '<h3>Take everything down</h3><div class="empty">You carry all items to the next layer.</div>' + (em ? "<h3>Empty before you descend</h3>" + em : "");
  } else {
    if (Object.keys(x.gathered).length) h += "<h3>Gather first</h3>" + chips(x.gathered);
    h += "<h3>Needs (consumed)</h3>" + (chips(x.used, "neg") || '<div class="empty">nothing</div>');
    if (x.incl && Object.keys(x.incl).length) h += "<h3>Includes sub-crafts</h3>" + chips(x.incl);
    const made: Record<string, number> = {};
    for (const [k, v] of Object.entries(x.delta)) if (v > 0) made[k] = v;
    if (Object.keys(made).length) h += (p.flags.includes("discard") ? "<h3>Result (drop it now, only the XP counts)</h3>" : "<h3>Result</h3>") + chips(made, "pos");
    const tk = Object.keys(x.toolWear);
    const wearChips = (o: Record<string, number>, ks: string[]) => '<div class="chips">' + ks.map((k) => `<span class="chip">${icon(k, 24)}<span class="n">${esc(nm(k))}</span><b style="color:var(--warn)">-${fmt(Math.round(o[k]! * 1000) / 10)}%</b></span>`).join("") + "</div>";
    if (tk.length) h += "<h3>Tool wear</h3>" + wearChips(x.toolWear, tk);
    const mk = Object.keys(x.matWear);
    if (mk.length) h += "<h3>Materials used (kept slot)</h3>" + wearChips(x.matWear, mk);
  }
  return h + invHTML(i) + "</article>";
}

function listHTML(): string {
  let h = "";
  let L = -1;
  P!.steps.forEach((x, i) => {
    const a = x.after;
    const p = PN[i]!;
    if (a.layer !== L) {
      L = a.layer;
      h += `<div class="lh">Layer ${L}</div>`;
    }
    const t = x.kind === "descend" ? esc(x.name.replace(/^to /, "Descend to ")) : esc(nm(x.name)) + (x.times > 1 ? " ×" + x.times : "");
    const g = xpGain(i);
    const hands = [a.handR, a.handL, a.mouth].filter((c) => c).map((c) => esc(nm(c!.id))).join(", ") || "empty";
    const items = a.bags.reduce((n, b) => n + b.items.reduce((m, c) => m + c.n, 0), 0);
    h += `<div class="lr" data-row="${i}"><button class="li" data-i="${i}"><span class="num">${x.step}</span>${x.kind === "descend" ? "" : icon(x.name, 28)}<span class="lt"><b>${t}</b><span>` +
      `${p.int !== null ? `Recipe INT ${p.int} d${p.d >= 0 ? "+" : ""}${p.d} · ` : ""}INT ${a.int} · ${fmt(a.xp)}/${fmt(a.nextXp)} xp${g ? ` (+${fmt(g)})` : ""} · L${a.layer} · ${fmt(Math.round(a.load * 100) / 100)}/${fmt(a.cap)}u</span></span>${isBN(x) ? '<span class="dot" title="bottleneck"></span>' : ""}</button>` +
      `<details class="sub" data-inv="${i}"><summary>Hands: ${hands} · ${a.worn.length} worn · ${a.bags.length} bag${a.bags.length === 1 ? "" : "s"}, ${items} item${items === 1 ? "" : "s"}${a.over.length ? ' · <b style="color:var(--bad)">no room</b>' : ""}</summary></details></div>`;
  });
  return h;
}

function sumHTML(): string {
  const st = P!.steps;
  if (!st.length) return '<div class="empty">The plan has no steps.</div>';
  const last = st[st.length - 1]!.after;
  const raw: Record<string, number> = {};
  for (const x of st) for (const k of Object.keys(x.gathered).sort()) raw[k] = (raw[k] ?? 0) + x.gathered[k]!;
  for (const id of S.assumptions.startItems) raw[id] = (raw[id] ?? 0) + 1;
  const rawSorted: Record<string, number> = {};
  for (const [k, v] of Object.entries(raw).sort((a, b) => b[1] - a[1])) rawSorted[k] = v;
  const gates = st.filter((x) => x.bottleneck);
  const breaks = st.filter((x) => x.breaks);
  let h = `<div class="kv"><span class="pill">${st.length} steps</span><span class="pill">ends layer ${last.layer} (limit ${P!.maxLayer})</span><span class="pill">INT ${last.int} (${fmt(last.xp)} xp)</span>` +
    `<span class="pill">load ${fmt(Math.round(last.load * 100) / 100)}/${fmt(last.cap)}u</span><span class="pill">${gates.length} INT gates</span>${P!.lost.length ? `<span class="pill">${P!.lost.length} targets used up later</span>` : ""}</div>`;
  h += '<h3>Final kit, worn</h3><div class="grid">' + last.worn.map((w) => tile({ id: w.id, n: 1, conds: [w.cond] }, { slot: slotName(w.slot), tool: true })).join("") + "</div>";
  const carried: Cell[] = [];
  for (const c of [last.handR, last.handL, last.mouth]) if (c) carried.push(c);
  for (const b of last.bags) carried.push(...b.items);
  h += '<h3>Final kit, carried</h3><div class="grid">' + carried.map((c, k) => tile(c, { tool: k === 0 && c === last.handR })).join("") + "</div>";
  h += "<h3>Total raw resources</h3>" + chips(rawSorted);
  h += '<h3>Bottlenecks</h3><div class="bl">' +
    gates.map((x) => `<button data-i="${x.step - 1}"><b>Step ${x.step}</b> · L${x.kind === "descend" ? x.after.layer - 1 : x.after.layer}: ${esc(gateText(x.bottleneck!))}</button>`).join("") +
    breaks.map((x) => `<button data-i="${x.step - 1}"><b>Step ${x.step}</b> · ${esc(nm(x.name))}: tool breaks (${esc(x.breaks!.replace(/ BREAKS/g, ",").replace(/,$/, ""))})</button>`).join("") + "</div>";
  return h;
}

function missHTML(): string {
  const M = P!.notReached;
  if (!M.length) return `<div class="okbox">All targets reached by layer ${P!.maxLayer}.</div>`;
  return `<details class="warnbox mbox"${innerWidth >= 700 ? " open" : ""}><summary><b style="color:var(--bad)">${M.length} target${M.length > 1 ? "s" : ""} not reached by layer ${P!.maxLayer}</b></summary>` + '<div class="misses">' + M.map((m) => `<div class="miss">${icon(m.id, 24)}<span><b>${esc(nm(m.id))}</b>${m.n > 1 ? ` (${m.done}/${m.n})` : ""} · blocked by ${esc(m.block)}<br><span class="why">${esc(m.why)}</span></span></div>`).join("") + "</div></details>";
}

// lostHTML shows the reached targets that later steps use up, and the planner warnings.
function lostHTML(): string {
  const L = P!.lost;
  const W = P!.warnings;
  if (!L.length && !W.length) return "";
  let h = '<div class="warnbox">';
  if (L.length) {
    h += `<b style="color:var(--warn)">${L.length} reached target${L.length > 1 ? "s are" : " is"} used up later, so the final kit does not have ${L.length > 1 ? "them" : "it"}</b><div class="misses">` +
      L.map((l) => {
        const by = l.by.map((n) => `<button class="lnk" data-i="${n - 1}">step ${n}, ${esc(nm(P!.steps[n - 1]!.name))}</button>`).join(", ");
        return `<div class="miss">${icon(l.id, 24)}<span><b>${esc(nm(l.id))}</b> · made ${esc(l.made)}, ${esc(l.left)} left<br><span class="why">used in ${by || "a later step"}</span></span></div>`;
      }).join("") + "</div>";
  }
  for (const w of W) h += `<p class="why">${esc(w)}</p>`;
  return h + "</div>";
}

function setupPlanView(): void {
  if (!P) return;
  PN = P.steps.map((x) => parseNote(x.note));
  bnIdx = P.steps.flatMap((x, i) => (isBN(x) ? [i] : []));
  $("title").innerHTML = `Crafting plan<small>${P.steps.length} steps · to layer ${esc(P.maxLayer)}</small>`;
  $("sumHead").textContent = "Summary · final kit, raw totals, bottlenecks";
  $("sumBody").innerHTML = sumHTML();
  $("miss").innerHTML = missHTML() + lostHTML();
  ($("slider") as HTMLInputElement).max = String(P.steps.length);
}

function saveNav(): void {
  try {
    localStorage.setItem(LS.step, String(cur));
    localStorage.setItem(LS.view, view);
  } catch {
    /* no storage */
  }
}

// listOf is the plan that the list view in #main shows. show does not draw the list again for the same plan, so
// opened rows stay open.
let listOf: Plan | null = null;

function show(scroll = true): void {
  const setup = mode === "setup";
  const N = P ? P.steps.length : 0;
  $("setup").classList.toggle("hide", !setup);
  $("planv").classList.toggle("hide", setup);
  $("navbar").classList.toggle("hide", setup || N === 0);
  $("setupbar").classList.toggle("hide", !setup);
  $("viewBtn").classList.toggle("hide", setup || N === 0);
  $("setupBtn").setAttribute("aria-pressed", String(setup));
  $("setupBtn").textContent = setup ? "Plan view" : "Setup";
  if (setup) {
    try {
      renderSetup();
    } catch (e) {
      $("setup").innerHTML = `<div class="sec"><h2>Settings problem</h2><p class="hint">${esc(e instanceof Error ? e.message : e)}</p>` +
        '<div class="brow2"><button class="btn p" id="resetBtn">Reset to defaults</button></div></div>';
    }
    return;
  }
  if (!P || N === 0) {
    listOf = null;
    $("main").innerHTML = P ? '<div class="empty">The plan has no steps. Look at the targets that the plan does not reach.</div>' : "";
    return;
  }
  const x = P.steps[cur]!;
  $("viewBtn").textContent = view === "list" ? "Step view" : "List";
  $("viewBtn").setAttribute("aria-pressed", String(view === "list"));
  if (view === "list") {
    const m = $("main");
    if (listOf !== P) {
      m.innerHTML = listHTML();
      listOf = P;
    }
    m.querySelector(".lr.cur")?.classList.remove("cur");
    const c = m.querySelector(`.lr[data-row="${cur}"]`);
    c?.classList.add("cur");
    if (c && scroll) c.scrollIntoView({ block: "center" });
  } else {
    listOf = null;
    $("main").innerHTML = stepHTML(cur);
    if (scroll) {
      const r = $("main").getBoundingClientRect();
      if (r.top < 0) window.scrollTo(0, window.scrollY + r.top - 8);
    }
  }
  $("ctrN").textContent = `Step ${cur + 1} / ${N}`;
  $("ctrS").textContent = `L${x.after.layer} · INT ${x.after.int}` + (isBN(x) ? " · ⚠ bottleneck" : " · jump ▴");
  ($("slider") as HTMLInputElement).value = String(cur + 1);
  ($("prev") as HTMLButtonElement).disabled = cur === 0;
  ($("next") as HTMLButtonElement).disabled = cur === N - 1;
  history.replaceState(null, "", "#" + (cur + 1));
  saveNav();
}

function go(i: number, scroll = true): void {
  if (!P || !P.steps.length) return;
  cur = Math.max(0, Math.min(P.steps.length - 1, i));
  show(scroll);
}

function goStep(i: number): void {
  view = "step";
  mode = "plan";
  go(i);
  window.scrollTo(0, 0);
}

function nextBN(dir: number): void {
  const c = dir > 0 ? bnIdx.find((i) => i > cur) : bnIdx.filter((i) => i < cur).pop();
  if (c !== undefined) goStep(c);
}

function openSheet(): void {
  if (!P || !P.steps.length) return;
  const st = P.steps;
  const ls: [number, number][] = [];
  let L = -1;
  st.forEach((x, i) => {
    if (x.after.layer !== L) {
      L = x.after.layer;
      ls.push([L, i]);
    }
  });
  const curL = st[cur]!.after.layer;
  $("jlayers").innerHTML = ls.map(([l, i], k) => {
    const end = k + 1 < ls.length ? ls[k + 1]![1] : st.length;
    return `<button data-i="${i}"${l === curL ? ' class="cur"' : ""}>Layer ${l}<span>${end - i > 1 ? `steps ${i + 1}-${end}` : `step ${end}`}</span></button>`;
  }).join("");
  $("jbn").innerHTML = bnIdx.map((i) => {
    const x = st[i]!;
    return `<button data-i="${i}"><b>Step ${x.step}</b> · ${esc(x.bottleneck ? gateText(x.bottleneck) : nm(x.name) + ": tool breaks")}</button>`;
  }).join("");
  const j = $("jnum") as HTMLInputElement;
  j.max = String(st.length);
  j.value = String(cur + 1);
  j.placeholder = "1-" + st.length;
  $("sheet").classList.add("open");
}

const closeSheets = () => document.querySelectorAll(".sheet.open").forEach((e) => e.classList.remove("open"));

// ---------------------------------------------------------------- setup view

const craftable = Object.keys(D.items).filter((id) => D.recipes.some((r) => r.result === id && !r.repair && !r.liquid));
const allItems = Object.keys(D.items);
let pickCB: ((id: string) => void) | null = null;
let pickIDs: string[] = [];

function openPicker(title: string, ids: string[], cb: (id: string) => void): void {
  pickCB = cb;
  pickIDs = ids.slice().sort((a, b) => nm(a).localeCompare(nm(b)));
  $("pickTitle").textContent = title;
  const q = $("pickQ") as HTMLInputElement;
  q.value = "";
  renderPicker();
  $("picker").classList.add("open");
  setTimeout(() => q.focus(), 0);
}

function renderPicker(): void {
  const q = ($("pickQ") as HTMLInputElement).value.trim().toLowerCase();
  const ids = pickIDs.filter((id) => !q || id.includes(q) || nm(id).toLowerCase().includes(q)).slice(0, 120);
  $("pickList").innerHTML = ids.map((id) => {
    const r = D.recipes.find((r) => r.result === id && !r.repair);
    return `<button data-pick="${esc(id)}">${icon(id, 32)}<span>${esc(nm(id))}</span>${r ? `<small>INT ${r.int}</small>` : ""}</button>`;
  }).join("") || '<div class="empty">no match</div>';
}

interface Target {
  id: string;
  n: number;
}

function targetList(): Target[] {
  return S.targets.map((t) => {
    const [id, n] = t.split("*");
    return { id: id ?? "", n: Number(n ?? 1) || 1 };
  });
}

function setTargets(ts: Target[]): void {
  S.targets = ts.map((t) => (t.n > 1 ? `${t.id}*${t.n}` : t.id));
}

const num = (id: string, label: string, v: number, attrs = "") =>
  `<label class="fld">${esc(label)}<input type="number" data-num="${id}" value="${esc(v)}" ${attrs}></label>`;
const chk = (id: string, label: string, on: boolean) => `<label class="chk"><input type="checkbox" data-chk="${id}"${on ? " checked" : ""}>${esc(label)}</label>`;
function sel(id: string, label: string, v: string, opts: [string, string][]): string {
  return `<label class="fld">${esc(label)}<select data-sel="${id}">${opts.map(([k, t]) => `<option value="${esc(k)}"${k === v ? " selected" : ""}>${esc(t)}</option>`).join("")}</select></label>`;
}

function idChips(list: string, ids: string[], addLabel: string): string {
  return `<div class="brow2">${ids.map((id, i) => `<span class="cchip">${icon(id, 24)}${esc(nm(id))}<button data-rm="${list}" data-k="${i}" aria-label="Remove ${esc(nm(id))}">✕</button></span>`).join("")}` +
    `<button class="btn" data-add="${list}">${esc(addLabel)}</button></div>`;
}

function renderSetup(): void {
  const a = S.assumptions;
  const o = S.options;
  const ts = targetList();
  let h = '<div class="setupcols"><div>';
  h += `<div class="sec"><h2>Targets <small>${ts.length} items, crafted in this order</small></h2>`;
  h += `<div class="brow2">${Object.keys(D.presets).map((k) => `<button class="btn" data-preset="${esc(k)}">${esc(k)}</button>`).join("")}<button class="btn" data-clear="targets">Clear</button></div>`;
  h += '<div style="margin-top:8px">' + ts.map((t, i) => `<div class="trow">${icon(t.id, 28)}<span class="tnm">${esc(nm(t.id))} <small>${esc(t.id)}</small></span>` +
    `<input class="cnt" type="number" min="1" max="99" value="${esc(t.n)}" data-tn="${i}" aria-label="Count">` +
    `<button class="ibtn" data-tmv="${i}" data-d="-1" aria-label="Move up">▲</button><button class="ibtn" data-tmv="${i}" data-d="1" aria-label="Move down">▼</button>` +
    `<button class="ibtn x" data-trm="${i}" aria-label="Remove">✕</button></div>`).join("") + "</div>";
  h += '<div class="brow2"><button class="btn p" data-add="targets">+ Add target</button></div></div>';

  h += '<div class="sec"><h2>Character</h2><div class="fgrid">' +
    num("assumptions.startINT", "Start INT", a.startINT, 'min="0" max="40"') +
    num("assumptions.carryCap", "Carry cap (units)", a.carryCap, 'min="1" step="0.5"') +
    num("assumptions.backSlots", "Back slots", a.backSlots, 'min="0" max="6"') +
    sel("assumptions.maxLayer", "Layer limit", String(a.maxLayer), Array.from({ length: a.layers }, (_, i) => [String(i + 1), `Layer ${i + 1}`])) +
    num("assumptions.firstCraftXP", "XP per first craft", a.firstCraftXP, 'min="0"') +
    num("assumptions.descendXP", "XP per descent", a.descendXP, 'min="0"') +
    "</div></div>";

  h += '<div class="sec"><h2>Start items</h2>' + idChips("startItems", a.startItems, "+ Add start item") +
    '<p class="hint">Light items, best first. One hand always holds the best light you have.</p>' + idChips("lights", a.lights, "+ Add light") +
    '<div class="fgrid" style="margin-top:8px">' +
    sel("assumptions.liquidHolder", "Water holder", a.liquidHolder, allItems.filter((id) => (D.items[id]?.capacity ?? 0) > 0).map((id) => [id, nm(id)])) +
    sel("assumptions.storageCraft", "Bag to craft when full", a.storageCraft, S.game.bags.map((id) => [id, nm(id)])) +
    num("assumptions.biochemLastLayer", "Last biochem layer", a.biochemLastLayer, 'min="1"') + "</div>" +
    chk("options.biochemFull", "First canteen / IV bag always full of biochem", o.biochemFull) + "</div>";
  h += "</div><div>";

  h += '<div class="sec"><h2>Rules</h2>' +
    chk("options.fillers", "Craft extra items for first-craft XP (INT-up crafts)", o.fillers) +
    '<div class="fgrid">' +
    sel("options.guns", "Gun crafts for XP", o.guns, [["auto", "If a target is a gun"], ["always", "Always"], ["never", "Never"]]) +
    num("options.intCap", "INT cap (0 = none)", o.intCap, 'min="0" max="40"') + "</div>" +
    chk("options.safeInt", "Always craft with enough INT (d ≥ 0: no lost condition, no failure)", o.safeInt) +
    `<fieldset class="gset"${o.safeInt ? " disabled" : ""}>` +
    (o.safeInt ? '<p class="hint">Off while "Always craft with enough INT" is on.</p>' : "") +
    '<div class="fgrid">' +
    sel("options.gambleMode", "d = -1 gamble, last layer", o.gambleMode, [["list", "Armor list only"], ["all", "Any target"], ["off", "Never"]]) +
    num("assumptions.gambleCondition", "Gamble result condition (%)", Math.round(a.gambleCondition * 100), 'min="20" max="90"') + "</div>" +
    '<p class="hint">Armor list for the gamble:</p>' + idChips("gamble", S.upChain.gamble, "+ Add") + "</fieldset>" +
    '<p class="hint">Recipes the planner must not use:</p>' + idChips("disabled", o.disabled, "+ Turn off a recipe") +
    '<p class="hint">Never crafted only for XP:</p>' + idChips("neverFiller", S.upChain.neverFiller, "+ Add") +
    '<h3 class="hint" style="margin-top:12px">INT failure rules (game code)</h3>' +
    `<p class="info">You see a recipe at INT ≥ recipe INT - ${D.rules.intWindow}. d = INT - recipe INT.</p>` +
    D.rules.outcomes.map((x) => `<p class="info"><b>d ${x.d === 0 ? "≥ 0" : "= " + x.d}</b>: ${esc(x.text)}</p>`).join("") + "</div>";

  h += '<div class="sec"><h2>Resources and traps</h2><p class="hint">Layers where you can gather each raw, and its condition.</p><table class="rawt"><tbody>' +
    Object.entries(S.raws).map(([id, r]) => `<tr><td><input type="checkbox" data-rawon="${esc(id)}"${r.layers.length ? " checked" : ""} aria-label="Use ${esc(nm(id))}"></td>` +
      `<td class="n"><span class="nm">${icon(id, 24)}<span>${esc(nm(id))}</span></span></td>` +
      `<td><input type="text" data-rawl="${esc(id)}" value="${esc(layerText(r.layers))}" aria-label="Layers"></td>` +
      `<td><input type="number" data-rawc="${esc(id)}" value="${esc(Math.round(r.cond * 100))}" min="1" max="100" aria-label="Condition %"></td></tr>`).join("") +
    "</tbody></table>";
  const traps = Object.values(D.buildings).filter((b) => b.drops.some((d) => d.id === "circuitboard") && D.rules.traps[b.id]);
  h += '<p class="hint">Circuit boards from traps (game prefabs):</p>' + traps.map((b) => {
    const d = b.drops.find((d) => d.id === "circuitboard")!;
    const tr = D.rules.traps[b.id]!;
    return `<p class="info"><b>${esc(tr.name)}</b>: ${Math.round(d.chance * 100)}% at ${Math.round(d.min * 100)}-${Math.round(d.max * 100)}%, layers ${esc(layerText(tr.layers))}</p>`;
  }).join("");
  h += '<p class="hint">Carry extra items down from a layer (for layers without the source):</p>' +
    a.extraGathers.map((e, i) => `<div class="trow">${icon(e.item, 24)}<span class="tnm">${esc(nm(e.item))}</span>` +
      `<label class="fld" style="flex-direction:row;align-items:center">L<input class="cnt" type="number" min="1" max="${esc(a.layers)}" value="${esc(e.layer)}" data-eg="${i}" data-f="layer" aria-label="Layer"></label>` +
      `<label class="fld" style="flex-direction:row;align-items:center">×<input class="cnt" type="number" min="1" max="99" value="${esc(e.count)}" data-eg="${i}" data-f="count" aria-label="Count"></label>` +
      `<button class="ibtn x" data-egrm="${i}" aria-label="Remove">✕</button></div>` +
      (e.layer >= a.maxLayer ? `<p class="hint">Not used: the plan does not go below layer ${esc(a.maxLayer)} (the layer limit).</p>` : "")).join("") +
    '<div class="brow2"><button class="btn" data-add="extraGathers">+ Add</button></div></div>';

  h += '<div class="sec"><h2>Settings file</h2><div class="brow2">' +
    '<button class="btn" id="exportBtn">Export JSON</button><button class="btn" id="importBtn">Import JSON</button>' +
    '<button class="btn" id="shareBtn">Copy share link</button><button class="btn" id="resetBtn">Reset to defaults</button></div>' +
    '<details class="sub"><summary>Edit all settings as JSON</summary>' +
    `<textarea class="json" id="jsonBox" spellcheck="false" aria-label="Settings JSON">${esc(JSON.stringify(S, null, 1))}</textarea>` +
    '<div class="brow2"><button class="btn p" id="jsonApply">Apply JSON</button></div></details></div>';
  h += "</div></div>";
  $("setup").innerHTML = h;
}

function setPath(path: string, v: unknown): void {
  const [sec, key] = path.split(".") as [keyof Settings, string];
  (S[sec] as unknown as Record<string, unknown>)[key] = v;
}

function changed(): void {
  saveSettings();
  renderSetup();
}

function onSetupInput(e: Event): void {
  const t = e.target as HTMLInputElement;
  const d = t.dataset;
  if (d["num"]) {
    let v = Number(t.value);
    if (!Number.isFinite(v)) return;
    if (d["num"] === "assumptions.gambleCondition") v = Math.min(0.9, Math.max(0.2, v / 100));
    setPath(d["num"], v);
  } else if (d["sel"]) {
    setPath(d["sel"], d["sel"] === "assumptions.maxLayer" ? Number(t.value) : t.value);
  } else if (d["chk"]) {
    setPath(d["chk"], t.checked);
    if (d["chk"] === "options.safeInt") {
      changed();
      return;
    }
  } else if (d["tn"] !== undefined) {
    const ts = targetList();
    const x = ts[Number(d["tn"])];
    if (x) x.n = Math.max(1, Math.min(99, Math.round(Number(t.value)) || 1));
    setTargets(ts);
  } else if (d["rawon"]) {
    const r = S.raws[d["rawon"]];
    if (r) r.layers = t.checked ? (D.defaults.raws[d["rawon"]]?.layers ?? [1]).slice() : [];
    changed();
    return;
  } else if (d["rawl"]) {
    const r = S.raws[d["rawl"]];
    if (r) r.layers = parseLayers(t.value);
  } else if (d["rawc"]) {
    const r = S.raws[d["rawc"]];
    if (r) r.cond = Math.min(1, Math.max(0.01, Number(t.value) / 100));
  } else if (d["eg"] !== undefined) {
    const g = S.assumptions.extraGathers[Number(d["eg"])];
    if (g && (d["f"] === "layer" || d["f"] === "count")) g[d["f"]] = clampTo(Number(t.value) || 1, 1, d["f"] === "layer" ? S.assumptions.layers : 99);
    changed();
    return;
  } else return;
  saveSettings();
}

const lists: Record<string, () => string[]> = {
  startItems: () => S.assumptions.startItems,
  lights: () => S.assumptions.lights,
  gamble: () => S.upChain.gamble,
  disabled: () => S.options.disabled,
  neverFiller: () => S.upChain.neverFiller,
};

function download(name: string, text: string): void {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
}

// applySettings uses the settings in v only when they pass the check.
function applySettings(v: unknown, msg: string): void {
  const s = merge(D.defaults, v);
  const bad = check(GAME, s);
  if (bad) {
    status("Settings not applied: " + bad, true);
    return;
  }
  S = s;
  saveSettings();
  show();
  status(msg);
}

async function onSetupClick(e: MouseEvent): Promise<void> {
  const b = (e.target as HTMLElement).closest("button");
  if (!b || !$("setup").contains(b)) return;
  const d = b.dataset;
  if (d["preset"]) {
    S.targets = (D.presets[d["preset"]] ?? []).slice();
    changed();
  } else if (d["clear"]) {
    S.targets = [];
    changed();
  } else if (d["trm"] !== undefined) {
    const ts = targetList();
    ts.splice(Number(d["trm"]), 1);
    setTargets(ts);
    changed();
  } else if (d["tmv"] !== undefined) {
    const ts = targetList();
    const i = Number(d["tmv"]);
    const j = i + Number(d["d"]);
    if (j >= 0 && j < ts.length) [ts[i], ts[j]] = [ts[j]!, ts[i]!];
    setTargets(ts);
    changed();
  } else if (d["rm"]) {
    lists[d["rm"]]?.().splice(Number(d["k"]), 1);
    changed();
  } else if (d["egrm"] !== undefined) {
    S.assumptions.extraGathers.splice(Number(d["egrm"]), 1);
    changed();
  } else if (d["add"] === "targets") {
    openPicker("Add a target", craftable, (id) => {
      S.targets.push(id);
      changed();
    });
  } else if (d["add"] === "extraGathers") {
    openPicker("Carry down an item", Object.keys(S.raws), (id) => {
      S.assumptions.extraGathers.push({ layer: 1, item: id, count: 1 });
      changed();
    });
  } else if (d["add"]) {
    const l = lists[d["add"]];
    const ids = d["add"] === "disabled" || d["add"] === "gamble" ? craftable : allItems;
    if (l) {
      openPicker("Add an item", ids, (id) => {
        if (!l().includes(id) || d["add"] === "startItems") l().push(id);
        changed();
      });
    }
  } else if (b.id === "exportBtn") {
    download("cuplan-settings.json", JSON.stringify(S, null, 1));
  } else if (b.id === "importBtn") {
    $("importFile").click();
  } else if (b.id === "resetBtn") {
    applySettings(D.defaults, "Settings reset to the defaults.");
  } else if (b.id === "jsonApply") {
    try {
      applySettings(JSON.parse(($("jsonBox") as HTMLTextAreaElement).value), "JSON settings applied.");
    } catch (err) {
      status("Bad JSON: " + (err as Error).message, true);
    }
  } else if (b.id === "shareBtn") {
    const url = location.href.split("#")[0] + "#cfg=" + (await encodeShare(S));
    try {
      await navigator.clipboard.writeText(url);
      status(`Share link copied (${url.length} characters).`);
    } catch {
      history.replaceState(null, "", url);
      status("Share link is in the address bar.");
    }
  }
}

// ---------------------------------------------------------------- events and start

function bind(): void {
  $("prev").onclick = () => go(cur - 1);
  $("next").onclick = () => go(cur + 1);
  $("slider").oninput = (e) => go(Number((e.target as HTMLInputElement).value) - 1, false);
  $("viewBtn").onclick = () => {
    view = view === "list" ? "step" : "list";
    show();
  };
  $("setupBtn").onclick = () => {
    mode = mode === "setup" ? "plan" : "setup";
    show();
    window.scrollTo(0, 0);
  };
  $("backBtn").onclick = () => {
    mode = "plan";
    show();
  };
  $("planBtn").onclick = () => planNow();
  $("ctr").onclick = openSheet;
  $("jnextB").onclick = () => {
    closeSheets();
    nextBN(1);
  };
  $("jprevB").onclick = () => {
    closeSheets();
    nextBN(-1);
  };
  $("jform").onsubmit = (e) => {
    e.preventDefault();
    const v = parseInt(($("jnum") as HTMLInputElement).value, 10);
    if (P && v >= 1 && v <= P.steps.length) {
      closeSheets();
      goStep(v - 1);
    }
  };
  $("pickQ").oninput = renderPicker;
  $("setup").addEventListener("change", onSetupInput);
  $("setup").addEventListener("click", (e) => void onSetupClick(e));
  $("importFile").onchange = async (e) => {
    const f = (e.target as HTMLInputElement).files?.[0];
    if (!f) return;
    try {
      applySettings(JSON.parse(await f.text()), "Settings imported from " + f.name + ".");
    } catch (err) {
      status("Bad settings file: " + (err as Error).message, true);
    }
    (e.target as HTMLInputElement).value = "";
  };
  document.addEventListener("click", (e) => {
    const t = e.target as HTMLElement;
    const close = t.closest("[data-close]");
    if (close) {
      closeSheets();
      return;
    }
    const p = t.closest("[data-pick]") as HTMLElement | null;
    if (p) {
      closeSheets();
      pickCB?.(p.dataset["pick"] ?? "");
      return;
    }
    const b = t.closest("[data-i]");
    if (b) {
      closeSheets();
      ($("sum") as HTMLDetailsElement).open = false;
      goStep(Number(b.getAttribute("data-i")));
    }
  });
  document.addEventListener("keydown", (e) => {
    const t = e.target as HTMLElement;
    if (e.key === "Escape") {
      closeSheets();
      return;
    }
    if ((t.tagName === "INPUT" && (t as HTMLInputElement).type !== "range") || t.tagName === "TEXTAREA" || t.tagName === "SELECT") return;
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    const k = e.key;
    if (k === "s" || k === "S") {
      $("setupBtn").click();
      return;
    }
    if (mode !== "plan" || !P) return;
    if (k === "ArrowRight") go(cur + 1);
    else if (k === "ArrowLeft") go(cur - 1);
    else if (k === "Home") go(0);
    else if (k === "End") go(P.steps.length - 1);
    else if (k === "j" || k === "J") {
      openSheet();
      setTimeout(() => ($("jnum") as HTMLInputElement).select(), 0);
      return;
    } else if (k === "l" || k === "L") {
      view = view === "list" ? "step" : "list";
      show();
      return;
    } else return;
    e.preventDefault();
  });
  // The list view renders a step's inventory only when its row is opened.
  $("main").addEventListener("toggle", (e) => {
    const d = e.target as HTMLDetailsElement;
    const i = d.dataset["inv"];
    if (d.open && i !== undefined && !d.querySelector("h3")) d.insertAdjacentHTML("beforeend", invHTML(Number(i)));
  }, true);
  let tx: number | null = null;
  let ty = 0;
  $("main").addEventListener("touchstart", (e) => {
    const t = e.touches[0];
    if (view !== "step" || e.touches.length !== 1 || !t) {
      tx = null;
      return;
    }
    tx = t.clientX;
    ty = t.clientY;
  }, { passive: true });
  $("main").addEventListener("touchend", (e) => {
    const t = e.changedTouches[0];
    if (tx === null || !t) return;
    const dx = t.clientX - tx;
    const dy = t.clientY - ty;
    tx = null;
    if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) go(cur + (dx < 0 ? 1 : -1));
  }, { passive: true });
}

async function start(): Promise<void> {
  bind();
  const h = location.hash;
  if (h.startsWith("#cfg=")) {
    history.replaceState(null, "", location.pathname + location.search);
    try {
      const s = merge(D.defaults, await decodeShare(h.slice(5)));
      const bad = check(GAME, s);
      if (bad) throw new Error(bad);
      S = s;
      saveSettings();
    } catch (e) {
      notice = "Share link not used: " + (e instanceof Error ? e.message : String(e)) + ".";
    }
  }
  try {
    const sv = parseInt(localStorage.getItem(LS.step) ?? "", 10);
    if (sv >= 0) cur = sv;
    if (localStorage.getItem(LS.view) === "list") view = "list";
  } catch {
    /* no storage */
  }
  const hm = /^#(\d+)$/.exec(location.hash);
  if (hm) cur = Math.max(0, Number(hm[1]) - 1);
  if (/^#setup$/.test(location.hash)) mode = "setup";
  show(false);
  planNow(mode === "plan");
}

void start();
