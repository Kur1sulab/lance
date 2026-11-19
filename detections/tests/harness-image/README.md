# 检测测试用的预构建镜像

八套检测套件和三个校验门（`check-harness-sync`、`triage-selftest`、
`check-defense-template`）默认用 `python:3.12-slim` 现装依赖。这在网络正常的 CI 上没问题，
但在国内网络或离线机器上有两个坑：**每套都要重装一遍依赖**（sigma-cli + 四个后端插件，
2-4 分钟 × 8），以及 `check-defense-template.sh` 会**没有任何输出地卡死**。

## 卡死的根因

`sigma check` 会跑 pySigma 的标签校验器，其中两个校验器在**首次使用时联网下载**参考数据：

| 校验器 | 下载内容 | 大小 | 缓存位置 |
|---|---|---|---|
| `ATTACKTagValidator` | MITRE ATT&CK STIX（`enterprise-attack.json`） | ~55 MB | `~/.cache/pysigma/mitre_attack` |
| `D3FENDTagValidator` | MITRE D3FEND 本体（`d3fend.json`） | ~4.8 MB | `~/.cache/pysigma/mitre_d3fend` |

两个缓存都没有过期时间。下载被墙/超时时 `urlopen` 会长时间阻塞，阶段 2 的
`sigma check` 就永远回不来，门禁既不 PASS 也不 FAIL。若是 DNS 彻底不通，则会抛
`RuntimeError: Failed to load MITRE ATT&CK data: <urlopen error …>`，stderr 里只剩半截
traceback，看起来像文档写错了。

## 解决办法：把依赖和缓存一起烤进镜像

```bash
cd detections/tests/harness-image
./fetch-refs.sh                              # 拉两个 JSON（ATTACK 走 gh-proxy 镜像）
docker build -t lance-detections:latest .
cd - && PYTHON_IMAGE=lance-detections:latest detections/tests/run-all.sh
```

国内直连 d3fend.mitre.org 约 90 KB/s，走代理 316 KB/s；`fetch-refs.sh` 支持
`HTTPS_PROXY=…` 与 `ATTACK_URL`/`D3FEND_URL` 覆盖，并会校验 JSON 完整性。

镜像里预装（版本与各套件默认钉的一致）：sigma-cli 3.1.0 + splunk / elasticsearch /
loki / kusto 四个后端、pySigma-validators-sigmahq 0.21.0、pymisp（与 misp 套件同版本）、scapy。
套件里的 `pip install` 与 `sigma plugin install` 于是变成空操作。

## 验证

```bash
# 1) 断网能读到参考数据：证明缓存真的在镜像里，而不是运行时又去拉了一次
docker run --rm --network none lance-detections:latest python3 -c \
  "from sigma.data.mitre_attack import mitre_attack_techniques as t; \
   from sigma.data.mitre_d3fend import mitre_d3fend_tactics as d; \
   print(len(t), 'ATT&CK techniques,', len(d), 'D3FEND tactics')"

# 2) 门禁真绿（不再卡住）
PYTHON_IMAGE=lance-detections:latest detections/tests/check-defense-template.sh

# 3) 八套检测套件整轮
PYTHON_IMAGE=lance-detections:latest detections/tests/run-all.sh
```

`enterprise-attack.json` / `d3fend.json` 只是构建输入，已被本目录的 `.gitignore` 忽略，
不进仓库。
