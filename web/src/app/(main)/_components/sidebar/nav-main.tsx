"use client";

import * as React from "react";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { ChevronRight, MailIcon, PlusCircleIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  DEFAULT_OPEN_GROUP_IDS,
  type NavBadge,
  type NavGroup,
  type NavMainItem,
  type NavMainLinkItem,
  type NavMainParentItem,
  SIDEBAR_GROUP_STORAGE_KEY,
} from "@/navigation/sidebar/sidebar-items";

interface NavMainProps {
  readonly items: readonly NavGroup[];
}
interface NavItemProps {
  readonly item: NavMainItem;
  readonly isItemActive: (item: NavMainItem) => boolean;
  readonly isSubItemActive: (url: string) => boolean;
  readonly isSubmenuOpen: (item: NavMainParentItem) => boolean;
}

interface NavLinkItemProps {
  readonly item: NavMainLinkItem;
  readonly isActive: boolean;
  readonly showIconFallback: boolean;
}

interface NavLinkIconProps {
  readonly item: NavMainLinkItem;
  readonly title: string;
  readonly showFallback: boolean;
}

interface NavDropdownItemProps {
  readonly item: NavMainParentItem;
  readonly isActive: boolean;
  readonly isSubItemActive: (url: string) => boolean;
}

interface NavCollapsibleItemProps {
  readonly item: NavMainParentItem;
  readonly isActive: boolean;
  readonly defaultOpen: boolean;
  readonly isSubItemActive: (url: string) => boolean;
}

function CollapsedIconFallback({ title }: { title: string }) {
  return (
    <span className="flex size-4 shrink-0 items-center justify-center rounded-xs font-medium text-[10px] outline">
      {title.slice(0, 1)}
    </span>
  );
}

function hasSubItems(item: NavMainItem): item is NavMainParentItem {
  return Boolean(item.subItems?.length);
}

// 去掉静态导出的尾斜杠，保留根路径 "/"。
function stripTrailingSlash(path: string): string {
  if (path.length <= 1) return path;
  return path.replace(/\/+$/, "") || "/";
}

// 折叠状态只在浏览器侧读写；SSR 阶段与 localStorage 无关，返回空对象即可。
function readStoredGroupState(): Record<string, boolean> {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(SIDEBAR_GROUP_STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const stored: Record<string, boolean> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value === "boolean") stored[key] = value;
    }
    return stored;
  } catch {
    // 存的是坏 JSON（旧版本/手工改过）就当没存过，不让导航整体崩掉。
    return {};
  }
}

function persistGroupState(state: Record<string, boolean>) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(SIDEBAR_GROUP_STORAGE_KEY, JSON.stringify(state));
  } catch {
    /* 隐私模式/配额满：折叠状态丢失不影响使用 */
  }
}

// 分组标题走 nav.group.<messageKey>，由 sidebar-items.ts 的 messageKey 指定；缺键则回退原文。
// 这样侧边栏和搜索对话框（search-dialog）用同一份域名文案，不会各写一套。

