import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  LineChartOutlined,
  MobileOutlined,
  PlusOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  ThunderboltOutlined
} from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  App as AntApp,
  Button,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Progress,
  QRCode,
  Result,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import dayjs, { type Dayjs } from 'dayjs';
import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Page } from '../../../components/Page';
import { DevicesDrawer } from '../components/DevicesDrawer';
import { ApiCode, ApiError } from '../../../services/http';
import { policiesApi } from '../../../services/policies';
import { protocolsApi } from '../../../services/protocols';
import { queryClient } from '../../../services/queryClient';
import {
  usersApi,
  type ResourceUser,
  type ResourceUserStatus,
  subscriptionUrl
} from '../../../services/users';

type UserFormValues = {
  name: string;
  enabled: boolean;
  quotaGB: number;
  expiresAt?: Dayjs;
  resetIntervalDays: number;
  protocolIds: string[];
  policyId?: string;
};

type QuotaFormValues = { mode: 'add' | 'sub' | 'reset'; gb: number };

const statusMeta: Record<ResourceUserStatus, { label: string; color: string }> = {
  active: { label: '可用', color: 'success' },
  disabled: { label: '停用', color: 'default' },
  expired: { label: '已过期', color: 'warning' },
  quotaExceeded: { label: '额度耗尽', color: 'error' }
};

const subscriptionFormats = ['clash.yaml'];

const formatHints: Record<string, string> = {
  'clash.yaml': 'Clash 系客户端（Verge、ClashX、Clash for Windows、Clash Meta）：订阅 → 从 URL 导入。'
};

