import { english } from "./en.js";

export function supportedLocale(value) {
  const language = String(value || "").toLowerCase();
  if (language === "zh" || language.startsWith("zh-")) return "zh-CN";
  if (language === "en" || language.startsWith("en-")) return "en";
  return null;
}
export function resolveLocale(preference, languages = []) {
  return supportedLocale(preference) || languages.map(supportedLocale).find(Boolean) || "en";
}
const browser = typeof document !== "undefined";
const storageKey = browser ? "xops.locale:" + new URL(".", document.baseURI).href : "xops.locale";
let preference = browser ? new URL(location.href).searchParams.get("lang") : null;
try { preference = supportedLocale(preference) || globalThis.localStorage?.getItem(storageKey); }
catch { /* The URL and browser language still work when storage is unavailable. */ }
export const locale = resolveLocale(preference, globalThis.navigator?.languages || [globalThis.navigator?.language]);
export function saveLocale(value) {
  const next = supportedLocale(value);
  if (!next) throw new Error("unsupported locale");
  try { globalThis.localStorage?.setItem(storageKey, next); }
  catch { /* The language URL also persists the choice for this tab. */ }
  return next;
}
export function translate(language, message, values = {}) {
  const template = language === "en" ? english[message] ?? message : message;
  return template.replace(/\{(\w+)\}/g, (match, key) => Object.hasOwn(values, key) ? String(values[key]) : match);
}
export function t(message, values) { return translate(locale, message, values); }
if (browser) {
  document.documentElement.lang = locale;
  document.title = "XOps · " + t("控制台");
}
