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

  it('routes the runtime reason shortcut in-app without opening a browser window', async () => {
    const open = vi.spyOn(window, 'open');
    renderOverview();
    fireEvent.click(await screen.findByRole('button', { name: '前往服务端配置' }));
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
