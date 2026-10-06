export interface LoginFlow {
  email: string;
  requestID: string;
  next: "" | "passkeys" | "security";
  challenge: string;
}

const key = "taskly-auth-login-flow";
const requestIDPattern = /^[A-Za-z0-9._~-]{43,128}$/;

export function startFlow(search: string, storage: Storage): LoginFlow | null {
  const params = new URLSearchParams(search);
  const requestID = params.get("request_id") ?? "";
  const next = params.get("next");
  const management = !requestID && (next === "passkeys" || next === "security") ? next : "";
  if (!requestID && !next) {
    const previous = loadFlow(storage);
    if (previous) {
      const restarted = { ...previous, email: "", challenge: "" };
      saveFlow(restarted, storage);
      return restarted;
    }
  }
  storage.removeItem(key);
  if (!management && !requestIDPattern.test(requestID)) return null;
  const flow: LoginFlow = { email: "", requestID, next: management, challenge: "" };
  saveFlow(flow, storage);
  return flow;
}

export function loadFlow(storage: Storage): LoginFlow | null {
  try {
    const value = JSON.parse(storage.getItem(key) ?? "null") as LoginFlow | null;
    if (!value || typeof value.email !== "string" || typeof value.challenge !== "string" ||
        typeof value.requestID !== "string" || !["", "passkeys", "security"].includes(value.next)) return null;
    if (!value.next && !requestIDPattern.test(value.requestID)) return null;
    return value;
  } catch {
    return null;
  }
}

export function saveFlow(flow: LoginFlow, storage: Storage): void {
  storage.setItem(key, JSON.stringify(flow));
}

export function clearFlow(storage: Storage): void {
  storage.removeItem(key);
}
