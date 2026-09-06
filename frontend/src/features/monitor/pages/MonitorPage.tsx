import { ArrowDownOutlined, ArrowUpOutlined, ReloadOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Result, Space, Spin, Table, Typography } from 'antd';
import dayjs from 'dayjs';
import { useState } from 'react';
import { InteractiveChart } from '../../../components/InteractiveChart';
import { Page } from '../../../components/Page';
import { UsageGauge } from '../../../components/UsageGauge';
import { useElementWidth } from '../../../components/useElementWidth';
import { monitorApi, type MonitorStatus } from '../../../services/monitor';
import { ApiError } from '../../../services/http';
import type { components } from '../../../generated/management-api';
import { useAppTheme } from '../../../context/ThemeContext';

type MonitorInterface = components['schemas']['MonitorInterface'];

const bytes = (value: number) => {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
};
const rate = (value: number) => `${bytes(value)}/s`;

function duration(seconds: number) {
  if (seconds <= 0) return '刚刚启动';
  if (seconds < 60) return '< 1 分钟';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (days > 0) parts.push(`${days} 天`);
  if (hours > 0) parts.push(`${hours} 小时`);
  parts.push(`${minutes} 分钟`);
  return parts.join(' ');
}

// 占用色阶：75% 起预警、90% 起告急，与告警页的视觉语义一致。
function usageColor(value: number): string | undefined {
  if (value >= 90) return 'var(--color-error)';
  if (value >= 75) return 'var(--color-warning)';
  return undefined;
}

const interfaceColumns = [
  { title: '接口', dataIndex: 'name', key: 'name' },
  { title: '下行速率', dataIndex: 'recvRate', key: 'recvRate', render: rate, align: 'right' as const },
  { title: '上行速率', dataIndex: 'sendRate', key: 'sendRate', render: rate, align: 'right' as const },
  { title: '累计接收', dataIndex: 'bytesRecv', key: 'bytesRecv', render: bytes, align: 'right' as const },
  { title: '累计发送', dataIndex: 'bytesSent', key: 'bytesSent', render: bytes, align: 'right' as const }
];

