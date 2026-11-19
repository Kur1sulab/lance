"use client";

import * as React from "react";

import { usePathname, useRouter, useSearchParams } from "next/navigation";

/**
 * 详情抽屉与 URL 的 `?id=` 双向同步（设计文档 §四）。
 *
 * - 打开：`router.push("?id=<id>")` 写一条历史记录，于是刷新能还原、链接可分享；
 * - 关闭：**上一次打开是本页新增的历史时用 `router.back()`**，把那条记录弹掉，
 *   浏览器后退键与关闭键走同一条路径，不会在历史里留下一个「空抽屉」状态；
 * - 若不是本页打开的（直接打开/刷新带 ?id= 的深链），关闭用 `replace` 抹掉参数——
 *   此时 `back()` 会退出整个页面，属于误伤。
 *
 * 其它查询参数（筛选、分页）原样保留：只增删 `param` 这一个键。
 */
export interface DetailParam {
  /** 当前 URL 上的 ?id=（空串 = 抽屉关闭）。 */
  id: string;
  open: boolean;
  openDetail: (id: string | number) => void;
  closeDetail: () => void;
}

export function useDetailParam(param = "id"): DetailParam {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const current = searchParams.get(param) ?? "";
  const pushedRef = React.useRef(false);

  const hrefWith = React.useCallback(
    (value: string | null) => {
      const params = new URLSearchParams(searchParams.toString());
      if (value === null || value === "") params.delete(param);
      else params.set(param, value);
      const qs = params.toString();
      return qs ? `${pathname}?${qs}` : pathname;
    },
    [param, pathname, searchParams],
  );

  const openDetail = React.useCallback(
    (next: string | number) => {
      const value = String(next);
      if (!value || value === current) return;
      pushedRef.current = true;
      router.push(hrefWith(value), { scroll: false });
    },
    [current, hrefWith, router],
  );

  const closeDetail = React.useCallback(() => {
    if (pushedRef.current) {
      pushedRef.current = false;
      router.back();
      return;
    }
    router.replace(hrefWith(null), { scroll: false });
  }, [hrefWith, router]);

  // 浏览器后退把 ?id= 弹掉时，本页压入的那条历史也一并没了——同步复位标记，
  // 否则下一次关闭会再 back() 一次，多退一页。
  React.useEffect(() => {
    if (!current) pushedRef.current = false;
  }, [current]);

  return { id: current, open: current !== "", openDetail, closeDetail };
}
