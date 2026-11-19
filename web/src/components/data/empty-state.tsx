import Link from "next/link";
import type { ReactNode } from "react";

import { InboxIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface EmptyStateNextStep {
  label: string;
  href?: string;
  onClick?: () => void;
}

export interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  /** 为什么空 —— 必填，不能只写「暂无数据」 */
  reason: string;
  /** 下一步做什么 */
  nextStep?: EmptyStateNextStep;
  className?: string;
}

export function EmptyState({
  icon,
  title,
  reason,
  nextStep,
  className,
}: EmptyStateProps) {
  return (
    <div
      data-slot="empty-state"
      role="status"
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className,
      )}
    >
      <div className="flex size-9 items-center justify-center rounded-full border border-border text-muted-foreground [&_svg]:size-4">
        {icon ?? <InboxIcon />}
      </div>
      <p className="font-medium text-foreground text-sm">{title}</p>
      <p className="max-w-md text-balance text-muted-foreground text-xs leading-relaxed">
        {reason}
      </p>
      {nextStep ? (
        <div className="mt-1">
          {nextStep.href ? (
            <Button asChild size="sm" variant="outline">
              <Link href={nextStep.href}>{nextStep.label}</Link>
            </Button>
          ) : (
            <Button size="sm" variant="outline" onClick={nextStep.onClick}>
              {nextStep.label}
            </Button>
          )}
        </div>
      ) : null}
    </div>
  );
}
