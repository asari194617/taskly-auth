import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/auth-error-event.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { authErrorEvent, sanitizeAuthEvent } = await import(`data:text/javascript,${encodeURIComponent(compiled)}`);
const gsa = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6_2 like Mac OS X) AppleWebKit/605.1.15 GSA/441.5.989047059 Mobile/15E148 Safari/604.1";

test("passkey failures retain the phase, DOMException name and Google app version", () => {
  const event = authErrorEvent("passkey.credential", new DOMException("Cancelled or unsupported", "NotAllowedError"), {}, "/password", gsa);
  assert.equal(event.level, "warning");
  assert.equal(event.tags["auth.operation"], "passkey.credential");
  assert.equal(event.tags["auth.error_name"], "NotAllowedError");
  assert.deepEqual(event.contexts.browser, { name: "Google app", version: "441.5.989047059" });
  assert.equal(event.contexts.os.name, "iOS");
  assert.equal(event.exception.values[0].mechanism.handled, true);
});

test("unavailable authenticators are warnings and HTTP failures include a safe status and endpoint", () => {
  assert.equal(authErrorEvent("passkey.support", { name: "Unavailable" }, {}, "/password", gsa).level, "warning");
  const event = authErrorEvent("api.server", new Response(null, { status: 503 }), { status: 503, endpoint: "/auth/passkey/begin" }, "/password", gsa);
  assert.equal(event.level, "error");
  assert.equal(event.tags["http.status_code"], "503");
  assert.equal(event.tags["auth.endpoint"], "/auth/passkey/begin");
  assert.equal(event.exception.values[0].type, "HttpError");
});

test("raw exception messages, stacks, arbitrary names, URLs, query strings and UA suffixes never enter an event", () => {
  const secret = "test@example.test password=private-secret code=123456 auth_challenge=secret";
  const cause = Object.assign(new Error(secret), { name: secret, stack: secret, credential: secret });
  const event = authErrorEvent("passkey.begin", cause, { endpoint: "/auth/passkey/begin?code=" + secret }, "/password?request_id=" + secret, gsa + " " + secret);
  assert.ok(!JSON.stringify(event).includes(secret));
  assert.ok(!JSON.stringify(event).includes("private-secret"));
  assert.equal(event.tags["auth.error_name"], "Error");
  assert.equal(event.tags["auth.endpoint"], "other");
  assert.equal(event.tags["auth.page"], "other");
});

test("beforeSend removes all SDK-added user, request, trace, breadcrumb and arbitrary scope data", () => {
  const original = authErrorEvent("passkey.credential", { name: "SecurityError" }, {}, "/password", gsa);
  const event = sanitizeAuthEvent({
    ...original, event_id: "a".repeat(32), timestamp: 123, release: "taskly-auth-ui@abc123", environment: "production",
    request: { url: "https://example.test/?code=secret", headers: { Cookie: "secret" }, data: { password: "secret" } },
    user: { email: "secret@example.test", ip_address: "127.0.0.1" }, extra: { credential: "secret" },
    breadcrumbs: [{ message: "secret" }], contexts: { ...original.contexts, trace: { data: { code: "secret" } }, arbitrary: { value: "secret" } },
    tags: { ...original.tags, credential: "secret" },
    exception: { values: [{ type: "Error", value: "secret", stacktrace: { frames: [{ filename: "https://example.test/?code=secret", vars: { code: "secret" } }] } }] },
  });
  assert.ok(!JSON.stringify(event).includes("secret"));
  assert.deepEqual(event.fingerprint, original.fingerprint);
  assert.equal(event.event_id, "a".repeat(32));
  assert.equal(event.release, "taskly-auth-ui@abc123");
  assert.equal(event.request, undefined);
  assert.equal(event.user, undefined);
  assert.equal(event.sdk.settings.infer_ip, "never");
});

test("unrelated events are dropped instead of being forwarded from another SDK scope", () => {
  assert.equal(sanitizeAuthEvent({ message: "unexpected secret", tags: {} }), null);
});
