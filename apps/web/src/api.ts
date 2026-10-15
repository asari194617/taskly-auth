import { apiBase } from "./config";
import { reportAuthError } from "./telemetry";

export async function request(path: string, body?: unknown): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(apiBase + path, {
      method: body === undefined ? "GET" : "POST",
      credentials: "include",
      headers: {
        "X-Auth-UI": "1",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (cause) {
    reportAuthError("api.network", cause, { endpoint: path });
    throw cause;
  }
  if (response.status >= 500) reportAuthError("api.server", response, { endpoint: path, status: response.status });
  return response;
}

export async function json<T>(response: Response): Promise<T> {
  return response.json() as Promise<T>;
}
