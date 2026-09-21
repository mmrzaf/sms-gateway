// Endpoints and credentials, overridable with k6 -e NAME=value or environment variables.
export const API_URL = __ENV.API_URL || "http://localhost:8080";
export const ADMIN_URL = __ENV.ADMIN_URL || "http://localhost:8081";
export const ADMIN_TOKEN = __ENV.ADMIN_TOKEN || "admin";

// A short, unique suffix so every run creates its own customers.
export const RUN_ID = __ENV.RUN_ID || String(Date.now()).slice(-7);

// Returns a duration from the environment, for example DURATION=30s.
export function duration(fallback) {
  return __ENV.DURATION || fallback;
}
