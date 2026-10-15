import type { ErrorEvent } from "@sentry/browser";

export const authErrorOperations = ["passkey.support", "passkey.begin", "passkey.credential", "passkey.finish", "login.submit", "email-code.send", "api.network", "api.server"] as const;
export type AuthErrorOperation = typeof authErrorOperations[number];
const errorNames = ["Error", "TypeError", "SyntaxError", "RangeError", "NotAllowedError", "NotSupportedError", "SecurityError", "InvalidStateError", "AbortError", "TimeoutError", "NetworkError", "UnknownError", "HttpError", "Unavailable"];
const pages = ["/login", "/password", "/mail-code", "/mfa", "/security", "/profile", "/passkeys", "/login-history", "/logout"];
const endpoints = ["/auth/login", "/auth/request-code", "/auth/passkey/begin", "/auth/passkey/finish", "/auth/security/step-up/status", "/auth/security/step-up/request-code", "/auth/security/step-up/verify", "/auth/logout", "/auth/profile", "/auth/login-history", "/auth/login-history/export"];

export interface AuthErrorDetails { endpoint?: string; status?: number }
const browserNames: Record<string, string> = { GSA: "Google app", CriOS: "Chrome", Chrome: "Chrome", FxiOS: "Firefox", Firefox: "Firefox", Edg: "Edge", Version: "Safari" };
const osNames = ["iOS", "Android", "Windows", "macOS", "Linux"];

export function authErrorEvent(operation: AuthErrorOperation, cause: unknown, details: AuthErrorDetails, pathname: string, userAgent: string): ErrorEvent {
  const rawName = cause && typeof cause === "object" && "name" in cause ? cause.name : details.status ? "HttpError" : "Error";
  const browser = /GSA\/[\d.]+/.exec(userAgent) ?? /Edg\/[\d.]+/.exec(userAgent) ?? /(?:CriOS|Chrome|FxiOS|Firefox|Version)\/[\d.]+/.exec(userAgent);
  const [browserToken = "Other", version = ""] = browser?.[0].split("/") ?? [];
  const browserName = browserNames[browserToken] ?? "Other";
  const osName = /iPhone|iPad|iPod|Macintosh.*Mobile/.test(userAgent) ? "iOS" : /Android/.test(userAgent) ? "Android" : /Windows/.test(userAgent) ? "Windows" : /Macintosh/.test(userAgent) ? "macOS" : /Linux/.test(userAgent) ? "Linux" : "Other";
  return makeEvent(operation, rawName, details, pathname, browserName, version.slice(0, 30), osName);
}

function makeEvent(operation: AuthErrorOperation, rawName: unknown, details: AuthErrorDetails, pathname: string, rawBrowser: unknown, rawVersion: unknown, rawOS: unknown): ErrorEvent {
  const name = typeof rawName === "string" && errorNames.includes(rawName) ? rawName : "Error";
  const endpoint = endpoints.includes(details.endpoint ?? "") ? details.endpoint! : "other";
  const status = Number.isInteger(details.status) && details.status! >= 100 && details.status! <= 599 ? String(details.status) : "none";
  const page = pages.includes(pathname) ? pathname : "other";
  const browserName = typeof rawBrowser === "string" && Object.values(browserNames).includes(rawBrowser) ? rawBrowser : "Other";
  const version = typeof rawVersion === "string" && /^[\d.]{1,30}$/.test(rawVersion) ? rawVersion : "";
  const osName = typeof rawOS === "string" && osNames.includes(rawOS) ? rawOS : "Other";
  const warning = operation === "passkey.support" || name === "NotAllowedError" || name === "AbortError";
  return {
    type: undefined,
    platform: "javascript",
    level: warning ? "warning" : "error",
    message: `Auth UI failure: ${operation}`,
    exception: { values: [{ type: name, value: `Auth UI failure: ${operation}`, mechanism: { type: "handled", handled: true } }] },
    fingerprint: ["auth-ui", operation, name, endpoint, status],
    tags: { component: "auth-ui", "auth.operation": operation, "auth.error_name": name, "auth.page": page, "auth.endpoint": endpoint, "http.status_code": status, "browser.name": browserName },
    contexts: { browser: { name: browserName, version }, os: { name: osName } },
  };
}

// Keep only fields constructed by the reporter. In particular, SDK scope data,
// request URLs, breadcrumbs, user details, and exception messages are omitted.
export function sanitizeAuthEvent(event: ErrorEvent): ErrorEvent | null {
  const operation = event.tags?.["auth.operation"];
  if (!authErrorOperations.includes(operation as AuthErrorOperation)) return null;
  const safe = makeEvent(operation as AuthErrorOperation, event.tags?.["auth.error_name"],
    { endpoint: String(event.tags?.["auth.endpoint"] ?? ""), status: Number(event.tags?.["http.status_code"]) },
    String(event.tags?.["auth.page"] ?? ""), event.contexts?.browser?.name, event.contexts?.browser?.version, event.contexts?.os?.name);
  return { ...safe, event_id: event.event_id, timestamp: event.timestamp, environment: event.environment, release: event.release, sdk: { name: event.sdk?.name, version: event.sdk?.version, settings: { infer_ip: "never" } } };
}
