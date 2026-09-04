import { AndroidOutlined, AppleOutlined, BarChartOutlined, DeleteOutlined, DesktopOutlined, KeyOutlined, LinkOutlined, QrcodeOutlined, ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Descriptions, Drawer, Empty, Modal, Popconfirm, Progress, QRCode, Space, Switch, Tag, Tooltip, Typography } from 'antd';
import { useState } from 'react';
import { ApiError } from '../../../services/http';
import { queryClient } from '../../../services/queryClient';
import { subscriptionUrl, usersApi, type IssuedCredential, type ResourceDevice, type ResourceUser } from '../../../services/users';

const subscriptionFormats = ['clash.yaml'];

const platformLabels: Record<string, string> = {
  windows: 'Windows', macos: 'macOS', linux: 'Linux', android: 'Android', ios: 'iOS', unknown: '未知平台'
};

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

// DeviceUsageSummary 汇总一台台设备的累计流量占用户总量的比例：按用量降序
// 排列 + 主题色比例条，停用设备淡出。全部为 0 时提示暂无记录。
function DeviceUsageSummary({ devices }: { devices: { id: string; name: string; usedBytes: number; enabled: boolean; platform?: string; client?: string }[] }) {
  if (devices.length === 0) return null;
  const total = devices.reduce((sum, device) => sum + (device.usedBytes || 0), 0);
  const ranked = [...devices].sort((a, b) => (b.usedBytes || 0) - (a.usedBytes || 0));
  return <div className="gauge-cell" style={{ alignItems: 'stretch', textAlign: 'left', padding: '12px 16px' }}>
    <div className="page-toolbar">
      <Space size={6}><BarChartOutlined /><Typography.Text strong>设备流量统计</Typography.Text></Space>
      <Typography.Text type="secondary">用户累计 {formatBytes(total)}</Typography.Text>
    </div>
    {total === 0 ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>暂无流量记录</Typography.Text> : (
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        {ranked.map((device) => {
          const percent = total > 0 ? (device.usedBytes / total) * 100 : 0;
          return <div key={device.id} style={{ opacity: device.enabled ? 1 : 0.45 }}>
            <div className="page-toolbar" style={{ marginBottom: 2 }}>
              <Space size={6}>
                {platformIcon(device.platform || 'unknown')}
                <Typography.Text strong>{device.name}</Typography.Text>
                {device.client ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>{device.client}</Typography.Text> : null}
              </Space>
              <Space size={8}>
                <Typography.Text style={{ fontSize: 12 }}>{formatBytes(device.usedBytes || 0)}</Typography.Text>
                <Typography.Text type="secondary" style={{ fontSize: 12, minWidth: 32, textAlign: 'right' }}>{percent >= 1 ? `${percent.toFixed(0)}%` : '<1%'}</Typography.Text>
              </Space>
            </div>
            <Progress percent={percent} showInfo={false} size="small" strokeColor="var(--primary-color)" />
          </div>;
        })}
      </Space>
    )}
  </div>;
}

