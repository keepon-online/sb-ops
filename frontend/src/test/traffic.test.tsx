import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TrafficPage } from '../features/traffic/pages/TrafficPage';

vi.mock('../services/users', () => ({
  usersApi: {
    list: vi.fn(() => Promise.resolve({
      items: [
        { id: 'u1', name: 'Alice', enabled: true, createdAt: '2026-08-01T00:00:00Z' },
        { id: 'u2', name: 'Bob', enabled: true, createdAt: '2026-08-01T00:00:00Z' }
      ],
      total: 2
    })),
    listDevices: vi.fn((userId: string) => Promise.resolve({
      items: [
        { id: 'd1', name: 'iPhone 15', userId, enabled: true, createdAt: '2026-08-01T00:00:00Z' }
      ],
      subscriptionToken: 'tok123',
      maxDevices: 5
    }))
  }
}));

vi.mock('../services/protocols', () => ({
  protocolsApi: {
    list: vi.fn(() => Promise.resolve({ items: [] }))
  }
}));

vi.mock('../services/traffic', () => ({
  trafficApi: {
    series: vi.fn(() => Promise.resolve({
      items: [
        { bucket: '2026-08-14T10:00:00Z', upBytes: 12500000, downBytes: 25000000 }
      ]
    })),
    summary: vi.fn(() => Promise.resolve({
      upBytes: 12500000,
      downBytes: 25000000,
      totalBytes: 37500000,
      samples: 12
    })),
    rankings: vi.fn((params: { dimension: string; userId?: string }) => {
      if (params.dimension === 'device') {
        return Promise.resolve({
          items: [
            { id: 'd1', name: params.userId ? 'iPhone 15' : 'iPhone 15 (Alice)', upBytes: 5000000, downBytes: 15000000, totalBytes: 20000000 }
          ]
        });
      }
      return Promise.resolve({
        items: [
          { id: 'u1', name: 'Alice', upBytes: 12500000, downBytes: 25000000, totalBytes: 37500000 }
        ]
      });
    }),
    activeUsers: vi.fn(() => Promise.resolve({
      items: [{ bucket: '2026-08-14T10:00:00Z', users: 1 }],
      totalUsers: 1
    })),
    protocolShares: vi.fn(() => Promise.resolve({
      items: []
    }))
  }
}));

function renderTrafficPage(initialEntry = '/traffic') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <ConfigProvider>
        <AntApp>
          <QueryClientProvider client={client}>
            <TrafficPage />
          </QueryClientProvider>
        </AntApp>
      </ConfigProvider>
    </MemoryRouter>
  );
}

describe('traffic analysis enhancements', () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders bandwidth rate stats cards and chart controls', async () => {
    renderTrafficPage();
    expect(await screen.findByText('峰值网络速率')).toBeInTheDocument();
    expect(screen.getByText('平均网络速率')).toBeInTheDocument();
    expect(screen.getByText('流量体积')).toBeInTheDocument();
    expect(screen.getByText('网络速率')).toBeInTheDocument();
    expect(screen.getByText('折线')).toBeInTheDocument();
    expect(screen.getByText('面积')).toBeInTheDocument();
    expect(screen.getByText('堆叠')).toBeInTheDocument();
  });

  it('switches between volume and rate views', async () => {
    renderTrafficPage();
    expect(await screen.findByText('流量体积')).toBeInTheDocument();
    const rateButton = screen.getByText('网络速率');
    fireEvent.click(rateButton);
    expect(screen.getByRole('img', { name: /网络速率趋势/ })).toBeInTheDocument();
  });

  it('supports device breakdown dimension and drill-down from user', async () => {
    renderTrafficPage();
    expect(await screen.findByText('用户用量排行')).toBeInTheDocument();
    expect(await screen.findByText('Alice')).toBeInTheDocument();

    // Click "设备下钻" on Alice's row
    const drillDownBtn = screen.getByText('设备下钻');
    fireEvent.click(drillDownBtn);

    // Should switch to device ranking
    expect(await screen.findByText('设备用量排行')).toBeInTheDocument();
  });
});
