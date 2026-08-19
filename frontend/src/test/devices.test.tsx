import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DevicesDrawer } from '../features/users/components/DevicesDrawer';
import type { ResourceUser } from '../services/users';

vi.mock('../services/users', () => ({
  usersApi: {
    rotateSubscription: vi.fn(() => Promise.resolve({ subscriptionToken: 'rotated-token' })),
    listDevices: vi.fn(() => Promise.resolve({
      subscriptionToken: 'user-link-token',
      items: [
        { id: 'd1', name: '默认设备', enabled: true, usedBytes: 2048, token: 'token-one', uuid: 'u1', auto: false, platform: 'windows', client: 'Clash Verge', clientVersion: '2.1.3', lastSeenAt: '2026-08-18T00:00:00Z', rotatedAt: '2026-08-18T00:00:00Z', createdAt: '2026-08-01T00:00:00Z' },
        { id: 'd2', name: '手机', enabled: false, usedBytes: 1024, token: 'token-two', uuid: 'u2', auto: true, platform: 'android', client: 'Clash Meta Android', clientVersion: 'v2.11.5', lastSeenAt: null, rotatedAt: '2026-08-18T00:00:00Z', createdAt: '2026-08-10T00:00:00Z' }
      ]
    })),
    createDevice: vi.fn(() => Promise.resolve({ device: {}, credential: { deviceId: 'd3', token: 't3', uuid: 'u3', password: 'p3' } })),
    updateDevice: vi.fn(() => Promise.resolve()),
    deleteDevice: vi.fn(() => Promise.resolve()),
    rotateDevice: vi.fn(() => Promise.resolve({ device: {}, credential: { deviceId: 'd1', token: 't9', uuid: 'u9', password: 'p9' } }))
  },
  subscriptionUrl: (token: string, format: string) => `http://localhost/subscribe/${token}/${format}`
}));

const user: ResourceUser = {
  id: 'user-1', name: 'alice', enabled: true, status: 'active', quotaBytes: 0, usedBytes: 3072,
  resetIntervalDays: 0, protocolIds: [], policyIds: [], deviceCount: 2, revision: 1,
  createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z'
};

function renderDrawer() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <DevicesDrawer user={user} onClose={() => {}} />
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

describe('devices drawer', () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it('lists devices with per-device usage, state and links', async () => {
    renderDrawer();
    expect(await screen.findByText('alice 的设备')).toBeInTheDocument();
    expect((await screen.findAllByText('默认设备')).length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('用户订阅链接')).toBeInTheDocument();
    expect(screen.getByText('http://localhost/subscribe/user-link-token/clash.yaml')).toBeInTheDocument();
    expect(screen.getByText('设备流量统计')).toBeInTheDocument();
    expect(screen.getByText('用户累计 3.0 KB')).toBeInTheDocument();
    expect(screen.getByText('67%')).toBeInTheDocument();
    expect(screen.getByText('33%')).toBeInTheDocument();
    expect(screen.getAllByText('手机').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('已停用')).toBeInTheDocument();
    expect(screen.getByText('Windows')).toBeInTheDocument();
    expect(screen.getByText('Android')).toBeInTheDocument();
    expect(screen.getByText('Clash Verge v2.1.3')).toBeInTheDocument();
    expect(screen.getByText('Clash Meta Android v2.11.5')).toBeInTheDocument();
    expect(screen.getByText(/最近活跃/)).toBeInTheDocument();
    expect(screen.getByText('尚未识别')).toBeInTheDocument();
    expect(screen.getByText('已用 2.0 KB')).toBeInTheDocument();
    expect(screen.getByText('已用 1.0 KB')).toBeInTheDocument();
    expect(screen.getByText('http://localhost/subscribe/token-one/clash.yaml')).toBeInTheDocument();
    expect(screen.getByText('http://localhost/subscribe/token-two/clash.yaml')).toBeInTheDocument();
  });

  it('creates a device through the add modal', async () => {
    const { usersApi } = await import('../services/users');
    renderDrawer();
    fireEvent.click(await screen.findByRole('button', { name: /添加设备/ }));
    fireEvent.change(await screen.findByPlaceholderText('设备名称，如：手机、iPad'), { target: { value: 'iPad' } });
    fireEvent.click(screen.getByRole('button', { name: '添 加' }));
    await waitFor(() => expect(usersApi.createDevice).toHaveBeenCalledWith('user-1', 'iPad'));
  });
});
