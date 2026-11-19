import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export interface ToolbarProps {
  /** 左侧搜索插槽 */
  search?: ReactNode;
  /** 中间筛选插槽 */
  filters?: ReactNode;
  /** 右侧视图切换 / 批量操作插槽 */
  viewSwitch?: ReactNode;
  className?: string;
}

export function Toolbar({ search, filters, viewSwitch, className }: ToolbarProps) {
  return (
    <div
      data-slot="toolbar"
      className={cn("flex flex-wrap items-center gap-x-2 gap-y-2", className)}
    >
      {search ? (
        <div data-slot="toolbar-search" className="min-w-40 flex-1 sm:max-w-72">
          {search}
        </div>
      ) : (
        <div className="min-w-0 flex-1" />
      )}

      {filters ? (
        <div data-slot="toolbar-filters" className="flex flex-wrap items-center gap-2">
          {filters}
        </div>
      ) : null}

      {viewSwitch ? (
        <div
          data-slot="toolbar-view"
          className="ml-auto flex shrink-0 items-center gap-1"
        >
          {viewSwitch}
        </div>
      ) : null}
    </div>
  );
}
