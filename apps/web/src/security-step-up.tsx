import { Button, TextField } from "@charcoal-ui/react";
import { useEffect, useState, type FormEvent } from "react";
import { json, request } from "./api";
import { tr } from "./i18n";
import { BusyLabel, LoadingStatus } from "./loading";

type Method = "email" | "totp" | "recovery";
interface Status { required: boolean; verified: boolean; methods: Method[] }
interface ErrorPayload { error?: string }

export function useSecurityStepUp(next: "security" | "passkeys") {
  const [status, setStatus] = useState<Status | null>(null);
  const [method, setMethod] = useState<Method>("email");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busyAction, setBusyAction] = useState<"send" | "verify" | null>(null);
  const busy = busyAction !== null;
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function reload() {
    try {
      const response = await request("/auth/security/step-up/status");
      if (response.status === 401) { location.assign(`/login?next=${next}`); return; }
      if (!response.ok) throw new Error("status unavailable");
      const value = await json<Status>(response);
      setStatus(value);
      setMethod((current) => value.methods.includes(current) ? current : value.methods[0] ?? "email");
      setError("");
    } catch {
      // A failed status check must never make a security change look available.
      setStatus(null);
      setError(tr("追加認証の状態を確認できませんでした。ページを再読み込みしてください。", "Could not check your additional verification status. Please reload the page."));
    }
  }

  useEffect(() => { void reload(); }, [next]);

  async function requestCode() {
    setBusyAction("send"); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/request-code", {});
      if (response.status === 401) { location.assign(`/login?next=${next}`); return; }
      if (!response.ok) throw new Error(tr("確認コードを送信できませんでした。しばらくしてからお試しください。", "Could not send a verification code. Please try again later."));
      setSent(true); setNotice(tr("登録メールアドレスへ確認コードを送信しました。", "A verification code was sent to your registered email address."));
    } catch (cause) {
      setError(cause instanceof Error && !(cause instanceof TypeError) ? cause.message : tr("確認コードを送信できませんでした。", "Could not send a verification code."));
    } finally { setBusyAction(null); }
  }

  async function verify(event: FormEvent) {
    event.preventDefault(); setBusyAction("verify"); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/verify", { method, code });
      if (!response.ok) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (response.status === 401 && payload.error === "login_required") { location.assign(`/login?next=${next}`); return; }
        throw new Error(payload.error === "rate_limited"
          ? tr("試行回数の上限です。時間をおいてください。", "Too many attempts. Please try again later.")
          : tr("コードを確認できませんでした。入力内容と有効期限を確認してください。", "Could not verify the code. Check the code and its expiration time."));
      }
      setCode(""); setSent(false);
      await reload();
      setNotice(tr("本人確認が完了しました。設定を変更できます。", "Verification complete. You can now change your settings."));
    } catch (cause) {
      setError(cause instanceof Error && !(cause instanceof TypeError) ? cause.message : tr("本人確認を完了できませんでした。", "Could not complete verification."));
    } finally { setBusyAction(null); }
  }

  function invalidate() {
    setStatus((current) => current ? { ...current, required: true, verified: false } : null);
    setError(tr("確認の有効期限が切れました。もう一度本人確認してください。", "Your verification has expired. Please verify again."));
  }

  return {
    status, method, setMethod, code, setCode, sent, busy, busyAction, error, notice,
    canEdit: status !== null && (!status.required || status.verified),
    requestCode, verify, invalidate, reload,
  };
}

type StepUp = ReturnType<typeof useSecurityStepUp>;

export function SecurityStepUp({ stepUp }: { stepUp: StepUp }) {
  if (stepUp.status && (!stepUp.status.required || stepUp.status.verified)) return null;
  return <section className="security-step-up" aria-labelledby="security-step-up-heading">
    <h2 id="security-step-up-heading">{tr("設定変更のための追加認証", "Additional verification for settings changes")}</h2>
    {stepUp.status ? <>
      <p>{tr("認証設定を変更する前に、本人確認をしてください。確認後5分間、設定を変更できます。", "Verify your identity before changing authentication settings. You can make changes for five minutes afterward.")}</p>
      {stepUp.status.methods.includes("totp") && <div className="step-up-methods" aria-label={tr("確認方法", "Verification method")}>
        <Button type="button" size="S" variant="Navigation" isActive={stepUp.method === "totp"} aria-pressed={stepUp.method === "totp"} onClick={() => { stepUp.setMethod("totp"); stepUp.setCode(""); }}>{tr("認証アプリ", "Authenticator app")}</Button>
        {stepUp.status.methods.includes("recovery") && <Button type="button" size="S" variant="Navigation" isActive={stepUp.method === "recovery"} aria-pressed={stepUp.method === "recovery"} onClick={() => { stepUp.setMethod("recovery"); stepUp.setCode(""); }}>{tr("復旧コード", "Recovery code")}</Button>}
      </div>}
      {stepUp.method === "email" && <Button type="button" onClick={() => void stepUp.requestCode()} disabled={stepUp.busy}><BusyLabel busy={stepUp.busyAction === "send"}>{stepUp.sent ? tr("コードを再送信", "Resend code") : tr("登録メールへコードを送信", "Send code to registered email")}</BusyLabel></Button>}
      {(stepUp.method !== "email" || stepUp.sent) && <form onSubmit={(event) => void stepUp.verify(event)}>
        <TextField id="security-step-up-code" label={stepUp.method === "email" ? tr("メールの6桁コード", "Six-digit email code") : stepUp.method === "totp" ? tr("認証アプリの6桁コード", "Six-digit authenticator code") : tr("復旧コード", "Recovery code")} showLabel required inputMode={stepUp.method === "recovery" ? "text" : "numeric"} pattern={stepUp.method === "recovery" ? undefined : "[0-9]{6}"} autoComplete={stepUp.method === "recovery" ? "off" : "one-time-code"} value={stepUp.code} onChange={stepUp.setCode} />
        <Button type="submit" variant="Primary" disabled={stepUp.busy}><BusyLabel busy={stepUp.busyAction === "verify"}>{tr("確認する", "Verify")}</BusyLabel></Button>
      </form>}
    </> : !stepUp.error && <LoadingStatus>{tr("追加認証の状態を読み込んでいます。", "Loading verification status")}</LoadingStatus>}
    <p className="message error" role="alert">{stepUp.error}</p>
    <p className="message" role="status">{stepUp.notice}</p>
  </section>;
}
