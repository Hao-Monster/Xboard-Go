export type AdminLocale = "zh-CN" | "en-US" | "ru-RU";
let phrases: Readonly<Record<string, string>> = {};

// Only explicit interface copy is passed here. Names, form values and API values
// must remain in their original language, even when they match a translated word.
export function translateAdmin(text: string): string {
  const trimmed = text.trim();
  const translated = phrases[trimmed];
  return translated === undefined ? text : text.replace(trimmed, () => translated);
}

export async function loadAdminLocale(locale: AdminLocale): Promise<Readonly<Record<string, string>>> {
  if (locale === "zh-CN") return {};
  return locale === "en-US" ? (await import("./admin-locales/en-US.json")).default : (await import("./admin-locales/ru-RU.json")).default;
}

export function applyAdminLocale(next: Readonly<Record<string, string>>) { phrases = next; }

export function savedAdminLocale(): AdminLocale {
  try {
    const value = localStorage.getItem("xboard-admin-locale");
    return value === "en-US" || value === "ru-RU" ? value : "zh-CN";
  } catch { return "zh-CN"; }
}
