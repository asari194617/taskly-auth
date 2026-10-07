import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { request, json } from "./api";
import { tasklyLoginURL } from "./config";
import { decodeBase64url, encodeBase64url } from "./dom";
import { AuthHeader } from "./auth-header";

interface PasskeySummary { id: number; device_name: string; created_at: string }
interface PasskeyListPayload { passkeys: PasskeySummary[] }
interface PasskeyRegisterPayload { request_id: string; options: { publicKey: PublicKeyCredentialCreationOptionsJSON } }

async function authenticatedRequest<T>(path: string, body?: unknown): Promise<T> {
  const response = await request(path, body);
  if (response.status === 401) { location.assign("/login?next=passkeys"); throw new Error("login required"); }
  const payload = await json<T & { error?: string }>(response).catch(() => ({} as T & { error?: string }));
  if (!response.ok) throw new Error(payload.error || "request failed");
  return payload;
}

function PasskeysApp() {
  const [keys, setKeys] = useState<PasskeySummary[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function loadKeys() {
    const result = await authenticatedRequest<PasskeyListPayload>("/auth/passkeys");
    setKeys(result.passkeys);
  }
  useEffect(() => { void loadKeys().catch(() => setError("パスキーを読み込めませんでした。")); }, []);

  async function rename(key: PasskeySummary) {
    const name = prompt("新しい名前", key.device_name);
    if (name === null) return;
    setBusy(true); setError("");
    try { await authenticatedRequest("/auth/passkey/rename", { id:key.id, name }); await loadKeys(); }
    catch { setError("名前を変更できませんでした。"); }
    finally { setBusy(false); }
  }
  async function remove(key: PasskeySummary) {
    if (!confirm("このパスキーを削除しますか？")) return;
    setBusy(true); setError("");
    try { await authenticatedRequest("/auth/passkey/delete", { id:key.id }); await loadKeys(); }
    catch { setError("削除できませんでした。最後のログイン手段は削除できません。"); }
    finally { setBusy(false); }
  }
  async function register() {
    setError(""); setNotice("");
    if (!navigator.credentials?.create) { setError("このブラウザはパスキーに対応していません。"); return; }
    const name = prompt("パスキーの名前", "Passkey");
    if (name === null) return;
    setBusy(true);
    try {
      const begin = await authenticatedRequest<PasskeyRegisterPayload>("/auth/passkey/register/begin", {});
      const source = begin.options.publicKey;
      const publicKey = {
        ...source,
        challenge: decodeBase64url(source.challenge),
        user: { ...source.user, id: decodeBase64url(source.user.id) },
        excludeCredentials: source.excludeCredentials?.map((item) => ({
          id: decodeBase64url(item.id), type: "public-key",
          transports: item.transports as AuthenticatorTransport[] | undefined,
        })),
      } as unknown as PublicKeyCredentialCreationOptions;
      const credential = await navigator.credentials.create({ publicKey }) as PublicKeyCredential | null;
      if (!credential) throw new Error("no credential");
      const attestation = credential.response as AuthenticatorAttestationResponse;
      await authenticatedRequest("/auth/passkey/register/finish", {
        request_id: begin.request_id, device_name: name,
        credential: {
          id: credential.id, rawId: encodeBase64url(credential.rawId), type: credential.type,
          response: { attestationObject: encodeBase64url(attestation.attestationObject), clientDataJSON: encodeBase64url(attestation.clientDataJSON), transports: attestation.getTransports?.() ?? [] },
          clientExtensionResults: credential.getClientExtensionResults(),
        },
      });
      setNotice("パスキーを登録しました。"); await loadKeys();
    } catch { setError("パスキーを登録できませんでした。再ログインしてからお試しください。"); }
    finally { setBusy(false); }
  }

  return <CharcoalProvider><main className="wide">
    <AuthHeader />
    <h1>パスキーを管理</h1><p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    <ul className="key-list">{keys.length ? keys.map((key) => <li key={key.id}><span>{key.device_name} ({key.created_at})</span><div className="actions"><Button type="button" disabled={busy} onClick={() => void rename(key)}>名前を変更</Button><Button type="button" disabled={busy} onClick={() => void remove(key)}>削除</Button></div></li>) : <li>登録済みのパスキーはありません。</li>}</ul>
    <Button type="button" variant="Primary" disabled={busy} onClick={() => void register()}>パスキーを登録</Button>
    <nav><a href="/security">認証設定</a><a href="/profile">アカウント情報</a><a href={tasklyLoginURL}>Taskly に戻る</a></nav>
  </main></CharcoalProvider>;
}
createRoot(document.getElementById("app")!).render(<PasskeysApp />);
