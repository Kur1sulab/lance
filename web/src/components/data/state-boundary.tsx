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
  error?: {
    title?: string;
    detail?: string;
    code?: string | number;
    /** 「错误码」标签与重试按钮文案，由调用方传 i18n 结果 */
    codeLabel?: string;
    retryLabel?: string;
  } | null;
  /**
   * 无权限的权限标识（如 `findings:read`）。非空即渲染无权限态。
   * 优先级在 loading 之后、error 之前——403 不是「请求失败」，是「你没这个权力」。
   */
  permission?: string | null;
  permissionProps?: {
    title?: string;
    detail?: string;
    ask?: string;
    /** 「缺少权限」标签文案（i18n 由调用方给） */
    missingLabel?: string;
    /** 「找」标签文案（i18n 由调用方给） */
    askLabel?: string;
    action?: ReactNode;
  };
  /** true = 无数据。无权限请用 permission，不要伪装成 empty。 */
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
 * 优先级：loading → permission → error → empty → children（设计文档 §五）。
 */
export function StateBoundary({
  loading = false,
  error = null,
  permission = null,
  permissionProps,
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

  if (permission) {
    return (
      <div data-slot="state-permission" className={cn("w-full", className)}>
        <PermissionState
          permission={permission}
          ask={permissionProps?.ask}
          title={permissionProps?.title}
          detail={permissionProps?.detail}
          missingLabel={permissionProps?.missingLabel}
          askLabel={permissionProps?.askLabel}
          action={permissionProps?.action}
        />
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
          codeLabel={error.codeLabel}
          retryLabel={error.retryLabel}
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
  /** 「缺少权限」标签；调用方传 i18n 文案 */
  missingLabel?: string;
  /** 「找」标签；调用方传 i18n 文案 */
  askLabel?: string;
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
  missingLabel = "缺少权限",
  askLabel = "找",
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
        <span>{missingLabel}</span>
        <code className="rounded-sm border border-border px-1.5 py-0.5 font-mono text-foreground">
          {permission}
        </code>
        <span>{askLabel}</span>
        <span className="text-foreground">{ask}</span>
      </div>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

/** 便捷导出：无权限时的重试/申请按钮 */
export function PermissionStateAction({
  onClick,
  label = "申请权限",
}: {
  onClick?: () => void;
  label?: string;
}) {
  return (
    <Button size="sm" variant="outline" onClick={onClick}>
      {label}
    </Button>
  );
}
