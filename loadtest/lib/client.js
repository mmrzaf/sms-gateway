// Customer API calls.
import http from "k6/http";
import { API_URL } from "./config.js";

function headers(key) {
  return { Authorization: `Bearer ${key}`, "Content-Type": "application/json" };
}

// Sends one message. With a client_ref, a request that fails without an
// answer (network error or 503) is retried with the same client_ref, which
// the gateway turns into a replay rather than a second message.
export function send(key, message, tags = {}) {
  const body = JSON.stringify(message);
  let res;
  for (let attempt = 0; attempt < 3; attempt++) {
    res = http.post(`${API_URL}/v1/messages`, body, { headers: headers(key), tags });
    const unanswered = res.status === 0 || res.status === 503;
    if (!unanswered || !message.client_ref) break;
  }
  return res;
}

// Sends a batch of messages as one request.
export function sendBatch(key, messages, tags = {}) {
  return http.post(`${API_URL}/v1/messages/batch`, JSON.stringify({ messages }), {
    headers: headers(key),
    tags,
  });
}

// Returns a message of the key's customer.
export function getMessage(key, id) {
  return http.get(`${API_URL}/v1/messages/${id}`, { headers: headers(key) });
}

// Returns the key's balance.
export function balance(key) {
  return http.get(`${API_URL}/v1/balance`, { headers: headers(key) }).json("balance");
}

// A random Iranian mobile number in E.164 form.
export function recipient() {
  return "+98912" + String(Math.floor(Math.random() * 1e7)).padStart(7, "0");
}
