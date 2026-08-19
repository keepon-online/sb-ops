import { Typography } from 'antd';
import type { ReactNode } from 'react';

// UsageGauge 用环形进度呈现「容量占比」类指标（CPU / 内存 / 磁盘）：中心是
// 当前百分比，环下是标题与明细。色阶由调用方按阈值传入。
export function UsageGauge({ value, label, detail, color }: { value: number; label: string; detail?: ReactNode; color?: string }) {
  const clamped = Math.min(100, Math.max(0, value));
  const radius = 52;
  const circumference = 2 * Math.PI * radius;
  return (
    <div className="gauge-cell" role="img" aria-label={`${label}使用率 ${clamped.toFixed(1)}%`}>
      <svg viewBox="0 0 120 120" width={124} height={124} aria-hidden>
        <circle cx="60" cy="60" r={radius} fill="none" stroke="var(--border-color)" strokeWidth="10" />
        <circle
          cx="60" cy="60" r={radius} fill="none"
          stroke={color || 'var(--primary-color)'} strokeWidth="10" strokeLinecap="round"
          strokeDasharray={circumference} strokeDashoffset={circumference * (1 - clamped / 100)}
          transform="rotate(-90 60 60)" style={{ transition: 'stroke-dashoffset 0.6s ease' }}
        />
        <text x="60" y="63" textAnchor="middle" fill="var(--text-color)" fontSize="21" fontWeight="600">{clamped.toFixed(1)}%</text>
      </svg>
      <div>
        <Typography.Text strong>{label}</Typography.Text>
        {detail ? <div><Typography.Text type="secondary" style={{ fontSize: 12 }}>{detail}</Typography.Text></div> : null}
      </div>
    </div>
  );
}
