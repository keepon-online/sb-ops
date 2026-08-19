import { CheckOutlined, ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App, Button, Segmented, Select, Space, Table, Tag } from 'antd';
import { useState } from 'react';
import { Page } from '../../../components/Page';
import { alertsApi } from '../../../services/alerts';
import { queryClient } from '../../../services/queryClient';
import { alertTypeLabel, auditResourceLabel } from '../../../util/labels';

export function AlertsPage() {
  const { message } = App.useApp();
  const [status, setStatus] = useState<'open' | 'resolved'>('open');
  const [severity, setSeverity] = useState('');
  const alerts = useQuery({ queryKey: ['alerts', status, severity], queryFn: () => alertsApi.list({ status, severity }) });
  const acknowledge = useMutation({ mutationFn: alertsApi.acknowledge, onSuccess: () => { void message.success('已确认告警'); void queryClient.invalidateQueries({ queryKey: ['alerts'] }); } });
  return <Page title="运行告警">
    <div className="page-toolbar"><Space wrap><Segmented value={status} onChange={(value) => setStatus(value as 'open' | 'resolved')} options={[{ label: '开放', value: 'open' }, { label: '已恢复', value: 'resolved' }]} /><Select allowClear value={severity || undefined} onChange={(value) => setSeverity(value || '')} placeholder="严重级别" options={[{ label: '严重', value: 'critical' }, { label: '警告', value: 'warning' }]} /><Tag color={alerts.data?.notifications.pending ? 'processing' : 'default'}>待发送 {alerts.data?.notifications.pending || 0}</Tag><Tag color={alerts.data?.notifications.failed ? 'error' : 'default'}>失败 {alerts.data?.notifications.failed || 0}</Tag></Space><Button icon={<ReloadOutlined />} onClick={() => void alerts.refetch()}>刷新</Button></div>
    <Table rowKey="id" size="small" scroll={{ x: 1100 }} loading={alerts.isLoading} dataSource={alerts.data?.items || []} columns={[{ title: '级别', dataIndex: 'severity', width: 90, render: (value) => <Tag color={value === 'critical' ? 'error' : 'warning'}>{value === 'critical' ? '严重' : '警告'}</Tag> }, { title: '类型', dataIndex: 'type', width: 180, render: (value: string) => alertTypeLabel(value) }, { title: '消息', dataIndex: 'message', width: 360 }, { title: '资源', width: 260, render: (_, row) => `${auditResourceLabel(row.resourceType)}:${row.resourceId}` }, { title: '最近出现', dataIndex: 'lastSeenAt', width: 180, render: (value) => new Date(value).toLocaleString() }, { title: '操作', width: 80, render: (_, row) => status === 'open' && !row.acknowledgedAt ? <Button type="text" icon={<CheckOutlined />} title="确认" loading={acknowledge.isPending} onClick={() => acknowledge.mutate(row.id)} /> : null }]} />
  </Page>;
}
