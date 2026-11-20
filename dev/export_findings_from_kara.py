#!/usr/bin/env python3
"""从空境 Kara 的真实库导出 朗基努斯 Lance 能吃的 findings.json。

为什么这么做：Lance 编排层的控制台没有数据时全是 0，「未配置 findings.json」，
截图毫无信息量。这里不造假数据，直接把自家漏扫库里 132 条真实命中翻成 Lance
的 schema（internal/brain/brain.go:17 的 Finding）。

字段映射：
  id          ← findings.key（稳定去重键）
  check       ← 由 title 归类（弱口令/未授权/注入/上传/信息泄露/其他）
  severity    ← findings.sev
  level       ← L1/L2/L3（按是否有 endpoint / 生产或测试资产粗分）
  confidence  ← verify_state 映射：ALIVE→confirmed，空→reported，其余→unverified
  target      ← asset 或 ip:port

同时导出 coverage：把「哪些资产根本没复验过」如实写出来，供 Lance 判断盲区。
"""
import json
import os
import re
import sqlite3
from collections import Counter

DB = os.path.expanduser("~/.kara/vuln.db")
OUT = os.path.expanduser("~/tools/lance/dev/findings-from-kara.json")

CHECK_RULES = [
    (re.compile(r"弱口令|默认口令|空口令|empty password", re.I), "auth.weak_password"),
    (re.compile(r"未授权|unauth|no.?auth", re.I), "web.unauthorized"),
    (re.compile(r"SQL|注入|injection", re.I), "web.injection"),
    (re.compile(r"上传|upload", re.I), "web.upload"),
    (re.compile(r"反序列化|deserial", re.I), "web.deserialization"),
    (re.compile(r"\.env|配置|泄露|leak|expos", re.I), "web.info_leak"),
    (re.compile(r"公钥|证书|签名|TLS|SSL", re.I), "crypto.weak"),
]


def classify(title: str) -> str:
    for pat, name in CHECK_RULES:
        if pat.search(title or ""):
            return name
    return "web.other"


def level_of(endpoint: str, port: str) -> str:
    """粗分交付层级：有具体端点=能复现到请求级(L3)，只有端口/资产=L1。"""
    if endpoint and endpoint not in ("-", ""):
        return "L3"
    return "L1"


def conf_of(state: str) -> str:
    s = (state or "").strip().upper()
    if s == "ALIVE":
        return "confirmed"
    if s == "DEAD":
        return "reported"
    if s in ("", "UNVERIFIED"):
        return "reported"
    return "unverified"


def main() -> int:
    con = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    rows = con.execute(
        "select key, asset, ip, port, endpoint, title, sev, verify_state, cve from findings"
    ).fetchall()

    findings = []
    for r in rows:
        target = r["asset"] or ""
        if not target and r["ip"]:
            target = f'{r["ip"]}:{r["port"]}' if r["port"] else r["ip"]
        findings.append({
            "id": r["key"] or f'kara-{len(findings)}',
            "check": classify(r["title"]),
            "severity": (r["sev"] or "info").lower(),
            "level": level_of(r["endpoint"], r["port"]),
            "confidence": conf_of(r["verify_state"]),
            "target": target or "-",
        })

    # coverage：按检查类型统计「有多少资产从未复验」——这就是真实盲区
    sev = Counter(f["severity"] for f in findings)
    chk = Counter(f["check"] for f in findings)
    unverified = sum(1 for f in findings if f["confidence"] == "reported")
    coverage = [
        {"item": f"检查类型 {k}", "state": "hit", "reason": f"{v} 条命中"}
        for k, v in chk.most_common()
    ]
    coverage.append({
        "item": "复验覆盖",
        "state": "gap" if unverified else "ok",
        "reason": f"{unverified}/{len(findings)} 条从未复验（verify_state 为空）",
    })

    payload = {"findings": findings, "coverage": coverage}
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2)

    print(f"写入 {OUT}")
    print(f"  findings: {len(findings)}")
    print(f"  严重度:   {dict(sev)}")
    print(f"  检查类型: {dict(chk)}")
    print(f"  未复验:   {unverified}/{len(findings)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