// DevicesDrawer 管理一个用户的全部接入设备：每台设备一份独立凭据与订阅链接，
// 流量按设备归因、额度按用户汇总。
export function DevicesDrawer({ user, onClose }: { user: ResourceUser | null; onClose: () => void }) {
  const { message, notification } = AntApp.useApp();
  const [qrDevice, setQrDevice] = useState<string>();
  const [credential, setCredential] = useState<IssuedCredential | null>(null);

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
  const afterWrite = () => { void refresh(); }

  const toggle = useMutation({
    mutationFn: (device: ResourceDevice) => usersApi.updateDevice(user!.id, device.id, !device.enabled),
    onSuccess: () => { afterWrite(); message.success('设备状态已更新'); },
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

  return <>
    <Modal
      title="订阅凭据"
      width={620}
      open={Boolean(credential)}
      footer={<Button type="primary" onClick={() => setCredential(null)}>我已保存</Button>}
      closable={false}
    >
      {credential ? (
        <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="订阅 Token"><Typography.Text code copyable>{credential.token}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="UUID"><Typography.Text code copyable>{credential.uuid}</Typography.Text></Descriptions.Item>
          <Descriptions.Item label="密码"><Typography.Text code copyable>{credential.password}</Typography.Text></Descriptions.Item>
          <Typography.Text type="secondary">设备凭据已更新。请继续使用上方用户级订阅链接，客户端会按来源自动归入设备。</Typography.Text>
        </Descriptions>
      ) : null}
    </Modal>
    <Drawer
      title={`${user.name} 的设备`}
      width={520}
      open
      destroyOnClose
      onClose={onClose}
      extra={<Popconfirm title="重置后旧订阅链接立即失效（设备凭据不受影响），继续？" onConfirm={() => rotateSubscription.mutate()}>
        <Button size="small" icon={<ReloadOutlined />} loading={rotateSubscription.isPending}>重置链接</Button>
      </Popconfirm>}
    >
      <div className="gauge-cell" style={{ alignItems: 'stretch', textAlign: 'left', padding: '12px 16px' }}>
        <div className="page-toolbar">
          <Space size={4} wrap>
            <LinkOutlined />
            <Typography.Text strong>用户订阅链接</Typography.Text>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>导入后按客户端与来源网络自动识别，最多 {devices.data?.maxDevices ?? 10} 台</Typography.Text>
        </div>
        {subscriptionFormats.map((format) => (
          <div key={format}>
            <Space size={4} wrap>
              <Typography.Text copyable={{ text: subscriptionUrl(devices.data?.subscriptionToken || '', format) }} ellipsis style={{ maxWidth: 360 }}>
                {subscriptionUrl(devices.data?.subscriptionToken || '', format)}
              </Typography.Text>
              <Button type="text" size="small" icon={<QrcodeOutlined />} aria-label={`用户链接 ${format} 二维码`} onClick={() => setQrDevice((current) => current === 'user-link' ? undefined : 'user-link')} />
            </Space>
            {qrDevice === 'user-link' ? (
              <Space direction="vertical" align="center" size={4} style={{ width: '100%', padding: '8px 0' }}>
                <QRCode value={subscriptionUrl(devices.data?.subscriptionToken || '', format)} size={150} bordered={false} />
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>用客户端扫码后导入订阅</Typography.Text>
              </Space>
            ) : null}
          </div>
        ))}
      </div>
      {devices.isLoading ? <Typography.Text type="secondary">加载中…</Typography.Text> : null}
      {devices.isError ? <Typography.Text type="danger">{(devices.error as Error).message}</Typography.Text> : null}
      {devices.data && devices.data.items.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有设备——在客户端导入上方订阅链接后会自动出现" />
      ) : null}
      {devices.data ? <DeviceUsageSummary devices={devices.data.items} /> : null}
      {devices.data && devices.data.items.length >= devices.data.maxDevices ? <Typography.Text type="warning" style={{ display: 'block', margin: '8px 0' }}>已达到每用户最多 {devices.data.maxDevices} 台设备；新的客户端会回退到已有设备。</Typography.Text> : null}
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        {(devices.data?.items || []).map((device) => (
          <div key={device.id} className="gauge-cell" style={{ alignItems: 'stretch', textAlign: 'left', padding: '12px 16px', gap: 8 }}>
            <div className="page-toolbar">
              <Space size={8} wrap>
                {platformIcon(device.platform || 'unknown')}
                <Typography.Text strong>{device.name}</Typography.Text>
                <Tag>{platformLabels[device.platform] || '未知平台'}</Tag>
                {device.client ? <Tag color="processing">{device.client}{formatClientVersion(device.clientVersion) ? ` ${formatClientVersion(device.clientVersion)}` : ''}</Tag> : null}
                {device.enabled ? <Tag color="success">启用</Tag> : <Tag>已停用</Tag>}
                {device.auto ? <Tooltip title="由订阅链接按客户端类型自动创建，流量自动归入此设备"><Tag color="processing" bordered={false}>自动发现</Tag></Tooltip> : null}
                {device.sourceIp ? <Tooltip title="最近一次订阅请求的来源地址"><Tag>IP {device.sourceIp}</Tag></Tooltip> : null}
                <Typography.Text type="secondary">已用 {formatBytes(device.usedBytes)}</Typography.Text>
                {device.lastSeenAt ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>最近活跃 {new Date(device.lastSeenAt).toLocaleString()}</Typography.Text> : <Typography.Text type="secondary" style={{ fontSize: 12 }}>尚未识别</Typography.Text>}
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>轮换于 {new Date(device.rotatedAt).toLocaleString()}</Typography.Text>
              </Space>
              <Space size={2}>
                <Tooltip title={device.enabled ? '停用设备' : '启用设备'}>
                  <Switch size="small" checked={device.enabled} loading={toggle.isPending && toggle.variables?.id === device.id} onChange={() => toggle.mutate(device)} />
                </Tooltip>
                <Popconfirm title="轮换后该设备旧凭据立即失效，继续？" onConfirm={() => rotate.mutate(device)}>
                  <Button type="text" size="small" icon={<KeyOutlined />} aria-label={`轮换 ${device.name} 凭据`} />
                </Popconfirm>
                <Popconfirm title="删除后该设备订阅立即失效，继续？" onConfirm={() => remove.mutate(device)}>
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} aria-label={`删除 ${device.name}`} />
                </Popconfirm>
              </Space>
            </div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              设备凭据由用户订阅链接自动匹配，设备本身不再单独展示订阅链接。
            </Typography.Text>
          </div>
        ))}
      </Space>
    </Drawer>
  </>;
}
