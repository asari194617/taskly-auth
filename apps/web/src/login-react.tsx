import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, DropdownMenuItem, DropdownSelector, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useState, type FormEvent } from "react";
import { AuthHeader } from "./auth-header";
import { request, json } from "./api";
import { apiBase, tasklyLoginURL } from "./config";
import { decodeBase64url, encodeBase64url } from "./dom";
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
  const [busy, setBusy] = useState(false);
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
        setError("認証できませんでした。パスワードからやり直してください。");
      } else setError("認証できませんでした。入力内容を確認してください。");
      return;
    }
    afterLogin(flow);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!flow || busy) return;
    setError(""); setNotice(""); setBusy(true);
    try {
      if (step === "email") {
        flow.email = email.trim().toLowerCase();
        flow.challenge = "";
        saveFlow(flow, sessionStorage);
        location.assign("/password");
      } else if (step === "password") await signIn({ password });
      else if (step === "mail-code") await signIn({ code: factor });
      else await signIn({ [method === "recovery" ? "recovery_code" : method]: factor });
    } catch { setError("通信に失敗しました。"); }
    finally { setBusy(false); }
  }

  async function sendCode(destination?: string) {
    if (!flow || busy) return;
    setError(""); setNotice(""); setBusy(true);
    try {
      const response = await request("/auth/request-code", { email: flow.email });
      if (!response.ok) throw new Error("request failed");
      if (destination) location.assign(destination);
      else setNotice("メールコードを送信しました。");
    } catch { setError("コードを送信できませんでした。"); }
    finally { setBusy(false); }
  }

  async function passkeyLogin() {
    if (!flow || busy) return;
    setError("");
    if (!navigator.credentials?.get) { setError("このブラウザはパスキーに対応していません。"); return; }
    setBusy(true);
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
    } catch { setError("パスキーで認証できませんでした。別の方法をお試しください。"); }
    finally { setBusy(false); }
  }

  const title = { email: "メールアドレスを入力", password: "パスワードを入力", "mail-code": "メールコードを入力", mfa: "2段階認証" }[step];
  const progress = { email: "ログイン · 1 / 3", password: "ログイン · 2 / 3", "mail-code": "ログイン · メールコード", mfa: "ログイン · 3 / 3" }[step];
  const methodName = { totp: "認証アプリ", code: "メールコード", recovery: "復旧コード" }[method];
  const displayedError = invalid ? (step === "email" ? "Taskly からログインを開始してください。" : "ログインを最初からやり直してください。") : error;
  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">{progress}</p><h1>{title}</h1>
    {step === "email" && <p>Taskly のアカウントで続行します。</p>}
    {step === "password" && <><p id="email-display" className="account-email">{flow?.email}</p><a className="back-link" href="/login">メールアドレスを変更</a></>}
    {step === "mail-code" && <p>送信した6桁のコードを入力してください。</p>}
    {step === "mfa" && <p>設定済みの方法で本人確認を行います。</p>}
    {displayedError && <p id="error" className="message error" role="alert">{displayedError}</p>}
    {notice && <p className="message" role="status">{notice}</p>}
    {restart && <a id="restart" href="/password">パスワード入力に戻る</a>}
    {!invalid && !restart && <form id="form" onSubmit={submit}>
      {step === "email" && <TextField id="email" type="email" label="メールアドレス" showLabel required autoComplete="username" value={email} onChange={setEmail} autoFocus />}
      {step === "password" && <TextField id="password" type="password" label="パスワード" showLabel required autoComplete="current-password" value={password} onChange={setPassword} autoFocus />}
      {step === "mail-code" && <TextField id="code" label="メールコード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={factor} onChange={setFactor} autoFocus />}
      {step === "mfa" && <><DropdownSelector className="mfa-method-selector" label="認証方法" showLabel name="method" value={method} assistiveText={`選択中: ${methodName}`} onChange={(value) => { setMethod(value as typeof method); setFactor(""); }}><DropdownMenuItem value="totp">認証アプリ</DropdownMenuItem><DropdownMenuItem value="code">メールコード</DropdownMenuItem><DropdownMenuItem value="recovery">復旧コード</DropdownMenuItem></DropdownSelector>
        <TextField id="factor" label={{ totp: "認証アプリのコード", code: "メールコード", recovery: "復旧コード" }[method]} showLabel required inputMode={method === "recovery" ? "text" : "numeric"} autoComplete={method === "recovery" ? "off" : "one-time-code"} value={factor} onChange={setFactor} autoFocus />
        {method === "code" && <Button id="mfa-send-code" type="button" onClick={() => void sendCode()} disabled={busy}>メールコードを送信</Button>}</>}
      <div className="actions"><Button id="submit" type="submit" variant="Primary" fullWidth disabled={busy}>{step === "email" ? "次へ" : step === "mfa" ? "認証してログイン" : "ログイン"}</Button></div>
    </form>}
    {!invalid && step === "password" && <div className="alternative-actions"><p>別の方法でログイン</p><Button id="send-code" type="button" fullWidth onClick={() => void sendCode("/mail-code")} disabled={busy}>メールコードを送信</Button><Button id="passkey" type="button" fullWidth onClick={() => void passkeyLogin()} disabled={busy}>パスキーを使う</Button></div>}
    {!invalid && step === "mail-code" && <div className="alternative-actions"><Button id="resend-code" type="button" fullWidth onClick={() => void sendCode()} disabled={busy}>コードを再送信</Button><a href="/password">パスワード入力に戻る</a></div>}
    {!invalid && step === "mfa" && <p className="footer-note"><a href="/password">パスワード入力に戻る</a></p>}
    {step === "email" && <p className="footer-note">認証できない場合は <a href={tasklyLoginURL}>従来のログイン画面</a>を利用できます。</p>}
    {step === "password" && <p className="footer-note"><a href={tasklyLoginURL}>従来のログイン画面</a></p>}
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LoginApp />);
