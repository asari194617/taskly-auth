export interface LoginFlow {
  email: string;
  requestID: string;
  next: "" | "passkeys" | "security" | "profile" | "login-history";
  nextSection: SecurityDetailSection | "";
  challenge: string;
}

const key = "taskly-auth-login-flow";
const requestIDPattern = /^[A-Za-z0-9._~-]{43,128}$/;
export const schoolEmailDomain = "nagaoka-ct.ac.jp";
const emailPattern = /^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i;

export function normalizeLoginEmail(value: string): string | null {
  const input = value.trim().toLowerCase();
  const email = input.includes("@") ? input : `${input}@${schoolEmailDomain}`;
  return emailPattern.test(email) ? email : null;
}

export const securityDetailSections = ["email", "totp", "recovery", "password", "passkeys"] as const;
export type SecurityDetailSection = typeof securityDetailSections[number];

export function securityDetailSection(value: string | null): SecurityDetailSection | null {
  return securityDetailSections.find((section) => section === value) ?? null;
}

export function securityDetailURL(section: SecurityDetailSection | null): string {
  return section ? `/security?section=${section}` : "/security";
}

export function securityLoginURL(section: SecurityDetailSection | null): string {
  return `/login?next=security${section ? `&section=${section}` : ""}`;
}

export function securityMfaURL(section: SecurityDetailSection): string {
  return `/mfa?purpose=security&section=${section}`;
}

export function startFlow(search: string, storage: Storage): LoginFlow | null {
  const params = new URLSearchParams(search);
  const requestID = params.get("request_id") ?? "";
  const next = params.get("next");
  const management = !requestID && (next === "passkeys" || next === "security" || next === "profile" || next === "login-history") ? next : "";
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
  const flow: LoginFlow = {
    email: "", requestID, next: management,
    nextSection: management === "security" ? securityDetailSection(params.get("section")) ?? "" : "",
    challenge: "",
  };
  saveFlow(flow, storage);
  return flow;
}

export function loadFlow(storage: Storage): LoginFlow | null {
  try {
    const value = JSON.parse(storage.getItem(key) ?? "null") as LoginFlow | null;
    if (!value || typeof value.email !== "string" || typeof value.challenge !== "string" ||
        typeof value.requestID !== "string" || !["", "passkeys", "security", "profile", "login-history"].includes(value.next)) return null;
    if (!value.next && !requestIDPattern.test(value.requestID)) return null;
    const nextSection = value.nextSection ?? "";
    if (nextSection !== "" && (value.next !== "security" || !securityDetailSection(nextSection))) return null;
    return { ...value, nextSection };
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
