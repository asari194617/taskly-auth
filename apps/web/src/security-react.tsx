import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, Checkbox, Modal, ModalAlign, ModalBody, ModalButtons, ModalHeader, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { json, request } from "./api";
import { AccountLayout } from "./account-layout";
import { SecurityStepUp, useSecurityStepUp } from "./security-step-up";

interface PasswordStatus { has_password: boolean }
interface EmailSettings { enabled: boolean }
interface TotpStatus { enabled: boolean }
interface TotpSetup { secret: string; otpauth: string }
interface RecoveryCodes { codes: string[] }
interface ErrorPayload { error?: string }

type SecuritySection = "overview" | "email" | "totp" | "recovery" | "password";
const sectionNames: Record<SecuritySection, string> = {
  overview: "認証設定",
  email: "メールコード",
  totp: "認証アプリ",
  recovery: "復旧コード",
  password: "パスワード",
};

function currentSection(): SecuritySection {
  const section = new URLSearchParams(location.search).get("section");
  return section && Object.hasOwn(sectionNames, section) ? section as SecuritySection : "overview";
}

function sectionURL(section: Exclude<SecuritySection, "overview">) {
  return `/security?section=${section}`;
}

function SecurityCard({ title, description, status, href }: { title: string; description: string; status?: string; href: string }) {
  return <a className="security-card" href={href}>
    <div><h2>{title}</h2><p>{description}</p>{status && <p className="security-card-status">{status}</p>}</div>
    <span className="security-card-link">設定を開く<span aria-hidden="true"> →</span></span>
  </a>;
}

