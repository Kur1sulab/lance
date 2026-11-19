#!/usr/bin/env python3
"""Lance 主题对比度门禁（WCAG 2.x）。

用法:
  python3 scripts/contrast.py            # 校验 lance/brutalist 主要色对 + 语义色阶
  python3 scripts/contrast.py --json     # 机器可读输出

oklch → sRGB 走 OKLab → LMS → linear sRGB 精确换算（不是近似），
色对全部取自 web/src/styles/presets/*.css 的真实 token。
阈值：正文/小字 ≥ 4.5:1，大字号(≥24px 或 ≥18.66px bold) ≥ 3:1。
"""
from __future__ import annotations

import json
import math
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PRESETS = ROOT / "web" / "src" / "styles" / "presets"


# ---------- 颜色换算 ----------
def oklch_to_srgb(L: float, C: float, h: float) -> tuple[float, float, float]:
    hr = math.radians(h)
    a, b = C * math.cos(hr), C * math.sin(hr)
    l_ = L + 0.3963377774 * a + 0.2158037573 * b
    m_ = L - 0.1055613458 * a - 0.0638541728 * b
    s_ = L - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_ ** 3, m_ ** 3, s_ ** 3
    r = +4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    b2 = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s

    def gam(c: float) -> float:
        c = max(0.0, min(1.0, c))
        return 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055

    return gam(r), gam(g), gam(b2)


def srgb_to_lin(c: float) -> float:
    return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4


def luminance(rgb: tuple[float, float, float]) -> float:
    r, g, b = (srgb_to_lin(c) for c in rgb)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(fg: tuple, bg: tuple) -> float:
    l1, l2 = luminance(fg), luminance(bg)
    hi, lo = max(l1, l2), min(l1, l2)
    return (hi + 0.05) / (lo + 0.05)


def blend(fg: tuple, bg: tuple, alpha: float) -> tuple:
    """sRGB 空间 alpha 合成（浏览器行为）。"""
    return tuple(f * alpha + b * (1 - alpha) for f, b in zip(fg, bg))


def parse_oklch(text: str) -> tuple[float, float, float] | None:
    m = re.search(r"oklch\(\s*([\d.]+)%?\s+([\d.]+)\s+([\d.]+)\s*\)", text)
    if not m:
        return None
    L = float(m.group(1))
    if L > 1:
        L /= 100
    return L, float(m.group(2)), float(m.group(3))


def load_tokens(preset: str) -> dict[str, dict[str, tuple]]:
    """返回 {selector_block: {token: oklch}}，按文件内块顺序。"""
    css = (PRESETS / f"{preset}.css").read_text()
    blocks: dict[str, dict[str, tuple]] = {}
    for m in re.finditer(r"(:root[^{]*|\.dark[^{]*)\{([^}]*)\}", css):
        sel, body = m.group(1).strip(), m.group(2)
        toks = {}
        for tm in re.finditer(r"--([\w-]+):\s*([^;]+);", body):
            v = parse_oklch(tm.group(2))
            if v:
                toks[tm.group(1)] = v
        blocks.setdefault(sel, {}).update(toks)
    return blocks


def resolve(blocks: dict, mode: str) -> dict[str, tuple]:
    """light = :root 块 + :not(.dark) 覆盖；dark = :root 块 + .dark 覆盖。"""
    out: dict[str, tuple] = {}
    for sel, toks in blocks.items():
        is_dark_block = sel.startswith(".dark")
        is_light_block = ":not(.dark)" in sel
        if mode == "dark" and is_light_block:
            continue
        if mode == "light" and is_dark_block:
            continue
        out.update(toks)
    return out


