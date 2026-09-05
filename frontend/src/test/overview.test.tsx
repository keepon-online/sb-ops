import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { OverviewPage } from '../features/overview/pages/OverviewPage';

vi.mock('../services/controlPlane', () => ({
  controlPlaneApi: {
    overview: vi.fn(() => Promise.resolve({
      counts: { users: 0, protocols: 0, policies: 0 },
      devices: { total: 3, auto: 2, activeDay: 1, platforms: { windows: 1, android: 1, unknown: 1 } },
      runtimeStatus: 'not_configured',
      configStatus: 'not_configured',
      kernelStatus: 'not_checked',
      runtimeReason: '尚未配置公网地址',
      tasks: [],
      artifactCount: 0,
      connections: { available: false, count: 0 },
      traffic24h: { totalBytes: 0, upBytes: 0, downBytes: 0, samples: 0 },
      topUsers: []
    }))
  }
}));

vi.mock('../services/protocols', () => ({
  protocolsApi: {
    probeResults: vi.fn(() => Promise.resolve({
      items: [
        { protocolId: 'p1', protocolName: 'VLESS Reality', status: 'ok', latencyMs: 32, checkedAt: '2026-09-05T10:00:00Z' },
        { protocolId: 'p2', protocolName: 'VMess', status: 'failed', error: '连接超时', checkedAt: '2026-09-05T10:00:00Z' }
      ]
    })),
    runProbe: vi.fn(() => Promise.resolve({ checkedAt: '2026-09-05T10:05:00Z', items: [] }))
  },
  certificatesApi: {
    list: vi.fn(() => Promise.resolve({
      items: [
        { id: 'c1', name: '站点证书', source: 'acme', domain: 'example.com', fingerprint: 'aa', notBefore: '2026-06-01T00:00:00Z', notAfter: new Date(Date.now() + 10 * 86400000).toISOString() },
        { id: 'c2', name: '长期证书', source: 'acme', domain: 'far.example', fingerprint: 'bb', notBefore: '2026-01-01T00:00:00Z', notAfter: new Date(Date.now() + 200 * 86400000).toISOString() }
      ]
    }))
  }
}));

function renderOverview() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/overview']}>
            <Routes>
              <Route path="/overview" element={<OverviewPage />} />
              <Route path="/singbox" element={<div>singbox destination</div>} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

describe('overview navigation', () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it('renders probe health rows and certificate warning card', async () => {
    renderOverview();
    // 拨测卡片：一行通、一行失败带原因。
    expect(await screen.findByText('VLESS Reality')).toBeInTheDocument();
    expect(screen.getByText('32 ms')).toBeInTheDocument();
    expect(screen.getByText('VMess')).toBeInTheDocument();
    expect(screen.getByText('连接超时')).toBeInTheDocument();
    // 证书预警卡片：只列 30 天内到期的证书，远期证书不出现。
    expect(await screen.findByText('证书预警')).toBeInTheDocument();
    expect(screen.getByText('站点证书')).toBeInTheDocument();
    expect(screen.queryByText('长期证书')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '证书管理' })).toBeInTheDocument();
  });

  it('routes the runtime reason shortcut in-app without opening a browser window', async () => {
    const open = vi.spyOn(window, 'open');
    renderOverview();
    fireEvent.click(await screen.findByRole('button', { name: '前往服务配置' }));
    expect(await screen.findByText('singbox destination')).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it('renders device stats and platform breakdown', async () => {
    renderOverview();
    expect(await screen.findByText(/设备平台分布/)).toHaveTextContent('Windows 1 · Android 1 · 未知 1');
    expect(screen.getByText('接入设备')).toBeInTheDocument();
    expect(screen.getByText('24h 活跃设备')).toBeInTheDocument();
  });

  it('tolerates a backend response without device stats (frontend newer than backend)', async () => {
    const { controlPlaneApi } = await import('../services/controlPlane');
    const legacy = {
      counts: { users: 0, protocols: 0, policies: 0 },
      runtimeStatus: 'not_configured',
      configStatus: 'not_configured',
      kernelStatus: 'not_checked',
      tasks: [],
      artifactCount: 0,
      connections: { available: false, count: 0 },
      traffic24h: { totalBytes: 0, upBytes: 0, downBytes: 0, samples: 0 },
      topUsers: []
    };
    vi.mocked(controlPlaneApi.overview).mockResolvedValueOnce(legacy as never);
    renderOverview();
    // 页面不抛错，设备指标回落为 0。
    expect(await screen.findByText('接入设备')).toBeInTheDocument();
    expect(screen.getByText('24h 活跃设备')).toBeInTheDocument();
    expect(screen.queryByText(/设备平台分布/)).not.toBeInTheDocument();
  });
});
