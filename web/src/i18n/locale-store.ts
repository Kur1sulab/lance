"use client";

import * as React from "react";

import { setClientCookie } from "@/lib/cookie.client";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

import { isLocale, LOCALE_COOKIE, LOCALE_STORAGE_KEY, type Locale, resolveLocale } from "./config";

// 用户选择的界面显示语言是纯前端配置，因此不保存到服务器，而
// 只保存在 localStorage（和 cookie）中。不与账号同步，因此更换浏览器或站点
// 清除数据后回到默认语言。agent 输出语言另行保存在服务器设置中。

function parseLocale(raw: string | null): Locale {
  return isLocale(raw) ? raw : resolveLocale();
}

//同一标签页内的订阅者集合。storage 事件只在“其他”标签页中触发，因此在当前标签页中
// 切换语言后需通过 emit 通知同一标签页的订阅者，才会立即重新渲染。
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

function emit() {
  for (const listener of listeners) listener();
}

function getSnapshot(): Locale {
  return parseLocale(getLocalStorageValue(LOCALE_STORAGE_KEY));
}

// 服务器没有 localStorage，因此先渲染构建时的默认 locale，hydrate 后
// getSnapshot 用用户选定的值纠正。
function getServerSnapshot(): Locale {
  return resolveLocale();
}

// 订阅当前显示语言的 hook。语言变化时，使用该 hook 的组件会重新渲染。
export function useActiveLocale(): Locale {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

// 切换显示语言。写入 Cookie 与 localStorage，并通知同一标签页的订阅者。保留一年，
// 使下次访问仍保留该选择。
export function setActiveLocale(locale: Locale) {
  if (!isLocale(locale)) return;
  setLocalStorageValue(LOCALE_STORAGE_KEY, locale);
  setClientCookie(LOCALE_COOKIE, locale, 365);
  emit();
}
