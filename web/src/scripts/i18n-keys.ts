/**
 * i18n 语言键一致性助手。
 *
 * 以 zh.json 为基准（baseline），比较各语言「扁平化后的键集合」，找出缺失（missing）
 * 与多余（extra）。CLI（check-i18n-keys.ts）与 Vitest 测试共用这份纯逻辑，
 * 检查规则只在一处定义。
 */

export interface KeyDiff {
  missing: string[]; // baseline 中存在而 target 中不存在的键
  extra: string[]; // baseline 中没有而 target 中存在的键
}

/** 将嵌套 JSON 对象扁平化为 "a.b.c" 形式的键集合。数组和原始值视为叶子。 */
export function flattenKeys(tree: unknown, prefix = ""): Set<string> {
  const out = new Set<string>();
  if (tree === null || typeof tree !== "object" || Array.isArray(tree)) {
    if (prefix) out.add(prefix);
    return out;
  }
  for (const [key, value] of Object.entries(tree as Record<string, unknown>)) {
    const path = prefix ? `${prefix}.${key}` : key;
    for (const leaf of flattenKeys(value, path)) out.add(leaf);
  }
  return out;
}

/** 以排序后的数组返回 target 相对 baseline 缺失和新增的键。 */
export function diffKeys(baseline: Set<string>, target: Set<string>): KeyDiff {
  const missing = [...baseline].filter((k) => !target.has(k)).sort();
  const extra = [...target].filter((k) => !baseline.has(k)).sort();
  return { missing, extra };
}

/** 缺失和新增都为空时返回 true。 */
export function isInSync(diff: KeyDiff): boolean {
  return diff.missing.length === 0 && diff.extra.length === 0;
}
