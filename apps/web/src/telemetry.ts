import { BrowserClient, Scope, defaultStackParser, makeFetchTransport } from "@sentry/browser";
import { authErrorEvent, sanitizeAuthEvent, type AuthErrorDetails, type AuthErrorOperation } from "./auth-error-event";

let scope: Scope | undefined;
const reported = new WeakSet<object>();
const fingerprints = new Set<string>();

export function reportAuthError(operation: AuthErrorOperation, cause: unknown, details: AuthErrorDetails = {}): void {
  if (!import.meta.env.VITE_SENTRY_DSN) return;
  try {
    if (cause && typeof cause === "object" && reported.has(cause)) return;
    const event = authErrorEvent(operation, cause, details, location.pathname, navigator.userAgent);
    if (cause && typeof cause === "object") reported.add(cause);
    const fingerprint = JSON.stringify(event.fingerprint);
    if (fingerprints.has(fingerprint)) return;
    if (!scope) {
      const client = new BrowserClient({
        dsn: import.meta.env.VITE_SENTRY_DSN,
        environment: import.meta.env.VITE_SENTRY_ENVIRONMENT || import.meta.env.MODE,
        release: import.meta.env.VITE_SENTRY_RELEASE,
        transport: makeFetchTransport,
        stackParser: defaultStackParser,
        integrations: [],
        maxBreadcrumbs: 0,
        sendClientReports: false,
        dataCollection: { userInfo: false, cookies: false, httpHeaders: false, httpBodies: [], urlQueryParams: false, stackFrameVariables: false, frameContextLines: 0 },
        transportOptions: { fetchOptions: { credentials: "omit", referrerPolicy: "no-referrer" } },
        beforeSend: sanitizeAuthEvent,
      });
      client.init();
      scope = new Scope();
      scope.setClient(client);
    }
    fingerprints.add(fingerprint);
    scope.captureEvent(event);
  } catch { /* Reporting must never interrupt authentication. */ }
}
