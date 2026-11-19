"use client";

import * as React from "react";

import { ApprovalRecords } from "@/components/approval-records";
import { ApprovalDrawer } from "@/components/domain/approval-drawer";
import { useDetailParam } from "@/hooks/use-detail-param";

function ApprovalsPageInner() {
  // 审批详情从行内展开改为右侧抽屉：?id= 与 URL 双向同步（设计文档 §四）。
  const detail = useDetailParam();
  const [revision, setRevision] = React.useState(0);

  return (
    <div className="flex min-w-0 flex-1 flex-col p-4 sm:p-6">
      <ApprovalRecords key={revision} onOpenDetail={detail.openDetail} />
      <ApprovalDrawer
        id={detail.id}
        open={detail.open}
        onClose={detail.closeDetail}
        onChanged={() => setRevision((n) => n + 1)}
      />
    </div>
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
