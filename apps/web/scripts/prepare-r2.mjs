import { copyFile, readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { listFiles } from "./deploy-plan.mjs";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const dist = resolve(scriptDirectory, "../dist");
const pages = ["login", "password", "mail-code", "mfa", "logout", "passkeys", "security"];

for (const page of pages) await copyFile(resolve(dist, `${page}.html`), resolve(dist, page));

const files = await listFiles(dist);
for (const page of pages) {
  if (!files.includes(page)) throw new Error(`missing extensionless page: ${page}`);
}
if (!files.some((file) => /^assets\/.+-[A-Za-z0-9_-]+\.(css|js)$/.test(file))) {
  throw new Error("missing hashed Vite assets");
}
const login = await readFile(resolve(dist, "login"), "utf8");
if (!login.includes("/assets/")) throw new Error("login page does not reference bundled assets");
