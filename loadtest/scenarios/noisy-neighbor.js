// A high-volume customer floods its lane with batches at its full rate limit
// while a small customer sends a trickle. The small customer's messages must
// still be sent promptly: accept-to-sent p95 under 2 s.
import * as admin from "../lib/admin.js";
import { send, sendBatch, recipient } from "../lib/client.js";
import { duration } from "../lib/config.js";
import { criterion, criteriaThreshold, invariantsPass, sentLatency } from "../lib/checks.js";

const batch = Array.from({ length: 100 }, () => ({ to: "+989100000000", text: "bulk" }));

export const options = {
  scenarios: {
    flood: {
      executor: "constant-arrival-rate",
      exec: "flood",
      rate: 20, // batches of 100: 2,000 messages per second
      timeUnit: "1s",
      duration: duration("2m"),
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
    trickle: {
      executor: "constant-arrival-rate",
      exec: "trickle",
      rate: 10,
      timeUnit: "1s",
      duration: duration("2m"),
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  thresholds: criteriaThreshold,
  teardownTimeout: "5m",
};

export function setup() {
  return {
    bulk: admin.createCustomer("bulkco", 1000000, 2000),
    small: admin.createCustomer("acme", 10000, 100),
  };
}

export function flood(d) {
  sendBatch(d.bulk.key, batch);
}

export function trickle(d) {
  send(d.small.key, { to: recipient(), text: "small customer" });
}

export function teardown(d) {
  const small = admin.messages(d.small.id, {}, 5000);
  const p95 = sentLatency(small, 95);
  criterion("small customer p95 accept-to-sent under 2 s", p95 < 2, `p95 ${p95.toFixed(3)} s over ${small.length}`);
  invariantsPass();
}
