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
import { LocaleRoot, tr } from "./i18n";
import { BusyLabel } from "./loading";
import { clearFlow, loadFlow, saveFlow, startFlow, type LoginFlow } from "./login-flow";

type Step = "email" | "password" | "mail-code" | "mfa";
interface LoginPayload { mfa_required?: boolean; auth_challenge?: string }
interface PasskeyBeginPayload { request_id: string; options: { publicKey: PublicKeyCredentialRequestOptionsJSON } }

const step = document.body.dataset.step as Step;
const flow = step === "email" ? startFlow(location.search, sessionStorage) : loadFlow(sessionStorage);

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
  const invalid = !flow || (step !== "email" && !flow.email) || (step === "mfa" && !flow.challenge);

  function afterLogin(current: LoginFlow) {
    const target = current.next ? `/${current.next}` : `${apiBase}/oauth/authorize?request_id=${encodeURIComponent(current.requestID)}`;
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
        flow.email = email.trim().toLowerCase();
        flow.challenge = "";
        saveFlow(flow, sessionStorage);
        location.assign("/password");
      } else if (step === "password") await signIn({ password });
      else if (step === "mail-code") await signIn({ code: factor });
      else await signIn({ [method === "recovery" ? "recovery_code" : method]: factor });
    } catch { setError(tr("通信に失敗しました。", "The request failed. Please try again.")); }
    finally { setBusyAction(null); }
  }

  async function sendCode(destination?: string) {
    if (!flow || busy) return;
    setError(""); setNotice(""); setBusyAction("send-code");
    try {
      const response = await request("/auth/request-code", { email: flow.email });
      if (!response.ok) throw new Error("request failed");
      if (destination) location.assign(destination);
      else setNotice(tr("メールコードを送信しました。", "Email code sent."));
    } catch { setError(tr("コードを送信できませんでした。", "Could not send the code.")); }
    finally { setBusyAction(null); }
  }

  async function passkeyLogin() {
    if (!flow || busy) return;
    setError("");
    if (!navigator.credentials?.get) { setError(tr("このブラウザはパスキーに対応していません。", "This browser does not support passkeys.")); return; }
    setBusyAction("passkey");
    try {
      const beginResponse = await request("/auth/passkey/begin", { email: flow.email });
      if (!beginResponse.ok) throw new Error("begin failed");
      const begin = await json<PasskeyBeginPayload>(beginResponse);
      const source = begin.options.publicKey;
      const publicKey = {
        ...source, challenge: decodeBase64url(source.challenge),
        allowCredentials: source.allowCredentials?.map((item) => ({
          id: decodeBase64url(item.id), type: "public-key",
          transports: item.transports as AuthenticatorTransport[] | undefined,
        })),
      } as unknown as PublicKeyCredentialRequestOptions;
      const credential = await navigator.credentials.get({ publicKey }) as PublicKeyCredential | null;
      if (!credential) throw new Error("no credential");
      const assertion = credential.response as AuthenticatorAssertionResponse;
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
      if (!finishResponse.ok) throw new Error("finish failed");
      afterLogin(flow);
    } catch { setError(tr("パスキーで認証できませんでした。別の方法をお試しください。", "Could not sign in with your passkey. Please try another method.")); }
    finally { setBusyAction(null); }
  }

  const title = { email: tr("メールアドレスを入力", "Enter your email address"), password: tr("パスワードを入力", "Enter your password"), "mail-code": tr("メールコードを入力", "Enter your email code"), mfa: tr("2段階認証", "Two-factor authentication") }[step];
  const progress = { email: tr("ログイン · 1 / 3", "Sign in · 1 / 3"), password: tr("ログイン · 2 / 3", "Sign in · 2 / 3"), "mail-code": tr("ログイン · メールコード", "Sign in · Email code"), mfa: tr("ログイン · 3 / 3", "Sign in · 3 / 3") }[step];
  const methodName = { totp: tr("認証アプリ", "Authenticator app"), code: tr("メールコード", "Email code"), recovery: tr("復旧コード", "Recovery code") }[method];
  const displayedError = invalid ? (step === "email" ? tr("Taskly からログインを開始してください。", "Start signing in from Taskly.") : tr("ログインを最初からやり直してください。", "Please start signing in again.")) : error;
  useEffect(() => { document.title = `${title} | ${tr("Taskly 認証", "Taskly Authentication")}`; }, [title]);
  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">{progress}</p><h1>{title}</h1>
    {step === "email" && <p>{tr("Taskly のアカウントで続行します。", "Continue with your Taskly account.")}</p>}
    {step === "password" && <><p id="email-display" className="account-email">{flow?.email}</p><a className="back-link" href="/login">{tr("メールアドレスを変更", "Change email address")}</a></>}
    {step === "mail-code" && <p>{tr("送信した6桁のコードを入力してください。", "Enter the six-digit code we sent.")}</p>}
    {step === "mfa" && <p>{tr("設定済みの方法で本人確認を行います。", "Verify your identity using a method you have set up.")}</p>}
    {displayedError && <p id="error" className="message error" role="alert">{displayedError}</p>}
    {notice && <p className="message" role="status">{notice}</p>}
    {restart && <a id="restart" href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a>}
    {!invalid && !restart && <form id="form" onSubmit={submit} aria-busy={busy}>
      {step === "email" && <TextField id="email" type="email" label={tr("メールアドレス", "Email address")} showLabel required autoComplete="username" value={email} onChange={setEmail} autoFocus />}
      {step === "password" && <TextField id="password" type="password" label={tr("パスワード", "Password")} showLabel required autoComplete="current-password" value={password} onChange={setPassword} autoFocus />}
      {step === "mail-code" && <TextField id="code" label={tr("メールコード", "Email code")} showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={factor} onChange={setFactor} autoFocus />}
      {step === "mfa" && <><DropdownSelector className="mfa-method-selector" label={tr("認証方法", "Authentication method")} showLabel name="method" value={method} assistiveText={tr(`選択中: ${methodName}`, `Selected: ${methodName}`)} onChange={(value) => { setMethod(value as typeof method); setFactor(""); }}><DropdownMenuItem value="totp">{tr("認証アプリ", "Authenticator app")}</DropdownMenuItem><DropdownMenuItem value="code">{tr("メールコード", "Email code")}</DropdownMenuItem><DropdownMenuItem value="recovery">{tr("復旧コード", "Recovery code")}</DropdownMenuItem></DropdownSelector>
        <TextField id="factor" label={{ totp: tr("認証アプリのコード", "Authenticator code"), code: tr("メールコード", "Email code"), recovery: tr("復旧コード", "Recovery code") }[method]} showLabel required inputMode={method === "recovery" ? "text" : "numeric"} autoComplete={method === "recovery" ? "off" : "one-time-code"} value={factor} onChange={setFactor} autoFocus />
        {method === "code" && <Button id="mfa-send-code" type="button" onClick={() => void sendCode()} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("メールコードを送信", "Send email code")}</BusyLabel></Button>}</>}
      <div className="actions"><Button id="submit" type="submit" variant="Primary" fullWidth disabled={busy}><BusyLabel busy={busyAction === "submit"}>{step === "email" ? tr("次へ", "Next") : step === "mfa" ? tr("認証してログイン", "Verify and sign in") : tr("ログイン", "Sign in")}</BusyLabel></Button></div>
    </form>}
    {!invalid && step === "password" && <div className="alternative-actions"><p>{tr("別の方法でログイン", "Sign in another way")}</p><Button id="send-code" type="button" fullWidth onClick={() => void sendCode("/mail-code")} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("メールコードを送信", "Send email code")}</BusyLabel></Button><Button id="passkey" type="button" fullWidth onClick={() => void passkeyLogin()} disabled={busy}><BusyLabel busy={busyAction === "passkey"}>{tr("パスキーを使う", "Use a passkey")}</BusyLabel></Button></div>}
    {!invalid && step === "mail-code" && <div className="alternative-actions"><Button id="resend-code" type="button" fullWidth onClick={() => void sendCode()} disabled={busy}><BusyLabel busy={busyAction === "send-code"}>{tr("コードを再送信", "Resend code")}</BusyLabel></Button><a href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a></div>}
    {!invalid && step === "mfa" && <p className="footer-note"><a href="/password">{tr("パスワード入力に戻る", "Return to password entry")}</a></p>}
    <AuthFooter />
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={LoginApp} />);
