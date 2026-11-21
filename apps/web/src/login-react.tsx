import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, DropdownMenuItem, DropdownSelector, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { AuthHeader } from "./auth-header";
import { AuthFooter } from "./auth-footer";
import { request, json } from "./api";
import { apiBase } from "./config";
import { decodeBase64url, encodeBase64url } from "./dom";
import { LocaleRoot, tr, useLocale } from "./i18n";
import { BusyLabel, LoadingStatus } from "./loading";
import { clearFlow, loadFlow, normalizeLoginEmail, saveFlow, schoolEmailDomain, securityDetailSection, securityDetailURL, securityLoginURL, startFlow, type LoginFlow, type SecurityDetailSection } from "./login-flow";
import type { StepUpMethod, StepUpStatus } from "./security-step-up";
import { isIOSGoogleApp, passkeySupport, type PasskeySupport } from "./passkey-support";
import { PasskeyBrowserHelp } from "./passkey-browser-help";
import { reportAuthError } from "./telemetry";

type Step = "email" | "password" | "mail-code" | "mfa";
interface LoginPayload { mfa_required?: boolean; auth_challenge?: string }
interface PasskeyBeginPayload { request_id: string; options: { publicKey: PublicKeyCredentialRequestOptionsJSON } }
type MfaMethod = "totp" | "code" | "email" | "recovery";

function mfaMethodLabel(method: MfaMethod): string {
  return method === "totp" ? tr("認証アプリ", "Authenticator app")
    : method === "recovery" ? tr("復旧コード", "Recovery code") : tr("メールコード", "Email code");
}

function MfaMethodSelector({ method, methods, onChange }: { method: MfaMethod; methods: MfaMethod[]; onChange: (value: MfaMethod) => void }) {
  if (methods.length < 2) return null;
  return <DropdownSelector className="mfa-method-selector" label={tr("認証方法", "Authentication method")} showLabel name="method" value={method} assistiveText={tr(`選択中: ${mfaMethodLabel(method)}`, `Selected: ${mfaMethodLabel(method)}`)} onChange={(value) => {
    if (methods.includes(value as MfaMethod)) onChange(value as MfaMethod);
  }}>{methods.map((option) => <DropdownMenuItem key={option} value={option}>{mfaMethodLabel(option)}</DropdownMenuItem>)}</DropdownSelector>;
}

function MfaCodeField({ method, value, onChange }: { method: MfaMethod; value: string; onChange: (value: string) => void }) {
  const label = method === "totp" ? tr("認証アプリの6桁コード", "Six-digit authenticator code")
    : method === "recovery" ? tr("復旧コード", "Recovery code") : tr("メールコード", "Email code");
  return <TextField id="factor" label={label} showLabel required inputMode={method === "recovery" ? "text" : "numeric"} pattern={method === "recovery" ? undefined : "[0-9]{6}"} autoComplete={method === "recovery" ? "off" : "one-time-code"} value={value} onChange={onChange} autoFocus />;
}

function MfaFeedback({ error, notice }: { error: string; notice: string }) {
  return <>{error && <p id="error" className="message error" role="alert">{error}</p>}{notice && <p className="message" role="status">{notice}</p>}</>;
}

const step = document.body.dataset.step as Step;
const securityMode = step === "mfa" && new URLSearchParams(location.search).get("purpose") === "security";
const flow = securityMode ? null : step === "email" ? startFlow(location.search, sessionStorage) : loadFlow(sessionStorage);

