import { AndroidOutlined, AppleOutlined, BarChartOutlined, DeleteOutlined, DesktopOutlined, EyeInvisibleOutlined, EyeOutlined, KeyOutlined, LinkOutlined, PlusOutlined, QrcodeOutlined, ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Descriptions, Drawer, Empty, Grid, Input, Modal, Popconfirm, Popover, Progress, QRCode, Space, Switch, Table, Tag, Tooltip, Typography, type TableColumnsType } from 'antd';
import { useState } from 'react';
import { ApiError } from '../../../services/http';
import { queryClient } from '../../../services/queryClient';
import { subscriptionUrl, usersApi, type IssuedCredential, type ResourceDevice, type ResourceUser } from '../../../services/users';
import { formatDateTime } from '../../../util/time';

const subscriptionFormats = ['clash.yaml'];

const platformLabels: Record<string, string> = {
  windows: 'Windows', macos: 'macOS', linux: 'Linux', android: 'Android', ios: 'iOS', unknown: '未知平台'
};

export function maskIp(ip?: string): string {
  if (!ip) return '';
  const trimmed = ip.trim();
  if (trimmed.includes('.')) {
    return trimmed.replace(/\b(\d{1,3}\.\d{1,3}\.)\d{1,3}\.\d{1,3}\b/g, '$1***.***');
  }
  if (trimmed.includes(':')) {
    const parts = trimmed.split(':');
    return parts.length > 2 ? `${parts[0]}:${parts[1]}:****:****` : '****:****';
  }
  return trimmed;
}

export function maskDeviceName(name: string, masked: boolean): string {
  if (!masked || !name) return name;
  return name.replace(/\b(\d{1,3}\.\d{1,3}\.)\d{1,3}\.\d{1,3}\b/g, '$1***.***');
}

export function maskHost(host: string): string {
  if (!host) return '';
  if (/^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host)) {
    return maskIp(host);
  }
  if (host.includes(':')) {
    return maskIp(host);
  }
  const parts = host.split('.');
  if (parts.length > 2) {
    return `${parts[0]}.***.***`;
  }
  if (parts.length === 2) {
    return `***.${parts[1]}`;
  }
  return '***';
}

export function maskSubscriptionUrl(url: string, masked: boolean): string {
  if (!masked || !url) return url;
  try {
    const parsed = new URL(url);
    const maskedHostname = maskHost(parsed.hostname);
    const port = parsed.port ? `:${parsed.port}` : '';
    const pathParts = parsed.pathname.split('/');
    if (pathParts.length >= 4 && pathParts[1] === 'subscribe') {
      const format = pathParts.slice(3).join('/');
      return `${parsed.protocol}//${maskedHostname}${port}/subscribe/****************/${format}`;
    }
    return `${parsed.protocol}//${maskedHostname}${port}${parsed.pathname.replace(/\/subscribe\/[^/]+/, '/subscribe/****************')}`;
  } catch {
    return url.replace(/^(https?:\/\/)[^/]+(\/subscribe\/)[^/]+/, '$1***.***$2****************');
  }
}

function platformIcon(platform: string) {
  if (platform === 'android' || platform === 'ios') return platform === 'android' ? <AndroidOutlined /> : <AppleOutlined />;
  return <DesktopOutlined />;
}

function formatClientVersion(value?: string) {
  const version = value?.trim().replace(/^v+/i, '');
  return version ? `v${version}` : '';
}

