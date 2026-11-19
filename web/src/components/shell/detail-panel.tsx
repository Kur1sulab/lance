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
  /**
   * modal=true（默认）时带遮罩、焦点锁在抽屉内。
   * modal=false 用于「列表要同时可见可操作」的场景（设计文档 §四）：不铺遮罩、
   * 不锁焦点，Esc 仍关闭，右侧抽屉只吃掉自己的那块区域。
   */
  modal?: boolean;
  /** 是否允许拖动左缘调整宽度（480 → 720，双击复位），宽度按 storageKey 记住。 */
  resizable?: boolean;
  storageKey?: string;
  closeLabel?: string;
  resizeLabel?: string;
}

const WIDTH_CLASS: Record<"md" | "lg", string> = {
  md: "sm:w-[480px]",
  lg: "sm:w-[720px]",
};

const DEFAULT_WIDTH = 480;
const MIN_WIDTH = 360;
const MAX_WIDTH = 720;

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

function clampWidth(value: number) {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(value)));
}

/**
 * 右侧详情抽屉（设计文档 §四）。
 * - 默认 480px，lg 720px；resizable 时可拖到 720px
 * - modal 模式下遮罩点击 / Esc 关闭，焦点锁在抽屉内并归还触发元素
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
  modal = true,
  resizable = false,
  storageKey = "lance_detail_panel_width",
  closeLabel = "关闭",
  resizeLabel = "拖动调整宽度，双击复位",
}: DetailPanelProps) {
  const panelRef = React.useRef<HTMLDivElement | null>(null);
  const titleId = React.useId();
  const restoreRef = React.useRef<HTMLElement | null>(null);
  const [widthPx, setWidthPx] = React.useState<number | null>(null);
  const widthRef = React.useRef<number | null>(null);
  const dragRef = React.useRef<{ x: number; w: number } | null>(null);

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

  // 记住上次拖出来的宽度
  React.useEffect(() => {
    if (!resizable) return;
    try {
      const raw = window.localStorage.getItem(storageKey);
      const saved = raw ? Number(raw) : Number.NaN;
      if (Number.isFinite(saved)) {
        const clamped = clampWidth(saved);
        widthRef.current = clamped;
        setWidthPx(clamped);
      }
    } catch {
      // localStorage 不可用（隐私模式/配额）时退回默认宽度，不影响打开
    }
  }, [resizable, storageKey]);

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

  // 焦点陷阱（Tab / Shift+Tab 循环）——非 modal 时不锁，允许 Tab 到列表继续操作
  const onKeyDownTrap = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!modal || e.key !== "Tab") return;
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

  const onGripPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    const panel = panelRef.current;
    if (!panel) return;
    e.preventDefault();
    dragRef.current = { x: e.clientX, w: panel.getBoundingClientRect().width };
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // 指针已失效（或合成事件）时拿不到捕获，拖拽仍按 move 事件走
    }
  };

  const onGripPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    const next = clampWidth(drag.w - (e.clientX - drag.x));
    widthRef.current = next;
    setWidthPx(next);
  };

  const onGripPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return;
    dragRef.current = null;
    try {
      if (widthRef.current) window.localStorage.setItem(storageKey, String(widthRef.current));
    } catch {
      // 记不住就算了，宽度只影响本次会话
    }
    try {
      if (e.currentTarget.hasPointerCapture?.(e.pointerId)) {
        e.currentTarget.releasePointerCapture(e.pointerId);
      }
    } catch {
      // 捕获已释放，忽略
    }
  };

  const resetWidth = () => {
    widthRef.current = DEFAULT_WIDTH;
    setWidthPx(DEFAULT_WIDTH);
    try {
      window.localStorage.setItem(storageKey, String(DEFAULT_WIDTH));
    } catch {
      // 同上
    }
  };

  return (
    <div data-slot="detail-panel-root" className={cn("fixed inset-0 z-50", !modal && "pointer-events-none")}>
      {modal ? (
        <button
          type="button"
          aria-label={closeLabel}
          data-slot="detail-panel-overlay"
          onClick={onClose}
          className="absolute inset-0 bg-foreground/20 supports-backdrop-filter:backdrop-blur-[2px] motion-safe:animate-in motion-safe:fade-in-0"
        />
      ) : null}
      <div
        ref={panelRef}
        role="dialog"
        aria-modal={modal ? "true" : undefined}
        aria-labelledby={titleId}
        tabIndex={-1}
        onKeyDown={onKeyDownTrap}
        data-slot="detail-panel"
        data-width={width}
        style={resizable && widthPx ? { width: widthPx } : undefined}
        className={cn(
          "absolute inset-y-0 right-0 flex w-full flex-col border-border border-l bg-background text-foreground shadow-lg outline-none",
          "motion-safe:transition-transform motion-safe:duration-200 motion-safe:ease-out",
          !modal && "pointer-events-auto",
          WIDTH_CLASS[width],
        )}
      >
        {resizable ? (
          // 左缘拖拽条：8px 命中区 + 中间 2px 视觉线，双击复位到 480px
          <div
            role="separator"
            aria-orientation="vertical"
            aria-label={resizeLabel}
            tabIndex={-1}
            onPointerDown={onGripPointerDown}
            onPointerMove={onGripPointerMove}
            onPointerUp={onGripPointerUp}
            onPointerCancel={onGripPointerUp}
            onDoubleClick={resetWidth}
            className="group absolute inset-y-0 -left-1 z-10 hidden w-2 cursor-col-resize touch-none items-center justify-center sm:flex"
          >
            <span className="h-10 w-0.5 rounded-full bg-border group-hover:bg-primary/60" />
          </div>
        ) : null}

        <div className="flex shrink-0 items-start gap-2 border-border border-b px-4 py-3">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="truncate font-heading font-medium text-sm">
              {title}
            </h2>
            {subtitle ? <p className="mt-0.5 truncate text-muted-foreground text-xs">{subtitle}</p> : null}
          </div>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label={closeLabel}>
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
