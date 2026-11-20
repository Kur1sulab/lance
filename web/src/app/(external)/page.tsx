"use client";

// 根路径不做 server redirect：本项目走 `output: "export"` 静态导出，
// 没有 Node 运行时，`redirect()` 在构建期无法执行，Next 会把它当作渲染错误，
// 导出一个 __next_error__ 页面（表现为 / 永远是错误页，而 /function/tasks 正常）。
// 改成客户端跳转，静态托管下也能工作。
import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function Home() {
  const router = useRouter();

  useEffect(() => {
    router.replace("/function/tasks");
  }, [router]);

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "60vh",
        gap: 10,
        fontSize: 14,
        opacity: 0.72,
      }}
    >
      <span
        aria-hidden
        style={{
          width: 12,
          height: 12,
          borderRadius: "50%",
          border: "2px solid currentColor",
          borderTopColor: "transparent",
          display: "inline-block",
          animation: "lance-enter-spin 0.8s linear infinite",
        }}
      />
      正在进入控制台…
      <style>{`@keyframes lance-enter-spin{to{transform:rotate(360deg)}}`}</style>
      <noscript>
        <a href="/function/tasks" style={{ textDecoration: "underline" }}>
          继续
        </a>
      </noscript>
    </div>
  );
}
