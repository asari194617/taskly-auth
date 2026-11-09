import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";

const compile = (source) => stripTypeScriptTypes(source, { mode: "strip" });
const dataURL = (source) => `data:text/javascript,${encodeURIComponent(source)}`;
const events = dataURL(compile(await readFile(new URL("../src/auth-error-event.ts", import.meta.url), "utf8")));
const source = compile(await readFile(new URL("../src/telemetry.ts", import.meta.url), "utf8"));
let sequence = 0;

async function reporter({ dsn = "https://public@example.test/1", throwOnCapture = false } = {}) {
  const sdk = dataURL(`
    // isolated SDK fixture ${sequence++}
    export const captured = [];
    export const defaultStackParser = () => [];
    export const makeFetchTransport = () => {};
    export class BrowserClient { constructor(options) { this.options = options; } init() {} }
    export class Scope {
      setClient(client) { this.client = client; }
      captureEvent(event) {
        if (${throwOnCapture}) throw new Error("SDK unavailable");
        captured.push(this.client.options.beforeSend(event));
      }
    }
  `);
  const compiled = source.replace('"@sentry/browser"', JSON.stringify(sdk)).replace('"./auth-error-event"', JSON.stringify(events))
    .replaceAll("import.meta.env", JSON.stringify({ VITE_SENTRY_DSN: dsn, MODE: "test" }))
    .replaceAll("location.pathname", '"/password"').replaceAll("navigator.userAgent", '"Google app"');
  return { ...(await import(dataURL(compiled))), ...(await import(sdk)) };
}

test("repeated API failures remain deduplicated when each is also caught by the login screen", async () => {
  const { reportAuthError, captured } = await reporter();
  for (let attempt = 0; attempt < 3; attempt++) {
    const cause = new TypeError("private network details");
    reportAuthError("api.network", cause, { endpoint: "/auth/passkey/begin" });
    reportAuthError("passkey.begin", cause);
  }
  assert.equal(captured.length, 1);
  assert.equal(captured[0].tags["auth.operation"], "api.network");
});

test("missing DSN disables reporting", async () => {
  const { reportAuthError, captured } = await reporter({ dsn: "" });
  reportAuthError("passkey.credential", new Error("private details"));
  assert.equal(captured.length, 0);
});

test("SDK failures never propagate into the authentication handler", async () => {
  const { reportAuthError } = await reporter({ throwOnCapture: true });
  assert.doesNotThrow(() => reportAuthError("passkey.credential", new DOMException("private details", "SecurityError")));
});
