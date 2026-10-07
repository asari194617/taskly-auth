import { CharcoalProvider, SegmentedControl } from "@charcoal-ui/react";
import { useEffect, useRef, type ReactNode } from "react";
import { AuthFooter } from "./auth-footer";
import { AuthHeader } from "./auth-header";
import { tr } from "./i18n";

type AccountPage = "profile" | "security" | "login-history";

const pages: { id: AccountPage; href: string; labelJa: string; labelEn: string }[] = [
  { id: "profile", href: "/profile", labelJa: "アカウント", labelEn: "Account" },
  { id: "security", href: "/security", labelJa: "認証設定", labelEn: "Authentication" },
  { id: "login-history", href: "/login-history", labelJa: "ログイン履歴", labelEn: "Login history" },
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
        <nav className="account-menu" aria-label={tr("アカウントメニュー", "Account menu")}>
          <div
            ref={segmentsRef}
            className="account-menu-segments"
            onKeyDownCapture={(event) => { keyboardNavigation.current = event.key.startsWith("Arrow") || event.key === " " || event.key === "Enter"; }}
            onPointerDownCapture={() => { keyboardNavigation.current = false; }}
          >
            <SegmentedControl
              name={tr("アカウントのページ", "Account pages")}
              className="account-menu-control"
              value={page}
              data={pages.map((item) => ({ value: item.id, label: tr(item.labelJa, item.labelEn) }))}
              fullWidth
              onChange={changePage}
            />
          </div>
        </nav>
        <main className="account-content">{children}</main>
      </div>
      <AuthFooter showReturnLink />
    </div>
  </CharcoalProvider>;
}
