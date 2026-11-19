#!/usr/bin/env python3
"""检查静态导出的 Web UI(`web/out`)的 HTML 中是否残留韩文。

本项目的界面默认语言是简体中文，多语言只保留 en / zh / es / ja 四种。历史版本
曾附带一套韩文文案与韩文默认回退，那套东西已经全部移除；但只要有谁在页面里
重新硬编码一段韩文，或者把某个语言包写回韩文默认值，界面上就会冒出看不懂的
文字，而只有下一个人用眼睛看的时候才会发现。这个脚本把这类回归挡在提交之前。

检查方式
--------
把 `web/out` 下每个 `.html` 文件的**原始内容**（渲染后的标记 + 内联脚本 +
RSC 飞行数据）整体扫一遍，只要出现韩文音节或韩文字母就返回退出码 1。
之所以看原始内容而不是剥掉标签后的正文，是因为用户看到的文本也会以 JSON
字符串的形式随飞行数据一起下发给浏览器，只解析标签会漏掉那条路径。

检查范围之外（诚实说明）
-----------------------
只覆盖静态导出的产物。浏览器在运行期通过 `mockHandle()` 之类的方式取数后再
渲染的内容不在其中——那部分只存在于运行时 JS 包里，需要靠代码评审把关。
同样，`.json` 语言包本身不由这个脚本检查，它的键一致性交给
`npm run check:i18n`。

允许的例外
----------
如果确实需要在界面上保留韩文（例如证据引用、原文截图说明），把字符加进下面的
`ALLOWED` 集合并写明理由，让例外尽量窄。

只依赖标准库，不访问网络。
用法：`python3 -I scripts/check-web-hangul.py [out 目录]`
默认目录是仓库根下的 `web/out`。
"""
import os
import re
import subprocess
import sys

# 韩文音节（U+AC00–U+D7A3）+ 韩文字母（U+1100–U+11FF 初声/中声/终声、
# U+3130–U+318F 兼容字母）。汉字与假名不在范围内，不会被误报。
HANGUL = re.compile(r"[\u1100-\u11ff\u3130-\u318f\uac00-\ud7a3]")

# 有意保留的韩文字符（目前为空 = 任何韩文都拦截）。
ALLOWED: set = set()


def find_out_dir() -> str:
    """确定要检查的 `out` 目录：给了参数就用参数，否则用仓库根下的 `web/out`。"""
    if len(sys.argv) > 1:
        return os.path.abspath(sys.argv[1])
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    return os.path.join(root, "web", "out")


def main() -> int:
    out_dir = find_out_dir()
    if not os.path.isdir(out_dir):
        print(
            f"错误：没有要检查的目录 — {out_dir}\n"
            "请先执行 `npm run build:static` 生成静态导出，再运行本脚本。"
        )
        return 2

    checked = 0
    offenders = []  # (相对路径, 韩文字符数, 字符预览)
    for dirpath, _dirnames, filenames in os.walk(out_dir):
        for name in filenames:
            if not name.endswith(".html"):
                continue
            checked += 1
            path = os.path.join(dirpath, name)
            with open(path, encoding="utf-8", errors="ignore") as fh:
                text = fh.read()
            found = [ch for ch in HANGUL.findall(text) if ch not in ALLOWED]
            if found:
                rel = os.path.relpath(path, out_dir)
                kinds = "".join(sorted(set(found)))
                offenders.append((rel, len(found), kinds))

    print(f"已检查 HTML {checked} 个（目标：{out_dir}）")
    if offenders:
        print(f"韩文残留 {len(offenders)} 个文件 — 用户可见的 HTML 中存在韩文：")
        for rel, count, kinds in offenders:
            preview = kinds if len(kinds) <= 30 else kinds[:30] + "…"
            print(f"  out/{rel}: 韩文 {count} 个 · 字符 {preview}")
        print(
            "\n请把这些文案改成简体中文；若确实属于需要保留的原文引用，"
            "把字符与理由一并加到 scripts/check-web-hangul.py 的 ALLOWED 中。"
        )
        return 1
    if checked == 0:
        print("警告：没有检查到 HTML — 请确认构建是否正确。")
        return 2
    print("韩文残留 0 处 — 用户可见的 HTML 中没有韩文。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
