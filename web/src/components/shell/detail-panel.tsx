"use client";

import * as React from "react";

import { XIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

export interface DetailPanelSegment {
  key: string;
  label: string;
  content: React.ReactNode;
}

export interface DetailPanelProps {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle?: string;
  width?: "md" | "lg";
  segments?: DetailPanelSegment[];
  children?: React.ReactNode;
}

const WIDTH_CLASS: Record<"md" | "lg", string> = {
  md: "sm:w-[480px]",
  lg: "sm:w-[720px]",
};

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

/**
 * 右侧详情抽屉（设计文档 §四）。
 * - 默认 480px，lg 720px
 * - 遮罩点击 / Esc 关闭
 * - 焦点锁在抽屉内，关闭后归还触发元素
 * - 动效尊重 prefers-reduced-motion
 */
export function DetailPanel({
  open,
  onClose,
  title,
  subtitle,
  width = "md",
  segments,
  children,
}: DetailPanelProps) {
  const panelRef = React.useRef<HTMLDivElement | null>(null);
  const titleId = React.useId();
  const restoreRef = React.useRef<HTMLElement | null>(null);

  // Esc 关闭
  React.useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  // 焦点：打开时移入抽屉，关闭时归还
  React.useEffect(() => {
    if (!open) return;
    restoreRef.current = document.activeElement as HTMLElement | null;
    const t = window.setTimeout(() => {
      const first = panelRef.current?.querySelector<HTMLElement>(FOCUSABLE);
      (first ?? panelRef.current)?.focus();
    }, 0);
    return () => {
      window.clearTimeout(t);
      restoreRef.current?.focus?.();
    };
  }, [open]);

  // 焦点陷阱（Tab / Shift+Tab 循环）
  const onKeyDownTrap = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "Tab") return;
    const nodes = panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE);
    if (!nodes || nodes.length === 0) return;
    const list = Array.from(nodes).filter((n) => n.offsetParent !== null);
    if (list.length === 0) return;
    const first = list[0];
    const last = list[list.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };

  if (!open) return null;

  return (
    <div data-slot="detail-panel-root" className="fixed inset-0 z-50">
      <button
        type="button"
        aria-label="关闭详情"
        data-slot="detail-panel-overlay"
        onClick={onClose}
        className="absolute inset-0 bg-foreground/20 supports-backdrop-filter:backdrop-blur-[2px] motion-safe:animate-in motion-safe:fade-in-0"
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        onKeyDown={onKeyDownTrap}
        data-slot="detail-panel"
        data-width={width}
        className={cn(
          "absolute inset-y-0 right-0 flex w-full flex-col border-border border-l bg-background text-foreground shadow-lg outline-none",
          "motion-safe:transition-transform motion-safe:duration-200 motion-safe:ease-out",
          WIDTH_CLASS[width],
        )}
      >
        <div className="flex shrink-0 items-start gap-2 border-border border-b px-4 py-3">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="truncate font-heading font-medium text-sm">
              {title}
            </h2>
            {subtitle ? (
              <p className="mt-0.5 truncate text-muted-foreground text-xs">{subtitle}</p>
            ) : null}
          </div>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="关闭">
            <XIcon />
          </Button>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          {segments && segments.length > 0 ? (
            <Tabs defaultValue={segments[0].key} className="gap-0 px-4 py-3">
              <TabsList variant="line" className="justify-start gap-4">
                {segments.map((s) => (
                  <TabsTrigger key={s.key} value={s.key}>
                    {s.label}
                  </TabsTrigger>
                ))}
              </TabsList>
              {segments.map((s) => (
                <TabsContent key={s.key} value={s.key} className="pt-3">
                  {s.content}
                </TabsContent>
              ))}
            </Tabs>
          ) : null}
          {children ? <div className="px-4 py-3">{children}</div> : null}
        </div>
      </div>
    </div>
  );
}
