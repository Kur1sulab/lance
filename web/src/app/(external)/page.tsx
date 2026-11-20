"use client";

// 根路径 = 品牌门面（未登录）/ 控制台入口（已登录自动跳）。
//
// 版面语言参考 pavise.club：
//   · 英文小标签 + 编号（01 / ...）做区块索引
//   · 超大中文短句标题，句尾带句号
//   · 大留白、单像素分隔线、两栏（标签列 + 内容列）
//   · 四步流程横条、FAQ 手风琴、底部一句 CTA
// 色彩沿用内部 lance 主题：纯灰阶、无彩度、靠形状与线宽分层。
//
// 本项目走 output:"export" 静态导出，不能用 server redirect（会被导出成
// __next_error__ 页面），跳转一律在客户端做。
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { auth } from "@/lib/auth";

const VERSION = "2.2.0";

// logo 取自 public/logo.svg：四角切括号 + 斜穿长枪 + 实心枪头。
const CORNERS = [
  "M220,680 L220,220 L680,220",
  "M1240,220 L1700,220 L1700,680",
  "M1700,1240 L1700,1700 L1240,1700",
  "M680,1700 L220,1700 L220,1240",
];
const SHAFT = "M400,1690 L1200,540";
const TIP = "M1327,360 L1306,615 L1200,540 L1094,465 Z";

const STEPS = [
  ["01", "定目标", "描述测试对象与要达成什么"],
  ["02", "派意图", "planner 把探索方向写进探索图"],
  ["03", "认领执行", "worker 并发取活，可单条掐停"],
  ["04", "出报告", "只有过验证的发现才归档"],
];

const FAQ = [
  [
    "会自动挑出最优攻击路径吗？",
    "不会。规划者只产出候选意图并给出理由，执行顺序由优先级与你设置的规则决定；没有配置 LLM 时整条链路不出计划。",
  ],
  [
    "误报怎么处理？",
    "发现必须过验证才算确认，验证状态分 verified / refuted / unreproducible。只有 verified 能进报告，报告头部带可重放率与误报率。",
  ],
  [
    "会把目标打挂吗？",
    "拦截规则先于允许规则匹配，命中即禁止测试；资产级与任务级规则都生效，未配置白名单时不启用放行。",
  ],
  [
    "每一步能追吗？",
    "意图、动作、审批、流量都落库。编排层另有 lance-ledger.jsonl，每条动作带 why 与 conf，可离线回放审计。",
  ],
];

