import "./style.css";
import "@charcoal-ui/react/dist/index.css";
import { Button, CharcoalProvider, LoadingSpinner } from "@charcoal-ui/react";
import { createRoot } from "react-dom/client";
import { useEffect, useRef, useState } from "react";
import { AuthHeader } from "./auth-header";
import { request } from "./api";
import { tasklyLoginURL } from "./config";

function LogoutApp() {
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

  return <CharcoalProvider><main>
    <AuthHeader />
    <p className="eyebrow">アカウント</p>
    <h1>{status === "working" ? "ログアウトしています" : "ログアウトできませんでした"}</h1>
    {status === "working" ? <div className="logout-progress" role="status">
      <span aria-hidden="true"><LoadingSpinner size={24} padding={0} /></span>
      <p>完了するとログイン画面に移動します。</p>
    </div> : <>
      <p id="error" className="message error" role="alert">ログアウトを完了できませんでした。再試行してください。</p>
      <div className="actions"><Button id="retry" type="button" variant="Primary" fullWidth onClick={() => void finishLogout()}>再試行</Button></div>
    </>}
  </main></CharcoalProvider>;
}

createRoot(document.getElementById("app")!).render(<LogoutApp />);
