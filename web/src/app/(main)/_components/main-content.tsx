"use client";

import { type ReactNode, useEffect, useState } from "react";

import { usePathname } from "next/navigation";

import { useTranslations } from "next-intl";

import { Separator } from "@/components/ui/separator";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { useCurrentUser } from "@/hooks/use-current-user";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

import { AccountSwitcher } from "./sidebar/account-switcher";
import { LayoutControls } from "./sidebar/layout-controls";
import { SearchDialog } from "./sidebar/search-dialog";
import { ThemeSwitcher } from "./sidebar/theme-switcher";

// 任务详情页保持原样：它自带头部/Tabs 与内边距，这里不再叠加全局头部和 padding。
function isFullBleed(pathname: string) {
  const p = (() => {
    try {
      return decodeURIComponent(pathname);
    } catch {
      return pathname;
    }
  })();
  // 静态导出开了 trailingSlash，列表页自身的 pathname 是 "/function/tasks/"，
  // 先去掉尾斜杠再判前缀，否则列表页会被误判成详情页而丢掉全局头部。
  const normalized = p.replace(/\/+$/, "");
  return normalized.startsWith("/function/tasks/");
}

export function MainContent({ children }: { children: ReactNode }) {
  const currentUser = useCurrentUser();
  const pathname = usePathname();
  const t = useTranslations("header");
  const [version, setVersion] = useState("");
  // 遥测：在途任务数 + 时钟。10s 一轮，失败就静默显示 "—"，不弹错误。
  const [inflight, setInflight] = useState<number | null>(null);
  const [clock, setClock] = useState("");

  useEffect(() => {
    api
      .health()
      .then((h) => setVersion((h.version ?? "").replace(/^v(?=\d)/, "")))
      .catch(() => setVersion(""));
  }, []);

  useEffect(() => {
    let alive = true;
    const tick = () => {
      setClock(new Date().toLocaleTimeString("zh-CN", { hour12: false }));
    };
    tick();
    const clockTimer = setInterval(tick, 1000);
    const poll = () => {
      api
        .tasks()
        .then((r) => {
          if (!alive) return;
          const n = r.tasks.filter((x) => (x as { status?: string }).status === "running").length;
          setInflight(n);
        })
        .catch(() => alive && setInflight(null));
    };
    poll();
    const taskTimer = setInterval(poll, 10000);
    return () => {
      alive = false;
      clearInterval(clockTimer);
      clearInterval(taskTimer);
    };
  }, []);

  if (isFullBleed(pathname)) {
    return <>{children}</>;
  }

  return (
    <>
      <header
        className={cn(
          "flex h-12 shrink-0 items-center gap-2 border-b transition-[width,height] ease-linear group-has-data-[collapsible=icon]/sidebar-wrapper:h-12",
          "[html[data-navbar-style=sticky]_&]:sticky [html[data-navbar-style=sticky]_&]:top-0 [html[data-navbar-style=sticky]_&]:z-50 [html[data-navbar-style=sticky]_&]:overflow-hidden [html[data-navbar-style=sticky]_&]:rounded-t-[inherit] [html[data-navbar-style=sticky]_&]:bg-background/50 [html[data-navbar-style=sticky]_&]:backdrop-blur-md",
        )}
      >
        <div className="flex w-full items-center justify-between px-4 lg:px-6">
          <div className="flex items-center gap-1 lg:gap-2">
            <SidebarTrigger className="-ml-1" />
            <Separator
              orientation="vertical"
              className="mx-2 data-[orientation=vertical]:h-4 data-[orientation=vertical]:self-center"
            />
            <SearchDialog />
            <span className="ml-1 hidden font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground md:inline">
              ▸ {pathname.replace(/\/+$/, "") || "/"}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden items-center gap-3 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground md:flex">
              <span className="flex items-center gap-1.5">
                <span className="inline-block size-1.5 bg-foreground/70" />
                ONLINE
              </span>
              <span className="tabular-nums">
                RUN {inflight === null ? "—" : String(inflight).padStart(2, "0")}
              </span>
              <span className="tabular-nums">{clock}</span>
            </span>
            {version && (
              <span className="font-medium text-muted-foreground text-xs tabular-nums">
                {t("version", { version })}
              </span>
            )}
            <LayoutControls />
            <ThemeSwitcher />
            <AccountSwitcher users={[currentUser]} />
          </div>
        </div>
      </header>
      <div data-cockpit="1" className="min-h-0 min-w-0 flex-1 overflow-x-hidden p-4 has-data-[content-padding=false]:p-0 md:p-6 md:has-data-[content-padding=false]:p-0">
        {children}
      </div>
    </>
  );
}
