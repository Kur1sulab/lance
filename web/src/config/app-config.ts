import { DEFAULT_LOCALE, type Locale, resolveLocale } from "@/i18n/config";

import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

// 浏览器标签页标题·搜索引擎元描述。写入 layout.tsx 的静态 metadata，对所有
// 会渲染到页面的 <title>·<meta name="description"> 中，因此按照构建时的默认 locale
// 对四种语言分别分支。zh 为与上游对照原样保留原文。标题不用长破折号，
// 使用冒号，写成“产品名: 说明”的形式。该 meta 是构建期静态值，运行时语言
// 不响应切换（仅为标签页标题一行，允许）。
const META_BY_LOCALE: Record<Locale, { title: string; description: string }> = {
  en: {
    title: "Lance: Autonomous Penetration Testing Console",
    description: "An LLM-driven system console for autonomous penetration testing",
  },
  zh: {
    title: "朗基努斯 · Lance —— 自主渗透测试控制台",
    description: "LLM 驱动的自主渗透测试系统控制台",
  },
  es: {
    title: "Lance: Consola de Pruebas de Penetración Autónomas",
    description: "Una consola de sistema impulsada por LLM para pruebas de penetración autónomas",
  },
  ja: {
    title: "Lance: 自律型ペネトレーションテストコンソール",
    description: "LLM を活用して自律的にペネトレーションテストを行うシステムコンソール",
  },
};

// 品牌唯一来源：换名字只改这一行，侧栏、标题、版权、登录页全部跟随。
export const BRAND = "Lance";

export const APP_CONFIG = {
  name: BRAND,
  version: packageJson.version,
  copyright: `© ${currentYear}, ${BRAND}.`,
  meta: META_BY_LOCALE[resolveLocale()] ?? META_BY_LOCALE[DEFAULT_LOCALE],
};
