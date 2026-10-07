const preferenceKey = "taskly-auth-theme";
export type ThemePreference = "system" | "light" | "dark";

export function getThemePreference(): ThemePreference {
  try {
    const value = localStorage.getItem(preferenceKey);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

function applyTheme(): void {
  const selected = getThemePreference();
  const dark = selected === "dark" || (selected === "system" && window.matchMedia?.("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export function setThemePreference(value: ThemePreference): void {
  try { localStorage.setItem(preferenceKey, value); } catch { /* Private browsing may block storage. */ }
  applyTheme();
}

applyTheme();
window.matchMedia?.("(prefers-color-scheme: dark)").addEventListener?.("change", applyTheme);
