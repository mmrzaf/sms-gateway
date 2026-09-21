// B1/B2: acceptance throughput with single-message requests.
// CUSTOMERS=1 measures the per-customer ceiling (B2); the default spreads
// load over 50 customers (B1). A 15 s warm-up precedes the 60 s measurement.
import exec from "k6/execution";
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { send } from "../lib/client.js";

const accepted = new Counter("measured_accepted");
const customers = Number(__ENV.CUSTOMERS || 50);
const vus = Number(__ENV.VUS || 200);

export const options = {
  scenarios: {
    warmup: { executor: "constant-vus", vus, duration: "15s" },
    measure: { executor: "constant-vus", vus, duration: "60s", startTime: "15s" },
  },
  summaryTrendStats: ["p(50)", "p(95)", "p(99)", "max"],
};

export function setup() {
  return Array.from({ length: customers }, (_, i) => admin.createCustomer(`bench-accept-${i}`, 1000000000, 100000));
}

export default function (list) {
  const c = list[exec.vu.idInTest % list.length];
  const res = send(c.key, { to: "+989121234567", text: "bench" });
  if (res.status === 202 && exec.scenario.name === "measure") accepted.add(1);
}
