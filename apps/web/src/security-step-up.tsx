import { useEffect, useState } from "react";
import { json, request } from "./api";
import { tr } from "./i18n";
import { LoadingStatus } from "./loading";
import { securityLoginURL, securityMfaURL, type SecurityDetailSection } from "./login-flow";

export type StepUpMethod = "email" | "totp" | "recovery";
export interface StepUpStatus { required: boolean; verified: boolean; methods: StepUpMethod[] }

export function useSecurityStepUp(section: SecurityDetailSection | null) {
  const [status, setStatus] = useState<StepUpStatus | null>(null);
  const [error, setError] = useState("");

  async function reload() {
    try {
      const response = await request("/auth/security/step-up/status");
      if (response.status === 401) { location.assign(securityLoginURL(section)); return; }
      if (!response.ok) throw new Error("status unavailable");
      const value = await json<StepUpStatus>(response);
      setStatus(value);
      setError("");
      if (section && value.required && !value.verified) location.replace(securityMfaURL(section));
    } catch {
      // A failed status check must never make a security change look available.
      setStatus(null);
      setError(tr("追加認証の状態を確認できませんでした。ページを再読み込みしてください。", "Could not check your additional verification status. Please reload the page."));
    }
  }

  useEffect(() => { void reload(); }, [section]);

  function invalidate() {
    setStatus((current) => current ? { ...current, required: true, verified: false } : null);
    if (section) location.assign(securityMfaURL(section));
  }

  return {
    status, error,
    canEdit: status !== null && (!status.required || status.verified),
    invalidate, reload,
  };
}

type StepUp = ReturnType<typeof useSecurityStepUp>;

export function SecurityStepUp({ stepUp }: { stepUp: StepUp }) {
  if (stepUp.canEdit) return null;
  if (stepUp.error) return <p className="message error" role="alert">{stepUp.error}</p>;
  return <LoadingStatus>{tr("追加認証の状態を確認しています。", "Checking additional verification status.")}</LoadingStatus>;
}
