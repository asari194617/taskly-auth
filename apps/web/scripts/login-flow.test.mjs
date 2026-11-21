import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";

const source = await readFile(new URL("../src/login-flow.ts", import.meta.url), "utf8");
const compiled = stripTypeScriptTypes(source, { mode: "strip" });
const { startFlow, loadFlow, saveFlow, clearFlow, normalizeLoginEmail, schoolEmailLocalPart, securityDetailSection, securityDetailURL, securityLoginURL, securityMfaURL } = await import(`data:text/javascript,${encodeURIComponent(compiled)}`);
const storage = () => {
  const values = new Map();
  return { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) };
};
const requestID = "a".repeat(43);

test("login accepts the local part or a full address at the school domain", () => {
  assert.equal(normalizeLoginEmail(" Student01 "), "student01@nagaoka-ct.ac.jp");
  assert.equal(normalizeLoginEmail("Student+Tag@NAGAOKA-CT.AC.JP"), "student+tag@nagaoka-ct.ac.jp");
  assert.equal(schoolEmailLocalPart(" Student01@NAGAOKA-CT.AC.JP "), "Student01");
  assert.equal(schoolEmailLocalPart("student@example.org"), "student@example.org");
  for (const invalid of ["", "   ", "@nagaoka-ct.ac.jp", "first last", "user@", "user@example.com", "user@nagaoka-ct.ac.jp.example.com", "user@@nagaoka-ct.ac.jp"]) {
    assert.equal(normalizeLoginEmail(invalid), null);
  }
});

test("OAuth context and email stay in tab storage across page navigation", () => {
  const tab = storage();
  const flow = startFlow(`?request_id=${requestID}`, tab);
  assert.equal(flow.requestID, requestID);
  flow.email = "test@example.com";
  flow.challenge = "challenge";
  saveFlow(flow, tab);
  assert.deepEqual(loadFlow(tab), flow);
  assert.equal(startFlow("", tab).email, "");
  assert.equal(loadFlow(tab).challenge, "");
  clearFlow(tab);
  assert.equal(loadFlow(tab), null);
});

test("invalid OAuth request and unknown management target are rejected", () => {
  const tab = storage();
  assert.equal(startFlow("?request_id=short", tab), null);
  assert.equal(startFlow("?next=https://example.com", tab), null);
  assert.equal(startFlow("?next=security", tab).next, "security");
  assert.equal(startFlow("?next=login-history", tab).next, "login-history");
});

test("security verification returns only to allowlisted settings sections", () => {
  const tab = storage();
  const flow = startFlow("?next=security&section=passkeys", tab);
  assert.equal(flow.nextSection, "passkeys");
  assert.equal(loadFlow(tab).nextSection, "passkeys");
  assert.equal(securityMfaURL("passkeys"), "/mfa?purpose=security&section=passkeys");
  assert.equal(securityDetailURL(flow.nextSection), "/security?section=passkeys");
  assert.equal(securityLoginURL(flow.nextSection), "/login?next=security&section=passkeys");
  assert.equal(securityDetailSection("https://example.com"), null);
  assert.equal(startFlow("?next=security&section=https%3A%2F%2Fexample.com", tab).nextSection, "");
  const invalid = { ...flow, nextSection: "https://example.com" };
  saveFlow(invalid, tab);
  assert.equal(loadFlow(tab), null);
});
