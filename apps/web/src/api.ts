import { apiBase } from "./config";

export async function request(path: string, body?: unknown): Promise<Response> {
  return fetch(apiBase + path, {
    method: body === undefined ? "GET" : "POST",
    credentials: "include",
    headers: {
      "X-Auth-UI": "1",
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
}

export async function json<T>(response: Response): Promise<T> {
  return response.json() as Promise<T>;
}
