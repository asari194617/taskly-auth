import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { json, request } from "./api";
import { AuthHeader } from "./auth-header";

interface PasswordStatus { has_password: boolean }
interface EmailSettings { enabled: boolean }
interface TotpStatus { enabled: boolean }
interface TotpSetup { secret: string; otpauth: string }
interface RecoveryCodes { codes: string[] }
interface ErrorPayload { error?: string }

function SecurityApp() {
  const [passwordStatus, setPasswordStatus] = useState<PasswordStatus | null>(null);
  const [emailSettings, setEmailSettings] = useState<EmailSettings | null>(null);
  const [totpStatus, setTotpStatus] = useState<TotpStatus | null>(null);
  const [totpSetup, setTotpSetup] = useState<TotpSetup | null>(null);
  const [totpCode, setTotpCode] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [nextPassword, setNextPassword] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [passwordChanged, setPasswordChanged] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function fetchStatus<T>(path: string, update: (value: T) => void) {
    try {
      const response = await request(path);
      if (response.status === 401) { location.assign("/login?next=security"); return; }
      if (!response.ok) throw new Error("status");
      update(await json<T>(response));
    } catch { setError("認証設定を読み込めませんでした。"); }
  }
  useEffect(() => {
    void fetchStatus<PasswordStatus>("/auth/password/status", setPasswordStatus);
    void fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
    void fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus);
  }, []);

  async function perform(path: string, body: unknown, failure: string, success: string, done?: (response: Response) => Promise<void> | void) {
    setBusy(true); setError(""); setNotice("");
    try {
      const response = await request(path, body);
      if (response.status === 401) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (payload.error === "login_required") { location.assign("/login?next=security"); return; }
      }
      if (!response.ok) throw new Error(failure);
      await done?.(response);
      setNotice(success);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : failure);
      if (path === "/auth/email-code/settings") {
        void fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
      }
    }
    finally { setBusy(false); }
  }

  function updateEmail(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/email-code/settings", { enabled: emailSettings?.enabled }, "メールコード設定を更新できませんでした。パスワードかパスキーが必要な場合があります。", "メールコード設定を更新しました。", async () => {
      await fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
    });
  }
  function activateTotp(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/totp/activate", { code: totpCode }, "認証アプリを有効にできませんでした。", "認証アプリを有効にしました。", async () => {
      setTotpSetup(null); setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus);
    });
  }
  function disableTotp(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/totp/disable", { code: totpCode }, "認証アプリを無効にできませんでした。別のログイン方法が必要です。", "認証アプリを無効にしました。", async () => {
      setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus);
    });
  }
  function changePassword(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/password", { current: currentPassword, next: nextPassword }, "パスワードを更新できませんでした。現在のパスワードと入力内容を確認してください。", "パスワードを更新しました。再度ログインしてください。", () => {
      setCurrentPassword(""); setNextPassword(""); setPasswordChanged(true);
    });
  }
  function generateRecoveryCodes() {
    if (!confirm("以前の未使用コードは使えなくなります。復旧コードを再発行しますか？")) return;
    setRecoveryCodes([]);
    void perform("/auth/recovery/generate", {}, "復旧コードを再発行できませんでした。", "復旧コードを再発行しました。この画面を閉じると再表示できません。", async (response) => {
      setRecoveryCodes((await json<RecoveryCodes>(response)).codes);
    });
  }

  return <CharcoalProvider><main className="wide">
    <AuthHeader />
    <h1>認証設定</h1><p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    {emailSettings && <section><h2>メールコード</h2><p>無効にするにはパスワードかパスキーが必要です。</p><form onSubmit={updateEmail}><label className="checkbox"><input type="checkbox" checked={emailSettings.enabled} onChange={(event) => setEmailSettings({ enabled: event.target.checked })} /> メールコードを使う</label><Button type="submit" variant="Primary" disabled={busy}>設定を保存</Button></form></section>}
    <section><h2>復旧コード</h2><p>再発行すると以前の未使用コードは使えなくなります。表示されるコードを安全な場所に保存してください。</p><Button type="button" onClick={generateRecoveryCodes} disabled={busy}>復旧コードを再発行</Button>{recoveryCodes.length > 0 && <pre>{recoveryCodes.join("\n")}</pre>}</section>
    {totpStatus && <section><h2>認証アプリ</h2><p>{totpStatus.enabled ? "有効です。" : "無効です。"}</p>{!totpStatus.enabled && <Button type="button" onClick={() => void perform("/auth/totp/setup", {}, "設定を開始できませんでした。", "認証アプリに秘密鍵を登録してください。", async (response) => setTotpSetup(await json<TotpSetup>(response)))} disabled={busy}>設定を始める</Button>}
      {totpSetup && !totpStatus.enabled && <><p>認証アプリで次のリンクを開くか、秘密鍵を手動入力してください。</p><p><a href={totpSetup.otpauth}>認証アプリで開く</a></p><pre>{totpSetup.secret}</pre><form onSubmit={activateTotp}><TextField id="totp-activate-code" label="認証アプリの6桁コード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" variant="Primary" disabled={busy}>有効にする</Button></form></>}
      {totpStatus.enabled && <form onSubmit={disableTotp}><TextField id="totp-disable-code" label="現在の6桁コード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" disabled={busy}>無効にする</Button></form>}
    </section>}
    {passwordStatus && <section><h2>パスワード</h2>{passwordChanged ? <a href="/login?next=security">ログインし直す</a> : <form onSubmit={changePassword}>{passwordStatus.has_password && <TextField id="current" label="現在のパスワード" showLabel type="password" autoComplete="current-password" value={currentPassword} onChange={setCurrentPassword} />}<TextField id="next" label="新しいパスワード（8文字以上）" showLabel type="password" required minLength={8} autoComplete="new-password" value={nextPassword} onChange={setNextPassword} /><Button type="submit" variant="Primary" disabled={busy}>更新する</Button></form>}</section>}
    <nav><a href="/profile">アカウント情報</a><a href="/passkeys">パスキーを管理</a></nav>
  </main></CharcoalProvider>;
}
createRoot(document.getElementById("app")!).render(<SecurityApp />);
