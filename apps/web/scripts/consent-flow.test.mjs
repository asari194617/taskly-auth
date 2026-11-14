import assert from "node:assert/strict";
import test from "node:test";
import { consentRequestID, parseConsentDetails, consentRedirectURL } from "../src/consent-flow.ts";
const token = "a".repeat(43);
const callback = "https://chatgpt.com/connector/oauth/example";
const details = {
  client_name: "ChatGPT · Taskly企業ガイダンス",
  redirect_uri: callback,
  resource: "https://guidance.example.test/mcp",
  scopes: ["guidance:read", "guidance:bookmarks:write", "guidance:tasks:write"],
  csrf_token: token,
};
const result = (query) => ({ redirect_uri: `${callback}?${query}` });

test("consent accepts one opaque request ID and rejects missing, duplicate, or malformed IDs", () => {
  assert.equal(consentRequestID(`?request_id=${token}`), token);
  for (const query of ["", "?request_id=short", `?request_id=${token}&request_id=${token}`, `?request_id=${"/".repeat(43)}`]) {
    assert.equal(consentRequestID(query), null);
  }
});

test("consent displays precisely the requested read and write permissions", () => {
  assert.deepEqual(parseConsentDetails(details), details);
  assert.deepEqual(parseConsentDetails({ ...details, scopes: ["guidance:read"] }).scopes, ["guidance:read"]);
  for (const scopes of [[], ["guidance:tasks:write"], ["guidance:read", "unknown"], ["guidance:read", "guidance:read"], "guidance:read"]) {
    assert.equal(parseConsentDetails({ ...details, scopes }), null);
  }
});

test("malformed consent or CSRF token cannot enable authorization", () => {
  for (const value of [null, false, "details", {}, { ...details, client_name: "" }, { ...details, client_name: "a".repeat(201) }, { ...details, csrf_token: "short" }]) {
    assert.equal(parseConsentDetails(value), null);
  }
});

test("consent only accepts safe service and callback URLs", () => {
  for (const url of ["/relative", "javascript:alert(1)", "https://user:password@example.test", "https://example.test/#fragment", "http://example.test", "https://example.test/" + "a".repeat(2048)]) {
    assert.equal(parseConsentDetails({ ...details, redirect_uri: url }), null);
    assert.equal(parseConsentDetails({ ...details, resource: url }), null);
  }
  assert.equal(parseConsentDetails({ ...details, resource: "https://guidance.example.test/mcp?unexpected=1" }), null);
  assert.equal(parseConsentDetails({ ...details, resource: "http://127.0.0.1:3067/mcp" }), null);
  assert.ok(parseConsentDetails({ ...details, resource: "http://127.0.0.1:3067/mcp" }, true));
  assert.ok(parseConsentDetails({ ...details, redirect_uri: "http://localhost:4321/callback" }, true));
  assert.equal(parseConsentDetails({ ...details, resource: "http://untrusted.example.test/mcp" }, true), null);
});

test("approval and denial return to the registered callback with the OAuth result", () => {
  for (const query of ["code=opaque&state=original&iss=https%3A%2F%2Fauth.example.test", "error=access_denied&error_description=Declined&state=original&iss=https%3A%2F%2Fauth.example.test"]) {
    assert.equal(consentRedirectURL(result(query), callback), `${callback}?${query}`);
  }
});

test("decision rejects callback origin, path, credential, and protocol changes", () => {
  for (const url of ["https://evil.example.test/connector/oauth/example?code=x&state=s&iss=i", "https://chatgpt.com/other?code=x&state=s&iss=i", "https://user:secret@chatgpt.com/connector/oauth/example?code=x&state=s&iss=i", "javascript:alert(1)", callback + "?code=x&state=s&iss=i#fragment"]) {
    assert.equal(consentRedirectURL({ redirect_uri: url }, callback), null);
  }
  assert.equal(consentRedirectURL(null, callback), null);
  assert.equal(consentRedirectURL({}, callback), null);
  assert.equal(consentRedirectURL(result("code=x&state=s&iss=i"), "bad-url"), null);
});

test("decision rejects ambiguous results and parameters outside the OAuth callback contract", () => {
  for (const query of ["code=x&state=s", "code=x&iss=i", "state=s&iss=i", "code=x&error=access_denied&state=s&iss=i", "code=x&code=y&state=s&iss=i", "code=x&state=s&state=t&iss=i", "code=x&state=s&iss=i&next=https%3A%2F%2Fevil.example.test"]) {
    assert.equal(consentRedirectURL(result(query), callback), null);
  }
});

test("decision preserves existing registered query values without requiring an unchanged OAuth result query", () => {
  const registered = `${callback}?connection=example&mode=link&mode=manual`;
  const suffix = "code=x&state=s&iss=i";
  assert.equal(consentRedirectURL({ redirect_uri: `${registered}&${suffix}` }, registered), `${registered}&${suffix}`);
  assert.equal(consentRedirectURL({ redirect_uri: `${callback}?connection=other&mode=link&mode=manual&${suffix}` }, registered), null);
  assert.equal(consentRedirectURL({ redirect_uri: `${callback}?connection=example&mode=link&${suffix}` }, registered), null);
});