export function MonitorPage() {
  const { trafficDownColor, chartAccentColor } = useAppTheme();
  const monitor = useQuery({ queryKey: ['monitor'], queryFn: monitorApi.status, retry: false, refetchInterval: 5000 });
  if (monitor.isError) return <Result status={monitor.error instanceof ApiError && monitor.error.status === 403 ? '403' : 'error'} title={monitor.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void monitor.refetch()}>重试</Button>} />;
  const value: MonitorStatus | undefined = monitor.data;
  if (monitor.isLoading) return <Page title="主机监控"><Spin size="large" style={{ display: 'block', margin: '80px auto' }} /></Page>;
  if (value && !value.ready) return <Page title="主机监控"><Alert type="info" showIcon message="正在等待首次采样" description="服务器指标采样随面板启动，几秒后自动刷新。" /></Page>;

  const history = value?.history || [];
  const labels = history.map((point) => dayjs(point.time).format('HH:mm:ss'));
  const sampledAt = value?.sampledAt ? new Date(value.sampledAt).toLocaleTimeString() : '';
  const peakRecv = Math.max(0, ...history.map((point) => point.netRecvRate));
  const peakSend = Math.max(0, ...history.map((point) => point.netSendRate));

  return <Page title="主机监控">
    <div className="page-toolbar">
      <Typography.Text type="secondary">主机 CPU / 内存 / 磁盘 / 网络实时指标，每 5 秒自动刷新{sampledAt ? `，采样于 ${sampledAt}` : ''}</Typography.Text>
      <Button icon={<ReloadOutlined />} loading={monitor.isFetching} onClick={() => void monitor.refetch()}>刷新</Button>
    </div>

    <div className="gauge-band">
      <UsageGauge label="CPU" value={value?.cpu.usagePercent || 0} color={usageColor(value?.cpu.usagePercent || 0)}
        detail={value ? `${value.cpu.logicalCores} 逻辑核 · 负载 ${value.cpu.load1.toFixed(2)} / ${value.cpu.load5.toFixed(2)} / ${value.cpu.load15.toFixed(2)}` : ''} />
      <UsageGauge label="内存" value={value?.memory.usedPercent || 0} color={usageColor(value?.memory.usedPercent || 0)}
        detail={value ? <>{bytes(value.memory.used)} / {bytes(value.memory.total)}（可用 {bytes(value.memory.available)}）<br />交换 {bytes(value.memory.swapUsed)} / {bytes(value.memory.swapTotal)}</> : ''} />
      <UsageGauge label="磁盘（/）" value={value?.disk.usedPercent || 0} color={usageColor(value?.disk.usedPercent || 0)}
        detail={value ? <>{bytes(value.disk.used)} / {bytes(value.disk.total)}<br />剩余 {bytes(value.disk.free)}</> : ''} />
      <div className="gauge-cell" role="img" aria-label={`网络速率，下行 ${rate(value?.network.recvRate || 0)}，上行 ${rate(value?.network.sendRate || 0)}`}>
        <Typography.Text strong>网络速率</Typography.Text>
        <div className="gauge-rate">
          <Typography.Text style={{ color: trafficDownColor, fontSize: 18, fontWeight: 600 }}><ArrowDownOutlined /> {rate(value?.network.recvRate || 0)}</Typography.Text>
          <Typography.Text style={{ color: 'var(--primary-color)', fontSize: 18, fontWeight: 600 }}><ArrowUpOutlined /> {rate(value?.network.sendRate || 0)}</Typography.Text>
        </div>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>10 分钟峰值 ↓{rate(peakRecv)} · ↑{rate(peakSend)}</Typography.Text>
      </div>
    </div>

    <section className="workspace-section">
      <Space className="section-heading"><Typography.Title level={4}>使用率趋势</Typography.Title><Typography.Text type="secondary">最近 10 分钟，悬停查看数值</Typography.Text></Space>
      <InteractiveChart height={220} area maxValue={100} ariaLabel="CPU 与内存使用率趋势，悬停查看数值" emptyText="暂无采样数据" labels={labels} formatValue={(v) => `${Math.round(v)}%`}
        series={[
          { name: 'CPU', color: 'var(--primary-color)', values: history.map((point) => point.cpuPercent) },
          { name: '内存', color: chartAccentColor, values: history.map((point) => point.memoryPercent) }
        ]} />
    </section>

    <section className="workspace-section">
      <Space className="section-heading"><Typography.Title level={4}>网络速率趋势</Typography.Title><Typography.Text type="secondary">上行 (↑) · 下行 (↓)</Typography.Text></Space>
      <InteractiveChart height={220} area ariaLabel="网络速率趋势，上行与下行，悬停查看数值" emptyText="暂无采样数据" labels={labels} formatValue={rate}
        series={[
          { name: '下行', color: trafficDownColor, values: history.map((point) => point.netRecvRate) },
          { name: '上行', color: 'var(--primary-color)', values: history.map((point) => point.netSendRate) }
        ]} />
    </section>

    {value && value.cpu.perCore.length > 0 ? <section className="workspace-section">
      <div className="section-heading"><Typography.Title level={4}>CPU 每核占用（共 {value.cpu.logicalCores} 逻辑核 / {value.cpu.physicalCores} 物理核）</Typography.Title><Typography.Text type="secondary">柱高为占用率，悬停查看数值</Typography.Text></div>
      <CoreBars perCore={value.cpu.perCore} />
    </section> : null}

    <section className="workspace-section">
      <Typography.Title level={4}>主机信息</Typography.Title>
      <Descriptions bordered size="small" column={2} items={[
        { key: 'hostname', label: '主机名', children: value?.host.hostname || '—' },
        { key: 'os', label: '系统', children: value ? `${value.host.platform} ${value.host.platformVersion}（${value.host.os}）` : '—' },
        { key: 'kernel', label: '内核', children: value ? `${value.host.kernelVersion} ${value.host.kernelArch}` : '—' },
        { key: 'cpu', label: '处理器', children: value?.cpu.modelName || '—' },
        { key: 'virtualization', label: '虚拟化', children: value?.host.virtualizationSystem || '—' },
        { key: 'boot', label: '开机时间', children: value?.host.bootTime ? new Date(value.host.bootTime * 1000).toLocaleString() : '—' },
        { key: 'uptime', label: '运行时长', children: value ? duration(value.host.uptimeSeconds) : '—' },
        { key: 'load', label: '负载', children: value ? `${value.cpu.load1.toFixed(2)} / ${value.cpu.load5.toFixed(2)} / ${value.cpu.load15.toFixed(2)}` : '—' }
      ]} />
    </section>

    <section className="workspace-section">
      <Space className="section-heading"><Typography.Title level={4}>网络接口</Typography.Title><Typography.Text type="secondary">不含回环接口</Typography.Text></Space>
      <Table<MonitorInterface> rowKey="name" size="small" pagination={false} scroll={{ x: 520 }} dataSource={value?.network.interfaces || []} columns={interfaceColumns} />
    </section>
  </Page>;
}