export function NavMain({ items }: NavMainProps) {
  const t = useTranslations("nav");
  const { state: sidebarState, isMobile } = useSidebar();
  const rawPath = usePathname();
  // 路由段含中文，pathname 可能是百分号编码，解码后再与导航 url 比较，保证高亮命中。
  // 静态导出开了 trailingSlash，usePathname 返回 "/chat/" 而导航 url 是 "/chat"，
  // 不去尾斜杠的话高亮与「当前域自动展开」会整体失效。
  const path = (() => {
    try {
      return stripTrailingSlash(decodeURIComponent(rawPath));
    } catch {
      return stripTrailingSlash(rawPath);
    }
  })();

  const isItemActive = (item: NavMainItem) => {
    if (hasSubItems(item)) {
      return item.subItems.some((sub) => path.startsWith(sub.url));
    }

    return path === item.url;
  };

  const isSubItemActive = (url: string) => {
    return path === url;
  };

  const isSubmenuOpen = (item: NavMainParentItem) => {
    return item.subItems.some((sub) => path.startsWith(sub.url));
  };

  const groupTitle = (group: NavGroup) => {
    if (group.messageKey && t.has(`group.${group.messageKey}`)) {
      return t(`group.${group.messageKey}`);
    }
    return group.label ?? String(group.id);
  };

  // 当前页所在域：刷新后必须自动展开，否则用户找不到自己在哪一栏。
  const activeGroupId = items.find((group) => group.items.some(isItemActive))?.id ?? null;

  const [openGroups, setOpenGroups] = React.useState<Record<string, boolean> | null>(null);

  // 首帧（SSR 与 hydration）用确定性的默认值，挂载后再读 localStorage，避免水合不一致。
  React.useEffect(() => {
    setOpenGroups(readStoredGroupState());
  }, []);

  React.useEffect(() => {
    if (activeGroupId === null) return;
    setOpenGroups((prev) => {
      if (!prev || prev[String(activeGroupId)] === true) return prev;
      return { ...prev, [String(activeGroupId)]: true };
    });
  }, [activeGroupId]);

  const isGroupOpen = (group: NavGroup) => {
    // 侧边栏收成图标条时所有域都必须摊开，否则非默认域里的页面整片消失。
    if (sidebarState === "collapsed" && !isMobile) return true;
    return openGroups?.[String(group.id)] ?? DEFAULT_OPEN_GROUP_IDS.includes(group.id);
  };

  const setGroupOpen = (groupId: number, open: boolean) => {
    setOpenGroups((prev) => {
      const next = { ...(prev ?? {}), [String(groupId)]: open };
      persistGroupState(next);
      return next;
    });
  };

  return (
    <>
      {items.map((group) => {
        const open = isGroupOpen(group);

        return (
          <SidebarGroup key={group.id} className="py-1">
            <Collapsible
              open={open}
              onOpenChange={(next) => setGroupOpen(group.id, next)}
              className="group/nav-domain"
            >
              <CollapsibleTrigger asChild>
                <button
                  type="button"
                  className="flex h-6 w-full shrink-0 cursor-pointer items-center gap-1 rounded-md px-2 text-xs font-medium text-sidebar-foreground/85 outline-hidden transition-[margin,opacity,background-color] duration-150 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring group-data-[collapsible=icon]:pointer-events-none group-data-[collapsible=icon]:-mt-8 group-data-[collapsible=icon]:opacity-0"
                >
                  <span className="truncate">{groupTitle(group)}</span>
                  <ChevronRight className="ml-auto size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]/nav-domain:rotate-90" />
                </button>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <SidebarGroupContent>
                  <SidebarMenu>
                    {group.items.map((item) => (
                      <NavItem
                        key={item.id}
                        item={item}
                        isItemActive={isItemActive}
                        isSubItemActive={isSubItemActive}
                        isSubmenuOpen={isSubmenuOpen}
                      />
                    ))}
                  </SidebarMenu>
                </SidebarGroupContent>
              </CollapsibleContent>
            </Collapsible>
          </SidebarGroup>
        );
      })}
    </>
  );
}

function NavItem({ item, isItemActive, isSubItemActive, isSubmenuOpen }: NavItemProps) {
  const { state, isMobile } = useSidebar();
  const isCollapsedDesktop = state === "collapsed" && !isMobile;

  if (!hasSubItems(item)) {
    return <NavLinkItem item={item} isActive={isItemActive(item)} showIconFallback={isCollapsedDesktop} />;
  }

  if (isCollapsedDesktop) {
    return <NavDropdownItem item={item} isActive={isItemActive(item)} isSubItemActive={isSubItemActive} />;
  }

  return (
    <NavCollapsibleItem
      item={item}
      isActive={isItemActive(item)}
      defaultOpen={isSubmenuOpen(item)}
      isSubItemActive={isSubItemActive}
    />
  );
}

