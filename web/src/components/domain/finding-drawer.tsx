"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpRightIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { CopyButton } from "@/components/copy-button";
import { FindingRetestPanel } from "@/components/finding-retest-panel";
import { FindingTrafficPanel } from "@/components/finding-traffic-panel";
import { Markdown } from "@/components/markdown";
import { DetailField, DetailFields } from "@/components/shell/detail-fields";
import { DetailPanel } from "@/components/shell/detail-panel";
import { DetailEmpty, DetailError, DetailLoading } from "@/components/shell/detail-status";
import { StatusBadge } from "@/components/status-badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useDetailRecord } from "@/hooks/use-detail-record";
import { getDateLocale } from "@/i18n/datetime";
import { api } from "@/lib/api";
import type { FindingStatus, Severity } from "@/lib/types";

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low"];
const STATUSES: FindingStatus[] = [
  "pending",
  "in_progress",
  "confirmed",
  "resolved",
  "fixed",
  "false_positive",
  "ignored",
  "duplicate",
  "risk_accepted",
];

/**
 * 发现详情抽屉（设计文档 §四）：摘要 / 证据 / 流量 / 复现 四段。
 * 取数按 ?id= 走 `api.getFinding`，所以刷新后的深链能自己还原，不依赖列表里的那一行。
 */
export function FindingDrawer({
  id,
  open,
  onClose,
  contextTask,
  onChanged,
}: {
  id: string;
  open: boolean;
  onClose: () => void;
  contextTask?: string;
  onChanged?: () => void;
}) {
  const t = useTranslations("detailDrawer");
  const tStatus = useTranslations("status");
  const { data, loading, error, reload } = useDetailRecord(id, (findingID) => api.getFinding(findingID, contextTask));

  const handle = data?.finding_id || data?.id || id;

  async function patchSeverity(next: Severity) {
    try {
      await api.setFindingSeverity(handle, next);
      reload();
      onChanged?.();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  async function patchStatus(next: FindingStatus) {
    try {
      await api.setFindingStatus(handle, next);
      reload();
      onChanged?.();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  const segments = data
    ? [
        {
          key: "summary",
          label: t("summary"),
          content: (
            <div className="space-y-4">
              <DetailFields>
                <DetailField label={t("severity")}>
                  <div className="flex flex-wrap items-center gap-2">
                    <StatusBadge domain="severity" value={data.severity} />
                    <Select value={data.severity} onValueChange={(v) => void patchSeverity(v as Severity)}>
                      <SelectTrigger size="sm" className="w-28" aria-label={t("severity")}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {SEVERITIES.map((sv) => (
                          <SelectItem key={sv} value={sv}>
                            {tStatus(`severity.${sv}`)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </DetailField>
                <DetailField label={t("status")}>
                  <div className="flex flex-wrap items-center gap-2">
                    <Select value={data.status} onValueChange={(v) => void patchStatus(v as FindingStatus)}>
                      <SelectTrigger size="sm" className="w-32" aria-label={t("status")}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {STATUSES.map((sv) => (
                          <SelectItem key={sv} value={sv}>
                            {tStatus(`finding.${sv}`)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </DetailField>
                <DetailField label={t("type")}>
                  <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{data.vulnclass || "—"}</code>
                </DetailField>
                <DetailField label={t("task")}>
                  {data.task_id ? (
                    <Link
                      href={`/function/tasks/detail?id=${encodeURIComponent(data.task_id)}`}
                      className="inline-flex items-center gap-1 hover:underline"
                    >
                      <span className="truncate">{data.task_description || data.task_id}</span>
                      <ArrowUpRightIcon className="size-3.5 shrink-0" />
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </DetailField>
                <DetailField label={t("asset")}>
                  {data.assets && data.assets.length > 0 ? (
                    <span className="flex flex-wrap gap-1">
                      {data.assets.map((a) => (
                        <code key={a.id} className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs" title={a.type}>
                          {a.label}
                        </code>
                      ))}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </DetailField>
                <DetailField label={t("createdAt")} mono>
                  {data.ts ? new Date(data.ts).toLocaleString(getDateLocale()) : "—"}
                </DetailField>
              </DetailFields>

              {data.report_stale ? (
                <p className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-1.5 text-amber-800 text-xs dark:text-amber-300">
                  {t("reportStale")}
                </p>
              ) : null}

              {data.summary ? <Markdown text={data.summary} /> : null}

              <div className="flex flex-wrap items-center gap-2 border-border border-t pt-3">
                <Link
                  href={`/function/findings/detail?id=${encodeURIComponent(handle)}`}
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
          key: "evidence",
          label: t("evidence"),
          content: (
            <div className="space-y-2">
              {data.evidence ? (
                <>
                  <div className="flex justify-end">
                    <CopyButton text={data.evidence} />
                  </div>
                  <Markdown text={data.evidence} />
                </>
              ) : (
                <DetailEmpty message={t("noEvidence")} />
              )}
            </div>
          ),
        },
        {
          key: "traffic",
          label: t("traffic"),
          content: <FindingTrafficPanel findingId={handle} contextTask={contextTask} onChanged={reload} />,
        },
        {
          key: "repro",
          label: t("repro"),
          content: data.report ? (
            <div className="space-y-2">
              <div className="flex justify-end">
                <CopyButton text={data.report} />
              </div>
              <Markdown text={data.report} />
            </div>
          ) : (
            <DetailEmpty message={t("noReport")} />
          ),
        },
        {
          key: "retest",
          label: t("retest"),
          content: <FindingRetestPanel findingId={handle} findingName={data.name ?? data.vulnclass} onCompleted={reload} />,
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
      title={data?.name || data?.vulnclass || t("findingTitle")}
      subtitle={data ? `${data.finding_id || data.id}` : undefined}
      segments={segments}
    >
      {loading && !data ? <DetailLoading label={t("loading")} /> : null}
      {error ? (
        <DetailError
          message={t("loadFailed", { error })}
          retryLabel={t("retry")}
          onRetry={reload}
        />
      ) : null}
      {!loading && !error && !data ? <DetailEmpty message={t("notFound")} /> : null}
    </DetailPanel>
  );
}
