import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { InteractiveChart } from '../components/InteractiveChart';

describe('interactive chart axis', () => {
  it('uses whole-number ticks for count data instead of duplicated quarter labels', () => {
    render(
      <svg data-testid="chart">
        <InteractiveChart
          height={140}
          integerTicks
          ariaLabel="活跃用户数趋势"
          labels={['7/1', '7/2']}
          formatValue={(value) => String(Math.round(value))}
          series={[{ name: '活跃用户', color: '#e8a33d', values: [0, 1] }]}
        />
      </svg>
    );
    // max=1 的整数轴只应有 0 与 1 两个刻度，不再出现 0,0,1,1,1。
    const texts = screen.getAllByText(/^(0|1)$/).map((node) => node.textContent);
    expect(texts).toContain('0');
    expect(texts).toContain('1');
    expect(texts.filter((value) => value === '0').length).toBe(1);
    expect(texts.filter((value) => value === '1').length).toBe(1);
  });

  it('keeps fractional quarter ticks for magnitude data by default', () => {
    render(
      <InteractiveChart
        height={220}
        ariaLabel="流量趋势"
        labels={['7/1', '7/2']}
        formatValue={(value) => `${Math.round(value)} B`}
        series={[{ name: '下行', color: '#42a56f', values: [100, 400] }]}
      />
    );
    expect(screen.getByText('0 B')).toBeInTheDocument();
    expect(screen.getByText('400 B')).toBeInTheDocument();
  });
});
