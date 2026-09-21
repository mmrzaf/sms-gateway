// Pass/fail criteria evaluated after a scenario's load.
//
// Every criterion that fails adds one to the criteria_failed counter, whose
// threshold (count==0) makes k6 exit non-zero. Each scenario includes
// criteriaThreshold in its options.
import { Counter } from "k6/metrics";
import * as admin from "./admin.js";

export const criteriaFailed = new Counter("criteria_failed");
export const criteriaThreshold = { criteria_failed: ["count==0"] };

// Records one criterion and prints its outcome.
export function criterion(name, ok, detail = "") {
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  (" + detail + ")" : ""}`);
  criteriaFailed.add(ok ? 0 : 1);
  return ok;
}

// Runs the invariant checker through the admin API.
export function invariantsPass() {
  const report = admin.invariants();
  const failed = report.checks.filter((c) => !c.ok).map((c) => `${c.name}: ${c.violations}`);
  return criterion("invariants", report.ok, failed.join(", "));
}

// Returns a percentile, in seconds, of acceptance-to-sent latency over the
// messages that have been sent.
export function sentLatency(messages, percentile) {
  const values = messages
    .filter((m) => m.sent_at)
    .map((m) => (Date.parse(m.sent_at) - Date.parse(m.accepted_at)) / 1000)
    .sort((a, b) => a - b);
  if (values.length === 0) return NaN;
  const index = Math.ceil((percentile / 100) * values.length) - 1;
  return values[Math.min(Math.max(index, 0), values.length - 1)];
}

// Counts messages by the value of a field.
export function countBy(messages, field) {
  const counts = {};
  for (const m of messages) counts[m[field]] = (counts[m[field]] || 0) + 1;
  return counts;
}
