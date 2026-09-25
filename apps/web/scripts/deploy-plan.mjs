import { readdir } from "node:fs/promises";
import { extname, join, relative, sep } from "node:path";

const mimeTypes = new Map([
  [".css", "text/css; charset=utf-8"],
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".json", "application/json; charset=utf-8"],
  [".map", "application/json; charset=utf-8"],
  [".svg", "image/svg+xml"],
  [".webp", "image/webp"],
  [".woff2", "font/woff2"],
]);

export async function listFiles(root, directory = root) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(entries.map((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(root, path) : [relative(root, path).split(sep).join("/")];
  }));
  return files.flat().sort();
}

export function objectMetadata(key) {
  const extension = extname(key);
  const isPage = extension === "" || extension === ".html";
  return {
    contentType: isPage ? "text/html; charset=utf-8" : (mimeTypes.get(extension) ?? "application/octet-stream"),
    cacheControl: key.startsWith("assets/")
      ? "public, max-age=31536000, immutable"
      : "no-cache",
  };
}

export function uploadOrder(keys) {
  return [...keys].sort((left, right) => {
    const leftAsset = left.startsWith("assets/") ? 0 : 1;
    const rightAsset = right.startsWith("assets/") ? 0 : 1;
    return leftAsset - rightAsset || left.localeCompare(right);
  });
}
