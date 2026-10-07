import { CharcoalProvider, SegmentedControl } from "@charcoal-ui/react";
import { useEffect, useRef, type ReactNode } from "react";
import { AuthHeader } from "./auth-header";
import { tasklyLoginURL } from "./config";

type AccountPage = "profile" | "security" | "login-history";

const pages: { id: AccountPage; href: string; label: string }[] = [
  { id: "profile", href: "/profile", label: "アカウント" },
  { id: "security", href: "/security", label: "認証設定" },
  { id: "login-history", href: "/login-history", label: "ログイン履歴" },
];

export function AccountLayout({ page, children }: { page: AccountPage; children: ReactNode }) {
  const segmentsRef = useRef<HTMLDivElement>(null);
  const keyboardNavigation = useRef(false);

  useEffect(() => {
    try {
      if (sessionStorage.getItem("account-menu-keyboard-target") !== page) return;
      sessionStorage.removeItem("account-menu-keyboard-target");
      segmentsRef.current?.querySelector<HTMLInputElement>('input[type="radio"]:checked')?.focus();
    } catch { /* Navigation still works when session storage is unavailable. */ }
  }, [page]);

  function changePage(value: string) {
    const destination = pages.find((item) => item.id === value);
    if (!destination || destination.id === page) return;
    if (keyboardNavigation.current) {
      try { sessionStorage.setItem("account-menu-keyboard-target", destination.id); } catch { /* Navigation still works. */ }
    }
    location.assign(destination.href);
  }

  return <CharcoalProvider>
    <div className="account-page">
      <AuthHeader />
      <div className="account-layout">
        <nav className="account-menu" aria-label="アカウントメニュー">
          <div
            ref={segmentsRef}
            className="account-menu-segments"
            onKeyDownCapture={(event) => { keyboardNavigation.current = event.key.startsWith("Arrow") || event.key === " " || event.key === "Enter"; }}
            onPointerDownCapture={() => { keyboardNavigation.current = false; }}
          >
            <SegmentedControl
              name="アカウントのページ"
              className="account-menu-control"
              value={page}
              data={pages.map((item) => ({ value: item.id, label: item.label }))}
              fullWidth
              onChange={changePage}
            />
          </div>
          <a className="account-menu-back" href={new URL(tasklyLoginURL).origin}>Taskly に戻る</a>
        </nav>
        <main className="account-content">{children}</main>
      </div>
    </div>
  </CharcoalProvider>;
}
