"use client";

import * as React from "react";

import {
  BuildingIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  GlobeIcon,
  KeyRoundIcon,
  LayoutTemplateIcon,
  LinkIcon,
  type LucideIcon,
  NetworkIcon,
  RefreshCwIcon,
  SmartphoneIcon,
  Trash2Icon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { AssetDslSearch } from "@/components/asset-dsl-search";
import { StateBoundary } from "@/components/data/state-boundary";
import { AssetDrawer } from "@/components/domain/asset-drawer";
import { ScopeTextEditor } from "@/components/scope-text-editor";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useDetailParam } from "@/hooks/use-detail-param";
import { usePermissionDenied } from "@/hooks/use-permission-denied";
import { api } from "@/lib/api";
import { errorCodeOf } from "@/lib/api-error";
import { parseCompanyScopeText } from "@/lib/company-scope";
import type { Asset, Company, CompanyScopeRule } from "@/lib/types";
import { cn } from "@/lib/utils";

const METHOD_COLOR: Record<string, string> = {
  GET: "bg-emerald-100 text-emerald-700",
  POST: "bg-blue-100 text-blue-700",
  PUT: "bg-amber-100 text-amber-700",
  PATCH: "bg-orange-100 text-orange-700",
  DELETE: "bg-red-100 text-red-700",
  HEAD: "bg-purple-100 text-purple-700",
  OPTIONS: "bg-slate-100 text-slate-600",
};

function MethodBadge({ method }: { method: string }) {
  const m = method.toUpperCase();
  return (
    <span
      className={cn(
        "inline-block rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold leading-none",
        METHOD_COLOR[m] ?? "bg-muted text-muted-foreground",
      )}
    >
      {m || "—"}
    </span>
  );
}

function statusTone(code: number) {
  if (code >= 500) return "text-red-700 dark:text-red-500";
  if (code >= 400) return "text-amber-700 dark:text-amber-500";
  if (code >= 300) return "text-blue-700 dark:text-blue-500";
  if (code >= 200) return "text-emerald-700 dark:text-emerald-500";
  return "text-muted-foreground";
}

const PAGE_SIZES = [25, 50, 100, 200];

const TABS: { key: string; labelKey: string; icon: LucideIcon }[] = [
  { key: "company", labelKey: "company", icon: BuildingIcon },
  { key: "root_domain", labelKey: "rootDomain", icon: GlobeIcon },
  { key: "ip", labelKey: "ip", icon: NetworkIcon },
  { key: "subdomain", labelKey: "subdomain", icon: GlobeIcon },
  { key: "app", labelKey: "app", icon: SmartphoneIcon },
  { key: "service", labelKey: "service", icon: LayoutTemplateIcon },
  { key: "endpoint", labelKey: "endpoint", icon: LinkIcon },
];

