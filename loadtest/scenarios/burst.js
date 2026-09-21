// A burst from 100 to 5,000 messages per second against a 2,000 msg/s rate
// limit. Only 202 and 429 responses are allowed, and the backlog must drain.
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { send, recipient } from "../lib/client.js";
import { criterion, criteriaThreshold, invariantsPass } from "../lib/checks.js";

const accepted = new Counter("accepted");
const limited = new Counter("rate_limited");
const other = new Counter("other_status");

export const options = {
  scenarios: {
    burst: {
      executor: "ramping-arrival-rate",
      startRate: 100,
      timeUnit: "1s",
      preAllocatedVUs: 500,
      maxVUs: 3000,
      stages: [
        { target: 5000, duration: "5s" },
        { target: 5000, duration: "30s" },
        { target: 100, duration: "5s" },
        { target: 100, duration: "30s" },
      ],
    },
  },
  thresholds: Object.assign({ other_status: ["count==0"] }, criteriaThreshold),
  teardownTimeout: "5m",
};

export function setup() {
  return admin.createCustomer("burst", 100000000, 2000);
}

export default function (c) {
  const res = send(c.key, { to: recipient(), text: "burst" });
  if (res.status === 202) accepted.add(1);
  else if (res.status === 429) limited.add(1);
  else other.add(1);
}

export function teardown() {
  criterion("backlog drains within 2 minutes", admin.waitForDrain(120));
  invariantsPass();
}
