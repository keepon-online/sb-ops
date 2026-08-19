import { Empty, Typography } from 'antd';
import { useState } from 'react';

export type ChartSeries = { name: string; color: string; values: number[] };

// InteractiveChart renders a lightweight SVG line chart with y-axis value ticks,
// x-axis time labels, and a hover crosshair with a value tooltip. `area` fills
// under each line for magnitude trends; `maxValue` pins the y-axis (e.g. 100 for
// percentage charts) so the scale does not jump with the data; `integerTicks`
// switches the y-axis to whole-number steps for count data (users online),
// where quarter-splitting a small max would produce duplicate labels like
// 0,0,1,1,1.
export function InteractiveChart({ series, labels, formatValue, height, ariaLabel, emptyText = '当前范围暂无数据', area = false, maxValue, integerTicks = false }: { series: ChartSeries[]; labels: string[]; formatValue: (value: number) => string; height: number; ariaLabel: string; emptyText?: string; area?: boolean; maxValue?: number; integerTicks?: boolean }) {
  const [hover, setHover] = useState<number | null>(null);
  const width = 1000;
  const left = 68;
  const right = 16;
  const top = 14;
  const bottom = 30;
  const count = labels.length;
  const { yMax, tickValues } = axisScale(
    Math.max(1, ...(maxValue !== undefined ? [maxValue] : []), ...series.flatMap((item) => item.values)),
    integerTicks
  );
  const x = (index: number) => (count <= 1 ? (left + width - right) / 2 : left + (index * (width - left - right)) / (count - 1));
  const y = (value: number) => top + (1 - value / yMax) * (height - top - bottom);
  const labelStep = Math.max(1, Math.ceil(count / 6));
  const handleMove = (event: React.MouseEvent<SVGRectElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const svgX = ((event.clientX - rect.left) / rect.width) * width;
    const index = Math.round(((svgX - left) / (width - left - right)) * (count - 1));
    setHover(Math.min(count - 1, Math.max(0, index)));
  };
  if (count === 0) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyText} />;
  return <div className="traffic-chart" style={{ position: 'relative' }} role="img" aria-label={ariaLabel}>
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ width: '100%', display: 'block' }}>
      {tickValues.map((value) => <g key={value}>
        <line x1={left} y1={y(value)} x2={width - right} y2={y(value)} stroke="var(--border-color)" strokeWidth={value === 0 ? 1.5 : 0.5} />
        <text x={left - 6} y={y(value) + 4} textAnchor="end" fontSize="11" fill="var(--text-color)">{formatValue(value)}</text>
      </g>)}
      {labels.map((label, index) => index % labelStep === 0 || index === count - 1
        ? <text key={index} x={x(index)} y={height - 8} textAnchor="middle" fontSize="11" fill="var(--text-color)">{label}</text>
        : null)}
      {area && series.map((item) => (
        <polygon key={`${item.name}-area`} aria-hidden
          points={[
            `${x(0)},${y(0)}`,
            ...item.values.map((value, index) => `${x(index)},${y(value)}`),
            `${x(count - 1)},${y(0)}`
          ].join(' ')}
          fill={item.color} fillOpacity={0.1} stroke="none" />
      ))}
      {series.map((item) => (
        <polyline key={item.name} points={item.values.map((value, index) => `${x(index)},${y(value)}`).join(' ')} fill="none" stroke={item.color} strokeWidth="2.5" vectorEffect="non-scaling-stroke" />
      ))}
      {hover !== null && <>
        <line x1={x(hover)} y1={top} x2={x(hover)} y2={height - bottom} stroke="var(--border-color)" strokeDasharray="4 3" vectorEffect="non-scaling-stroke" />
        {series.map((item) => item.values[hover] !== undefined
          ? <circle key={item.name} cx={x(hover)} cy={y(item.values[hover])} r="4" fill={item.color} stroke="var(--bg-container)" strokeWidth="2" />
          : null)}
      </>}
      <rect x={left} y={top} width={width - left - right} height={height - top - bottom} fill="transparent" onMouseMove={handleMove} onMouseLeave={() => setHover(null)} />
    </svg>
    {hover !== null && (
      <div style={{ position: 'absolute', top: 6, left: `${Math.min(72, Math.max(14, (x(hover) / width) * 100))}%`, transform: 'translateX(-50%)', background: 'var(--bg-container)', border: '1px solid var(--border-color)', borderRadius: 6, padding: '6px 10px', pointerEvents: 'none', whiteSpace: 'nowrap', zIndex: 2, boxShadow: 'var(--card-shadow)' }}>
        <Typography.Text strong style={{ fontSize: 12 }}>{labels[hover]}</Typography.Text>
        {series.map((item) => (
          <div key={item.name} style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{ width: 8, height: 8, borderRadius: 2, background: item.color, display: 'inline-block' }} />
            <Typography.Text type="secondary">{item.name}</Typography.Text>
            <Typography.Text>{formatValue(item.values[hover] || 0)}</Typography.Text>
          </div>
        ))}
      </div>
    )}
  </div>;
}

// axisScale picks the y-axis top and tick values. Fractional mode quarters the
// range (5 gridlines); integer mode steps by a nice whole number (1/2/2.5/5×10ⁿ)
// so count data never shows duplicate or fractional labels, and pads yMax to a
// multiple of the step.
function axisScale(max: number, integerTicks: boolean): { yMax: number; tickValues: number[] } {
  if (!integerTicks) {
    return { yMax: max, tickValues: [0, 1, 2, 3, 4].map((tick) => (max * tick) / 4) };
  }
  const rawStep = Math.max(1, Math.ceil(max / 4));
  const magnitude = 10 ** Math.floor(Math.log10(rawStep));
  const step = [1, 2, 2.5, 5, 10].map((multiplier) => multiplier * magnitude).find((candidate) => candidate >= rawStep) ?? 10 * magnitude;
  const tickCount = Math.ceil(max / step);
  return { yMax: tickCount * step, tickValues: Array.from({ length: tickCount + 1 }, (_, index) => index * step) };
}