function formatBytes(value: number, unlimited = false) {
  if (value === 0) return unlimited ? '不限' : '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

// remainingText renders the quota remainder, surfacing overuse explicitly
// instead of a meaningless "剩余 0 B" once used has passed the quota.
function remainingText(quotaBytes: number, usedBytes: number) {
  if (quotaBytes <= 0) return '不限额度';
  const diff = usedBytes - quotaBytes;
  if (diff >= 0) return `已超出 ${formatBytes(diff)}`;
  return `剩余 ${formatBytes(-diff)}`;
}


// 到期时间快捷项：按天粒度的常用时长；留空（allowClear）为永久。
const expiresPresets = [
  { label: '7 天', value: dayjs().add(7, 'day') },
  { label: '30 天', value: dayjs().add(30, 'day') },
  { label: '90 天', value: dayjs().add(90, 'day') },
  { label: '半年', value: dayjs().add(6, 'month') },
  { label: '一年', value: dayjs().add(1, 'year') }
];

export function UsersPage() {
  const { message, notification } = AntApp.useApp();
  const screens = Grid.useBreakpoint();
  const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<ResourceUserStatus | undefined>();
  const [sort, setSort] = useState('createdAt:desc');
  const [cursor, setCursor] = useState<string>();
  const [cursorHistory, setCursorHistory] = useState<(string | undefined)[]>([]);
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [editing, setEditing] = useState<ResourceUser | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [issuedToken, setIssuedToken] = useState<string | null>(null);
  const [qrFormat, setQrFormat] = useState<string>();
  const [devicesUser, setDevicesUser] = useState<ResourceUser | null>(null);
  const [trafficUser, setTrafficUser] = useState<ResourceUser | null>(null);
  const [form] = Form.useForm<UserFormValues>();
  const [trafficForm] = Form.useForm<QuotaFormValues>();

  const params = useMemo(() => ({ cursor, limit: 20, query: search || undefined, status, sort }), [cursor, search, status, sort]);
  const users = useQuery({ queryKey: ['resources', 'users', params], queryFn: () => usersApi.list(params), retry: false });
  const protocols = useQuery({ queryKey: ['resources', 'protocols'], queryFn: protocolsApi.list, retry: false });
  const policies = useQuery({ queryKey: ['resources', 'policies'], queryFn: policiesApi.list, retry: false });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['resources', 'users'] });
  const reportError = (error: Error) => {
    const syncFailed = error instanceof ApiError && error.code === ApiCode.SyncFailed;
    if (syncFailed) {
      void Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: ['overview'] })]);
      notification.error({
        message: '已保存，但尚未应用到 sing-box',
        description: `上一份成功配置仍在运行。请在总览查看原因后修正并再次保存。${error.requestId ? ` 请求 ID：${error.requestId}` : ''}`
      });
    } else {
      notification.error({
        message: error.message,
        description: error instanceof ApiError && error.requestId ? `请求 ID：${error.requestId}` : undefined
      });
    }
    if (error instanceof ApiError && Object.keys(error.fieldErrors).length > 0) {
      const fieldNames: Record<string, keyof UserFormValues> = {
        name: 'name',
        enabled: 'enabled',
        quotaBytes: 'quotaGB',
        expiresAt: 'expiresAt',
        resetIntervalDays: 'resetIntervalDays',
        protocolIds: 'protocolIds',
        policyId: 'policyId'
      };
      const errors = Object.entries(error.fieldErrors)
        .map(([name, message]) => ({ name: fieldNames[name] ?? name, errors: [message] }));
      form.setFields(errors);
    }
    return syncFailed;
  };
  const closeDrawer = () => {
    setDrawerOpen(false);
    setEditing(null);
    form.resetFields();
  };
  const create = useMutation({
    mutationFn: (values: UserFormValues) => usersApi.create({
      name: values.name,
      enabled: values.enabled,
      quotaBytes: Math.round((values.quotaGB || 0) * 1024 * 1024 * 1024),
      expiresAt: values.expiresAt ? values.expiresAt.endOf('day').toISOString() : undefined,
      resetIntervalDays: values.resetIntervalDays || 0,
      protocolIds: values.protocolIds,
      policyIds: values.policyId ? [values.policyId] : []
    }),
    onSuccess: (result) => {
      void refresh();
      closeDrawer();
      setIssuedToken(result.subscriptionToken);
      message.success('用户已创建并同步到 sing-box');
    },
    onError: (error: Error) => { if (reportError(error)) closeDrawer(); }
  });
  const update = useMutation({
    mutationFn: (values: UserFormValues) => usersApi.update(editing!.id, {
      revision: editing!.revision,
      name: values.name,
      enabled: values.enabled,
      quotaBytes: Math.round((values.quotaGB || 0) * 1024 * 1024 * 1024),
      expiresAt: values.expiresAt ? values.expiresAt.endOf('day').toISOString() : undefined,
      resetIntervalDays: values.resetIntervalDays || 0,
      protocolIds: values.protocolIds,
      policyIds: values.policyId ? [values.policyId] : []
    }),
    onSuccess: () => {
      void refresh();
      closeDrawer();
      message.success('用户已更新');
    },
    onError: (error: Error) => { void refresh(); if (reportError(error)) closeDrawer(); }
  });
  const remove = useMutation({
    mutationFn: usersApi.remove,
    onSuccess: () => { void refresh(); message.success('用户已删除'); },
    onError: reportError
  });
  const batchStatus = useMutation({
    mutationFn: (enabled: boolean) => usersApi.batchStatus(selected.map(String), enabled),
    onSuccess: () => { setSelected([]); void refresh(); message.success('用户状态已更新'); },
    onError: reportError
  });
  const resetTraffic = useMutation({
    mutationFn: usersApi.resetTraffic,
    onSuccess: () => { void refresh(); message.success('流量已清零'); },
    onError: reportError
  });
  const adjustQuota = useMutation({
    mutationFn: (values: QuotaFormValues) => usersApi.adjustQuota(trafficUser!.id, {
      mode: 'add',
      bytes: Math.round((values.gb || 0) * 1024 * 1024 * 1024) * (values.mode === 'sub' ? -1 : 1)
    }),
    onSuccess: () => {
      void refresh();
      setTrafficUser(null);
      trafficForm.resetFields();
      message.success('额度已调整');
    },
    onError: reportError
  });

  const resetPaging = () => { setCursor(undefined); setCursorHistory([]); setSelected([]); };
  const openCreate = () => {
    setEditing(null);
    form.setFieldsValue({
      name: '', enabled: true, quotaGB: 0, resetIntervalDays: 0,
      protocolIds: protocols.data?.items.filter((item) => item.enabled).map((item) => item.id) || [],
      policyId: policies.data?.items.find((item) => item.enabled)?.id
    });
    setDrawerOpen(true);
  };
  const openEdit = (value: ResourceUser) => {
    setEditing(value);
    form.setFieldsValue({
      name: value.name,
      enabled: value.enabled,
      quotaGB: Math.round((value.quotaBytes / (1024 * 1024 * 1024)) * 1000) / 1000,
      expiresAt: value.expiresAt ? dayjs(value.expiresAt) : undefined,
      resetIntervalDays: value.resetIntervalDays,
      protocolIds: value.protocolIds,
      policyId: value.policyIds[0]
    });
    setDrawerOpen(true);
  };
  const [copyingUserId, setCopyingUserId] = useState<string | null>(null);

  const handleQuickCopySubscription = async (user: ResourceUser) => {
    setCopyingUserId(user.id);
    try {
      const cached = queryClient.getQueryData<{ subscriptionToken?: string }>(['resources', 'users', user.id, 'devices']);
      let token = cached?.subscriptionToken;
      if (!token) {
        const devices = await usersApi.listDevices(user.id);
        token = devices.subscriptionToken;
      }
      if (token) {
        const url = subscriptionUrl(token, 'clash.yaml');
        await navigator.clipboard.writeText(url);
        message.success(`已复制 ${user.name} 的 Clash 订阅链接`);
      } else {
        message.error('未找到该用户的订阅凭据');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : '复制订阅链接失败';
      message.error(msg);
    } finally {
      setCopyingUserId(null);
    }
  };

  const columns: ColumnsType<ResourceUser> = [
    {
      title: '用户', dataIndex: 'name', width: screens.md ? 180 : 132,
      fixed: screens.md ? 'left' : undefined,
      render: (name: string) => <Typography.Text strong ellipsis={{ tooltip: name }} style={{ maxWidth: 150 }}>{name}</Typography.Text>
    },
    { title: '状态', dataIndex: 'status', width: 96, render: (value: ResourceUserStatus) => <Tag color={statusMeta[value].color}>{statusMeta[value].label}</Tag> },
    {
      title: '已用 / 额度', width: 200, responsive: ['md'],
      render: (_, value) => {
        const isUnlimited = value.quotaBytes <= 0;
        const percent = isUnlimited ? 0 : Math.min(100, Math.round((value.usedBytes / value.quotaBytes) * 100));
        const isOver = !isUnlimited && value.usedBytes > value.quotaBytes;
        const strokeColor = isOver || percent >= 90 ? 'var(--color-error)' : percent >= 70 ? 'var(--color-warning)' : 'var(--primary-color)';
        return (
          <div style={{ minWidth: 140 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, marginBottom: 2 }}>
              <Typography.Text strong={isOver}>{formatBytes(value.usedBytes)}</Typography.Text>
              <Typography.Text type="secondary">/ {formatBytes(value.quotaBytes, true)}</Typography.Text>
            </div>
            {isUnlimited ? (
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>不限额度</Typography.Text>
            ) : (
              <Progress
                percent={percent}
                size="small"
                showInfo={false}
                strokeColor={strokeColor}
                status={isOver ? 'exception' : 'normal'}
                style={{ margin: 0 }}
              />
            )}
            {isOver ? (
              <Typography.Text type="danger" style={{ fontSize: 11 }}>{remainingText(value.quotaBytes, value.usedBytes)}</Typography.Text>
            ) : null}
          </div>
        );
      }
    },
    { title: '到期时间', dataIndex: 'expiresAt', width: 180, responsive: ['lg'], render: (value?: string) => value ? new Date(value).toLocaleString() : '永久' },
    { title: '设备', dataIndex: 'deviceCount', width: 96, responsive: ['lg'], render: (value: number) => `${value} 台` },
    {
      title: '操作', fixed: screens.md ? 'right' : undefined, width: screens.md ? 210 : 160,
      render: (_, value) => (
        <Space size={2}>
          <Tooltip title="一键复制订阅链接"><Button type="text" icon={<CopyOutlined />} loading={copyingUserId === value.id} onClick={() => void handleQuickCopySubscription(value)} /></Tooltip>
          <Tooltip title="编辑"><Button type="text" icon={<EditOutlined />} onClick={() => openEdit(value)} /></Tooltip>
          <Tooltip title="查看流量"><Button type="text" icon={<LineChartOutlined />} onClick={() => navigate(`/traffic?userId=${encodeURIComponent(value.id)}`)} /></Tooltip>
          <Tooltip title="设备与订阅二维码"><Button type="text" icon={<MobileOutlined />} onClick={() => setDevicesUser(value)} /></Tooltip>
          <Tooltip title="调整额度"><Button type="text" icon={<ThunderboltOutlined />} onClick={() => { setTrafficUser(value); trafficForm.resetFields(); trafficForm.setFieldsValue({ mode: 'add' }); }} /></Tooltip>
          <Popconfirm title="确认删除该用户？" onConfirm={() => remove.mutate(value.id)}><Tooltip title="删除"><Button type="text" danger icon={<DeleteOutlined />} /></Tooltip></Popconfirm>
        </Space>
      )
    }
  ];

  if (users.isError) {
    return <Result status={users.error instanceof ApiError && users.error.status === 403 ? '403' : 'error'} title={users.error.message} extra={<Button type="primary" icon={<ReloadOutlined />} onClick={() => void users.refetch()}>重试</Button>} />;
  }

  return (
    <Page title="用户订阅">
      <div className="page-toolbar user-toolbar">
        <Space wrap>
          <Input.Search allowClear placeholder="搜索用户" style={{ width: 220 }} onSearch={(value) => { setSearch(value.trim()); resetPaging(); }} />
          <Select allowClear placeholder="全部状态" style={{ width: 132 }} value={status} onChange={(value) => { setStatus(value); resetPaging(); }} options={Object.entries(statusMeta).map(([value, meta]) => ({ value, label: meta.label }))} />
          <Select value={sort} style={{ width: 148 }} onChange={(value) => { setSort(value); resetPaging(); }} options={[
            { value: 'createdAt:desc', label: '最新创建' }, { value: 'name:asc', label: '名称升序' }, { value: 'usedBytes:desc', label: '用量降序' }
          ]} />
        </Space>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>创建用户</Button>
      </div>
      {selected.length > 0 ? (
        <Space className="selection-toolbar" wrap>
          <Typography.Text type="secondary">已选择 {selected.length} 项</Typography.Text>
          <Button size="small" onClick={() => batchStatus.mutate(true)}>启用</Button>
          <Button size="small" onClick={() => batchStatus.mutate(false)}>停用</Button>
        </Space>
      ) : null}
      <Table<ResourceUser>
        rowKey="id"
        loading={users.isLoading}
        dataSource={users.data?.items || []}
        columns={columns}
        rowSelection={{ selectedRowKeys: selected, onChange: setSelected }}
        pagination={false}
        scroll={{ x: screens.md ? 1000 : 360 }}
      />
      <div className="table-pager">
        <Button disabled={cursorHistory.length === 0} onClick={() => { const history = [...cursorHistory]; setCursor(history.pop()); setCursorHistory(history); }}>上一页</Button>
        <Button disabled={!users.data?.nextCursor} onClick={() => { setCursorHistory((value) => [...value, cursor]); setCursor(users.data?.nextCursor); }}>下一页</Button>
      </div>

      <Drawer title={editing ? '编辑用户' : '创建用户'} width={440} open={drawerOpen} destroyOnClose onClose={closeDrawer} extra={<Button type="primary" loading={create.isPending || update.isPending} onClick={() => form.submit()}>保存</Button>}>
        <Form<UserFormValues> form={form} layout="vertical" onFinish={(values) => editing ? update.mutate(values) : create.mutate(values)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true, message: '请输入用户名称' }]}><Input /></Form.Item>
          <Form.Item name="enabled" label="启用用户" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="protocolIds" label="可用协议" rules={[{ required: true, type: 'array', min: 1, message: '请至少选择一个已启用协议' }]}>
            <Select
              mode="multiple"
              placeholder="选择用户可用的接入协议"
              options={protocols.data?.items.map((item) => ({
                value: item.id,
                label: `${item.name} (${item.type})${item.enabled ? '' : ' - 已停用'}`,
                disabled: !item.enabled && !editing?.protocolIds.includes(item.id)
              }))}
            />
          </Form.Item>
          <Form.Item name="policyId" label="订阅分流策略" extra="写入该用户下载的 Clash 订阅，只影响该用户的客户端，不改变服务器转发。">
            <Select placeholder="默认使用 Clash 默认分流" options={policies.data?.items.map((item) => ({ value: item.id, label: `${item.name}${item.enabled ? '' : ' - 已停用'}`, disabled: !item.enabled && !editing?.policyIds.includes(item.id) }))} />
          </Form.Item>
          <Form.Item name="quotaGB" label="流量额度（GB，0 为不限）" rules={[{ required: true }]}><InputNumber min={0} step={0.5} style={{ width: '100%' }} /></Form.Item>
          <Form.Item name="expiresAt" label="到期时间" extra="留空为永久；到期按当天 23:59:59 生效">
            <DatePicker
              style={{ width: '100%' }}
              allowClear
              placeholder="留空为永久"
              presets={expiresPresets}
              disabledDate={(current) => current.isBefore(dayjs().startOf('day'))}
            />
          </Form.Item>
          <Form.Item
            name="resetIntervalDays"
            label="重置周期（天，0 为不重置）"
            extra={editing?.nextResetAt ? `下次重置：${new Date(editing.nextResetAt).toLocaleString()}（到期自动清零用量）` : undefined}
            rules={[{ required: true }]}
          ><InputNumber min={0} precision={0} style={{ width: '100%' }} /></Form.Item>
        </Form>
      </Drawer>

      <Modal title={`调整 ${trafficUser?.name || ''} 的额度`} open={Boolean(trafficUser)} confirmLoading={adjustQuota.isPending || resetTraffic.isPending} onOk={() => trafficForm.submit()} onCancel={() => setTrafficUser(null)} destroyOnClose>
        {trafficUser ? (
          <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>
            当前额度 <Typography.Text strong>{trafficUser.quotaBytes > 0 ? formatBytes(trafficUser.quotaBytes) : '不限'}</Typography.Text> / 已用 {formatBytes(trafficUser.usedBytes)}
            （{trafficUser.quotaBytes > 0 && trafficUser.usedBytes >= trafficUser.quotaBytes
              ? <Typography.Text type="danger">{remainingText(trafficUser.quotaBytes, trafficUser.usedBytes)}</Typography.Text>
              : remainingText(trafficUser.quotaBytes, trafficUser.usedBytes)}）
          </Typography.Paragraph>
        ) : null}
        <Form<QuotaFormValues> form={trafficForm} layout="vertical" onFinish={(values) => {
          if (values.mode === 'reset') {
            if (trafficUser) resetTraffic.mutate(trafficUser.id);
            setTrafficUser(null);
            return;
          }
          adjustQuota.mutate(values);
        }}>
          <Form.Item name="mode" label="方式" rules={[{ required: true }]}>
            <Select options={[
              { value: 'add', label: '增加额度' },
              { value: 'sub', label: '减少额度' },
              { value: 'reset', label: '重置额度（已用清零，恢复满额）' }
            ]} />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(prev, next) => prev.mode !== next.mode || prev.gb !== next.gb}>
            {({ getFieldValue, getFieldError }) => {
              const mode = getFieldValue('mode') as QuotaFormValues['mode'];
              const gb = Number(getFieldValue('gb')) || 0;
              const hasError = getFieldError('gb').length > 0;
              const deltaBytes = Math.round(gb * 1024 * 1024 * 1024);
              const quota = trafficUser?.quotaBytes || 0;
              const used = trafficUser?.usedBytes || 0;
              if (mode === 'reset') {
                return <Typography.Paragraph type="secondary">
                  重置后：已用 <Typography.Text strong>0 B</Typography.Text>，额度恢复 {quota > 0 ? formatBytes(quota) : '不限'}（可用满额）
                </Typography.Paragraph>;
              }
              const targetQuota = quota + (mode === 'add' ? deltaBytes : -deltaBytes);
              return <>
                <Form.Item
                  name="gb"
                  label="额度（GB）"
                  rules={[
                    { required: true, message: '请输入额度数值' },
                    { validator: (_: unknown, value: number) => {
                        if (value < 0) return Promise.reject(new Error('请输入正数，减少额度请选择「减少」方式'));
                        if (mode === 'sub' && Math.round(value * 1024 * 1024 * 1024) > quota) return Promise.reject(new Error('减少的额度不能超过当前额度'));
                        return Promise.resolve();
                      } }
                  ]}
                >
                  <InputNumber step={0.1} min={0} placeholder="输入额度数值（GB），支持小数" style={{ width: '100%' }} />
                </Form.Item>
                {gb > 0 && !hasError && trafficUser ? (
                  <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
                    调整后额度：<Typography.Text strong>{targetQuota > 0 ? formatBytes(targetQuota) : '不限（0）'}</Typography.Text>
                    （{targetQuota > 0 && used >= targetQuota
                      ? <Typography.Text type="danger">{remainingText(targetQuota, used)}</Typography.Text>
                      : remainingText(targetQuota, used)}）
                  </Typography.Paragraph>
                ) : null}
              </>;
            }}
          </Form.Item>
        </Form>
      </Modal>

      <Modal title="订阅链接" width={620} open={Boolean(issuedToken)} footer={<Button type="primary" onClick={() => { setIssuedToken(null); setQrFormat(undefined); }}>我已保存</Button>} closable={false}>
        {issuedToken ? (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="订阅 Token"><Typography.Text code copyable>{issuedToken}</Typography.Text></Descriptions.Item>
            {subscriptionFormats.map((format) => (
              <Descriptions.Item key={format} label={<Space size={4}>{format}</Space>}>
                <Space direction="vertical" size={4} style={{ width: '100%' }}>
                  <Space size={4} wrap>
                    <Typography.Text copyable={{ text: subscriptionUrl(issuedToken, format) }} ellipsis style={{ maxWidth: 420 }}>
                      {subscriptionUrl(issuedToken, format)}
                    </Typography.Text>
                    <Button type="text" size="small" icon={<QrcodeOutlined />} aria-label={`${format} 二维码`} onClick={() => setQrFormat(qrFormat === format ? undefined : format)} />
                  </Space>
                  {qrFormat === format ? (
                    <Space direction="vertical" align="center" size={4} style={{ width: '100%', padding: '4px 0' }}>
                      <QRCode value={subscriptionUrl(issuedToken, format)} size={150} bordered={false} />
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>用客户端扫码后导入订阅</Typography.Text>
                    </Space>
                  ) : null}
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>{formatHints[format]}</Typography.Text>
                </Space>
              </Descriptions.Item>
            ))}
            <Descriptions.Item label="说明">
              <Typography.Text type="secondary">在各设备的客户端导入这条链接即可；面板会按客户端类型自动识别设备并分别统计流量。</Typography.Text>
            </Descriptions.Item>
          </Descriptions>
        ) : null}
      </Modal>

      <DevicesDrawer key={devicesUser?.id} user={devicesUser} onClose={() => setDevicesUser(null)} />
    </Page>
  );
}
