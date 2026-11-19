"use client";

import Link from "next/link";
import * as React from "react";

import { ChevronRightIcon, MoreHorizontalIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export interface PageHeaderBreadcrumb {
  label: string;
  href?: string;
}

export interface PageHeaderProps {
  title: string;
  description?: string;
  breadcrumb?: PageHeaderBreadcrumb[];
  actions?: React.ReactNode;
  className?: string;
}

/** 右侧最多 2 个内联操作，其余收进 ⋯ 菜单（设计文档 §三）。 */
const MAX_INLINE_ACTIONS = 2;

export function PageHeader({
  title,
  description,
  breadcrumb,
  actions,
  className,
}: PageHeaderProps) {
  // Children.toArray 已剔除 null/undefined/布尔值，但不会展开 Fragment；
  // 调用方常写 <>a b c</>，这里递归摊平，否则「超过 2 个收进 ⋯」永远不触发。
  const flatten = (nodes: React.ReactNode): React.ReactNode[] =>
    React.Children.toArray(nodes).flatMap((node) =>
      React.isValidElement(node) && node.type === React.Fragment
        ? flatten((node.props as { children?: React.ReactNode }).children)
        : [node],
    );
  const items = flatten(actions);
  const inline = items.slice(0, MAX_INLINE_ACTIONS);
  const overflow = items.slice(MAX_INLINE_ACTIONS);

  return (
    <div
      data-slot="page-header"
      className={cn("flex flex-col gap-1.5 px-4 py-3 md:px-6", className)}
    >
      {breadcrumb && breadcrumb.length > 0 ? (
        <nav
          aria-label="面包屑"
          className="flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground"
        >
          {breadcrumb.map((crumb, i) => {
            const last = i === breadcrumb.length - 1;
            return (
              <React.Fragment key={`${crumb.label}-${i}`}>
                {i > 0 ? (
                  <ChevronRightIcon className="size-3 shrink-0 opacity-60" aria-hidden />
                ) : null}
                {crumb.href && !last ? (
                  <Link
                    href={crumb.href}
                    className="truncate rounded-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    {crumb.label}
                  </Link>
                ) : (
                  <span
                    aria-current={last ? "page" : undefined}
                    className={cn("truncate", last && "text-foreground")}
                  >
                    {crumb.label}
                  </span>
                )}
              </React.Fragment>
            );
          })}
        </nav>
      ) : null}

      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate font-heading font-medium text-base text-foreground">
            {title}
          </h1>
          {description ? (
            <p className="mt-0.5 line-clamp-2 text-[11.5px] text-muted-foreground leading-[1.6]">
              {description}
            </p>
          ) : null}
        </div>

        {inline.length > 0 || overflow.length > 0 ? (
          <div className="flex shrink-0 items-center gap-2">
            {inline}
            {overflow.length > 0 ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon-sm" aria-label="更多操作">
                    <MoreHorizontalIcon />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent
                  align="end"
                  className="flex w-auto min-w-44 flex-col gap-1 p-1"
                >
                  {overflow.map((node, i) => (
                    <div key={`overflow-${i}`} className="[&>*]:w-full">
                      {node}
                    </div>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}
