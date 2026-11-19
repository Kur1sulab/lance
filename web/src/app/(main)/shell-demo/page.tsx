import { ShellDemoPage } from "../_components/shell-demo";

/**
 * 组件演示页：PageShell / PageHeader / Toolbar / DetailPanel 与四态的活样板。
 *
 * 不叫 `__shell` 是因为 Next 16 把 `_` 前缀目录当私有目录、不生成路由，
 * 当时只能靠 `%5F%5Fshell`（下划线转义）绕过，留下一个没人看得懂的目录名。
 * 改成普通路径后行为一致，URL 更直白。
 */
export default function Page() {
  return <ShellDemoPage />;
}
