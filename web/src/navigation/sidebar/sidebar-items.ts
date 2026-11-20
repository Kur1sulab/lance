import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  Crosshair,
  Flame,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  /** 侧边栏与搜索对话框的域标题都走 nav.group.<messageKey>，不硬编码文案。 */
  messageKey?: string;
  /** 消息缺失时的回退文案，仅作兜底，正常渲染不出现。 */
  label?: string;
  items: NavMainItem[];
}

// 信息架构按「用户的真实动作」分五个域，不按代码目录分。
// 每个域的成员只决定它在侧边栏里出现在哪一栏 —— 页面路径一律不动：
// 破甲引擎仍挂在 /system/armor，资产拦截仍挂在 /system/intercept/assets，
// 改路径就是断链（书签、审计日志里的历史链接、页面内硬编码跳转全得跟着改）。
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    messageKey: "war",
    label: "作战",
    items: [
      { id: "dashboard", title: "仪表盘", url: "/dashboard", icon: LayoutDashboard },
      { id: "tasks", title: "任务", url: "/function/tasks", icon: Target },
      { id: "findings", title: "发现", url: "/function/findings", icon: Bug },
      { id: "chat", title: "对话", url: "/chat", icon: MessageSquare },
    ],
  },
  {
    id: 2,
    messageKey: "target",
    label: "目标",
    items: [
      { id: "assets", title: "资产", url: "/function/assets", icon: Network },
      { id: "sync", title: "资产同步", url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: "工作空间", url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 3,
    messageKey: "capability",
    label: "能力",
    items: [
      { id: "arsenal", title: "武器库", url: "/function/arsenal", icon: Crosshair },
      { id: "armor", title: "破甲引擎", url: "/system/armor", icon: Flame },
      { id: "agents", title: "Agent", url: "/system/agents", icon: Bot },
      { id: "skills", title: "Skill", url: "/system/skills", icon: Sparkles },
      { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
      { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
      { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
    ],
  },
  {
    id: 4,
    messageKey: "observe",
    label: "观测",
    items: [
      { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
      { id: "commands", title: "工具调用记录", url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: "LLM 录制", url: "/function/llm-records", icon: Radio },
    ],
  },
  {
    id: 5,
    messageKey: "govern",
    label: "治理",
    items: [
      { id: "intercept", title: "拦截规则", url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: "资产拦截", url: "/system/intercept/assets", icon: Ban },
      { id: "approvals", title: "审批记录", url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "notify", title: "通知推送", url: "/system/notify", icon: BellRing },
      { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
      { id: "settings", title: "系统配置", url: "/system/settings", icon: Settings2 },
    ],
  },
];

/** 侧边栏折叠状态持久化键；值形如 {"1":true,"4":false}。
 *  v2：默认值从「只展开作战」改为「全展开」，键升版以丢弃旧的 {1:true} 存量，
 *  否则老用户永远看不到被折叠的 19 个条目。 */
export const SIDEBAR_GROUP_STORAGE_KEY = "sidebarOpenGroupsV2";

/** 默认展开的域：全部。分组只是导航手段，不该成为「功能看不见」的门槛——
 *  用户手动收起的域会写进 storage，后续刷新尊重用户的选择。 */
export const DEFAULT_OPEN_GROUP_IDS: readonly number[] = [1, 2, 3, 4, 5];
