import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { UsersPage } from '../features/users/pages/UsersPage';
import { usersApi } from '../services/users';

vi.mock('../services/users', () => ({
  usersApi: {
    list: vi.fn(() => Promise.resolve({
      items: [
        { id: 'u1', name: 'alice', enabled: true, status: 'active', quotaBytes: 0, usedBytes: 0, resetIntervalDays: 0, protocolIds: [], policyIds: [], deviceCount: 0, revision: 1, createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z' },
        { id: 'u2', name: 'bob', enabled: false, status: 'disabled', quotaBytes: 0, usedBytes: 0, resetIntervalDays: 0, protocolIds: [], policyIds: [], deviceCount: 0, revision: 1, createdAt: '2026-08-02T00:00:00Z', updatedAt: '2026-08-02T00:00:00Z' }
      ],
      nextCursor: undefined
    })),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
    batchStatus: vi.fn(() => Promise.resolve()),
    batchQuota: vi.fn(() => Promise.resolve()),
    batchProtocols: vi.fn(() => Promise.resolve()),
    resetTraffic: vi.fn(),
    adjustQuota: vi.fn(),
    rotateSubscription: vi.fn(),
    listDevices: vi.fn(),
    updateDevice: vi.fn(),
    deleteDevice: vi.fn(),
    rotateDevice: vi.fn()
  },
  subscriptionUrl: (token: string, format: string, baseUrl?: string) => `${baseUrl || 'http://localhost'}/subscribe/${token}/${format}`
}));

vi.mock('../services/protocols', () => ({
  protocolsApi: {
    list: vi.fn(() => Promise.resolve({
      items: [
        { id: 'p1', name: '主线路', type: 'vless', enabled: true, listenPort: 443, revision: 1, createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z' }
      ]
    }))
  },
  certificatesApi: { list: vi.fn(() => Promise.resolve({ items: [] })) }
}));

vi.mock('../services/policies', () => ({
  policiesApi: { list: vi.fn(() => Promise.resolve({ items: [] })) }
}));

function renderUsersPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter>
            <UsersPage />
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

/** 勾选表格行复选框（首枚是表头全选框，其后每行一枚）。 */
async function selectAllRows(count: number) {
  const boxes = screen.getAllByRole('checkbox');
  for (const box of boxes.slice(1, 1 + count)) {
    fireEvent.click(box);
  }
  await waitFor(() => expect(screen.getByText(`已选择 ${count} 项`)).toBeInTheDocument());
}

describe('users batch operations', () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it('批量调额度提交 GB 换算后的字节数', async () => {
    renderUsersPage();
    expect(await screen.findByText('alice')).toBeInTheDocument();
    await selectAllRows(2);
    fireEvent.click(screen.getByRole('button', { name: '调额度' }));
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'OK' }));
    await waitFor(() => expect(usersApi.batchQuota).toHaveBeenCalledWith(['u1', 'u2'], { mode: 'add', bytes: 2 * 1024 * 1024 * 1024 }));
  }, 15000);

  it('批量分配协议：add 模式空选择被校验拦下，set 模式提示清空后果', async () => {
    renderUsersPage();
    expect(await screen.findByText('alice')).toBeInTheDocument();
    await selectAllRows(2);
    fireEvent.click(screen.getByRole('button', { name: '分配协议' }));
    fireEvent.click(screen.getByRole('button', { name: 'OK' }));
    // add（默认）模式下空协议列表不允许提交。
    expect(await screen.findByText('请至少选择一个协议')).toBeInTheDocument();
    expect(usersApi.batchProtocols).not.toHaveBeenCalled();
    // 切到覆盖模式后提示清空后果且允许留空提交（清空语义）。
    fireEvent.mouseDown(screen.getAllByText('追加协议')[0].closest('.ant-select')!.querySelector('.ant-select-selector')!);
    expect(await screen.findByText('覆盖全部协议')).toBeInTheDocument();
    fireEvent.click(screen.getByText('覆盖全部协议'));
    expect(await screen.findByText(/留空即清空所选用户的全部协议/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'OK' }));
    await waitFor(() => expect(usersApi.batchProtocols).toHaveBeenCalledWith(['u1', 'u2'], { mode: 'set', protocolIds: [] }));
  });

  it('批量启停仍走既有 batch-status 接口', async () => {
    renderUsersPage();
    expect(await screen.findByText('alice')).toBeInTheDocument();
    await selectAllRows(1);
    fireEvent.click(screen.getByRole('button', { name: '启 用' }));
    await waitFor(() => expect(usersApi.batchStatus).toHaveBeenCalledWith(['u1'], true));
  });
});
