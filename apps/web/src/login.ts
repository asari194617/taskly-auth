import "./style.css";
import "./theme";
import { request, json } from "./api";
import { apiBase, configureTasklyLinks } from "./config";
import { decodeBase64url, element, encodeBase64url } from "./dom";
import { clearFlow, loadFlow, saveFlow, startFlow, type LoginFlow } from "./login-flow";

interface LoginPayload { mfa_required?: boolean; auth_challenge?: string }
interface PasskeyBeginPayload {
  request_id: string;
  options: { publicKey: PublicKeyCredentialRequestOptionsJSON };
}

const step = document.body.dataset.step;
const error = element("error", HTMLParagraphElement);
const form = element("form", HTMLFormElement);
const flow = step === "email" ? startFlow(location.search, sessionStorage) : loadFlow(sessionStorage);
configureTasklyLinks();

if (!flow || (step !== "email" && !flow.email) || (step === "mfa" && !flow.challenge)) {
  error.textContent = step === "email" ? "Taskly からログインを開始してください。" : "ログインを最初からやり直してください。";
  form.hidden = true;
}

function requireFlow(): LoginFlow {
  if (!flow) throw new Error("missing login flow");
  return flow;
}

function afterLogin(): void {
  const current = requireFlow();
  const target = current.next ? `/${current.next}` : `${apiBase}/oauth/authorize?request_id=${encodeURIComponent(current.requestID)}`;
  clearFlow(sessionStorage);
  location.assign(target);
}

async function signIn(fields: { password?: string; code?: string; totp?: string; recovery_code?: string }): Promise<void> {
  const current = requireFlow();
  const response = await request("/auth/login", {
    email: current.email,
    password: fields.password ?? "",
    code: fields.code ?? "",
    totp: fields.totp ?? "",
    recovery_code: fields.recovery_code ?? "",
    auth_challenge: current.challenge,
  });
  const payload = await json<LoginPayload>(response).catch((): LoginPayload => ({}));
  if (response.status === 202 && payload.mfa_required && payload.auth_challenge) {
    current.challenge = payload.auth_challenge;
    saveFlow(current, sessionStorage);
    location.assign("/mfa");
    return;
  }
  if (!response.ok || response.status === 202) {
    if (current.challenge) {
      // The API consumes a challenge even when the second factor is wrong.
      current.challenge = "";
      saveFlow(current, sessionStorage);
      error.textContent = "認証できませんでした。パスワードからやり直してください。";
      form.hidden = true;
      element("restart", HTMLAnchorElement).hidden = false;
    } else {
      error.textContent = "認証できませんでした。入力内容を確認してください。";
    }
    return;
  }
  afterLogin();
}

function bindSubmit(handler: () => Promise<void>): void {
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.textContent = "";
    const button = element("submit", HTMLButtonElement);
    button.disabled = true;
    try { await handler(); }
    catch { error.textContent = "通信に失敗しました。"; }
    finally { button.disabled = false; }
  });
}

if (step === "email") {
  const email = element("email", HTMLInputElement);
  bindSubmit(async () => {
    const current = requireFlow();
    current.email = email.value.trim().toLowerCase();
    current.challenge = "";
    saveFlow(current, sessionStorage);
    location.assign("/password");
  });
}

if (step === "password") {
  const current = flow;
  element("email-display", HTMLElement).textContent = current?.email ?? "";
  bindSubmit(() => signIn({ password: element("password", HTMLInputElement).value }));
  element("send-code", HTMLButtonElement).addEventListener("click", async () => {
    error.textContent = "";
    const button = element("send-code", HTMLButtonElement);
    button.disabled = true;
    try {
      const response = await request("/auth/request-code", { email: requireFlow().email });
      if (!response.ok) throw new Error("request failed");
      location.assign("/mail-code");
    } catch { error.textContent = "コードを送信できませんでした。"; }
    finally { button.disabled = false; }
  });
  element("passkey", HTMLButtonElement).addEventListener("click", async () => {
    error.textContent = "";
    const button = element("passkey", HTMLButtonElement);
    if (!navigator.credentials?.get) {
      error.textContent = "このブラウザはパスキーに対応していません。";
      return;
    }
    button.disabled = true;
    try {
      const beginResponse = await request("/auth/passkey/begin", { email: requireFlow().email });
      if (!beginResponse.ok) throw new Error("begin failed");
      const begin = await json<PasskeyBeginPayload>(beginResponse);
      const source = begin.options.publicKey;
      const publicKey = {
        ...source,
        challenge: decodeBase64url(source.challenge),
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
      afterLogin();
    } catch { error.textContent = "パスキーで認証できませんでした。別の方法をお試しください。"; }
    finally { button.disabled = false; }
  });
}

if (step === "mail-code") {
  bindSubmit(() => signIn({ code: element("code", HTMLInputElement).value }));
  element("resend-code", HTMLButtonElement).addEventListener("click", async () => {
    const button = element("resend-code", HTMLButtonElement);
    button.disabled = true;
    try {
      const response = await request("/auth/request-code", { email: requireFlow().email });
      error.textContent = response.ok ? "新しいコードを送信しました。" : "コードを送信できませんでした。";
    } catch { error.textContent = "通信に失敗しました。"; }
    finally { button.disabled = false; }
  });
}

if (step === "mfa") {
  const methods = { totp: "totp", code: "code", recovery: "recovery_code" } as const;
  const method = element("method", HTMLSelectElement);
  const value = element("factor", HTMLInputElement);
  const update = (): void => {
    const labels = { totp: "認証アプリのコード", code: "メールコード", recovery: "復旧コード" };
    element("factor-label", HTMLLabelElement).textContent = labels[method.value as keyof typeof labels];
    value.value = "";
    value.autocomplete = method.value === "recovery" ? "off" : "one-time-code";
    value.inputMode = method.value === "recovery" ? "text" : "numeric";
    element("mfa-send-code", HTMLButtonElement).hidden = method.value !== "code";
  };
  method.addEventListener("change", update);
  update();
  bindSubmit(() => signIn({ [methods[method.value as keyof typeof methods]]: value.value }));
  element("mfa-send-code", HTMLButtonElement).addEventListener("click", async () => {
    const button = element("mfa-send-code", HTMLButtonElement);
    button.disabled = true;
    try {
      const response = await request("/auth/request-code", { email: requireFlow().email });
      error.textContent = response.ok ? "メールコードを送信しました。" : "コードを送信できませんでした。";
    } catch { error.textContent = "通信に失敗しました。"; }
    finally { button.disabled = false; }
  });
}