# ---------- 色对定义 ----------
def core_pairs(t: dict[str, tuple]) -> list[tuple[str, tuple, tuple, float]]:
    """(名称, 前景, 背景, 阈值) —— 每套主题 ≥8 对。"""
    pairs = []

    def add(name, fg_key, bg_key, need=4.5):
        if fg_key in t and bg_key in t:
            pairs.append((name, t[fg_key], t[bg_key], need))

    add("正文/页面底", "foreground", "background")
    add("卡片正文/卡片底", "card-foreground", "card")
    add("次要文字/页面底", "muted-foreground", "background")
    add("次要文字/卡片底", "muted-foreground", "card")
    add("次要文字/muted底", "muted-foreground", "muted")
    add("主色按钮文字/主色底", "primary-foreground", "primary")
    add("次级按钮文字/次级底", "secondary-foreground", "secondary")
    add("accent文字/accent底", "accent-foreground", "accent")
    add("弹层文字/弹层底", "popover-foreground", "popover")
    add("侧栏文字/侧栏底", "sidebar-foreground", "sidebar")
    add("侧栏激活文字/激活底", "sidebar-accent-foreground", "sidebar-accent")
    add("侧栏主色文字/主色底", "sidebar-primary-foreground", "sidebar-primary")
    if "destructive-foreground" in t:
        add("危险按钮文字/危险底", "destructive-foreground", "destructive")
    return pairs


def semantic_pairs(t: dict[str, tuple], mode: str) -> list[tuple[str, tuple, tuple, float]]:
    """语义色阶当文字用：压卡片底 + 压同色 15% 淡底（徽章最难底）。"""
    card = oklch_to_srgb(*t["card"]) if "card" in t else ((1, 1, 1) if mode == "light" else (0.02, 0.02, 0.02))
    pairs = []
    text_shades = ["400", "500", "600", "700"]
    families = ["red", "rose", "amber", "orange", "emerald", "green", "blue", "violet", "slate", "sky", "cyan", "teal"]
    for fam in families:
        for sh in text_shades:
            key = f"color-{fam}-{sh}"
            if key not in t:
                continue
            fg = oklch_to_srgb(*t[key])
            pairs.append((f"{fam}-{sh} 文字/卡片", fg, card, 4.5))
            # 徽章：bg-{fam}-500/15 + text-{fam}-600（light）或 400（dark）
            base = t.get(f"color-{fam}-500")
            if base:
                tint = blend(oklch_to_srgb(*base), card, 0.15)
                pairs.append((f"{fam}-{sh} 文字/15%淡底", fg, tint, 4.5))
    return pairs


def main() -> int:
    as_json = "--json" in sys.argv
    report = []
    failed = 0
    for preset in ("lance", "brutalist"):
        blocks = load_tokens(preset)
        for mode in ("light", "dark"):
            t = resolve(blocks, mode)
            for name, fg, bg, need in core_pairs(t):
                cr = contrast(oklch_to_srgb(*fg), oklch_to_srgb(*bg))
                ok = cr >= need
                failed += 0 if ok else 1
                report.append({"preset": preset, "mode": mode, "pair": name, "ratio": round(cr, 2), "need": need, "ok": ok})
            if any(k.startswith("color-") for k in t):
                for name, fg, bg, need in semantic_pairs(t, mode):
                    cr = contrast(fg, bg)
                    ok = cr >= need
                    failed += 0 if ok else 1
                    report.append({"preset": preset, "mode": mode, "pair": name, "ratio": round(cr, 2), "need": need, "ok": ok})
    if as_json:
        print(json.dumps({"failed": failed, "pairs": report}, ensure_ascii=False, indent=1))
    else:
        cur = None
        for r in report:
            key = (r["preset"], r["mode"])
            if key != cur:
                cur = key
                print(f"\n== {r['preset']} / {r['mode']} ==")
            mark = "PASS" if r["ok"] else "FAIL"
            print(f"  [{mark}] {r['ratio']:>6.2f}:1 (need {r['need']}) {r['pair']}")
        n_core = sum(1 for r in report if "/" in r["pair"] and not r["pair"].endswith(("卡片", "淡底")))
        print(f"\n总计 {len(report)} 对，不达标 {failed} 对")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
