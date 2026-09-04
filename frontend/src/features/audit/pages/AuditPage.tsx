import { EyeOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { Button, Descriptions, Drawer, Form, Result, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useState } from 'react';
import { Page } from '../../../components/Page';
import { controlPlaneApi, type ManagementAuditEntry } from '../../../services/controlPlane';
import { ApiError } from '../../../services/http';
import {
  auditActionLabel,
  auditActionValues,
  auditResourceLabel,
  auditResourceTypeValues,
  resultLabels
} from '../../../util/labels';

const resourceOptions = auditResourceTypeValues.map((value) => ({ value, label: auditResourceLabel(value) }));
const actionOptions = auditActionValues.map((value) => ({ value, label: auditActionLabel(value) }));

function formatDiffValue(v: unknown) {
  if (v === null || v === undefined) return <span style={{ color: 'var(--text-secondary)', fontStyle: 'italic' }}>空</span>;
  if (typeof v === 'boolean') return v ? 'true (开启)' : 'false (关闭)';
  if (typeof v === 'object') return JSON.stringify(v, null, 2);
  return String(v);
}

function AuditDetailDrawer({ entry, onClose }: { entry: ManagementAuditEntry | null; onClose: () => void }) {
  if (!entry) return null;
  const rawDetails = typeof entry.details === 'object' && entry.details !== null
    ? (entry.details as Record<string, unknown>)
    : {};
  const changes = rawDetails.changes as Record<string, { old: unknown; new: unknown }> | undefined;
  const hasChanges = Boolean(changes && Object.keys(changes).length > 0);
  const otherDetails = { ...rawDetails };
  delete otherDetails.changes;
  const hasOtherDetails = Object.keys(otherDetails).length > 0;

  return (
    <Drawer title="审计详情与变更对账" width={520} open={Boolean(entry)} onClose={onClose} destroyOnClose>
      <Descriptions column={1} size="small" bordered style={{ marginBottom: 20 }}>
        <Descriptions.Item label="记录时间">{new Date(entry.createdAt).toLocaleString()}</Descriptions.Item>
        <Descriptions.Item label="操作动作">{auditActionLabel(entry.action)} ({entry.action})</Descriptions.Item>
        <Descriptions.Item label="影响资源">{auditResourceLabel(entry.resourceType)} ({entry.resourceId || '无'})</Descriptions.Item>
        <Descriptions.Item label="操作主体">{entry.actorId || '系统'}</Descriptions.Item>
        <Descriptions.Item label="执行结果">
          <Tag color={entry.result === 'success' ? 'success' : entry.result === 'denied' ? 'warning' : 'error'}>
            {resultLabels[entry.result] || entry.result}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="请求 ID">
          <Typography.Text code copyable>{entry.requestId}</Typography.Text>
        </Descriptions.Item>
      </Descriptions>

      {hasChanges && changes ? (
        <div style={{ marginBottom: 20 }}>
          <Typography.Title level={5} style={{ marginBottom: 12 }}>字段级变更对账 (Diff)</Typography.Title>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {Object.entries(changes).map(([field, diff]) => (
              <div
                key={field}
                style={{
                  border: '1px solid var(--border-color)',
                  borderRadius: 8,
                  overflow: 'hidden',
                  background: 'var(--bg-container)'
                }}
              >
                <div style={{ padding: '6px 12px', background: 'rgba(var(--primary-color-rgb), 0.08)', fontWeight: 600, fontSize: 13, borderBottom: '1px solid var(--border-color)' }}>
                  {field}
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', fontSize: 12 }}>
                  <div style={{ padding: '8px 12px', background: 'rgba(239, 68, 68, 0.08)', borderRight: '1px solid var(--border-color)', wordBreak: 'break-all' }}>
                    <div style={{ color: 'var(--color-error)', fontWeight: 600, marginBottom: 4 }}>- 改前旧值</div>
                    <pre style={{ margin: 0, whiteSpace: 'pre-wrap', fontFamily: "'JetBrains Mono', monospace" }}>{formatDiffValue(diff.old)}</pre>
                  </div>
                  <div style={{ padding: '8px 12px', background: 'rgba(34, 197, 94, 0.08)', wordBreak: 'break-all' }}>
                    <div style={{ color: 'var(--color-success)', fontWeight: 600, marginBottom: 4 }}>+ 改后新值</div>
                    <pre style={{ margin: 0, whiteSpace: 'pre-wrap', fontFamily: "'JetBrains Mono', monospace" }}>{formatDiffValue(diff.new)}</pre>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {hasOtherDetails ? (
        <div>
          <Typography.Title level={5} style={{ marginBottom: 8 }}>附加上下文信息</Typography.Title>
          <pre style={{ background: 'rgba(var(--primary-color-rgb), 0.05)', padding: 12, borderRadius: 8, fontSize: 12, overflowX: 'auto', border: '1px solid var(--border-color)' }}>
            {JSON.stringify(otherDetails, null, 2)}
          </pre>
        </div>
      ) : null}
    </Drawer>
  );
}

type Filters = { actor?: string; resource?: string; action?: string; result?: string };

export function AuditPage() {
  const [filters, setFilters] = useState<Filters>({});
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [selectedEntry, setSelectedEntry] = useState<ManagementAuditEntry | null>(null);

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
    { title: '请求 ID', dataIndex: 'requestId', ellipsis: true, render: (value: string) => <Typography.Text copyable>{value}</Typography.Text> },
    {
      title: '详情', width: 80, fixed: 'right',
      render: (_, row) => (
        <Button
          type="text"
          size="small"
          icon={<EyeOutlined />}
          onClick={() => setSelectedEntry(row)}
        >
          查看
        </Button>
      )
    }
  ];

  if (audit.isError) return <Result status={audit.error instanceof ApiError && audit.error.status === 403 ? '403' : 'error'} title={audit.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void audit.refetch()}>重试</Button>} />;

  return (
    <Page title="审计日志">
      <Form<Filters> layout="inline" onFinish={(values) => { setFilters(values); setPage(1); }} style={{ marginBottom: 16 }}>
        <Form.Item name="resource"><Select allowClear showSearch optionFilterProp="label" placeholder="资源类型" style={{ width: 150 }} options={resourceOptions} /></Form.Item>
        <Form.Item name="action"><Select allowClear showSearch optionFilterProp="label" placeholder="动作" style={{ width: 170 }} options={actionOptions} /></Form.Item>
        <Form.Item name="result"><Select allowClear placeholder="结果" style={{ width: 110 }} options={Object.entries(resultLabels).map(([value, label]) => ({ value, label }))} /></Form.Item>
        <Space>
          <Button type="primary" htmlType="submit" icon={<SearchOutlined />}>查询</Button>
          <Button icon={<ReloadOutlined />} onClick={() => void audit.refetch()}>刷新</Button>
        </Space>
      </Form>
      <Table
        rowKey="id"
        size="small"
        columns={columns}
        dataSource={audit.data?.items || []}
        loading={audit.isLoading}
        pagination={{
          current: page,
          pageSize,
          total: audit.data?.total || 0,
          showSizeChanger: true,
          pageSizeOptions: [20, 50, 100],
          showTotal: (total) => `共 ${total} 条`,
          onChange: (nextPage, nextPageSize) => {
            setPage(nextPageSize !== pageSize ? 1 : nextPage);
            if (nextPageSize !== pageSize) setPageSize(nextPageSize);
          }
        }}
        scroll={{ x: 960 }}
      />
      <AuditDetailDrawer entry={selectedEntry} onClose={() => setSelectedEntry(null)} />
    </Page>
  );
}
