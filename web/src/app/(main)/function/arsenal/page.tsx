"use client";

// 武器库：C2 与 Webshell 都只是「适配层」——引擎分别是 Metasploit RPC 与
// 目标上的 shell 协议，这里只负责展示、下发与留痕。

import * as React from "react";

import { useTranslations } from "next-intl";

import { Loader2Icon, PlugZapIcon, RefreshCwIcon, SendIcon, ShieldIcon, TerminalIcon, Trash2Icon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StateBoundary } from "@/components/data/state-boundary";
import { PageShell } from "@/components/shell/page-shell";
import { Toolbar } from "@/components/shell/toolbar";
import { usePermissionDenied } from "@/hooks/use-permission-denied";
import { ApiError } from "@/lib/api";
import { errorCodeOf, errorMessageOf } from "@/lib/api-error";
import { auth } from "@/lib/auth";
import { cn } from "@/lib/utils";

type Engine = { id: number; kind: string; name: string; base_url: string; username: string; has_password: boolean };
type Session = { id: string; type: string; tunnel_peer: string; username: string; platform: string; info: string };
type Shell = { id: number; url: string; kind: string; status: string; last_check: string; remark: string; last_output: string };

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const token = auth.getToken();
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(init?.headers ?? {}) },
  });
  const text = await res.text();
  if (!res.ok) {
    // 状态码必须带出来，否则页面没法区分「失败」与「没权限」。
    throw new ApiError(text.slice(0, 200) || `HTTP ${res.status}`, res.status);
  }
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new Error(text.slice(0, 200) || `HTTP ${res.status}`);
  }
}

function Panel({ title, children, right }: { title: string; children: React.ReactNode; right?: React.ReactNode }) {
  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between">
        <div className="font-medium text-sm">{title}</div>
        {right}
      </div>
      {children}
    </Card>
  );
}

