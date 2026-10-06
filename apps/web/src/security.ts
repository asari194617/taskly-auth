import "./style.css";
import "./theme";
import { request, json } from "./api";
import { element } from "./dom";

interface PasswordStatus { has_password: boolean }
interface EmailSettings { enabled: boolean }
interface TotpStatus { enabled: boolean }
interface TotpSetup { secret: string; otpauth: string }
interface RecoveryCodes { codes: string[] }
interface ErrorPayload { error?: string }

const error = element("error", HTMLParagraphElement);
const notice = element("notice", HTMLParagraphElement);
const form = element("form", HTMLFormElement);
const submit = element("submit", HTMLButtonElement);
const emailForm = element("email-form", HTMLFormElement);
const emailSubmit = element("email-submit", HTMLButtonElement);
const recoveryGenerate = element("recovery-generate", HTMLButtonElement);
const totpSetup = element("totp-setup", HTMLButtonElement);
const totpActivateForm = element("totp-activate-form", HTMLFormElement);
const totpDisableForm = element("totp-disable-form", HTMLFormElement);

async function authenticatedRequest(path: string, body?: unknown): Promise<Response | null> {
  const response = await request(path, body);
  if (response.status === 401 && body === undefined) {
    location.assign("/login?next=security");
    return null;
  }
  return response;
}

async function loadPasswordStatus(): Promise<void> {
  try {
    const response = await authenticatedRequest("/auth/password/status");
    if (!response) return;
    if (!response.ok) throw new Error("status");
    const status = await json<PasswordStatus>(response);
    element("current-field", HTMLDivElement).hidden = !status.has_password;
    form.hidden = false;
  } catch {
    error.textContent = "パスワード設定を読み込めませんでした。";
  }
}

async function loadEmailSettings(): Promise<void> {
  try {
    const response = await authenticatedRequest("/auth/email-code/settings");
    if (!response) return;
    if (!response.ok) throw new Error("status");
    const settings = await json<EmailSettings>(response);
    element("email-enabled", HTMLInputElement).checked = settings.enabled;
    element("email-section", HTMLElement).hidden = false;
  } catch {
    error.textContent = "メールコード設定を読み込めませんでした。";
  }
}

async function loadTotpStatus(): Promise<void> {
  try {
    const response = await authenticatedRequest("/auth/totp/status");
    if (!response) return;
    if (!response.ok) throw new Error("status");
    const status = await json<TotpStatus>(response);
    element("totp-state", HTMLParagraphElement).textContent = status.enabled ? "有効です。" : "無効です。";
    totpSetup.hidden = status.enabled;
    totpDisableForm.hidden = !status.enabled;
    element("totp-setup-details", HTMLDivElement).hidden = true;
    element("totp-section", HTMLElement).hidden = false;
  } catch {
    error.textContent = "認証アプリ設定を読み込めませんでした。";
  }
}

totpSetup.addEventListener("click", async () => {
  error.textContent = "";
  notice.textContent = "";
  totpSetup.disabled = true;
  try {
    const response = await authenticatedRequest("/auth/totp/setup", {});
    if (!response) return;
    if (response.status === 401) {
      location.assign("/login?next=security");
      return;
    }
    if (!response.ok) throw new Error("setup");
    const setup = await json<TotpSetup>(response);
    element("totp-secret", HTMLPreElement).textContent = setup.secret;
    element("totp-link", HTMLAnchorElement).href = setup.otpauth;
    element("totp-setup-details", HTMLDivElement).hidden = false;
  } catch {
    error.textContent = "認証アプリの設定を開始できませんでした。";
  } finally {
    totpSetup.disabled = false;
  }
});

totpActivateForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  error.textContent = "";
  notice.textContent = "";
  const button = element("totp-activate", HTMLButtonElement);
  button.disabled = true;
  try {
    const response = await authenticatedRequest("/auth/totp/activate", {
      code: element("totp-activate-code", HTMLInputElement).value,
    });
    if (!response) return;
    if (response.status === 401) {
      error.textContent = "コードが正しくないか、再ログインが必要です。";
      return;
    }
    if (!response.ok) throw new Error("activate");
    notice.textContent = "認証アプリを有効にしました。";
    await loadTotpStatus();
  } catch {
    error.textContent = "認証アプリを有効にできませんでした。";
  } finally {
    button.disabled = false;
  }
});

totpDisableForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  error.textContent = "";
  notice.textContent = "";
  const button = element("totp-disable", HTMLButtonElement);
  button.disabled = true;
  try {
    const response = await authenticatedRequest("/auth/totp/disable", {
      code: element("totp-disable-code", HTMLInputElement).value,
    });
    if (!response) return;
    if (response.status === 401) {
      error.textContent = "コードが正しくないか、再ログインが必要です。";
      return;
    }
    if (!response.ok) throw new Error("disable");
    notice.textContent = "認証アプリを無効にしました。";
    await loadTotpStatus();
  } catch {
    error.textContent = "認証アプリを無効にできませんでした。別のログイン方法が必要です。";
  } finally {
    button.disabled = false;
  }
});

emailForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  error.textContent = "";
  notice.textContent = "";
  emailSubmit.disabled = true;
  try {
    const response = await authenticatedRequest("/auth/email-code/settings", {
      enabled: element("email-enabled", HTMLInputElement).checked,
    });
    if (!response) return;
    const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
    if (response.status === 401 && payload.error === "login_required") {
      location.assign("/login?next=security");
      return;
    }
    if (!response.ok) {
      await loadEmailSettings();
      error.textContent = "設定を更新できませんでした。メールコードを無効にするにはパスワードかパスキーが必要です。";
      return;
    }
    notice.textContent = "メールコード設定を更新しました。";
  } catch {
    error.textContent = "通信に失敗しました。";
  } finally {
    emailSubmit.disabled = false;
  }
});

recoveryGenerate.addEventListener("click", async () => {
  if (!confirm("以前の未使用コードは使えなくなります。復旧コードを再発行しますか？")) return;
  error.textContent = "";
  notice.textContent = "";
  recoveryGenerate.disabled = true;
  const output = element("recovery-codes", HTMLPreElement);
  output.hidden = true;
  try {
    const response = await authenticatedRequest("/auth/recovery/generate", {});
    if (!response) return;
    if (response.status === 401) {
      location.assign("/login?next=security");
      return;
    }
    if (!response.ok) throw new Error("generate");
    const result = await json<RecoveryCodes>(response);
    output.textContent = result.codes.join("\n");
    output.hidden = false;
    notice.textContent = "復旧コードを再発行しました。この画面を閉じると再表示できません。";
  } catch {
    error.textContent = "復旧コードを再発行できませんでした。";
  } finally {
    recoveryGenerate.disabled = false;
  }
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  error.textContent = "";
  notice.textContent = "";
  submit.disabled = true;
  try {
    const response = await authenticatedRequest("/auth/password", {
      current: element("current", HTMLInputElement).value,
      next: element("next", HTMLInputElement).value,
    });
    if (!response) return;
    const payload = await json<ErrorPayload>(response).catch((): ErrorPayload => ({}));
    if (response.status === 401 && payload.error === "login_required") {
      location.assign("/login?next=security");
      return;
    }
    if (!response.ok) {
      error.textContent = "パスワードを更新できませんでした。現在のパスワードと入力内容を確認してください。";
      return;
    }
    form.hidden = true;
    notice.textContent = "パスワードを更新しました。再度ログインしてください。";
    element("login-again", HTMLAnchorElement).hidden = false;
  } catch {
    error.textContent = "通信に失敗しました。";
  } finally {
    submit.disabled = false;
  }
});

void Promise.all([loadPasswordStatus(), loadEmailSettings(), loadTotpStatus()]);
