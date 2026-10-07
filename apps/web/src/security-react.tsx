import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, Modal, ModalAlign, ModalBody, ModalButtons, ModalHeader, Switch, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { json, request } from "./api";
import { AccountLayout } from "./account-layout";
import { BusyLabel, LoadingStatus } from "./loading";
import { PasskeysSection } from "./passkeys-react";
import { SecurityStepUp, useSecurityStepUp } from "./security-step-up";
import { LocaleRoot, tr, useLocale } from "./i18n";
import { securityLoginURL } from "./login-flow";

interface PasswordStatus { has_password: boolean }
interface EmailSettings { enabled: boolean }
interface TotpStatus { enabled: boolean }
interface TotpSetup { secret: string; otpauth: string }
interface RecoveryCodes { codes: string[] }
interface RecoveryStatus { issued: boolean; remaining: number }
interface PasskeyList { passkeys: { id: number }[] }
interface ErrorPayload { error?: string }

type SecuritySection = "overview" | "email" | "totp" | "recovery" | "password" | "passkeys";
const sectionNames: Record<SecuritySection, [string, string]> = {
  overview: ["認証設定", "Security settings"],
  email: ["メールコード", "Email codes"],
  totp: ["認証アプリ", "Authenticator app"],
  recovery: ["復旧コード", "Recovery codes"],
  password: ["パスワード", "Password"],
  passkeys: ["パスキー", "Passkeys"],
};

function sectionTitle(section: SecuritySection): string {
  return tr(...sectionNames[section]);
}

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
    <span className="security-card-link">{tr("設定を開く", "Open settings")}<span aria-hidden="true"> →</span></span>
  </a>;
}

function recoverySummary(status: RecoveryStatus): string {
  if (!status.issued) return tr("未発行", "Not issued");
  return tr(`発行済み・残り${status.remaining}件`, `Issued · ${status.remaining} code${status.remaining === 1 ? "" : "s"} remaining`);
}

function passkeySummary(count: number): string {
  return tr(`${count}件登録済み`, `${count} passkey${count === 1 ? "" : "s"} registered`);
}

