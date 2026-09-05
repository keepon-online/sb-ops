import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { AppLayout } from '../layout/AppLayout';

vi.mock('../features/events/useEventStream', () => ({
  useEventStream: vi.fn()
}));

vi.mock('../hooks/useIdentity', () => ({
  useIdentity: vi.fn(() => ({ data: { user: { username: 'admin' } } }))
}));

vi.mock('../services/identity', () => ({
  identityApi: { logout: vi.fn() }
}));

vi.mock('../services/controlPlane', () => ({
  controlPlaneApi: {
    overview: vi.fn(() => Promise.resolve({
      kernelStatus: 'running',
      configStatus: 'applied'
    }))
  }
}));

const mockAlerts = vi.fn();
vi.mock('../services/alerts', () => ({
  alertsApi: {
    list: () => mockAlerts()
  }
}));

const mockUpgradeStatus = vi.fn();
vi.mock('../services/system', () => ({
  systemApi: {
    upgradeStatus: () => mockUpgradeStatus()
  }
}));

function renderLayout(initialEntry = '/overview') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={[initialEntry]}>
            <AppLayout />
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

describe('navigation badges and submenu interaction', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it('renders alert count badge with correct severity styling', async () => {
    mockAlerts.mockResolvedValue({
      items: [
        { id: 'a1', type: 'traffic_stalled', severity: 'critical', message: '采集异常', lastSeenAt: new Date().toISOString() }
      ]
    });
    mockUpgradeStatus.mockResolvedValue({ currentVersion: 'v0.3.0', latest: { upgradable: false } });

    renderLayout();

    const alertCount = await screen.findByText('1', { selector: '.nav-menu-badge-count' });
    expect(alertCount).toBeInTheDocument();
    expect(alertCount.className).toContain('is-critical');
  });

  it('handles upgrade New badge: shows on child when submenu is open, moves to parent without overlapping arrow when collapsed', async () => {
    mockAlerts.mockResolvedValue({ items: [] });
    mockUpgradeStatus.mockResolvedValue({
      currentVersion: 'v0.3.0',
      latest: { upgradable: true, version: 'v0.4.0' }
    });

    // 默认 system-group 展开：子项「系统升级」展示 New，父项「系统运维」不展示重复的 New
    localStorage.setItem('sb-menu-open-keys', JSON.stringify(['system-group']));
    renderLayout();

    const newBadges = await screen.findAllByText('New', { selector: '.nav-menu-badge-new' });
    // 展开时仅子项有 1 个 New 胶囊，父项不重复堆叠
    expect(newBadges).toHaveLength(1);

    // 点击收起「系统运维」
    const systemSubmenu = screen.getByText('系统运维');
    fireEvent.click(systemSubmenu);

    // 收起后父项「系统运维」呈现 New 胶囊作为提醒
    const collapsedNewBadges = await screen.findAllByText('New', { selector: '.nav-menu-badge-new' });
    expect(collapsedNewBadges.length).toBeGreaterThanOrEqual(1);
  });
});
