import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, LoadingSpinner } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useRef, useState } from "react";
import { AuthHeader } from "./auth-header";
import { AuthFooter } from "./auth-footer";
import { request } from "./api";
import { tasklyLoginURL } from "./config";
import { LocaleRoot, tr, useLocale } from "./i18n";

function LogoutApp() {
  const locale = useLocale();
  const [status, setStatus] = useState<"working" | "failed">("working");
  const pending = useRef(false);

  async function finishLogout(): Promise<void> {
    if (pending.current) return;
    pending.current = true;
    setStatus("working");
    try {
      const response = await request("/auth/logout", {});
      if (!response.ok) throw new Error("logout failed");
      location.replace(tasklyLoginURL);
    } catch {
      setStatus("failed");
    } finally {
      pending.current = false;
    }
  }

  useEffect(() => { void finishLogout(); }, []);
  useEffect(() => {
    document.title = `${status === "working" ? tr("ログアウトしています", "Signing out") : tr("ログアウトできませんでした", "Could not sign out")} | ${tr("Taskly 認証", "Taskly Authentication")}`;
  }, [status, locale]);

  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">{tr("アカウント", "Account")}</p>
    <h1>{status === "working" ? tr("ログアウトしています", "Signing out") : tr("ログアウトできませんでした", "Could not sign out")}</h1>
    {status === "working" ? <div className="logout-progress" role="status" aria-label={tr("ログアウト処理中", "Signing out") }>
      <span aria-hidden="true"><LoadingSpinner size={24} padding={0} /></span>
    </div> : <>
      <p id="error" className="message error" role="alert">{tr("ログアウトを完了できませんでした。再試行してください。", "Sign out failed. Please try again.")}</p>
      <div className="actions"><Button id="retry" type="button" variant="Primary" fullWidth onClick={() => void finishLogout()}>{tr("再試行", "Try again")}</Button></div>
    </>}
    <AuthFooter />
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LocaleRoot component={LogoutApp} />);
