"use client";

import * as React from "react";

import { useTranslations } from "next-intl";

import { DetailField, DetailFields } from "@/components/shell/detail-fields";
import { DetailPanel } from "@/components/shell/detail-panel";
import { DetailEmpty, DetailError, DetailLoading } from "@/components/shell/detail-status";
import { Badge } from "@/components/ui/badge";
import { useDetailRecord } from "@/hooks/use-detail-record";
import { getDateLocale } from "@/i18n/datetime";
import { api } from "@/lib/api";
import type { Asset } from "@/lib/types";

/**
 * 资产详情抽屉（设计文档 §四）。
 *
 * 后端没有单资产 GET，所以「刷新后按 ?id= 还原」走 `/assets?limit=500` 后按 id 匹配；
 * 列表里点开时直接把行对象传进来，省掉这次请求。
 */
async function fetchAssetByID(id: string): Promise<Asset> {
  const { assets } = await api.assets("", 500, 0);
  const found = assets.find((a) => String(a.id) === id);
  if (!found) throw new Error(`asset ${id} not in the first 500 rows`);
  return found;
}

function humanValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function AssetDrawer({
  id,
  open,
  onClose,
  asset,
}: {
  id: string;
  open: boolean;
  onClose: () => void;
  /** 列表里点开时带上行对象；深链还原时留空，由抽屉自己查。 */
  asset?: Asset | null;
}) {
  const t = useTranslations("detailDrawer");

  // 有行对象就不打请求：把 id 传空让 hook 停在空态，读取时以 asset 优先。
  const { data, loading, error, reload } = useDetailRecord(asset ? "" : id, fetchAssetByID);
  const record = asset ?? data;

  const statusCode = record?.status_code;
  const technologies = record?.technologies ?? [];
  const boundDomains = record?.bound_domains ?? [];
  const openPorts = record?.open_ports ?? [];

  const segments = record
    ? [
        {
          key: "summary",
          label: t("summary"),
          content: (
            <div className="space-y-4">
              <DetailFields>
                <DetailField label={t("assetType")}>
                  <Badge variant="outline" className="font-mono text-xs">
                    {record.type}
                  </Badge>
                </DetailField>
                <DetailField label={t("domain")} mono>
                  {record.domain || record.root_domain || "—"}
                </DetailField>
                <DetailField label={t("ip")} mono>
                  {record.ip || "—"}
                </DetailField>
                <DetailField label={t("port")} mono>
                  {record.port ?? "—"}
                </DetailField>
                <DetailField label={t("url")} mono>
                  <span className="break-all">{record.url || "—"}</span>
                </DetailField>
                <DetailField label={t("statusCode")} mono>
                  {statusCode ? (
                    <span className={statusCode >= 400 ? "text-destructive" : undefined}>{statusCode}</span>
                  ) : (
                    "—"
                  )}
                </DetailField>
                <DetailField label={t("pageTitle")}>{record.page_title || "—"}</DetailField>
                <DetailField label={t("company")} mono>
                  {record.company_id ?? "—"}
                </DetailField>
                <DetailField label={t("lastSeen")} mono>
                  {record.last_seen ? new Date(record.last_seen).toLocaleString(getDateLocale()) : "—"}
                </DetailField>
              </DetailFields>

              <div className="space-y-1.5">
                <h3 className="font-medium text-muted-foreground text-xs uppercase tracking-wide">{t("technologies")}</h3>
                {technologies.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {technologies.map((tech) => (
                      <Badge key={tech} variant="secondary" className="text-xs">
                        {tech}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <p className="text-muted-foreground text-xs">{t("none")}</p>
                )}
              </div>
            </div>
          ),
        },
        {
          key: "surface",
          label: t("surface"),
          content: (
            <DetailFields>
              <DetailField label={t("method")} mono>
                {record.method || "—"}
              </DetailField>
              <DetailField label={t("serviceType")} mono>
                {record.service_type || record.service_name || "—"}
              </DetailField>
              <DetailField label={t("bundleId")} mono>
                {record.bundle_id || "—"}
              </DetailField>
              <DetailField label={t("appName")}>{record.app_name || "—"}</DetailField>
              <DetailField label={t("boundDomains")} mono>
                {boundDomains.length > 0 ? boundDomains.join(", ") : "—"}
              </DetailField>
              <DetailField label={t("openPorts")} mono>
                {openPorts.length > 0 ? openPorts.map((p) => `${p.port}${p.service ? `/${p.service}` : ""}`).join(", ") : "—"}
              </DetailField>
              <DetailField label={t("favicon")} mono>
                {record.favicon_mmh3 || "—"}
              </DetailField>
              <DetailField label={t("contentLength")} mono>
                {typeof record.content_length === "number" ? record.content_length : "—"}
              </DetailField>
            </DetailFields>
          ),
        },
        {
          key: "meta",
          label: t("meta"),
          content: (
            <div className="space-y-3">
              <DetailFields>
                <DetailField label={t("assetId")} mono>
                  {record.id}
                </DetailField>
                <DetailField label={t("taskIds")} mono>
                  {record.task_ids && record.task_ids.length > 0 ? record.task_ids.join(", ") : "—"}
                </DetailField>
                <DetailField label={t("taskSource")}>{record.task_source_summary || record.task_source || "—"}</DetailField>
                <DetailField label={t("auth")} mono>
                  {record.auth && record.auth.length > 0 ? `${record.auth.length}` : "—"}
                </DetailField>
              </DetailFields>
              {record.params && record.params.length > 0 ? (
                <pre className="max-h-60 overflow-auto rounded-md bg-muted p-2 font-mono text-xs">
                  {humanValue(record.params)}
                </pre>
              ) : null}
              {record.extra && Object.keys(record.extra).length > 0 ? (
                <pre className="max-h-60 overflow-auto rounded-md bg-muted p-2 font-mono text-xs">
                  {humanValue(record.extra)}
                </pre>
              ) : null}
            </div>
          ),
        },
      ]
    : undefined;

  const title = record?.domain || record?.ip || record?.app_name || record?.url || record?.page_title || t("assetTitle");

  return (
    <DetailPanel
      open={open}
      onClose={onClose}
      modal={false}
      resizable
      storageKey="lance_detail_panel_width"
      closeLabel={t("close")}
      resizeLabel={t("resize")}
      title={title}
      subtitle={record ? `${record.type} · #${record.id}` : undefined}
      segments={segments}
    >
      {loading && !record ? <DetailLoading label={t("loading")} /> : null}
      {error ? <DetailError message={t("loadFailed", { error })} retryLabel={t("retry")} onRetry={reload} /> : null}
      {!loading && !error && !record ? <DetailEmpty message={t("notFound")} /> : null}
    </DetailPanel>
  );
}
