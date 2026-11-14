export const consentScopes = ["guidance:read", "guidance:bookmarks:write", "guidance:tasks:write"] as const;
export type ConsentScope = typeof consentScopes[number];
export interface ConsentDetails {
  client_name: string;
  redirect_uri: string;
  resource: string;
  scopes: ConsentScope[];
  csrf_token: string;
}

const opaqueToken = /^[A-Za-z0-9_-]{43}$/;

export function consentRequestID(search: string): string | null {
  const values = new URLSearchParams(search).getAll("request_id");
  return values.length === 1 && opaqueToken.test(values[0]!) ? values[0]! : null;
}

function webURL(value: unknown, allowLoopback: boolean): URL | null {
  if (typeof value !== "string" || value.length > 2048) return null;
  try {
    const url = new URL(value);
    const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
    return !url.username && !url.password && !url.hash &&
      (url.protocol === "https:" || (allowLoopback && loopback && url.protocol === "http:")) ? url : null;
  } catch { return null; }
}

export function parseConsentDetails(value: unknown, allowLoopback = false): ConsentDetails | null {
  if (!value || typeof value !== "object") return null;
  const input = value as Record<string, unknown>;
  const redirect = webURL(input.redirect_uri, allowLoopback);
  const resource = webURL(input.resource, allowLoopback);
  if (typeof input.client_name !== "string" || !input.client_name.trim() || input.client_name.length > 200 ||
    !redirect || !resource || resource.search ||
    typeof input.csrf_token !== "string" || !opaqueToken.test(input.csrf_token) ||
    !Array.isArray(input.scopes) || input.scopes.length < 1 || input.scopes.length > consentScopes.length ||
    !input.scopes.includes("guidance:read") || new Set(input.scopes).size !== input.scopes.length ||
    input.scopes.some((scope) => !consentScopes.includes(scope as ConsentScope))) return null;
  return {
    client_name: input.client_name,
    redirect_uri: redirect.href,
    resource: resource.href,
    csrf_token: input.csrf_token,
    scopes: input.scopes as ConsentScope[],
  };
}

export function consentRedirectURL(value: unknown, registeredRedirect: string, allowLoopback = false): string | null {
  if (!value || typeof value !== "object") return null;
  const target = webURL((value as Record<string, unknown>).redirect_uri, allowLoopback);
  const registered = webURL(registeredRedirect, allowLoopback);
  if (!target || !registered || target.origin !== registered.origin || target.pathname !== registered.pathname) return null;
  for (const key of new Set(registered.searchParams.keys())) {
    const expected = registered.searchParams.getAll(key);
    const actual = target.searchParams.getAll(key);
    if (expected.length !== actual.length || expected.some((entry, index) => entry !== actual[index])) return null;
  }
  const allowed = new Set(["code", "state", "iss", "error", "error_description", ...registered.searchParams.keys()]);
  if ([...target.searchParams.keys()].some((key) => !allowed.has(key))) return null;
  if (["code", "state", "iss", "error", "error_description"].some((key) => target.searchParams.getAll(key).length > 1)) return null;
  if (!target.searchParams.get("state") || !target.searchParams.get("iss")) return null;
  const code = target.searchParams.get("code");
  const error = target.searchParams.get("error");
  if ((!code && !error) || (code && error)) return null;
  return target.href;
}
