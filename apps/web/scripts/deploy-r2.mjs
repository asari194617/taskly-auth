import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { DeleteObjectsCommand, ListObjectsV2Command, PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { listFiles, objectMetadata, uploadOrder } from "./deploy-plan.mjs";

const required = ["R2_ACCOUNT_ID", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_BUCKET"];
for (const name of required) {
  if (!process.env[name]) throw new Error(`${name} is required`);
}

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const dist = resolve(scriptDirectory, "../dist");
const bucket = process.env.R2_BUCKET;
const client = new S3Client({
  region: "auto",
  endpoint: `https://${process.env.R2_ACCOUNT_ID}.r2.cloudflarestorage.com`,
  credentials: {
    accessKeyId: process.env.R2_ACCESS_KEY_ID,
    secretAccessKey: process.env.R2_SECRET_ACCESS_KEY,
  },
});
const files = await listFiles(dist);

for (const key of uploadOrder(files)) {
  const metadata = objectMetadata(key);
  await client.send(new PutObjectCommand({
    Bucket: bucket,
    Key: key,
    Body: await readFile(resolve(dist, key)),
    ContentType: metadata.contentType,
    CacheControl: metadata.cacheControl,
  }));
  console.log(`uploaded ${key}`);
}

const existing = [];
let continuationToken;
do {
  const result = await client.send(new ListObjectsV2Command({
    Bucket: bucket,
    ContinuationToken: continuationToken,
  }));
  existing.push(...(result.Contents ?? []).flatMap((object) => object.Key ? [object.Key] : []));
  continuationToken = result.IsTruncated ? result.NextContinuationToken : undefined;
} while (continuationToken);

const expected = new Set(files);
const stale = existing.filter((key) => !expected.has(key));
for (let index = 0; index < stale.length; index += 1000) {
  const batch = stale.slice(index, index + 1000);
  await client.send(new DeleteObjectsCommand({
    Bucket: bucket,
    Delete: { Objects: batch.map((Key) => ({ Key })), Quiet: true },
  }));
  console.log(`deleted ${batch.length} stale object(s)`);
}
