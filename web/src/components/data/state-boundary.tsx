"use client";

import type { ReactNode } from "react";

import { LockIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { EmptyState, type EmptyStateNextStep } from "./empty-state";
import { ErrorState } from "./error-state";
import { SkeletonTable } from "./skeleton-table";

export interface StateBoundaryProps {
  loading?: boolean;
  error?: { title?: string; detail?: string; code?: string | number } | null;
  /** true = 无数据。无权限请用 PermissionState，不要伪装成 empty。 */
  empty?: boolean;
  emptyProps?: {
    title?: string;
    reason?: string;
    nextStep?: EmptyStateNextStep;
  };
  skeleton?: ReactNode;
  skeletonRows?: number;
  skeletonCols?: number;
  onRetry?: () => void;
  children: ReactNode;
  className?: string;
}

/**
 * 优先级：loading → error → empty → children（设计文档 §五）。
 */
export function StateBoundary({
  loading = false,
  error = null,
  empty = false,
  emptyProps,
  skeleton,
  skeletonRows = 8,
  skeletonCols = 5,
  onRetry,
  children,
  className,
}: StateBoundaryProps) {
  if (loading) {
    return (
      <div data-slot="state-loading" className={cn("w-full", className)}>
        {skeleton ?? <SkeletonTable rows={skeletonRows} cols={skeletonCols} />}
      </div>
    );
  }

  if (error) {
    return (
      <div data-slot="state-error" className={cn("w-full", className)}>
        <ErrorState
          title={error.title ?? "数据加载失败"}
          detail={error.detail}
          code={error.code}
          onRetry={onRetry}
        />
      </div>
    );
  }

  if (empty) {
    return (
      <div data-slot="state-empty" className={cn("w-full", className)}>
        <EmptyState
          title={emptyProps?.title ?? "这里还没有记录"}
          reason={
            emptyProps?.reason ??
            "当前筛选条件下没有任何结果。可以放宽筛选条件，或者先创建一条数据。"
          }
          nextStep={emptyProps?.nextStep}
        />
      </div>
    );
  }

  return <>{children}</>;
}

export interface PermissionStateProps {
  /** 缺哪个权限，例如 findings:read */
  permission: string;
  /** 找谁要，例如「项目管理员」 */
  ask?: string;
  title?: string;
  detail?: string;
  className?: string;
  action?: ReactNode;
}

/**
 * 无权限态。绝不渲染成空列表 —— 那会让用户以为系统里真的没有数据。
 */
export function PermissionState({
  permission,
  ask = "平台管理员",
  title = "没有查看权限",
  detail,
  className,
  action,
}: PermissionStateProps) {
  return (
    <div
      data-slot="permission-state"
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className,
      )}
    >
      <div className="flex size-9 items-center justify-center rounded-full border border-border text-muted-foreground [&_svg]:size-4">
        <LockIcon />
      </div>
      <p className="font-medium text-foreground text-sm">{title}</p>
      <p className="max-w-md text-balance text-muted-foreground text-xs leading-relaxed">
        {detail ?? `你的账号缺少查看此处数据所需的权限，请联系${ask}申请开通。`}
      </p>
      <div className="flex flex-wrap items-center justify-center gap-2 text-[11px] text-muted-foreground">
        <span>缺少权限</span>
        <code className="rounded-sm border border-border px-1.5 py-0.5 font-mono text-foreground">
          {permission}
        </code>
        <span>找</span>
        <span className="text-foreground">{ask}</span>
      </div>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

/** 便捷导出：无权限时的重试/申请按钮 */
export function PermissionStateAction({ onClick }: { onClick?: () => void }) {
  return (
    <Button size="sm" variant="outline" onClick={onClick}>
      申请权限
    </Button>
  );
}
