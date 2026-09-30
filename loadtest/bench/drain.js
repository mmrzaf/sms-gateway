// Each harness case starts after the previous queue has drained.
import * as admin from "../lib/admin.js";
import { criterion, criteriaThreshold, invariantsPass } from "../lib/checks.js";
export const options = { vus: 1, iterations: 1, thresholds: criteriaThreshold };
export default function () {
  criterion("previous workload drained", admin.waitForDrain(600));
  invariantsPass();
}