// CoreBars 用每核一根细柱呈现占用（btop/macOS 活动监视器式）：柱高即占用率，
// 核数多时柱变细、区块高度固定；悬停高亮并显示精确数值。SVG 按容器实测宽度
// 渲染，避免固定 viewBox 拉伸导致刻度文字变形，x 轴核号密度随宽度自适应。
function CoreBars({ perCore }: { perCore: number[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const [containerRef, width] = useElementWidth<HTMLDivElement>();
  const height = 150;
  const left = 40;
  const right = 8;
  const top = 10;
  const bottom = 22;
  const count = perCore.length;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const slot = plotWidth / count;
  const barWidth = slot * 0.7;
  const busiest = perCore.indexOf(Math.max(...perCore));
  const barX = (index: number) => left + index * slot + (slot - barWidth) / 2;
  const barY = (usage: number) => top + (1 - Math.min(usage, 100) / 100) * plotHeight;
  const labelStep = Math.max(1, Math.ceil(count / Math.max(4, Math.floor(plotWidth / 40))));
  const handleMove = (event: React.MouseEvent<SVGRectElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width === 0) return;
    const index = Math.floor(((event.clientX - rect.left) / rect.width) * count);
    setHover(Math.min(count - 1, Math.max(0, index)));
  };
  const handleTouch = (event: React.TouchEvent<SVGRectElement>) => {
    const touch = event.touches[0];
    if (!touch) return;
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width === 0) return;
    const index = Math.floor(((touch.clientX - rect.left) / rect.width) * count);
    setHover(Math.min(count - 1, Math.max(0, index)));
  };
  return <div className="core-chart" ref={containerRef} style={{ position: 'relative', marginTop: 8 }} role="img"
    aria-label={`CPU 每核占用柱状图，${count} 核，最忙为核 ${busiest}（${perCore[busiest].toFixed(1)}%）`}>
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ width: '100%', height: 150, display: 'block' }}>
      {[0, 25, 50, 75, 100].map((tick) => {
        const y = top + (1 - tick / 100) * plotHeight;
        return <g key={tick}>
          <line x1={left} y1={y} x2={width - right} y2={y} stroke="var(--border-color)" strokeWidth={tick === 0 ? 1.5 : 0.5} />
          {tick % 50 === 0
            ? <text x={left - 6} y={y + 4} textAnchor="end" fontSize="11" fill="var(--text-color)">{tick}%</text>
            : null}
        </g>;
      })}
      {perCore.map((_, index) => index % labelStep === 0 || index === count - 1
        ? <text key={`label-${index}`} x={left + index * slot + slot / 2} y={height - 6} textAnchor="middle" fontSize="11" fill="var(--text-color)">{index}</text>
        : null)}
      {perCore.map((usage, index) => (
        <rect key={index} className="core-bar"
          x={barX(index)} y={barY(usage)} width={barWidth}
          height={top + plotHeight - barY(usage)} rx={Math.min(2, barWidth / 2)}
          fill={usageColor(usage) || 'var(--primary-color)'}
          stroke={hover === index ? 'var(--text-color)' : 'none'} strokeWidth="1" vectorEffect="non-scaling-stroke"
          style={{ transition: 'y 0.4s ease, height 0.4s ease' }} />
      ))}
      <rect x={left} y={top} width={plotWidth} height={plotHeight} fill="transparent" onMouseMove={handleMove} onMouseLeave={() => setHover(null)} onTouchStart={handleTouch} onTouchMove={handleTouch} onTouchEnd={() => setHover(null)} />
    </svg>
    {hover !== null && (
      <div style={{ position: 'absolute', top: 4, left: `${Math.min(76, Math.max(16, ((barX(hover) + barWidth / 2) / width) * 100))}%`, transform: 'translateX(-50%)', background: 'var(--bg-container)', border: '1px solid var(--border-color)', borderRadius: 6, padding: '6px 10px', pointerEvents: 'none', whiteSpace: 'nowrap', zIndex: 2, boxShadow: 'var(--card-shadow)', display: 'flex', gap: 8, alignItems: 'baseline' }}>
        <Typography.Text strong style={{ fontSize: 12 }}>核 {hover}</Typography.Text>
        <Typography.Text style={{ fontSize: 12, color: usageColor(perCore[hover]!) || 'var(--primary-color)' }}>{perCore[hover]!.toFixed(1)}%</Typography.Text>
      </div>
    )}
  </div>;
}
