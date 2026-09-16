import {
  ApiOutlined,
  CloudDownloadOutlined,
  ControlOutlined,
  DesktopOutlined,
  LineChartOutlined,
  LinkOutlined,
  MobileOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
  ThunderboltOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Col, Descriptions, Progress, Result, Row, Space, Statistic, Tag, Typography } from 'antd';
import dayjs from 'dayjs';
import { useNavigate } from 'react-router-dom';
import { CERTIFICATE_WARNING_DAYS, CertificateExpiryTag, certificateDaysLeft } from '../../../components/CertificateExpiryTag';
import { InteractiveChart } from '../../../components/InteractiveChart';
import { Page } from '../../../components/Page';
import { controlPlaneApi, type ManagementOverview } from '../../../services/controlPlane';
import { ApiError } from '../../../services/http';
import { certificatesApi, protocolsApi } from '../../../services/protocols';
import { queryClient } from '../../../services/queryClient';
import { trafficApi } from '../../../services/traffic';
import { configStatusLabels, kernelStatusLabels, runtimeStatusLabels } from '../../../util/labels';
import { useAppTheme } from '../../../context/ThemeContext';
import { formatDateTime } from '../../../util/time';

const platformLabels: Record<string, string> = {
  windows: 'Windows', macos: 'macOS', linux: 'Linux', android: 'Android', ios: 'iOS', unknown: '未知'
};

