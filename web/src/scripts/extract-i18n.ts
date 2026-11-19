/**
 * 脚本：extract-i18n.ts
 *
 * Lance 界面文案抽取器（第一遍）。
 *
 * 用 TypeScript AST 解析 `src/` 下所有 `.ts`/`.tsx`，把暴露给用户的硬编码中文
 * 字符串抽出来。抽取对象有三类：
 *   1) 字符串字面量            例）title: "仪表盘"
 *   2) 模板字面量              例）`已删除 ${n} 个对话` → "已删除 {var0} 个对话"
 *   3) JSX 文本节点            例）<span>故障转移</span>
 * 注释（`//`、`/* *\/`）不是 AST 节点，自然被排除；注释里的中文不参与翻译。
 *
 * 产物（web/messages/）：
 *   - zh.json          按命名空间嵌套的 { 键: "中文原文" }（基准语言，原文保留）
 *   - en.json / es.json / ja.json   同一骨架，值为译文
 *   - zh.sources.json  每个键的出处（文件:行）、类型、占位符——用于接线、翻译与漂移追踪
 *
 * 重复抽取是非破坏性的：只刷新抽取生成的最上层命名空间（按文件路径生成：app、
 * components、lib 等），其余「策展命名空间」（手写的 nav、search、header 等）从现有
 * 文件原样保留。规则是：抽取命名空间是清单，不手改；运行时译文只放策展命名空间。
 * 这样重复抽取不会覆盖译文。
 *
 * 命名空间取自文件路径。例）src/app/(main)/system/llm/page.tsx
 *   → app.main.system.llm.page（去掉路由组括号，去掉 _folder 的下划线）
 * 键是原文 sha1 的前 8 位。顺序或文件变化时同一文案仍拿到同一个键，重跑 diff 更小。
 *
 * 执行：  npm run extract:i18n
 */

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

const SRC_DIR = path.resolve(__dirname, "..");
const MESSAGES_DIR = path.resolve(__dirname, "../../messages");
const REPO_WEB_DIR = path.resolve(__dirname, "../..");

/** 是否包含至少 1 个汉字(Han)。判定 UI 翻译对象的唯一标准。 */
const HAN = /\p{Script=Han}/u;

/** 提取时排除的目录（解析工具自身和构建产物）。 */
const SKIP_DIRS = new Set(["scripts", "node_modules", ".next"]);

type Kind = "string" | "template" | "jsx";

interface Message {
  ns: string;
  key: string;
  text: string;
  kind: Kind;
  placeholders: string[]; // 模板中的 ${...} 原始表达式（仅供参考）
  occurrences: string[]; // "相对路径:行"
}

/** 以 src 为基准的相对路径 → 用点连接的命名空间。 */
function toNamespace(absFile: string): string {
  const rel = path.relative(SRC_DIR, absFile).replace(/\.[tj]sx?$/, "");
  return rel
    .split(path.sep)
    .map((seg) =>
      seg
        .replace(/^\((.*)\)$/, "$1") // (main) → main  (路由组)
        .replace(/^_/, "") // _components → components
        .replace(/[^A-Za-z0-9韩文音节范围]+/g, "-")
        .replace(/^-+|-+$/g, ""),
    )
    .filter(Boolean)
    .join(".");
}

function hashKey(text: string): string {
  return crypto.createHash("sha1").update(text).digest("hex").slice(0, 8);
}

/** 递归遍历目录收集 .ts/.tsx 文件。 */
function collectFiles(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      collectFiles(path.join(dir, entry.name), out);
    } else if (/\.(ts|tsx)$/.test(entry.name)) {
      out.push(path.join(dir, entry.name));
    }
  }
  return out;
}

/** 将模板字面量重组为 "字面量部分 + {varN}"。返回 null = 字面量部分不含汉字。 */
function reconstructTemplate(node: ts.TemplateExpression): { text: string; placeholders: string[] } | null {
  let text = node.head.text;
  const placeholders: string[] = [];
  node.templateSpans.forEach((span, i) => {
    placeholders.push(span.expression.getText());
    text += `{var${i}}${span.literal.text}`;
  });
  // 汉字只出现在 ${} 中而不在固定文案里时，该汉字会被单独识别为内部节点。
  if (!HAN.test(node.head.text) && !node.templateSpans.some((s) => HAN.test(s.literal.text))) {
    return null;
  }
  return { text, placeholders };
}

