"use client";

import * as React from "react";

import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { ApprovalDetail } from "@/components/approval-records";
import { DetailPanel } from "@/components/shell/detail-panel";
import { DetailEmpty, DetailError, DetailLoading } from "@/components/shell/detail-status";
import { useDetailRecord } from "@/hooks/use-detail-record";
import { api } from "@/lib/api";

/**
 * 审批详情抽屉（设计文档 §四）。
 * 内容复用审批模块自己的 `ApprovalDetail`（审计链、参数、判定按钮都在里面），
 * 这里只负责「按 ?id= 取行 + 抽屉壳 + 决议后刷新」。
 */
export function ApprovalDrawer({
  id,
  open,
  onClose,
  onChanged,
}: {
  id: string;
  open: boolean;
  onClose: () => void;
  onChanged?: () => void;
}) {
  const t = useTranslations("detailDrawer");
  const numericID = Number(id);
  const valid = Number.isFinite(numericID) && numericID > 0;

  const { data, loading, error, reload } = useDetailRecord(valid ? id : "", (rowID) => api.interceptDetail(Number(rowID)));
  const [busy, setBusy] = React.useState(false);
  const [revision, setRevision] = React.useState(0);

  const decide = React.useCallback(
    async (rowID: number, decision: "allowed" | "denied") => {
      setBusy(true);
      try {
        await api.interceptDecide(rowID, decision);
        setRevision((n) => n + 1);
        reload();
        onChanged?.();
      } catch (err) {
        toast.error(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [onChanged, reload],
  );

  return (
    <DetailPanel
      open={open}
      onClose={onClose}
      modal={false}
      resizable
      storageKey="lance_detail_panel_width"
      closeLabel={t("close")}
      resizeLabel={t("resize")}
      title={data ? `${data.tool_name}` : t("approvalTitle")}
      subtitle={data ? `#${data.id} · ${data.status}` : undefined}
      width="lg"
    >
      {loading && !data ? <DetailLoading label={t("loading")} rows={5} /> : null}
      {error ? <DetailError message={t("loadFailed", { error })} retryLabel={t("retry")} onRetry={reload} /> : null}
      {!valid ? <DetailEmpty message={t("notFound")} /> : null}
      {data ? (
        <ApprovalDetail
          row={data}
          busy={busy}
          decide={decide}
          revision={revision}
          defaultExpanded
          onResolved={() => {
            setRevision((n) => n + 1);
            reload();
            onChanged?.();
          }}
        />
      ) : null}
    </DetailPanel>
  );
}
