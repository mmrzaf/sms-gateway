// B5: end-to-end sustained rate. Offers RATE messages per second in batches
// of 100 for 60 s and reports whether dispatch kept up: the queue must hold
// less than two seconds of traffic at the end. Run it at increasing rates;
// the highest rate that keeps up is the sustained rate.
import { sleep } from "k6";
import * as admin from "../lib/admin.js";
import { sendBatch } from "../lib/client.js";
import { criterion, criteriaThreshold } from "../lib/checks.js";

const rate = Number(__ENV.RATE || 1000);
const batch = Array.from({ length: 100 }, () => ({ to: "+989121234567", text: "bench" }));

export const options = {
  scenarios: {
    offer: {
      executor: "constant-arrival-rate",
      rate: Math.max(1, Math.round(rate / 100)),
      timeUnit: "1s",
      duration: "60s",
      preAllocatedVUs: 50,
      maxVUs: 400,
    },
  },
  thresholds: criteriaThreshold,
  teardownTimeout: "10m",
};

export function setup() {
  return Array.from({ length: 20 }, (_, i) => admin.createCustomer(`bench-e2e-${i}`, 1000000000, 100000));
}

export default function (list) {
  sendBatch(list[Math.floor(Math.random() * list.length)].key, batch);
}

export function teardown() {
  sleep(2);
  const backlog = admin.system().queue.reduce((n, l) => n + l.ready + l.in_flight + l.delayed, 0);
  criterion(`dispatch keeps up with ${rate} msg/s`, backlog < 2 * rate, `backlog ${backlog}`);
  admin.waitForDrain(600);
}