function AssetsPageInner() {
  const t = useTranslations("assets");
  // 壳层：面包屑走 nav，四态文案走 pageStates（文案全部来自 messages，不写死）。
  const tn = useTranslations("nav");
  const tp = useTranslations("pageStates");
  const perm = usePermissionDenied();
  // perm 每次渲染都是新对象；取它内部稳定的 check，避免把它放进 useCallback 依赖后
  // 让 loadMeta 的标识每渲染都变、进而让下面的 useEffect 反复触发。
  const checkPermission = perm.check;
  const lastError = React.useRef<unknown>(null);
  // 资产列表这一路自己的错误码：用来区分「DSL 语法错(400)」与「真的没取到数」，
  // 不能被企业/计数那一路的错误码串扰。
  const assetsErrorCode = React.useRef<string | number | undefined>(undefined);
  // 详情抽屉：?id= 与 URL 双向同步（设计文档 §四）
  const detail = useDetailParam();
  // 点开时先把行对象递给抽屉，省一次 /assets 请求；深链还原时抽屉自己查。
  const [detailAsset, setDetailAsset] = React.useState<Asset | null>(null);
  const [rows, setRows] = React.useState<Asset[]>([]);
  const [total, setTotal] = React.useState(0);
  const [companies, setCompanies] = React.useState<Company[]>([]);
  const [counts, setCounts] = React.useState<Record<string, number>>({});
  const [tab, setTab] = React.useState("company");
  const [query, setQuery] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [page, setPage] = React.useState(0);
  const [size, setSize] = React.useState(50);
  const [dslError, setDslError] = React.useState("");
  const [refreshKey, setRefreshKey] = React.useState(0);
  const assetsRequestRef = React.useRef(0);
  const [rowsTab, setRowsTab] = React.useState("");
  // 企业/计数这一路没有 loading 标记（原先靠 companies.length === 0 就地提示），
  // 四态需要一个「这一路已经回来过」的真实信号，否则首屏会把「还没回来」画成「没有企业」。
  const [metaLoaded, setMetaLoaded] = React.useState(false);

  // asset selection & delete
  const [selected, setSelected] = React.useState<Set<number>>(new Set());
  const [deleteIds, setDeleteIds] = React.useState<number[]>([]);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [deleting, setDeleting] = React.useState(false);

  // company delete
  const [companyDeleteTarget, setCompanyDeleteTarget] = React.useState<Company | null>(null);
  const [companyDeleteAssets, setCompanyDeleteAssets] = React.useState(false);
  const [companyDeleting, setCompanyDeleting] = React.useState(false);

  // Companies + per-type counts (tab badges) — loaded on demand, no background polling.
  const loadMeta = React.useCallback(() => {
    api
      .companies()
      .then(setCompanies)
      .catch((e) => {
        /* Keep the last successful company snapshot on a transient failure. */
        lastError.current = e;
        checkPermission(e);
      })
      .finally(() => setMetaLoaded(true));
    api
      .assetCounts()
      .then(setCounts)
      .catch((e) => {
        /* Keep the last successful counters on a transient failure. */
        lastError.current = e;
        checkPermission(e);
      });
  }, [checkPermission]);

  // Manual refresh: reload counts/companies and re-fetch the current page.
  const refresh = React.useCallback(() => {
    loadMeta();
    setRefreshKey((k) => k + 1);
    setSelected(new Set());
  }, [loadMeta]);

  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = (ids: number[]) => {
    setSelected((prev) => {
      const allSelected = ids.every((id) => prev.has(id));
      const next = new Set(prev);
      if (allSelected) {
        ids.forEach((id) => {
          next.delete(id);
        });
      } else {
        ids.forEach((id) => {
          next.add(id);
        });
      }
      return next;
    });
  };

  const openDelete = (ids: number[]) => {
    setDeleteIds(ids);
    setDeleteOpen(true);
  };

  const confirmDelete = async () => {
    setDeleting(true);
    try {
      const res = await api.deleteAssets(deleteIds);
      toast.success(t("toast.deleted", { count: res.deleted }));
      setSelected(new Set());
      refresh();
    } catch (e) {
      toast.error(t("toast.deleteFailed", { msg: String((e as Error)?.message ?? e) }));
    } finally {
      setDeleting(false);
      setDeleteOpen(false);
    }
  };

  const confirmDeleteCompany = async () => {
    if (!companyDeleteTarget) return;
    setCompanyDeleting(true);
    try {
      const res = await api.deleteCompany(companyDeleteTarget.id, companyDeleteAssets);
      const msg =
        companyDeleteAssets && res.assets_deleted > 0
          ? t("toast.companyDeletedWithAssets", { count: res.assets_deleted })
          : t("toast.companyDeleted");
      toast.success(msg);
      refresh();
    } catch (e) {
      toast.error(t("toast.deleteFailed", { msg: String((e as Error)?.message ?? e) }));
    } finally {
      setCompanyDeleting(false);
      setCompanyDeleteTarget(null);
      setCompanyDeleteAssets(false);
    }
  };

  React.useEffect(() => {
    loadMeta();
  }, [loadMeta]);

  // Reset query + page + selection when switching tabs
  // biome-ignore lint/correctness/useExhaustiveDependencies: tab changes intentionally reset tab-local controls.
  React.useEffect(() => {
    setQuery("");
    setDslError("");
    setSelected(new Set());
    setRows([]);
    setRowsTab("");
    setTotal(0);
    setLoaded(false);
  }, [tab]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: these controls intentionally reset server pagination.
  React.useEffect(() => setPage(0), [tab, size, query]);

  const dslMode = query.trim() !== "";

  // Server-side paginated page loader for the active data tab (company tab excluded).
  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshKey is an explicit manual-reload trigger.
  React.useEffect(() => {
    if (tab === "company") return;
    const request = ++assetsRequestRef.current;
    const dsl = query.trim();
    setLoading(true);
    const offset = page * size;
    const run = async () => {
      try {
        const r = dsl ? await api.searchAssets(dsl, tab, size, offset) : await api.assets(tab, size, offset);
        if (assetsRequestRef.current !== request) return;
        setRows(r.assets);
        setRowsTab(tab);
        setTotal(r.total);
        setDslError("");
        assetsErrorCode.current = undefined;
      } catch (e) {
        if (assetsRequestRef.current !== request) return;
        lastError.current = e;
        checkPermission(e);
        assetsErrorCode.current = errorCodeOf(e);
        setDslError(String((e as Error)?.message ?? e));
        setRows([]);
        setRowsTab(tab);
        setTotal(0);
      } finally {
        if (assetsRequestRef.current === request) {
          setLoading(false);
          setLoaded(true);
        }
      }
    };
    const tid = setTimeout(run, dsl ? 400 : 0);
    return () => {
      clearTimeout(tid);
      if (assetsRequestRef.current === request) assetsRequestRef.current++;
    };
  }, [tab, page, size, query, refreshKey]);

  React.useEffect(() => {
    if (tab === "company") return;
    const lastPage = Math.max(0, Math.ceil(total / size) - 1);
    if (page > lastPage) setPage(lastPage);
  }, [page, size, tab, total]);

  const companyById = React.useMemo(() => {
    const m = new Map<number, string>();
    for (const c of companies) m.set(c.id, c.name);
    return m;
  }, [companies]);

  const companyName = (id?: number) => (id ? (companyById.get(id) ?? "") : "");

  const tabCounts: Record<string, number> = { ...counts, company: companies.length };
  const totalAssets = Object.values(counts).reduce((a, b) => a + b, 0);

  // rows are already the current server-side page.
  const tabData = (type: string): Asset[] => (rowsTab === type ? rows : []);
  const currentRows = tabData(tab);
  const slice = <T,>(list: T[]) => list;

  const searchBox = (
    <AssetDslSearch
      query={query}
      onChange={setQuery}
      loading={loading}
      error={dslError}
      count={dslMode ? total : undefined}
    />
  );

  // ── 四态判定（全部来自页面上已有的取数结果，不新增请求）────────────────
  // 400 = DSL 语法错：留在搜索框里就地提示（原有行为），不当成整页加载失败。
  const loadFailed = dslError !== "" && assetsErrorCode.current !== 400;
  const permissionProps = {
    title: tp("permissionTitle"),
    detail: tp("permissionDetail", { name: tn("item.assets"), ask: tp("askAdmin") }),
    missingLabel: tp("permissionMissing"),
    askLabel: tp("permissionAsk"),
    ask: tp("askAdmin"),
  };
  const tableSkeleton = (
    <Card className="overflow-hidden py-0">
      <div className="space-y-2 p-3">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-9 animate-pulse rounded-md bg-muted" />
        ))}
      </div>
    </Card>
  );
  // 数据 tab 的空态 = 取数成功 + 这一 tab 真的 0 条 + 没有在搜。
  // 搜了却 0 条不是「平台没有资产」，那种情况仍交给表格自己的 emptyData 行。
  const tableState: Omit<React.ComponentProps<typeof StateBoundary>, "children"> = {
    // 只在「这个 tab 还没拿到过第一轮数据」时铺骨架；翻页/改搜索条件时保留旧表格，
    // 加载中的反馈仍由搜索框与刷新按钮上的 spinner 给（避免每敲一个字整块表格闪一下）。
    loading: loading && !loaded,
    error: loadFailed
      ? {
          title: tp("errorTitle"),
          detail: tp("errorDetail"),
          code: assetsErrorCode.current,
          codeLabel: tp("errorCodeLabel"),
          retryLabel: tp("retry"),
        }
      : null,
    permission: perm.denied ? "assets:read" : null,
    permissionProps,
    empty: !loading && !loadFailed && loaded && total === 0 && !dslMode,
    emptyProps: { title: tp("emptyTitle"), reason: tp("empty.assets") },
    skeleton: tableSkeleton,
    onRetry: refresh,
  };

  // 资产表在子组件里，行上带 data-asset-id，这里统一代理点击：
  // 点复选框/按钮/链接不算「点行」，避免勾选时把抽屉弹开。
  function openAssetFromEvent(event: React.MouseEvent<HTMLDivElement>) {
    const target = event.target as HTMLElement;
    if (target.closest('a,button,input,select,textarea,[role="checkbox"],[data-no-row-open]')) return;
    const row = target.closest<HTMLElement>("[data-asset-id]");
    const raw = row?.dataset.assetId;
    if (!raw) return;
    if (window.getSelection()?.toString()) return;
    const idNum = Number(raw);
    setDetailAsset(rows.find((item) => item.id === idNum) ?? null);
    detail.openDetail(raw);
  }

  return (
    <>
      <PageShell
        title={t("title")}
        breadcrumb={[{ label: tn("group.target"), href: "/dashboard" }, { label: tn("item.assets") }]}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
              <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> {t("refresh")}
            </Button>
            <CompanyDialog onSaved={refresh} />
          </>
        }
        toolbar={
          <Toolbar
            search={tab === "company" ? undefined : searchBox}
            viewSwitch={
              <>
                <span className="text-xs text-muted-foreground">
                  {t.rich("totalAssets", {
                    count: totalAssets,
                    n: (chunks) => <span className="tabular-nums">{chunks}</span>,
                  })}
                </span>
                {selected.size > 0 && (
                  <Button variant="destructive" size="sm" onClick={() => openDelete(Array.from(selected) as number[])}>
                    <Trash2Icon className="size-3.5" /> {t("deleteSelected", { count: selected.size })}
                  </Button>
                )}
              </>
            }
          />
        }
      >
        <div className="flex h-full min-h-0 flex-col gap-4 px-4 py-3 md:px-6" onClick={openAssetFromEvent}>
          <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-4">
            <div className="overflow-x-auto overflow-y-hidden">
              <TabsList variant="default">
                {TABS.map((tt) => (
                  <TabsTrigger key={tt.key} value={tt.key}>
                    <tt.icon className="size-3.5" />
                    {t(`tab.${tt.labelKey}`)}
                    <span className="ml-1 tabular-nums text-muted-foreground">{tabCounts[tt.key]}</span>
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>

            {/* 企业 */}
            <TabsContent value="company" className="mt-0 flex min-h-0 flex-1 flex-col">
              <StateBoundary
                loading={!metaLoaded && !perm.denied}
                permission={perm.denied ? "assets:read" : null}
                permissionProps={permissionProps}
                empty={metaLoaded && companies.length === 0}
                emptyProps={{ title: tp("emptyTitle"), reason: tp("empty.assets") }}
                skeleton={tableSkeleton}
                onRetry={refresh}
              >
                <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
                  <div className="min-h-0 flex-1 overflow-auto">
                    <Table>
                      <TableHeader className="sticky top-0 z-10 bg-card">
                        <TableRow>
                          <TableHead>{t("companyTable.name")}</TableHead>
                          <TableHead className="w-24 text-right">{t("companyTable.count")}</TableHead>
                          <TableHead>{t("companyTable.scope")}</TableHead>
                          <TableHead className="w-36 text-right">{t("companyTable.actions")}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {companies.map((c) => (
                          <TableRow key={c.id}>
                            <TableCell>
                              <div className="flex items-center gap-2">
                                <CompanyAvatar name={c.name} logo={c.logo} />
                                <span className="font-medium">{c.name}</span>
                              </div>
                            </TableCell>
                            <TableCell className="text-right tabular-nums text-sm">{c.asset_count}</TableCell>
                            <TableCell>
                              {c.scope?.length ? (
                                <div className="flex flex-wrap gap-1">
                                  {c.scope.map((s, i) => (
                                    <Badge key={i} variant="secondary" className="font-mono text-[11px]">
                                      {s.raw}
                                    </Badge>
                                  ))}
                                </div>
                              ) : (
                                <span className="text-xs text-muted-foreground">{t("noScope")}</span>
                              )}
                            </TableCell>
                            <TableCell className="text-right">
                              <div className="flex items-center justify-end gap-1.5">
                                <EditScopeDialog company={c} onSaved={refresh} />
                                <AppendScopeDialog company={c} onSaved={refresh} />
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="size-7 text-muted-foreground hover:text-destructive"
                                  onClick={() => {
                                    setCompanyDeleteTarget(c);
                                    setCompanyDeleteAssets(false);
                                  }}
                                  aria-label={t("deleteCompanyAria", { name: c.name })}
                                >
                                  <Trash2Icon className="size-3.5" />
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                </Card>
              </StateBoundary>
            </TabsContent>

            {/* 根域名 */}
            <TabsContent value="root_domain" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={["", t("col.domain"), t("col.icp"), t("col.ownerCompany"), ""]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("root_domain")).map((a) => (
                    <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                      <TableCell className="w-8 pr-0">
                        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                      </TableCell>
                      <TableCell className="font-mono text-xs font-medium">{a.domain}</TableCell>
                      <TableCell className="text-xs">{a.icp || "—"}</TableCell>
                      <TableCell className="text-xs">
                        {companyName(a.company_id) || <span className="text-muted-foreground">{t("unassigned")}</span>}
                      </TableCell>
                      <TableCell className="w-8 pl-0">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground hover:text-destructive"
                          onClick={() => openDelete([a.id])}
                          aria-label={t("deleteAssetAria", { target: a.domain || a.id })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </AssetCard>
              </StateBoundary>
            </TabsContent>

            {/* IP */}
            <TabsContent value="ip" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={["", "IP", t("col.cSegment"), t("col.boundDomains"), t("col.openPorts"), ""]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("ip")).map((a) => (
                    <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                      <TableCell className="w-8 pr-0">
                        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                      </TableCell>
                      <TableCell className="font-mono text-xs font-medium">{a.ip}</TableCell>
                      <TableCell className="font-mono text-xs">{a.c_segment || "—"}</TableCell>
                      <TableCell>
                        <Chips items={a.bound_domains ?? []} mono />
                      </TableCell>
                      <TableCell>
                        <Chips
                          items={(a.open_ports ?? []).map((p) => (p.service ? `${p.port}/${p.service}` : String(p.port)))}
                          mono
                        />
                      </TableCell>
                      <TableCell className="w-8 pl-0">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground hover:text-destructive"
                          onClick={() => openDelete([a.id])}
                          aria-label={t("deleteAssetAria", { target: a.ip || a.id })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </AssetCard>
              </StateBoundary>
            </TabsContent>

            {/* 子域名 */}
            <TabsContent value="subdomain" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={["", t("col.domain"), t("col.rootDomain"), t("col.recordType"), t("col.recordValue"), ""]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("subdomain")).map((a) => (
                    <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                      <TableCell className="w-8 pr-0">
                        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                      </TableCell>
                      <TableCell className="font-mono text-xs font-medium">{a.domain}</TableCell>
                      <TableCell className="font-mono text-xs">{a.root_domain || "—"}</TableCell>
                      <TableCell className="text-xs">{a.record_type || "—"}</TableCell>
                      <TableCell className="max-w-xs truncate font-mono text-xs">
                        {(Array.isArray(a.record_value) ? a.record_value.join(", ") : a.record_value) || "—"}
                      </TableCell>
                      <TableCell className="w-8 pl-0">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground hover:text-destructive"
                          onClick={() => openDelete([a.id])}
                          aria-label={t("deleteAssetAria", { target: a.domain || a.id })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </AssetCard>
              </StateBoundary>
            </TabsContent>

            {/* 应用 */}
            <TabsContent value="app" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={["", t("col.appName"), "Bundle ID", t("col.category"), t("col.icp"), ""]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("app")).map((a) => (
                    <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                      <TableCell className="w-8 pr-0">
                        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                      </TableCell>
                      <TableCell className="text-xs font-medium">{a.app_name || "—"}</TableCell>
                      <TableCell className="font-mono text-xs">{a.bundle_id || "—"}</TableCell>
                      <TableCell className="text-xs">{a.category || "—"}</TableCell>
                      <TableCell className="text-xs">{a.app_icp || "—"}</TableCell>
                      <TableCell className="w-8 pl-0">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground hover:text-destructive"
                          onClick={() => openDelete([a.id])}
                          aria-label={t("deleteAssetAria", { target: a.app_name || a.id })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </AssetCard>
              </StateBoundary>
            </TabsContent>

            {/* 服务 */}
            <TabsContent value="service" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={[
                    "",
                    t("col.service"),
                    t("col.domain"),
                    "IP",
                    t("col.port"),
                    t("col.statusCode"),
                    t("col.pageTitle"),
                    t("col.fingerprint"),
                    t("col.auth"),
                    "",
                  ]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("service")).map((a) => {
                    const isHttp = a.service_type === "http";
                    const svc = a.service_name || (isHttp ? "http" : "") || a.service_type || "";
                    let domainCell: React.ReactNode = "—";
                    if (isHttp && a.url) {
                      domainCell = (
                        <a
                          href={a.url}
                          target="_blank"
                          rel="noreferrer"
                          className="text-blue-600 hover:underline dark:text-blue-400"
                        >
                          {a.domain || a.url}
                        </a>
                      );
                    } else if (a.domain) {
                      domainCell = a.domain;
                    }
                    return (
                      <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                        <TableCell className="w-8 pr-0">
                          <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                        </TableCell>
                        <TableCell className="text-xs">
                          <Badge variant={isHttp ? "default" : "secondary"} className="font-mono text-[10px]">
                            {svc || "—"}
                          </Badge>
                        </TableCell>
                        <TableCell className="max-w-[14rem] truncate font-mono text-xs" title={a.domain || a.url}>
                          {domainCell}
                        </TableCell>
                        <TableCell className="font-mono text-xs">{a.ip || "—"}</TableCell>
                        <TableCell className="font-mono text-xs tabular-nums">{a.port || "—"}</TableCell>
                        <TableCell>
                          {a.status_code != null ? (
                            <span className={cn("font-mono text-xs font-semibold tabular-nums", statusTone(a.status_code))}>
                              {a.status_code}
                            </span>
                          ) : (
                            "—"
                          )}
                        </TableCell>
                        <TableCell className="max-w-[12rem] truncate text-xs" title={a.page_title}>
                          {a.page_title || "—"}
                        </TableCell>
                        <TableCell>
                          <Chips items={a.technologies ?? []} />
                        </TableCell>
                        <TableCell>
                          {(a.auth ?? []).length === 0 ? (
                            <span className="text-xs text-muted-foreground">—</span>
                          ) : (
                            (a.auth ?? []).map((authItem, i) => {
                              const item = authItem as Record<string, string>;
                              const label = item.type || item.username || t("authFallback");
                              return (
                                <span key={i} className="inline-flex items-center gap-1 text-[11px]">
                                  <KeyRoundIcon className="size-3 text-muted-foreground" />
                                  <span className="font-mono">{label}</span>
                                </span>
                              );
                            })
                          )}
                        </TableCell>
                        <TableCell className="w-8 pl-0">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7 text-muted-foreground hover:text-destructive"
                            onClick={() => openDelete([a.id])}
                            aria-label={t("deleteAssetAria", { target: a.url || a.id })}
                          >
                            <Trash2Icon className="size-3.5" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </AssetCard>
              </StateBoundary>
            </TabsContent>

            {/* 端点 */}
            <TabsContent value="endpoint" className="mt-0 flex min-h-0 flex-1 flex-col gap-2">
              <StateBoundary {...tableState}>
                <AssetCard
                  cols={["", t("col.method"), t("col.fullUrl"), t("col.params"), ""]}
                  loaded={loaded}
                  total={total}
                  page={page}
                  size={size}
                  onSize={setSize}
                  onPage={setPage}
                  rows={currentRows}
                  selected={selected}
                  onToggleAll={(ids) => toggleSelectAll(ids)}
                >
                  {slice(tabData("endpoint")).map((a) => (
                    <TableRow
                      key={a.id}
                      data-asset-id={a.id}
                      className={cn("cursor-pointer", selected.has(a.id) && "bg-muted/40")}
                    >
                      <TableCell className="w-8 pr-0">
                        <Checkbox checked={selected.has(a.id)} onCheckedChange={() => toggleSelect(a.id)} />
                      </TableCell>
                      <TableCell className="w-16">
                        <MethodBadge method={a.method || ""} />
                      </TableCell>
                      <TableCell className="max-w-sm truncate font-mono text-xs" title={a.url}>
                        {a.url || "—"}
                      </TableCell>
                      <TableCell>
                        <Chips
                          items={(a.params ?? []).map((p) => {
                            const item = p as Record<string, string>;
                            return item.name ? `${item.name}(${item.location || "?"})` : "?";
                          })}
                          mono
                        />
                      </TableCell>
                      <TableCell className="w-8 pl-0">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground hover:text-destructive"
                          onClick={() => openDelete([a.id])}
                          aria-label={t("deleteAssetAria", { target: a.url || a.id })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </AssetCard>
              </StateBoundary>
            </TabsContent>
          </Tabs>
        </div>
      </PageShell>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("deleteDialog.title")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t.rich("deleteDialog.body", {
                count: deleteIds.length,
                n: (chunks) => <span className="font-semibold tabular-nums">{chunks}</span>,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void confirmDelete();
              }}
              disabled={deleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleting ? t("deleting") : t("deleteDialog.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!companyDeleteTarget}
        onOpenChange={(o) => {
          if (!o) {
            setCompanyDeleteTarget(null);
            setCompanyDeleteAssets(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("deleteCompanyDialog.title", { name: companyDeleteTarget?.name ?? "" })}
            </AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-3">
                <p>{t("deleteCompanyDialog.body")}</p>
                <label
                  htmlFor="delete-assets-opt"
                  className="flex cursor-pointer items-center gap-2.5 rounded-md border p-3 hover:bg-muted/50"
                >
                  <Checkbox
                    id="delete-assets-opt"
                    checked={companyDeleteAssets}
                    onCheckedChange={(v) => setCompanyDeleteAssets(!!v)}
                  />
                  <span className="text-sm leading-snug">
                    {t("deleteCompanyDialog.alsoDeleteAssets")}
                    <span className="block text-xs text-muted-foreground">
                      {t("deleteCompanyDialog.alsoDeleteHint")}
                    </span>
                  </span>
                </label>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={companyDeleting}>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void confirmDeleteCompany();
              }}
              disabled={companyDeleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {companyDeleting ? t("deleting") : t("deleteDialog.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {/* 抽屉在滚动容器外，滚列表时不跟着动 */}
      <AssetDrawer
        id={detail.id}
        open={detail.open}
        onClose={() => {
          setDetailAsset(null);
          detail.closeDetail();
        }}
        asset={detailAsset && String(detailAsset.id) === detail.id ? detailAsset : null}
      />
    </>
  );
}

// useSearchParams 必须落在 Suspense 边界内（静态导出要求）。
export default function AssetsPage() {
  return (
    <React.Suspense fallback={null}>
      <AssetsPageInner />
    </React.Suspense>
  );
}

function AssetCard({
  cols,
  loaded,
  total,
  page,
  size,
  onSize,
  onPage,
  rows: dataRows,
  selected,
  onToggleAll,
  children,
}: {
  cols: string[];
  loaded: boolean;
  total: number;
  page: number;
  size: number;
  onSize: React.Dispatch<React.SetStateAction<number>>;
  onPage: React.Dispatch<React.SetStateAction<number>>;
  rows?: Asset[];
  selected?: Set<number>;
  onToggleAll?: (ids: number[]) => void;
  children: React.ReactNode;
}) {
  const t = useTranslations("assets");
  const tp = useTranslations("pagination");
  const childRows = React.Children.toArray(children);
  const pageCount = Math.max(1, Math.ceil(total / size));
  const start = total === 0 ? 0 : page * size + 1;
  const end = page * size + childRows.length;

  const pageIds = dataRows?.map((r) => r.id) ?? [];
  const allSelected = pageIds.length > 0 && selected != null && pageIds.every((id) => selected.has(id));
  const someSelected = selected != null && pageIds.some((id) => selected.has(id));

  return (
    <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
      <div className="min-h-0 flex-1 overflow-auto scrollbar-thin scrollbar-track-transparent">
        <Table>
          <TableHeader className="sticky top-0 z-10 bg-card">
            <TableRow>
              {cols.map((c, i) =>
                c === "" && i === 0 && onToggleAll ? (
                  <TableHead key={i} className="w-8 pr-0">
                    <Checkbox
                      checked={allSelected ? true : someSelected ? "indeterminate" : false}
                      onCheckedChange={() => onToggleAll(pageIds)}
                    />
                  </TableHead>
                ) : (
                  <TableHead key={c + i}>{c}</TableHead>
                ),
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {childRows.length > 0 ? (
              childRows
            ) : (
              <TableRow>
                <TableCell colSpan={cols.length} className="py-12 text-center text-sm text-muted-foreground">
                  {loaded ? t("emptyData") : t("loading")}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {total > 0 && (
        <div className="flex shrink-0 items-center gap-2 border-t px-3 py-1.5 text-xs text-muted-foreground">
          <Select value={String(size)} onValueChange={(v) => onSize(Number(v))}>
            <SelectTrigger size="sm" className="h-7 w-24">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {PAGE_SIZES.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}
                    {tp("perPage")}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <span className="tabular-nums">
            {start}–{end} / {total}
          </span>
          {pageCount > 1 && (
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="icon"
                className="size-7"
                disabled={page <= 0}
                onClick={() => onPage((p) => Math.max(0, p - 1))}
              >
                <ChevronLeftIcon />
              </Button>
              <span className="tabular-nums">
                {page + 1} / {pageCount}
              </span>
              <Button
                variant="outline"
                size="icon"
                className="size-7"
                disabled={page + 1 >= pageCount}
                onClick={() => onPage((p) => Math.min(pageCount - 1, p + 1))}
              >
                <ChevronRightIcon />
              </Button>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

function Chips({ items, mono }: { items: string[]; mono?: boolean }) {
  const clean = items.filter(Boolean);
  if (clean.length === 0) return <span className="text-xs text-muted-foreground">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {clean.map((s, i) => (
        <Badge key={i} variant="outline" className={cn("text-[10px]", mono && "font-mono")}>
          {s}
        </Badge>
      ))}
    </div>
  );
}

function CompanyAvatar({ name, logo }: { name: string; logo?: string }) {
  const initial = (name.trim()[0] ?? "?").toUpperCase();
  return (
    <Avatar className="size-7 shrink-0">
      {logo ? <AvatarImage src={logo} alt={name} /> : null}
      <AvatarFallback className="text-xs">{initial}</AvatarFallback>
    </Avatar>
  );
}

// 后端返回的 warnings 指向既有数据的问题（并非本次提交的行有误），保存本身已经
// 成功状态。用户需要亲自处理具体资产，仅扫一眼标题不够，因此给予更长的展示时间。
function showScopeWarnings(warnings?: string[]) {
  for (const warning of warnings ?? []) {
    toast.warning(warning, { duration: 15000 });
  }
}

function savedScopeRules(company: Company): CompanyScopeRule[] {
  return (company.scope ?? [])
    .map((scope) => ({ kind: scope.kind, value: scope.raw.trim() }))
    .filter((scope) => scope.value);
}

function savedScopeText(company: Company): string {
  return savedScopeRules(company)
    .map((scope) => scope.value)
    .join("\n");
}

// 企业添加使用与任务、LLM 编辑相同的右侧抽屉（drawer）。
function CompanyDialog({ onSaved }: { onSaved: () => void }) {
  const t = useTranslations("assets");
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [scopeText, setScopeText] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const parsedScope = React.useMemo(() => parseCompanyScopeText(scopeText), [scopeText]);

  React.useEffect(() => {
    if (!open) return;
    setName("");
    setScopeText("");
  }, [open]);

  const submit = async () => {
    if (!name.trim()) {
      toast.error(t("companyDialog.nameRequired"));
      return;
    }
    if (parsedScope.errors.length > 0) {
      toast.error(t("fixInvalidScope"));
      return;
    }
    setBusy(true);
    try {
      const res = await api.createCompany(name.trim(), parsedScope.rules);
      const added = res.scope_added ?? 0;
      const invalid = res.scope_invalid ?? 0;
      if (invalid > 0) toast.warning(t("companyDialog.createdWithInvalid", { added, invalid }));
      else toast.success(t("companyDialog.created", { added }));
      setOpen(false);
      onSaved();
    } catch (e) {
      const msg = String((e as Error)?.message ?? e);
      if (/:\s*409$/.test(msg)) toast.error(t("companyDialog.exists"));
      else toast.error(t("saveFailed", { msg }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm">
          <BuildingIcon data-icon="inline-start" /> {t("companyDialog.addButton")}
        </Button>
      </SheetTrigger>
      <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[520px]! sm:max-w-[520px]!">
        <SheetHeader className="border-b p-6">
          <SheetTitle>{t("companyDialog.title")}</SheetTitle>
          <SheetDescription>{t("companyDialog.description")}</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto p-6">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="cn-name">{t("companyDialog.nameLabel")}</FieldLabel>
              <Input
                id="cn-name"
                placeholder={t("companyDialog.namePlaceholder")}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <ScopeTextEditor id="cn-scope" value={scopeText} onValueChange={setScopeText} parsed={parsedScope} />
          </FieldGroup>
        </div>
        <SheetFooter className="flex-row justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} disabled={busy || !name.trim() || parsedScope.errors.length > 0}>
            {busy ? t("saving") : t("save")}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

// 编辑资产范围（覆盖）弹窗
function EditScopeDialog({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const t = useTranslations("assets");
  const [open, setOpen] = React.useState(false);
  const [scopeText, setScopeText] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const preservedScopeRules = React.useMemo(() => savedScopeRules(company), [company]);
  const parsedScope = React.useMemo(
    () => parseCompanyScopeText(scopeText, { preservedRules: preservedScopeRules }),
    [preservedScopeRules, scopeText],
  );

  React.useEffect(() => {
    if (!open) return;
    setScopeText(savedScopeText(company));
    setReason("");
  }, [open, company]);

  const submit = async () => {
    if (parsedScope.errors.length > 0) {
      toast.error(t("fixInvalidScope"));
      return;
    }
    setBusy(true);
    try {
      const res = await api.updateCompanyScope(company.id, parsedScope.rules, reason);
      const errCount = res.invalid ?? 0;
      if (errCount > 0) toast.warning(t("editScope.savedWithInvalid", { invalid: errCount }));
      else toast.success(t("editScope.updated", { added: res.added }));
      showScopeWarnings(res.warnings);
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error(t("saveFailed", { msg: String((e as Error)?.message ?? e) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="h-7">
          {t("editScope.button")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("editScope.title", { name: company.name })}</DialogTitle>
          <DialogDescription>{t("editScope.description")}</DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          <ScopeTextEditor
            id={`edit-company-scope-${company.id}`}
            value={scopeText}
            onValueChange={setScopeText}
            parsed={parsedScope}
          />
          <Field>
            <FieldLabel htmlFor="es-reason">{t("reasonLabel")}</FieldLabel>
            <Input
              id="es-reason"
              placeholder={t("reasonPlaceholder")}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} disabled={busy || parsedScope.errors.length > 0}>
            {busy ? t("saving") : t("editScope.overwrite")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// 添加资产范围弹窗
function AppendScopeDialog({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const t = useTranslations("assets");
  const [open, setOpen] = React.useState(false);
  const [scopeText, setScopeText] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const parsedScope = React.useMemo(() => parseCompanyScopeText(scopeText), [scopeText]);

  React.useEffect(() => {
    if (!open) return;
    setScopeText("");
    setReason("");
  }, [open]);

  const submit = async () => {
    if (parsedScope.rules.length === 0) {
      toast.error(t("appendScope.scopeRequired"));
      return;
    }
    if (parsedScope.errors.length > 0) {
      toast.error(t("fixInvalidScope"));
      return;
    }
    setBusy(true);
    try {
      const res = await api.addCompanyScope(company.id, parsedScope.rules, reason);
      const errCount = res.invalid ?? 0;
      if (errCount > 0) toast.warning(t("editScope.savedWithInvalid", { invalid: errCount }));
      else toast.success(t("appendScope.appended", { added: res.added }));
      showScopeWarnings(res.warnings);
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error(t("saveFailed", { msg: String((e as Error)?.message ?? e) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="h-7">
          {t("appendScope.button")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("appendScope.title", { name: company.name })}</DialogTitle>
          <DialogDescription>{t("appendScope.description")}</DialogDescription>
        </DialogHeader>
        <FieldGroup className="py-2">
          <ScopeTextEditor
            id={`append-company-scope-${company.id}`}
            value={scopeText}
            onValueChange={setScopeText}
            parsed={parsedScope}
          />
          <Field>
            <FieldLabel htmlFor="as-reason">{t("reasonLabel")}</FieldLabel>
            <Input
              id="as-reason"
              placeholder={t("reasonPlaceholder")}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} disabled={busy || parsedScope.rules.length === 0 || parsedScope.errors.length > 0}>
            {busy ? t("saving") : t("appendScope.button")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
