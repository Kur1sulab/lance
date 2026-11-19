import type { ReactNode } from "react";

import { RefreshCwIcon, TriangleAlertIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface ErrorStateProps {
  /** 说人话的标题 */
  title: string;
  detail?: string;
  code?: string | number;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
  children?: ReactNode;
}

export function ErrorState({
  title,
  detail,
  code,
  onRetry,
  retryLabel = "重试",
  className,
  children,
}: ErrorStateProps) {
  return (
    <div
      data-slot="error-state"
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className,
      )}
    >
      <div className="flex size-9 items-center justify-center rounded-full border border-border text-muted-foreground [&_svg]:size-4">
        <TriangleAlertIcon />
      </div>
      <p className="font-medium text-foreground text-sm">{title}</p>
      {detail ? (
        <p className="max-w-md text-balance text-muted-foreground text-xs leading-relaxed">
          {detail}
        </p>
      ) : null}
      {code !== undefined && code !== null ? (
        <p className="font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
          错误码 {code}
        </p>
      ) : null}
      {children}
      {onRetry ? (
        <Button size="sm" variant="outline" onClick={onRetry} className="mt-1">
          <RefreshCwIcon />
          {retryLabel}
        </Button>
      ) : null}
    </div>
  );
}
