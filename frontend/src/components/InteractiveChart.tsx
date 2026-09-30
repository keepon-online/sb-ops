import { Empty, Typography } from 'antd';
import { useId, useState } from 'react';
import { useElementWidth } from './useElementWidth';

export type ChartSeries = { name: string; color: string; values: number[] };
export type ChartType = 'line' | 'area' | 'stacked';

// InteractiveChart renders a lightweight SVG line chart with y-axis value ticks,
// x-axis time labels, and a hover crosshair with a value tooltip. `chartType`
// supports 'line', 'area' (gradient fill under lines), and 'stacked' (cumulative area);
// `maxValue` pins the y-axis (e.g. 100 for percentage charts) so the scale does
// not jump with the data; `integerTicks` switches the y-axis to whole-number steps
// for count data (users online), where quarter-splitting a small max would produce
// duplicate labels like 0,0,1,1,1. The SVG is rendered at the container's measured
// width so text labels are not distorted by horizontal stretching; x-axis label
// density adapts to that width.
export function InteractiveChart({
  series,
  labels,
  formatValue,
  height,
  ariaLabel,
  emptyText = '当前范围暂无数据',
  area = false,
  chartType,
  maxValue,
  integerTicks = false,
  showTotalInTooltip = true
}: {
  series: ChartSeries[];
  labels: string[];
  formatValue: (value: number) => string;
  height: number;
  ariaLabel: string;
  emptyText?: string;
  area?: boolean;
  chartType?: ChartType;
  maxValue?: number;
  integerTicks?: boolean;
  showTotalInTooltip?: boolean;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const [containerRef, width] = useElementWidth<HTMLDivElement>();
  const idPrefix = useId().replace(/:/g, '');
  const mode: ChartType = chartType ?? (area ? 'area' : 'line');

  const left = 68;
  const right = 16;
  const top = 14;
  const bottom = 30;
  const count = labels.length;

  // For stacked mode, compute cumulative series bands
  const stackedBands = mode === 'stacked' && count > 0
    ? series.map((s, sIdx) => {
        const bottoms: number[] = [];
        const tops: number[] = [];
        for (let i = 0; i < count; i++) {
          let b = 0;
          for (let prev = 0; prev < sIdx; prev++) {
            b += series[prev]?.values[i] || 0;
          }
          bottoms.push(b);
          tops.push(b + (s.values[i] || 0));
        }
        return { bottoms, tops };
      })
    : [];

  const rawMax = Math.max(
    1,
    ...(maxValue !== undefined ? [maxValue] : []),
    ...(mode === 'stacked'
      ? Array.from({ length: count }, (_, i) => series.reduce((sum, s) => sum + (s.values[i] || 0), 0))
      : series.flatMap((item) => item.values))
  );

  const { yMax, tickValues } = axisScale(rawMax, integerTicks);
  const x = (index: number) => (count <= 1 ? (left + width - right) / 2 : left + (index * (width - left - right)) / (count - 1));
  const y = (value: number) => top + (1 - value / yMax) * (height - top - bottom);
  const labelStep = Math.max(1, Math.ceil(count / Math.max(2, Math.floor((width - left - right) / 80))));

  const handleMove = (event: React.MouseEvent<SVGRectElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const svgX = ((event.clientX - rect.left) / rect.width) * width;
    const index = Math.round(((svgX - left) / (width - left - right)) * (count - 1));
    setHover(Math.min(count - 1, Math.max(0, index)));
  };

  if (count === 0) {
    return (
      <div className="traffic-chart" ref={containerRef} style={{ position: 'relative', width: '100%', height, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyText} />
      </div>
    );
  }

  return (
    <div className="traffic-chart" ref={containerRef} style={{ position: 'relative', width: '100%', height }} role="img" aria-label={ariaLabel}>
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ width: '100%', height, display: 'block' }}>
        <defs>
          {series.map((item, index) => (
            <linearGradient key={`grad-${index}`} id={`chart-grad-${idPrefix}-${index}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={item.color} stopOpacity={mode === 'stacked' ? 0.45 : 0.28} />
              <stop offset="100%" stopColor={item.color} stopOpacity={mode === 'stacked' ? 0.15 : 0.02} />
            </linearGradient>
          ))}
        </defs>

        {tickValues.map((value) => (
          <g key={value}>
            <line x1={left} y1={y(value)} x2={width - right} y2={y(value)} stroke="var(--border-color)" strokeWidth={value === 0 ? 1.5 : 0.5} />
            <text x={left - 6} y={y(value) + 4} textAnchor="end" fontSize="11" fill="var(--text-color)">{formatValue(value)}</text>
          </g>
        ))}

        {labels.map((label, index) =>
          index % labelStep === 0 || index === count - 1 ? (
            <text key={index} x={x(index)} y={height - 8} textAnchor="middle" fontSize="11" fill="var(--text-color)">{label}</text>
          ) : null
        )}

        {/* Area / Stacked Polygon Fills */}
        {mode === 'area' && series.map((item, index) => (
          <polygon
            key={`${item.name}-area`}
            aria-hidden
            points={[
              `${x(0)},${y(0)}`,
              ...item.values.map((value, idx) => `${x(idx)},${y(value)}`),
              `${x(count - 1)},${y(0)}`
            ].join(' ')}
            fill={`url(#chart-grad-${idPrefix}-${index})`}
            stroke="none"
          />
        ))}

        {mode === 'stacked' && stackedBands.map((band, index) => {
          const item = series[index];
          if (!item) return null;
          const topPoints = band.tops.map((topVal, idx) => `${x(idx)},${y(topVal)}`);
          const bottomPoints = band.bottoms.map((_, idx) => `${x(count - 1 - idx)},${y(band.bottoms[count - 1 - idx]!)}`);
          return (
            <polygon
              key={`${item.name}-stacked`}
              aria-hidden
              points={[...topPoints, ...bottomPoints].join(' ')}
              fill={`url(#chart-grad-${idPrefix}-${index})`}
              stroke="none"
            />
          );
        })}

        {/* Polylines for strokes */}
        {mode === 'stacked'
          ? stackedBands.map((band, index) => {
              const item = series[index];
              if (!item) return null;
              return (
                <polyline
                  key={item.name}
                  points={band.tops.map((topVal, idx) => `${x(idx)},${y(topVal)}`).join(' ')}
                  fill="none"
                  stroke={item.color}
                  strokeWidth="2.5"
                  vectorEffect="non-scaling-stroke"
                />
              );
            })
          : series.map((item) => (
              <polyline
                key={item.name}
                points={item.values.map((value, index) => `${x(index)},${y(value)}`).join(' ')}
                fill="none"
                stroke={item.color}
                strokeWidth="2.5"
                vectorEffect="non-scaling-stroke"
              />
            ))}

        {/* Hover crosshair and dots */}
        {hover !== null && (
          <>
            <line x1={x(hover)} y1={top} x2={x(hover)} y2={height - bottom} stroke="var(--border-color)" strokeDasharray="4 3" vectorEffect="non-scaling-stroke" />
            {series.map((item, index) => {
              const cyVal = mode === 'stacked' ? stackedBands[index]?.tops[hover] : item.values[hover];
              return cyVal !== undefined ? (
                <circle
                  key={item.name}
                  cx={x(hover)}
                  cy={y(cyVal)}
                  r="4"
                  fill={item.color}
                  stroke="var(--bg-container)"
                  strokeWidth="2"
                />
              ) : null;
            })}
          </>
        )}

        <rect x={left} y={top} width={width - left - right} height={height - top - bottom} fill="transparent" onMouseMove={handleMove} onMouseLeave={() => setHover(null)} />
      </svg>

      {hover !== null && (
        <div style={{
          position: 'absolute',
          top: 6,
          left: `${Math.min(72, Math.max(14, (x(hover) / width) * 100))}%`,
          transform: 'translateX(-50%)',
          background: 'var(--bg-container)',
          border: '1px solid var(--border-color)',
          borderRadius: 6,
          padding: '6px 12px',
          pointerEvents: 'none',
          whiteSpace: 'nowrap',
          zIndex: 2,
          boxShadow: 'var(--card-shadow)'
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4, gap: 12 }}>
            <Typography.Text strong style={{ fontSize: 12 }}>{labels[hover]}</Typography.Text>
            {(mode === 'stacked' || (showTotalInTooltip && series.length > 1)) && (
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                合计: <Typography.Text strong style={{ fontSize: 12 }}>{formatValue(series.reduce((sum, s) => sum + (s.values[hover] || 0), 0))}</Typography.Text>
              </Typography.Text>
            )}
          </div>
          {series.map((item) => (
            <div key={item.name} style={{ fontSize: 12, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ width: 8, height: 8, borderRadius: 2, background: item.color, display: 'inline-block' }} />
                <Typography.Text type="secondary">{item.name}</Typography.Text>
              </div>
              <Typography.Text>{formatValue(item.values[hover] || 0)}</Typography.Text>
            </div>
          ))}
        </div>
      )}
    </div>
  );
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
