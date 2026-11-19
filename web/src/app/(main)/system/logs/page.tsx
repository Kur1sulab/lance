"use client";

import * as React from "react";

import { useTranslations } from "next-intl";

import { StateBoundary } from "@/components/data/state-boundary";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { usePermissionDenied } from "@/hooks/use-permission-denied";
import { ApiError, sseUrl } from "@/lib/api";
import { errorCodeOf } from "@/lib/api-error";
import { MOCK } from "@/lib/mock/enabled";
import type { LogLine } from "@/lib/types";
import { cn } from "@/lib/utils";

// Mock 演示：因为后端没有 SSE，先填充几行示例日志。
const MOCK_LOGS: LogLine[] = [
  {
    seq: 1,
    ts: "2026-07-26T03:55:00Z",
    level: "info",
    tag: "engine",
    text: "Lance v0.1.0 backend listening on :8787 (workers=3)",
  },
  {
    seq: 2,
    ts: "2026-07-26T03:55:01Z",
    level: "info",
    tag: "config",
    text: "LLM configured from DB: anthropic / claude-opus-4-8",
  },
  {
    seq: 3,
    ts: "2026-07-26T03:56:10Z",
    level: "info",
    tag: "planner",
    text: "task t-acme-web：计划 3 轮，生成意图 i-4",
  },
  {
    seq: 4,
    ts: "2026-07-26T03:57:00Z",
    level: "warn",
    tag: "guard",
    text: "block bash: 越界，out.evil.example 超出 scope",
  },
  {
    seq: 5,
    ts: "2026-07-26T03:57:30Z",
    level: "info",
    tag: "work#1",
    text: "report_finding: Default Credentials (high) 保存完成",
  },
  {
    seq: 6,
    ts: "2026-07-26T03:58:20Z",
    level: "error",
    tag: "work#3",
    text: "intercept: mysqldump 破坏性规则命中，等待人工批准",
  },
];

// 只用语义 token：warn 走 muted，error 走 destructive（不写死色值）。
const levelTone: Record<LogLine["level"], string> = {
  info: "text-foreground/80",
  warn: "text-muted-foreground",
  error: "text-destructive",
};
const levelDot: Record<LogLine["level"], string> = {
  info: "bg-muted-foreground/40",
  warn: "bg-muted-foreground",
  error: "bg-destructive",
};

