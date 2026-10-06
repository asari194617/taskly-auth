import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { request } from "./api";
import { getThemePreference, setThemePreference } from "./theme";

interface Profile { email: string; display_name: string; email_verified: boolean }

function ProfileApp() {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [theme, setTheme] = useState(getThemePreference());

  async function load() {
    const response = await request("/auth/profile");
    if (response.status === 401) { location.assign("/login?next=profile"); return; }
    if (!response.ok) { setError("アカウント情報を読み込めませんでした。"); return; }
    const value = await response.json() as Profile;
    setProfile(value); setName(value.display_name);
  }
  useEffect(() => { void load(); }, []);

  async function save(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      const response = await request("/auth/profile", { display_name: name });
      if (response.status === 401) { location.assign("/login?next=profile"); return; }
      if (!response.ok) throw new Error(response.status === 503 ? "設定を保存しましたが、Taskly への反映を確認できません。もう一度保存してください。" : "表示名を保存できませんでした。");
      setMessage("表示名を保存しました。");
      setProfile((current) => current ? { ...current, display_name: name.trim() } : current);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "表示名を保存できませんでした。"); }
    finally { setBusy(false); }
  }

  async function sendCode() {
    setBusy(true); setError(""); setMessage("");
    try {
      const response = await request("/auth/profile/email-verification/request", {});
      if (response.status === 401) { location.assign("/login?next=profile"); return; }
      if (!response.ok) throw new Error("確認コードを送信できませんでした。");
      setSent(true); setMessage("メールアドレスに確認コードを送信しました。");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "送信できませんでした。"); }
    finally { setBusy(false); }
  }

  async function confirm(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      const response = await request("/auth/profile/email-verification/confirm", { code });
      if (response.status === 401) { location.assign("/login?next=profile"); return; }
      if (!response.ok) throw new Error("コードを確認できませんでした。入力内容と有効期限を確認してください。");
      setProfile((current) => current ? { ...current, email_verified: true } : current);
      setCode(""); setSent(false); setMessage("メールアドレスを確認しました。");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "確認できませんでした。"); }
    finally { setBusy(false); }
  }

  return <CharcoalProvider><main>
    <header className="page-header"><span className="brand">Taskly</span><label className="theme-control">テーマ <select data-theme-select aria-label="テーマ" value={theme} onChange={(event) => { const next = event.target.value as typeof theme; setThemePreference(next); setTheme(next); }}><option value="system">自動</option><option value="light">ライト</option><option value="dark">ダーク</option></select></label></header>
    <p className="eyebrow">アカウント</p><h1>アカウント情報</h1>
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{message}</p>
    {profile && <>
      <section><h2>表示名</h2><form onSubmit={save}><TextField id="display-name" label="表示名" showLabel required value={name} onChange={setName} /><Button type="submit" variant="Primary" disabled={busy}>保存</Button></form></section>
      <section><h2>メールアドレスの確認</h2><p className="account-email">{profile.email}</p><p>{profile.email_verified ? "確認済み" : "未確認"}</p>
        {!profile.email_verified && <><Button type="button" onClick={() => void sendCode()} disabled={busy}>{sent ? "コードを再送信" : "確認コードを送信"}</Button>{sent && <form onSubmit={confirm}><TextField id="verification-code" label="確認コード" showLabel required inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" value={code} onChange={setCode} /><Button type="submit" variant="Primary" disabled={busy}>確認する</Button></form>}</>}
      </section>
    </>}
    <nav><a href="/security">認証設定</a><a href="/passkeys">パスキー管理</a></nav>
  </main></CharcoalProvider>;
}
createRoot(document.getElementById("app")!).render(<ProfileApp />);