function formatBytes(value: number) {
  if (value === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

// DevicesDrawer 管理一个用户的全部接入设备：每台设备一份独立凭据与专属直链，
// 流量按设备归因、额度按用户汇总。多设备时以紧凑表格展示，支持排序与搜索。
export function DevicesDrawer({ user, onClose }: { user: ResourceUser | null; onClose: () => void }) {
  const { message, notification } = AntApp.useApp();
  const screens = Grid.useBreakpoint();
  const [qrDevice, setQrDevice] = useState<string>();
  const [credential, setCredential] = useState<IssuedCredential | null>(null);
  const [maskIpEnabled, setMaskIpEnabled] = useState(true);
  const [addingDevice, setAddingDevice] = useState(false);
  const [newDeviceName, setNewDeviceName] = useState('');
  const [searchQuery, setSearchQuery] = useState('');

  const devices = useQuery({
    queryKey: ['resources', 'users', user?.id, 'devices'],
    queryFn: () => usersApi.listDevices(user!.id),
    enabled: Boolean(user),
    retry: false
  });
  const refresh = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ['resources', 'users', user!.id, 'devices'] }),
    queryClient.invalidateQueries({ queryKey: ['resources', 'users'] })
  ]);
  const reportError = (error: Error) => {
    notification.error({
      message: error.message,
      description: error instanceof ApiError && error.requestId ? `请求 ID：${error.requestId}` : undefined
    });
  };
  const afterWrite = () => { void refresh(); };

  const create = useMutation({
    mutationFn: (name: string) => usersApi.createDevice(user!.id, name),
    onSuccess: (result) => {
      afterWrite();
      setAddingDevice(false);
      setNewDeviceName('');
      setCredential(result.credential);
      message.success('设备添加成功');
    },
    onError: reportError
  });
  const toggle = useMutation({
    mutationFn: (device: ResourceDevice) => usersApi.updateDevice(user!.id, device.id, { enabled: !device.enabled }),
    onSuccess: () => { afterWrite(); message.success('设备状态已更新'); },
    onError: reportError
  });
  const rename = useMutation({
    mutationFn: ({ device, name }: { device: ResourceDevice; name: string }) => usersApi.updateDevice(user!.id, device.id, { name }),
    onSuccess: () => { afterWrite(); message.success('设备名称已更新'); },
    onError: reportError
  });
  const rotate = useMutation({
    mutationFn: (device: ResourceDevice) => usersApi.rotateDevice(user!.id, device.id),
    onSuccess: (result) => { afterWrite(); setCredential(result.credential); },
    onError: reportError
  });
  const remove = useMutation({
    mutationFn: (device: ResourceDevice) => usersApi.deleteDevice(user!.id, device.id),
    onSuccess: () => { afterWrite(); message.success('设备已删除'); },
    onError: reportError
  });
  const rotateSubscription = useMutation({
    mutationFn: () => usersApi.rotateSubscription(user!.id),
    onSuccess: () => { afterWrite(); message.success('订阅链接已重置，旧链接立即失效'); },
    onError: reportError
  });

  if (!user) return null;

  const rawItems = devices.data?.items || [];
  const totalUsed = rawItems.reduce((sum, d) => sum + (d.usedBytes || 0), 0);
  const normalizedQuery = searchQuery.trim().toLowerCase();
  const displayItems = normalizedQuery
    ? rawItems.filter((d) =>
        d.name.toLowerCase().includes(normalizedQuery) ||
        (d.client && d.client.toLowerCase().includes(normalizedQuery)) ||
        (d.sourceIp && d.sourceIp.includes(normalizedQuery)) ||
        (d.platform && d.platform.toLowerCase().includes(normalizedQuery))
      )
    : rawItems;

  const columns: TableColumnsType<ResourceDevice> = [
    {
      title: '设备 / 客户端',
      key: 'device',
      render: (_, record) => (
        <div style={{ opacity: record.enabled ? 1 : 0.55 }}>
          <Space size={6} align="center" wrap>
            {platformIcon(record.platform || 'unknown')}
            <Typography.Text
              strong
              editable={{
                text: record.name,
                tooltip: '点击重命名设备',
                onChange: (val) => {
                  const trimmed = val.trim();
                  if (trimmed && trimmed !== record.name) {
                    rename.mutate({ device: record, name: trimmed });
                  }
                }
              }}
            >
              {maskDeviceName(record.name, maskIpEnabled)}
            </Typography.Text>
            {record.auto ? (
              <Tooltip title="由订阅链接按客户端类型自动创建，流量自动归入此设备">
                <Tag color="processing" bordered={false} style={{ marginInlineEnd: 0, fontSize: 11, lineHeight: '18px', padding: '0 4px' }}>
                  自动发现
                </Tag>
              </Tooltip>
            ) : (
              <Tag color="warning" bordered={false} style={{ marginInlineEnd: 0, fontSize: 11, lineHeight: '18px', padding: '0 4px' }}>
                手动添加
              </Tag>
            )}
            {!record.enabled ? (
              <Tag style={{ marginInlineEnd: 0, fontSize: 11, lineHeight: '18px', padding: '0 4px' }}>
                已停用
              </Tag>
            ) : null}
          </Space>
          <div style={{ marginTop: 3 }}>
            <Space size={4} wrap>
              <Tag style={{ fontSize: 11, padding: '0 4px', lineHeight: '18px', marginInlineEnd: 0 }}>
                {platformLabels[record.platform] || '未知平台'}
              </Tag>
              {record.client ? (
                <Tag color="processing" style={{ fontSize: 11, padding: '0 4px', lineHeight: '18px', marginInlineEnd: 0 }}>
                  {record.client}{formatClientVersion(record.clientVersion) ? ` ${formatClientVersion(record.clientVersion)}` : ''}
                </Tag>
              ) : null}
            </Space>
          </div>
        </div>
      )
    },
    {
      title: '流量用量',
      key: 'usage',
      width: 155,
      sorter: (a, b) => (a.usedBytes || 0) - (b.usedBytes || 0),
      render: (_, record) => {
        const percent = totalUsed > 0 ? ((record.usedBytes || 0) / totalUsed) * 100 : 0;
        const percentText = totalUsed > 0 ? (percent >= 1 ? `${percent.toFixed(0)}%` : '<1%') : '0%';
        return (
          <div style={{ opacity: record.enabled ? 1 : 0.55 }}>
            <Tooltip title="已用为本额度周期计数（周期到点清零）；占比为该设备占本周期全部用量的份额，非占配额比例">
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 2 }}>
                <Typography.Text style={{ fontSize: 12 }}>已用 {formatBytes(record.usedBytes || 0)}</Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 11 }}>周期占比 {percentText}</Typography.Text>
              </div>
            </Tooltip>
            <Progress
              percent={percent}
              size="small"
              showInfo={false}
              strokeColor="var(--primary-color)"
              style={{ margin: 0 }}
            />
            {record.lifetimeBytes != null && record.lifetimeBytes > 0 ? (
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                历史 {formatBytes(record.lifetimeBytes)}
              </Typography.Text>
            ) : null}
          </div>
        );
      }
    },
    {
      title: '网络 / 活跃',
      key: 'network',
      width: 165,
      sorter: (a, b) => {
        const timeA = a.lastSeenAt ? new Date(a.lastSeenAt).getTime() : 0;
        const timeB = b.lastSeenAt ? new Date(b.lastSeenAt).getTime() : 0;
        return timeA - timeB;
      },
      render: (_, record) => (
        <div style={{ opacity: record.enabled ? 1 : 0.55 }}>
          {record.sourceIp ? (
            <Tooltip title={maskIpEnabled ? '点击查看完整 IP 地址' : '点击脱敏隐藏 IP 地址'}>
              <Tag
                style={{ cursor: 'pointer', userSelect: 'none', marginInlineEnd: 0, fontSize: 11, padding: '0 4px', lineHeight: '18px' }}
                onClick={() => setMaskIpEnabled((prev) => !prev)}
              >
                IP {maskIpEnabled ? maskIp(record.sourceIp) : record.sourceIp}
              </Tag>
            </Tooltip>
          ) : (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>无 IP 记录</Typography.Text>
          )}
          <div style={{ fontSize: 11, color: 'var(--text-secondary, #8c8c8c)', marginTop: 2 }}>
            {record.lastSeenAt ? (
              <Tooltip title={`轮换于 ${formatDateTime(record.rotatedAt)}`}>
                <span>最近活跃 <span style={{ fontFamily: "'JetBrains Mono', ui-monospace, monospace" }}>{formatDateTime(record.lastSeenAt)}</span></span>
              </Tooltip>
            ) : (
              <Tooltip title={`轮换于 ${formatDateTime(record.rotatedAt)}`}>
                <span>尚未识别</span>
              </Tooltip>
            )}
          </div>
        </div>
      )
    },
    {
      title: '专属订阅直链',
      key: 'subscription',
      width: 195,
      render: (_, record) => (
        <Space direction="vertical" size={2} style={{ width: '100%', opacity: record.enabled ? 1 : 0.55 }}>
          {subscriptionFormats.map((format) => {
            const rawDevUrl = subscriptionUrl(record.token || '', format, devices.data?.subscriptionBaseUrl);
            const displayDevUrl = maskSubscriptionUrl(rawDevUrl, maskIpEnabled);
            const qrKey = `device-${record.id}-${format}`;
            const isQrOpen = qrDevice === qrKey;
            return (
              <div key={format} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                <Typography.Text copyable={{ text: rawDevUrl }} ellipsis style={{ maxWidth: 150, fontSize: 12 }}>
                  {displayDevUrl}
                </Typography.Text>
                <Popover
                  content={
                    <Space direction="vertical" align="center" size={4} style={{ padding: 4 }}>
                      <QRCode value={maskIpEnabled ? maskSubscriptionUrl(rawDevUrl, true) : rawDevUrl} size={130} bordered={false} />
                      <Typography.Text type="secondary" style={{ fontSize: 12, maxWidth: 160, textAlign: 'center' }}>
                        {maskIpEnabled ? '当前为脱敏预览，点击“显示 IP”后二维码才可导入' : '用客户端扫码后导入此设备专属订阅'}
                      </Typography.Text>
                    </Space>
                  }
                  trigger="click"
                  open={isQrOpen}
                  onOpenChange={(visible) => setQrDevice(visible ? qrKey : undefined)}
                >
                  <Button
                    type="text"
                    size="small"
                    icon={<QrcodeOutlined />}
                    aria-label={`设备 ${record.name} ${format} 二维码`}
                  />
                </Popover>
              </div>
            );
          })}
        </Space>
      )
    },
    {
      title: '操作',
      key: 'actions',
      width: 105,
      align: 'right',
      render: (_, record) => (
        <Space size={2}>
          <Tooltip title={record.enabled ? '停用设备' : '启用设备'}>
            <Switch
              size="small"
              checked={record.enabled}
              loading={toggle.isPending && toggle.variables?.id === record.id}
              onChange={() => toggle.mutate(record)}
            />
          </Tooltip>
          <Popconfirm title="轮换后该设备旧凭据立即失效，继续？" onConfirm={() => rotate.mutate(record)}>
            <Tooltip title="轮换凭据">
              <Button type="text" size="small" icon={<KeyOutlined />} aria-label={`轮换 ${record.name} 凭据`} />
            </Tooltip>
          </Popconfirm>
          <Popconfirm title="删除后该设备订阅立即失效，继续？" onConfirm={() => remove.mutate(record)}>
            <Tooltip title="删除设备">
              <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label={`删除 ${record.name}`} />
            </Tooltip>
          </Popconfirm>
        </Space>
      )
    }
  ];

  return <>
    <Modal
      title="订阅凭据"
      width={620}
      open={Boolean(credential)}
      destroyOnHidden
      footer={<Button type="primary" onClick={() => setCredential(null)}>我已保存</Button>}
      closable={false}
    >
      {credential ? (
        <>
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="订阅 Token"><Typography.Text code copyable>{credential.token}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="UUID"><Typography.Text code copyable>{credential.uuid}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="密码"><Typography.Text code copyable>{credential.password}</Typography.Text></Descriptions.Item>
          </Descriptions>
          <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            设备专属凭据已签发。请在下方设备列表复制该设备的专属订阅直链导入客户端；上方通用链接无法归入此设备。
          </Typography.Text>
        </>
      ) : null}
    </Modal>
    <Modal
      title="添加设备"
      width={480}
      open={addingDevice}
      destroyOnHidden
      onCancel={() => setAddingDevice(false)}
      onOk={() => {
        const trimmed = newDeviceName.trim();
        if (!trimmed) {
          message.error('请输入设备名称');
          return;
        }
        create.mutate(trimmed);
      }}
      confirmLoading={create.isPending}
      okText="添加并签发凭据"
      cancelText="取消"
    >
      <div style={{ marginTop: 8 }}>
        <Typography.Text type="secondary" style={{ fontSize: 13, display: 'block', marginBottom: 12 }}>
          手动添加独立设备行。用于多台同款客户端（如多台 Clash Verge）无法通过自动发现拆分的场景。添加后请复制该设备的专属直链导入客户端。
        </Typography.Text>
        <Input
          placeholder="例如：书房 · Verge、电信本"
          value={newDeviceName}
          maxLength={64}
          autoFocus
          onChange={(e) => setNewDeviceName(e.target.value)}
          onPressEnter={() => {
            const trimmed = newDeviceName.trim();
            if (trimmed) create.mutate(trimmed);
          }}
        />
      </div>
    </Modal>
    <Drawer
      title={`${user.name} 的设备`}
      width={screens.md ? 840 : '100%'}
      open
      destroyOnClose
      onClose={onClose}
      extra={<Space size={8}>
        <Button
          size="small"
          type="primary"
          icon={<PlusOutlined />}
          disabled={Boolean(devices.data && devices.data.items.length >= devices.data.maxDevices)}
          onClick={() => { setNewDeviceName(''); setAddingDevice(true); }}
        >
          添加设备
        </Button>
        <Tooltip title={maskIpEnabled ? '点击查看完整 IP' : '点击脱敏隐藏 IP'}>
          <Button
            size="small"
            icon={maskIpEnabled ? <EyeOutlined /> : <EyeInvisibleOutlined />}
            onClick={() => setMaskIpEnabled((prev) => !prev)}
            aria-label={maskIpEnabled ? '显示完整 IP' : '隐藏 IP'}
          >
            {maskIpEnabled ? '显示 IP' : '隐藏 IP'}
          </Button>
        </Tooltip>
        <Popconfirm title="重置后旧订阅链接立即失效（设备凭据不受影响），继续？" onConfirm={() => rotateSubscription.mutate()}>
          <Button size="small" icon={<ReloadOutlined />} loading={rotateSubscription.isPending}>重置链接</Button>
        </Popconfirm>
      </Space>}
    >
      <div className="gauge-cell" style={{ alignItems: 'stretch', textAlign: 'left', padding: '12px 16px', marginBottom: 16 }}>
        <div className="page-toolbar">
          <Space size={4} wrap>
            <LinkOutlined />
            <Typography.Text strong>用户订阅链接</Typography.Text>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>导入后按客户端与来源网络自动识别，最多 {devices.data?.maxDevices ?? 10} 台</Typography.Text>
        </div>
        {subscriptionFormats.map((format) => {
          const rawUrl = subscriptionUrl(devices.data?.subscriptionToken || '', format, devices.data?.subscriptionBaseUrl);
          const displayUrl = maskSubscriptionUrl(rawUrl, maskIpEnabled);
          return (
            <div key={format}>
              <Space size={4} wrap>
                <Typography.Text copyable={{ text: rawUrl }} ellipsis style={{ maxWidth: 420 }}>
                  {displayUrl}
                </Typography.Text>
                <Popover
                  content={
                    <Space direction="vertical" align="center" size={4} style={{ padding: 4 }}>
                      <QRCode value={maskIpEnabled ? maskSubscriptionUrl(rawUrl, true) : rawUrl} size={150} bordered={false} />
                      <Typography.Text type="secondary" style={{ fontSize: 12, maxWidth: 180, textAlign: 'center' }}>
                        {maskIpEnabled ? '当前为脱敏预览，点击“显示 IP”后二维码才可导入' : '用客户端扫码后导入订阅'}
                      </Typography.Text>
                    </Space>
                  }
                  trigger="click"
                  open={qrDevice === 'user-link'}
                  onOpenChange={(visible) => setQrDevice(visible ? 'user-link' : undefined)}
                >
                  <Button type="text" size="small" icon={<QrcodeOutlined />} aria-label={`用户链接 ${format} 二维码`} />
                </Popover>
              </Space>
            </div>
          );
        })}
      </div>
      {devices.isLoading ? <Typography.Text type="secondary">加载中…</Typography.Text> : null}
      {devices.isError ? <Typography.Text type="danger">{(devices.error as Error).message}</Typography.Text> : null}
      {devices.data && devices.data.items.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有设备——在客户端导入上方订阅链接后会自动出现" />
      ) : null}
      {devices.data && devices.data.items.length >= devices.data.maxDevices ? (
        <Typography.Text type="warning" style={{ display: 'block', margin: '8px 0' }}>
          已达到每用户最多 {devices.data.maxDevices} 台设备；新的客户端会回退到已有设备。
        </Typography.Text>
      ) : null}
      {devices.data && devices.data.items.length > 0 ? (
        <>
          <div className="page-toolbar" style={{ marginBottom: 10, padding: '0 2px' }}>
            <Space size={8}>
              <Space size={6}>
                <BarChartOutlined />
                <Typography.Text strong>设备流量统计</Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  ({devices.data.items.length} / {devices.data.maxDevices} 台)
                </Typography.Text>
              </Space>
              {rawItems.length > 3 ? (
                <Input
                  placeholder="搜索设备..."
                  size="small"
                  allowClear
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  style={{ width: 140 }}
                />
              ) : null}
            </Space>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              本周期已用 {formatBytes(totalUsed)}
              {devices.data?.userLifetimeBytes != null && devices.data.userLifetimeBytes > 0
                ? ` · 历史累计 ${formatBytes(devices.data.userLifetimeBytes)}`
                : ''}
            </Typography.Text>
          </div>
          <Table<ResourceDevice>
            rowKey="id"
            size="small"
            pagination={false}
            scroll={{ x: 760 }}
            dataSource={displayItems}
            columns={columns}
          />
        </>
      ) : null}
    </Drawer>
  </>;
}