function fmtTime(ts: string) {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export default function LogsPage() {
  const t = useTranslations("logsPage");
  const tn = useTranslations("nav");
  const tp = useTranslations("pageStates");
  const perm = usePermissionDenied();

  const [lines, setLines] = React.useState<LogLine[]>([]);
  const [q, setQ] = React.useState("");
  const [level, setLevel] = React.useState<"all" | LogLine["level"]>("all");
  const [paused, setPaused] = React.useState(false);
  const [loadingHistory, setLoadingHistory] = React.useState(false);
  const [loaded, setLoaded] = React.useState(MOCK);
  const [error, setError] = React.useState<string | null>(null);
  const [hasMore, setHasMore] = React.useState(false);
  const lastError = React.useRef<unknown>(null);
  const bottom = React.useRef<HTMLDivElement>(null);
  const stick = React.useRef(true);
  const pausedRef = React.useRef(false);
  pausedRef.current = paused;

  // Minimum db_id seen — used as the cursor for loading older history.
  const minDbId = React.useMemo(() => {
    let min = 0;
    for (const l of lines) {
      if (l.db_id && (min === 0 || l.db_id < min)) min = l.db_id;
    }
    return min;
  }, [lines]);

  // Live tail via SSE. on connect, replays whatever is in the ring (includes
  // DB-restored history from the last 100 rows written before restart).
  React.useEffect(() => {
    if (MOCK) {
      setLines(MOCK_LOGS);
      return;
    }
    const es = new EventSource(sseUrl("/api/logs/stream?since=0"));
    es.onopen = () => setLoaded(true);
    es.onmessage = (e) => {
      setLoaded(true);
      if (pausedRef.current) return;
      try {
        const l = JSON.parse(e.data) as LogLine;
        setLines((prev) => (prev.some((x) => x.seq === l.seq) ? prev : [...prev, l].slice(-3000)));
      } catch {
        /* ignore malformed frame */
      }
    };
    return () => es.close();
  }, []);

  // Mark "has more" once we have any db_id in view.
  React.useEffect(() => {
    if (minDbId > 1) setHasMore(true);
  }, [minDbId]);

  async function loadOlderHistory() {
    if (loadingHistory) return;
    setLoadingHistory(true);
    try {
      const params = minDbId > 0 ? `?before=${minDbId}&limit=200` : `?limit=200`;
      const res = await fetch(`/api/logs/history${params}`);
      if (!res.ok) {
        // 原来这里静默 return，用户只看到「点了没反应」——错误态就是要把它说出来。
        const err = new ApiError(`GET /logs/history: ${res.status}`, res.status);
        lastError.current = err;
        perm.check(err);
        setError(err.message);
        return;
      }
      const data = (await res.json()) as { items: LogLine[]; has_more: boolean };
      if (data.items?.length) {
        // Assign synthetic seq numbers below current minimum to keep dedup working.
        setLines((prev) => {
          const minSeq = prev.reduce((m, l) => Math.min(m, l.seq ?? 0), 0);
          const older = data.items.map((l, i) => ({
            ...l,
            seq: minSeq - data.items.length + i,
          }));
          // Prepend, dedup by db_id, cap at 5000.
          const merged = [...older, ...prev];
          const seen = new Set<number>();
          const deduped = merged.filter((l) => {
            if (!l.db_id) return true;
            if (seen.has(l.db_id)) return false;
            seen.add(l.db_id);
            return true;
          });
          return deduped.slice(-5000);
        });
      }
      setHasMore(data.has_more ?? false);
    } finally {
      setLoadingHistory(false);
    }
  }

  const filtered = React.useMemo(() => {
    const needle = q.trim().toLowerCase();
    return lines.filter(
      (l) =>
        (level === "all" || l.level === level) &&
        (!needle || l.text.toLowerCase().includes(needle) || l.tag.toLowerCase().includes(needle)),
    );
  }, [lines, q, level]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: filtered changes intentionally re-run the auto-scroll to the newest line.
  React.useEffect(() => {
    if (stick.current && !paused) bottom.current?.scrollIntoView();
  }, [filtered, paused]);

  const counts = React.useMemo(() => {
    let warn = 0,
      error = 0;
    for (const l of lines) {
      if (l.level === "warn") warn++;
      else if (l.level === "error") error++;
    }
    return { warn, error, total: lines.length };
  }, [lines]);

  return (
    <PageShell
      title={t("title")}
      description={t("subtitle")}
      breadcrumb={[{ label: tn("group.govern"), href: "/dashboard" }, { label: tn("item.logs") }]}
      toolbar={
        <Toolbar
          search={
            <Input
              placeholder={t("filterPlaceholder")}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              className="h-8"
            />
          }
          filters={
            <>
              <div className="flex gap-1">
                {(["all", "info", "warn", "error"] as const).map((lv) => (
                  <Button
                    key={lv}
                    size="sm"
                    variant={level === lv ? "default" : "outline"}
                    className="h-8"
                    onClick={() => setLevel(lv)}
                  >
                    {lv === "all" ? t("all") : lv}
                  </Button>
                ))}
              </div>
              <Button
                size="sm"
                variant={paused ? "default" : "outline"}
                className="h-8"
                onClick={() => setPaused((p) => !p)}
              >
                {paused ? t("paused") : t("pause")}
              </Button>
              <Button size="sm" variant="outline" className="h-8" onClick={() => setLines([])}>
                {t("clear")}
              </Button>
            </>
          }
          viewSwitch={
            <span className="text-xs text-muted-foreground">
              {t("totalLines", { count: counts.total })} ·{" "}
              <span className="text-muted-foreground">{t("warnCount", { count: counts.warn })}</span> ·{" "}
              <span className="text-destructive">{t("errorCount", { count: counts.error })}</span>
            </span>
          }
        />
      }
    >
      <div className="px-4 py-3 md:px-6">
        <StateBoundary
          loading={!loaded}
          error={
            error
              ? {
                  title: tp("errorTitle"),
                  detail: tp("errorDetail"),
                  code: errorCodeOf(lastError.current),
                  codeLabel: tp("errorCodeLabel"),
                  retryLabel: tp("retry"),
                }
              : null
          }
          permission={perm.denied ? "logs:read" : null}
          permissionProps={{
            title: tp("permissionTitle"),
            detail: tp("permissionDetail", { name: tn("item.logs"), ask: tp("askAdmin") }),
            missingLabel: tp("permissionMissing"),
            askLabel: tp("permissionAsk"),
            ask: tp("askAdmin"),
          }}
          empty={filtered.length === 0}
          emptyProps={{
            title: tp("emptyTitle"),
            reason: tp("empty.logs"),
          }}
          skeleton={
            <div className="space-y-1.5 rounded-lg border bg-card p-3">
              {Array.from({ length: 12 }).map((_, i) => (
                <div
                  // biome-ignore lint/suspicious/noArrayIndexKey: 骨架屏占位，无真实数据标识。
                  key={i}
                  className="h-3.5 animate-pulse rounded-sm bg-muted"
                  style={{ width: `${55 + ((i * 7) % 40)}%` }}
                />
              ))}
            </div>
          }
          onRetry={() => {
            setError(null);
            lastError.current = null;
            perm.reset();
            void loadOlderHistory();
          }}
        >
          <div
            onScroll={(e) => {
              const el = e.currentTarget;
              stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
            }}
            className="h-[calc(100vh-16rem)] overflow-auto rounded-lg border bg-card p-2 font-mono text-xs leading-relaxed"
          >
            {hasMore && (
              <div className="flex justify-center py-2">
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 text-xs"
                  disabled={loadingHistory}
                  onClick={loadOlderHistory}
                >
                  {loadingHistory ? t("loading") : t("loadOlder")}
                </Button>
              </div>
            )}
            {filtered.map((l) => {
              const body =
                l.tag && l.text.startsWith("[" + l.tag + "]")
                  ? l.text.slice(l.tag.length + 2).trimStart()
                  : l.text;
              return (
                <div key={l.seq} className="flex items-start gap-2 px-1 py-0.5 hover:bg-muted/40">
                  <span className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", levelDot[l.level])} />
                  <span className="shrink-0 tabular-nums text-muted-foreground">{fmtTime(l.ts)}</span>
                  {l.tag && (
                    <span className="shrink-0 rounded bg-muted px-1 text-[10px] text-foreground/70">
                      {l.tag}
                    </span>
                  )}
                  <span className={cn("min-w-0 whitespace-pre-wrap break-words", levelTone[l.level])}>
                    {body}
                  </span>
                </div>
              );
            })}
            <div ref={bottom} />
          </div>
        </StateBoundary>
      </div>
    </PageShell>
  );
}
