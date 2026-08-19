import {
  ApiOutlined,
  CloudDownloadOutlined,
  ControlOutlined,
  DesktopOutlined,
  LineChartOutlined,
  LinkOutlined,
  MobileOutlined,
  ReloadOutlined,
  SettingOutlined,
  TeamOutlined
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Progress, Result, Space, Statistic, Tag, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';
import { Page } from '../../../components/Page';
import { controlPlaneApi, type ManagementOverview } from '../../../services/controlPlane';
import { ApiError } from '../../../services/http';
import { configStatusLabels, kernelStatusLabels, runtimeStatusLabels } from '../../../util/labels';

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
  if (overview.isError) return <Result status={overview.error instanceof ApiError && overview.error.status === 403 ? '403' : 'error'} title={overview.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void overview.refetch()}>重试</Button>} />;
  const value = overview.data;
  const showTasks = Boolean(value?.tasks.some((task) => !task.completed));
  const autoCount = value?.devices?.auto ?? 0;
  return <Page title="总览">
    <div className="page-toolbar" style={{ marginBottom: 8 }}><Typography.Text type="secondary">控制面与本地 sing-box 运行状态</Typography.Text><Button icon={<ReloadOutlined />} loading={overview.isFetching} onClick={() => void overview.refetch()}>刷新</Button></div>

    <div className="stat-grid">
      <StatCard icon={<TeamOutlined />} label="用户" value={value?.counts.users || 0} />
      <StatCard icon={<ApiOutlined />} label="接入协议" value={value?.counts.protocols || 0} />
      <StatCard icon={<ControlOutlined />} label="订阅策略" value={value?.counts.policies || 0} />
      <StatCard icon={<CloudDownloadOutlined />} label="可下载订阅" value={value?.artifactCount || 0} />
      <StatCard icon={<MobileOutlined />} label="接入设备" value={value?.devices?.total || 0} extra={autoCount > 0 ? `自动发现 ${autoCount}` : undefined} />
      <StatCard icon={<DesktopOutlined />} label="24h 活跃设备" value={value?.devices?.activeDay || 0} />
      <StatCard icon={<LinkOutlined />} label="实时连接" value={value?.connections.count || 0} extra={value?.connections.available ? undefined : '不可用'} />
    </div>

    <section className="overview-card">
      <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>本地 sing-box</Typography.Title></div>
      <Descriptions column={1} size="small" items={[
        { key: 'runtime', label: '运行状态', children: <Tag color={statusColor(value?.runtimeStatus)}>{value ? runtimeStatusLabels[value.runtimeStatus] : '正在检查'}</Tag> },
        { key: 'config', label: '配置状态', children: value ? configStatusLabels[value.configStatus] : '正在检查' },
        { key: 'kernel', label: '内核健康检查', children: <Tag color={value?.kernelStatus === 'running' ? 'success' : value?.kernelStatus === 'unhealthy' ? 'error' : 'default'}>{value ? kernelStatusLabels[value.kernelStatus] : '正在检查'}</Tag> },
        { key: 'applied', label: '最后成功同步', children: value?.lastAppliedAt ? new Date(value.lastAppliedAt).toLocaleString() : '暂无' },
        { key: 'digest', label: '当前配置摘要', children: value?.configDigest ? <Typography.Text code style={{ fontSize: 12 }}>{value.configDigest}</Typography.Text> : '暂无' }
      ]} />
    </section>

    {value?.runtimeReason ? <Alert style={{ marginBottom: 16 }} type={value.runtimeStatus === 'unhealthy' ? 'error' : 'warning'} message="运行环境需要处理" description={value.runtimeReason} action={<Button size="small" onClick={() => navigate('/singbox')}>前往服务端配置</Button>} /> : null}

    <section className="overview-card">
      <div className="overview-card-title">
        <Typography.Title level={4} style={{ margin: 0 }}>实时连接</Typography.Title>
        <Space>
          <Button size="small" icon={<SettingOutlined />} onClick={() => navigate('/singbox')}>服务端配置</Button>
          <Button size="small" icon={<LineChartOutlined />} onClick={() => navigate('/traffic?dimension=user')}>流量分析</Button>
        </Space>
      </div>
      {value?.connections.available
        ? <Typography.Paragraph type="secondary" style={{ margin: 0 }}>当前 {value.connections.count} 条连接。这是当前时刻的 Clash 连接快照，不等同于用户计费流量。</Typography.Paragraph>
        : <Alert type="info" message="实时连接不可用" description={value?.connections.reason || '尚未配置本机或内部 Clash API'} action={<Button size="small" onClick={() => navigate('/singbox')}>前往配置</Button>} />}
    </section>

    <section className="overview-card">
      <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>流量概览</Typography.Title><Typography.Text type="secondary">过去 24 小时</Typography.Text></div>
      <Space size={36} wrap>
        <Statistic title="总流量" value={formatBytes(value?.traffic24h.totalBytes || 0)} />
        <Statistic title="上行" value={formatBytes(value?.traffic24h.upBytes || 0)} />
        <Statistic title="下行" value={formatBytes(value?.traffic24h.downBytes || 0)} />
        <Statistic title="采样次数" value={value?.traffic24h.samples || 0} />
        <Statistic title="最近采集" value={value?.lastTrafficCollectedAt ? new Date(value.lastTrafficCollectedAt).toLocaleString() : '暂无'} />
      </Space>
      {value?.devices && Object.keys(value.devices.platforms || {}).length > 0
        ? <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0 }}>设备平台分布：{Object.entries(value.devices.platforms).map(([platform, count]) => `${platformLabels[platform] || platform} ${count}`).join(' · ')}</Typography.Paragraph>
        : null}
    </section>

    <section className="overview-card">
      <div className="overview-card-title"><Typography.Title level={4} style={{ margin: 0 }}>用户历史流量排行</Typography.Title></div>
      {value?.topUsers?.length
        ? <Space direction="vertical" style={{ width: '100%' }} size={8}>
            {value.topUsers.map((item) => {
              const total = value.topUsers.reduce((sum, user) => sum + user.totalBytes, 0);
              const percent = total > 0 ? (item.totalBytes / total) * 100 : 0;
              return <div key={item.id}>
                <div className="page-toolbar" style={{ marginBottom: 2 }}>
                  <Typography.Text>{item.name}</Typography.Text>
                  <Typography.Text type="secondary">{formatBytes(item.totalBytes)} · {percent.toFixed(0)}%</Typography.Text>
                </div>
                <Progress percent={percent} showInfo={false} size="small" strokeColor="var(--primary-color)" />
              </div>;
            })}
          </Space>
        : <Typography.Paragraph type="secondary" style={{ margin: 0 }}>暂无可归因到用户的历史流量。</Typography.Paragraph>}
    </section>

    {value?.lastFailedAt ? <Alert style={{ marginBottom: 16 }} type="error" message={`最近同步失败 ${new Date(value.lastFailedAt).toLocaleString()}`} description={`${value.failureReason || '未知错误'}。资源已保存，但 sing-box 仍使用上一份成功配置。`} /> : null}
    {showTasks ? <section className="overview-card">
      <Typography.Title level={4} style={{ margin: 0 }}>开始使用</Typography.Title>
      <Space direction="vertical" style={{ width: '100%' }} size={8}>
        {value?.tasks.map((task) => <div className="page-toolbar" key={task.key}><Space><Tag color={task.completed ? 'success' : 'default'}>{task.completed ? '已完成' : '待完成'}</Tag><Typography.Text>{task.title}</Typography.Text></Space>{!task.completed && task.action ? <Button size="small" onClick={() => navigate(task.action!)}>前往处理</Button> : null}</div>)}
      </Space>
    </section> : null}
  </Page>;
}

function statusColor(status: ManagementOverview['runtimeStatus'] | undefined) {
  if (status === 'running') return 'success';
  if (status === 'unhealthy') return 'error';
  return 'default';
}
