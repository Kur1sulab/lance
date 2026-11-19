#!/usr/bin/env python3
"""检查被跟踪的 Markdown 文档所指向的外部链接（http·https）是否仍然存活。

姊妹脚本 `check-doc-links.py` 只校验仓库内部的链接与锚点，外部 URL 按设计跳过
（依赖网络、结果容易抖，不放进合并门禁）。那部分「单独巡检」由这个脚本承担：
README 首部、防御指南、detections 各 README 里给读者指路的外部链接（事故举报
窗口、CISA KEV、OWASP、SigmaHQ、Suricata、MITRE、MISP、演示站点、GitHub 徽章
等）一旦改址、下线或失效，就会安静地烂在那里；这个脚本按周期手动跑一遍把它们
捞出来。

挑选待查 URL 的规则
- 遍历所有被跟踪的 `.md`，但跳过围栏代码块（``` 或 ~~~ 包裹）与行内代码
  （`` `...` ``）里的 URL：那是命令示例、配置值或引用的外部资产，不是「给读者
  查阅的参考链接」。
- 从 Markdown 链接与图片（`[text](url)`、`![alt](url)`）、自动链接（`<url>`）
  以及正文里裸露的 URL 中收集 http·https 地址。
- 排除保留名与占位主机：localhost、私有/回环 IP（127.·10.·192.168.·169.254.·
  172.16~31.·0.0.0.0·::1）、RFC 2606/6761 保留域（example.com/org/net/edu·
  `*.example.*`·`.test`·`.invalid`·`.local`·`.tld`），以及不带点、不是 FQDN 的
  名字（例如 `target`）。

怎么确认「活着」（把 MAINTAINING.md 8.2 的教训落成代码）
公共机构站点经常拒绝 HEAD 请求或默认 User-Agent，还会多次重定向，简单一探就会
把好好的链接误判成坏链。所以这个脚本**用浏览器 UA、发 GET、跟随重定向**去确认，
并把状态分三类：
- OK：最终状态 2xx·3xx，链接有效。
- RESTRICTED：401·403·405·429。主机还活着，只是访问方式被服务端策略挡住（机器人
  拦截、方法拒绝、限速），不算坏链。照实报告，但不算失败。
- DOWN：404·410·5xx（重试后仍然）、DNS/连接/超时/SSL 错误（重试后仍然）。很可能
  是真的坏了。

网络错误、5xx、429 会小幅延迟后重试，用来区分瞬时抖动和真故障。
只用标准库。脚本只对仓库**公开文档里已经写着**的参考 URL 发 GET 确认存活，
不扫描、不探测任何目标。

已知例外（allowlist）
写在 `scripts/external-links-allowlist.txt` 里的 URL 判为 DOWN 时单独归为
「ALLOWED」，不计入 strict 退出码。这里只放我们不拥有、也修不了（或按保留原则不动）
的死链，让周期性 strict 巡检不会因为这一条永远报红，而**只有新出现的坏链**才报红。

执行
- 默认（只报告，退出码 0）：`python3 -I scripts/check-external-links.py`
- 周期/发版巡检（有新坏链就报红）：`--strict`（allowlist 之外的 DOWN 只要有一个
  就以退出码 1 结束）。RESTRICTED 与 ALLOWED 在 strict 下也不算失败。
- 不联网、只预览待查集合：`--list`（不发请求，打印待查 URL 与出处）。
"""
import argparse
import http.client
import os
import re
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from collections import defaultdict
from urllib.parse import urlsplit

# [text](url) 正文链接（非图片）与 ![alt](url) 图片。路径直到空格或右括号为止。
MD_LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)")
MD_IMAGE = re.compile(r"!\[[^\]]*\]\(([^)\s]+)")
# <https://...> 自动链接。
AUTOLINK = re.compile(r"<(https?://[^>\s]+)>")
# 散文中裸露的 URL。后续标点从后面剥离。
BARE_URL = re.compile(r"https?://[^\s)>\]\"'`]+")
# 行内代码段 `...`（单反引号）。提取前先替换为空格。
INLINE_CODE = re.compile(r"`[^`]*`")
# 散文中 URL 末尾常见的标点。
TRAILING_PUNCT = ".,;:!?\"'»)]}>"

# IPv4 私有/回环/链路本地网段（172.16~31. 另有单独模式）。
PRIVATE_IPV4 = re.compile(r"^(127\.|10\.|192\.168\.|169\.254\.|0\.0\.0\.0$)")
PRIVATE_IPV4_172 = re.compile(r"^172\.(1[6-9]|2\d|3[01])\.")

BROWSER_UA = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
    "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
)

# 主机仍存活，但确认方式被服务器策略阻止的状态码（并非损坏）。
RESTRICTED_CODES = {401, 403, 405, 429}

ALLOWLIST_PATH = os.path.join("scripts", "external-links-allowlist.txt")


def load_allowlist(root: str) -> set:
    """读取我们无法修复的已知 DOWN URL 集合（不存在则为空集合）。

    每行一个 URL。`#` 之后是注释，空行与纯注释行忽略。
    """
    path = os.path.join(root, ALLOWLIST_PATH)
    allow = set()
    if not os.path.exists(path):
        return allow
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.split("#", 1)[0].strip()
            if line:
                allow.add(line)
    return allow


def strip_code(line: str) -> str:
    """从代码围栏外的一行中删除行内代码段（其中的 URL 不是引用）。"""
    return INLINE_CODE.sub(" ", line)


