// B7: delivery report intake. Posts reports for accepted messages directly to
// the gateway's DLR endpoint and measures reports per second.
import http from "k6/http";
import exec from "k6/execution";
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { sendBatch } from "../lib/client.js";
import { ADMIN_URL } from "../lib/config.js";

const acknowledged = new Counter("measured_reports");
const secret = __ENV.PROVIDER_SECRET || "local-provider-secret";

export const options = {
  scenarios: {
    warmup: { executor: "constant-vus", vus: 100, duration: "15s" },
    measure: { executor: "constant-vus", vus: 100, duration: "60s", startTime: "15s" },
  },
  setupTimeout: "5m",
  summaryTrendStats: ["p(50)", "p(95)", "p(99)", "max"],
};

export function setup() {
  const c = admin.createCustomer("bench-dlr", 1000000000, 100000);
  const batch = Array.from({ length: 500 }, () => ({ to: "+989121234567", text: "bench" }));
  const ids = [];
  for (let i = 0; i < 40; i++) {
    ids.push(...sendBatch(c.key, batch).json("messages").map((m) => m.id));
  }
  return ids;
}

export default function (ids) {
  const id = ids[Math.floor(Math.random() * ids.length)];
  const res = http.post(
    `${ADMIN_URL}/internal/dlr`,
    JSON.stringify({ message_id: id, provider: "A", provider_ref: "bench", status: "delivered", reported_at: new Date().toISOString() }),
    { headers: { "Content-Type": "application/json", "X-Provider-Secret": secret } }
  );
  if (res.status === 200 && exec.scenario.name === "measure") acknowledged.add(1);
}