/** JSX 文本规范化：去除首尾空白 + 将内部连续空白/换行合并为一个空格（与浏览器渲染规则一致）。 */
function normalizeJsxText(raw: string): string {
  return raw.replace(/\s+/g, " ").trim();
}

const messages = new Map<string, Message>(); // "ns\u0000key" → Message
const scannedFiles = collectFiles(SRC_DIR).sort();
const capturedLines = new Map<string, Set<number>>(); // 文件 → 挂载提取节点的行号集合
let fileWithHan = 0;

function record(ns: string, text: string, kind: Kind, placeholders: string[], relFile: string, line: number) {
  const key = hashKey(text);
  const id = `${ns}\u0000${key}`;
  let msg = messages.get(id);
  if (!msg) {
    msg = { ns, key, text, kind, placeholders, occurrences: [] };
    messages.set(id, msg);
  }
  const occ = `${relFile}:${line}`;
  if (!msg.occurrences.includes(occ)) msg.occurrences.push(occ);
}

for (const absFile of scannedFiles) {
  const source = fs.readFileSync(absFile, "utf8");
  if (!HAN.test(source)) continue;
  fileWithHan++;

  const relFile = path.relative(REPO_WEB_DIR, absFile);
  const ns = toNamespace(absFile);
  const scriptKind = absFile.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(absFile, source, ts.ScriptTarget.Latest, true, scriptKind);
  const lineOf = (pos: number) => sf.getLineAndCharacterOfPosition(pos).line + 1;
  const markLine = (pos: number) => {
    const set = capturedLines.get(relFile) ?? new Set<number>();
    set.add(lineOf(pos));
    capturedLines.set(relFile, set);
  };

  const visit = (node: ts.Node): void => {
    if (ts.isStringLiteralLike(node) && !ts.isTemplateExpression(node.parent)) {
      // 字符串字面量 + 无替换的模板。（含替换的模板的 head/middle 不在此列）
      if (HAN.test(node.text)) {
        record(ns, node.text, "string", [], relFile, lineOf(node.getStart(sf)));
        markLine(node.getStart(sf));
      }
    } else if (ts.isTemplateExpression(node)) {
      const r = reconstructTemplate(node);
      if (r) {
        record(ns, r.text, "template", r.placeholders, relFile, lineOf(node.getStart(sf)));
        markLine(node.getStart(sf));
      }
    } else if (ts.isJsxText(node)) {
      const text = normalizeJsxText(node.text);
      if (text && HAN.test(text)) {
        record(ns, text, "jsx", [], relFile, lineOf(node.getStart(sf)));
        markLine(node.getStart(sf));
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
}

// --- 嵌套 JSON 组装 ---------------------------------------------------------
type Tree = { [k: string]: Tree | string };

function setNested(root: Tree, nsPath: string, key: string, value: string) {
  const segs = nsPath.split(".");
  let node: Tree = root;
  for (const seg of segs) {
    if (typeof node[seg] !== "object") node[seg] = {};
    node = node[seg] as Tree;
  }
  node[key] = value;
}

/** 递归排序键，尽量减少重复执行时的 diff。 */
function sortTree(t: Tree): Tree {
  const out: Tree = {};
  for (const k of Object.keys(t).sort()) {
    const v = t[k];
    out[k] = typeof v === "string" ? v : sortTree(v);
  }
  return out;
}

const zhTree: Tree = {};
const koTree: Tree = {};
const sources: Record<string, { text: string; kind: Kind; placeholders: string[]; occurrences: string[] }> = {};

for (const msg of messages.values()) {
  setNested(zhTree, msg.ns, msg.key, msg.text);
  setNested(koTree, msg.ns, msg.key, "");
  sources[`${msg.ns}.${msg.key}`] = {
    text: msg.text,
    kind: msg.kind,
    placeholders: msg.placeholders,
    occurrences: msg.occurrences.sort(),
  };
}

const sortedSources: typeof sources = {};
for (const k of Object.keys(sources).sort()) sortedSources[k] = sources[k];

fs.mkdirSync(MESSAGES_DIR, { recursive: true });
const write = (name: string, data: unknown) =>
  fs.writeFileSync(path.join(MESSAGES_DIR, name), `${JSON.stringify(data, null, 2)}\n`, "utf8");

/** 读取已有的消息文件。不存在或已损坏则返回空树。 */
function readTree(name: string): Tree {
  try {
    return JSON.parse(fs.readFileSync(path.join(MESSAGES_DIR, name), "utf8")) as Tree;
  } catch {
    return {};
  }
}

/**
 * 非破坏性合并。抽取新生成的树（fresh）里属于抽取命名空间的最上层节点以代码为准
 * 直接采用；只存在于现有文件（existing）里的最上层命名空间（=手写译文所在的策展
 * 命名空间）保留不动。抽取命名空间里已经过时的键自然消失。
 */
function mergeCurated(fresh: Tree, existing: Tree): Tree {
  const extractedTop = new Set(Object.keys(fresh));
  const out: Tree = { ...fresh };
  for (const [ns, subtree] of Object.entries(existing)) {
    if (!extractedTop.has(ns)) out[ns] = subtree;
  }
  return out;
}

write("zh.json", sortTree(mergeCurated(zhTree, readTree("zh.json"))));
write("zh.sources.json", sortedSources);

// --- 摘要报告 ------------------------------------------------------------
const byKind: Record<Kind, number> = { string: 0, template: 0, jsx: 0 };
const byNs = new Map<string, number>();
let totalOccurrences = 0;
for (const msg of messages.values()) {
  byKind[msg.kind]++;
  byNs.set(msg.ns, (byNs.get(msg.ns) ?? 0) + 1);
  totalOccurrences += msg.occurrences.length;
}

// 覆盖率估算：含汉字的源码行中，命中提取节点的行所占比例。
// 其余几乎都是注释（=非翻译对象）。
let hanLines = 0;
let capturedHanLines = 0;
for (const absFile of scannedFiles) {
  const relFile = path.relative(REPO_WEB_DIR, absFile);
  const lines = fs.readFileSync(absFile, "utf8").split(/\r?\n/);
  const capSet = capturedLines.get(relFile) ?? new Set<number>();
  lines.forEach((ln, i) => {
    if (HAN.test(ln)) {
      hanLines++;
      if (capSet.has(i + 1)) capturedHanLines++;
    }
  });
}

const topNs = [...byNs.entries()].sort((a, b) => b[1] - a[1]).slice(0, 15);

console.log("─".repeat(64));
console.log("Lance i18n 提取完成");
console.log("─".repeat(64));
console.log(`已扫描文件            : ${scannedFiles.length}（src/ 下的 .ts/.tsx，不含 scripts）`);
console.log(`含汉字的文件         : ${fileWithHan}`);
console.log(`唯一消息(键)        : ${messages.size}`);
console.log(`  ├─ 字符串字面量      : ${byKind.string}`);
console.log(`  ├─ 模板字面量      : ${byKind.template}`);
console.log(`  └─ JSX 文本         : ${byKind.jsx}`);
console.log(`总出现位置           : ${totalOccurrences}（含重复使用）`);
console.log(`命名空间数量        : ${byNs.size}`);
console.log("");
console.log(`含汉字的源码行       : ${hanLines}`);
console.log(`  └─ 提取捕获到的行   : ${capturedHanLines} (${((capturedHanLines / hanLines) * 100).toFixed(1)}%)`);
console.log(`      其余 ${hanLines - capturedHanLines} 行大多是代码注释（不翻译）`);
console.log("");
console.log("消息量最多的命名空间前 15 个：");
for (const [ns, n] of topNs) console.log(`  ${String(n).padStart(4)}  ${ns}`);
console.log("");
console.log(`输出：web/messages/{zh.json, zh.sources.json}`);
console.log("─".repeat(64));