const css = `
.lp {
  position: relative;
  /* Pavise 式机甲配色：深蓝黑底 + 猩红强调。
     品牌门面固定自己的一套色，不跟随控制台的主题偏好。 */
  --lp-bg: #07070c;
  --lp-bg-soft: #0d0d15;
  --lp-fg: #f3f3f7;
  --lp-dim: #8b8b9c;
  --lp-line: #1b1b28;
  --lp-red: #e5122c;
  --lp-red-bright: #ff3049;
  --lp-red-dim: rgba(229, 18, 44, 0.34);
  --background: var(--lp-bg);
  --foreground: var(--lp-fg);
  --muted-foreground: var(--lp-dim);
  --border: var(--lp-line);
  --primary: var(--lp-red);
  --primary-foreground: #ffffff;
  color-scheme: dark;
  background: var(--lp-bg);
  color: var(--lp-fg);
  isolation: isolate;
}
/* 极淡网格：只给「仪表」质感，不抢内容 */
.lp::before {
  content: "";
  position: fixed;
  inset: 0;
  background-image:
    linear-gradient(to right, rgba(255,60,80,.045) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(255,60,80,.045) 1px, transparent 1px);
  background-size: 64px 64px;
  mask-image: linear-gradient(to bottom, #000 0%, #000 60%, transparent 100%);
  pointer-events: none;
  z-index: -1;
}

/* ── 顶栏 ── */
.lp-top {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 16px clamp(18px, 4vw, 56px);
  border-bottom: 1px solid var(--border);
  /* 纯色不透明：color-mix + backdrop-filter 在滚动截图中会在顶栏下方留一层
     半透明残影（下层文字透上来），静态门面用实底更干净。 */
  background: var(--background);
}
.lp-brand { display: flex; align-items: center; gap: 10px; text-decoration: none; color: inherit; }
.lp-brand svg { width: 22px; height: 22px; flex: none; }
.lp-brand b { font-size: 13px; font-weight: 600; letter-spacing: .34em; }
.lp-brand span { font-family: var(--font-mono, ui-monospace, monospace); font-size: 10px; letter-spacing: .2em; color: var(--muted-foreground); }
.lp-nav { display: flex; align-items: center; gap: clamp(12px, 2vw, 26px); font-family: var(--font-mono, ui-monospace, monospace); font-size: 10.5px; letter-spacing: .18em; text-transform: uppercase; }
.lp-nav a { color: var(--muted-foreground); text-decoration: none; transition: color .18s ease; }
.lp-nav a:hover { color: var(--foreground); }
.lp-nav .lp-go { color: var(--lp-red-bright); border-bottom: 1px solid var(--lp-red); padding-bottom: 2px; }

/* ── 通用区块 ── */
.lp-sec { padding: clamp(64px, 9vw, 132px) clamp(18px, 4vw, 56px); border-top: 1px solid var(--border); }
.lp-sec:first-of-type { border-top: 0; }
.lp-lab {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 10px;
  letter-spacing: .26em;
  text-transform: uppercase;
  color: var(--lp-red);
  display: inline-flex;
  align-items: center;
  gap: 9px;
}
.lp-lab::before {
  content: "";
  width: 14px;
  height: 1px;
  background: var(--lp-red);
  flex: none;
}
.lp-h { margin: 18px 0 0; font-size: clamp(26px, 4.6vw, 58px); font-weight: 600; line-height: 1.14; letter-spacing: -.01em; }
.lp-h .lp-dim { color: var(--muted-foreground); }
.lp-p { margin: 18px 0 0; font-size: clamp(13.5px, 1.25vw, 15.5px); line-height: 1.9; color: var(--muted-foreground); max-width: 46em; }
.lp-p b { color: var(--foreground); font-weight: 500; }

/* 两栏：标签列 + 内容列 */
.lp-two { display: flex; flex-wrap: wrap; gap: clamp(18px, 3vw, 48px); align-items: flex-start; }
.lp-two > .lp-col-lab { flex: 0 0 auto; min-width: 128px; }
.lp-two > .lp-col-body { flex: 1 1 320px; min-width: 0; }

/* ── Hero ── */
.lp-hero { padding: clamp(48px, 8vw, 104px) clamp(18px, 4vw, 56px) clamp(40px, 6vw, 72px); }
.lp-hero-mark { width: clamp(74px, 9vw, 116px); height: auto; display: block; }
.lp-hero-mark .s { fill: none; stroke: var(--lp-red-bright); stroke-linecap: round; stroke-linejoin: round; stroke-dasharray: 1; stroke-dashoffset: 1; animation: lp-draw 1.35s cubic-bezier(.6,0,.2,1) .12s forwards; filter: drop-shadow(0 0 14px var(--lp-red-dim)); }
.lp-hero-mark .f { fill: var(--lp-red-bright); opacity: 0; animation: lp-tip .45s ease-out 1.3s forwards; }
/* 顶栏小标：不做绘制动画，纯描边红 */
.lp-brand svg .s { fill: none; stroke: var(--lp-red-bright); stroke-linecap: round; stroke-linejoin: round; }
.lp-brand svg .f { fill: var(--lp-red-bright); }
@keyframes lp-draw { to { stroke-dashoffset: 0; } }
@keyframes lp-tip { to { opacity: 1; } }

.lp-mega {
  margin: clamp(22px, 3.4vw, 40px) 0 0;
  font-size: clamp(44px, 12.2vw, 168px);
  font-weight: 700;
  line-height: .92;
  letter-spacing: -.03em;
  text-transform: uppercase;
}
.lp-mega span { display: block; }
.lp-mega .lp-out {
  -webkit-text-stroke: 1.5px var(--lp-red);
  color: transparent;
}
.lp-sub {
  margin: clamp(20px, 3vw, 32px) 0 0;
  font-size: clamp(13.5px, 1.4vw, 17px);
  line-height: 1.85;
  color: var(--muted-foreground);
  max-width: 40em;
}
.lp-cta-row { display: flex; flex-wrap: wrap; align-items: center; gap: 14px; margin-top: clamp(26px, 3.4vw, 38px); }
.lp-cta {
  display: inline-flex; align-items: center; gap: 10px;
  padding: 14px 30px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11.5px; letter-spacing: .22em; text-transform: uppercase;
  text-decoration: none;
  color: var(--primary-foreground); background: var(--primary);
  clip-path: polygon(12px 0, 100% 0, 100% calc(100% - 12px), calc(100% - 12px) 100%, 0 100%, 0 12px);
  transition: background .18s ease, color .18s ease, box-shadow .18s ease;
}
.lp-cta:hover, .lp-cta:focus-visible { background: transparent; color: var(--lp-red-bright); box-shadow: inset 0 0 0 1px var(--lp-red), 0 0 26px var(--lp-red-dim); outline: none; }
.lp-cta-2 {
  display: inline-flex; align-items: center; gap: 8px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11px; letter-spacing: .2em; text-transform: uppercase;
  color: var(--muted-foreground); text-decoration: none;
  border-bottom: 1px solid var(--border); padding-bottom: 3px;
  transition: color .18s ease, border-color .18s ease;
}
.lp-cta-2:hover { color: var(--foreground); border-color: var(--foreground); }

/* ── 四步流程条 ── */
.lp-steps { display: flex; flex-wrap: wrap; border-top: 1px solid var(--lp-red-dim); margin-top: clamp(36px, 5vw, 56px); }
.lp-step { flex: 1 1 200px; padding: 20px clamp(14px, 2vw, 26px) 22px; border-left: 1px solid var(--border); }
.lp-step:first-child { border-left: 0; padding-left: 0; }
.lp-step .n { font-family: var(--font-mono, ui-monospace, monospace); font-size: 10px; letter-spacing: .24em; color: var(--lp-red); }
.lp-step h4 { margin: 10px 0 0; font-size: 15px; font-weight: 600; }
.lp-step p { margin: 7px 0 0; font-size: 12.5px; line-height: 1.7; color: var(--muted-foreground); }

/* ── 三体能力（两栏 × 三行） ── */
.lp-rows { display: flex; flex-direction: column; margin-top: clamp(28px, 4vw, 44px); border-top: 1px solid var(--border); }
.lp-row { display: flex; flex-wrap: wrap; gap: clamp(12px, 2.4vw, 40px); padding: clamp(20px, 2.6vw, 30px) 0; border-bottom: 1px solid var(--border); }
.lp-row .k { flex: 0 0 auto; min-width: 132px; padding-left: 13px; position: relative; font-family: var(--font-mono, ui-monospace, monospace); font-size: 10.5px; letter-spacing: .22em; text-transform: uppercase; color: var(--lp-red); }
.lp-row .k::before { content: ""; position: absolute; left: 0; top: 2px; bottom: 2px; width: 2px; background: var(--lp-red); opacity: .85; }
.lp-row .t { flex: 1 1 180px; font-size: clamp(15px, 1.6vw, 19px); font-weight: 500; }
.lp-row .d { flex: 2 1 300px; font-size: 13px; line-height: 1.8; color: var(--muted-foreground); }

/* ── FAQ 手风琴 ── */
.lp-faq { margin-top: clamp(28px, 4vw, 44px); border-top: 1px solid var(--border); }
.lp-faq details { border-bottom: 1px solid var(--border); }
.lp-faq summary {
  display: flex; align-items: flex-start; justify-content: space-between; gap: 20px;
  padding: 20px 0; cursor: pointer; list-style: none;
  font-size: clamp(14px, 1.5vw, 17px); font-weight: 500;
}
.lp-faq summary::-webkit-details-marker { display: none; }
.lp-faq summary .pm { font-family: var(--font-mono, ui-monospace, monospace); font-size: 15px; color: var(--lp-red); transition: transform .2s ease; flex: none; }
.lp-faq details[open] summary .pm { transform: rotate(45deg); }
.lp-faq .ans { padding: 0 0 22px; font-size: 13px; line-height: 1.9; color: var(--muted-foreground); max-width: 52em; }

/* ── 底部 ── */
.lp-foot { padding: clamp(56px, 8vw, 110px) clamp(18px, 4vw, 56px) 34px; border-top: 1px solid var(--border); }
.lp-foot .lp-h { margin-top: 16px; }
.lp-foot-bar {
  display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px;
  margin-top: clamp(40px, 6vw, 72px); padding-top: 20px; border-top: 1px solid var(--border);
  font-family: var(--font-mono, ui-monospace, monospace); font-size: 10px; letter-spacing: .2em;
  text-transform: uppercase; color: var(--muted-foreground);
}

@media (max-width: 640px) {
  .lp-step { flex: 1 1 100%; border-left: 0; border-top: 1px solid var(--border); padding-left: 0; }
  .lp-step:first-child { border-top: 0; }
  .lp-row .k { min-width: 100%; }
}

@media (prefers-reduced-motion: reduce) {
  .lp-hero-mark .s { animation: none; stroke-dashoffset: 0; }
  .lp-hero-mark .f { animation: none; opacity: 1; }
}
`;