function LoginApp() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [factor, setFactor] = useState("");
  const [method, setMethod] = useState<"totp" | "code" | "recovery">("totp");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busyAction, setBusyAction] = useState<"submit" | "send-code" | "passkey" | null>(null);
  const busy = busyAction !== null;
  const [restart, setRestart] = useState(false);
  const [passkeyAvailability, setPasskeyAvailability] = useState<PasskeySupport | "checking">("checking");
  const [passkeyBrowserHelp, setPasskeyBrowserHelp] = useState(false);
  const showPasskeyBrowserHelp = passkeyAvailability === "ios-google-app" || passkeyBrowserHelp;
  const invalid = !flow || (step !== "email" && !flow.email) || (step === "mfa" && !flow.challenge);

  useEffect(() => {
    if (step !== "password") return;
    void passkeySupport(navigator.userAgent, !!navigator.credentials?.get, window.PublicKeyCredential).then((availability) => {
      setPasskeyAvailability(availability);
      if (availability !== "available") reportAuthError("passkey.support", { name: "Unavailable" });
    });
  }, []);

  function afterLogin(current: LoginFlow) {
    const target = current.next === "security" ? securityDetailURL(current.nextSection || null)
      : current.next ? `/${current.next}` : `${apiBase}/oauth/authorize?request_id=${encodeURIComponent(current.requestID)}`;
    clearFlow(sessionStorage);
    location.assign(target);
  }

  async function signIn(fields: { password?: string; code?: string; totp?: string; recovery_code?: string }) {
    if (!flow) return;
    const response = await request("/auth/login", {
      email: flow.email, password: fields.password ?? "", code: fields.code ?? "",
      totp: fields.totp ?? "", recovery_code: fields.recovery_code ?? "", auth_challenge: flow.challenge,
    });
    const payload = await json<LoginPayload>(response).catch((): LoginPayload => ({}));
    if (response.status === 202 && payload.mfa_required && payload.auth_challenge) {
      flow.challenge = payload.auth_challenge;
      saveFlow(flow, sessionStorage);
      location.assign("/mfa");
      return;
    }
    if (!response.ok || response.status === 202) {
      if (flow.challenge) {
        flow.challenge = "";
        saveFlow(flow, sessionStorage);
        setRestart(true);
        setError(tr("認証できませんでした。パスワードからやり直してください。", "Authentication failed. Please start again with your password."));
      } else setError(tr("認証できませんでした。入力内容を確認してください。", "Authentication failed. Check your details and try again."));
      return;
    }
    afterLogin(flow);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!flow || busy) return;
    setError(""); setNotice(""); setBusyAction("submit");
    try {
      if (step === "email") {
        const submittedEmail = new FormData(event.currentTarget).get("email");
        const normalizedEmail = normalizeLoginEmail(typeof submittedEmail === "string" ? submittedEmail : email);
        if (!normalizedEmail) {
          setError(tr("メールアドレスの形式を確認してください。", "Enter a valid email address."));
          return;
        }
        flow.email = normalizedEmail;
        flow.challenge = "";
        saveFlow(flow, sessionStorage);
        location.assign("/password");
      } else if (step === "password") await signIn({ password });
      else if (step === "mail-code") await signIn({ code: factor });
      else await signIn({ [method === "recovery" ? "recovery_code" : method]: factor });
    } catch (cause) { reportAuthError("login.submit", cause); setError(tr("通信に失敗しました。", "The request failed. Please try again.")); }
    finally { setBusyAction(null); }
  }

  async function sendCode(destination?: string) {
    if (!flow || busy) return;
    setError(""); setNotice(""); setBusyAction("send-code");
    try {
      const response = await request("/auth/request-code", { email: flow.email });
      if (!response.ok) throw response;
      if (destination) location.assign(destination);
      else setNotice(tr("メールコードを送信しました。", "Email code sent."));
    } catch (cause) { reportAuthError("email-code.send", cause, cause instanceof Response ? { status: cause.status, endpoint: "/auth/request-code" } : {}); setError(tr("コードを送信できませんでした。", "Could not send the code.")); }
    finally { setBusyAction(null); }
  }

  async function passkeyLogin() {
    if (!flow || busy || passkeyAvailability !== "available") return;
    setError("");
    if (!navigator.credentials?.get) { setError(tr("このブラウザはパスキーに対応していません。", "This browser does not support passkeys.")); return; }
    setBusyAction("passkey");
    let stage: "passkey.begin" | "passkey.credential" | "passkey.finish" = "passkey.begin";
    try {
      const beginResponse = await request("/auth/passkey/begin", { email: flow.email });
      if (!beginResponse.ok) throw beginResponse;
      const begin = await json<PasskeyBeginPayload>(beginResponse);
      const source = begin.options.publicKey;
      const publicKey = {
        ...source, challenge: decodeBase64url(source.challenge),
        allowCredentials: source.allowCredentials?.map((item) => ({
          id: decodeBase64url(item.id), type: "public-key",
          transports: item.transports as AuthenticatorTransport[] | undefined,
        })),
      } as unknown as PublicKeyCredentialRequestOptions;
      let credential: PublicKeyCredential | null;
      stage = "passkey.credential";
      try {
        credential = await navigator.credentials.get({ publicKey }) as PublicKeyCredential | null;
      } catch (cause) {
        // Some embedded browsers report availability but reject the ceremony.
        if (isIOSGoogleApp(navigator.userAgent)) setPasskeyBrowserHelp(true);
        throw cause;
      }
      if (!credential) throw new Error("no credential");
      const assertion = credential.response as AuthenticatorAssertionResponse;
      stage = "passkey.finish";
      const finishResponse = await request("/auth/passkey/finish", {
        request_id: begin.request_id,
        credential: {
          id: credential.id, rawId: encodeBase64url(credential.rawId), type: credential.type,
          response: {
            clientDataJSON: encodeBase64url(assertion.clientDataJSON),
            authenticatorData: encodeBase64url(assertion.authenticatorData),
            signature: encodeBase64url(assertion.signature),
            userHandle: assertion.userHandle ? encodeBase64url(assertion.userHandle) : null,
          },
          clientExtensionResults: credential.getClientExtensionResults(),
        },
      });
      if (!finishResponse.ok) throw finishResponse;
      afterLogin(flow);
    } catch (cause) {
      reportAuthError(stage, cause, cause instanceof Response ? { status: cause.status, endpoint: stage === "passkey.begin" ? "/auth/passkey/begin" : "/auth/passkey/finish" } : {});
      setError(tr("パスキーで認証できませんでした。別の方法をお試しください。", "Could not sign in with your passkey. Please try another method."));
    }
    finally { setBusyAction(null); }
  }

  const title = { email: tr("メールアドレスを入力", "Enter your email address"), password: tr("パスワードを入力", "Enter your password"), "mail-code": tr("メールコードを入力", "Enter your email code"), mfa: tr("2段階認証", "Two-factor authentication") }[step];
  const progress = { email: tr("ログイン · 1 / 3", "Sign in · 1 / 3"), password: tr("ログイン · 2 / 3", "Sign in · 2 / 3"), "mail-code": tr("ログイン · メールコード", "Sign in · Email code"), mfa: tr("ログイン · 3 / 3", "Sign in · 3 / 3") }[step];
  const displayedError = invalid ? (step === "email" ? tr("Taskly からログインを開始してください。", "Start signing in from Taskly.") : tr("ログインを最初からやり直してください。", "Please start signing in again.")) : error;
  useEffect(() => { document.title = `${title} | ${tr("Taskly 認証", "Taskly Authentication")}`; }, [title]);
  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">{progress}</p><h1>{title}</h1>
    {step === "email" && <p>{tr("Taskly のアカウントで続行します。", "Continue with your Taskly account.")}</p>}
    {step === "password" && <><p id="email-display" className="account-email">{flow?.email}</p><a className="back-link" href="/login">{tr("メールアドレスを変更", "Change email address")}</a></>}
    {step === "mail-code" && <p>{tr("送信した6桁のコードを入力してください。", "Enter the six-digit code we sent.")}</p>}
    {step === "mfa" && <p>{tr("設定済みの方法で本人確認を行います。", "Verify your identity using a method you have set up.")}</p>}
    <MfaFeedback error={displayedError} notice={notice} />
    {restart && <a id="restart" href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a>}
    {!invalid && !restart && <form id="form" onSubmit={submit} aria-busy={busy}>
      {step === "email" && <TextField id="email" name="email" inputMode="email" autoCapitalize="none" spellCheck={false} label={tr("メールアドレス", "Email address")} assistiveText={tr(`@${schoolEmailDomain} のアドレスは、@の前だけで入力できます。`, `For @${schoolEmailDomain} addresses, you can enter just the part before @.`)} invalid={Boolean(error)} showLabel required autoComplete="username" value={email} onChange={(value) => { setEmail(value); setError(""); }} autoFocus />}
      {step === "password" && <TextField id="password" type="password" label={tr("パスワード", "Password")} showLabel required autoComplete="current-password" value={password} onChange={setPassword} autoFocus />}
      {step === "mail-code" && <TextField id="code" label={tr("メールコード", "Email code")} showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={factor} onChange={setFactor} autoFocus />}
      {step === "mfa" && <><MfaMethodSelector method={method} methods={["totp", "code", "recovery"]} onChange={(value) => { setMethod(value as typeof method); setFactor(""); }} />
        <MfaCodeField method={method} value={factor} onChange={setFactor} />
        {method === "code" && <Button id="mfa-send-code" type="button" onClick={() => void sendCode()} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("メールコードを送信", "Send email code")}</BusyLabel></Button>}</>}
      <div className="actions"><Button id="submit" type="submit" variant="Primary" fullWidth disabled={busy}><BusyLabel busy={busyAction === "submit"}>{step === "email" ? tr("次へ", "Next") : step === "mfa" ? tr("認証してログイン", "Verify and sign in") : tr("ログイン", "Sign in")}</BusyLabel></Button></div>
    </form>}
    {!invalid && step === "password" && <div className="alternative-actions">
      <p>{tr("別の方法でログイン", "Sign in another way")}</p>
      <Button id="send-code" type="button" fullWidth onClick={() => void sendCode("/mail-code")} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("メールコードを送信", "Send email code")}</BusyLabel></Button>
      <Button id="passkey" type="button" fullWidth onClick={() => void passkeyLogin()} disabled={busy || passkeyAvailability !== "available"} aria-describedby={showPasskeyBrowserHelp ? "passkey-browser-help" : passkeyAvailability === "unavailable" ? "passkey-unavailable" : undefined}><BusyLabel busy={busyAction === "passkey"}>{tr("パスキーを使う", "Use a passkey")}</BusyLabel></Button>
      {showPasskeyBrowserHelp && <PasskeyBrowserHelp />}
      {passkeyAvailability === "unavailable" && <p id="passkey-unavailable">{tr("このブラウザはパスキーに対応していません。別の方法でログインしてください。", "This browser does not support passkeys. Sign in using another method.")}</p>}
    </div>}
    {!invalid && step === "mail-code" && <div className="alternative-actions"><Button id="resend-code" type="button" fullWidth onClick={() => void sendCode()} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("コードを再送信", "Resend code")}</BusyLabel></Button><a href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a></div>}
    {!invalid && step === "mfa" && <p className="footer-note"><a href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a></p>}
    <AuthFooter />
  </main></CharcoalProvider>;
}

