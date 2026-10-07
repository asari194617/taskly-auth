import { Button, Modal, ModalAlign, ModalBody, ModalButtons, ModalHeader, TextField } from "@charcoal-ui/react";
import { useEffect, useState, type FormEvent } from "react";
import { request, json } from "./api";
import { decodeBase64url, encodeBase64url } from "./dom";
import { BusyLabel, LoadingStatus } from "./loading";
import { useSecurityStepUp } from "./security-step-up";

interface PasskeySummary { id: number; device_name: string; created_at: string }
interface PasskeyListPayload { passkeys: PasskeySummary[] }
interface PasskeyRegisterPayload { request_id: string; options: { publicKey: PublicKeyCredentialCreationOptionsJSON } }
type PasskeyDialog = { action: "register" } | { action: "rename" | "delete"; key: PasskeySummary };

function registrationDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("ja-JP", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

async function authenticatedRequest<T>(path: string, body?: unknown): Promise<T> {
  const response = await request(path, body);
  const payload = await json<T & { error?: string }>(response).catch(() => ({} as T & { error?: string }));
  if (response.status === 401 && payload.error === "login_required") { location.assign("/login?next=passkeys"); throw new Error("login required"); }
  if (!response.ok) throw new Error(payload.error || "request failed");
  return payload;
}

export function PasskeysSection({ stepUp }: { stepUp: ReturnType<typeof useSecurityStepUp> }) {
  const [keys, setKeys] = useState<PasskeySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [dialog, setDialog] = useState<PasskeyDialog | null>(null);
  const [draftName, setDraftName] = useState("");
  const [dialogError, setDialogError] = useState("");
  async function loadKeys() {
    const result = await authenticatedRequest<PasskeyListPayload>("/auth/passkeys");
    setKeys(result.passkeys);
    setLoadFailed(false);
  }
  useEffect(() => {
    void loadKeys()
      .catch(() => { setLoadFailed(true); setError("パスキーを読み込めませんでした。"); })
      .finally(() => setLoading(false));
  }, []);

  async function finishChange(message: string) {
    setDialog(null); setNotice(message);
    try { await loadKeys(); }
    catch { setError("パスキー一覧を更新できませんでした。ページを再読み込みしてください。"); }
  }

  function actionFailed(cause: unknown, fallback: string) {
    if (cause instanceof Error && cause.message === "step_up_required") {
      stepUp.invalidate();
      setDialog(null);
      setError("設定を変更する前に、もう一度本人確認をしてください。");
      return;
    }
    setDialogError(fallback);
  }

  function openDialog(next: PasskeyDialog) {
    if (!stepUp.canEdit || busy) return;
    if (next.action === "register" && !navigator.credentials?.create) {
      setError("このブラウザはパスキーに対応していません。");
      return;
    }
    setError(""); setNotice(""); setDialogError("");
    setDraftName(next.action === "register" ? "Passkey" : next.key.device_name);
    setDialog(next);
  }

  function closeDialog() {
    if (busy) return;
    setDialog(null); setDialogError("");
  }

  async function saveName(event: FormEvent) {
    event.preventDefault();
    if (!dialog || dialog.action === "delete" || !stepUp.canEdit || busy) return;
    const name = draftName.trim();
    if (!name || Array.from(name).length > 64 || new TextEncoder().encode(name).length > 255) {
      setDialogError("名前は1〜64文字、255バイト以内で入力してください。");
      return;
    }
    const action = dialog;
    setDialogError("");
    setBusy(true);
    try {
      if (action.action === "rename") {
        await authenticatedRequest("/auth/passkey/rename", { id: action.key.id, name });
        await finishChange("パスキーの名前を変更しました。");
        return;
      }
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
      await finishChange("パスキーを登録しました。");
    } catch (cause) { actionFailed(cause, action.action === "rename" ? "名前を変更できませんでした。" : "パスキーを登録できませんでした。再ログインしてからお試しください。"); }
    finally { setBusy(false); }
  }

  async function remove() {
    if (dialog?.action !== "delete" || !stepUp.canEdit || busy) return;
    setBusy(true); setDialogError("");
    try {
      await authenticatedRequest("/auth/passkey/delete", { id: dialog.key.id });
      await finishChange("パスキーを削除しました。");
    } catch (cause) { actionFailed(cause, "削除できませんでした。最後のログイン手段は削除できません。"); }
    finally { setBusy(false); }
  }

  return <section className="security-detail" aria-label="パスキーの管理">
    <p className="account-intro">登録した端末やセキュリティキーを管理します。</p>
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    <div className="passkey-heading"><h2>登録済みのパスキー</h2><Button type="button" variant="Primary" disabled={busy || !stepUp.canEdit} onClick={() => openDialog({ action: "register" })}>パスキーを登録</Button></div>
    {loading ? <LoadingStatus>パスキーを読み込んでいます。</LoadingStatus> : loadFailed ? null : keys.length ? <ul className="passkey-cards">{keys.map((key) => <li className="passkey-card" key={key.id}>
      <div className="passkey-details"><h3>{key.device_name}</h3><p>登録日時: {registrationDate(key.created_at)}</p></div>
      <div className="passkey-actions">
        <Button type="button" size="S" disabled={busy || !stepUp.canEdit} aria-label={`${key.device_name}の名前を変更`} onClick={() => openDialog({ action: "rename", key })}>名前を変更</Button>
        <Button type="button" size="S" variant="Danger" disabled={busy || !stepUp.canEdit} aria-label={`${key.device_name}を削除`} onClick={() => openDialog({ action: "delete", key })}>削除</Button>
      </div>
    </li>)}</ul> : <p className="passkey-empty">登録済みのパスキーはありません。</p>}
    {dialog && <Modal title={dialog.action === "register" ? "パスキーを登録" : dialog.action === "rename" ? "パスキーの名前を変更" : "パスキーを削除"} size="S" bottomSheet isOpen onClose={closeDialog} isDismissable={!busy} closeButtonAriaLabel="閉じる">
      <ModalHeader />
      <ModalBody>
        {dialog.action === "delete" ? <>
          <ModalAlign><p className="account-dialog-description">「{dialog.key.device_name}」を削除しますか？このパスキーではログインできなくなります。</p></ModalAlign>
          {dialogError && <ModalAlign><p className="message error" role="alert">{dialogError}</p></ModalAlign>}
          <ModalButtons>
            <Button type="button" variant="Danger" fullWidth disabled={busy || !stepUp.canEdit} onClick={() => void remove()}><BusyLabel busy={busy}>{busy ? "削除中…" : "削除する"}</BusyLabel></Button>
            <Button type="button" fullWidth disabled={busy} onClick={closeDialog}>キャンセル</Button>
          </ModalButtons>
        </> : <form className="passkey-dialog-form" onSubmit={(event) => void saveName(event)} aria-busy={busy}>
          <ModalAlign><TextField id="passkey-name" label="パスキーの名前" showLabel required autoFocus assistiveText="64文字以内" value={draftName} onChange={setDraftName} disabled={busy} /></ModalAlign>
          {dialogError && <ModalAlign><p className="message error" role="alert">{dialogError}</p></ModalAlign>}
          <ModalButtons>
            <Button type="submit" variant="Primary" fullWidth disabled={busy || !stepUp.canEdit}><BusyLabel busy={busy}>{busy ? dialog.action === "register" ? "登録中…" : "保存中…" : dialog.action === "register" ? "登録する" : "保存する"}</BusyLabel></Button>
            <Button type="button" fullWidth disabled={busy} onClick={closeDialog}>キャンセル</Button>
          </ModalButtons>
        </form>}
      </ModalBody>
    </Modal>}
  </section>;
}
