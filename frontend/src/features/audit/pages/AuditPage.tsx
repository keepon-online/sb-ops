import { ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Button, Form, Input, Result, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useState } from 'react';
import { Page } from '../../../components/Page';
import { controlPlaneApi, type ManagementAuditEntry } from '../../../services/controlPlane';
import { ApiError } from '../../../services/http';
import { auditActionLabel, auditResourceLabel, resultLabels } from '../../../util/labels';

type Filters = { actor?: string; resource?: string; action?: string; result?: string };
export function AuditPage() {
  const [filters, setFilters] = useState<Filters>({});
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const audit = useQuery({
    queryKey: ['audit', filters, page, pageSize],
    queryFn: () => controlPlaneApi.audit({ ...filters, limit: pageSize, cursor: String((page - 1) * pageSize) }),
    retry: false
  });
  const columns: ColumnsType<ManagementAuditEntry> = [
    { title: '时间', dataIndex: 'createdAt', width: 180, render: (value: string) => new Date(value).toLocaleString() },
    { title: '动作', dataIndex: 'action', width: 180, render: (value: string) => auditActionLabel(value) },
    { title: '资源', width: 200, render: (_, value) => `${auditResourceLabel(value.resourceType)}:${value.resourceId}` },
    { title: '结果', dataIndex: 'result', width: 90, render: (value: string) => <Tag color={value === 'success' ? 'success' : value === 'denied' ? 'warning' : 'error'}>{resultLabels[value] || value}</Tag> },
    { title: '请求 ID', dataIndex: 'requestId', ellipsis: true, render: (value: string) => <Typography.Text copyable>{value}</Typography.Text> }
  ];
  if (audit.isError) return <Result status={audit.error instanceof ApiError && audit.error.status === 403 ? '403' : 'error'} title={audit.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void audit.refetch()}>重试</Button>} />;
  return <Page title="审计日志"><Form<Filters> layout="inline" onFinish={(values) => { setFilters(values); setPage(1); }} style={{ marginBottom: 16 }}><Form.Item name="resource"><Input placeholder="资源类型" /></Form.Item><Form.Item name="action"><Input placeholder="动作" /></Form.Item><Form.Item name="result"><Select allowClear placeholder="结果" style={{ width: 110 }} options={Object.entries(resultLabels).map(([value, label]) => ({ value, label }))} /></Form.Item><Space><Button type="primary" htmlType="submit" icon={<SearchOutlined />}>查询</Button><Button icon={<ReloadOutlined />} onClick={() => void audit.refetch()}>刷新</Button></Space></Form><Table rowKey="id" size="small" columns={columns} dataSource={audit.data?.items || []} loading={audit.isLoading} pagination={{ current: page, pageSize, total: audit.data?.total || 0, showSizeChanger: true, pageSizeOptions: [20, 50, 100], showTotal: (total) => `共 ${total} 条`, onChange: (nextPage, nextPageSize) => { setPage(nextPageSize !== pageSize ? 1 : nextPage); if (nextPageSize !== pageSize) setPageSize(nextPageSize); } }} scroll={{ x: 900 }} /></Page>;
}