const securitySection: SecurityDetailSection | null = securityDetailSection(new URLSearchParams(location.search).get("section"));

function SecurityMfaApp() {
  const locale = useLocale();
  const [status, setStatus] = useState<StepUpStatus | null>(null);
  const [method, setMethod] = useState<StepUpMethod>("email");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busyAction, setBusyAction] = useState<"send" | "verify" | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const busy = busyAction !== null;

  useEffect(() => {
    document.title = `${tr("2段階認証", "Two-factor authentication")} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [locale]);

  useEffect(() => {
    if (!securitySection) { location.replace("/security"); return; }
    async function loadStatus() {
      try {
        const response = await request("/auth/security/step-up/status");
        if (response.status === 401) { location.assign(securityLoginURL(securitySection)); return; }
        if (!response.ok) throw new Error("status unavailable");
        const value = await json<StepUpStatus>(response);
        if (!value.required || value.verified) { location.replace(securityDetailURL(securitySection)); return; }
        const methods = value.methods.filter((candidate) => candidate === "email" || candidate === "totp" || candidate === "recovery");
        if (methods.length === 0) throw new Error("no verification method");
        setStatus({ ...value, methods });
        setMethod(methods[0]!);
      } catch {
        setError(tr("追加認証の状態を確認できませんでした。ページを再読み込みしてください。", "Could not check your verification status. Please reload the page."));
      } finally { setLoading(false); }
    }
    void loadStatus();
  }, []);

  async function sendCode() {
    if (!securitySection || !status?.methods.includes("email") || busy) return;
    setBusyAction("send"); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/request-code", {});
      if (response.status === 401) {
        const payload = await json<{ error?: string }>(response).catch((): { error?: string } => ({}));
        if (payload.error === "login_required") { location.assign(securityLoginURL(securitySection)); return; }
      }
      if (!response.ok) throw new Error(tr("確認コードを送信できませんでした。しばらくしてからお試しください。", "Could not send a verification code. Please try again later."));
      setSent(true);
      setNotice(tr("登録メールアドレスへ確認コードを送信しました。", "A verification code was sent to your registered email address."));
    } catch (cause) {
      setError(cause instanceof Error && !(cause instanceof TypeError) ? cause.message : tr("確認コードを送信できませんでした。", "Could not send a verification code."));
    } finally { setBusyAction(null); }
  }

  async function verify(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!securitySection || !status?.methods.includes(method) || busy) return;
    setBusyAction("verify"); setError(""); setNotice("");
    try {
      const response = await request("/auth/security/step-up/verify", { method, code });
      if (!response.ok) {
        const payload = await json<{ error?: string }>(response).catch((): { error?: string } => ({}));
        if (response.status === 401 && payload.error === "login_required") { location.assign(securityLoginURL(securitySection)); return; }
        throw new Error(payload.error === "rate_limited"
          ? tr("試行回数の上限です。時間をおいてください。", "Too many attempts. Please try again later.")
          : tr("コードを確認できませんでした。入力内容と有効期限を確認してください。", "Could not verify the code. Check the code and its expiration time."));
      }
      location.replace(securityDetailURL(securitySection));
    } catch (cause) {
      setError(cause instanceof Error && !(cause instanceof TypeError) ? cause.message : tr("本人確認を完了できませんでした。", "Could not complete verification."));
    } finally { setBusyAction(null); }
  }

  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">{tr("認証設定", "Security settings")}</p>
    <h1>{tr("2段階認証", "Two-factor authentication")}</h1>
    <p>{tr("認証設定を変更する前に本人確認をしてください。確認後5分間、設定を変更できます。", "Verify your identity before changing authentication settings. You can make changes for five minutes afterward.")}</p>
    {loading && !error && <LoadingStatus>{tr("追加認証の状態を読み込んでいます。", "Loading verification status.")}</LoadingStatus>}
    <MfaFeedback error={error} notice={notice} />
    {status && <>
      <MfaMethodSelector method={method} methods={status.methods} onChange={(value) => { setMethod(value as StepUpMethod); setCode(""); }} />
      {method === "email" && <div className="actions"><Button type="button" fullWidth onClick={() => void sendCode()} disabled={busy}><BusyLabel busy={busyAction === "send"}>{sent ? tr("コードを再送信", "Resend code") : tr("登録メールへコードを送信", "Send code to registered email")}</BusyLabel></Button></div>}
      {(method !== "email" || sent) && <form id="form" onSubmit={(event) => void verify(event)} aria-busy={busy}>
        <MfaCodeField method={method} value={code} onChange={setCode} />
        <div className="actions"><Button id="submit" type="submit" variant="Primary" fullWidth disabled={busy}><BusyLabel busy={busyAction === "verify"}>{tr("確認する", "Verify")}</BusyLabel></Button></div>
      </form>}
    </>}
    <p className="footer-note"><a href="/security">{tr("認証設定に戻る", "Back to security settings")}</a></p>
    <AuthFooter />
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={securityMode ? SecurityMfaApp : LoginApp} />);
