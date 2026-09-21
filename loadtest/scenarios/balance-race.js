// A customer with 100 credits sends 1,000 single-segment messages at once.
// Exactly 100 must be accepted and 900 refused, and the balance must end at 0.
import { Counter } from "k6/metrics";
import * as admin from "../lib/admin.js";
import { send, recipient, balance } from "../lib/client.js";
import { criterion, criteriaThreshold, invariantsPass } from "../lib/checks.js";

const accepted = new Counter("accepted");
const refused = new Counter("refused");
const other = new Counter("other_status");

export const options = {
  scenarios: {
    race: { executor: "shared-iterations", vus: 200, iterations: 1000, maxDuration: "2m" },
  },
  thresholds: Object.assign(
    { accepted: ["count==100"], refused: ["count==900"], other_status: ["count==0"] },
    criteriaThreshold
  ),
  teardownTimeout: "2m",
};

export function setup() {
  return admin.createCustomer("race", 100, 100000);
}

export default function (c) {
  const res = send(c.key, { to: recipient(), text: "race" });
  if (res.status === 202) accepted.add(1);
  else if (res.status === 402) refused.add(1);
  else other.add(1);
}

export function teardown(c) {
  criterion("balance is 0", balance(c.key) === 0);
  const count = admin.messages(c.id).length;
  criterion("exactly 100 messages recorded", count === 100, `${count}`);
  invariantsPass();
}
