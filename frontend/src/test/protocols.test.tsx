import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ProtocolsPage } from '../features/protocols/pages/ProtocolsPage';
import type { ResourceCertificate, ResourceProtocol, ResourceProtocolSchema } from '../services/protocols';

vi.mock('../services/protocols', () => ({
  certificatesApi: {
    list: () => Promise.resolve({ items: [] as ResourceCertificate[] })
  },
  protocolsApi: {
    list: () => Promise.resolve({
      items: [
        { id: 'proto-a', name: 'alpha', type: 'vless', enabled: true, listen: '::', listenPort: 8443, settings: {}, certificateId: null, revision: 1, createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z' },
        { id: 'proto-b', name: 'beta', type: 'trojan', enabled: true, listen: '::', listenPort: 443, settings: {}, certificateId: null, revision: 1, createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-08-01T00:00:00Z' }
      ] as ResourceProtocol[]
    }),
    runtimeStatus: () => Promise.resolve({
      kernelStatus: 'running',
      configStatus: 'applied',
      listeningAvailable: true,
      connectionsAvailable: true,
      items: [
        { id: 'proto-a', name: 'alpha', type: 'vless', transport: 'tcp', enabled: true, listenPort: 8443, status: 'listening', activeConnections: 2 },
        { id: 'proto-b', name: 'beta', type: 'trojan', transport: 'tcp', enabled: true, listenPort: 443, status: 'not_listening', activeConnections: 0 }
      ]
    }),
    probeResults: () => Promise.resolve({
      items: [
        { protocolId: 'proto-a', protocolName: 'alpha', status: 'ok', latencyMs: 42, error: '', checkedAt: '2026-09-03T02:00:00Z' },
        { protocolId: 'proto-b', protocolName: 'beta', status: 'failed', latencyMs: null, error: '握手失败', checkedAt: '2026-09-03T02:00:00Z' }
      ]
    }),
    runProbe: vi.fn(() => Promise.resolve({ checkedAt: '2026-09-03T02:05:00Z', items: [] })),
    schema: () => Promise.resolve({ type: 'vless', fields: [], defaults: {}, certificateRequired: false } as unknown as ResourceProtocolSchema),
    suggestPort: () => Promise.resolve({ listenPort: 8444 })
  }
}));

function renderProtocols() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/protocols']}>
            <Routes>
              <Route path="/protocols" element={<ProtocolsPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

function findRow(name: string) {
  return screen.getByText(name).closest('tr')!;
}

describe('protocols page', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders per-protocol runtime status and active connections', async () => {
    renderProtocols();
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    const alpha = within(findRow('alpha'));
    expect(alpha.getByText('监听中')).toBeInTheDocument();
    expect(alpha.getByText('2 连接')).toBeInTheDocument();
    const beta = within(findRow('beta'));
    expect(beta.getByText('未监听')).toBeInTheDocument();
    expect(beta.getByText('0 连接')).toBeInTheDocument();
  });

  it('renders per-protocol end-to-end probe status with latency', async () => {
    renderProtocols();
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    const alpha = within(findRow('alpha'));
    expect(alpha.getByText('链路正常')).toBeInTheDocument();
    expect(alpha.getByText('42ms')).toBeInTheDocument();
    const beta = within(findRow('beta'));
    expect(beta.getByText('链路失败')).toBeInTheDocument();
  });

  it('triggers a manual probe from the toolbar button', async () => {
    // vitest 未开 globals 自动 cleanup，跨用例 DOM 残留：用 container 作用域查询。
    const { container } = renderProtocols();
    const label = await within(container).findByText('立即拨测');
    label.closest('button')!.click();
    const { protocolsApi } = await import('../services/protocols');
    await vi.waitFor(() => expect(protocolsApi.runProbe).toHaveBeenCalled());
  });
});
