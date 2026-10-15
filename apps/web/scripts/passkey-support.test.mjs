import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/passkey-support.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { passkeySupport } = await import(`data:text/javascript,${encodeURIComponent(compiled)}`);
const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6_2 like Mac OS X) AppleWebKit/605.1.15";
const gsa = `${iphone} GSA/441.5.989047059 Mobile/15E148 Safari/604.1`;
const unavailable = { isUserVerifyingPlatformAuthenticatorAvailable: async () => false };
const available = { isUserVerifyingPlatformAuthenticatorAvailable: async () => true };

test("Google app on iOS offers the browser fallback when WebAuthn exists but the platform authenticator is unavailable", async () => {
  assert.equal(await passkeySupport(gsa, true, unavailable), "ios-google-app");
  assert.equal(await passkeySupport(gsa.replace("iPhone", "iPad"), true, unavailable), "ios-google-app");
  assert.equal(await passkeySupport(gsa.replace(/iPhone; CPU iPhone OS 18_6_2 like Mac OS X/, "Macintosh; Intel Mac OS X 10_15"), true, unavailable), "ios-google-app");
});

test("missing or rejected APIs in the Google app also offer the browser fallback", async () => {
  assert.equal(await passkeySupport(gsa, false, undefined), "ios-google-app");
  assert.equal(await passkeySupport(gsa, true, {
    isUserVerifyingPlatformAuthenticatorAvailable: async () => { throw new Error("unavailable"); },
  }), "ios-google-app");
});

test("a Google app that supports the authenticator can still use passkeys", async () => {
  assert.equal(await passkeySupport(gsa, true, available), "available");
});

test("Safari, Chrome, and Android keep external security key support even without a platform authenticator", async () => {
  for (const userAgent of [
    `${iphone} Version/18.6 Mobile/15E148 Safari/604.1`,
    `${iphone} CriOS/141.0 Mobile/15E148 Safari/604.1`,
    "Mozilla/5.0 (Linux; Android 16) GSA/441.5 Chrome/141.0 Mobile Safari/537.36",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/141.0 Safari/537.36",
  ]) assert.equal(await passkeySupport(userAgent, true, unavailable), "available");
});

test("browsers missing WebAuthn are unavailable without being identified as the Google app", async () => {
  assert.equal(await passkeySupport(iphone, false, undefined), "unavailable");
  assert.equal(await passkeySupport(iphone, true, undefined), "unavailable");
});
