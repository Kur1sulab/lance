"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpRightIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Markdown } from "@/components/markdown";
import { DetailField, DetailFields } from "@/components/shell/detail-fields";
import { DetailPanel } from "@/components/shell/detail-panel";
import { DetailEmpty, DetailError, DetailLoading } from "@/components/shell/detail-status";
import { StatusBadge } from "@/components/status-badge";
import { useDetailRecord } from "@/hooks/use-detail-record";
import { getDateLocale } from "@/i18n/datetime";
import { api } from "@/lib/api";

function fmt(ts?: string) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(getDateLocale());
}

function fmtDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

/**
 * 任务详情抽屉（设计文档 §四）。取数走 `api.task(id)`，刷新带 ?id= 能自行还原。
 * 完整的时间线/会话/报告仍在整页详情里，"在整页打开" 保留通往旧页面的路。
 */
export function TaskDrawer({ id, open, onClose }: { id: string; open: boolean; onClose: () => void }) {
  const t = useTranslations("detailDrawer");
  const { data, loading, error, reload } = useDetailRecord(id, (taskID) => api.task(taskID));

  const startedUnix = data?.created_unix ?? 0;
  const endedUnix = data?.completed_unix ?? 0;
  const durationSec = startedUnix > 0 ? (endedUnix > 0 ? endedUnix - startedUnix : Math.floor(Date.now() / 1000) - startedUnix) : 0;

  const findings = data?.findings;
  const findingsTotal = findings ? findings.critical + findings.high + findings.medium + findings.low : 0;

  const segments = data
    ? [
        {
          key: "summary",
          label: t("summary"),
          content: (
            <div className="space-y-4">
              <DetailFields>
                <DetailField label={t("status")}>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <StatusBadge domain="task" value={data.status} dot />
                    {/* status 已经是 paused/queued 时不重复挂同义徽标 */}
                    {data.paused && data.status !== "paused" ? <StatusBadge domain="task" value="paused" /> : null}
                    {data.queued && data.status !== "queued" ? <StatusBadge domain="task" value="queued" /> : null}
                  </div>
                </DetailField>
                <DetailField label={t("taskGoal")}>{data.goal || "—"}</DetailField>
                <DetailField label={t("category")}>{data.category_name || "—"}</DetailField>
                <DetailField label={t("severity")}>
                  {findings && findingsTotal > 0 ? (
                    <span className="font-medium tabular-nums">
                      {findings.critical}/{findings.high}/{findings.medium}/{findings.low}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">0</span>
                  )}
                </DetailField>
                <DetailField label={t("goals")}>
                  {typeof data.goals_total === "number" && data.goals_total > 0 ? (
                    <span className="tabular-nums">
                      {data.goals_met ?? 0}/{data.goals_total}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </DetailField>
                <DetailField label={t("createdAt")} mono>
                  {fmt(data.created_at)}
                </DetailField>
                <DetailField label={t("completedAt")} mono>
                  {fmt(data.completed_at)}
                </DetailField>
                <DetailField label={t("duration")} mono>
                  {fmtDuration(durationSec)}
                </DetailField>
              </DetailFields>

              {data.description ? <Markdown text={data.description} /> : null}

              <div className="flex flex-wrap items-center gap-2 border-border border-t pt-3">
                <Link
                  href={`/function/tasks/detail?id=${encodeURIComponent(data.id)}`}
                  className="inline-flex items-center gap-1 text-muted-foreground text-xs hover:text-foreground hover:underline"
                >
                  {t("openFull")}
                  <ArrowUpRightIcon className="size-3.5" />
                </Link>
              </div>
            </div>
          ),
        },
        {
          key: "runtime",
          label: t("runtime"),
          content: (
            <DetailFields>
              <DetailField label={t("engineMode")}>{data.engine_mode || "—"}</DetailField>
              <DetailField label={t("inFlight")} mono>
                {typeof data.in_flight === "number" ? data.in_flight : "—"}
              </DetailField>
              <DetailField label={t("lastActivity")} mono>
                {fmt(data.last_activity)}
              </DetailField>
              <DetailField label={t("timeout")} mono>
                {data.timeout_seconds ? fmtDuration(data.timeout_seconds) : t("unlimited")}
              </DetailField>
              <DetailField label={t("heartbeat")} mono>
                {data.plan_heartbeat_seconds ? fmtDuration(data.plan_heartbeat_seconds) : "—"}
              </DetailField>
              <DetailField label={t("tokens")} mono>
                {data.tokens
                  ? `${data.tokens.input_tokens} in / ${data.tokens.output_tokens} out`
                  : "—"}
              </DetailField>
              <DetailField label={t("stalled")}>{data.stalled ? t("yes") : t("no")}</DetailField>
            </DetailFields>
          ),
        },
        {
          key: "links",
          label: t("links"),
          content: (
            <DetailFields>
              <DetailField label={t("taskId")} mono>
                {data.id}
              </DetailField>
              <DetailField label={t("parentTask")} mono>
                {data.parent_ref ? (
                  <Link
                    href={`/function/tasks/detail?id=${encodeURIComponent(data.parent_ref)}`}
                    className="hover:underline"
                  >
                    {data.parent_ref}
                  </Link>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </DetailField>
              <DetailField label={t("sourceTasks")} mono>
                {data.source_task_ids && data.source_task_ids.length > 0 ? data.source_task_ids.join(", ") : "—"}
              </DetailField>
              <DetailField label={t("companies")} mono>
                {data.company_ids && data.company_ids.length > 0 ? data.company_ids.join(", ") : "—"}
              </DetailField>
              <DetailField label={t("llmProfile")} mono>
                {data.llm_profile_ids && data.llm_profile_ids.length > 0
                  ? data.llm_profile_ids.join(" → ")
                  : (data.llm_profile_id ?? "—")}
              </DetailField>
              <DetailField label={t("coverage")}>{data.coverage_enabled ? t("yes") : t("no")}</DetailField>
            </DetailFields>
          ),
        },
      ]
    : undefined;

  return (
    <DetailPanel
      open={open}
      onClose={onClose}
      modal={false}
      resizable
      storageKey="lance_detail_panel_width"
      closeLabel={t("close")}
      resizeLabel={t("resize")}
      title={data?.name || data?.description || t("taskTitle")}
      subtitle={data ? `${data.id} · ${data.status}` : undefined}
      segments={segments}
    >
      {loading && !data ? <DetailLoading label={t("loading")} /> : null}
      {error ? <DetailError message={t("loadFailed", { error })} retryLabel={t("retry")} onRetry={reload} /> : null}
      {!loading && !error && !data ? <DetailEmpty message={t("notFound")} /> : null}
    </DetailPanel>
  );
}