function Mark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 1920 1920" role="img" aria-label="Lance">
      <g>
        {CORNERS.map((d, i) => (
          <path
            key={i}
            d={d}
            pathLength={1}
            className="s"
            strokeWidth={90}
            style={{ animationDelay: `${0.12 + i * 0.1}s` }}
          />
        ))}
        <path d={SHAFT} pathLength={1} className="s" strokeWidth={120} style={{ animationDelay: "0.55s" }} />
      </g>
      <path d={TIP} className="f" />
    </svg>
  );
}

export default function LandingPage() {
  const router = useRouter();
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    if (auth.getToken()) {
      router.replace("/function/tasks");
      return;
    }
    setMounted(true);
  }, [router]);

  return (
    <div className="lp" style={{ visibility: mounted ? "visible" : "hidden" }}>
      <style>{css}</style>

      <header className="lp-top">
        <Link prefetch={false} href="/" className="lp-brand">
          <Mark />
          <b>LANCE</b>
          <span>/ CONSOLE</span>
        </Link>
        <nav className="lp-nav">
          <a href="#workspace">编排</a>
          <a href="#scenes">两种模式</a>
          <a href="#faq">须知</a>
          <Link prefetch={false} href="/login" className="lp-go">
            进入 ↗
          </Link>
        </nav>
      </header>

      {/* ── HERO ── */}
      <section className="lp-hero">
        <Mark className="lp-hero-mark" />
        <h1 className="lp-mega">
          <span>LANCE</span>
          <span className="lp-out">PENETRATION</span>
        </h1>
        <p className="lp-sub">
          决策与执行分离的自主渗透系统。
          <br />
          规划者发意图，执行者认领，报告者归档——每一步都留痕，每条动作都带理由。
        </p>
        <div className="lp-cta-row">
          <Link prefetch={false} href="/login" className="lp-cta">
            进入控制台 →
          </Link>
          <a href="#workspace" className="lp-cta-2">
            先看看它怎么工作 ↓
          </a>
        </div>

        <div className="lp-steps">
          {STEPS.map(([n, t, d]) => (
            <div className="lp-step" key={n}>
              <div className="n">{n} /</div>
              <h4>{t}</h4>
              <p>{d}</p>
            </div>
          ))}
        </div>
      </section>

      {/* ── RELEASE ── */}
      <section className="lp-sec">
        <div className="lp-two">
          <div className="lp-col-lab">
            <div className="lp-lab">RELEASE / v{VERSION}</div>
          </div>
          <div className="lp-col-body">
            <h2 className="lp-h">
              当前版本。
              <span className="lp-dim">三体各司其职。</span>
            </h2>
            <div className="lp-rows">
              <div className="lp-row">
                <div className="k">01 / planner</div>
                <div className="t">把探索方向写成意图</div>
                <div className="d">读资产图、查已有事实、判目标是否达成，产出带优先级的意图节点；资产变化会唤醒它重新规划。</div>
              </div>
              <div className="lp-row">
                <div className="k">02 / worker</div>
                <div className="t">并发认领，单条可掐停</div>
                <div className="d">N 个执行体从 frontier 取活；纠偏走 steer 队列，下一次工具调用前生效，不用杀进程。</div>
              </div>
              <div className="lp-row">
                <div className="k">03 / reporter</div>
                <div className="t">只归档过验证的发现</div>
                <div className="d">验证状态机拦在报告导出之前，报告头部带可重放率与误报率——数字可查，不接受模型自报。</div>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ── WORKSPACE ── */}
      <section className="lp-sec" id="workspace">
        <div className="lp-lab">THE CURRENT WORKSPACE</div>
        <h2 className="lp-h">
          目标进来。<span className="lp-dim">意图出去。</span>
        </h2>
        <p className="lp-p">
          输入只要求两件事：<b>测什么</b>与<b>要拿到什么</b>。剩下的拆解、排期、复验由引擎接手，
          你随时能看到它此刻在第几层、卡在哪、为什么这么走。
        </p>
      </section>

      {/* ── 两种模式 ── */}
      <section className="lp-sec" id="scenes">
        <div className="lp-lab">TWO SCENES. ONE SESSION MODEL.</div>
        <h2 className="lp-h">
          全自动与半自动。<span className="lp-dim">边界清楚。</span>
        </h2>
        <div className="lp-rows">
          <div className="lp-row">
            <div className="k">01 / 你按下才动手</div>
            <div className="t">编排层</div>
            <div className="d">只输出计划：四类动作、参数白名单、每条带 why 与 conf，低于阈值不执行。人不按，它不动。</div>
          </div>
          <div className="lp-row">
            <div className="k">02 / 认领即执行</div>
            <div className="t">平台</div>
            <div className="d">意图被 worker 认领后直接开打，拦截规则、审批闸门与流量录制全程在线。</div>
          </div>
        </div>
      </section>

      {/* ── FAQ ── */}
      <section className="lp-sec" id="faq">
        <div className="lp-lab">BEFORE YOUR NEXT ROUND</div>
        <h2 className="lp-h">开始之前，先了解这些。</h2>
        <div className="lp-faq">
          {FAQ.map(([q, a]) => (
            <details key={q}>
              <summary>
                <span>{q}</span>
                <span className="pm" aria-hidden>
                  +
                </span>
              </summary>
              <div className="ans">{a}</div>
            </details>
          ))}
        </div>
      </section>

      {/* ── 底部 CTA ── */}
      <footer className="lp-foot">
        <div className="lp-lab">LANCE AUTO / v{VERSION}</div>
        <h2 className="lp-h">下一个目标，按你的意图开始。</h2>
        <div className="lp-cta-row">
          <Link prefetch={false} href="/login" className="lp-cta">
            进入控制台 ↗
          </Link>
        </div>
        <div className="lp-foot-bar">
          <span>Lance · autonomous penetration system</span>
          <span>darwin / arm64 · v{VERSION}</span>
        </div>
      </footer>
    </div>
  );
}
