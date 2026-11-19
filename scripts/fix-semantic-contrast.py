#!/usr/bin/env python3
"""给浅色主题补语义色的 dark: 配对。

背景：Lance 的默认主题是浅色（纸白机甲），但代码里大量语义色只写了
`text-emerald-400` / `text-amber-500` 这类深色主题配色。在浅色底上：

    emerald-400 #34d399 → 约 1.9:1
    amber-400   #fbbf24 → 约 1.6:1
    red-400     #f87171 → 约 2.3:1

全部低于 WCAG AA 的 4.5:1。

修法：`text-X-400` 改成 `text-X-700 dark:text-X-400`。
- 浅色主题拿到 -700（在白色上 ≥5:1）
- 深色主题保持原来的 -400/-500，**逐字节不变**

只改 `text-*`，不碰 `bg-*` / `border-*` / `fill-*`——那些不是文字对比度问题。

用法：
    python3 scripts/fix-semantic-contrast.py --dry-run   # 只报数
    python3 scripts/fix-semantic-contrast.py --apply     # 落盘
"""
import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"

COLORS = r"(?:emerald|amber|red|rose|green|blue|sky|violet|teal|lime|orange)"
SHADES = r"(?:300|400|500)"

# 前缀限定：只匹配带可选修饰符的 text- 类（hover:text-X-400 也要覆盖）
PAT = re.compile(rf"((?:[a-z-]+:)*)text-({COLORS})-({SHADES})\b")

# 只有这些 shade 在浅色底上不达标；-600 属于边界，本次不动（避免一次改太多）
LIGHT_MAP = {"300": "700", "400": "700", "500": "700"}


def has_dark_pair(line: str, color: str) -> bool:
    """同一行里已经有这个颜色的 dark: 变体，就认为已配对，跳过。"""
    return re.search(rf"dark:[a-z-]*text-{color}-", line) is not None


def rewrite_line(line: str):
    """返回 (新行, 改动数)。同一行同一颜色只配对一次。"""
    changes = 0

    def repl(m):
        nonlocal changes
        prefix, color, shade = m.group(1), m.group(2), m.group(3)
        if has_dark_pair(line, color):
            return m.group(0)
        light = LIGHT_MAP.get(shade)
        if not light:
            return m.group(0)
        changes += 1
        # 深色侧保留原来的 shade 与修饰符
        return f"{prefix}text-{color}-{light} dark:{prefix}text-{color}-{shade}"

    new = PAT.sub(repl, line)
    return new, changes


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    if not args.apply and not args.dry_run:
        args.dry_run = True

    files = sorted(SRC.rglob("*.tsx")) + sorted(SRC.rglob("*.ts"))
    total_files = total_lines = 0
    samples = []

    for f in files:
        try:
            text = f.read_text(encoding="utf-8")
        except Exception:
            continue
        if not PAT.search(text):
            continue
        out_lines, n = [], 0
        for line in text.splitlines(keepends=True):
            new, c = rewrite_line(line)
            out_lines.append(new)
            n += c
        if n == 0:
            continue
        total_files += 1
        total_lines += n
        if len(samples) < 5:
            for old, new in zip(text.splitlines(), out_lines):
                if old != new.rstrip("\n"):
                    samples.append((f.relative_to(ROOT), old.strip()[:100], new.strip()[:130]))
                    break
        if args.apply:
            f.write_text("".join(out_lines), encoding="utf-8")

    mode = "已落盘" if args.apply else "试运行（未写盘）"
    print(f"{mode}")
    print(f"  涉及文件: {total_files}")
    print(f"  改动处数: {total_lines}")
    if samples:
        print("\n样例:")
        for rel, old, new in samples:
            print(f"  {rel}")
            print(f"    - {old}")
            print(f"    + {new}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
