# 自主 AI 攻击防御与检测指南

> 本文帮**防守方**理解 LANCE 这类**自主 AI 渗透智能体**如何运作，以便建立**检出并阻断**这类攻击的能力。它不是攻击操作指南。全部内容只用于保护自己拥有或已获得书面明确授权的系统。未经授权探测、攻击他人的信息通信网络，本身即构成犯罪（参见[顶层 README 的安全与滥用警示](../README.md)）。
>
> English: **[Defense and Detection Guide (defense-en.md)](defense-en.md)**

自主 AI 攻击工具把过去由一个人手工执行的渗透测试，变成 LLM 智能体**自行拆解目标、实际调用工具、持续累积发现**的 24 小时自动流程。防守方面对的对手，从「一名熟练攻击者」变成「一群不知疲倦、不停歇的智能体」。本文梳理这一变化对检测与响应提出了什么要求。

---

## 1. 自主 AI 攻击与既有扫描器的区别

传统漏洞扫描器（例如基于固定签名的工具）按预定检查项依次投递，跑完即止。LANCE 这类自主智能体结构不同。如[系统架构](../README.md#架构)所述，下列要素结合，使其**无需人工介入即可跑完多阶段攻击链**。

- **按角色分工的多智能体。** 负责拆解目标的 `goals`、判定下一步方向并产出意图（intent）的 `planner`、各自把一条意图落到真实工具的多个 `worker`，以及供人介入的 `mainagent`。planner 是唯一的意图生成者，worker 并行执行这些意图。
- **在双图上累积状态。** 「有什么」（资产图）与「测到哪儿了」（探索图）分开累积，再用锚点关联。攻击因此**逐步加深**，会从不同角度重访同一资产，并在已有观察之上安排下一步。
- **事件驱动的闭环。** 图一变，planner 就被唤醒并派发下一个意图；worker 的写入又触发下一轮。这个循环要等目标被证明才停。
- **串行攻击链的稳定推进。** planner 把「找到注入点 → 取得凭据 → 横向移动 → 提权」这类依赖顺序一次性写进共享 todolist，只给前置步骤已满足的下一环派发意图。即便在无状态会话环境中，攻击链也不会错位，能跑完。
- **LLM 按上下文变形载荷。** 执行工具与载荷不是代码里的固定常量，而是 LLM 读情境后生成的值，所以每个请求的形态各有差异。

### 为什么更难检测

- **固定签名对不上。** 载荷随上下文变化，靠精确匹配已知恶意字符串的规则（WAF 签名）容易被绕过。
- **可以放慢，也可以按人的节奏断点推进。** 智能体在轮次之间休息，并随发现改变方向，只看「短时间大量请求」的速率型检测会漏。
- **侦察与入侵连成一条流。** 人看完侦察结果、隔几天再手动下手的那个间隙消失了，从首次接触到数据外带的窗口被大幅压缩。

### 仍然能检出的理由：行为难以隐藏

静态指纹（User-Agent、特定载荷字符串）运营者想改就能改。但**自主智能体的行为形态**是攻击的本质，改不动。下面第 2～4 节的重心放在这套行为视角上。

- 同一个来源**把性质不同的多阶段请求**（侦察 → 枚举 → 认证尝试 → 利用）**串成连续会话**的模式。
- 人到了会因为疲劳停手的时段仍然**不间断**的探测。
- 从失败响应里取线索、**系统性地变形下一个请求**，自适应而非随机。

---

## 2. 防守方可以观测的指纹（IoC 与签名）

指纹按两个视角分开看。**(A) 目标（被攻击方）视角**：从打向本系统的 LANCE 流量里能看到的。**(B) 运营者与取证视角**：在 LANCE 实际运行过（或作为跳板被攻陷）的主机上能看到的。两者不要混。目标侧能看到的静态指纹有限，行为指纹才是核心。

### (A) 目标侧视角：指向本系统的流量

- **补全（enrich）查询的 User-Agent `lance-enrich/1.0`。** LANCE 自动补全资产信息（DNS、HTTP 探活）时，以该 User-Agent 直接向目标发 `GET`（`enrich/enrich.go`）。这条路径由 LANCE 自己产生，与 LLM 无关，特征是**不跟随重定向、关闭 keep-alive、只读响应开头以取出 `<title>`**。默认并发为 4。所以当带 `lance-enrich/1.0` UA 的查询表现为**短连接、单次 GET、只读标题即断开**，且同时打到多个资产时，强烈指向 LANCE 系补全流量。但运营者可以改 UA，**查不到不等于安全**。
- **真正的攻击流量沿用工具自身指纹。** worker 通过真实工具（Bash 执行的外部工具、HTTP）发请求。LANCE 只是往子进程的环境变量里注入 `HTTP_PROXY` 与代理 CA 路径，让这些流量走自己的录制代理，**不给攻击流量强加 LANCE 自有 User-Agent**。所以目标看到的 UA 与请求头，是**当时所执行工具的默认值**（各类命令行工具的默认 UA）。运营者没定制，就留下常见自动化工具指纹；定制了，也可能伪装成正常浏览器。因此**不要只靠单一 UA 匹配，要与行为检测结合**。
- **worker 内置的 WebFetch 工具会留下 `norma/0.4` User-Agent。** 与上面经 Bash 执行的外部工具不同，LANCE 用 norma SDK（`github.com/Kur1sulab/norma`）直接发起的 HTTP 查询（WebFetch 工具）会在攻击阶段带上该 SDK 的默认 UA `norma/0.4`。走明文 HTTP 时（或在 TLS 终结点上）可在网络侧观测到，[Suricata 规则 sid 1000003](../detections/suricata/README.md)匹配的正是这个前缀（`norma/`）。但该 UA 并非 LANCE 独有，其他基于 norma SDK 的工具也在用，所以和 `lance-enrich/1.0` 一样只是旁证；经 Bash 执行的工具各带自己的 UA，**查不到不等于安全**。
- **没有内置速率限制。** LANCE 自身没有针对目标流量的限速，请求速率由 LLM 驱动的外部工具决定。不过每个任务默认 3 个 worker 并行，同一个目标上可能有**多个意图同时推进**。也就是说它既可能表现为「单条慢会话」，也可能表现为「多角度并发」，单一固定阈值很难抓住。
- **行为签名（最重要）。** 下列模式的**同时出现**指向自主智能体。
  - 同一来源（或少数轮换来源）上，**侦察 → 目录与端点枚举 → 参数探测 → 认证与注入尝试**在短间隔内连锁。
  - 发往同一端点的连续请求，会**按响应码与响应长度系统性地变形**（不是随机 fuzz，而是自适应）。
  - 脱离人工作息的时段仍**长时间不间断**的会话。
  - 失败（401/403/429）之后不停手，继续尝试**绕过变形**的执着。

### (B) 运营者与取证视角：运行 LANCE 的主机

入侵调查中，寻找跳板或中转主机上 LANCE 被安装、被执行过的痕迹时参考。

- **默认监听端口 `:8787`。** LANCE 服务的默认 HTTP 接收地址（`cmd/lance/main.go`，可用 `--addr` 修改）。内网主机在这个端口上开着管理界面（仪表盘、任务、资产图），就是怀疑 LANCE 实例的依据。
- **录制型 MITM 代理 `127.0.0.1:8788`。** 拦截并记录 worker 全部 Bash 与 HTTP 执行的本地代理默认地址（`cmd/lance/main.go` 中 `--proxy` 的默认值，仅限回环）。它会自建 CA 解密并记录 TLS（`mitmproxy-ca-cert.pem`），所以两条线索有用：主机上是否存在**由 LANCE 安装的受信 CA 证书**；子进程环境里是否有注入 `HTTP_PROXY` 与代理 CA 路径的痕迹。该注入会进入 LANCE 生成的每个 worker 工具，变量名硬编码在源码里（`agent/worker.go`），因此**一个运行中的进程同时带着代理变量和 mitmproxy CA 信任变量**，比单纯一个端口更具特指性。[主机分类工具](../detections/triage/README.md)会在 `/proc`（取证镜像里则是抓下来的环境变量转储）中核对这个组合。
- **自更新回调 `lance-selfupdate`。** 自更新向 GitHub releases 查询时使用的 User-Agent（`selfupdate/`）。出站日志里出现以该 UA 发往代码托管站点的请求，说明该主机存在 LANCE 二进制。
- **PostgreSQL 上的双图。** 库里有 `exploration_nodes`、`assets`、`companies`、`activity` 等表，以及 `agent_prompts` 种子数据，是 LANCE 数据存储的特征。
- **存于数据库的正则审批规则与审计日志。** 评估工具调用的 intercept 规则存在库里，按优先级作为正则求值（`intercept/`）。被拦下的调用会连同以 `【LANCE 平台管控·非目标防御】` 开头的管控框架记进审计日志（`GET /api/audit`）。所以在被入侵主机的审计记录里看到这个字符串，可佐证 LANCE 曾在此运行。
- **破坏性命令的狩猎指标。** LANCE 自身护栏内置为拦截目标的命令模式，正是一台自主智能体**可能尝试**的命令族的镜像。在主机命令审计里可把下列当作狩猎指标：`rm -rf`、`mkfs`、`dd of=/dev/`、`shred`/`wipe`、SQL `DROP DATABASE`/`DROP TABLE`/`TRUNCATE`、MongoDB `drop`/`dropDatabase`、Redis `FLUSHALL`/`FLUSHDB`、`curl`/`wget` 的 `-X DELETE`，以及 `curl … | nc …` 这类数据外带管道。最后这类外带管道性质不同：它不是破坏数据的命令，而是把数据往外搬的泄露信号。与上面的破坏模式不同，LANCE 护栏对这一条是默认关闭内置的（正常测试用的反弹 shell 与数据传输管道误报很多），面向部署的破坏性命令狩猎规则（[`destructive_command_hunting.yml`](../detections/sigma/destructive_command_hunting.yml)）也限定在破坏范围、不含此模式，所以外带管道不要直接拦截，另设为狩猎指标并按环境调参。

> 小结：**目标侧防御押在行为指纹上，静态 UA（`lance-enrich/1.0`、`norma/0.4` 等）只作旁证。** 运营者与取证指纹（`:8787`、`127.0.0.1:8788`、`lance-selfupdate`、数据库结构、审计日志框架）在**调查被攻陷的中转主机时**有效。

### 为什么 IP 地址封禁是弱的第一道防线

安全事件一旦公开，社交平台和社区里常有人发「这是攻击 IP 列表，拿去在防火墙上封掉」。无论分享者的出发点如何，**把来源不明的非官方 IP 列表直接塞进封禁规则，不值得推荐**。面对自主 AI 攻击尤其如此。

- **来源难以核实。** 个人发布的非官方列表，无从确认是谁、依据什么收集的；混进与事件无关的 IP 也没有手段筛掉。
- **很快就过时。** 自主智能体会经 VPN、云实例、被劫持的中转服务器不断更换出口 IP（即 (A) 节说的「少数轮换来源」）。昨天观测到的攻击 IP，今天很可能已经弃用，封了列表，攻击者换个 IP 再来。
- **误封风险很大。** 列表里混入共享网段、CDN、正常云 IP 时，一封就把正当的客户流量或内部服务一起掐断。再叠加自动封禁（见第 6 节），误封的影响扩散得更快。

这不是说 IP 封禁毫无用处。**从响应机构或可信威胁情报拿到官方入侵指标（IoC），评估有效期与误封风险之后再落地**，它才有意义。但封一行 IP 只是追着轮换出口跑的临时措施；真正耐用的是难以改变的**行为**（第 2～4 节的行为型检测）和**攻击面收敛**（第 3、5 节的加固：清理暴露资产、打补丁、上 MFA）。「指纹可以换，行为难以藏」——本文的这个前提在这里同样成立。

---

## 3. 攻击者瞄准的入口与加固

自主智能体盯的是和人一样的**弱点**，只是重复得更快、更执着。下面是防守视角的优先加固点。

### 3.1 外部暴露的攻击面与已知（n-day）漏洞

自主智能体最先、也最稳定瞄准的入口，不是精巧的 0-day，而是**暴露在外、尚未修补的已公开漏洞**。边界设备（VPN、防火墙）、对公网开放的管理与运维控制台、应用服务器与中间件与框架（例如被广泛利用的 WebLogic、Struts 系），以及**不是主服务、而是挂给合作方、招聘或员工用的辅助系统**，都是典型目标。自主智能体先用资产图自动枚举暴露面，然后在补丁落地之前，用公开利用（PoC）同时打上百个资产的 n-day。「找到暴露弱点就试」这个环节的速度，正是它与人工攻击者拉开差距的地方。

- **收缩攻击面。** 持续维护互联网暴露资产、管理控制台、辅助系统的清单，不必对外的一律挪到内网、VPN 或白名单之后。
- **快速修补已知漏洞。** 边界设备、Web 服务器、应用服务器、中间件的已公开漏洞（n-day）是自主智能体的头号目标，所以对有公开 PoC 的组件，把补丁间隔压到最短。先修哪个，用一份汇集实际在野利用漏洞的清单作优先级输入：[CISA KEV（已知被利用漏洞）目录](https://www.cisa.gov/known-exploited-vulnerabilities-catalog)，并与 7.1 的韩国官方公告（KISA / Boho Nara）交叉核对。不要在文档里钉死具体 CVE 编号，而要跟着这种持续更新的官方清单走，自主智能体转向新流通的 n-day 时你才不会落后。
- **收紧不得不暴露的接口。** 对必须对外开放的管理与运维接口，叠加访问来源限制（IP 白名单）、MFA、VPN，把免认证的枚举本身挡掉。
- **辅助系统按主服务的标准管。** 合作方、招聘、员工用的辅助系统，补丁与监控水平要和主服务一致。自主智能体钻进去的入口，常常是这些辅助路径而非主服务。认证流程本身的加固接 3.2。

检测侧：针对某个漏洞已知路径（URL、参数）的请求，从外部短间隔连锁出现，就是 n-day 扫描的信号。这个信号与第 2 节的行为指纹、第 4 节的同源多阶段关联规则结合时最清楚。

### 3.2 辅助认证与身份核验流程

**通过主服务之外的路径接入的认证与身份核验流程**——增值服务、合作渠道、招聘渠道——校验往往更松，因而成为绕过目标。自主智能体会自动枚举这类路径，读取响应差异，系统地找出绕过条件。

- 身份核验与认证环节**与主服务同等强度**，并把可绕过的辅助路径逐条排查。
- 认证状态迁移（未登录 → 登录、普通 → 特权）**在服务端重新校验**，不要原样相信客户端送来的信任标记（Cookie、请求头、参数）。
- 检查身份核验令牌与一次性验证码的**有效期、可重用性、可猜测性**。

### 3.3 API 认证与授权（IDOR、越权）

- 所有对象访问都强制**服务端归属与权限校验**（挡住只改标识符就能打开别人资源的 IDOR）。
- 自己枚举水平、垂直越权路径。自主智能体会机械地增减标识符并批量尝试，**人工单点测试漏掉的洞**它很快就能挖出来。

### 3.4 撞库（credential stuffing）

拿泄露的账号口令列表逐个试的这类攻击，被自主智能体用**速度与分散**放大。

- 登录与身份核验端点加**自适应限速**（基于 IP、账号、设备、行为）。
- 敏感操作强制**多因素认证（MFA）**。撞库即使撞对口令，第二因子仍然挡得住。
- 用**凭据泄露检测**（比对已知泄露列表、登录位置与速度异常）前置拦截。
- 对登录失败与成功的**分布变化**告警（突然出现的低速大范围尝试）。

### 3.5 会话、令牌与密钥管理

- 会话令牌的**作用域、有效期、续期**压到最小，敏感迁移时重新认证。
- API 密钥与内部令牌**不要出现在响应、日志、错误信息里**（自主智能体会积极从错误响应里收集线索）。

---

## 4. 检测规则与日志模式（实务）

以不绑定特定产品的**伪规则**形式给出，请自行转写成你的 WAF、IPS、SIEM 语法。下面依托静态指纹的部分，以可直接部署的 [Sigma 规则（`detections/sigma/`）](../detections/README.md)提供。作为核心的行为与关联检测（4.1、4.2）同样无法归约成单条规则，但其中有 LANCE 源码可作依据的行为指标，以可部署的 [Sigma 关联规则（`detections/sigma/correlation/`）](../detections/README.md)提供：补全查询速率、补全查询的目标数量、护栏拦截突发、同一主机上护栏标记与破坏性命令同时出现。

纯 Web 的多阶段关联（枚举 → 探测 → 认证）需要按环境自建的基线规则，因为这段流量归约不到单一的 LANCE 独有 UA；下面 4.2 给了一份可直接调参的通用行为型 Sigma 基线模板，请按自己的 SIEM 与基线改造成起点。网络层走明文 HTTP 时（或在 TLS 终结点上）能观测到的 LANCE User-Agent 有两个，都以 [Suricata 规则（`detections/suricata/`）](../detections/suricata/README.md)提供：一个是补全探测器的 `lance-enrich/1.0`（存在性 sid 1000001、高速枚举变体 sid 1000002），另一个是 norma SDK 的 WebFetch 工具在攻击阶段发出的 `norma/0.4`（sid 1000003）。

### 4.1 WAF/IPS（基于行为）

- 单一来源上**性质不同的请求群**（静态资源占比低，枚举、参数探测、认证尝试占比高）**接成一条会话**，就加权。
- 对**随响应码与正文长度变形的连续请求**加权（熵高，但不是随机而是自适应）。
- 已知自动化 UA 如 `lance-enrich/1.0` 直接标为**高危**，但不要因为 UA 缺席就判定安全。

### 4.2 SIEM 关联规则

- **同源多阶段关联**：同一 IP、ASN 或会话上，(a) 目录与端点枚举、(b) 参数探测、(c) 认证或注入尝试**在短窗口内全部**出现，即报「疑似自主攻击」。
- **时段异常**：脱离服务正常流量分布、**长时间不间断**的单条会话。
- **失败后持续**：收到 403/429 之后仍不停手、继续做**绕过变形**的来源。

下面这份可直接部署的基线模板，就是上面的**同源多阶段关联**。攻击流量不带 LANCE 独有 User-Agent，所以与 `detections/sigma/` 下的 LANCE 源码型规则不同，它是**通用行为型规则**：只看「一个来源在短窗口内把枚举、探测、认证都做了一遍」这个行为，不看任何具体攻击工具的指纹。文件自足：三条子规则，加一条只有同一客户端在窗口内三样都满足才触发的时序关联规则。代码块与英文版逐字节一致，因此块内注释保持英文。

```yaml
# ── Generic behavioural template (NOT an LANCE-specific signature) ──
# The same-source multi-stage web pattern in defense guide section 4.2
# (enumeration -> probe -> auth). LANCE's attack traffic carries no LANCE
# fingerprint, so unlike the rules under detections/sigma/ this is a generic
# behavioural starting point, not grounded in LANCE source. Field names
# (SigmaHQ webserver taxonomy) and the thresholds/window WILL need tuning to
# your own logs and baseline. Self-contained: three sub-rules plus a temporal
# correlation that fires only when all three occur from one client in the window.
title: Web Endpoint Enumeration Burst From One Source
id: f03c360c-dc33-4a8a-afa8-821b1ff5c4e3
status: experimental
description: |
    Stage 1 of the same-source multi-stage pattern in the LANCE defense guide section 4.2: a
    burst of endpoint or directory enumeration from a single client, seen as a high rate of 404
    and 400 responses in a short window. This is generic behaviour, not an LANCE-specific
    signature; tune the count and window to your own baseline. On its own this leg is low signal
    and earns weight only inside the correlation below.
references:
    - https://github.com/Kur1sulab/LANCE/blob/main/docs/defense-en.md
author: Lance defense guide (generic template)
date: 2026-10-07
tags:
    - attack.reconnaissance
    - attack.t1595
logsource:
    category: webserver
detection:
    enum_misses:
        sc-status:
            - 404
            - 400
    condition: enum_misses
falsepositives:
    - Broken links, authorised vulnerability scanners, or misconfigured clients that generate
      many 404 responses.
level: low
---
title: Web Parameter Or Path Injection Probe From One Source
id: f9296e55-6b7a-4030-b5f7-5f7b146233be
status: experimental
description: |
    Stage 2 of the same-source multi-stage pattern: parameter or path probing, matched here as
    query strings carrying common injection or traversal markers. This leg is unavoidably
    signature-like and noisy on its own, so it is scored low and earns weight only inside the
    correlation below. Extend the marker list to your own probe corpus and WAF categories; it is
    a coarse proxy for the broader "adapts requests to responses" behaviour the guide describes.
references:
    - https://github.com/Kur1sulab/LANCE/blob/main/docs/defense-en.md
author: Lance defense guide (generic template)
date: 2026-10-07
tags:
    - attack.initial-access
    - attack.t1190
logsource:
    category: webserver
detection:
    probe_markers:
        cs-uri-query|contains:
            - '../'
            - "' or "
            - ' union select '
            - '<script'
            - '; drop '
    condition: probe_markers
falsepositives:
    - Legitimate request payloads that resemble probe markers; tune the marker list to your
      application.
level: low
---
title: Authentication Or Identity-Verification Attempt From One Source
id: 2614cacb-7455-46ad-9af2-7b9633f12d7b
status: experimental
description: |
    Stage 3 of the same-source multi-stage pattern: requests to login, authentication, or
    identity-verification endpoints, or 401 and 403 responses. Map the paths and your own
    authentication-event fields to your application; auxiliary, affiliate, and broker channels
    often expose weaker identity-verification endpoints than the main service and belong here
    too. This leg is broad by design and is only meaningful inside the correlation below.
references:
    - https://github.com/Kur1sulab/LANCE/blob/main/docs/defense-en.md
author: Lance defense guide (generic template)
date: 2026-10-07
tags:
    - attack.credential-access
    - attack.t1110
logsource:
    category: webserver
detection:
    auth_path:
        cs-uri-stem|contains:
            - '/login'
            - '/auth'
            - '/verify'
            - '/otp'
    auth_deny:
        sc-status:
            - 401
            - 403
    condition: auth_path or auth_deny
falsepositives:
    - Ordinary users signing in; this leg is broad and only meaningful inside the correlation.
level: low
---
title: Same-Source Multi-Stage Web Attack (Enumeration, Probe, Auth)
id: 9b7c7b86-702f-42b8-be99-3e60a188ec5b
status: experimental
description: |
    The behaviour-based core of LANCE defense guide section 4.2 as a deployable template: one
    client runs endpoint enumeration, parameter or path probing, and an authentication or
    identity-verification attempt within the same short window. This is the pattern an autonomous
    agent drives at machine speed and keeps driving past 403 and 429 responses. It is UA-free and
    carries no LANCE fingerprint, so it is a GENERIC behavioural rule, not one of the
    LANCE-source-grounded rules under detections/sigma/. Normalise the client field (c-ip, or a
    session identifier if you have one) and tune the window to your baseline. If three legs are
    too strict and miss cases, relax to any two of the three.
references:
    - https://github.com/Kur1sulab/LANCE/blob/main/docs/defense-en.md
author: Lance defense guide (generic template)
date: 2026-10-07
tags:
    - attack.initial-access
    - attack.t1190
correlation:
    type: temporal
    rules:
        - f03c360c-dc33-4a8a-afa8-821b1ff5c4e3
        - f9296e55-6b7a-4030-b5f7-5f7b146233be
        - 2614cacb-7455-46ad-9af2-7b9633f12d7b
    group-by:
        - c-ip
    timespan: 10m
falsepositives:
    - An authorised vulnerability scan or QA run from a single source; allow-list its address.
level: high
```

使用这份模板时的注意点：

- 这个块用与检测包相同的工具验证过：`sigma check` 以 0 error、0 condition error 通过（SigmaHQ 附加规约只报文件名、分支链接与自定义 logsource 这几类——正是本项目校验基线里明确排除的，出现其他任何一类都算失败）；`sigma convert -t splunk` 能生成查询（三条子规则在 10 分钟窗口内按 `c-ip` 归组，三样齐备即触发）。但它的依据无法固定到 LANCE 源码，所以没有放进 `detections/` 那棵受测试的规则树，以维持那棵树「只收录源码中确认的东西，不做推测」的原则。这些事实不是一次性测得的数字：[`detections/tests/check-defense-template.sh`](../detections/tests/check-defense-template.sh)在本指南变动时于 CI 重跑一遍（`sigma check` 通过、splunk/eql/loki 转换、lucene/kusto 拒绝该关联、以及各语言版本之间规则正文逐字节相同），以免上面的说法随上游 `sigma-cli` 变更悄悄失效。
- 这份模板是关联（correlation）规则，`sigma convert` 输出整个模板还是只输出三条子规则，取决于后端是否支持 Sigma 关联转换。用同一固定版本（`sigma-cli` 3.1.0）实测：整个模板在 Splunk（`-t splunk`）、Elasticsearch EQL（`-t eql`）、Grafana Loki（`-t loki`）上能转换。而在 Microsoft `kusto` 后端（Sentinel、Defender）与 Elasticsearch Lucene（`-t lucene`）上关联规则不转换（`Backend does not support correlation rules`），这时只转换三条子规则，「10 分钟窗口、同一 `c-ip`」的关联在产品里自行表达（例如 Sentinel 计划分析规则的 `summarize ... by bin(TimeGenerated, 10m), <client>`）。这与检测包记录的移植性一致，实测支持表在 [Sigma 后端移植性](../detections/README.md#sigma-backend-portability)。
- 第二阶段（探测）依赖注入与目录穿越标记列表，是粗糙信号，单用误报很多。所以三条子规则的 `level` 都压低，只有三样在同一来源同时出现时的关联规则才升为高等级告警。
- 客户端按 `c-ip` 归组。在代理或 CDN 之后，换成从 `X-Forwarded-For` 还原的真实客户端地址，或用会话标识符。要求三阶段全都满足过于严格、有漏网时，放宽成三条中命中两条即触发。

### 4.3 认证日志

- 按账号与 IP 的**登录失败率突变**、跨大量账号的**低速分散尝试**（撞库的特征）、失败转成功的**异常迁移速度**。
- 对身份核验与一次性验证码端点的**枚举式访问**。

### 4.4 出站（egress）与取证

- 内网主机以 `lance-selfupdate` UA 向代码托管站点发出的请求。
- 内网有进程绑定在 `:8787`（管理界面）或 `127.0.0.1:8788`（录制代理）上。
- 以 `lance-enrich/1.0` UA 在短时间内查询大量外部资产的 DNS、HTTP 补全模式。

---

## 5. 加固清单

汇总成防守团队可以直接对照检查的形式。

- [ ] 登录、身份核验与敏感 API 已施加**自适应限速**（IP、账号、设备、行为）。
- [ ] 敏感操作强制**MFA**。
- [ ] 所有对象访问都有**服务端归属与权限校验**（挡住 IDOR）。
- [ ] 认证状态迁移**在服务端重新校验**，不原样相信客户端信任标记。
- [ ] **辅助、合作、招聘渠道**的身份核验强度已与主服务统一。
- [ ] 运营**泄露凭据的比对与入侵检测**。
- [ ] WAF 以**行为模式**运行，不只依赖固定签名。
- [ ] **不直接套用流传的非官方 IP 封禁列表**，而是从可信来源取得官方入侵指标（IoC），评估有效期与误封风险后再落地。
- [ ] SIEM 里配了**同源多阶段关联规则**。
- [ ] 认证、访问、出站日志**保留足够时长**（自主攻击很快，事后追溯靠这些材料）。
- [ ] 维护互联网暴露资产、管理控制台、辅助系统的**清单以收缩攻击面**，并对边界设备、应用服务器、中间件的**已知（n-day）漏洞快速打补丁**。
- [ ] 用网络**分段**压缩横向移动与提权的爆炸半径。
- [ ] 密钥（key、token）**不出现在响应、日志、错误信息里**。
- [ ] 准备了**自动拦截与隔离**响应（只等人批就追不上自主攻击的速度）。

---

## 6. 事件响应要点

自主 AI 攻击的特征是**速度**。一个人花一年做的入侵与外带，智能体能在短得多的时间里跑完。响应设计也要以此为前提。

- **拦截自动化前置。** 隔离可疑来源、使会话失效、速率骤降这类动作，要能做到在人工批准之前**自动触发**。全绑在人工审批环上，就跟不上攻击速度。
- **提前定好要留哪些日志。** 认证日志、访问日志（在可采集范围内含请求正文）、出站日志、DNS 查询。自主攻击的痕迹堆得很快，事后复原攻击链要靠这些材料。
- **按资产追踪受侵范围。** 攻击沿资产图蔓延，所以要复原从最初入口资产到横向移动、提权的**整条路径**，才能防住二次入侵。

### 6.1 可疑主机与流量分类（triage）流程

把怀疑 LANCE 介入时最先要确认的东西排成顺序。与第 2 节把指纹分成两个视角一样，分类也分成 **(A) 本服务是否被当成目标**与 **(B) 某台主机上是否跑过 LANCE**。任何一步都别凭单个命中下结论，要看多个指标与行为信号是否同时出现。静态指标全部缺席时，只要看到行为信号就继续查。

**(A) 目标侧：本服务是否被 LANCE 盯上**

1. 在访问与认证日志里查补全查询 User-Agent `lance-enrich/1.0`。看是否有不跟随重定向的单次 `GET`，在短间隔内同时打到多个资产（第 2 节 (A)）。运营者可以换 User-Agent，所以没命中也要往下一步走。
2. 找同一来源（或少数轮换来源）的**多阶段连锁**：侦察接端点枚举、参数探测、认证与注入尝试，间隔很短，会随响应码与长度调整，且 401/403/429 之后仍不停止绕过变形。这个行为信号比静态 User-Agent 留得久（第 2 节 (A) 行为签名）。
3. 若已运行 SIEM，就用 [Sigma 关联规则](../detections/README.md)（补全查询速率、目标数量、护栏拦截突发、护栏标记与破坏命令同时出现）把这一行为挂上去，命中的来源按上面「拦截自动化前置」的原则列入隔离与会话失效。

**(B) 主机取证：某台主机上是否跑过 LANCE**

在可疑主机上确认下列各项。指标的依据在第 2 节 (B) 与机器可读的[入侵指标清单](../detections/indicators/lance_indicators.csv)。下面五项只读检查里，前四项（监听端口、出站日志、审计日志、状态与记录存储）由[主机分类脚本](../detections/triage/)（`detections/triage/lance_host_triage.py`）一次替你跑完。第五项命令审计脚本不代跑：破坏性命令不是 LANCE 独有指纹，正当管理员也在用，属于狩猎线索，所以刻意没放进自动化指标，需要你自己拿主机命令历史比对。只有 shell 访问、没有 SIEM 时先跑脚本，每处命中都按下文说明只当线索看。

1. **监听端口。** 在主机上直接确认默认服务端口 `:8787` 与回环录制代理 `127.0.0.1:8788` 是否在监听。
   ```sh
   ss -ltnp | grep -E ':8787|:8788'   # use netstat -ltnp if ss is unavailable
   ```
   （该行注释与英文版逐字节一致，故保留英文。）这两个端口可用 `--addr`、`--proxy` 改动，所以查询为空时，也要一并看全部开放端口和是否有内部管理界面在跑。
2. **出站日志。** 在出站日志里查是否有以自更新 User-Agent `lance-selfupdate` 发往代码托管站点（GitHub releases）的请求（`selfupdate/`）。这提示该主机跑过 LANCE 二进制。
3. **审计日志。** 审计记录里出现护栏管控标记 `【LANCE 平台管控·非目标防御】`，可佐证 LANCE 的执行（`guard/guard.go`）。每次工具调用被拦都会以这个框架留痕。
4. **状态与记录存储。** LANCE 把探索图放在 PostgreSQL（`exploration_nodes`、`assets`、`companies`、`activity` 表与 `agent_prompts` 种子），状态与记录落在可执行文件旁的数据目录（`cmd/lance/main.go` 中 `--data` 的默认值）。该目录下直接是存放各任务产物的 `tasks/` 与存放对话记录的 `transcripts/`；录制代理的产物则再下一层，单独收在 `traffic/` 子目录（`server/manager.go` 在数据目录下打开 `traffic/` 作为录制代理的存储）。所以受信 CA 证书在 `traffic/_ca/mitmproxy-ca-cert.pem`，流量索引在 `traffic/_index/index.sqlite`，录下的请求与响应正文在 `traffic/_blobs/`。这三样与 `tasks/`、`transcripts/` 一起出现时，录制代理确实跑过的把握就更大。
5. **命令审计。** 把破坏性命令狩猎指标（第 2 节 (B) 末尾的 `rm -rf`、`DROP DATABASE`、`FLUSHALL`、外带管道等）与主机命令历史对照。正当管理员也敲同样的命令，所以只当线索。

静态指标（端口、User-Agent、标记）运营者可以改动或抹掉。因此**缺席不等于安全**，把 (A) 的行为信号与 (B) 的主机痕迹合起来判断，才是自主 AI 攻击分类的核心。

---

## 7. 韩国官方渠道：威胁指标、安全公告与报告义务

原文面向韩国防守组织：威胁指标与安全公告从官方渠道取，出事后要履行法律规定的报告义务。请以下面的官方来源为第一依据，而不是流传的非官方列表。

### 7.1 威胁指标与安全公告的获取渠道

- **KISA（韩国互联网振兴院）的 [Boho Nara / KrCERT/CC](https://www.boho.or.kr)** 提供安全公告、漏洞通报与入侵事件响应信息，并通过网络威胁信息分析共享体系（C-TAS）在机构之间共享威胁情报（C-TAS 是加盟机构之间的共享体系，与公开门户不同，需另行申请）。
- **[金融安全院（FSI）](https://www.fsec.or.kr)** 跨金融业共享入侵与威胁信息（金融业 ISAC）。属金融行业的话，这条渠道要一并关注。
- **[个人信息保护委员会（PIPC）](https://www.pipc.go.kr)** 公开个人信息泄露的报告标准与保护措施的告示、指南。

第 5 节加固清单里「从可信来源取得官方入侵指标」，指的就是这些渠道。即便是官方指标，也要先评估有效期与误封风险再落地——这个原则与第 2 节「为什么 IP 地址封禁是弱的第一道防线」所说的一致。

### 7.2 韩国法律下的报告义务

自主攻击蔓延很快，所以第 6 节的事件响应流程里要预先嵌入法定报告环节。以下是要点，确切适用对象、时限与要件须按各机构最新告示确认。

- **个人信息泄露**：依《个人信息保护法》第 34 条，自得知泄露起 72 小时内向个人信息保护委员会或 KISA 报告，并通知信息主体（报告要件包括涉及 1000 名以上信息主体、敏感信息或唯一标识信息的泄露、因外部非法访问导致的泄露等）。
- **入侵事件**：依《信息通信网利用促进与信息保护等相关法律》，信息通信服务提供者须在得知入侵事件后 24 小时内向科学技术信息通信部与 KISA（KrCERT/CC）报告。
- **金融公司**：依金融领域监督规定，可能还需另行向金融监督院、金融安全院等主管机构报告，请一并核对该规定。

实际报送时，入侵事件走 [Boho Nara](https://www.boho.or.kr) 或不加区号的 118（KISA 网络民事服务中心），个人信息泄露走个人信息保护委员会的[个人信息门户](https://www.privacy.go.kr)。时限很短，责任人姓名与联系路径要提前写进第 6 节的事件响应流程。

---

## 参考

- 上游项目：[Kur1sulab/LANCE](https://github.com/Kur1sulab/LANCE)（AGPL-3.0，当前无法访问，2026-10 核实）。本文档是该全球版仓库的防御资料。
- 顶层 [README 的安全与滥用警示、使用范围与法律提示](../README.md)。
- 本文为中文译本，原文以韩国为适用场景；涉及个人信息时同时适用韩国《信息通信网利用促进与信息保护等相关法律》与《个人信息保护法》。无论是否涉及个人信息，未经授权的检测在多数法域都构成犯罪，务必先取得书面授权并约定测试范围。
- 通用 Web 安全加固的标准参考：[OWASP Top 10](https://owasp.org/www-project-top-ten/)、[OWASP ASVS（应用安全验证标准）](https://owasp.org/www-project-application-security-verification-standard/)、[OWASP API Security Top 10](https://api-security.owasp.org/)。

> 本文会持续补充，以支持防守与检测能力。补充检测规则或加固项的建议，欢迎提 issue。
