const preferenceKey = "taskly-auth-theme";
type ThemePreference = "system" | "light" | "dark";

function preference(): ThemePreference {
  try {
    const value = localStorage.getItem(preferenceKey);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

function applyTheme(): void {
  const selected = preference();
  const dark = selected === "dark" || (selected === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
  document.querySelectorAll<HTMLSelectElement>("[data-theme-select]").forEach((select) => { select.value = selected; });
}

applyTheme();
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", applyTheme);
document.querySelectorAll<HTMLSelectElement>("[data-theme-select]").forEach((select) => {
  select.addEventListener("change", () => {
    try { localStorage.setItem(preferenceKey, select.value); } catch { /* Private browsing may block storage. */ }
    applyTheme();
  });
});
