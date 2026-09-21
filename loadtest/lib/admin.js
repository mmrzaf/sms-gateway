// Admin API calls used for setup and verification.
import http from "k6/http";
import encoding from "k6/encoding";
import { sleep } from "k6";
import { ADMIN_URL, ADMIN_TOKEN, RUN_ID } from "./config.js";

const auth = "Basic " + encoding.b64encode(`admin:${ADMIN_TOKEN}`);
const json = { Authorization: auth, "Content-Type": "application/json" };

function must(res, what) {
  if (res.status < 200 || res.status >= 300) {
    throw new Error(`${what}: HTTP ${res.status} ${res.body}`);
  }
  return res;
}

// Creates a customer for this run and returns { id, name, key }.
export function createCustomer(name, credits, rateLimitRPS) {
  const res = must(
    http.post(
      `${ADMIN_URL}/admin/api/customers`,
      JSON.stringify({ name: `${name}-${RUN_ID}`, initial_credits: credits, rate_limit_rps: rateLimitRPS }),
      { headers: json }
    ),
    `create customer ${name}`
  );
  return { id: res.json("id"), name: res.json("name"), key: res.json("api_key") };
}

export function customer(id) {
  return must(http.get(`${ADMIN_URL}/admin/api/customers/${id}`, { headers: json }), "get customer").json();
}

// Lists up to max messages of a customer, newest first, following cursors.
export function messages(customerId, filters = {}, max = 5000) {
  const out = [];
  let cursor = "";
  while (out.length < max) {
    const params = Object.assign({ customer_id: customerId, limit: 200 }, filters);
    if (cursor) params.cursor = cursor;
    const query = Object.entries(params)
      .map(([k, v]) => `${k}=${encodeURIComponent(v)}`)
      .join("&");
    const res = must(http.get(`${ADMIN_URL}/admin/api/messages?${query}`, { headers: json }), "list messages");
    out.push(...res.json("data"));
    cursor = res.json("next_cursor");
    if (!cursor) break;
  }
  return out;
}

export function system() {
  return must(http.get(`${ADMIN_URL}/admin/api/system`, { headers: json }), "system overview").json();
}

export function invariants() {
  return must(http.get(`${ADMIN_URL}/admin/api/invariants`, { headers: json }), "invariants").json();
}

export function setProvider(name, settings) {
  return must(
    http.put(`${ADMIN_URL}/admin/api/providers/${name}/config`, JSON.stringify(settings), { headers: json }),
    `configure provider ${name}`
  ).json();
}

export function providerStats(name) {
  return must(http.get(`${ADMIN_URL}/admin/api/providers/${name}/stats`, { headers: json }), "provider stats").json();
}

// The fake provider defaults, restored after scenarios that change them.
export const PROVIDER_DEFAULTS = {
  latency_ms: 50,
  jitter_ms: 50,
  failure_rate: 0,
  timeout_rate: 0,
  reject_rate: 0,
  outage: false,
  delivery_ratio: 0.95,
  dlr_delay_ms: 1000,
  dlr_jitter_ms: 500,
};

// Waits until the queue holds no rows, up to timeoutSeconds. Returns true if
// it drained.
export function waitForDrain(timeoutSeconds) {
  for (let waited = 0; waited <= timeoutSeconds; waited += 2) {
    const busy = system().queue.reduce((n, lane) => n + lane.ready + lane.in_flight + lane.delayed, 0);
    if (busy === 0) return true;
    sleep(2);
  }
  return false;
}
