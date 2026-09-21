// Normal lanes are saturated while Express traffic flows. Express
// accept-to-sent p99 must stay under 2 s with no SLA breaches.
import * as admin from "../lib/admin.js";
import { send, sendBatch, recipient } from "../lib/client.js";
import { duration } from "../lib/config.js";
import { criterion, criteriaThreshold, invariantsPass, sentLatency } from "../lib/checks.js";

const batch = Array.from({ length: 100 }, () => ({ to: "+989100000000", text: "bulk" }));

export const options = {
  scenarios: {
    saturate: {
      executor: "constant-arrival-rate",
      exec: "saturate",
      rate: 20,
      timeUnit: "1s",
      duration: duration("2m"),
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
    express: {
      executor: "constant-arrival-rate",
      exec: "express",
      rate: 50,
      timeUnit: "1s",
      duration: duration("2m"),
      preAllocatedVUs: 20,
      maxVUs: 100,
    },
  },
  thresholds: criteriaThreshold,
  teardownTimeout: "5m",
};

export function setup() {
  return {
    bulk: admin.createCustomer("bulkco", 1000000, 2000),
    express: admin.createCustomer("quickpay", 100000, 200),
  };
}

export function saturate(d) {
  sendBatch(d.bulk.key, batch);
}

export function express(d) {
  send(d.express.key, { to: recipient(), text: "Your code is 482913", type: "express" });
}

export function teardown(d) {
  const msgs = admin.messages(d.express.id, {}, 10000);
  const p99 = sentLatency(msgs, 99);
  criterion("express p99 accept-to-sent under 2 s", p99 < 2, `p99 ${p99.toFixed(3)} s over ${msgs.length}`);
  const breaches = msgs.filter((m) => m.sla_breached).length;
  criterion("no express SLA breaches", breaches === 0, `${breaches}`);
  invariantsPass();
}
