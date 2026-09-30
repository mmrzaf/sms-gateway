// B5: sustained acceptance and dispatch. A pass requires the requested load
// to be accepted, no dropped iterations, and less than two seconds of backlog.
import { sleep } from "k6";
import { Counter, Rate } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { sendBatch } from "../lib/client.js";
import { criterion, criteriaThreshold, invariantsPass } from "../lib/checks.js";

const rate = Number(__ENV.RATE || 1000);
const seconds = Number(__ENV.BENCH_SECONDS || 60);
if (!Number.isInteger(rate) || rate < 100 || rate % 100 !== 0) throw new Error("RATE must be a positive multiple of 100");
if (!Number.isInteger(seconds) || seconds < 1) throw new Error("BENCH_SECONDS must be a positive integer");
const accepted = new Counter("sustained_accepted");
const successful = new Rate("batch_acceptance");
const batch = Array.from({ length: 100 }, () => ({ to: "+989121234567", text: "bench" }));

export const options = {
  scenarios: {
    offer: {
      executor: "constant-arrival-rate",
      rate: rate / 100,
      timeUnit: "1s",
      duration: `${seconds}s`,
      preAllocatedVUs: 50,
      maxVUs: 400,
    },
  },
  thresholds: Object.assign({}, criteriaThreshold, {
    dropped_iterations: ["count==0"],
    batch_acceptance: ["rate==1"],
    sustained_accepted: [`count>=${Math.floor(rate * seconds * 0.99)}`],
  }),
  setupTimeout: "15m",
  teardownTimeout: "15m",
};

export function setup() {
  if (!admin.waitForDrain(600)) throw new Error("previous workload did not drain");
  return Array.from({ length: 20 }, (_, i) => admin.createCustomer(`bench-e2e-${i}`, 1000000000, 100000));
}

export default function (list) {
  const res = sendBatch(list[Math.floor(Math.random() * list.length)].key, batch);
  let count = 0;
  if (res.status === 202) count = res.json("messages").length;
  successful.add(count === batch.length);
  accepted.add(count);
}

export function teardown() {
  sleep(2);
  const backlog = admin.system().queue.reduce((n, l) => n + l.ready + l.in_flight + l.delayed, 0);
  criterion(`dispatch keeps up with ${rate} msg/s`, backlog < 2 * rate, `backlog ${backlog}`);
  criterion("queue drains", admin.waitForDrain(600));
  invariantsPass();
}
