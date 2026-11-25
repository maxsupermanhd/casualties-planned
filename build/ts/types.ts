export interface Ing {
  id?: string;
  q?: string;
  amt?: number;
  liquid?: boolean;
  kept?: boolean;
  minCond: number;
  noMax?: boolean;
}

export interface Recipe {
  int: number;
  result: string;
  amount: number;
  cond: number;
  liquid?: boolean;
  category: string;
  repair?: boolean;
  items: Ing[];
}

export interface Qual {
  id: string;
  amt: number;
}

export interface ItemInfo {
  weight: number;
  capacity?: number;
  armor?: number;
  hitLoss?: number;
  slot?: string;
  limb?: string;
  destroyAt0?: boolean;
  combine?: boolean;
  scaleW?: boolean;
  tags?: string;
  category?: string;
  quals?: Qual[];
}

export interface Container {
  capacity: number;
  perItem: number;
  encumbrance: number;
  tags: string[];
}

export interface Drop {
  id: string;
  chance: number;
  min: number;
  max: number;
}

export interface Building {
  id: string;
  drops: Drop[];
  always?: Drop[];
}

export interface Raw {
  layers: number[];
  cond: number;
  note: string;
}

export interface Magazine {
  round: string;
  capacity: number;
}

export interface Rules {
  layers: number;
  carryCap: number;
  backSlots: number;
  firstCraftXP: number;
  descendXP: number;
  otherXP: Record<string, number>;
  intWindow: number;
  outcomes: { d: number; text: string }[];
  biochemLastLayer: number;
  raws: Record<string, Raw>;
  traps: Record<string, { name: string; layers: number[] }>;
  handsOnly: string[];
  toolQualities: string[];
  liquidQualities: Record<string, Record<string, number>>;
  magazines: Record<string, Magazine>;
  liquidNames: Record<string, string>;
}

export interface GameData {
  recipes: Recipe[];
  items: Record<string, ItemInfo>;
  containers: Record<string, Container>;
}

export interface PageData extends GameData {
  names: Record<string, string>;
  buildings: Record<string, Building>;
  rules: Rules;
  atlas: Record<string, [number, number, number, number]>;
  atlasSize: [number, number];
  defaults: Settings;
  presets: Record<string, string[]>;
}

export interface ExtraGather {
  layer: number;
  item: string;
  count: number;
}

export interface Assumptions {
  startINT: number;
  startItems: string[];
  carryCap: number;
  backSlots: number;
  lights: string[];
  liquidHolder: string;
  biochemHolders: Record<string, number>;
  biochemLastLayer: number;
  storageCraft: string;
  layers: number;
  maxLayer: number;
  firstCraftXP: number;
  descendXP: number;
  gambleCondition: number;
  extraGathers: ExtraGather[];
}

export interface UpChain {
  gunRecipes: string[];
  gamble: string[];
  neverFiller: string[];
}

export type GunMode = "auto" | "always" | "never";
export type GambleMode = "list" | "all" | "off";

export interface Options {
  fillers: boolean;
  guns: GunMode;
  gambleMode: GambleMode;
  safeInt: boolean;
  intCap: number;
  disabled: string[];
  biochemFull: boolean;
}

export interface GameRules {
  bags: string[];
  handsOnly: string[];
  toolQualities: string[];
  toolCandidates: Record<string, string[]>;
  qualityProviders: Record<string, string>;
  liquidQualities: Record<string, Record<string, number>>;
  magazines: Record<string, Magazine>;
}

export interface Settings {
  assumptions: Assumptions;
  upChain: UpChain;
  options: Options;
  raws: Record<string, Raw>;
  game: GameRules;
  targets: string[];
}

export interface Cell {
  id: string;
  n: number;
  conds: number[];
  tool?: boolean;
}

export interface WornJ {
  slot: string;
  id: string;
  cond: number;
}

export interface BagJ {
  id: string;
  where: string;
  max: number;
  used: number;
  enc: number;
  tags?: string;
  items: Cell[];
}

export interface Snap {
  int: number;
  xp: number;
  nextXp: number;
  load: number;
  cap: number;
  layer: number;
  handR: Cell | null;
  handL: Cell | null;
  mouth: Cell | null;
  worn: WornJ[];
  bags: BagJ[];
  over: Cell[];
  bioIn?: string;
  bioMl?: number;
  jug?: string;
  liq: Record<string, number>;
}

export interface Step {
  step: number;
  kind: string;
  name: string;
  times: number;
  note: string;
  breaks?: string;
  usedUp?: string;
  gathered: Record<string, number>;
  used: Record<string, number>;
  toolWear: Record<string, number>;
  matWear: Record<string, number>;
  delta: Record<string, number>;
  incl?: Record<string, number>;
  inclXp?: number;
  bottleneck?: string;
  goal: boolean;
  after: Snap;
}

export interface Miss {
  id: string;
  done: number;
  n: number;
  block: string;
  why: string;
}

export interface Plan {
  maxLayer: number;
  steps: Step[];
  notReached: Miss[];
  stopped?: string;
}

export interface PlanRequest {
  data: GameData;
  settings: Settings;
}

export type PlanReply = { ok: true; plan: Plan; ms: number } | { ok: false; error: string };
