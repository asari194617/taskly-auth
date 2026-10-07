import { Button, Modal, ModalAlign, ModalBody, ModalButtons, ModalHeader, TextField } from "@charcoal-ui/react";
import { useEffect, useState, type FormEvent } from "react";
import { request, json } from "./api";
import { decodeBase64url, encodeBase64url } from "./dom";
import { BusyLabel, LoadingStatus } from "./loading";
import { useSecurityStepUp } from "./security-step-up";
import { getLocale, tr } from "./i18n";

interface PasskeySummary { id: number; device_name: string; created_at: string }
interface PasskeyListPayload { passkeys: PasskeySummary[] }
interface PasskeyRegisterPayload { request_id: string; options: { publicKey: PublicKeyCredentialCreationOptionsJSON } }
type PasskeyDialog = { action: "register" } | { action: "rename" | "delete"; key: PasskeySummary };

function registrationDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(getLocale() === "ja" ? "ja-JP" : "en-US", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

async function authenticatedRequest<T>(path: string, body?: unknown): Promise<T> {
  const response = await request(path, body);
  const payload = await json<T & { error?: string }>(response).catch(() => ({} as T & { error?: string }));
  if (response.status === 401 && payload.error === "login_required") { location.assign("/login?next=security"); throw new Error("login required"); }
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
      .catch(() => { setLoadFailed(true); setError(tr("パスキーを読み込めませんでした。", "Could not load passkeys.")); })
      .finally(() => setLoading(false));
  }, []);

  async function finishChange(message: string) {
    setDialog(null); setNotice(message);
    try { await loadKeys(); }
    catch { setError(tr("パスキー一覧を更新できませんでした。ページを再読み込みしてください。", "Could not refresh the passkey list. Reload this page.")); }
  }

  function actionFailed(cause: unknown, fallback: string) {
    if (cause instanceof Error && cause.message === "step_up_required") {
      stepUp.invalidate();
      setDialog(null);
      setError(tr("設定を変更する前に、もう一度本人確認をしてください。", "Verify your identity again before changing settings."));
      return;
    }
    setDialogError(fallback);
  }

  function openDialog(next: PasskeyDialog) {
    if (!stepUp.canEdit || busy) return;
    if (next.action === "register" && !navigator.credentials?.create) {
      setError(tr("このブラウザはパスキーに対応していません。", "This browser does not support passkeys."));
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
      setDialogError(tr("名前は1〜64文字、255バイト以内で入力してください。", "Enter a name between 1 and 64 characters, up to 255 bytes."));
      return;
    }
    const action = dialog;
    setDialogError("");
    setBusy(true);
    try {
      if (action.action === "rename") {
        await authenticatedRequest("/auth/passkey/rename", { id: action.key.id, name });
        await finishChange(tr("パスキーの名前を変更しました。", "Passkey renamed."));
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
      await finishChange(tr("パスキーを登録しました。", "Passkey registered."));
    } catch (cause) { actionFailed(cause, action.action === "rename" ? tr("名前を変更できませんでした。", "Could not rename the passkey.") : tr("パスキーを登録できませんでした。再ログインしてからお試しください。", "Could not register the passkey. Sign in again and retry.")); }
    finally { setBusy(false); }
  }

  async function remove() {
    if (dialog?.action !== "delete" || !stepUp.canEdit || busy) return;
    setBusy(true); setDialogError("");
    try {
      await authenticatedRequest("/auth/passkey/delete", { id: dialog.key.id });
      await finishChange(tr("パスキーを削除しました。", "Passkey deleted."));
    } catch (cause) { actionFailed(cause, tr("削除できませんでした。最後のログイン手段は削除できません。", "Could not delete the passkey. You cannot remove your last sign-in method.")); }
    finally { setBusy(false); }
  }

  return <section className="security-detail" aria-label={tr("パスキーの管理", "Manage passkeys")}>
    <p className="account-intro">{tr("登録した端末やセキュリティキーを管理します。", "Manage registered devices and security keys.")}</p>
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{notice}</p>
    <div className="passkey-heading"><h2>{tr("登録済みのパスキー", "Registered passkeys")}{!loading && !loadFailed && ` (${keys.length})`}</h2><Button type="button" variant="Primary" disabled={busy || !stepUp.canEdit} onClick={() => openDialog({ action: "register" })}>{tr("パスキーを登録", "Register a passkey")}</Button></div>
    {loading ? <LoadingStatus>{tr("パスキーを読み込んでいます。", "Loading passkeys.")}</LoadingStatus> : loadFailed ? null : keys.length ? <ul className="passkey-cards">{keys.map((key) => <li className="passkey-card" key={key.id}>
      <div className="passkey-details"><h3>{key.device_name}</h3><p>{tr("登録日時: ", "Registered: ")}{registrationDate(key.created_at)}</p></div>
      <div className="passkey-actions">
        <Button type="button" size="S" disabled={busy || !stepUp.canEdit} aria-label={tr(`${key.device_name}の名前を変更`, `Rename ${key.device_name}`)} onClick={() => openDialog({ action: "rename", key })}>{tr("名前を変更", "Rename")}</Button>
        <Button type="button" size="S" variant="Danger" disabled={busy || !stepUp.canEdit} aria-label={tr(`${key.device_name}を削除`, `Delete ${key.device_name}`)} onClick={() => openDialog({ action: "delete", key })}>{tr("削除", "Delete")}</Button>
      </div>
    </li>)}</ul> : <p className="passkey-empty">{tr("登録済みのパスキーはありません。", "No passkeys registered.")}</p>}
    {dialog && <Modal title={dialog.action === "register" ? tr("パスキーを登録", "Register a passkey") : dialog.action === "rename" ? tr("パスキーの名前を変更", "Rename passkey") : tr("パスキーを削除", "Delete passkey")} size="S" bottomSheet isOpen onClose={closeDialog} isDismissable={!busy} closeButtonAriaLabel={tr("閉じる", "Close")}>
      <ModalHeader />
      <ModalBody>
        {dialog.action === "delete" ? <>
          <ModalAlign><p className="account-dialog-description">{tr(`「${dialog.key.device_name}」を削除しますか？このパスキーではログインできなくなります。`, `Delete “${dialog.key.device_name}”? You will no longer be able to sign in with this passkey.`)}</p></ModalAlign>
          {dialogError && <ModalAlign><p className="message error" role="alert">{dialogError}</p></ModalAlign>}
          <ModalButtons>
            <Button type="button" variant="Danger" fullWidth disabled={busy || !stepUp.canEdit} onClick={() => void remove()}><BusyLabel busy={busy}>{busy ? tr("削除中…", "Deleting…") : tr("削除する", "Delete")}</BusyLabel></Button>
            <Button type="button" fullWidth disabled={busy} onClick={closeDialog}>{tr("キャンセル", "Cancel")}</Button>
          </ModalButtons>
        </> : <form className="passkey-dialog-form" onSubmit={(event) => void saveName(event)} aria-busy={busy}>
          <ModalAlign><TextField id="passkey-name" label={tr("パスキーの名前", "Passkey name")} showLabel required autoFocus assistiveText={tr("64文字以内", "Up to 64 characters")} value={draftName} onChange={setDraftName} disabled={busy} /></ModalAlign>
          {dialogError && <ModalAlign><p className="message error" role="alert">{dialogError}</p></ModalAlign>}
          <ModalButtons>
            <Button type="submit" variant="Primary" fullWidth disabled={busy || !stepUp.canEdit}><BusyLabel busy={busy}>{busy ? dialog.action === "register" ? tr("登録中…", "Registering…") : tr("保存中…", "Saving…") : dialog.action === "register" ? tr("登録する", "Register") : tr("保存する", "Save")}</BusyLabel></Button>
            <Button type="button" fullWidth disabled={busy} onClick={closeDialog}>{tr("キャンセル", "Cancel")}</Button>
          </ModalButtons>
        </form>}
      </ModalBody>
    </Modal>}
  </section>;
}
