import { NextIntlClientProvider } from "next-intl";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import zh from "../../messages/zh.json";
import { useAgentLabels } from "./agent-i18n";
import { useBuiltinLabels } from "./builtin-i18n";
import toolSources from "./builtin-tool-sources.json";

function labelsOf<T>(useLabels: () => T): T {
  let out: T | undefined;
  const Probe = () => {
    out = useLabels();
    return null;
  };
  renderToStaticMarkup(
    <NextIntlClientProvider locale="zh" messages={zh}>
      <Probe />
    </NextIntlClientProvider>,
  );
  return out as T;
}

describe("useBuiltinLabels", () => {
  const labels = labelsOf(useBuiltinLabels);

  it("如果是种子原文，则翻译规则名称和备注", () => {
    expect(labels.ruleName("[内置] 磁盘格式化 mkfs")).toBe(zh.builtinRules.mkfs);
    expect(labels.assetNote("[内置] 政府网站 (.gov)")).toBe(zh.builtinAssetNotes.govGlobal);
  });

  it("保留用户修改过的值", () => {
    expect(labels.ruleName("我的规则")).toBe("我的规则");
    expect(labels.assetNote("我的笔记")).toBe("我的笔记");
    expect(labels.toolDescription({ key: "add_hint", system: true, description: "已修改" })).toBe("已修改");
  });

  it("仅翻译报告小标题，正文和代码块保持原样", () => {
    const report = "## 概述\n概述正文\n\n## PoC\n```text\n## 证据\n```\n\n## 修复建议\nfix";
    expect(labels.reportHeadings(report)).toBe(
      `## ${zh.builtinReportHeadings.overview}\n概述正文\n\n## PoC\n\`\`\`text\n## 证据\n\`\`\`\n\n## ${zh.builtinReportHeadings.remediation}\nfix`,
    );
  });

  it("不翻译自定义工具", () => {
    const description = toolSources.add_hint;
    expect(labels.toolDescription({ key: "add_hint", system: false, description })).toBe(description);
  });

  it("翻译所有系统工具说明，即使包含花括号也不会失败", () => {
    for (const [key, description] of Object.entries(toolSources)) {
      const translated = labels.toolDescription({ key, system: true, description });
      expect(translated).toBe(zh.builtinTools[key as keyof typeof zh.builtinTools]);
    }
  });
});

describe("useAgentLabels reporter", () => {
  const labels = labelsOf(useAgentLabels);

  it("仅当为种子值时查询翻译", () => {
    expect(labels.name({ key: "reporter", name: "编写报告" })).toBe(zh.builtinAgents.reporter.name);
    expect(labels.name({ key: "reporter", name: "我的报告" })).toBe("我的报告");
    expect(labels.description({ key: "reporter", description: "我的说明" })).toBe("我的说明");
  });
});
