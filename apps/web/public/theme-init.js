(function () {
  var root = document.documentElement;
  var storedTheme;
  var storedLanguage;
  try {
    storedTheme = localStorage.getItem("taskly-auth-theme");
    storedLanguage = localStorage.getItem("display_lang");
  } catch (_) {
    // Browser preference remains available when storage is blocked.
  }
  var systemDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  var dark = storedTheme === "dark" || (storedTheme !== "light" && systemDark);
  root.dataset.theme = dark ? "dark" : "light";
  var preferred = navigator.languages && navigator.languages[0] || navigator.language || "ja";
  root.lang = storedLanguage === "ja" || storedLanguage === "en"
    ? storedLanguage
    : preferred.toLowerCase().startsWith("ja") ? "ja" : "en";

  if (root.lang === "en") {
    document.addEventListener("DOMContentLoaded", function () {
      document.querySelectorAll("[data-i18n-en]").forEach(function (element) {
        element.textContent = element.getAttribute("data-i18n-en");
      });
    }, { once: true });
    var titles = {
      "/login": "Email address",
      "/password": "Password",
      "/mail-code": "Email code",
      "/mfa": "Two-factor authentication",
      "/logout": "Log out",
      "/profile": "Account",
      "/security": "Authentication settings",
      "/passkeys": "Passkeys",
      "/login-history": "Login history"
    };
    var page = location.pathname.replace(/\.html$/, "");
    if (titles[page]) {
      document.addEventListener("DOMContentLoaded", function () {
        if (document.title.includes("Taskly 認証")) document.title = titles[page] + " | Taskly Auth";
      }, { once: true });
    }
  }
})();
