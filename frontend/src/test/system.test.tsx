import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SystemPage } from '../features/system/pages/SystemPage';

const statusMock = vi.fn();
const backupListMock = vi.fn();
const backupCreateMock = vi.fn();
const backupDownloadMock = vi.fn();

vi.mock('../services/system', () => ({
  systemApi: {
    upgradeStatus: () => statusMock(),
    upgradeCheck: () => Promise.resolve({ version: 'v0.3.0', notes: '修复若干问题', publishedAt: 1755701779, upgradable: true }),
    upgradeStart: () => Promise.resolve({
      configured: true, repo: 'owner/repo', currentVersion: 'v0.2.0', phase: 'downloading', updatedAt: 1755701780
    }),
    backupList: () => backupListMock(),
    backupCreate: () => backupCreateMock(),
    backupDelete: (name: string) => Promise.resolve(name)
  },
  backupDownload: (name: string) => backupDownloadMock(name)
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
    backupListMock.mockResolvedValue({ items: [] });
  });

  it('renders current version and unconfigured guidance', async () => {
    statusMock.mockResolvedValue(baseStatus({ configured: false, repo: '' }));
    renderSystem();
    expect(await screen.findByText('v0.2.0')).toBeInTheDocument();
    expect(screen.getByText('升级源未配置')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /检查更新/ })).toBeDisabled();
  });

  it('auto-checks on mount and shows the latest release', async () => {
    statusMock.mockResolvedValue(baseStatus());
    renderSystem();
    expect(await screen.findByText('v0.2.0')).toBeInTheDocument();
    // 进页自动检查：缓存为空时不再依赖手动点击，最新版本与升级入口直接出现。
    expect(await screen.findByText('v0.3.0')).toBeInTheDocument();
    expect(screen.getByText('可升级')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /升级到 v0.3.0/ })).toBeEnabled();
  });

  it('keeps the cached latest release without re-checking on mount', async () => {
    statusMock.mockResolvedValue(baseStatus({ latest: { version: 'v0.2.1', notes: '', publishedAt: 1755701779, upgradable: false } }));
    renderSystem();
    expect(await screen.findByText('v0.2.1')).toBeInTheDocument();
    expect(screen.getByText('已最新')).toBeInTheDocument();
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

  it('renders the backup card with file metadata and row download', async () => {
    statusMock.mockResolvedValue(baseStatus());
    backupListMock.mockResolvedValue({ items: [{ name: 'sb-ops-backup-20260905-103000.db', sizeBytes: 20480, createdAt: 1757068200 }] });
    renderSystem();
    expect(await screen.findByText('数据备份')).toBeInTheDocument();
    expect(await screen.findByText('sb-ops-backup-20260905-103000.db')).toBeInTheDocument();
    expect(screen.getByText('20 KB')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /下载备份/ }));
    await waitFor(() => expect(backupDownloadMock).toHaveBeenCalledWith('sb-ops-backup-20260905-103000.db'));
  });

  it('creates a backup from the toolbar button', async () => {
    statusMock.mockResolvedValue(baseStatus());
    backupListMock.mockResolvedValue({ items: [] });
    backupCreateMock.mockResolvedValue({ name: 'sb-ops-backup-20260905-104000.db', sizeBytes: 20480, createdAt: 1757068800 });
    renderSystem();
    fireEvent.click(await screen.findByRole('button', { name: /立即备份/ }));
    await waitFor(() => expect(backupCreateMock).toHaveBeenCalledTimes(1));
  });
});
