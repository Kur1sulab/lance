import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export interface SkeletonTableProps {
  rows?: number;
  cols?: number;
  /** 首列宽度占比，模拟「名称/标题」列比其它列宽 */
  firstColFlex?: number;
  className?: string;
  /** 是否渲染表头骨架 */
  header?: boolean;
}

/**
 * 骨架形状与真实表格一致（表头 + 行 + 单元格网格），不是居中转圈。
 */
export function SkeletonTable({
  rows = 8,
  cols = 5,
  firstColFlex = 2,
  className,
  header = true,
}: SkeletonTableProps) {
  const safeRows = Math.max(1, rows);
  const safeCols = Math.max(1, cols);
  const colList = Array.from({ length: safeCols }, (_, i) => i);

  return (
    <div
      data-slot="skeleton-table"
      aria-busy="true"
      aria-live="polite"
      className={cn("w-full overflow-hidden", className)}
    >
      <span className="sr-only">正在加载数据</span>

      {header ? (
        <div className="flex items-center gap-4 border-border border-b bg-muted/40 px-4 py-2.5">
          {colList.map((c) => (
            <Skeleton
              key={`h-${c}`}
              className="h-2.5 rounded-sm"
              style={{ flex: c === 0 ? firstColFlex : 1 }}
            />
          ))}
        </div>
      ) : null}

      {Array.from({ length: safeRows }, (_, r) => (
        <div
          key={`r-${r}`}
          className="flex items-center gap-4 border-border border-b px-4 py-3 last:border-b-0"
        >
          {colList.map((c) => (
            <Skeleton
              key={`r-${r}-c-${c}`}
              className="h-3 rounded-sm"
              style={{
                flex: c === 0 ? firstColFlex : 1,
                // 列宽轻微抖动，避免整齐到像装饰
                maxWidth: c === 0 ? "60%" : undefined,
                opacity: 1 - (r % 3) * 0.12,
              }}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
