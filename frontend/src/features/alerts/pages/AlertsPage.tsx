import { CheckOutlined, LineChartOutlined, ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App, Button, Segmented, Select, Space, Statistic, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import { InteractiveChart } from '../../../components/InteractiveChart';
import { Page } from '../../../components/Page';
import { alertsApi } from '../../../services/alerts';
import { queryClient } from '../../../services/queryClient';
import { alertTypeLabel, auditResourceLabel } from '../../../util/labels';

function formatRepairDuration(seconds?: number): string {
  if (seconds === undefined) return '—';
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))} 分钟`;
  if (seconds < 86400) return `${(seconds / 3600).toFixed(1)} 小时`;
  return `${(seconds / 86400).toFixed(1)} 天`;
}

export function AlertsPage() {
  const { message } = App.useApp();
  const [status, setStatus] = useState<'open' | 'resolved'>('open');
  const [severity, setSeverity] = useState('');
  const alerts = useQuery({ queryKey: ['alerts', status, severity], queryFn: () => alertsApi.list({ status, severity }) });
  const stats = useQuery({ queryKey: ['alert-stats'], queryFn: () => alertsApi.stats(30) });
  const acknowledge = useMutation({ mutationFn: alertsApi.acknowledge, onSuccess: () => { void message.success('已确认告警'); void queryClient.invalidateQueries({ queryKey: ['alerts'] }); } });
  const daily = stats.data?.daily || [];
  return <Page title="运行告警">
    <div className="page-toolbar"><Space wrap><Segmented value={status} onChange={(value) => setStatus(value as 'open' | 'resolved')} options={[{ label: '开放', value: 'open' }, { label: '已恢复', value: 'resolved' }]} /><Select allowClear value={severity || undefined} onChange={(value) => setSeverity(value || '')} placeholder="严重级别" options={[{ label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }]} /><Tag color={alerts.data?.notifications.pending ? 'processing' : 'default'}>待发送 {alerts.data?.notifications.pending || 0}</Tag><Tag color={alerts.data?.notifications.failed ? 'error' : 'default'}>失败 {alerts.data?.notifications.failed || 0}</Tag></Space><Button icon={<ReloadOutlined />} onClick={() => { void alerts.refetch(); void stats.refetch(); }}>刷新</Button></div>
    <section className="overview-card">
      <div className="overview-card-title">
        <Typography.Title level={4} style={{ margin: 0 }}>最近 30 天趋势</Typography.Title>
        <Typography.Text type="secondary"><LineChartOutlined /> 新增 (+) · 恢复 (−)</Typography.Text>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 24, marginBottom: 12 }}>
        <Statistic title="当前开放" value={stats.data?.currentOpen ?? '—'} valueStyle={stats.data?.currentOpen ? { color: 'var(--color-error)' } : undefined} />
        <Statistic title="新增" value={stats.data?.totalOpened ?? '—'} />
        <Statistic title="恢复" value={stats.data?.totalResolved ?? '—'} />
        <Statistic title="平均修复" value={stats.data ? formatRepairDuration(stats.data.mttrSeconds) : '—'} />
      </div>
      <InteractiveChart
        height={200}
        integerTicks
        ariaLabel="告警趋势，新增与恢复每日计数，悬停查看数值"
        emptyText="最近 30 天暂无告警"
        labels={daily.map((item) => item.date.slice(5))}
        formatValue={(value) => String(Math.round(value))}
        series={[
          { name: '新增', color: 'var(--color-error)', values: daily.map((item) => item.opened) },
          { name: '恢复', color: 'var(--color-success)', values: daily.map((item) => item.resolved) }
        ]}
      />
    </section>
    <Table rowKey="id" size="small" scroll={{ x: 1100 }} loading={alerts.isLoading} dataSource={alerts.data?.items || []} columns={[{ title: '级别', dataIndex: 'severity', width: 90, render: (value) => <Tag color={value === 'critical' ? 'error' : 'warning'}>{value === 'critical' ? '严重' : '警告'}</Tag> }, { title: '类型', dataIndex: 'type', width: 180, render: (value: string) => alertTypeLabel(value) }, { title: '消息', dataIndex: 'message', width: 360 }, { title: '资源', width: 260, render: (_, row) => `${auditResourceLabel(row.resourceType)}:${row.resourceId}` }, { title: '最近出现', dataIndex: 'lastSeenAt', width: 180, render: (value) => new Date(value).toLocaleString() }, { title: '操作', width: 80, render: (_, row) => status === 'open' && !row.acknowledgedAt ? <Button type="text" icon={<CheckOutlined />} title="确认" loading={acknowledge.isPending} onClick={() => acknowledge.mutate(row.id)} /> : null }]} />
  </Page>;
}
