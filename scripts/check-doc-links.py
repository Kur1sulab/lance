#!/usr/bin/env python3
"""检查仓库内 Markdown 文档的内部链接、图片引用与标题锚点。

扫描被 git 跟踪的 `.md` 文档，校验两件事：

1. 指向仓库内其他文件的相对链接（`[text](path)`）、图片引用
   （`![alt](path)`、`<img src="path">`）是否指向真实存在的文件。
2. 锚点链接（`[text](#heading)`、`[text](other.md#heading)`）是否指向目标
   `.md` 中真实存在的标题。锚点 slug 按 GitHub 规则（转小写 → 只保留
   `\\p{Word}`、连字符与空格 → 空格转连字符 → 同一 slug 重复时按出现顺序
   追加 `-1`、`-2` 后缀）用标准库复现。

只要有引用损坏就以退出码 1 结束，可直接作为 CI 合并门禁（`.github/workflows/docs.yml`）
与贡献者的本地校验。

不检查的内容：
- 外部 URL（`http://`、`https://`、`mailto:`、`tel:`、`data:`）：依赖网络、
  结果不稳定，这个确定性门禁不处理，外部链接另有脚本单独巡检。
- 指向非 `.md` 文件的链接中 `#` 之后的部分（例如源码行号）：那不是标题锚点，
  只校验文件是否存在。
- 图片引用中的 `#` 片段：图片没有标题锚点，没有意义。
- 围栏代码块（``` 或 ~~~ 包裹的块）里的示例链接与标题形式的注释：属于代码
  示例，不是真实引用或标题，跳过。

只用标准库，不访问网络。执行：`python3 -I scripts/check-doc-links.py`
"""
import os
import re
import subprocess
import sys
import unicodedata
from collections import defaultdict

# [text](path) 中非图片（`![...]`）的链接。路径截至空格前（不含标题 "..."）。
MD_LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)")
# ![alt](path) 图片链接。
MD_IMAGE = re.compile(r"!\[[^\]]*\]\(([^)\s]+)")
# <img ... src="path" ...> HTML 图片。
HTML_IMAGE = re.compile(r"<img[^>]*\bsrc=[\"']([^\"']+)[\"']", re.IGNORECASE)

#网络及非文件协议。锚点（`#`）是同一文档引用，因此不放入此处。
EXTERNAL_PREFIXES = ("http://", "https://", "mailto:", "tel:", "data:")

ATX_HEADING = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
# 去除标题文本中的内联 Markdown，仅保留纯文本。
MD_INLINE_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")


def is_external(target: str) -> bool:
    return target.startswith(EXTERNAL_PREFIXES)


def _is_word_char(ch: str) -> bool:
    """判断是否为 GitHub 锚点 slug 规则所保留的 `\\p{Word}` 字符。

    Ruby 正则是 `\\p{Word}` = Letter(L*) + Mark(M*) + Decimal_Number(Nd)
    + Connector_Punctuation(Pc) + Join_Control(U+200C·U+200D)。汉字、
    下划线、emoji 的 variation selector(Mn) 等都会原样保留。
    """
    if ch == "_":
        return True
    if ch in ("‌", "‍"):
        return True
    category = unicodedata.category(ch)
    if category[0] in ("L", "M"):
        return True
    return category in ("Nd", "Pc")


def slugify(text: str) -> str:
    """将标题文本转换为 GitHub 的锚点 slug（不含重复后缀处理）。"""
    text = text.strip().lower()
    out = []
    for ch in text:
        if ch == " ":
            out.append("-")
        elif ch == "-" or _is_word_char(ch):
            out.append(ch)
    return "".join(out)


def heading_slugs(root: str, md: str) -> set:
    """从文件中所有 ATX 标题构建 GitHub 锚点 slug 集合。

    同一 slug 重复出现时，按 GitHub 规则按出现顺序追加 `-1`、`-2` 后缀。
    """
    slugs = set()
    counts = defaultdict(int)
    in_fence = False
    with open(os.path.join(root, md), encoding="utf-8", errors="ignore") as fh:
        for line in fh:
            stripped = line.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            match = ATX_HEADING.match(line.rstrip("\n"))
            if not match:
                continue
            raw = match.group(2)
            raw = MD_INLINE_LINK.sub(r"\1", raw)
            raw = raw.replace("`", "").replace("*", "").replace("_", "")
            base = slugify(raw)
            seen = counts[base]
            counts[base] += 1
            slugs.add(base if seen == 0 else f"{base}-{seen}")
    return slugs


def main() -> int:
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    tracked = subprocess.check_output(
        ["git", "ls-files"], cwd=root, text=True
    ).splitlines()
    # 已删除但尚未提交的条目仍会出现在 git ls-files 中，跳过以免 open() 直接崩。
    md_files = [
        f
        for f in tracked
        if f.endswith(".md") and os.path.exists(os.path.join(root, f))
    ]

    slug_cache: dict = {}

    def slugs_of(md_path: str) -> set:
        if md_path not in slug_cache:
            slug_cache[md_path] = heading_slugs(root, md_path)
        return slug_cache[md_path]

    checked_files = 0
    checked_anchors = 0
    broken_files = []
    broken_anchors = []
    for md in md_files:
        with open(os.path.join(root, md), encoding="utf-8", errors="ignore") as fh:
            lines = fh.readlines()
        base_dir = os.path.dirname(md)
        in_fence = False
        for lineno, line in enumerate(lines, 1):
            stripped = line.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            for pattern in (MD_LINK, MD_IMAGE, HTML_IMAGE):
                for match in pattern.finditer(line):
                    target = match.group(1).strip()
                    if is_external(target):
                        continue

                    if target.startswith("#"):
                        file_part, anchor = "", target[1:]
                    elif "#" in target:
                        file_part, anchor = target.split("#", 1)
                    else:
                        file_part, anchor = target, ""
                    file_part = file_part.split("?", 1)[0]
                    anchor = anchor.strip()

                    # 所指向的 .md 文档（锚点检查对象）。若链接到其他文件，则为该文件，
                    # 若 `#锚点` 没有文件片段，则为自身文档。
                    target_md = None
                    if file_part:
                        checked_files += 1
                        resolved = os.path.normpath(
                            os.path.join(root, base_dir, file_part)
                        )
                        if not os.path.exists(resolved):
                            broken_files.append((md, lineno, target))
                            continue
                        if file_part.endswith(".md"):
                            target_md = os.path.normpath(
                                os.path.join(base_dir, file_part)
                            )
                    else:
                        target_md = md

                    # 锚点只对正文链接(MD_LINK)有意义。图片引用的
                    # 跳过 `#` 片段。
                    if anchor and target_md is not None and pattern is MD_LINK:
                        checked_anchors += 1
                        if anchor not in slugs_of(target_md):
                            broken_anchors.append((md, lineno, target, target_md))

    print(
        f"跟踪的 Markdown {len(md_files)} 个 · 文件引用 {checked_files} 个 · "
        f"检查了 {checked_anchors} 个锚点引用"
    )
    if broken_files:
        print(f"损坏的文件引用 {len(broken_files)} 个 — 所指向的文件在仓库中不存在：")
        for md, lineno, target in broken_files:
            print(f"  {md}:{lineno} -> {target}")
    if broken_anchors:
        print(f"损坏的锚点引用 {len(broken_anchors)} 个 — 目标文档中不存在该标题：")
        for md, lineno, target, target_md in broken_anchors:
            print(f"  {md}:{lineno} -> {target}  (目标: {target_md})")
    if broken_files or broken_anchors:
        return 1
    print("损坏引用 0 — 所有内部链接、图片、锚点均指向实际存在的目标。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
