"use client";

import * as React from "react";

/**
 * 抽屉取数：id 变化就重取，id 为空清空，卸载/切 id 时丢弃过期响应。
 *
 * 抽屉是「按 id 恢复」的入口（刷新带 ?id= 要能还原），所以取数必须由 id 驱动，
 * 不能只靠列表里点来的那一行对象——那一行在刷新后就没了。
 */
export interface DetailRecord<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

export function useDetailRecord<T>(
  id: string,
  fetcher: (id: string) => Promise<T>,
): DetailRecord<T> {
  const fetcherRef = React.useRef(fetcher);
  fetcherRef.current = fetcher;

  const [data, setData] = React.useState<T | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [nonce, setNonce] = React.useState(0);

  React.useEffect(() => {
    if (!id) {
      setData(null);
      setError(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetcherRef
      .current(id)
      .then((record) => {
        if (!cancelled) setData(record);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setData(null);
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [id, nonce]);

  const reload = React.useCallback(() => setNonce((n) => n + 1), []);

  return { data, loading, error, reload };
}
