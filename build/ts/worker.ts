import { plan } from "./planner";
import type { PlanReply, PlanRequest } from "./types";

self.onmessage = (ev: MessageEvent<PlanRequest>) => {
  const t = performance.now();
  let r: PlanReply;
  try {
    r = { ok: true, plan: plan(ev.data.data, ev.data.settings), ms: performance.now() - t };
  } catch (e) {
    r = { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
  self.postMessage(r);
};
