// B3: acceptance throughput with batches of BATCH messages (default 100).
import exec from "k6/execution";
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { sendBatch } from "../lib/client.js";

const accepted = new Counter("measured_accepted");
const size = Number(__ENV.BATCH || 100);
const batch = Array.from({ length: size }, () => ({ to: "+989121234567", text: "bench" }));

export const options = {
  scenarios: {
    warmup: { executor: "constant-vus", vus: 50, duration: "15s" },
    measure: { executor: "constant-vus", vus: 50, duration: "60s", startTime: "15s" },
  },
  summaryTrendStats: ["p(50)", "p(95)", "p(99)", "max"],
};

export function setup() {
  return Array.from({ length: 50 }, (_, i) => admin.createCustomer(`bench-batch-${i}`, 1000000000, 100000));
}

export default function (list) {
  const c = list[exec.vu.idInTest % list.length];
  const res = sendBatch(c.key, batch);
  if (res.status === 202 && exec.scenario.name === "measure") accepted.add(size);
}
