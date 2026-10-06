const preferenceKey = "taskly-auth-theme";
type ThemePreference = "system" | "light" | "dark";

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
  const dark = selected === "dark" || (selected === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
  document.querySelectorAll<HTMLSelectElement>("[data-theme-select]").forEach((select) => { select.value = selected; });
}

export function setThemePreference(value: ThemePreference): void {
  try { localStorage.setItem(preferenceKey, value); } catch { /* Private browsing may block storage. */ }
  applyTheme();
}

applyTheme();
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", applyTheme);
document.querySelectorAll<HTMLSelectElement>("[data-theme-select]").forEach((select) => {
  select.addEventListener("change", () => {
    setThemePreference(select.value as ThemePreference);
  });
});
