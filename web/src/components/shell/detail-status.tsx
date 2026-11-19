"use client";

import * as React from "react";

import { AlertTriangleIcon, InboxIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";

/**
 * 抽屉的三种非内容态（设计文档 §五）：loading 骨架、error 说人话 + 重试、empty 说清为什么空。
 * 文案由调用方传入，组件自身不做翻译。
 */
export function DetailLoading({ label, rows = 4 }: { label: string; rows?: number }) {
  return (
    <div className="space-y-3" aria-busy="true" aria-live="polite">
      <span className="sr-only">{label}</span>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className={cn("h-4 animate-pulse rounded bg-muted", i === 0 ? "w-1/3" : "w-full")} />
      ))}
      <div className="flex items-center gap-2 text-muted-foreground text-xs">
        <Spinner className="size-3.5" />
        {label}
      </div>
    </div>
  );
}

export function DetailError({
  message,
  retryLabel,
  onRetry,
}: {
  message: string;
  retryLabel: string;
  onRetry: () => void;
}) {
  return (
    <div className="space-y-3 rounded-md border border-destructive/30 bg-destructive/5 p-3">
      <p className="flex items-start gap-2 text-sm">
        <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
        <span className="min-w-0 break-words">{message}</span>
      </p>
      <Button size="sm" variant="outline" onClick={onRetry}>
        {retryLabel}
      </Button>
    </div>
  );
}

export function DetailEmpty({ message }: { message: string }) {
  return (
    <div className="flex flex-col items-center gap-2 py-10 text-center text-muted-foreground text-sm">
      <InboxIcon className="size-5" />
      <span className="max-w-[28ch]">{message}</span>
    </div>
  );
}