// InterceptPendingBadge polls /api/intercept/pending every 5 s and shows a red
// count badge when there are unresolved approval requests.
function InterceptPendingBadge() {
  const [count, setCount] = React.useState(0);
  React.useEffect(() => {
    let live = true;
    async function poll() {
      try {
        const list = await api.interceptPending();
        if (live) setCount(list.length);
      } catch {
        /* ignore */
      }
    }
    void poll();
    const t = setInterval(poll, 5000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, []);
  if (count === 0) return null;
  return (
    <SidebarMenuBadge className="flex h-4.5 min-w-[18px] items-center justify-center rounded-full bg-amber-500 px-1 text-[10px] font-semibold leading-none text-white shadow-sm ring-1 ring-amber-400/40 dark:text-black">
      {count > 99 ? "99+" : count}
    </SidebarMenuBadge>
  );
}

function NavLinkItem({ item, isActive, showIconFallback }: NavLinkItemProps) {
  const t = useTranslations("nav");
  const title = t(`item.${item.id}`);
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild aria-disabled={item.disabled} tooltip={title} isActive={isActive} className="h-7.5">
        <Link
          prefetch={false}
          href={item.url}
          target={item.newTab ? "_blank" : undefined}
          rel={item.newTab ? "noreferrer" : undefined}
        >
          <NavLinkIcon item={item} title={title} showFallback={showIconFallback} />
          <span>{title}</span>
        </Link>
      </SidebarMenuButton>
      {item.id === "approvals" ? <InterceptPendingBadge /> : <NavItemBadge badge={item.badge} />}
    </SidebarMenuItem>
  );
}

function NavLinkIcon({ item, title, showFallback }: NavLinkIconProps) {
  const Icon = item.icon;

  if (Icon) {
    return <Icon />;
  }

  if (showFallback) {
    return <CollapsedIconFallback title={title} />;
  }

  return null;
}

function NavDropdownItem({ item, isActive, isSubItemActive }: NavDropdownItemProps) {
  const t = useTranslations("nav");
  const Icon = item.icon;
  const title = t(`item.${item.id}`);
  const subTitle = (id: string, fallback: string) => (t.has(`item.${id}`) ? t(`item.${id}`) : fallback);

  return (
    <SidebarMenuItem>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <SidebarMenuButton tooltip={title} isActive={isActive} disabled={item.disabled} className="h-7.5">
            {Icon ? <Icon /> : <CollapsedIconFallback title={title} />}
            <span>{title}</span>
          </SidebarMenuButton>
        </DropdownMenuTrigger>

        <DropdownMenuContent side="right" align="start" sideOffset={12} className="w-48">
          <DropdownMenuGroup>
            {item.subItems.map((subItem) => {
              const SubIcon = subItem.icon;

              return (
                <DropdownMenuItem key={subItem.id} asChild disabled={subItem.disabled}>
                  <Link
                    prefetch={false}
                    href={subItem.url}
                    target={subItem.newTab ? "_blank" : undefined}
                    rel={subItem.newTab ? "noreferrer" : undefined}
                    aria-current={isSubItemActive(subItem.url) ? "page" : undefined}
                    className="flex items-center gap-2"
                  >
                    {SubIcon && <SubIcon />}
                    <span>{subTitle(subItem.id, subItem.title)}</span>
                  </Link>
                </DropdownMenuItem>
              );
            })}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </SidebarMenuItem>
  );
}

function NavCollapsibleItem({ item, isActive, defaultOpen, isSubItemActive }: NavCollapsibleItemProps) {
  const t = useTranslations("nav");
  const Icon = item.icon;
  const title = t(`item.${item.id}`);
  const subTitle = (id: string, fallback: string) => (t.has(`item.${id}`) ? t(`item.${id}`) : fallback);

  return (
    <Collapsible asChild defaultOpen={defaultOpen} className="group/collapsible">
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          <SidebarMenuButton tooltip={title} isActive={isActive} disabled={item.disabled} className="h-7.5">
            {Icon && <Icon />}
            <span>{title}</span>
            <ChevronRight className="ml-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
          </SidebarMenuButton>
        </CollapsibleTrigger>
        <NavItemBadge badge={item.badge} />

        <CollapsibleContent>
          <SidebarMenuSub>
            {item.subItems.map((subItem) => {
              const SubIcon = subItem.icon;

              return (
                <SidebarMenuSubItem key={subItem.id}>
                  <SidebarMenuSubButton
                    asChild
                    aria-disabled={subItem.disabled}
                    isActive={isSubItemActive(subItem.url)}
                  >
                    <Link
                      prefetch={false}
                      href={subItem.url}
                      target={subItem.newTab ? "_blank" : undefined}
                      rel={subItem.newTab ? "noreferrer" : undefined}
                    >
                      {SubIcon && <SubIcon />}
                      <span>{subTitle(subItem.id, subItem.title)}</span>
                    </Link>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              );
            })}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  );
}

function NavItemBadge({ badge }: { badge?: NavBadge }) {
  if (!badge) {
    return null;
  }

  return (
    <SidebarMenuBadge
      className={cn(
        "rounded-sm border capitalize",
        badge === "new" &&
          "border-green-600 text-green-600 peer-hover/menu-button:text-green-600 peer-data-active/menu-button:text-green-600",
        badge === "soon" && "border-muted-foreground text-muted-foreground",
      )}
    >
      {badge}
    </SidebarMenuBadge>
  );
}
