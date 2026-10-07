import { CharcoalProvider } from "@charcoal-ui/react";
import type { ReactNode } from "react";
import { AuthHeader } from "./auth-header";
import { tasklyLoginURL } from "./config";

type AccountPage = "profile" | "security" | "passkeys";

const pages: { id: AccountPage; href: string; label: string }[] = [
  { id: "profile", href: "/profile", label: "アカウント情報" },
  { id: "security", href: "/security", label: "認証設定" },
  { id: "passkeys", href: "/passkeys", label: "パスキー" },
];

export function AccountLayout({ page, children }: { page: AccountPage; children: ReactNode }) {
  return <CharcoalProvider>
    <div className="account-page">
      <AuthHeader />
      <div className="account-layout">
        <nav className="account-menu" aria-label="アカウントメニュー">
          <p className="account-menu-heading">アカウント</p>
          {pages.map((item) => <a key={item.id} href={item.href} className="account-menu-link" aria-current={page === item.id ? "page" : undefined}>{item.label}</a>)}
          <a className="account-menu-back" href={new URL(tasklyLoginURL).origin}>Taskly に戻る</a>
        </nav>
        <main className="account-content">{children}</main>
      </div>
    </div>
  </CharcoalProvider>;
}
