// B7: duplicate delivery-report acknowledgment throughput. Fixtures are made
// terminal before measurement; every measured response must be a duplicate.
// This does not measure first-report completion throughput.
import http from "k6/http";
import exec from "k6/execution";
import { Counter, Rate } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { sendBatch } from "../lib/client.js";
import { ADMIN_URL } from "../lib/config.js";

const acknowledged = new Counter("measured_duplicate_reports");
const duplicates = new Rate("duplicate_acknowledgment");
const secret = __ENV.PROVIDER_SECRET || "local-provider-secret";

export const options = {
  scenarios: {
    warmup: { executor: "constant-vus", vus: 100, duration: "15s" },
    measure: { executor: "constant-vus", vus: 100, duration: "60s", startTime: "15s" },
  },
  thresholds: { duplicate_acknowledgment: ["rate==1"], measured_duplicate_reports: ["count>0"] },
  setupTimeout: "10m",
  summaryTrendStats: ["p(50)", "p(95)", "p(99)", "max"],
};

function report(id) {
  return http.post(
    `${ADMIN_URL}/internal/dlr`,
    JSON.stringify({ message_id: id, provider: "A", provider_ref: "bench", status: "delivered", reported_at: new Date().toISOString() }),
    { headers: { "Content-Type": "application/json", "X-Provider-Secret": secret } }
  );
}

export function setup() {
  if (!admin.waitForDrain(600)) throw new Error("previous workload did not drain");
  const c = admin.createCustomer("bench-dlr", 1000000000, 100000);
  const batch = Array.from({ length: 500 }, () => ({ to: "+989121234567", text: "bench" }));
  const ids = [];
  for (let i = 0; i < 40; i++) {
    const res = sendBatch(c.key, batch);
    if (res.status !== 202) throw new Error(`fixture acceptance: HTTP ${res.status}`);
    ids.push(...res.json("messages").map((m) => m.id));
  }
  for (const id of ids) {
    const res = report(id);
    if (res.status !== 200 || !["applied", "duplicate"].includes(res.json("outcome"))) {
      throw new Error(`fixture report failed: HTTP ${res.status}`);
    }
  }
  if (!admin.waitForDrain(600)) throw new Error("fixture queue did not drain");
  return ids;
}

export default function (ids) {
  const res = report(ids[Math.floor(Math.random() * ids.length)]);
  const duplicate = res.status === 200 && res.json("outcome") === "duplicate";
  duplicates.add(duplicate);
  if (duplicate && exec.scenario.name === "measure") acknowledged.add(1);
}
