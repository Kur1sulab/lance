"use client";

import * as React from "react";

import {
  DownloadIcon,
  FilterIcon,
  InboxIcon,
  LayoutListIcon,
  PlusIcon,
  SearchIcon,
  ShieldAlertIcon,
} from "lucide-react";

import { EmptyState } from "@/components/data/empty-state";
import { ErrorState } from "@/components/data/error-state";
import { SkeletonTable } from "@/components/data/skeleton-table";
import { PermissionState, StateBoundary } from "@/components/data/state-boundary";
import { DetailPanel, type DetailPanelSegment } from "@/components/shell/detail-panel";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

const ROWS = [
  { id: "F-1042", title: "存储型 XSS · 反馈表单", asset: "app.target.cn", sev: "高", status: "待复测" },
  { id: "F-1041", title: "越权读取订单 · /api/order", asset: "api.target.cn", sev: "严重", status: "已确认" },
  { id: "F-1039", title: "SSRF 打内网元数据", asset: "img.target.cn", sev: "中", status: "已修复" },
  { id: "F-1036", title: "JWT 未校验签名", asset: "auth.target.cn", sev: "高", status: "已确认" },
  { id: "F-1033", title: "目录遍历 · 备份文件泄露", asset: "static.target.cn", sev: "中", status: "已修复" },
  { id: "F-1030", title: "弱口令 · 管理后台", asset: "admin.target.cn", sev: "严重", status: "待复测" },
  { id: "F-1027", title: "CSRF · 绑定邮箱接口", asset: "passport.target.cn", sev: "低", status: "已修复" },
  { id: "F-1024", title: "SQL 注入 · 导出筛选参数", asset: "report.target.cn", sev: "严重", status: "已确认" },
  { id: "F-1021", title: "任意文件读取 · /download", asset: "file.target.cn", sev: "高", status: "待复测" },
  { id: "F-1018", title: "未授权访问 · Prometheus 指标", asset: "monitor.target.cn", sev: "中", status: "已修复" },
  { id: "F-1015", title: "CORS 反射任意 Origin", asset: "api2.target.cn", sev: "低", status: "已确认" },
  { id: "F-1012", title: "短信轰炸 · 无频控", asset: "sms.target.cn", sev: "中", status: "已修复" },
  { id: "F-1009", title: "GraphQL 内省未关闭", asset: "gql.target.cn", sev: "低", status: "已确认" },
  { id: "F-1006", title: "SSTI · 邮件模板", asset: "mail.target.cn", sev: "严重", status: "待复测" },
  { id: "F-1003", title: "越权删除他人草稿", asset: "cms.target.cn", sev: "中", status: "已修复" },
  { id: "F-0998", title: "反序列化 · Redis 会话", asset: "sso.target.cn", sev: "严重", status: "已确认" },
  { id: "F-0995", title: "日志注入 · User-Agent", asset: "log.target.cn", sev: "低", status: "已修复" },
  { id: "F-0992", title: "开放重定向 · return_url", asset: "passport.target.cn", sev: "低", status: "待复测" },
  { id: "F-0989", title: "XXE · 发票解析接口", asset: "billing.target.cn", sev: "高", status: "已确认" },
];

const SEGMENTS: DetailPanelSegment[] = [
  {
    key: "summary",
    label: "摘要",
    content: (
      <dl className="grid grid-cols-[72px_1fr] gap-x-3 gap-y-2 text-xs">
        <dt className="text-muted-foreground">编号</dt>
        <dd className="font-mono text-foreground">F-1042</dd>
        <dt className="text-muted-foreground">资产</dt>
        <dd className="text-foreground">app.target.cn</dd>
        <dt className="text-muted-foreground">严重度</dt>
        <dd className="text-foreground">高</dd>
        <dt className="text-muted-foreground">状态</dt>
        <dd className="text-foreground">待复测</dd>
      </dl>
    ),
  },
  {
    key: "evidence",
    label: "证据",
    content: (
      <pre className="overflow-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-[11px] text-foreground leading-relaxed">
        {`POST /feedback/submit HTTP/1.1
Host: app.target.cn
Content-Type: application/json

{"content":"<img src=x onerror=alert(1)>"}
--- response 200 ---
{"ok":true}`}
      </pre>
    ),
  },
  {
    key: "repro",
    label: "复现",
    content: (
      <ol className="list-inside list-decimal space-y-1.5 text-xs text-foreground">
        <li>登录任意账号，进入「意见反馈」页。</li>
        <li>在内容框提交带事件属性的 img 标签。</li>
        <li>打开管理端反馈列表，脚本在管理员会话中执行。</li>
      </ol>
    ),
  },
];