def is_checkable(url: str) -> bool:
    """过滤掉预留及占位符主机，只保留值得实际检查的外部 URL。"""
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https"):
        return False
    host = parts.hostname
    if not host:
        return False
    host = host.lower()
    if host == "localhost" or host.endswith(".localhost"):
        return False
    if host == "::1":
        return False
    if PRIVATE_IPV4.match(host) or PRIVATE_IPV4_172.match(host):
        return False
    if host in ("example.com", "example.org", "example.net", "example.edu"):
        return False
    if host.endswith((".example.com", ".example.org", ".example.net", ".example")):
        return False
    if host.endswith((".test", ".invalid", ".local", ".localdomain", ".tld")):
        return False
    if "." not in host:  # 非 FQDN 的裸名称（例如 target）
        return False
    return True


def extract(root: str, md_files: list) -> dict:
    """从被跟踪的 .md 中构建待检查外部 URL → [(文件, 行号), ...] 的字典。"""
    found = defaultdict(list)
    for md in md_files:
        with open(os.path.join(root, md), encoding="utf-8", errors="ignore") as fh:
            lines = fh.readlines()
        in_fence = False
        for lineno, raw in enumerate(lines, 1):
            stripped = raw.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            line = strip_code(raw)
            candidates = []
            for pattern in (MD_LINK, MD_IMAGE, AUTOLINK):
                candidates.extend(m.group(1) for m in pattern.finditer(line))
            for m in BARE_URL.finditer(line):
                candidates.append(m.group(0).rstrip(TRAILING_PUNCT))
            for url in candidates:
                url = url.strip().rstrip(TRAILING_PUNCT)
                if is_checkable(url):
                    where = (md, lineno)
                    if where not in found[url]:
                        found[url].append(where)
    return found


def probe(url: str, timeout: float, retries: int, delay: float):
    """用浏览器 UA 向 URL 发送 GET，返回（分类, 详情）。

    分类为 "OK" / "RESTRICTED" / "DOWN"。网络错误、5xx、429 会重试。
    """
    req = urllib.request.Request(
        url,
        method="GET",
        headers={
            "User-Agent": BROWSER_UA,
            "Accept": "*/*",
            "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        },
    )
    last = ""
    for attempt in range(retries + 1):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                code = resp.getcode()
                if code in RESTRICTED_CODES:
                    return "RESTRICTED", f"HTTP {code}"
                return "OK", f"HTTP {code}"
        except urllib.error.HTTPError as exc:
            code = exc.code
            if code in RESTRICTED_CODES:
                return "RESTRICTED", f"HTTP {code}"
            if code >= 500 or code == 408:
                last = f"HTTP {code}"  # 可能是暂时性的，可重试
            else:
                return "DOWN", f"HTTP {code}"  # 404、410 等视为确定，无需重试
        except (
            urllib.error.URLError,
            http.client.HTTPException,
            ssl.SSLError,
            socket.timeout,
            ConnectionError,
            TimeoutError,
            OSError,
        ) as exc:
            reason = getattr(exc, "reason", exc)
            last = f"{type(exc).__name__}: {reason}"
        if attempt < retries:
            time.sleep(delay * (attempt + 1))
    return "DOWN", last


def main() -> int:
    ap = argparse.ArgumentParser(description="检查跟踪 Markdown 中的外部链接存活状态")
    ap.add_argument("--strict", action="store_true",
                    help="只要有一个 DOWN 就以退出码 1 结束（用于定期/发布检查）")
    ap.add_argument("--list", action="store_true",
                    help="不发起网络调用，仅输出检查目标 URL 与来源")
    ap.add_argument("--timeout", type=float, default=15.0, help="请求超时(秒)")
    ap.add_argument("--retries", type=int, default=2, help="网络错误·5xx·429 重试次数")
    ap.add_argument("--delay", type=float, default=0.5, help="调用间·重试间默认延迟（秒）")
    args = ap.parse_args()

    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    tracked = subprocess.check_output(["git", "ls-files"], cwd=root, text=True).splitlines()
    md_files = [f for f in tracked if f.endswith(".md")]

    allow = load_allowlist(root)
    found = extract(root, md_files)
    urls = sorted(found)
    print(f"从 {len(md_files)} 个跟踪的 Markdown 中收集到 {len(urls)} 个待检查的外部 URL")

    if args.list:
        for url in urls:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            tag = " [allowlist]" if url in allow else ""
            print(f"  {url}{tag}  ({where})")
        return 0

    results = {"OK": [], "RESTRICTED": [], "ALLOWED": [], "DOWN": []}
    for i, url in enumerate(urls):
        if i:
            time.sleep(args.delay)  # 调用间小幅延迟（规避沙箱节流）
        verdict, detail = probe(url, args.timeout, args.retries, args.delay)
        if verdict == "DOWN" and url in allow:
            verdict = "ALLOWED"  #无法修复的已知上游继承 DOWN
        results[verdict].append((url, detail))
        print(f"  [{verdict:10}] {url} — {detail}")

    print(
        f"\n摘要: OK {len(results['OK'])} · RESTRICTED {len(results['RESTRICTED'])} · "
        f"ALLOWED {len(results['ALLOWED'])} · DOWN {len(results['DOWN'])}"
    )
    if results["RESTRICTED"]:
        print("RESTRICTED（主机存活/确认方式被阻断，并非失效链接）：")
        for url, detail in results["RESTRICTED"]:
            print(f"  {url} — {detail}")
    if results["ALLOWED"]:
        print("ALLOWED(allowlist 中已知的 DOWN —— 我们无法修复，故从 strict 中排除):")
        for url, detail in results["ALLOWED"]:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            print(f"  {url} — {detail}  (引用: {where})")
    if results["DOWN"]:
        print("DOWN(损坏可能性高 —— 需要确认):")
        for url, detail in results["DOWN"]:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            print(f"  {url} — {detail}  (引用: {where})")

    if args.strict and results["DOWN"]:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
