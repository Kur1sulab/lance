"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { NextIntlClientProvider } from "next-intl";

import enMessages from "../../messages/en.json";
import esMessages from "../../messages/es.json";
import jaMessages from "../../messages/ja.json";
import zhMessages from "../../messages/zh.json";
import type { Locale } from "./config";
import { useActiveLocale } from "./locale-store";

// 将四种语言的消息全部静态打包。这样用户才能在设置页面中
// 切换时无需重新构建、刷新页面或异步加载，所有文案就地切换为所选
// 语言重新渲染（与静态导出 output: "export" 也兼容）。
const MESSAGES: Record<Locale, Record<string, unknown>> = {
  en: enMessages as Record<string, unknown>,
  zh: zhMessages as Record<string, unknown>,
  es: esMessages as Record<string, unknown>,
  ja: jaMessages as Record<string, unknown>,
};

// 执行环境（浏览器、预渲染服务器）的时区。
const TIME_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone;

// 负责运行时语言切换的 Provider。根布局中的 NextIntlClientProvider
// 取而代之，按用户选定的活动 locale（cookie·localStorage）挑选并提供 messages。
export function IntlRuntimeProvider({ children }: Readonly<{ children: ReactNode }>) {
  const locale = useActiveLocale();

  // 语言变化时同步 <html lang>。根布局在服务端注入的初始 lang
  // 用客户端选择值覆盖，使屏幕阅读器和浏览器翻译以正确语言运行。
  React.useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  // 与根布局的 Provider 不同，此处不继承服务端请求配置，因此需要将 timeZone
  // 直接传入。若遗漏，next-intl 会在预渲染期间留下 ENVIRONMENT_FALLBACK 错误。
  return (
    <NextIntlClientProvider locale={locale} messages={MESSAGES[locale]} timeZone={TIME_ZONE}>
      {children}
    </NextIntlClientProvider>
  );
}