function SecurityApp() {
  const section = currentSection();
  const stepUp = useSecurityStepUp("security");
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
  const [showRecoveryConfirm, setShowRecoveryConfirm] = useState(false);

  async function fetchStatus<T>(path: string, update: (value: T) => void) {
    try {
      const response = await request(path);
      if (response.status === 401) { location.assign("/login?next=security"); return; }
      if (!response.ok) throw new Error("status");
      update(await json<T>(response));
    } catch { setError("認証設定を読み込めませんでした。"); }
  }
  useEffect(() => {
    document.title = `${sectionNames[section]} | Taskly 認証`;
    void fetchStatus<PasswordStatus>("/auth/password/status", setPasswordStatus);
    void fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
    void fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus);
  }, [section]);

  async function perform(path: string, body: unknown, failure: string, success: string, done?: (response: Response) => Promise<void> | void) {
    if (!stepUp.canEdit) { setError("設定を変更する前に本人確認をしてください。"); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const response = await request(path, body);
      if (response.status === 401) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (payload.error === "login_required") { location.assign("/login?next=security"); return; }
      }
      if (response.status === 403) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (payload.error === "step_up_required") { stepUp.invalidate(); return; }
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
      setTotpSetup(null); setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus); await stepUp.reload();
    });
  }
  function disableTotp(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/totp/disable", { code: totpCode }, "認証アプリを無効にできませんでした。別のログイン方法が必要です。", "認証アプリを無効にしました。", async () => {
      setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus); await stepUp.reload();
    });
  }
  function changePassword(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/password", { current: currentPassword, next: nextPassword }, "パスワードを更新できませんでした。現在のパスワードと入力内容を確認してください。", "パスワードを更新しました。再度ログインしてください。", () => {
      setCurrentPassword(""); setNextPassword(""); setPasswordChanged(true);
    });
  }
  function generateRecoveryCodes() {
    if (!stepUp.canEdit || busy) return;
    setShowRecoveryConfirm(false);
    setRecoveryCodes([]);
    void perform("/auth/recovery/generate", {}, "復旧コードを再発行できませんでした。", "復旧コードを再発行しました。この画面を閉じると再表示できません。", async (response) => {
      setRecoveryCodes((await json<RecoveryCodes>(response)).codes);
    });
  }

  return <AccountLayout page="security">
    {section !== "overview" && <a className="security-back-link" href="/security">← 認証設定に戻る</a>}
    <h1>{sectionNames[section]}</h1>
    {section === "overview" && <p className="account-intro">ログイン方法とアカウントの保護方法を選んで管理できます。</p>}
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    {section === "overview" && <div className="security-cards">
      <SecurityCard title="メールコード" description="メールに届くコードでログインします。" status={emailSettings ? emailSettings.enabled ? "有効" : "無効" : "読み込み中"} href={sectionURL("email")} />
      <SecurityCard title="認証アプリ" description="認証アプリが生成するコードを使用します。" status={totpStatus ? totpStatus.enabled ? "有効" : "無効" : "読み込み中"} href={sectionURL("totp")} />
      <SecurityCard title="復旧コード" description="ほかの認証方法を利用できないときのためにコードを再発行します。" href={sectionURL("recovery")} />
      <SecurityCard title="パスワード" description="パスワードの設定と変更を行います。" status={passwordStatus ? passwordStatus.has_password ? "設定済み" : "未設定" : "読み込み中"} href={sectionURL("password")} />
      <SecurityCard title="パスキー" description="登録済みの端末やセキュリティキーを管理します。" href="/passkeys" />
    </div>}
    {section !== "overview" && <SecurityStepUp stepUp={stepUp} />}
    {section === "email" && stepUp.canEdit && <section className="security-detail">
      <p>メールに届くコードをログインに使用します。無効にするには、パスワードかパスキーが必要です。</p>
      {emailSettings && <form onSubmit={updateEmail}>
        <Checkbox checked={emailSettings.enabled} onChange={(enabled) => setEmailSettings({ enabled })}>メールコードを使う</Checkbox>
        <Button type="submit" variant="Primary" disabled={busy}>設定を保存</Button>
      </form>}
    </section>}
    {section === "recovery" && stepUp.canEdit && <section className="security-detail">
      <p>再発行すると以前の未使用コードは使えなくなります。表示されるコードを安全な場所に保存してください。</p>
      <Button type="button" onClick={() => setShowRecoveryConfirm(true)} disabled={busy}>復旧コードを再発行</Button>
      {recoveryCodes.length > 0 && <pre>{recoveryCodes.join("\n")}</pre>}
    </section>}
    {section === "totp" && stepUp.canEdit && totpStatus && <section className="security-detail">
      <p>認証アプリは現在{totpStatus.enabled ? "有効" : "無効"}です。</p>
      {!totpStatus.enabled && <Button type="button" onClick={() => void perform("/auth/totp/setup", {}, "設定を開始できませんでした。", "認証アプリに秘密鍵を登録してください。", async (response) => setTotpSetup(await json<TotpSetup>(response)))} disabled={busy}>設定を始める</Button>}
      {totpSetup && !totpStatus.enabled && <><p>認証アプリで次のリンクを開くか、秘密鍵を手動入力してください。</p><p><a href={totpSetup.otpauth}>認証アプリで開く</a></p><pre>{totpSetup.secret}</pre><form onSubmit={activateTotp}><TextField id="totp-activate-code" label="認証アプリの6桁コード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" variant="Primary" disabled={busy}>有効にする</Button></form></>}
      {totpStatus.enabled && <form onSubmit={disableTotp}><TextField id="totp-disable-code" label="現在の6桁コード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" disabled={busy}>無効にする</Button></form>}
    </section>}
    {section === "password" && stepUp.canEdit && passwordStatus && <section className="security-detail">
      <p>{passwordStatus.has_password ? "現在のパスワードを入力して変更します。" : "ログインに使うパスワードを設定します。"}</p>
      {passwordChanged ? <a href="/login?next=security">ログインし直す</a> : <form onSubmit={changePassword}>{passwordStatus.has_password && <TextField id="current" label="現在のパスワード" showLabel type="password" autoComplete="current-password" value={currentPassword} onChange={setCurrentPassword} />}<TextField id="next" label="新しいパスワード（8文字以上）" showLabel type="password" required minLength={8} autoComplete="new-password" value={nextPassword} onChange={setNextPassword} /><Button type="submit" variant="Primary" disabled={busy}>更新する</Button></form>}
    </section>}
    {showRecoveryConfirm && <Modal title="復旧コードを再発行" size="S" isOpen onClose={() => setShowRecoveryConfirm(false)} isDismissable closeButtonAriaLabel="閉じる">
      <ModalHeader />
      <ModalBody>
        <ModalAlign><p className="account-dialog-description">再発行すると、以前の未使用コードは使えなくなります。新しいコードを安全な場所に保存してください。</p></ModalAlign>
        <ModalButtons>
          <Button type="button" variant="Danger" fullWidth onClick={generateRecoveryCodes}>再発行する</Button>
          <Button type="button" fullWidth onClick={() => setShowRecoveryConfirm(false)}>キャンセル</Button>
        </ModalButtons>
      </ModalBody>
    </Modal>}
  </AccountLayout>;
}
createRoot(document.getElementById("app")!).render(<SecurityApp />);
