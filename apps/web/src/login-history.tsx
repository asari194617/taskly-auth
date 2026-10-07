import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { request } from "./api";
import { AccountLayout } from "./account-layout";
import { LoadingStatus } from "./loading";

interface LoginHistoryEntry {
  method: string;
  logged_in_at: string;
}

interface LoginHistoryResponse { history: LoginHistoryEntry[] }

const methodLabels: Record<string, string> = {
  password: "パスワード",
  password_email_code: "パスワード + メールコード",
  password_totp: "パスワード + 認証アプリ",
  password_recovery_code: "パスワード + 復旧コード",
  email_code: "メールコード",
  totp: "認証アプリ",
  recovery_code: "復旧コード",
  passkey: "パスキー",
};

const dateFormatter = new Intl.DateTimeFormat("ja-JP", {
  year: "numeric",
  month: "numeric",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  timeZoneName: "short",
});

function formattedDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : dateFormatter.format(date);
}

function LoginHistoryApp() {
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
      } catch { setError("ログイン履歴を読み込めませんでした。時間をおいて再度お試しください。"); }
    }
    void load();
  }, []);

  return <AccountLayout page="login-history">
    <h1>ログイン履歴</h1>
    <p className="account-intro">この機能の開始後に行ったログインを、最新50件まで表示します。</p>
    {error && <p className="message error" role="alert">{error}</p>}
    {!error && history === null && <LoadingStatus>ログイン履歴を読み込んでいます。</LoadingStatus>}
    {history?.length === 0 && <p className="login-history-empty">記録されたログインはまだありません。次回のログインからここに表示されます。</p>}
    {history && history.length > 0 && <ol className="login-history-list" aria-label="ログイン履歴">
      {history.map((entry, index) => <li key={`${entry.logged_in_at}-${index}`} className="login-history-item">
        <span className="login-history-method">{methodLabels[entry.method] ?? "その他の方法"}</span>
        <time dateTime={entry.logged_in_at}>{formattedDate(entry.logged_in_at)}</time>
      </li>)}
    </ol>}
  </AccountLayout>;
}

createRoot(document.getElementById("app")!).render(<LoginHistoryApp />);
