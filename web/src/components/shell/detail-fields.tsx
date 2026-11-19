"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * 抽屉里的键值网格：左列标签右列值，窗口窄时自动单列。
 * 放在 shell/ 下供 domain/* 复用（shell 不反向依赖 domain）。
 */
export function DetailFields({ children, className }: { children: React.ReactNode; className?: string }) {
  return <dl className={cn("grid grid-cols-[minmax(5rem,auto)_1fr] gap-x-3 gap-y-2 text-sm", className)}>{children}</dl>;
}

export function DetailField({
  label,
  children,
  mono = false,
}: {
  label: string;
  children: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <>
      <dt className="text-muted-foreground text-xs leading-5">{label}</dt>
      <dd className={cn("min-w-0 break-words leading-5", mono && "font-mono text-xs")}>{children}</dd>
    </>
  );
}

/** 抽屉里的一段标题（用于没有 Tabs 的简单段落）。 */
export function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="font-medium text-muted-foreground text-xs uppercase tracking-wide">{title}</h3>
      {children}
    </section>
  );
}
