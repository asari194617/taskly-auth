import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { request } from "./api";
import { AccountLayout } from "./account-layout";
import { getLocale, LocaleRoot, tr, useLocale } from "./i18n";
import { LoadingStatus } from "./loading";

interface LoginHistoryEntry {
  method: string;
  logged_in_at: string;
}

interface LoginHistoryResponse { history: LoginHistoryEntry[] }

function methodLabel(method: string): string {
  const labels: Record<string, string> = {
    password: tr("パスワード", "Password"),
    password_email_code: tr("パスワード + メールコード", "Password + email code"),
    password_totp: tr("パスワード + 認証アプリ", "Password + authenticator app"),
    password_recovery_code: tr("パスワード + 復旧コード", "Password + recovery code"),
    email_code: tr("メールコード", "Email code"),
    totp: tr("認証アプリ", "Authenticator app"),
    recovery_code: tr("復旧コード", "Recovery code"),
    passkey: tr("パスキー", "Passkey"),
  };
  return labels[method] ?? tr("その他の方法", "Other method");
}

function formattedDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(getLocale() === "ja" ? "ja-JP" : "en-US", {
    year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "short",
  }).format(date);
}

function LoginHistoryApp() {
  const locale = useLocale();
  const [history, setHistory] = useState<LoginHistoryEntry[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    async function load() {
      try {
        const response = await request("/auth/login-history");
        if (response.status === 401) { location.assign("/login?next=login-history"); return; }
        if (!response.ok) throw new Error("load failed");
        const payload = await response.json() as LoginHistoryResponse;
        if (!Array.isArray(payload.history)) throw new Error("invalid response");
        setHistory(payload.history);
      } catch { setError(tr("ログイン履歴を読み込めませんでした。時間をおいて再度お試しください。", "Could not load login history. Please try again later.")); }
    }
    void load();
  }, []);
  useEffect(() => {
    document.title = `${tr("ログイン履歴", "Login history")} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [locale]);

  return <AccountLayout page="login-history">
    <h1>{tr("ログイン履歴", "Login history")}</h1>
    <p className="account-intro">{tr("この機能の開始後に行ったログインを、最新50件まで表示します。", "Shows up to 50 of your most recent sign-ins since this feature was introduced.")}</p>
    {error && <p className="message error" role="alert">{error}</p>}
    {!error && history === null && <LoadingStatus>{tr("ログイン履歴を読み込んでいます。", "Loading login history")}</LoadingStatus>}
    {history?.length === 0 && <p className="login-history-empty">{tr("記録されたログインはまだありません。次回のログインからここに表示されます。", "No sign-ins have been recorded yet. Your next sign-in will appear here.")}</p>}
    {history && history.length > 0 && <ol className="login-history-list" aria-label={tr("ログイン履歴", "Login history")}>
      {history.map((entry, index) => <li key={`${entry.logged_in_at}-${index}`} className="login-history-item">
        <span className="login-history-method">{methodLabel(entry.method)}</span>
        <time dateTime={entry.logged_in_at}>{formattedDate(entry.logged_in_at)}</time>
      </li>)}
    </ol>}
  </AccountLayout>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={LoginHistoryApp} />);
