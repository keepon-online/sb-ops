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
const backupRestoreExistingMock = vi.fn();
const waitForPanelRestartMock = vi.fn();
const warpStatusMock = vi.fn();
const warpInstallMock = vi.fn();
const warpReconfigureMock = vi.fn();

vi.mock('../services/system', () => ({
  systemApi: {
    upgradeStatus: () => statusMock(),
    upgradeCheck: () => Promise.resolve({ version: 'v0.3.0', notes: '修复若干问题', publishedAt: 1755701779, upgradable: true }),
    upgradeStart: () => Promise.resolve({
      configured: true, repo: 'owner/repo', currentVersion: 'v0.2.0', phase: 'downloading', updatedAt: 1755701780
    }),
    backupList: () => backupListMock(),
    backupCreate: () => backupCreateMock(),
    backupDelete: (name: string) => Promise.resolve(name),
    warpClientStatus: () => warpStatusMock(),
    warpInstall: () => warpInstallMock(),
    warpReconfigure: () => warpReconfigureMock()
  },
  backupDownload: (name: string) => backupDownloadMock(name),
  backupRestoreUpload: () => Promise.resolve(),
  backupRestoreExisting: (name: string) => backupRestoreExistingMock(name),
  // 恢复受理后页面等面板重启再整体刷新；测试里挂起该 Promise 即可停在
  // 「正在重启」态，避免触碰 jsdom 不支持的 location.reload。
  waitForPanelRestart: () => waitForPanelRestartMock()
}));

vi.mock('../services/singbox', () => ({
  singBoxApi: {
    createOutbound: vi.fn(() => Promise.resolve({ id: 'ob-1' })),
    applyAITemplate: vi.fn(() => Promise.resolve({ id: 'rule-1' }))
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

describe('system warp client section', () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  function warpStatus(overrides: Record<string, unknown> = {}) {
    return {
      installed: true, version: '2026.7.1377.0', serviceActive: true, serviceEnabled: true,
      registered: true, connected: true, modeProxy: true, proxyPort: 40000,
      outboundConfigured: true, linkVerdict: 'ok', linkCheckedAt: '2026-09-25T10:00:00Z',
      phase: 'idle', ...overrides
    };
  }

  it('renders the healthy warp client status with link verdict', async () => {
    warpStatusMock.mockResolvedValue(warpStatus());
    renderSystem();
    expect(await screen.findByText(/2026\.7\.1377\.0/)).toBeInTheDocument();
    expect(screen.getByText('正常')).toBeInTheDocument();
    expect(screen.getByText('warp-masque 已配置')).toBeInTheDocument();
    // 健康状态下不出现安装与接线按钮。
    expect(screen.queryByRole('button', { name: /一键安装 WARP 客户端/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /创建出站并应用 AI 解锁/ })).not.toBeInTheDocument();
  });

  it('offers install when the client is absent and triggers the mutation', async () => {
    warpStatusMock.mockResolvedValue(warpStatus({ installed: false, version: undefined, serviceActive: false, serviceEnabled: false, registered: false, connected: false, modeProxy: false, proxyPort: 0, outboundConfigured: false, linkVerdict: undefined, linkCheckedAt: undefined }));
    renderSystem();
    fireEvent.click(await screen.findByRole('button', { name: /一键安装 WARP 客户端/ }));
    fireEvent.click(await screen.findByRole('button', { name: /确 定|确定|OK/i }));
    await waitFor(() => expect(warpInstallMock).toHaveBeenCalled());
  });

  it('offers provisioning when installed but the outbound is missing', async () => {
    warpStatusMock.mockResolvedValue(warpStatus({ outboundConfigured: false, phase: 'done' }));
    renderSystem();
    expect(await screen.findByText('客户端已就绪')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /创建出站并应用 AI 解锁/ })).toBeEnabled();
  });

  it('shows the failure detail when the install phase failed', async () => {
    warpStatusMock.mockResolvedValue(warpStatus({ installed: false, phase: 'failed', phaseError: '安装 cloudflare-warp 失败: E: 无法锁定 dpkg' }));
    renderSystem();
    expect(await screen.findByText('WARP 客户端安装失败')).toBeInTheDocument();
    expect(screen.getByText(/无法锁定 dpkg/)).toBeInTheDocument();
  });
});

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

  it('gates row restore behind the confirmation word and enters restart waiting', async () => {
    statusMock.mockResolvedValue(baseStatus());
    backupListMock.mockResolvedValue({ items: [{ name: 'sb-ops-backup-20260905-103000.db', sizeBytes: 20480, createdAt: 1757068200 }] });
    backupRestoreExistingMock.mockResolvedValue(undefined);
    waitForPanelRestartMock.mockReturnValue(new Promise(() => {}));
    renderSystem();
    expect(await screen.findByText('sb-ops-backup-20260905-103000.db')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /恢复备份/ }));
    expect(await screen.findByText('恢复数据库备份')).toBeInTheDocument();
    // 确认词未输入时提交禁用；错误确认词不解锁。
    const submit = screen.getByRole('button', { name: /确认恢复/ });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText('恢复确认词'), { target: { value: '删除' } });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText('恢复确认词'), { target: { value: '恢复' } });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);
    await waitFor(() => expect(backupRestoreExistingMock).toHaveBeenCalledWith('sb-ops-backup-20260905-103000.db'));
    await waitFor(() => expect(waitForPanelRestartMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('面板正在重启以应用恢复的备份')).toBeInTheDocument();
  });
});
