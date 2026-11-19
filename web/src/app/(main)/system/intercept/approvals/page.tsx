"use client";

import * as React from "react";

import { RefreshCwIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { StateBoundary } from "@/components/data/state-boundary";
import { ApprovalDrawer } from "@/components/domain/approval-drawer";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useDetailParam } from "@/hooks/use-detail-param";
import { errorCodeOf } from "@/lib/api-error";
import type { InterceptApprovalFilter } from "@/lib/types";
import { cn } from "@/lib/utils";

import { ApprovalRecordsPanel, useApprovalRecords } from "./_components/approval-records-panel";

/**
 * 列表本体：取数与四态都在这里，外面再包 PageShell。
 * 抽屉改动后靠 `key={revision}` 重挂载重取（与原 <ApprovalRecords key=…> 一致）。
 */
function ApprovalsList({ onOpenDetail }: { onOpenDetail: (id: number) => void }) {
  const t = useTranslations("approval");
  const tn = useTranslations("nav");
  const tp = useTranslations("pageStates");
  const state = useApprovalRecords();

  const setStatus = (value: string) =>
    state.setFilter({
      ...state.filter,
      status: value === "all" ? undefined : (value as InterceptApprovalFilter["status"]),
    });
  const setSource = (value: string) =>
    state.setFilter({
      ...state.filter,
      decision_source: value === "all" ? undefined : (value as InterceptApprovalFilter["decision_source"]),
    });

  return (
    <PageShell
      title={t("title.history")}
      description={t("intro")}
      breadcrumb={[{ label: tn("group.govern"), href: "/dashboard" }, { label: tn("item.approvals") }]}
      actions={
        <>
          {state.pending.length ? (
            <Badge variant="secondary">{t("pendingBadge", { count: state.pending.length })}</Badge>
          ) : null}
          <Button variant="outline" size="sm" onClick={state.refresh} disabled={state.loading || state.refreshing}>
            <RefreshCwIcon data-icon="inline-start" className={cn(state.refreshing && "animate-spin")} />
            {t("refresh")}
          </Button>
        </>
      }
      toolbar={
        <Toolbar
          filters={
            <>
              <Select value={state.filter.status ?? "all"} onValueChange={setStatus}>
                <SelectTrigger size="sm" className="w-40" aria-label={t("filter.status")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">{t("filter.statusAll")}</SelectItem>
                    <SelectItem value="denied">{t("status.denied")}</SelectItem>
                    <SelectItem value="pending">{t("status.pending")}</SelectItem>
                    <SelectItem value="allowed">{t("status.allowed")}</SelectItem>
                    <SelectItem value="timeout">{t("status.timeout")}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select value={state.filter.decision_source ?? "all"} onValueChange={setSource}>
                <SelectTrigger size="sm" className="w-40" aria-label={t("filter.source")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">{t("filter.sourceAll")}</SelectItem>
                    <SelectItem value="model">{t("sourceOption.model")}</SelectItem>
                    <SelectItem value="rule">{t("sourceOption.rule")}</SelectItem>
                    <SelectItem value="unknown">{t("sourceOption.unknown")}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              {state.filtered ? (
                <Button variant="ghost" size="sm" onClick={() => state.setFilter({})}>
                  {t("filter.clear")}
                </Button>
              ) : null}
            </>
          }
        />
      }
    >
      <div className="px-4 py-3 md:px-6">
        <StateBoundary
          loading={state.loading}
          error={
            state.error
              ? {
                  title: tp("errorTitle"),
                  detail: tp("errorDetail"),
                  code: errorCodeOf(state.lastError.current),
                  codeLabel: tp("errorCodeLabel"),
                  retryLabel: tp("retry"),
                }
              : null
          }
          permission={state.denied ? "approvals:read" : null}
          permissionProps={{
            title: tp("permissionTitle"),
            detail: tp("permissionDetail", { name: tn("item.approvals"), ask: tp("askAdmin") }),
            missingLabel: tp("permissionMissing"),
            askLabel: tp("permissionAsk"),
            ask: tp("askAdmin"),
          }}
          empty={!state.loading && !state.error && state.rows.length === 0 && state.pending.length === 0}
          emptyProps={{ title: tp("emptyTitle"), reason: tp("empty.approvals") }}
          onRetry={state.retry}
        >
          <div className="space-y-4">
            {state.pendingError ? (
              <Alert variant="destructive">
                <AlertDescription>{t("pendingLoadError", { error: state.pendingError })}</AlertDescription>
              </Alert>
            ) : null}
            <ApprovalRecordsPanel state={state} onOpenDetail={onOpenDetail} />
          </div>
        </StateBoundary>
      </div>
    </PageShell>
  );
}

function ApprovalsPageInner() {
  // 审批详情从行内展开改为右侧抽屉：?id= 与 URL 双向同步（设计文档 §四）。
  const detail = useDetailParam();
  const [revision, setRevision] = React.useState(0);

  return (
    <>
      <ApprovalsList key={revision} onOpenDetail={detail.openDetail} />
      <ApprovalDrawer
        id={detail.id}
        open={detail.open}
        onClose={detail.closeDetail}
        onChanged={() => setRevision((n) => n + 1)}
      />
    </>
  );
}

// useSearchParams 必须落在 Suspense 边界内（静态导出要求）。
export default function ApprovalsPage() {
  return (
    <React.Suspense fallback={null}>
      <ApprovalsPageInner />
    </React.Suspense>
  );
}
