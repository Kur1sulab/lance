"use client";

import * as React from "react";

import { isPermissionError } from "@/lib/api-error";

/**
 * 页面级「无权限」标记。
 *
 * 用法：在 fetch 的 catch 里调 `check(e)`，渲染时把 `denied ? "<permission>:read" : null`
 * 传给 StateBoundary 的 `permission`。
 *
 * 之所以用布尔而不是把 error 换成对象：现有页面都用 string | null 存错误文案，
 * 改动面越小越好——这里只多记一个「这条错误是 403」的判定。
 */
export interface PermissionDeniedState {
  denied: boolean;
  check: (error: unknown) => boolean;
  reset: () => void;
}

export function usePermissionDenied(): PermissionDeniedState {
  const [denied, setDenied] = React.useState(false);

  const check = React.useCallback((error: unknown) => {
    const hit = isPermissionError(error);
    if (hit) setDenied(true);
    return hit;
  }, []);

  const reset = React.useCallback(() => setDenied(false), []);

  return { denied, check, reset };
}
