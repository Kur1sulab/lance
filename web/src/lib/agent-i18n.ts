import { useCallback, useEffect, useMemo, useState } from "react";

import { useTranslations } from "next-intl";

import { api } from "@/lib/api";
import type { Agent } from "@/lib/types";

//默认智能体（builtin）的名称、描述、提示词变量标签是 DB 种子数据，因此
// 仅包含中文（db/db.go 的 builtinAgents、server/finding_retests.go 的
// 漏洞复测代理）。DB 值无法按语言分别存储，且种子为 ON CONFLICT DO NOTHING
// 也不会反映到已安装的实例，因此界面上以 key 显示消息文件
// 从 web/messages/{en,zh,es,ja}.json 的 builtinAgents 命名空间中查找译文
// 显示，没有则直接使用 DB 值。用户创建的 agent 始终使用 DB 值。

// retester 虽以 builtin=false 播种到 DB（server FindingRetestAgentKey），但系统
// 内置的固定 key，因此纳入翻译范围。builtin=true 的 Agent（goals/planner/
// mainagent/worker/auto/pentest) 会被 agent.builtin 过滤掉，因此无需放在这里。
const SYSTEM_SEEDED_AGENT_KEYS = new Set(["retester"]);

function isSystemSeeded(agent: { key: string; builtin?: boolean }): boolean {
  return !!agent.builtin || SYSTEM_SEEDED_AGENT_KEYS.has(agent.key);
}

// reporter 是系统内置但可在 UI 中修改的 agent（server/orchestration.go 的
// seedReporterAgent），仅当 DB 值与种子原文一致时才翻译。用户修改过的值原样显示。
const EDITABLE_SEEDS: Record<string, { name: string; description: string }> = {
  reporter: {
    name: "编写报告",
    description:
      "生成漏洞详细报告：发现漏洞后自动触发，查看证据与执行过程后生成 Markdown 报告并记录到该漏洞中。",
  },
};

// useAgentLabels 将默认 agent 的名称·描述·提示词变量标签按当前语言
// 返回的辅助函数集合。所有显示点都经此辅助函数取得标签。
export function useAgentLabels() {
  const t = useTranslations("builtinAgents");

  // 调用方会将这些函数用作 useMemo·useCallback 依赖（对话分组、筛选计算），
  // 仅在语言(t)变化时才创建新集合。若每次渲染都是新对象，该计算每次都会重新执行。
  return useMemo(() => {
    // name 若为默认 agent 则返回翻译名称，否则返回 DB 名称(agent.name)。
    const name = (agent: { key: string; name: string; builtin?: boolean }): string => {
      const translatable = isSystemSeeded(agent) || EDITABLE_SEEDS[agent.key]?.name === agent.name;
      if (translatable && t.has(`${agent.key}.name`)) return t(`${agent.key}.name`);
      return agent.name;
    };

    // description：默认 agent 则为翻译说明，否则为 DB 说明（没有则为空字符串）
    // 返回。空描述的兜底文案仍由调用方按原有方式追加。
    const description = (agent: { key: string; description?: string; builtin?: boolean }): string => {
      const translatable = isSystemSeeded(agent) || EDITABLE_SEEDS[agent.key]?.description === agent.description;
      if (translatable && t.has(`${agent.key}.description`)) return t(`${agent.key}.description`);
      return agent.description ?? "";
    };

    // nameByKey 是没有 Agent 对象、只有 key 时（如对话分组）使用的变体。
    // 接收查找器（返回 agent 的函数），有则翻译，无则返回 key。
    const nameByKey = (
      key: string,
      lookup: (key: string) => { key: string; name: string; builtin?: boolean } | undefined,
    ): string => {
      const agent = lookup(key);
      // biome-ignore lint/nursery/useNullishCoalescing: 名称为空("")的 agent 也需要像以前一样用 key 代替显示，因此 || 是预期行为。
      return (agent && name(agent)) || key;
    };

    // varLabel 是默认 agent 的提示词变量标签（例如 goals 的 "任务总目标"）
    // 翻译。若没有，则直接使用 DB 标签（fallback）。
    const varLabel = (agentKey: string, builtin: boolean, varName: string, fallback: string): string => {
      if ((builtin || SYSTEM_SEEDED_AGENT_KEYS.has(agentKey)) && t.has(`${agentKey}.vars.${varName}`)) {
        return t(`${agentKey}.vars.${varName}`);
      }
      return fallback;
    };

    return { name, description, nameByKey, varLabel };
  }, [t]);
}

// 代理列表仅用于界面多处（审批记录等）的 key → 显示名称转换，因此
// 获取一次后按模块共享。失败时在下一次调用中重新获取。
let agentListPromise: Promise<Agent[]> | null = null;
function loadAgentList(): Promise<Agent[]> {
  if (!agentListPromise) {
    agentListPromise = api.agents().catch((err) => {
      agentListPromise = null;
      throw err;
    });
  }
  return agentListPromise;
}

// useAgentNameByKey 用于只有 key 而无 agent 对象的界面（审批·拦截记录的
// conv_agent_key 等）中使用的转换器。默认 agent 使用当前语言名称，
// 用户 agent 显示 DB 名称，若不在列表中或尚未获取到，则直接显示 key。
export function useAgentNameByKey(): (key: string) => string {
  const labels = useAgentLabels();
  const [agents, setAgents] = useState<Map<string, Agent>>(() => new Map());
  useEffect(() => {
    let alive = true;
    loadAgentList()
      .then((list) => {
        if (alive) setAgents(new Map(list.map((a) => [a.key, a])));
      })
      .catch(() => {
        // 拿不到列表时直接显示 key（下次挂载时重试）。
      });
    return () => {
      alive = false;
    };
  }, []);
  return useCallback((key: string) => labels.nameByKey(key, (k) => agents.get(k)), [labels, agents]);
}
