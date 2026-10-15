import { Button } from "@charcoal-ui/react";
import { useState } from "react";
import { tasklyLoginURL } from "./config";
import { tr } from "./i18n";

export function PasskeyBrowserHelp() {
  const [copyStatus, setCopyStatus] = useState<"" | "copied" | "failed">("");

  async function copyLoginLink() {
    try {
      await navigator.clipboard.writeText(tasklyLoginURL);
      setCopyStatus("copied");
    } catch { setCopyStatus("failed"); }
  }

  return <aside id="passkey-browser-help" className="passkey-browser-help" aria-label={tr("パスキーの利用について", "Using passkeys")}>
    <p>{tr("Google アプリ内ではパスキーを利用できない場合があります。パスワードまたはメールコードでもログインできます。", "Passkeys may be unavailable in the Google app. You can also sign in with your password or an email code.")}</p>
    <p>{tr("パスキーを使う場合は、下のリンクをコピーして Safari で開き、Taskly からログインをやり直してください。", "To use a passkey, copy the link below, open it in Safari, and start signing in again from Taskly.")}</p>
    <a className="passkey-login-link" href={tasklyLoginURL}>{tasklyLoginURL}</a>
    <Button type="button" fullWidth onClick={() => void copyLoginLink()}>{tr("ログイン用リンクをコピー", "Copy sign-in link")}</Button>
    <p role="status">{copyStatus === "copied" ? tr("コピーしました。Safari に貼り付けて開いてください。", "Link copied. Paste it into Safari to continue.")
      : copyStatus === "failed" ? tr("コピーできませんでした。上のリンクを長押ししてコピーし、Safari で開いてください。", "Could not copy the link. Touch and hold the link above to copy it, then open it in Safari.") : ""}</p>
  </aside>;
}
