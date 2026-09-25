import assert from "node:assert/strict";
import test from "node:test";
import { objectMetadata, uploadOrder } from "./deploy-plan.mjs";

test("extensionless pages are HTML and are not cached", () => {
  assert.deepEqual(objectMetadata("login"), {
    contentType: "text/html; charset=utf-8",
    cacheControl: "no-cache",
  });
});

test("hashed assets are immutable and uploaded first", () => {
  assert.deepEqual(objectMetadata("assets/login-abc123.js"), {
    contentType: "text/javascript; charset=utf-8",
    cacheControl: "public, max-age=31536000, immutable",
  });
  assert.deepEqual(uploadOrder(["login", "assets/login-abc123.js", "security"]), [
    "assets/login-abc123.js", "login", "security",
  ]);
});
