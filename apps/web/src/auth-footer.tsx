import { DropdownMenuItem, DropdownSelector } from "@charcoal-ui/react";
import { useState } from "react";
import { tasklyLoginURL } from "./config";
import { setLocale, tr, useLocale, type Locale } from "./i18n";
import { getThemePreference, setThemePreference, type ThemePreference } from "./theme";

export function AuthFooter({ showReturnLink = false }: { showReturnLink?: boolean }) {
  const locale = useLocale();
  const [theme, setTheme] = useState(getThemePreference);
  const themeName = {
    system: tr("自動", "System"),
    light: tr("ライト", "Light"),
    dark: tr("ダーク", "Dark"),
  }[theme];

  function changeLanguage(value: string) {
    if (value === "ja" || value === "en") setLocale(value as Locale);
  }

  function changeTheme(value: string) {
    if (value !== "system" && value !== "light" && value !== "dark") return;
    setThemePreference(value as ThemePreference);
    setTheme(value as ThemePreference);
  }

  return <footer className="auth-footer">
    {showReturnLink && <a className="auth-footer-return" href={new URL(tasklyLoginURL).origin}>
      {tr("Taskly に戻る", "Back to Taskly")}
    </a>}
    <div className="auth-footer-controls">
      <div className="auth-footer-control">
        <span aria-hidden="true">{tr("表示言語", "Language")}</span>
        <DropdownSelector
          label={tr("表示言語", "Language")}
          value={locale}
          assistiveText={tr("選択中の言語", "Selected language")}
          onChange={changeLanguage}
        >
          <DropdownMenuItem value="ja">{tr("日本語", "Japanese")}</DropdownMenuItem>
          <DropdownMenuItem value="en">English</DropdownMenuItem>
        </DropdownSelector>
      </div>
      <div className="auth-footer-control">
        <span aria-hidden="true">{tr("テーマ", "Theme")}</span>
        <DropdownSelector
          label={tr("テーマ", "Theme")}
          value={theme}
          assistiveText={`${tr("選択中", "Selected")}: ${themeName}`}
          onChange={changeTheme}
        >
          <DropdownMenuItem value="system">{tr("自動", "System")}</DropdownMenuItem>
          <DropdownMenuItem value="light">{tr("ライト", "Light")}</DropdownMenuItem>
          <DropdownMenuItem value="dark">{tr("ダーク", "Dark")}</DropdownMenuItem>
        </DropdownSelector>
      </div>
    </div>
  </footer>;
}
