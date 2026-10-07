import { Button, TextField } from "@charcoal-ui/react";
import { useEffect, useState, type FormEvent } from "react";
import { json, request } from "./api";

type Method = "email" | "totp" | "recovery";
interface Status { required: boolean; verified: boolean; methods: Method[] }
interface ErrorPayload { error?: string }

export function useSecurityStepUp(next: "security" | "passkeys") {
  const [status, setStatus] = useState<Status | null>(null);
  const [method, setMethod] = useState<Method>("email");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
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
      setError("追加認証の状態を確認できませんでした。ページを再読み込みしてください。");
    }
  }

  useEffect(() => { void reload(); }, [next]);

  async function requestCode() {
    setBusy(true); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/request-code", {});
      if (response.status === 401) { location.assign(`/login?next=${next}`); return; }
      if (!response.ok) throw new Error("確認コードを送信できませんでした。しばらくしてからお試しください。");
      setSent(true); setNotice("登録メールアドレスへ確認コードを送信しました。");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "確認コードを送信できませんでした。");
    } finally { setBusy(false); }
  }

  async function verify(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/verify", { method, code });
      if (!response.ok) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (response.status === 401 && payload.error === "login_required") { location.assign(`/login?next=${next}`); return; }
        throw new Error(payload.error === "rate_limited" ? "試行回数の上限です。時間をおいてください。" : "コードを確認できませんでした。入力内容と有効期限を確認してください。");
      }
      setCode(""); setSent(false);
      await reload();
      setNotice("本人確認が完了しました。設定を変更できます。");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "本人確認を完了できませんでした。");
    } finally { setBusy(false); }
  }

  function invalidate() {
    setStatus((current) => current ? { ...current, required: true, verified: false } : null);
    setError("確認の有効期限が切れました。もう一度本人確認してください。");
  }

  return {
    status, method, setMethod, code, setCode, sent, busy, error, notice,
    canEdit: status !== null && (!status.required || status.verified),
    requestCode, verify, invalidate, reload,
  };
}

type StepUp = ReturnType<typeof useSecurityStepUp>;

export function SecurityStepUp({ stepUp }: { stepUp: StepUp }) {
  if (stepUp.status && (!stepUp.status.required || stepUp.status.verified)) return null;
  return <section className="security-step-up" aria-labelledby="security-step-up-heading">
    <h2 id="security-step-up-heading">設定変更のための追加認証</h2>
    {stepUp.status ? <>
      <p>認証設定を変更する前に、本人確認をしてください。確認後5分間、設定を変更できます。</p>
      {stepUp.status.methods.includes("totp") && <div className="step-up-methods" aria-label="確認方法">
        <Button type="button" size="S" variant="Navigation" isActive={stepUp.method === "totp"} aria-pressed={stepUp.method === "totp"} onClick={() => { stepUp.setMethod("totp"); stepUp.setCode(""); }}>認証アプリ</Button>
        {stepUp.status.methods.includes("recovery") && <Button type="button" size="S" variant="Navigation" isActive={stepUp.method === "recovery"} aria-pressed={stepUp.method === "recovery"} onClick={() => { stepUp.setMethod("recovery"); stepUp.setCode(""); }}>復旧コード</Button>}
      </div>}
      {stepUp.method === "email" && <Button type="button" onClick={() => void stepUp.requestCode()} disabled={stepUp.busy}>{stepUp.sent ? "コードを再送信" : "登録メールへコードを送信"}</Button>}
      {(stepUp.method !== "email" || stepUp.sent) && <form onSubmit={(event) => void stepUp.verify(event)}>
        <TextField id="security-step-up-code" label={stepUp.method === "email" ? "メールの6桁コード" : stepUp.method === "totp" ? "認証アプリの6桁コード" : "復旧コード"} showLabel required inputMode={stepUp.method === "recovery" ? "text" : "numeric"} pattern={stepUp.method === "recovery" ? undefined : "[0-9]{6}"} autoComplete={stepUp.method === "recovery" ? "off" : "one-time-code"} value={stepUp.code} onChange={stepUp.setCode} />
        <Button type="submit" variant="Primary" disabled={stepUp.busy}>確認する</Button>
      </form>}
    </> : !stepUp.error && <p>追加認証の状態を読み込んでいます。</p>}
    <p className="message error" role="alert">{stepUp.error}</p>
    <p className="message" role="status">{stepUp.notice}</p>
  </section>;
}
