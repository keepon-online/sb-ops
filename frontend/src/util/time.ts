import dayjs, { type Dayjs } from 'dayjs';

export const DATETIME_FORMAT = 'YYYY-MM-DD HH:mm:ss';

/**
 * 将各类日期输入（ISO 字符串、毫秒时间戳、Date 或 Dayjs）格式化为统一的 YYYY-MM-DD HH:mm:ss 字符串。
 * 若值为空或非法，返回指定的 fallback（默认 '—'）。
 */
export function formatDateTime(
  value: string | number | Date | Dayjs | null | undefined,
  fallback = '—'
): string {
  if (value == null || value === '') return fallback;
  const d = dayjs(value);
  if (!d.isValid()) return fallback;
  return d.format(DATETIME_FORMAT);
}
