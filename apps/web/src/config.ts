export const apiBase = (import.meta.env.VITE_AUTH_API_URL || "http://localhost:3067").replace(/\/$/, "");
export const tasklyLoginURL = import.meta.env.DEV
  ? "http://localhost:4321/login"
  : "https://taskly.iy0306.com/login";

export function configureTasklyLinks(): void {
  document.querySelectorAll<HTMLAnchorElement>("[data-taskly-login]").forEach((link) => {
    link.href = tasklyLoginURL;
  });
}
