import { getRequestConfig } from "next-intl/server";

import { type Locale, resolveLocale } from "./config";

// 按 locale 的消息加载器。使用显式 import 映射，避免打包器把 messages/ 目录整体
// 避免将其引入 context 模块（仅本地存在的 zh.sources.json 除外）。
const loaders: Record<Locale, () => Promise<{ default: Record<string, unknown> }>> = {
  en: () => import("../../messages/en.json"),
  zh: () => import("../../messages/zh.json"),
  es: () => import("../../messages/es.json"),
  ja: () => import("../../messages/ja.json"),
};

// next-intl 请求配置。由于该配置不使用 i18n 路径路由（段·中间件），
// 在这里直接指定 locale。由于不引用 requestLocale，因此动态渲染
// 不会触发。此设置只决定服务端组件·预渲染的默认语言，运行时的
// 用户切换由客户端的 IntlRuntimeProvider 负责（messages/en·zh·es·ja）。
export default getRequestConfig(async () => {
  const locale = resolveLocale();
  const messages = (await loaders[locale]()).default;
  return { locale, messages };
});
