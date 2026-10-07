import { createElement, useSyncExternalStore, type ComponentType } from "react";

export type Locale = "ja" | "en";

const storageKey = "display_lang";
let selectedLocale: Locale | null = null;
const listeners = new Set<() => void>();

export function getLocale(): Locale {
  if (selectedLocale) return selectedLocale;
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored === "ja" || stored === "en") return stored;
  } catch { /* Language still follows the browser when storage is unavailable. */ }
  try {
    const preferred = navigator.languages?.[0] || navigator.language;
    return preferred?.toLowerCase().startsWith("ja") ? "ja" : "en";
  } catch {
    return "ja";
  }
}

export function tr(ja: string, en: string): string {
  return getLocale() === "ja" ? ja : en;
}

export function setLocale(locale: Locale): void {
  if (locale !== "ja" && locale !== "en") return;
  if (getLocale() === locale) return;
  selectedLocale = locale;
  try { localStorage.setItem(storageKey, locale); } catch { /* Private browsing may block storage. */ }
  document.documentElement.lang = locale;
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useLocale(): Locale {
  return useSyncExternalStore(subscribe, getLocale, () => "ja");
}

export function LocaleRoot({ component: Component }: { component: ComponentType }) {
  useLocale();
  return createElement(Component);
}
