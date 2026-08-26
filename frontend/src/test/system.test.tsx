import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SystemPage } from '../features/system/pages/SystemPage';

const statusMock = vi.fn();

vi.mock('../services/system', () => ({
  systemApi: {
    upgradeStatus: () => statusMock(),
    upgradeCheck: () => Promise.resolve({ version: 'v0.3.0', notes: '修复若干问题', publishedAt: 1755701779, upgradable: true }),
    upgradeStart: () => Promise.resolve({
      configured: true, repo: 'owner/repo', currentVersion: 'v0.2.0', phase: 'downloading', updatedAt: 1755701780
    })
  }
}));

function renderSystem() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/system']}>
            <Routes>
              <Route path="/system" element={<SystemPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

function baseStatus(overrides: Record<string, unknown> = {}) {
  return {
    configured: true,
    repo: 'owner/repo',
    currentVersion: 'v0.2.0',
    phase: 'idle',
    updatedAt: 1755701779,
    ...overrides
  };
}

describe('system upgrade page', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('renders current version and unconfigured guidance', async () => {
    statusMock.mockResolvedValue(baseStatus({ configured: false, repo: '' }));
    renderSystem();
    expect(await screen.findByText('v0.2.0')).toBeInTheDocument();
    expect(screen.getByText('升级源未配置')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /检查更新/ })).toBeDisabled();
  });

  it('shows latest release after check and upgrade button becomes available', async () => {
    statusMock.mockResolvedValue(baseStatus());
    renderSystem();
    expect(await screen.findByText('v0.2.0')).toBeInTheDocument();
    expect(screen.getByText('尚未检查')).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: /检查更新/ }));
    expect(await screen.findByText('v0.3.0')).toBeInTheDocument();
    expect(screen.getByText('可升级')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /升级到 v0.3.0/ })).toBeEnabled();
  });

  it('renders progress steps while an upgrade is running', async () => {
    statusMock.mockResolvedValue(baseStatus({ phase: 'verifying', latest: { version: 'v0.3.0', notes: '', publishedAt: 0, upgradable: true } }));
    renderSystem();
    expect(await screen.findByText('sha256 校验')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /升级到 v0.3.0/ })).toBeDisabled();
  });

  it('renders failure detail after a failed upgrade', async () => {
    statusMock.mockResolvedValue(baseStatus({ phase: 'failed', error: 'sha256 校验失败（期望 aaaa，实际 bbbb）', latest: { version: 'v0.3.0', notes: '', publishedAt: 0, upgradable: true } }));
    renderSystem();
    expect(await screen.findByText('上次升级失败')).toBeInTheDocument();
    expect(screen.getByText(/sha256 校验失败/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /升级到 v0.3.0/ })).toBeEnabled();
  });
});
