import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import { PageHeader, type PageHeaderBreadcrumb } from "./page-header";

export interface PageShellProps {
  title: string;
  description?: string;
  breadcrumb?: PageHeaderBreadcrumb[];
  actions?: ReactNode;
  toolbar?: ReactNode;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
}

/**
 * 三块定高骨架（设计文档 §三）：
 * Header 与 Toolbar 不随内容滚动，只有 Content 区滚。
 */
export function PageShell({
  title,
  description,
  breadcrumb,
  actions,
  toolbar,
  children,
  className,
  contentClassName,
}: PageShellProps) {
  return (
    <div
      data-slot="page-shell"
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden bg-background",
        className,
      )}
    >
      {/* 页头与工具栏固定在顶部，不参与 Content 滚动（设计文档 §三）。
          前提：调用方给 PageShell 一个确定高度（父级 flex 布局的 flex-1，
          或显式 h-*）。否则三块会一起被页面级滚动带走。 */}
      <div className="shrink-0 border-border border-b">
        <PageHeader
          title={title}
          description={description}
          breadcrumb={breadcrumb}
          actions={actions}
        />
      </div>

      {toolbar ? (
        <div
          data-slot="page-toolbar"
          className="shrink-0 border-border border-b px-4 py-2 md:px-6"
        >
          {toolbar}
        </div>
      ) : null}

      <div
        data-slot="page-content"
        className={cn("min-h-0 flex-1 overflow-auto", contentClassName)}
      >
        {children}
      </div>
    </div>
  );
}
