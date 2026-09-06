import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { MonitorPage } from '../features/monitor/pages/MonitorPage';

vi.mock('../services/monitor', () => ({
  monitorApi: {
    status: () => Promise.resolve({
      ready: true,
      sampledAt: '2026-08-17T12:00:05Z',
      host: {
        hostname: 'vps-1', os: 'linux', platform: 'Ubuntu', platformVersion: '24.04',
        kernelVersion: '6.8.0', kernelArch: 'x86_64', virtualizationSystem: 'kvm',
        bootTime: 1700000000, uptimeSeconds: 86400
      },
      cpu: {
        logicalCores: 2, physicalCores: 1, modelName: 'EPYC 9654',
        usagePercent: 42.5, perCore: [40, 45], load1: 0.5, load5: 0.4, load15: 0.3
      },
      memory: { total: 4000000000, used: 1600000000, available: 2400000000, usedPercent: 40, swapTotal: 0, swapUsed: 0, swapPercent: 0 },
      disk: { path: '/', total: 40000000000, used: 16000000000, free: 24000000000, usedPercent: 40 },
      network: {
        sendRate: 1024, recvRate: 2048,
        interfaces: [{ name: 'eth0', bytesSent: 3000, bytesRecv: 2500, packetsSent: 30, packetsRecv: 25, sendRate: 1024, recvRate: 2048 }]
      },
      history: [
        { time: '2026-08-17T12:00:00Z', cpuPercent: 30, memoryPercent: 40, netSendRate: 512, netRecvRate: 1024 },
        { time: '2026-08-17T12:00:05Z', cpuPercent: 42.5, memoryPercent: 40, netSendRate: 1024, netRecvRate: 2048 }
      ]
    })
  }
}));

function renderMonitor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <ConfigProvider>
      <AntApp>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/monitor']}>
            <Routes>
              <Route path="/monitor" element={<MonitorPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}

describe('monitor page', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('renders usage gauges and host details from the snapshot', async () => {
    renderMonitor();
    expect(await screen.findByRole('img', { name: 'CPU使用率 42.5%' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '内存使用率 40.0%' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '磁盘（/）使用率 40.0%' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '网络速率，下行 2.00 KB/s，上行 1.00 KB/s' })).toBeInTheDocument();
    expect(screen.getByText('vps-1')).toBeInTheDocument();
    expect(screen.getByText('EPYC 9654')).toBeInTheDocument();
    expect(screen.getByText('Ubuntu 24.04（linux）')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'CPU 与内存使用率趋势，悬停查看数值' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '网络速率趋势，上行与下行，悬停查看数值' })).toBeInTheDocument();
    expect(screen.getByText('eth0')).toBeInTheDocument();
  });

  it('renders one bar per core with height encoding usage', async () => {
    renderMonitor();
    const chart = await screen.findByRole('img', { name: 'CPU 每核占用柱状图，2 核，最忙为核 1（45.0%）' });
    const bars = chart.querySelectorAll('.core-bar');
    expect(bars).toHaveLength(2);
    const [first, second] = bars;
    expect(Number(second!.getAttribute('height'))).toBeGreaterThan(Number(first!.getAttribute('height')));
  });
});
