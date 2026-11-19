/**
 * API 错误的分类工具。
 *
 * 为什么单独一层：页面需要区分「加载失败」与「没有权限」两种呈现
 * （设计文档 §五：把没权限渲染成没数据是安全事故）。判定必须按
 * HTTP status，不能按后端文案——文案会随显示语言变化。
 */

import { ApiError } from "@/lib/api";

/** 403 / 401：账号能看到这个页面，但没有看这份数据的权限。 */
export function isPermissionError(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 403 || error.status === 401);
}

/** 错误码：只有 ApiError 带 status，其它异常一律 undefined（不编造）。 */
export function errorCodeOf(error: unknown): string | number | undefined {
  return error instanceof ApiError ? error.status : undefined;
}

/** 说人话的错误描述：Error.message 优先，其它类型兜底成字符串。 */
export function errorMessageOf(error: unknown): string {
  if (error instanceof Error && error.message.trim()) return error.message.trim();
  if (typeof error === "string" && error.trim()) return error.trim();
  return String(error);
}
