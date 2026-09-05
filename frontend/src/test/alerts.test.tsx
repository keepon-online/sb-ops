import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AlertsPage } from '../features/alerts/pages/AlertsPage';

vi.mock('../services/alerts', () => ({
  alertsApi: {
    list: vi.fn(() => Promise.resolve({ items: [], notifications: { pending: 0, failed: 0 } })),
    stats: vi.fn(() => Promise.resolve({
      daily: [
        { date: '2026-09-04', opened: 1, resolved: 0 },
        { date: '2026-09-05', opened: 2, resolved: 3 }
      ],
      totalOpened: 3,
      totalResolved: 3,
      currentOpen: 2,
      mttrSeconds: 3600
    })),
    acknowledge: vi.fn()
  }
}));

function renderAlertsPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <AlertsPage />
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

describe('alerts trend section', () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it('渲染趋势统计卡片与双线图表', async () => {
    renderAlertsPage();
    expect(await screen.findByText('1.0 小时')).toBeInTheDocument();
    expect(screen.getByText('当前开放')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '告警趋势，新增与恢复每日计数，悬停查看数值' })).toBeInTheDocument();
    // 图表横轴标签取 date 的月-日段
    expect(screen.getByText('09-04')).toBeInTheDocument();
  });
});