function formatBytes(value: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) { current /= 1024; index += 1; }
  return `${current.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function StatCard({ icon, label, value, extra }: { icon: React.ReactNode; label: string; value: React.ReactNode; extra?: React.ReactNode }) {
  return <div className="stat-card">
    <div className="stat-icon">{icon}</div>
    <div className="stat-label">{label}</div>
    <div className="stat-value">{value}</div>
    {extra ? <div className="stat-extra">{extra}</div> : null}
  </div>;
}

export function OverviewPage() {
  const navigate = useNavigate();
  const overview = useQuery({ queryKey: ['overview'], queryFn: controlPlaneApi.overview, retry: false, refetchInterval: 15000 });
  const traffic24hSeries = useQuery({
    queryKey: ['traffic', 'series-24h-overview'],
    queryFn: () => {
      const to = dayjs().endOf('hour');
      const from = to.subtract(24, 'hour');
      return trafficApi.series({ from: from.toISOString(), to: to.toISOString(), interval: 'hour' });
    },
    retry: false,
    refetchInterval: 60000
  });
  // 方案 1：拨测与证书在前端独立拉取（与协议页共享 queryKey 缓存），保持
  // overview 聚合端点不膨胀；拨测 5 分钟一轮，60s 轮询足够新鲜。
  const probeResults = useQuery({ queryKey: ['resources', 'protocols-probe'], queryFn: protocolsApi.probeResults, retry: false, refetchInterval: 60000 });
  const certificates = useQuery({ queryKey: ['resources', 'certificates'], queryFn: certificatesApi.list, retry: false });
  const runProbe = useMutation({
    mutationFn: () => protocolsApi.runProbe(),
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ['resources', 'protocols-probe'] }); }
  });
  // 所有 Hook 必须在任何提前 return 之前调用：overview 轮询中途转 isError
  // 会让重渲染少一个 Hook，React 直接抛「Rendered fewer hooks than expected」。
  const { trafficDownColor } = useAppTheme();

  if (overview.isError) {
    return (
      <Page title="控制总览">
        <div className="overview-card" style={{ padding: '48px 24px', textAlign: 'center' }}>
          <Result
            status={overview.error instanceof ApiError && overview.error.status === 403 ? '403' : 'error'}
            title={overview.error.message}
            extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void overview.refetch()}>重试</Button>}
          />
        </div>
      </Page>
    );
  }
  const value = overview.data;
  const showTasks = Boolean(value?.tasks.some((task) => !task.completed));
  const autoCount = value?.devices?.auto ?? 0;

  const trafficPoints = traffic24hSeries.data?.items || [];
  const probeItems = probeResults.data?.items || [];
  const expiringCertificates = (certificates.data?.items || [])
    .filter((certificate) => certificateDaysLeft(certificate.notAfter) <= CERTIFICATE_WARNING_DAYS)
    .sort((a, b) => certificateDaysLeft(a.notAfter) - certificateDaysLeft(b.notAfter));
  const sparklineLabels = trafficPoints.map((p) => dayjs(p.bucket).format('HH:mm'));
  const sparklineSeries = [
    { name: '下行', color: trafficDownColor, values: trafficPoints.map((p) => p.downBytes) },
    { name: '上行', color: 'var(--primary-color)', values: trafficPoints.map((p) => p.upBytes) }
  ];

  return <Page title="控制总览">
    <div className="page-toolbar" style={{ marginBottom: 8 }}>
      <Typography.Text type="secondary">控制面与本地 sing-box 运行状态</Typography.Text>
      <Button icon={<ReloadOutlined />} loading={overview.isFetching} onClick={() => void overview.refetch()}>刷新</Button>
    </div>

    <div className="stat-grid">
      <StatCard icon={<TeamOutlined />} label="用户" value={value?.counts.users || 0} />
      <StatCard icon={<ApiOutlined />} label="接入协议" value={value?.counts.protocols || 0} />
      <StatCard icon={<ControlOutlined />} label="订阅策略" value={value?.counts.policies || 0} />
      <StatCard icon={<CloudDownloadOutlined />} label="可下载订阅" value={value?.artifactCount || 0} />
      <StatCard icon={<MobileOutlined />} label="接入设备" value={value?.devices?.total || 0} extra={autoCount > 0 ? `自动发现 ${autoCount}` : undefined} />
      <StatCard icon={<DesktopOutlined />} label="24h 活跃设备" value={value?.devices?.activeDay || 0} />
      <StatCard icon={<LinkOutlined />} label="实时连接" value={value?.connections.count || 0} extra={value?.connections.available ? undefined : '不可用'} />
    </div>

    {value?.lastFailedAt ? (
      <Alert
        style={{ marginBottom: 16 }}
        type="error"
        showIcon
        message={`最近同步失败 ${formatDateTime(value.lastFailedAt)}`}
        description={`${value.failureReason || '未知错误'}。资源已保存，但 sing-box 仍使用上一份成功配置。`}
      />
    ) : null}
    {value?.runtimeReason ? (
      <Alert
        style={{ marginBottom: 16 }}
        type={value.runtimeStatus === 'unhealthy' ? 'error' : 'warning'}
        showIcon
        message="运行环境需要处理"
        description={value.runtimeReason}
        action={<Button size="small" onClick={() => navigate('/singbox')}>前往服务配置</Button>}
      />
    ) : null}

    <Row gutter={[16, 16]}>
      <Col xs={24} lg={12}>
        <section className="overview-card">
          <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>本地 sing-box</Typography.Title></div>
          <Descriptions column={1} size="small" items={[
            { key: 'runtime', label: '运行状态', children: <Tag color={statusColor(value?.runtimeStatus)}>{value ? runtimeStatusLabels[value.runtimeStatus] : '正在检查'}</Tag> },
            { key: 'config', label: '配置状态', children: value ? configStatusLabels[value.configStatus] : '正在检查' },
            { key: 'kernel', label: '内核健康检查', children: <Tag color={value?.kernelStatus === 'running' ? 'success' : value?.kernelStatus === 'unhealthy' ? 'error' : 'default'}>{value ? kernelStatusLabels[value.kernelStatus] : '正在检查'}</Tag> },
            { key: 'applied', label: '最后成功同步', children: value?.lastAppliedAt ? <span style={{ fontFamily: "'JetBrains Mono', ui-monospace, monospace" }}>{formatDateTime(value.lastAppliedAt)}</span> : '暂无' },
            { key: 'digest', label: '当前配置摘要', children: value?.configDigest ? <Typography.Text code style={{ fontSize: 12 }}>{value.configDigest}</Typography.Text> : '暂无' }
          ]} />
        </section>

        <section className="overview-card">
          <div className="overview-card-title">
            <Typography.Title level={4} style={{ margin: 0 }}>协议链路健康</Typography.Title>
            <Space>
              <Button size="small" icon={<ApiOutlined />} onClick={() => navigate('/protocols')}>协议管理</Button>
              <Button size="small" icon={<ThunderboltOutlined />} loading={runProbe.isPending} onClick={() => runProbe.mutate()}>立即拨测</Button>
            </Space>
          </div>
          {probeItems.length > 0 ? (
            <Space direction="vertical" style={{ width: '100%' }} size={6}>
              {probeItems.map((item) => (
                <div className="page-toolbar" key={item.protocolId} style={{ marginBottom: 0 }}>
                  <Space size={6}>
                    <Tag color={item.status === 'ok' ? 'success' : item.status === 'failed' ? 'error' : 'default'}>
                      {item.status === 'ok' ? '通' : item.status === 'failed' ? '失败' : '跳过'}
                    </Tag>
                    <Typography.Text>{item.protocolName}</Typography.Text>
                  </Space>
                  {item.status === 'ok' && item.latencyMs != null ? (
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>{item.latencyMs} ms</Typography.Text>
                  ) : item.error ? (
                    <Typography.Text type="secondary" ellipsis style={{ maxWidth: 220, fontSize: 12 }} title={item.error}>{item.error}</Typography.Text>
                  ) : (
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>未拨测</Typography.Text>
                  )}
                </div>
              ))}
            </Space>
          ) : (
            <Typography.Paragraph type="secondary" style={{ margin: 0 }}>暂无拨测结果（后台每 5 分钟一轮，或点击「立即拨测」）。</Typography.Paragraph>
          )}
        </section>

        <section className="overview-card">
          <div className="overview-card-title">
            <Typography.Title level={4} style={{ margin: 0 }}>实时连接</Typography.Title>
            <Space>
              <Button size="small" icon={<SettingOutlined />} onClick={() => navigate('/singbox')}>服务配置</Button>
              <Button size="small" icon={<LineChartOutlined />} onClick={() => navigate('/traffic?dimension=user')}>流量分析</Button>
            </Space>
          </div>
          {value?.connections.available
            ? <Typography.Paragraph type="secondary" style={{ margin: 0 }}>当前 {value.connections.count} 条活跃连接（Clash API 实时快照）。</Typography.Paragraph>
            : <Alert type="info" message="实时连接不可用" description={value?.connections.reason || '尚未配置本机或内部 Clash API'} action={<Button size="small" onClick={() => navigate('/singbox')}>前往配置</Button>} />}
        </section>

        {showTasks ? (
          <section className="overview-card">
            <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>开始使用</Typography.Title></div>
            <Space direction="vertical" style={{ width: '100%' }} size={8}>
              {value?.tasks.map((task) => (
                <div className="page-toolbar" key={task.key}>
                  <Space>
                    <Tag color={task.completed ? 'success' : 'default'}>{task.completed ? '已完成' : '待完成'}</Tag>
                    <Typography.Text>{task.title}</Typography.Text>
                  </Space>
                  {!task.completed && task.action ? <Button size="small" onClick={() => navigate(task.action!)}>前往处理</Button> : null}
                </div>
              ))}
            </Space>
          </section>
        ) : null}
      </Col>

      <Col xs={24} lg={12}>
        {value?.bandwidth ? (
          <section className="overview-card">
            <div className="overview-card-title">
              <Typography.Title level={4} style={{ margin: 0 }}>VPS 月流量</Typography.Title>
              <Typography.Text type="secondary">提供商计量 · 账单口径</Typography.Text>
            </div>
            <Space size={24} wrap>
              <Statistic title="已用" value={formatBytes(value.bandwidth.usedBytes)} />
              <Statistic title="月度配额" value={formatBytes(value.bandwidth.totalBytes)} />
              <Statistic title="已用比例" value={`${value.bandwidth.percent.toFixed(1)}%`} />
              <Statistic title="最近采样" value={formatDateTime(value.bandwidth.updatedAt)} valueStyle={{ fontSize: 14, fontFamily: "'JetBrains Mono', ui-monospace, monospace" }} />
            </Space>
            <Progress
              style={{ maxWidth: 480, marginTop: 8 }}
              percent={Math.min(100, value.bandwidth.percent)}
              format={(percent) => `${(percent ?? 0).toFixed(1)}%`}
              status={value.bandwidth.percent >= 80 ? 'exception' : 'normal'}
              strokeColor={value.bandwidth.percent >= 95 ? 'var(--color-error)' : undefined}
            />
            <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0, fontSize: 12 }}>
              数字来自 VPS 提供商 SolusVM API，含全部网卡流量与宿主层开销。
            </Typography.Paragraph>
          </section>
        ) : null}

        <section className="overview-card">
          <div className="overview-card-title">
            <Typography.Title level={4} style={{ margin: 0 }}>流量概览</Typography.Title>
            <Typography.Text type="secondary">过去 24 小时</Typography.Text>
          </div>
          <Space size={24} wrap>
            <Statistic title="总流量" value={formatBytes(value?.traffic24h.totalBytes || 0)} />
            <Statistic title="上行" value={formatBytes(value?.traffic24h.upBytes || 0)} />
            <Statistic title="下行" value={formatBytes(value?.traffic24h.downBytes || 0)} />
            <Statistic title="采样次数" value={value?.traffic24h.samples || 0} />
          </Space>
          {trafficPoints.length > 0 ? (
            <div style={{ marginTop: 14 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                <Typography.Text strong style={{ fontSize: 12 }}>24 小时流量走势</Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 11 }}>上行 (↑) · 下行 (↓)</Typography.Text>
              </div>
              <InteractiveChart
                height={140}
                area
                labels={sparklineLabels}
                formatValue={formatBytes}
                ariaLabel="24 小时流量时序趋势"
                emptyText="暂无 24 小时时序数据"
                series={sparklineSeries}
              />
            </div>
          ) : null}
          {value?.devices && Object.keys(value.devices.platforms || {}).length > 0 ? (
            <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0, fontSize: 12 }}>
              设备平台分布：{Object.entries(value.devices.platforms).map(([platform, count]) => `${platformLabels[platform] || platform} ${count}`).join(' · ')}
            </Typography.Paragraph>
          ) : null}
        </section>

        {expiringCertificates.length > 0 ? (
          <section className="overview-card">
            <div className="overview-card-title">
              <Typography.Title level={4} style={{ margin: 0 }}><SafetyCertificateOutlined /> 证书预警</Typography.Title>
              <Button size="small" onClick={() => navigate('/protocols?tab=certificates')}>证书管理</Button>
            </div>
            <Space direction="vertical" style={{ width: '100%' }} size={6}>
              {expiringCertificates.map((certificate) => (
                <div className="page-toolbar" key={certificate.id} style={{ marginBottom: 0 }}>
                  <Space size={6}>
                    <Typography.Text>{certificate.name}</Typography.Text>
                    {certificate.domain ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>{certificate.domain}</Typography.Text> : null}
                  </Space>
                  <CertificateExpiryTag notAfter={certificate.notAfter} showDate={false} />
                </div>
              ))}
            </Space>
            <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0, fontSize: 12 }}>
              {CERTIFICATE_WARNING_DAYS} 天内到期或已过期，续期判断以后台配置为准。
            </Typography.Paragraph>
          </section>
        ) : null}

        <section className="overview-card">
          <div className="overview-card-title">
            <Typography.Title level={4} style={{ margin: 0 }}>用户历史流量排行</Typography.Title>
          </div>
          {value?.topUsers?.length ? (
            <Space direction="vertical" style={{ width: '100%' }} size={8}>
              {value.topUsers.map((item) => {
                const total = value.topUsers.reduce((sum, user) => sum + user.totalBytes, 0);
                const percent = total > 0 ? (item.totalBytes / total) * 100 : 0;
                return (
                  <div key={item.id}>
                    <div className="page-toolbar" style={{ marginBottom: 2 }}>
                      <Typography.Text>{item.name}</Typography.Text>
                      <Typography.Text type="secondary">{formatBytes(item.totalBytes)} · {percent.toFixed(0)}%</Typography.Text>
                    </div>
                    <Progress percent={percent} showInfo={false} size="small" strokeColor="var(--primary-color)" />
                  </div>
                );
              })}
            </Space>
          ) : (
            <Typography.Paragraph type="secondary" style={{ margin: 0 }}>暂无可归因到用户的历史流量。</Typography.Paragraph>
          )}
        </section>
      </Col>
    </Row>
  </Page>;
}

function statusColor(status: ManagementOverview['runtimeStatus'] | undefined) {
  if (status === 'running') return 'success';
  if (status === 'unhealthy') return 'error';
  return 'default';
}
