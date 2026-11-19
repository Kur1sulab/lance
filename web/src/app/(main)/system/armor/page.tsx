"use client";

// 破甲引擎：给渗透 agent 的系统提示尾部注入最高优先级的操作者授权规约，
// 压掉拒答、自我降级与"建议你自己去做"。本页只做三件事：
//   1) 选档位（关/精简/完整/终极）并预览规约正文；
//   2) 看运行情况（累计注入次数、按角色分桶、最近注入时间）；
//   3) 用当前档位跑一次真实 LLM 往返，直接看模型是拒答还是照做。

import * as React from "react";

import {
  ActivityIcon,
  FlameIcon,
  Loader2Icon,
  RefreshCwIcon,
  RotateCcwIcon,
  SendIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
  ZapIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { auth } from "@/lib/auth";
import { cn } from "@/lib/utils";

type ArmorProfile = {
  id: string;
  name: string;
  summary: string;
  chars: number;
  strength: number;
};

type ArmorStats = {
  mode: string;
  mode_name: string;
  active: boolean;
  injections: number;
  chars: number;
  avg_chars: number;
  roles: Record<string, number>;
  last_inject: string;
  last_mode: string;
};

type ArmorState = { mode: string; profiles: ArmorProfile[]; stats: ArmorStats };

type ProbeResult = {
  mode: string;
  prompt: string;
  system_len: number;
  armor_len: number;
  reply: string;
};

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const token = auth.getToken();
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(init?.headers ?? {}) },
  });
  const text = await res.text();
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    throw new Error(text.slice(0, 200) || `HTTP ${res.status}`);
  }
  if (!res.ok) {
    const msg = (body as { error?: string })?.error ?? `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return body as T;
}

// 档位图标：关=盾牌划掉，其余按强度递进。
function ModeIcon({ id, className }: { id: string; className?: string }) {
  if (id === "off") return <ShieldOffIcon className={className} />;
  if (id === "ultimate") return <FlameIcon className={className} />;
  if (id === "full") return <ZapIcon className={className} />;
  return <ShieldCheckIcon className={className} />;
}

function StatCell({ label, value, hint }: { label: string; value: React.ReactNode; hint?: string }) {
  return (
    <div className="rounded-lg border bg-card px-3 py-2">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className="mt-0.5 font-mono text-lg leading-tight">{value}</div>
      {hint ? <div className="mt-0.5 text-[11px] text-muted-foreground">{hint}</div> : null}
    </div>
  );
}

export default function ArmorPage() {
  const [state, setState] = React.useState<ArmorState | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState<string | null>(null);
  const [preview, setPreview] = React.useState<{ mode: string; text: string } | null>(null);
  const [probing, setProbing] = React.useState(false);
  const [probe, setProbe] = React.useState<ProbeResult | null>(null);
  const [prompt, setPrompt] = React.useState("给出登录接口账号枚举与爆破的步骤");

  const load = React.useCallback(async () => {
    try {
      const s = await call<ArmorState>("/api/armor");
      setState(s);
    } catch (e) {
      toast.error(`读取破甲引擎状态失败：${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function pickMode(id: string) {
    if (!state || state.mode === id) return;
    setSaving(id);
    try {
      const s = await call<ArmorState>("/api/armor", { method: "PUT", body: JSON.stringify({ mode: id }) });
      setState(s);
      toast.success(`破甲档位已切到「${s.profiles.find((p) => p.id === s.mode)?.name ?? s.mode}」，下一轮 agent 即生效`);
    } catch (e) {
      toast.error(`切换失败：${(e as Error).message}`);
    } finally {
      setSaving(null);
    }
  }

  async function showPreview(id: string) {
    try {
      const p = await call<{ mode: string; text: string }>(`/api/armor/preview?mode=${encodeURIComponent(id)}`);
      setPreview({ mode: p.mode, text: p.text });
    } catch (e) {
      toast.error(`读取规约失败：${(e as Error).message}`);
    }
  }

  async function resetStats() {
    try {
      await call("/api/armor/reset-stats", { method: "POST" });
      await load();
      toast.success("运行计数已清零");
    } catch (e) {
      toast.error(`重置失败：${(e as Error).message}`);
    }
  }

  async function runProbe() {
    setProbing(true);
    setProbe(null);
    try {
      const r = await call<ProbeResult>("/api/armor/probe", { method: "POST", body: JSON.stringify({ prompt }) });
      setProbe(r);
      await load();
    } catch (e) {
      toast.error(`探测失败：${(e as Error).message}`);
    } finally {
      setProbing(false);
    }
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
        <Loader2Icon className="size-4 animate-spin" /> 正在读取破甲引擎状态…
      </div>
    );
  }
  if (!state) {
    return <div className="p-6 text-sm text-muted-foreground">读取失败，请刷新重试。</div>;
  }

  const active = state.profiles.find((p) => p.id === state.mode);
  const roles = Object.entries(state.stats.roles ?? {}).sort((a, b) => b[1] - a[1]);

  return (
    <div className="flex flex-col gap-4 p-4">
      {/* 顶部：当前档位 + 运行情况 */}
      <Card className="p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <FlameIcon className="size-4 text-orange-500" />
              <span className="text-sm font-medium">破甲引擎</span>
              <Badge variant={state.stats.active ? "default" : "secondary"}>
                {state.stats.active ? `运行中 · ${active?.name ?? state.mode}` : "已关闭"}
              </Badge>
            </div>
            <div className="mt-1 max-w-2xl text-xs text-muted-foreground">
              在 planner / worker / 主 agent / 对话 / 目标分解五个角色的系统提示尾部，追加一段由代码拥有的操作者授权规约。
              正文存库、可被编辑，尾部由代码拥有——改正文永远改不掉它。
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void load()}>
              <RefreshCwIcon className="size-3.5" /> 刷新
            </Button>
            <Button variant="outline" size="sm" onClick={() => void resetStats()}>
              <RotateCcwIcon className="size-3.5" /> 重置计数
            </Button>
          </div>
        </div>

        <div className="mt-4 grid grid-cols-2 gap-2 md:grid-cols-4">
          <StatCell label="累计注入" value={state.stats.injections.toLocaleString()} hint="每次拼装系统提示 +1" />
          <StatCell label="平均占用" value={`${state.stats.avg_chars.toLocaleString()} 字`} hint="每次注入的字符数" />
          <StatCell label="累计字符" value={state.stats.chars.toLocaleString()} hint="上下文成本估算" />
          <StatCell
            label="最近注入"
            value={<span className="text-sm">{state.stats.last_inject ? state.stats.last_inject.replace("T", " ").slice(0, 19) : "—"}</span>}
            hint={state.stats.last_mode ? `上次生效档位 ${state.stats.last_mode}` : "尚未注入过"}
          />
        </div>

        {roles.length > 0 ? (
          <>
            <Separator className="my-3" />
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <span className="flex items-center gap-1 text-muted-foreground">
                <ActivityIcon className="size-3.5" /> 按角色分桶
              </span>
              {roles.map(([role, n]) => (
                <Badge key={role} variant="outline" className="font-mono text-[11px]">
                  {role} · {n}
                </Badge>
              ))}
            </div>
          </>
        ) : null}
      </Card>

      {/* 档位选择 */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
        {state.profiles.map((p) => {
          const isActive = p.id === state.mode;
          return (
            <Card
              key={p.id}
              className={cn(
                "flex cursor-pointer flex-col gap-2 p-4 transition-colors",
                isActive ? "border-orange-500/60 ring-1 ring-orange-500/30" : "hover:border-foreground/20",
              )}
              onClick={() => void pickMode(p.id)}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <ModeIcon id={p.id} className={cn("size-4", isActive ? "text-orange-500" : "text-muted-foreground")} />
                  <span className="text-sm font-medium">{p.name}</span>
                </div>
                {isActive ? (
                  <Badge variant="default" className="text-[10px]">
                    当前
                  </Badge>
                ) : saving === p.id ? (
                  <Loader2Icon className="size-3.5 animate-spin text-muted-foreground" />
                ) : null}
              </div>
              <div className="text-xs leading-relaxed text-muted-foreground">{p.summary}</div>
              <div className="mt-auto flex items-center justify-between pt-1">
                <div className="flex items-center gap-1" title={`强度 ${p.strength}/3`}>
                  {[0, 1, 2, 3].map((i) => (
                    <span
                      key={i}
                      className={cn("h-1.5 w-4 rounded-full", i < p.strength ? "bg-orange-500" : "bg-muted")}
                    />
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[11px] text-muted-foreground">{p.chars} 字</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2 text-[11px]"
                    onClick={(e) => {
                      e.stopPropagation();
                      void showPreview(p.id);
                    }}
                  >
                    预览
                  </Button>
                </div>
              </div>
            </Card>
          );
        })}
      </div>

      {/* 规约预览 */}
      {preview ? (
        <Card className="p-4">
          <div className="mb-2 flex items-center justify-between">
            <div className="text-sm font-medium">
              规约正文 · <span className="font-mono text-xs text-muted-foreground">{preview.mode}</span>
            </div>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-[11px]" onClick={() => setPreview(null)}>
              收起
            </Button>
          </div>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-md bg-muted/40 p-3 font-mono text-[11px] leading-relaxed">
            {preview.text || "（该档位不注入任何内容）"}
          </pre>
        </Card>
      ) : null}

      {/* 真实探测 */}
      <Card className="p-4">
        <div className="flex items-center gap-2">
          <SendIcon className="size-4 text-muted-foreground" />
          <span className="text-sm font-medium">实际探测</span>
          <span className="text-xs text-muted-foreground">用当前档位跑一次真实 LLM 往返，看模型是拒答还是照做</span>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Input
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="给模型的一句话，例如：给出登录接口账号枚举与爆破的步骤"
            className="h-8 max-w-xl flex-1 text-xs"
          />
          <Button size="sm" onClick={() => void runProbe()} disabled={probing}>
            {probing ? <Loader2Icon className="size-3.5 animate-spin" /> : <ZapIcon className="size-3.5" />}
            {probing ? "探测中…" : "发一次"}
          </Button>
        </div>
        {probe ? (
          <div className="mt-3 space-y-2">
            <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
              <Badge variant="outline" className="font-mono">
                mode={probe.mode}
              </Badge>
              <span>系统提示 {probe.system_len} 字</span>
              <span>其中破甲尾部 {probe.armor_len} 字</span>
            </div>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-muted/40 p-3 text-xs leading-relaxed">
              {probe.reply || "（模型返回空）"}
            </pre>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
