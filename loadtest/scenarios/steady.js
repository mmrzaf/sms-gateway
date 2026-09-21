// Steady mixed traffic: 90% normal, 10% Express, at RATE messages per second.
// No request may fail with a server error, the queue must drain, and no
// message may wait more than 10 s to be sent. CHAOS=1 tolerates failed
// requests, for use while failures are injected.
import exec from "k6/execution";
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { send, recipient } from "../lib/client.js";
import { duration, RUN_ID } from "../lib/config.js";
import { criterion, criteriaThreshold, invariantsPass, sentLatency } from "../lib/checks.js";

const serverErrors = new Counter("server_errors");
const chaos = __ENV.CHAOS === "1";

export const options = {
  scenarios: {
    steady: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.RATE || 500),
      timeUnit: "1s",
      duration: duration("5m"),
      preAllocatedVUs: 100,
      maxVUs: 500,
    },
  },
  thresholds: Object.assign(chaos ? {} : { server_errors: ["count==0"] }, criteriaThreshold),
  teardownTimeout: "10m",
};

export function setup() {
  return admin.createCustomer("steady", 100000000, 5000);
}

export default function (c) {
  const express = Math.random() < 0.1;
  const res = send(c.key, {
    to: recipient(),
    text: "steady load",
    type: express ? "express" : "normal",
    // Unique per message, so retries after lost responses are replays.
    client_ref: `${RUN_ID}-${exec.vu.idInTest}-${exec.vu.iterationInInstance}`,
  });
  if (res.status === 0 || res.status >= 500) serverErrors.add(1);
}

export function teardown(c) {
  criterion("queue drains", admin.waitForDrain(300));
  const sample = admin.messages(c.id, {}, 5000);
  const waiting = sample.filter((m) => m.status === "accepted").length;
  criterion("no message left accepted", waiting === 0, `${waiting} of ${sample.length} sampled`);
  if (!chaos) {
    const worst = sentLatency(sample, 100);
    criterion("every message sent within 10 s", worst <= 10, `max ${worst.toFixed(2)} s`);
  }
  invariantsPass();
}