function SecurityApp() {
  const locale = useLocale();
  const section = currentSection();
  const stepUp = useSecurityStepUp(section === "overview" ? null : section);
  const [passwordStatus, setPasswordStatus] = useState<PasswordStatus | null>(null);
  const [emailSettings, setEmailSettings] = useState<EmailSettings | null>(null);
  const [totpStatus, setTotpStatus] = useState<TotpStatus | null>(null);
  const [recoveryStatus, setRecoveryStatus] = useState<RecoveryStatus | null>(null);
  const [passkeyCount, setPasskeyCount] = useState<number | null>(null);
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
  const [statusLoading, setStatusLoading] = useState(true);

  async function fetchStatus<T>(path: string, update: (value: T) => void) {
    try {
      const response = await request(path);
      if (response.status === 401) { location.assign(securityLoginURL(section === "overview" ? null : section)); return; }
      if (!response.ok) throw new Error("status");
      update(await json<T>(response));
    } catch { setError(tr("認証設定を読み込めませんでした。", "Could not load security settings.")); }
  }
  useEffect(() => {
    document.title = `${sectionTitle(section)} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [section, locale]);
  useEffect(() => {
    const reads: Promise<void>[] = [];
    if (section === "overview" || section === "password") reads.push(fetchStatus<PasswordStatus>("/auth/password/status", setPasswordStatus));
    if (section === "overview" || section === "email") reads.push(fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings));
    if (section === "overview" || section === "totp") reads.push(fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus));
    if (section === "overview" || section === "recovery") reads.push(fetchStatus<RecoveryStatus>("/auth/recovery/status", setRecoveryStatus));
    if (section === "overview") reads.push(fetchStatus<PasskeyList>("/auth/passkeys", (value) => setPasskeyCount(value.passkeys.length)));
    if (section === "overview") void Promise.all(reads).finally(() => setStatusLoading(false));
  }, [section]);

  async function perform(path: string, body: unknown, failure: string, success: string, done?: (response: Response) => Promise<void> | void) {
    if (!stepUp.canEdit) { setError(tr("設定を変更する前に本人確認をしてください。", "Verify your identity before changing settings.")); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const response = await request(path, body);
      if (response.status === 401) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (payload.error === "login_required") { location.assign(securityLoginURL(section === "overview" ? null : section)); return; }
      }
      if (response.status === 403) {
        const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
        if (payload.error === "step_up_required") {
          stepUp.invalidate();
          if (path === "/auth/email-code/settings") void fetchStatus<EmailSettings>(path, setEmailSettings);
          return;
        }
      }
      if (!response.ok) throw new Error(failure);
      await done?.(response);
      setNotice(success);
    } catch {
      setError(failure);
      if (path === "/auth/email-code/settings") {
        void fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
      }
    }
    finally { setBusy(false); }
  }

  function updateEmail(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/email-code/settings", { enabled: emailSettings?.enabled }, tr("メールコード設定を更新できませんでした。パスワードかパスキーが必要な場合があります。", "Could not update email code settings. You may need a password or passkey."), tr("メールコード設定を更新しました。", "Email code settings updated."), async () => {
      await fetchStatus<EmailSettings>("/auth/email-code/settings", setEmailSettings);
    });
  }
  function activateTotp(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/totp/activate", { code: totpCode }, tr("認証アプリを有効にできませんでした。", "Could not enable the authenticator app."), tr("認証アプリを有効にしました。", "Authenticator app enabled."), async () => {
      setTotpSetup(null); setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus); await stepUp.reload();
    });
  }
  function disableTotp(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/totp/disable", { code: totpCode }, tr("認証アプリを無効にできませんでした。別のログイン方法が必要です。", "Could not disable the authenticator app. Another sign-in method is required."), tr("認証アプリを無効にしました。", "Authenticator app disabled."), async () => {
      setTotpCode(""); await fetchStatus<TotpStatus>("/auth/totp/status", setTotpStatus); await stepUp.reload();
    });
  }
  function changePassword(event: FormEvent) {
    event.preventDefault();
    void perform("/auth/password", { current: currentPassword, next: nextPassword }, tr("パスワードを更新できませんでした。現在のパスワードと入力内容を確認してください。", "Could not update your password. Check your current password and input."), tr("パスワードを更新しました。再度ログインしてください。", "Password updated. Please sign in again."), () => {
      setCurrentPassword(""); setNextPassword(""); setPasswordChanged(true);
    });
  }
  function generateRecoveryCodes() {
    if (!stepUp.canEdit || busy) return;
    setShowRecoveryConfirm(false);
    setRecoveryCodes([]);
    void perform("/auth/recovery/generate", {}, tr("復旧コードを発行できませんでした。", "Could not generate recovery codes."), tr("復旧コードを発行しました。この画面を閉じると再表示できません。", "Recovery codes generated. They cannot be shown again after you leave this page."), async (response) => {
      setRecoveryCodes((await json<RecoveryCodes>(response)).codes);
      setRecoveryStatus({ issued: true, remaining: 10 });
    });
  }

  return <AccountLayout page="security">
    {section !== "overview" && <a className="security-back-link" href="/security">← {tr("認証設定に戻る", "Back to security settings")}</a>}
    <h1>{sectionTitle(section)}</h1>
    {section === "overview" && <p className="account-intro">{tr("ログイン方法とアカウントの保護方法を選んで管理できます。", "Manage your sign-in methods and account protection.")}</p>}
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    {section === "overview" && statusLoading && <LoadingStatus>{tr("認証設定を読み込んでいます。", "Loading security settings.")}</LoadingStatus>}
    {section === "overview" && passwordStatus?.has_password === false && <aside className="security-recommendation" aria-labelledby="two-factor-recommendation">
      <h2 id="two-factor-recommendation">{tr("2段階認証を利用するには", "Set up two-factor authentication")}</h2>
      <p>{tr("まずパスワードを設定してください。メールコードや認証アプリを、ログイン時の追加確認に使えるようになります。", "First, set a password. You can then use email codes or an authenticator app for an extra sign-in check.")}</p>
      <a href={sectionURL("password")}>{tr("パスワードを設定する", "Set a password")} →</a>
    </aside>}
    {section === "overview" && passwordStatus?.has_password === true && totpStatus?.enabled === false && <aside className="security-recommendation" aria-labelledby="two-factor-recommendation">
      <h2 id="two-factor-recommendation">{tr("認証アプリを追加しませんか", "Add an authenticator app")}</h2>
      <p>{tr("パスワードでログインするときの追加確認に、認証アプリのコードも利用できます。", "Use an authenticator app code for an extra check when signing in with a password.")}</p>
      <a href={sectionURL("totp")}>{tr("認証アプリを設定する", "Set up an authenticator app")} →</a>
    </aside>}
    {section === "overview" && <div className="security-cards">
      <SecurityCard title={tr("メールコード", "Email codes")} description={tr("メールに届くコードでログインします。", "Sign in using a code sent to your email.")} status={emailSettings ? emailSettings.enabled ? tr("有効", "Enabled") : tr("無効", "Disabled") : statusLoading ? undefined : tr("確認できません", "Unavailable")} href={sectionURL("email")} />
      <SecurityCard title={tr("認証アプリ", "Authenticator app")} description={tr("認証アプリが生成するコードを使用します。", "Use codes generated by an authenticator app.")} status={totpStatus ? totpStatus.enabled ? tr("有効", "Enabled") : tr("無効", "Disabled") : statusLoading ? undefined : tr("確認できません", "Unavailable")} href={sectionURL("totp")} />
      <SecurityCard title={tr("復旧コード", "Recovery codes")} description={tr("ほかの認証方法を利用できないときに使うコードを管理します。", "Manage codes for when other sign-in methods are unavailable.")} status={recoveryStatus ? recoverySummary(recoveryStatus) : statusLoading ? undefined : tr("確認できません", "Unavailable")} href={sectionURL("recovery")} />
      <SecurityCard title={tr("パスワード", "Password")} description={tr("パスワードの設定と変更を行います。", "Set or change your password.")} status={passwordStatus ? passwordStatus.has_password ? tr("設定済み", "Set") : tr("未設定", "Not set") : statusLoading ? undefined : tr("確認できません", "Unavailable")} href={sectionURL("password")} />
      <SecurityCard title={tr("パスキー", "Passkeys")} description={tr("登録済みの端末やセキュリティキーを管理します。", "Manage registered devices and security keys.")} status={passkeyCount !== null ? passkeySummary(passkeyCount) : statusLoading ? undefined : tr("確認できません", "Unavailable")} href={sectionURL("passkeys")} />
    </div>}
    {section !== "overview" && <SecurityStepUp stepUp={stepUp} />}
    {section === "email" && stepUp.canEdit && !emailSettings && !error && <LoadingStatus>{tr("メールコード設定を読み込んでいます。", "Loading email code settings.")}</LoadingStatus>}
    {section === "totp" && stepUp.canEdit && !totpStatus && !error && <LoadingStatus>{tr("認証アプリの設定を読み込んでいます。", "Loading authenticator app settings.")}</LoadingStatus>}
    {section === "password" && stepUp.canEdit && !passwordStatus && !error && <LoadingStatus>{tr("パスワード設定を読み込んでいます。", "Loading password settings.")}</LoadingStatus>}
    {section === "email" && stepUp.canEdit && <section className="security-detail">
      <p>{tr("メールに届くコードをログインに使用します。無効にするには、パスワードかパスキーが必要です。", "Use codes sent to your email to sign in. A password or passkey is required before you can disable this option.")}</p>
      {emailSettings && <form onSubmit={updateEmail}>
        <Switch checked={emailSettings.enabled} onChange={(enabled) => setEmailSettings({ enabled })}>{tr("メールコードを使う", "Use email codes")}</Switch>
        <Button type="submit" variant="Primary" disabled={busy}><BusyLabel busy={busy}>{tr("設定を保存", "Save settings")}</BusyLabel></Button>
      </form>}
    </section>}
    {section === "recovery" && <section className="security-detail">
      {!recoveryStatus && !error && <LoadingStatus>{tr("復旧コードの状態を読み込んでいます。", "Loading recovery code status.")}</LoadingStatus>}
      {recoveryStatus && <>
        <p className="security-detail-status">{recoverySummary(recoveryStatus)}</p>
        <p>{recoveryStatus.issued
          ? tr("再発行すると以前の未使用コードは使えなくなります。新しいコードを安全な場所に保存してください。", "Reissuing codes invalidates all previous unused codes. Save the new codes in a safe place.")
          : tr("ほかの認証方法を利用できない場合に備えて、復旧コードを発行できます。表示されるコードを安全な場所に保存してください。", "Generate recovery codes in case you lose access to your other sign-in methods. Save them in a safe place.")}</p>
        {stepUp.canEdit && <Button type="button" onClick={() => setShowRecoveryConfirm(true)} disabled={busy}>{recoveryStatus.issued ? tr("復旧コードを再発行", "Reissue recovery codes") : tr("復旧コードを発行", "Generate recovery codes")}</Button>}
      </>}
      {recoveryCodes.length > 0 && <pre>{recoveryCodes.join("\n")}</pre>}
    </section>}
    {section === "totp" && stepUp.canEdit && totpStatus && <section className="security-detail">
      <p>{tr("認証アプリは現在", "Authenticator app is currently ")}{totpStatus.enabled ? tr("有効", "enabled") : tr("無効", "disabled")}{tr("です。", ".")}</p>
      {!totpStatus.enabled && <Button type="button" onClick={() => void perform("/auth/totp/setup", {}, tr("設定を開始できませんでした。", "Could not start setup."), tr("認証アプリに秘密鍵を登録してください。", "Add the secret key to your authenticator app."), async (response) => setTotpSetup(await json<TotpSetup>(response)))} disabled={busy}><BusyLabel busy={busy}>{tr("設定を始める", "Start setup")}</BusyLabel></Button>}
      {totpSetup && !totpStatus.enabled && <><p>{tr("認証アプリで次のリンクを開くか、秘密鍵を手動入力してください。", "Open this link in your authenticator app or enter the secret key manually.")}</p><p><a href={totpSetup.otpauth}>{tr("認証アプリで開く", "Open in authenticator app")}</a></p><pre>{totpSetup.secret}</pre><form onSubmit={activateTotp}><TextField id="totp-activate-code" label={tr("認証アプリの6桁コード", "Six-digit authenticator code")} showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" variant="Primary" disabled={busy}><BusyLabel busy={busy}>{tr("有効にする", "Enable")}</BusyLabel></Button></form></>}
      {totpStatus.enabled && <form onSubmit={disableTotp}><TextField id="totp-disable-code" label={tr("現在の6桁コード", "Current six-digit code")} showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={totpCode} onChange={setTotpCode} /><Button type="submit" disabled={busy}><BusyLabel busy={busy}>{tr("無効にする", "Disable")}</BusyLabel></Button></form>}
    </section>}
    {section === "password" && stepUp.canEdit && passwordStatus && <section className="security-detail">
      <p>{passwordStatus.has_password ? tr("現在のパスワードを入力して変更します。", "Enter your current password to change it.") : tr("ログインに使うパスワードを設定します。", "Set a password for signing in.")}</p>
      {passwordChanged ? <a href={securityLoginURL("password")}>{tr("ログインし直す", "Sign in again")}</a> : <form onSubmit={changePassword}>{passwordStatus.has_password && <TextField id="current" label={tr("現在のパスワード", "Current password")} showLabel type="password" autoComplete="current-password" value={currentPassword} onChange={setCurrentPassword} />}<TextField id="next" label={tr("新しいパスワード（8文字以上）", "New password (8 characters or more)")} showLabel type="password" required minLength={8} autoComplete="new-password" value={nextPassword} onChange={setNextPassword} /><Button type="submit" variant="Primary" disabled={busy}><BusyLabel busy={busy}>{tr("更新する", "Update")}</BusyLabel></Button></form>}
    </section>}
    {section === "passkeys" && <PasskeysSection stepUp={stepUp} />}
    {showRecoveryConfirm && <Modal title={recoveryStatus?.issued ? tr("復旧コードを再発行", "Reissue recovery codes") : tr("復旧コードを発行", "Generate recovery codes")} size="S" bottomSheet isOpen onClose={() => setShowRecoveryConfirm(false)} isDismissable closeButtonAriaLabel={tr("閉じる", "Close")}>
      <ModalHeader />
      <ModalBody>
        <ModalAlign><p className="account-dialog-description">{recoveryStatus?.issued
          ? tr("再発行すると、以前の未使用コードは使えなくなります。新しいコードを安全な場所に保存してください。", "Reissuing codes invalidates any previous unused codes. Save the new codes in a safe place.")
          : tr("表示されるコードを安全な場所に保存してください。この画面を閉じると再表示できません。", "Save the codes shown in a safe place. You cannot view them again after closing this page.")}</p></ModalAlign>
        <ModalButtons>
          <Button type="button" variant={recoveryStatus?.issued ? "Danger" : "Primary"} fullWidth disabled={busy} onClick={generateRecoveryCodes}><BusyLabel busy={busy}>{recoveryStatus?.issued ? tr("再発行する", "Reissue") : tr("発行する", "Generate")}</BusyLabel></Button>
          <Button type="button" fullWidth onClick={() => setShowRecoveryConfirm(false)}>{tr("キャンセル", "Cancel")}</Button>
        </ModalButtons>
      </ModalBody>
    </Modal>}
  </AccountLayout>;
}
createRoot(document.getElementById("app")!).render(<LocaleRoot component={SecurityApp} />);
