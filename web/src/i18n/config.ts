// 支持的 locale 与默认值。默认语言为简体中文，另提供英语、西班牙语、日语，
// 均可随用户在设置界面切换。
export const LOCALES = ["en", "zh", "es", "ja"] as const;
export type Locale = (typeof LOCALES)[number];

// 默认显示语言是中文。英语、西班牙语、日语通过设置切换。构建时
// 可被 NEXT_PUBLIC_LOCALE 覆盖；运行时用户在设置界面选定的值（cookie、localStorage）拥有最终决定权。
export const DEFAULT_LOCALE: Locale = "zh";

// 语言选择器中显示的每个 locale 的本语言名称。无论以何种语言查看，都显示自己的语言
// 名称应按该语言读音，因此不作为翻译对象，保持固定写法。
export const LOCALE_LABELS: Record<Locale, string> = {
  en: "English",
  zh: "中文",
  es: "Español",
  ja: "日本語",
};

// 用于保存用户所选显示语言的键。界面读取 localStorage 值。Cookie 中也相同
//写入该值，但目前没有地方读取（保留以备服务器判断语言之需）。
export const LOCALE_COOKIE = "lance_locale";
export const LOCALE_STORAGE_KEY = "lance_locale";

// 将任意字符串收窄为受支持 locale 的守卫。
export function isLocale(raw: unknown): raw is Locale {
  return typeof raw === "string" && (LOCALES as readonly string[]).includes(raw);
}

// 在构建时确定活动 locale。与静态导出（next.config 的 output: "export"）
// 为保持兼容，不使用 cookies()·headers() 等动态 API，只读取环境变量。
// 若 NEXT_PUBLIC_LOCALE 为空或不在支持列表中，则回落到默认值。该值在服务端
// 仅用于预渲染的初始 HTML，实际用户显示语言由客户端通过 Cookie 覆盖。
export function resolveLocale(): Locale {
  const raw = process.env.NEXT_PUBLIC_LOCALE;
  return isLocale(raw) ? raw : DEFAULT_LOCALE;
}
