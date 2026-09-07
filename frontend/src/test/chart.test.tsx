import { act, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
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

  it('renders at the measured container width and thins x labels on narrow cards', async () => {
    const observers: { callback: ResizeObserverCallback }[] = [];
    vi.stubGlobal('ResizeObserver', class {
      constructor(callback: ResizeObserverCallback) { observers.push({ callback }); }
      observe() { /* entries dispatched manually in the test */ }
      unobserve() { /* noop */ }
      disconnect() { /* noop */ }
    });
    const labels = Array.from({ length: 41 }, (_, index) => `L${index}`);
    const { container } = render(
      <InteractiveChart
        height={220}
        ariaLabel="流量趋势"
        labels={labels}
        formatValue={(value) => `${Math.round(value)} B`}
        series={[{ name: '下行', color: '#42a56f', values: labels.map(() => 100) }]}
      />
    );
    await act(async () => {
      observers[0]!.callback([{ contentRect: { width: 438 } } as unknown as ResizeObserverEntry], {} as ResizeObserver);
    });
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('viewBox')).toBe('0 0 438 220');
    // plot ≈ 354px → 每个标签留 80px → 4 段 → 0,11,22,33 + 末位 40，共 5 个 x 标签。
    const xLabels = Array.from(svg.querySelectorAll('text')).map((node) => node.textContent).filter((value) => /^L\d+$/.test(value));
    expect(xLabels).toEqual(['L0', 'L11', 'L22', 'L33', 'L40']);
    vi.unstubAllGlobals();
  });

  it('renders area mode with gradient-filled polygon', () => {
    const { container } = render(
      <InteractiveChart
        height={220}
        chartType="area"
        ariaLabel="面积图测试"
        labels={['10:00', '11:00']}
        formatValue={(v) => `${v} B`}
        series={[{ name: '上行', color: '#4f8ef7', values: [100, 200] }]}
      />
    );
    const polygon = container.querySelector('polygon');
    expect(polygon).toBeInTheDocument();
    expect(polygon?.getAttribute('fill')).toMatch(/^url\(#chart-grad-/);
  });

  it('renders stacked mode with cumulative max and layered polygons', () => {
    const { container } = render(
      <InteractiveChart
        height={220}
        chartType="stacked"
        ariaLabel="堆叠面积图测试"
        labels={['10:00', '11:00']}
        formatValue={(v) => `${v} B`}
        series={[
          { name: '上行', color: '#4f8ef7', values: [100, 200] },
          { name: '下行', color: '#42a56f', values: [300, 400] }
        ]}
      />
    );
    // At 11:00, sum is 200 + 400 = 600 B, which becomes the yMax
    expect(screen.getByText('600 B')).toBeInTheDocument();
    const polygons = container.querySelectorAll('polygon');
    expect(polygons.length).toBe(2);
  });
});
