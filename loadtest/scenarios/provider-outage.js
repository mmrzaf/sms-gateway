// Provider A is down for 60 s in the middle of mixed traffic. Express must
// fail over quickly, no message may fail or expire, and the backlog must
// drain after recovery.
import { sleep } from "k6";
import * as admin from "../lib/admin.js";
import { send, recipient } from "../lib/client.js";
import { criterion, criteriaThreshold, invariantsPass, sentLatency } from "../lib/checks.js";

export const options = {
  scenarios: {
    normal: {
      executor: "constant-arrival-rate",
      exec: "normal",
      rate: 200,
      timeUnit: "1s",
      duration: "3m",
      preAllocatedVUs: 50,
      maxVUs: 300,
    },
    express: {
      executor: "constant-arrival-rate",
      exec: "express",
      rate: 20,
      timeUnit: "1s",
      duration: "3m",
      preAllocatedVUs: 10,
      maxVUs: 100,
    },
    outage: { executor: "per-vu-iterations", exec: "outage", vus: 1, iterations: 1, maxDuration: "3m" },
  },
  thresholds: criteriaThreshold,
  teardownTimeout: "10m",
};

export function setup() {
  admin.setProvider("A", admin.PROVIDER_DEFAULTS);
  admin.setProvider("B", admin.PROVIDER_DEFAULTS);
  return {
    normal: admin.createCustomer("outage-normal", 10000000, 1000),
    express: admin.createCustomer("outage-express", 10000000, 1000),
  };
}

export function normal(d) {
  send(d.normal.key, { to: recipient(), text: "normal during outage" });
}

export function express(d) {
  send(d.express.key, { to: recipient(), text: "express during outage", type: "express" });
}

export function outage() {
  sleep(30);
  console.log("provider A: outage on");
  admin.setProvider("A", { outage: true });
  sleep(60);
  console.log("provider A: outage off");
  admin.setProvider("A", { outage: false });
}

export function teardown(d) {
  admin.setProvider("A", admin.PROVIDER_DEFAULTS);
  criterion("backlog drains after recovery", admin.waitForDrain(300));
  for (const c of [d.normal, d.express]) {
    const failed = admin.messages(c.id, { status: "failed" }).length;
    const expired = admin.messages(c.id, { status: "expired" }).length;
    criterion(`${c.name}: no failed or expired messages`, failed + expired === 0, `${failed} failed, ${expired} expired`);
  }
  const p99 = sentLatency(admin.messages(d.express.id, {}, 10000), 99);
  criterion("express p99 accept-to-sent under 5 s", p99 < 5, `p99 ${p99.toFixed(3)} s`);
  invariantsPass();
}
