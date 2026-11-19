import { useMemo } from "react";

import { useTranslations } from "next-intl";

import toolSources from "./builtin-tool-sources.json";

//默认拦截规则名称、资产规则备注、工具说明是 DB 种子数据，无法按语言分别存放。
//仅在界面上通过消息文件（web/messages/*.json 中的 builtinRules、builtinAssetNotes、builtinTools）
// 翻译，若 DB 值与种子原文不同（用户修改过的值、代码中变更的说明）则原样显示。
// 工具描述 DB 值会作为指令传给模型，因此编辑表单中不放译文。
// 原文含有花括号，为避免经过 ICU 解析，使用 t.raw 读取。

const RULE_KEYS: Record<string, string> = {
  "[内置] 递归强制删除 rm -rf": "rmRf",
  "[内置] 删除系统关键目录": "rmSystemDirs",
  "[内置] 磁盘格式化 mkfs": "mkfs",
  "[内置] 覆写磁盘设备 dd": "ddOverwrite",
  "[内置] Fork 炸弹": "forkBomb",
  "[内置] 关机 / 重启": "shutdownReboot",
  "[内置] 杀死全部进程": "killAll",
  "[内置] 磁盘擦除 shred / wipe": "shredWipe",
  "[内置] 清空防火墙规则": "flushFirewall",
  "[内置] SQL DROP DATABASE / TABLE / SCHEMA": "sqlDrop",
  "[内置] SQL TRUNCATE": "sqlTruncate",
  "[内置] MongoDB drop / dropDatabase": "mongoDrop",
  "[内置] Redis FLUSHALL / FLUSHDB": "redisFlush",
  "[内置] curl / wget 发送 DELETE 请求": "curlWgetDelete",
  "[内置] Python HTTP 客户端 DELETE（requests/httpx/aiohttp）": "pythonHttpDelete",
  "[内置] 脚本中声明 HTTP DELETE 方法（JS/通用）": "scriptHttpDelete",
  "[内置] 批量清空 / 清除接口路径": "bulkClearPath",
  "[内置] 破坏性系统命令": "destructiveCommand",
  "[内置] 数据外泄管道": "exfilPipe",
  "[内置] 删除类接口路径": "deletePath",
};

const ASSET_NOTE_KEYS: Record<string, string> = {
  "[内置] 政府网站 (.gov)": "govGlobal",
  "[内置] 政府网站 (.gov.cn)": "govCn",
  "[内置] 教育网站 (.edu)": "eduGlobal",
  "[内置] 教育网站 (.edu.cn)": "eduCn",
};

const TOOL_SOURCES: Record<string, string> = toolSources;

// 报告 agent 提示词（agent/promptcatalog.go）指定的中文小标题。已保存的报告
//不修改，仅在显示时转换。复制和导出保持原文。PoC 不翻译。
const REPORT_HEADING_KEYS: Record<string, string> = {
  概述: "overview",
  影响与危害: "impact",
  受影响范围: "scope",
  复现步骤: "reproduce",
  证据: "evidence",
  根因分析: "rootCause",
  修复建议: "remediation",
};

export function useBuiltinLabels() {
  const tRule = useTranslations("builtinRules");
  const tNote = useTranslations("builtinAssetNotes");
  const tTool = useTranslations("builtinTools");
  const tHeading = useTranslations("builtinReportHeadings");

  return useMemo(() => {
    const ruleName = (name: string): string => {
      const key = RULE_KEYS[name];
      return key && tRule.has(key) ? (tRule.raw(key) as string) : name;
    };

    const assetNote = (note: string): string => {
      const key = ASSET_NOTE_KEYS[note];
      return key && tNote.has(key) ? (tNote.raw(key) as string) : note;
    };

    const toolDescription = (tool: { key: string; system: boolean; description: string }): string => {
      if (tool.system && TOOL_SOURCES[tool.key] === tool.description && tTool.has(tool.key)) {
        return tTool.raw(tool.key) as string;
      }
      return tool.description;
    };

    // 代码块内的行是证据原文，不做改动。
    const reportHeadings = (markdown: string): string => {
      let inFence = false;
      return markdown
        .split("\n")
        .map((line) => {
          if (/^\s*(```|~~~)/.test(line)) inFence = !inFence;
          if (inFence) return line;
          const match = /^(#{1,6}\s+)(.+?)\s*$/.exec(line);
          const key = match && REPORT_HEADING_KEYS[match[2]];
          return match && key ? match[1] + (tHeading.raw(key) as string) : line;
        })
        .join("\n");
    };

    return { ruleName, assetNote, toolDescription, reportHeadings };
  }, [tRule, tNote, tTool, tHeading]);
}
