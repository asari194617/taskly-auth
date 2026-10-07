import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { request } from "./api";
import { AccountLayout } from "./account-layout";
import { BusyLabel, LoadingStatus } from "./loading";

interface Profile { email: string; display_name: string }

function ProfileApp() {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  async function load() {
    try {
      const response = await request("/auth/profile");
      if (response.status === 401) { location.assign("/login?next=profile"); return; }
      if (!response.ok) throw new Error("load failed");
      const value = await response.json() as Profile;
      setProfile(value); setName(value.display_name);
    } catch { setError("アカウント情報を読み込めませんでした。"); }
    finally { setLoading(false); }
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

  return <AccountLayout page="profile">
    <h1>アカウント情報</h1>
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{message}</p>
    {loading && <LoadingStatus>アカウント情報を読み込んでいます。</LoadingStatus>}
    {profile && <>
      <section><h2>表示名</h2><form onSubmit={save}><TextField id="display-name" label="表示名" showLabel required value={name} onChange={setName} /><Button type="submit" variant="Primary" disabled={busy}><BusyLabel busy={busy}>保存</BusyLabel></Button></form></section>
      <section><h2>メールアドレス</h2><p className="account-email">{profile.email}</p></section>
    </>}
  </AccountLayout>;
}
createRoot(document.getElementById("app")!).render(<ProfileApp />);
