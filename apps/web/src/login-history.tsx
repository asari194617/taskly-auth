import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { request } from "./api";
import { AccountLayout } from "./account-layout";
import { getLocale, LocaleRoot, tr, useLocale } from "./i18n";
import { BusyLabel, LoadingStatus } from "./loading";

interface LoginLocation {
  city: string | null;
  region: string | null;
  country: string | null;
}

interface LoginHistoryEntry {
  method: string;
  logged_in_at: string;
  ip_address: string | null;
  location: LoginLocation | null;
  device: string | null;
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

function locationLabel(location: LoginLocation | null): string {
  if (!location) return tr("不明", "Unknown");
  const country = location.country?.trim();
  let countryName = country || "";
  if (country && /^[A-Z]{2}$/.test(country)) {
    try { countryName = new Intl.DisplayNames([getLocale()], { type: "region" }).of(country) || country; }
    catch { countryName = country; }
  }
  const parts = [location.city, location.region, countryName].map((value) => value?.trim()).filter(Boolean);
  return [...new Set(parts)].join(", ") || tr("不明", "Unknown");
}

function LoginHistoryApp() {
  const locale = useLocale();
  const [history, setHistory] = useState<LoginHistoryEntry[] | null>(null);
  const [error, setError] = useState("");
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");

  async function exportCSV() {
    if (exporting) return;
    setExporting(true); setExportError("");
    try {
      const response = await request("/auth/login-history/export");
      if (response.status === 401) { location.assign("/login?next=login-history"); return; }
      if (!response.ok) throw new Error("export failed");
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `taskly-login-history-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch {
      setExportError(tr("CSVをダウンロードできませんでした。時間をおいて再度お試しください。", "Could not download the CSV. Please try again later."));
    } finally { setExporting(false); }
  }

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
    <p className="account-intro">{tr("最新50件を表示します。場所はIPアドレスからの推定です。", "Shows the 50 most recent sign-ins. Locations are estimated from IP addresses.")}</p>
    <div className="login-history-toolbar">
      <p>{tr("CSVには保存されている全履歴を含みます。古い記録には場所やデバイス情報がない場合があります。", "The CSV includes all retained sign-ins. Older records may lack location or device details.")}</p>
      <Button type="button" disabled={exporting} onClick={() => void exportCSV()}><BusyLabel busy={exporting}>{tr("CSVをダウンロード", "Download CSV")}</BusyLabel></Button>
    </div>
    {exportError && <p className="message error" role="alert">{exportError}</p>}
    {error && <p className="message error" role="alert">{error}</p>}
    {!error && history === null && <LoadingStatus>{tr("ログイン履歴を読み込んでいます。", "Loading login history")}</LoadingStatus>}
    {history?.length === 0 && <p className="login-history-empty">{tr("記録されたログインはまだありません。次回のログインからここに表示されます。", "No sign-ins have been recorded yet. Your next sign-in will appear here.")}</p>}
    {history && history.length > 0 && <ol className="login-history-list" aria-label={tr("ログイン履歴", "Login history")}>
      {history.map((entry, index) => <li key={`${entry.logged_in_at}-${index}`} className="login-history-item">
        <div className="login-history-heading">
          <span className="login-history-method">{methodLabel(entry.method)}</span>
          <time dateTime={entry.logged_in_at}>{formattedDate(entry.logged_in_at)}</time>
        </div>
        <dl className="login-history-meta">
          <div><dt>{tr("場所", "Location")}</dt><dd>{locationLabel(entry.location)}</dd></div>
          <div><dt>{tr("IPアドレス", "IP address")}</dt><dd>{entry.ip_address || tr("不明", "Unknown")}</dd></div>
          <div><dt>{tr("デバイス情報", "Device")}</dt><dd>{entry.device || tr("不明", "Unknown")}</dd></div>
        </dl>
      </li>)}
    </ol>}
  </AccountLayout>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={LoginHistoryApp} />);