export default function ArsenalPage() {
  const t = useTranslations("nav");
  const tp = useTranslations("pageStates");
  const { denied, check, reset } = usePermissionDenied();

  const [tab, setTab] = React.useState<"c2" | "shell">("c2");
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const lastError = React.useRef<unknown>(null);

  // ---- C2 ----
  const [engines, setEngines] = React.useState<Engine[]>([]);
  const [engineID, setEngineID] = React.useState<number>(0);
  const [c2State, setC2State] = React.useState<string>("");
  const [sessions, setSessions] = React.useState<Session[]>([]);
  const [jobs, setJobs] = React.useState<Record<string, Record<string, unknown>>>({});
  const [sid, setSid] = React.useState<string>("");
  const [cmd, setCmd] = React.useState<string>("whoami");
  const [out, setOut] = React.useState<string>("");
  const [busy, setBusy] = React.useState<string>("");
  const [form, setForm] = React.useState({ base_url: "http://127.0.0.1:55553", username: "msf", password: "" });

  // ---- Webshell ----
  const [shells, setShells] = React.useState<Shell[]>([]);
  const [shForm, setShForm] = React.useState({ url: "", kind: "php-base64", secret: "pass", remark: "" });
  const [shOut, setShOut] = React.useState<string>("");
  const [shCmd, setShCmd] = React.useState<string>("id");
  const [activeShell, setActiveShell] = React.useState<number>(0);

  const loadEngines = React.useCallback(async () => {
    const r = await call<{ engines: Engine[] }>("/api/c2/engines");
    setEngines(r.engines ?? []);
    if (!engineID && r.engines?.length) setEngineID(r.engines[0].id);
  }, [engineID]);

  const loadShells = React.useCallback(async () => {
    const r = await call<{ shells: Shell[] }>("/api/shells");
    setShells(r.shells ?? []);
  }, []);

  // 首屏加载：原来两个 loader 是裸 void 调用，失败会变成 unhandled rejection
  // 且界面毫无反应；这里包一层把 loading / error / 403 落到四态上。
  const load = React.useCallback(async () => {
    setLoading(true);
    setError(null);
    lastError.current = null;
    reset();
    try {
      await Promise.all([loadEngines(), loadShells()]);
    } catch (e) {
      lastError.current = e;
      check(e);
      setError(errorMessageOf(e));
    } finally {
      setLoading(false);
    }
  }, [loadEngines, loadShells, check, reset]);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function createEngine() {
    setBusy("engine");
    try {
      await call("/api/c2/engines", { method: "POST", body: JSON.stringify({ kind: "msf-rpc", name: "Metasploit RPC", ...form }) });
      await loadEngines();
      setC2State("引擎已添加");
    } catch (e) {
      setC2State(String(e));
    } finally {
      setBusy("");
    }
  }

  async function testEngine() {
    if (!engineID) return;
    setBusy("test");
    try {
      const r = await call<{ ok: boolean; error?: string; sessions?: number }>(`/api/c2/test?engine=${engineID}`, { method: "POST" });
      setC2State(r.ok ? `连接正常，当前 ${r.sessions ?? 0} 个会话` : `连接失败：${r.error}`);
    } catch (e) {
      setC2State(String(e));
    } finally {
      setBusy("");
    }
  }

  async function loadSessions() {
    if (!engineID) return;
    setBusy("sessions");
    try {
      const r = await call<{ ok: boolean; sessions: Session[]; error?: string }>(`/api/c2/sessions?engine=${engineID}`);
      setSessions(r.sessions ?? []);
      if (!r.ok) setC2State(`拉会话失败：${r.error}`);
      const j = await call<{ ok: boolean; jobs: Record<string, Record<string, unknown>> }>(`/api/c2/jobs?engine=${engineID}`);
      setJobs(j.jobs ?? {});
    } catch (e) {
      setC2State(String(e));
    } finally {
      setBusy("");
    }
  }

  async function runCmd() {
    if (!engineID || !sid || !cmd.trim()) return;
    setBusy("exec");
    setOut("");
    try {
      const r = await call<{ ok: boolean; output?: string; error?: string }>(`/api/c2/exec?engine=${engineID}`, {
        method: "POST",
        body: JSON.stringify({ session: sid, cmd }),
      });
      setOut(r.ok ? r.output || "（无输出）" : `执行失败：${r.error}`);
    } catch (e) {
      setOut(String(e));
    } finally {
      setBusy("");
    }
  }

  async function createShell() {
    setBusy("shell");
    try {
      await call("/api/shells", { method: "POST", body: JSON.stringify(shForm) });
      await loadShells();
      setShForm({ url: "", kind: "php-base64", secret: "pass", remark: "" });
    } catch (e) {
      setShOut(String(e));
    } finally {
      setBusy("");
    }
  }

  async function shellOp(id: number, op: string, extra: Record<string, string> = {}) {
    setBusy(`shell-${op}`);
    setShOut("");
    try {
      const r = await call<{ ok: boolean; output?: string; error?: string; status?: string }>(`/api/shells/${id}/op`, {
        method: "POST",
        body: JSON.stringify({ op, ...extra }),
      });
      setShOut(r.ok ? r.output || "（无输出）" : `失败：${r.error}`);
      await loadShells();
    } catch (e) {
      setShOut(String(e));
    } finally {
      setBusy("");
    }
  }

  return (
    <PageShell
      title={t("item.arsenal")}
      breadcrumb={[{ label: t("group.capability"), href: "/dashboard" }, { label: t("item.arsenal") }]}
      actions={
        <>
          <Button variant={tab === "c2" ? "default" : "outline"} size="sm" onClick={() => setTab("c2")}>
            <PlugZapIcon className="mr-1 size-4" /> C2 会话
          </Button>
          <Button variant={tab === "shell" ? "default" : "outline"} size="sm" onClick={() => setTab("shell")}>
            <ShieldIcon className="mr-1 size-4" /> Webshell
          </Button>
        </>
      }
      toolbar={
        <Toolbar
          viewSwitch={
            <span className="font-mono text-muted-foreground text-xs">
              {tab === "c2" ? "ENGINE: METASPLOIT RPC" : "ENGINE: SHELL PROTOCOL"}
            </span>
          }
        />
      }
    >
      <div className="px-4 py-3 md:px-6">
      <StateBoundary
        loading={loading}
        error={
          error
            ? {
                title: tp("errorTitle"),
                detail: tp("errorDetail"),
                code: errorCodeOf(lastError.current),
                codeLabel: tp("errorCodeLabel"),
                retryLabel: tp("retry"),
              }
            : null
        }
        permission={denied ? "arsenal:read" : null}
        permissionProps={{
          title: tp("permissionTitle"),
          detail: tp("permissionDetail", { name: t("item.arsenal"), ask: tp("askAdmin") }),
          missingLabel: tp("permissionMissing"),
          askLabel: tp("permissionAsk"),
          ask: tp("askAdmin"),
        }}
        empty={!loading && !error && engines.length === 0 && shells.length === 0}
        emptyProps={{ title: tp("emptyTitle"), reason: tp("empty.arsenal") }}
        onRetry={() => void load()}
        skeleton={
          <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
            {[0, 1].map((i) => (
              <div key={i} className="space-y-2 rounded-lg border bg-card p-4">
                {Array.from({ length: 6 }).map((_, j) => (
                  <div key={j} className="h-9 animate-pulse rounded-md bg-muted" />
                ))}
              </div>
            ))}
          </div>
        }
      >
      {tab === "c2" ? (
        <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <div className="flex flex-col gap-4">
            <Panel title="引擎">
              <div className="flex flex-col gap-2">
                <select
                  className="h-9 rounded-md border bg-background px-2 text-sm"
                  value={engineID}
                  onChange={(e) => setEngineID(Number(e.target.value))}
                >
                  <option value={0}>未选择</option>
                  {engines.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.name} · {e.base_url}
                    </option>
                  ))}
                </select>
                <div className="flex gap-2">
                  <Button size="sm" onClick={testEngine} disabled={!engineID || busy !== ""}>
                    {busy === "test" ? <Loader2Icon className="size-4 animate-spin" /> : <PlugZapIcon className="size-4" />}
                    <span className="ml-1">测试</span>
                  </Button>
                  <Button size="sm" variant="outline" onClick={loadSessions} disabled={!engineID || busy !== ""}>
                    {busy === "sessions" ? <Loader2Icon className="size-4 animate-spin" /> : <RefreshCwIcon className="size-4" />}
                    <span className="ml-1">刷新</span>
                  </Button>
                </div>
                {c2State && <div className="text-muted-foreground text-xs">{c2State}</div>}
              </div>
            </Panel>

            <Panel title="新增引擎">
              <div className="flex flex-col gap-2">
                <Input placeholder="http://127.0.0.1:55553" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} />
                <Input placeholder="用户名 msf" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
                <Input placeholder="msfrpcd 密码" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
                <Button size="sm" onClick={createEngine} disabled={busy !== ""}>
                  添加
                </Button>
              </div>
            </Panel>

            <Panel title={`监听器（${Object.keys(jobs).length}）`}>
              {Object.keys(jobs).length === 0 ? (
                <div className="text-muted-foreground text-xs">暂无后台 job</div>
              ) : (
                <div className="flex flex-col gap-1 font-mono text-xs">
                  {Object.entries(jobs).map(([id, j]) => (
                    <div key={id} className="flex justify-between gap-2">
                      <span>#{id}</span>
                      <span className="truncate text-muted-foreground">{String(j.name ?? j.uripath ?? "")}</span>
                    </div>
                  ))}
                </div>
              )}
            </Panel>
          </div>

          <div className="flex flex-col gap-4">
            <Panel title={`会话（${sessions.length}）`}>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16">ID</TableHead>
                    <TableHead className="w-24">类型</TableHead>
                    <TableHead>对端</TableHead>
                    <TableHead className="w-28">用户</TableHead>
                    <TableHead className="w-24">平台</TableHead>
                    <TableHead className="w-16" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sessions.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground text-sm">
                        还没有会话上线
                      </TableCell>
                    </TableRow>
                  ) : (
                    sessions.map((s) => (
                      <TableRow key={s.id} className={cn(sid === s.id && "bg-accent/40")}>
                        <TableCell className="font-mono text-xs">{s.id}</TableCell>
                        <TableCell>
                          <Badge variant="secondary" className="text-xs">
                            {s.type || "—"}
                          </Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs">{s.tunnel_peer}</TableCell>
                        <TableCell className="text-xs">{s.username || "—"}</TableCell>
                        <TableCell className="text-xs">{s.platform || "—"}</TableCell>
                        <TableCell>
                          <Button size="sm" variant="ghost" onClick={() => setSid(s.id)}>
                            选中
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </Panel>

            <Panel title={`命令台${sid ? ` · session ${sid}` : ""}`}>
              <div className="flex gap-2">
                <Input
                  className="font-mono"
                  value={cmd}
                  onChange={(e) => setCmd(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") void runCmd();
                  }}
                  placeholder="命令，回车执行"
                />
                <Button size="sm" onClick={runCmd} disabled={!sid || busy !== ""}>
                  {busy === "exec" ? <Loader2Icon className="size-4 animate-spin" /> : <SendIcon className="size-4" />}
                </Button>
              </div>
              <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-muted/40 p-3 font-mono text-xs">
                {out || "输出会显示在这里"}
              </pre>
            </Panel>
          </div>
        </div>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <Panel title="新增 webshell">
            <div className="flex flex-col gap-2">
              <Input placeholder="http://target/upload/a.php" value={shForm.url} onChange={(e) => setShForm({ ...shForm, url: e.target.value })} />
              <select className="h-9 rounded-md border bg-background px-2 text-sm" value={shForm.kind} onChange={(e) => setShForm({ ...shForm, kind: e.target.value })}>
                <option value="php-base64">PHP · base64 eval</option>
                <option value="php-eval">PHP · eval</option>
                <option value="php-assert">PHP · assert</option>
                <option value="jsp">JSP · exec</option>
                <option value="aspx">ASPX · start</option>
                <option value="custom">自定义模板</option>
              </select>
              <Input placeholder="参数名/口令，如 pass" value={shForm.secret} onChange={(e) => setShForm({ ...shForm, secret: e.target.value })} />
              <Input placeholder="备注" value={shForm.remark} onChange={(e) => setShForm({ ...shForm, remark: e.target.value })} />
              <Button size="sm" onClick={createShell} disabled={busy !== ""}>
                添加
              </Button>
            </div>
          </Panel>

          <div className="flex flex-col gap-4">
            <Panel title={`已登记（${shells.length}）`} right={<Button size="sm" variant="ghost" onClick={loadShells}><RefreshCwIcon className="size-4" /></Button>}>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-12">ID</TableHead>
                    <TableHead>URL</TableHead>
                    <TableHead className="w-24">协议</TableHead>
                    <TableHead className="w-20">状态</TableHead>
                    <TableHead className="w-36">最近检测</TableHead>
                    <TableHead className="w-40" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shells.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground text-sm">
                        还没有登记 shell
                      </TableCell>
                    </TableRow>
                  ) : (
                    shells.map((s) => (
                      <TableRow key={s.id} className={cn(activeShell === s.id && "bg-accent/40")}>
                        <TableCell className="font-mono text-xs">{s.id}</TableCell>
                        <TableCell className="max-w-[320px] truncate font-mono text-xs">{s.url}</TableCell>
                        <TableCell className="text-xs">{s.kind}</TableCell>
                        <TableCell>
                          <Badge variant={s.status === "alive" ? "default" : s.status === "dead" ? "destructive" : "secondary"} className="text-xs">
                            {s.status === "alive" ? "在线" : s.status === "dead" ? "失联" : "未知"}
                          </Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs">{s.last_check || "—"}</TableCell>
                        <TableCell>
                          <div className="flex gap-1">
                            <Button size="sm" variant="ghost" onClick={() => { setActiveShell(s.id); void shellOp(s.id, "ping"); }}>
                              探活
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => setActiveShell(s.id)}>
                              选中
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => void call(`/api/shells/${s.id}`, { method: "DELETE" }).then(loadShells)}>
                              <Trash2Icon className="size-3.5" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </Panel>

            <Panel title={`操作台${activeShell ? ` · shell ${activeShell}` : ""}`}>
              <div className="flex gap-2">
                <Input className="font-mono" value={shCmd} onChange={(e) => setShCmd(e.target.value)} placeholder="命令，如 id / ls /tmp" />
                <Button size="sm" onClick={() => shellOp(activeShell, "exec", { cmd: shCmd })} disabled={!activeShell || busy !== ""}>
                  <TerminalIcon className="size-4" />
                </Button>
              </div>
              <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-muted/40 p-3 font-mono text-xs">
                {shOut || "输出会显示在这里"}
              </pre>
            </Panel>
          </div>
        </div>
      )}
      </StateBoundary>
      </div>
    </PageShell>
  );
}