export function ShellDemoPage() {
  const [open, setOpen] = React.useState(false);
  const [activeId, setActiveId] = React.useState("F-1042");
  const active = ROWS.find((r) => r.id === activeId) ?? ROWS[0];

  return (
    <>
      <PageShell
        className="h-[calc(100svh-13rem)] min-h-[26rem] rounded-lg border border-border"
        title="页面骨架演示"
        description="Header 与 Toolbar 固定，仅 Content 区滚动。点任意行打开右侧详情抽屉（Esc 关闭）。"
        breadcrumb={[
          { label: "平台", href: "/dashboard" },
          { label: "组件", href: "/__shell" },
          { label: "PageShell" },
        ]}
        actions={
          <>
            <Button size="sm">
              <PlusIcon />
              新建任务
            </Button>
            <Button size="sm" variant="outline">
              <DownloadIcon />
              导出
            </Button>
            <Button size="sm" variant="ghost">
              批量删除
            </Button>
            <Button size="sm" variant="ghost">
              归档
            </Button>
          </>
        }
        toolbar={
          <Toolbar
            search={
              <div className="relative">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input placeholder="搜索编号 / 标题 / 资产" className="pl-8" />
              </div>
            }
            filters={
              <>
                <Button size="sm" variant="outline">
                  <FilterIcon />
                  严重度
                </Button>
                <Button size="sm" variant="outline">
                  状态
                </Button>
              </>
            }
            viewSwitch={
              <ToggleGroup type="single" defaultValue="list" size="sm" variant="outline">
                <ToggleGroupItem value="list" aria-label="列表视图">
                  <LayoutListIcon />
                </ToggleGroupItem>
                <ToggleGroupItem value="grid" aria-label="网格视图">
                  <LayoutListIcon className="rotate-90" />
                </ToggleGroupItem>
              </ToggleGroup>
            }
          />
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-28">编号</TableHead>
              <TableHead>标题</TableHead>
              <TableHead className="w-48">资产</TableHead>
              <TableHead className="w-20">严重度</TableHead>
              <TableHead className="w-24">状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ROWS.map((r) => (
              <TableRow
                key={r.id}
                onClick={() => {
                  setActiveId(r.id);
                  setOpen(true);
                }}
                className="cursor-pointer"
              >
                <TableCell className="font-mono text-xs">{r.id}</TableCell>
                <TableCell className="text-foreground">{r.title}</TableCell>
                <TableCell className="text-muted-foreground text-xs">{r.asset}</TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    <span className="inline-block size-1.5 rounded-full bg-foreground/70" />
                    {r.sev}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-[11px]">
                    {r.status}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </PageShell>

      <DetailPanel
        open={open}
        onClose={() => setOpen(false)}
        title={active.title}
        subtitle={`${active.id} · ${active.asset}`}
        width="lg"
        segments={SEGMENTS}
      />

      {/* 四态实例 —— 故意放在骨架外，便于单独看 */}
      <section className="mt-6 space-y-6">
        <h2 className="font-heading font-medium text-muted-foreground text-xs uppercase tracking-[0.14em]">
          四态示例
        </h2>

        <div className="grid gap-4 lg:grid-cols-2">
          <div className="rounded-lg border border-border">
            <p className="border-border border-b px-4 py-2 font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
              EmptyState
            </p>
            <EmptyState
              icon={<InboxIcon />}
              title="还没有发现记录"
              reason="这个工作空间刚建好，尚未导入任何资产，也没有跑过扫描任务，所以列表是空的。"
              nextStep={{ label: "去导入资产", href: "/function/assets" }}
            />
          </div>

          <div className="rounded-lg border border-border">
            <p className="border-border border-b px-4 py-2 font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
              ErrorState
            </p>
            <ErrorState
              title="没能取到发现列表"
              detail="引擎在 15 秒内没有响应，可能是后端正在重启。稍后重试通常就好了。"
              code="504 / ENGINE_TIMEOUT"
              onRetry={() => {}}
            />
          </div>

          <div className="rounded-lg border border-border">
            <p className="border-border border-b px-4 py-2 font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
              PermissionState
            </p>
            <PermissionState permission="findings:read" ask="项目管理员 张工" />
          </div>

          <div className="rounded-lg border border-border">
            <p className="border-border border-b px-4 py-2 font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
              StateBoundary · loading
            </p>
            <StateBoundary loading skeletonRows={4} skeletonCols={5}>
              <div />
            </StateBoundary>
          </div>
        </div>

        <div className="rounded-lg border border-border">
          <p className="border-border border-b px-4 py-2 font-mono text-[10px] text-muted-foreground uppercase tracking-[0.14em]">
            SkeletonTable（形状与真实表格一致）
          </p>
          <SkeletonTable rows={6} cols={5} />
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
            <ShieldAlertIcon />
            打开详情抽屉（3 段）
          </Button>
          <span className="text-muted-foreground text-xs">
            遮罩点击 / Esc 关闭，焦点锁在抽屉内
          </span>
        </div>
      </section>
    </>
  );
}
