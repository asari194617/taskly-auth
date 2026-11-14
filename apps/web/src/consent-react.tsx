import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useRef, useState } from "react";
import { request } from "./api";
import { AuthHeader } from "./auth-header";
import { AuthFooter } from "./auth-footer";
import { BusyLabel, LoadingStatus } from "./loading";
import { LocaleRoot, tr, useLocale } from "./i18n";
import { consentRedirectURL, consentRequestID, parseConsentDetails, type ConsentDetails, type ConsentScope } from "./consent-flow";

const requestID = consentRequestID(location.search);

function permissionLabel(scope: ConsentScope): string {
  switch (scope) {
    case "guidance:read": return tr("企業情報・配布資料・会場マップ・自分のガイダンスタスクを確認する", "View company information, handouts, venue maps, and your guidance tasks");
    case "guidance:bookmarks:write": return tr("「気になる」企業を追加・削除する", "Add or remove companies from your favorites");
    case "guidance:tasks:write": return tr("自分のガイダンスタスクの完了状態を変更する", "Change completion of your guidance tasks");
  }
}

function ConsentApp() {
  const locale = useLocale();
  const [details, setDetails] = useState<ConsentDetails | null>(null);
  const [loading, setLoading] = useState(!!requestID);
  const [error, setError] = useState("");
  const [needsLogin, setNeedsLogin] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const pending = useRef(false);

  useEffect(() => {
    document.title = `${tr("連携の許可", "Authorize connection")} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [locale]);

  useEffect(() => {
    if (!requestID) return;
    let active = true;
    async function load() {
      try {
        const response = await request(`/oauth/consent?request_id=${encodeURIComponent(requestID!)}`);
        if (!active) return;
        if (response.status === 401) { setNeedsLogin(true); return; }
        if (!response.ok) throw new Error("consent unavailable");
        const parsed = parseConsentDetails(await response.json(), import.meta.env.DEV);
        if (!parsed) throw new Error("invalid consent");
        if (active) setDetails(parsed);
      } catch {
        if (active) setError(tr("連携の内容を確認できませんでした。ChatGPT から接続をやり直してください。", "Could not load the requested permissions. Start the connection again from ChatGPT."));
      } finally { if (active) setLoading(false); }
    }
    void load();
    return () => { active = false; };
  }, []);

  async function decide(approve: boolean) {
    if (!details || !requestID || pending.current) return;
    pending.current = true;
    setBusy(approve ? "allow" : "deny");
    setError("");
    let navigating = false;
    try {
      const response = await request("/oauth/consent", { request_id: requestID, csrf_token: details.csrf_token, approve });
      if (response.status === 401) { setDetails(null); setNeedsLogin(true); return; }
      if (!response.ok) throw new Error("decision failed");
      const redirect = consentRedirectURL(await response.json(), details.redirect_uri, import.meta.env.DEV);
      if (!redirect) throw new Error("invalid redirect");
      location.assign(redirect);
      navigating = true;
    } catch {
      setDetails(null);
      setError(tr("連携を完了できませんでした。ChatGPT から接続をやり直してください。", "Could not complete the connection. Start the connection again from ChatGPT."));
    } finally { if (!navigating) { pending.current = false; setBusy(null); } }
  }

  return <CharcoalProvider><main className="wide">
    <AuthHeader />
    <h1>{tr("連携の許可", "Authorize connection")}</h1>
    {loading && <LoadingStatus>{tr("連携の内容を確認しています。", "Loading requested permissions")}</LoadingStatus>}
    {!requestID && <p className="message error" role="alert">{tr("接続情報が無効です。ChatGPT から接続をやり直してください。", "This connection request is invalid. Start the connection again from ChatGPT.")}</p>}
    {error && <p className="message error" role="alert">{error}</p>}
    {needsLogin && requestID && <p><a href={`/login?request_id=${encodeURIComponent(requestID)}`}>{tr("ログインして続ける", "Sign in to continue")}</a></p>}
    {details && <>
      <p><strong>{details.client_name}</strong>{tr("に、次の操作を許可しますか？", " is requesting permission to:")}</p>
      <ul>{details.scopes.map((scope) => <li key={scope}><p>{permissionLabel(scope)}</p></li>)}</ul>
      <p className="account-intro">{tr("対象サービス", "Service")}: {new URL(details.resource).host}</p>
      <p className="account-intro">{tr("接続先", "Return to")}: {new URL(details.redirect_uri).host}</p>
      <div className="actions" aria-busy={busy !== null}>
        <Button type="button" disabled={busy !== null} onClick={() => void decide(false)}><BusyLabel busy={busy === "deny"}>{tr("許可しない", "Deny")}</BusyLabel></Button>
        <Button type="button" variant="Primary" disabled={busy !== null} onClick={() => void decide(true)}><BusyLabel busy={busy === "allow"}>{tr("許可する", "Allow")}</BusyLabel></Button>
      </div>
    </>}
    <AuthFooter />
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={ConsentApp} />);
