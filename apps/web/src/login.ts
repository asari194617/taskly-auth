import "./style.css";
import { request, json } from "./api";
import { apiBase, configureTasklyLinks } from "./config";
import { decodeBase64url, element, encodeBase64url } from "./dom";

interface LoginPayload {
  mfa_required?: boolean;
  auth_challenge?: string;
}

interface PasskeyBeginPayload {
  request_id: string;
  options: { publicKey: PublicKeyCredentialRequestOptionsJSON };
}

const parameters = new URLSearchParams(location.search);
const requestID = parameters.get("request_id");
const next = parameters.get("next");
const managementTarget = !requestID && (next === "passkeys" || next === "security") ? next : "";
const form = element("form", HTMLFormElement);
const error = element("error", HTMLParagraphElement);
const submit = element("submit", HTMLButtonElement);
const email = element("email", HTMLInputElement);
const password = element("password", HTMLInputElement);
const code = element("code", HTMLInputElement);
const totp = element("totp", HTMLInputElement);
const recovery = element("recovery", HTMLInputElement);
const passkeyButton = element("passkey", HTMLButtonElement);
let challenge = "";

configureTasklyLinks();

if (!managementTarget && (!requestID || !/^[A-Za-z0-9._~-]{43,128}$/.test(requestID))) {
  error.textContent = "Taskly からログインを開始してください。";
  form.hidden = true;
}

function afterLogin(): void {
  const target = managementTarget
    ? `/${managementTarget}`
    : `${apiBase}/oauth/authorize?request_id=${encodeURIComponent(requestID ?? "")}`;
  location.assign(target);
}

element("send-code", HTMLButtonElement).addEventListener("click", async () => {
  error.textContent = "";
  if (!email.value) {
    error.textContent = "メールアドレスを入力してください。";
    return;
  }
  try {
    const response = await request("/auth/request-code", { email: email.value });
    error.textContent = response.ok ? "メールコードを送信しました。" : "コードを送信できませんでした。";
  } catch {
    error.textContent = "通信に失敗しました。";
  }
});

passkeyButton.addEventListener("click", async () => {
  error.textContent = "";
  if (!email.value) {
    error.textContent = "メールアドレスを入力してください。";
    return;
  }
  if (!navigator.credentials?.get) {
    error.textContent = "このブラウザはパスキーに対応していません。";
    return;
  }
  passkeyButton.disabled = true;
  try {
    const beginResponse = await request("/auth/passkey/begin", { email: email.value });
    if (!beginResponse.ok) throw new Error("begin failed");
    const begin = await json<PasskeyBeginPayload>(beginResponse);
    const source = begin.options.publicKey;
    const publicKey = {
      ...source,
      challenge: decodeBase64url(source.challenge),
      allowCredentials: source.allowCredentials?.map((item) => ({
        id: decodeBase64url(item.id),
        type: "public-key",
        transports: item.transports as AuthenticatorTransport[] | undefined,
      })),
    } as unknown as PublicKeyCredentialRequestOptions;
    const credential = await navigator.credentials.get({ publicKey }) as PublicKeyCredential | null;
    if (!credential) throw new Error("no credential");
    const assertion = credential.response as AuthenticatorAssertionResponse;
    const finishResponse = await request("/auth/passkey/finish", {
      request_id: begin.request_id,
      credential: {
        id: credential.id,
        rawId: encodeBase64url(credential.rawId),
        type: credential.type,
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
  } catch {
    error.textContent = "パスキーで認証できませんでした。従来のログインも利用できます。";
  } finally {
    passkeyButton.disabled = false;
  }
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  error.textContent = "";
  submit.disabled = true;
  try {
    const response = await request("/auth/login", {
      email: email.value,
      password: challenge ? "" : password.value,
      code: code.value,
      totp: totp.value,
      recovery_code: recovery.value,
      auth_challenge: challenge,
    });
    const payload = await json<LoginPayload>(response).catch((): LoginPayload => ({}));
    if (response.status === 202 && payload.mfa_required && payload.auth_challenge) {
      challenge = payload.auth_challenge;
      element("primary", HTMLDivElement).hidden = true;
      code.value = "";
      totp.value = "";
      recovery.value = "";
      error.textContent = "2段階認証のコードを入力してください。";
      return;
    }
    if (!response.ok) {
      error.textContent = "認証できませんでした。入力内容を確認してください。";
      return;
    }
    afterLogin();
  } catch {
    error.textContent = "通信に失敗しました。";
  } finally {
    submit.disabled = false;
  }
});
