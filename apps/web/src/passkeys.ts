import "./style.css";
import "./theme";
import { request, json } from "./api";
import { configureTasklyLinks } from "./config";
import { decodeBase64url, element, encodeBase64url } from "./dom";

interface PasskeySummary {
  id: number;
  device_name: string;
  created_at: string;
}

interface PasskeyListPayload { passkeys: PasskeySummary[] }
interface PasskeyRegisterPayload {
  request_id: string;
  options: { publicKey: PublicKeyCredentialCreationOptionsJSON };
}

const keys = element("keys", HTMLUListElement);
const error = element("error", HTMLParagraphElement);
const notice = element("notice", HTMLParagraphElement);
const register = element("register", HTMLButtonElement);

configureTasklyLinks();

async function authenticatedRequest<T>(path: string, body?: unknown): Promise<T> {
  const response = await request(path, body);
  if (response.status === 401) {
    location.assign("/login?next=passkeys");
    throw new Error("login required");
  }
  const payload = await json<T & { error?: string }>(response).catch(() => ({} as T & { error?: string }));
  if (!response.ok) throw new Error(payload.error || "request failed");
  return payload;
}

async function loadKeys(): Promise<void> {
  const result = await authenticatedRequest<PasskeyListPayload>("/auth/passkeys");
  keys.replaceChildren();
  for (const key of result.passkeys) {
    const item = document.createElement("li");
    const description = document.createElement("span");
    description.textContent = `${key.device_name} (${key.created_at})`;
    const rename = document.createElement("button");
    rename.type = "button";
    rename.textContent = "名前を変更";
    rename.addEventListener("click", async () => {
      const name = prompt("新しい名前", key.device_name);
      if (name === null) return;
      error.textContent = "";
      try {
        await authenticatedRequest("/auth/passkey/rename", { id: key.id, name });
        await loadKeys();
      } catch {
        error.textContent = "名前を変更できませんでした。";
      }
    });
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = "削除";
    remove.addEventListener("click", async () => {
      if (!confirm("このパスキーを削除しますか？")) return;
      error.textContent = "";
      try {
        await authenticatedRequest("/auth/passkey/delete", { id: key.id });
        await loadKeys();
      } catch {
        error.textContent = "削除できませんでした。最後のログイン手段は削除できません。";
      }
    });
    item.append(description, rename, remove);
    keys.append(item);
  }
  if (result.passkeys.length === 0) {
    const item = document.createElement("li");
    item.textContent = "登録済みのパスキーはありません。";
    keys.append(item);
  }
}

register.addEventListener("click", async () => {
  error.textContent = "";
  notice.textContent = "";
  if (!navigator.credentials?.create) {
    error.textContent = "このブラウザはパスキーに対応していません。";
    return;
  }
  const name = prompt("パスキーの名前", "Passkey");
  if (name === null) return;
  register.disabled = true;
  try {
    const begin = await authenticatedRequest<PasskeyRegisterPayload>("/auth/passkey/register/begin", {});
    const source = begin.options.publicKey;
    const publicKey = {
      ...source,
      challenge: decodeBase64url(source.challenge),
      user: { ...source.user, id: decodeBase64url(source.user.id) },
      excludeCredentials: source.excludeCredentials?.map((item) => ({
        id: decodeBase64url(item.id),
        type: "public-key",
        transports: item.transports as AuthenticatorTransport[] | undefined,
      })),
    } as unknown as PublicKeyCredentialCreationOptions;
    const credential = await navigator.credentials.create({ publicKey }) as PublicKeyCredential | null;
    if (!credential) throw new Error("no credential");
    const attestation = credential.response as AuthenticatorAttestationResponse;
    await authenticatedRequest("/auth/passkey/register/finish", {
      request_id: begin.request_id,
      device_name: name,
      credential: {
        id: credential.id,
        rawId: encodeBase64url(credential.rawId),
        type: credential.type,
        response: {
          attestationObject: encodeBase64url(attestation.attestationObject),
          clientDataJSON: encodeBase64url(attestation.clientDataJSON),
          transports: attestation.getTransports?.() ?? [],
        },
        clientExtensionResults: credential.getClientExtensionResults(),
      },
    });
    notice.textContent = "パスキーを登録しました。";
    await loadKeys();
  } catch {
    error.textContent = "パスキーを登録できませんでした。再ログインしてからお試しください。";
  } finally {
    register.disabled = false;
  }
});

void loadKeys().catch(() => {
  error.textContent = "パスキーを読み込めませんでした。";
});
