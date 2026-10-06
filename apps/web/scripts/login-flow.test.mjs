import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/login-flow.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { startFlow, loadFlow, saveFlow, clearFlow } = await import(`data:text/javascript,${encodeURIComponent(compiled)}`);
const storage = () => {
  const values = new Map();
  return { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) };
};
const requestID = "a".repeat(43);

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
});
