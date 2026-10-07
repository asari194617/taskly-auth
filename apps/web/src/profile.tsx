import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, TextField } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useState, type FormEvent } from "react";
import { request } from "./api";
import { AccountLayout } from "./account-layout";
import { LocaleRoot, tr, useLocale } from "./i18n";
import { BusyLabel, LoadingStatus } from "./loading";

interface Profile { email: string; display_name: string }

function ProfileApp() {
  const locale = useLocale();
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
    } catch { setError(tr("アカウント情報を読み込めませんでした。", "Could not load account information.")); }
    finally { setLoading(false); }
  }
  useEffect(() => {
    void load();
  }, []);
  useEffect(() => {
    document.title = `${tr("アカウント情報", "Account information")} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [locale]);

  async function save(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      const response = await request("/auth/profile", { display_name: name });
      if (response.status === 401) { location.assign("/login?next=profile"); return; }
      if (!response.ok) throw new Error(response.status === 503
        ? tr("設定を保存しましたが、Taskly への反映を確認できません。もう一度保存してください。", "Your changes were saved, but we could not confirm they reached Taskly. Please save again.")
        : tr("表示名を保存できませんでした。", "Could not save your display name."));
      setMessage(tr("表示名を保存しました。", "Display name saved."));
      setProfile((current) => current ? { ...current, display_name: name.trim() } : current);
    } catch (cause) { setError(cause instanceof Error && !(cause instanceof TypeError) ? cause.message : tr("表示名を保存できませんでした。", "Could not save your display name.")); }
    finally { setBusy(false); }
  }

  return <AccountLayout page="profile">
    <h1>{tr("アカウント情報", "Account information")}</h1>
    <p className="message error" role="alert">{error}</p><p className="message" role="status">{message}</p>
    {loading && <LoadingStatus>{tr("アカウント情報を読み込んでいます。", "Loading account information")}</LoadingStatus>}
    {profile && <>
      <section><h2>{tr("表示名", "Display name")}</h2><form onSubmit={save}><TextField id="display-name" label={tr("表示名", "Display name")} showLabel required value={name} onChange={setName} /><Button type="submit" variant="Primary" disabled={busy}><BusyLabel busy={busy}>{tr("保存", "Save")}</BusyLabel></Button></form></section>
      <section><h2>{tr("メールアドレス", "Email address")}</h2><p className="account-email">{profile.email}</p></section>
    </>}
  </AccountLayout>;
}
createRoot(document.getElementById("app")!).render(<LocaleRoot component={ProfileApp} />);
