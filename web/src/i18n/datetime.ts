import { DEFAULT_LOCALE, isLocale, LOCALE_STORAGE_KEY, type Locale, resolveLocale } from "@/i18n/config";
import { getLocalStorageValue } from "@/lib/local-storage.client";

//用于日期和时间表示的 BCP47 语言区域标签。跟随界面显示语言（locale-store），因此
// 用此函数代替硬编码的“zh-CN”。服务端渲染（无 window）时使用构建默认 locale，
// 客户端则反映用户所选的值。对于在模块加载时只读取一次的 Intl 格式化器也
// 可以安全使用。
const DATE_LOCALE: Record<Locale, string> = {
  en: "en-US",
  zh: "zh-CN",
  es: "es-ES",
  ja: "ja-JP",
};

export function getDateLocale(): string {
  if (typeof window === "undefined") return DATE_LOCALE[DEFAULT_LOCALE];
  const raw = getLocalStorageValue(LOCALE_STORAGE_KEY);
  const loc: Locale = isLocale(raw) ? raw : resolveLocale();
  return DATE_LOCALE[loc];
}
