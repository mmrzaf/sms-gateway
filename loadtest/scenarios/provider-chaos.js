// Provider A fails 30% of requests and times out 5% of them (after accepting
// the message) while normal traffic flows. Every message must end sent or
// terminal, and provider deduplication must keep every normal message
// accepted at most once: provider A's acceptances equal the messages sent
// through it.
import * as admin from "../lib/admin.js";
import { send, recipient } from "../lib/client.js";
import { duration } from "../lib/config.js";
import { criterion, criteriaThreshold, countBy, invariantsPass } from "../lib/checks.js";

export const options = {
  scenarios: {
    traffic: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.RATE || 100),
      timeUnit: "1s",
      duration: duration("5m"),
      preAllocatedVUs: 50,
      maxVUs: 300,
    },
  },
  thresholds: criteriaThreshold,
  teardownTimeout: "15m",
};

export function setup() {
  admin.setProvider("B", admin.PROVIDER_DEFAULTS);
  admin.setProvider("A", Object.assign({}, admin.PROVIDER_DEFAULTS, { failure_rate: 0.3, timeout_rate: 0.05 }));
  const before = admin.providerStats("A");
  return { customer: admin.createCustomer("chaos", 100000000, 1000), acceptedBefore: before.accepted };
}

export default function (d) {
  send(d.customer.key, { to: recipient(), text: "chaos" });
}

export function teardown(d) {
  admin.setProvider("A", admin.PROVIDER_DEFAULTS);
  criterion("backlog drains", admin.waitForDrain(600));

  const msgs = admin.messages(d.customer.id, {}, 1000000);
  const statuses = countBy(msgs, "status");
  criterion("no message left accepted", !statuses.accepted, JSON.stringify(statuses));

  const viaA = msgs.filter((m) => m.provider === "A" && m.sent_at).length;
  const acceptedByA = admin.providerStats("A").accepted - d.acceptedBefore;
  criterion("provider A accepted each message at most once", acceptedByA === viaA,
    `provider accepted ${acceptedByA}, gateway sent ${viaA} through A`);
  invariantsPass();
}
