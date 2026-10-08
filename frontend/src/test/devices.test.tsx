import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DevicesDrawer, maskDeviceName, maskHost, maskIp, maskSubscriptionUrl } from '../features/users/components/DevicesDrawer';
import { usersApi, type ResourceUser } from '../services/users';

vi.mock('../services/users', () => ({
  usersApi: {
    rotateSubscription: vi.fn(() => Promise.resolve({ subscriptionToken: 'rotated-token' })),
    listDevices: vi.fn(() => Promise.resolve({
      subscriptionToken: 'user-link-token',
      maxDevices: 10,
      userLifetimeBytes: 4096,
      items: [
        { id: 'd1', name: '默认设备', enabled: true, usedBytes: 2048, token: 'token-one', uuid: 'u1', auto: false, platform: 'windows', client: 'Clash Verge', clientVersion: '2.1.3', lastSeenAt: '2026-08-18T00:00:00Z', rotatedAt: '2026-08-18T00:00:00Z', createdAt: '2026-08-01T00:00:00Z', lifetimeBytes: 3072 },
        { id: 'd2', name: '手机', enabled: false, usedBytes: 1024, token: 'token-two', uuid: 'u2', auto: true, platform: 'android', client: 'Clash Meta Android', clientVersion: 'v2.11.5', lastSeenAt: null, rotatedAt: '2026-08-18T00:00:00Z', createdAt: '2026-08-10T00:00:00Z', lifetimeBytes: 1024 }
      ]
    })),
    createDevice: vi.fn(() => Promise.resolve({
      device: { id: 'd3', name: '新设备', enabled: true, usedBytes: 0, token: 'token-three', uuid: 'u3', auto: false, platform: 'unknown', client: '', clientVersion: '', rotatedAt: '2026-08-18T00:00:00Z', createdAt: '2026-08-18T00:00:00Z' },
      credential: { deviceId: 'd3', token: 'token-three', uuid: 'u3', password: 'p3' }
    })),
    updateDevice: vi.fn(() => Promise.resolve()),
    deleteDevice: vi.fn(() => Promise.resolve()),
    rotateDevice: vi.fn(() => Promise.resolve({ device: {}, credential: { deviceId: 'd1', token: 't9', uuid: 'u9', password: 'p9' } }))
  },
  subscriptionUrl: (token: string, format: string, baseUrl?: string) => `${baseUrl || 'http://localhost'}/subscribe/${token}/${format}`
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

  it('lists devices with usage and state, while keeping one user link and dedicated device links', async () => {
    renderDrawer();
    expect(await screen.findByText('alice 的设备')).toBeInTheDocument();
    expect((await screen.findAllByText('默认设备')).length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('用户订阅链接')).toBeInTheDocument();
    expect(screen.getAllByText('http://***/subscribe/****************/clash.yaml').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('设备流量统计')).toBeInTheDocument();
    expect(screen.getByText(/本周期已用 3\.0 KB · 历史累计 4\.0 KB/)).toBeInTheDocument();
    expect(screen.getByText('周期占比 67%')).toBeInTheDocument();
    expect(screen.getByText('周期占比 33%')).toBeInTheDocument();
    expect(screen.getByText('历史 3.0 KB')).toBeInTheDocument();
    expect(screen.getByText('历史 1.0 KB')).toBeInTheDocument();
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
    expect(screen.getByText('导入后按客户端与来源网络自动识别，最多 10 台')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /添加设备/ })).toBeInTheDocument();
    expect(screen.getAllByText('专属订阅直链').length).toBeGreaterThanOrEqual(1);
  });

  it('masks IP addresses and subscription URLs correctly with helper functions', () => {
    expect(maskIp('203.0.113.99')).toBe('203.0.***.***');
    expect(maskIp('192.168.1.1')).toBe('192.168.***.***');
    expect(maskDeviceName('Clash Verge · 203.0.113.99', true)).toBe('Clash Verge · 203.0.***.***');
    expect(maskDeviceName('Clash Verge · 203.0.113.99', false)).toBe('Clash Verge · 203.0.113.99');
    expect(maskDeviceName('默认设备', true)).toBe('默认设备');
    expect(maskHost('panel.example.com')).toBe('panel.***.***');
    expect(maskHost('sub.example.com')).toBe('sub.***.***');
    expect(maskHost('example.com')).toBe('***.com');
    expect(maskHost('192.168.1.1')).toBe('192.168.***.***');
    expect(maskSubscriptionUrl('https://panel.example.com/subscribe/0123456789abcdef0123456789abcdef/clash.yaml', true))
      .toBe('https://panel.***.***/subscribe/****************/clash.yaml');
    expect(maskSubscriptionUrl('https://panel.example.com/subscribe/0123456789abcdef0123456789abcdef/clash.yaml', false))
      .toBe('https://panel.example.com/subscribe/0123456789abcdef0123456789abcdef/clash.yaml');
  });

  it('masks IP addresses and subscription link by default and toggles between masked and raw', async () => {
    renderDrawer();
    expect(await screen.findByText('alice 的设备')).toBeInTheDocument();
    // Default is masked, toggle button shows "显示 IP"
    const toggleBtn = screen.getByRole('button', { name: '显示完整 IP' });
    expect(toggleBtn).toBeInTheDocument();
    expect(toggleBtn).toHaveTextContent('显示 IP');
    expect(screen.getAllByText('http://***/subscribe/****************/clash.yaml').length).toBeGreaterThanOrEqual(1);

    // Click to reveal full IP and subscription link
    fireEvent.click(toggleBtn);
    expect(screen.getByRole('button', { name: '隐藏 IP' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '隐藏 IP' })).toHaveTextContent('隐藏 IP');
    expect(screen.getByText('http://localhost/subscribe/user-link-token/clash.yaml')).toBeInTheDocument();
    expect(screen.getByText('http://localhost/subscribe/token-one/clash.yaml')).toBeInTheDocument();
    expect(screen.getByText('http://localhost/subscribe/token-two/clash.yaml')).toBeInTheDocument();

    // Click again to hide
    fireEvent.click(screen.getByRole('button', { name: '隐藏 IP' }));
    expect(screen.getByRole('button', { name: '显示完整 IP' })).toBeInTheDocument();
    expect(screen.getAllByText('http://***/subscribe/****************/clash.yaml').length).toBeGreaterThanOrEqual(1);
  });

  it('uses subscriptionBaseUrl when provided by API', async () => {
    vi.mocked(usersApi.listDevices).mockResolvedValueOnce({
      subscriptionToken: 'token-custom-base',
      subscriptionBaseUrl: 'https://sub.example.com',
      maxDevices: 10,
      items: []
    });
    renderDrawer();
    expect(await screen.findByText('https://sub.***.***/subscribe/****************/clash.yaml')).toBeInTheDocument();
  });
});


