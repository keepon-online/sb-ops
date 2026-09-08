import { CloudUploadOutlined, DatabaseOutlined, DeleteOutlined, DownloadOutlined, ReloadOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { App as AntApp, Alert, Button, Descriptions, Popconfirm, Space, Spin, Steps, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useRef, useState } from 'react';
import { Page } from '../../../components/Page';
import { ApiError } from '../../../services/http';
import { backupDownload, systemApi, type BackupFile, type UpgradeStatus } from '../../../services/system';
import { formatDateTime } from '../../../util/time';

// 升级流水线的对外阶段 → 展示步骤；checking 瞬时完成不进 Steps。
const activeStepIndex: Partial<Record<UpgradeStatus['phase'], number>> = {
  downloading: 0,
  verifying: 1,
  applying: 2,
  restarting: 3
};

const phaseActive = (phase: UpgradeStatus['phase']) => phase in activeStepIndex;

const describeError = (error: unknown) => (error instanceof ApiError ? `${error.message}（${error.requestId ?? '无请求标识'}）` : String(error));

// 字节格式化（与设备抽屉同款；仓库暂无共享 util，按页局部实现）。
function formatBytes(value: number): string {
  if (value <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const size = value / 1024 ** exponent;
  return `${size.toFixed(size >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

export function SystemPage() {
  return <Page title="系统管理">
    <UpgradeSection />
    <BackupSection />
  </Page>;
}

function UpgradeSection() {
  const { notification } = AntApp.useApp();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ['system', 'upgrade'],
    queryFn: systemApi.upgradeStatus,
    retry: false,
    // 进行中每 2 秒轮询进度；idle/failed 停表（SSE upgrade.* 事件兜底失效）。
    refetchInterval: (query) => (phaseActive(query.state.data?.phase ?? 'idle') ? 2000 : false)
  });

  const check = useMutation({
    mutationFn: systemApi.upgradeCheck,
    onSuccess: (release) => {
      queryClient.setQueryData<UpgradeStatus>(['system', 'upgrade'], (previous) => (previous ? { ...previous, latest: release } : previous));
      if (!release.upgradable) notification.success({ message: '当前已是最新版本' });
    },
    onError: (error) => notification.error({ message: '检查更新失败', description: describeError(error) })
  });

  const start = useMutation({
    mutationFn: systemApi.upgradeStart,
    onSuccess: (snapshot) => {
      queryClient.setQueryData<UpgradeStatus>(['system', 'upgrade'], snapshot);
      notification.success({ message: '升级已开始', description: '下载与替换在后台进行，页面会展示进度。' });
    },
    onError: (error) => notification.error({ message: '启动升级失败', description: describeError(error) })
  });

  const value = status.data;
  const phase = value?.phase ?? 'idle';
  const latest = value?.latest;

  // 进页自动检查一次：后台虽有 6 小时自动检查，但缓存为空（后台检查失败/
  // 刚重启）时页面自己补一次，不再依赖手动点「检查更新」；每次挂载至多一次。
  const autoChecked = useRef(false);
  useEffect(() => {
    if (autoChecked.current || status.isLoading || !value?.configured || latest || phaseActive(phase) || check.isPending) return;
    autoChecked.current = true;
    check.mutate();
  }, [value, latest, phase, status.isLoading, check]);

  // 重启阶段：面板进程即将被替换，先等旧进程下线、再等新进程就绪后整页刷新，
  // 避免对仍存活的老进程反复 reload。
  useEffect(() => {
    if (phase !== 'restarting') return;
    let stopped = false;
    let sawDown = false;
    let timer = 0;
    const probe = async () => {
      try {
        const response = await fetch('/health/live');
        if (response.ok && sawDown) {
          window.location.reload();
          return;
        }
      } catch {
        sawDown = true;
      }
      if (!stopped) timer = window.setTimeout(probe, 3000);
    };
    timer = window.setTimeout(probe, 3000);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [phase]);

  if (status.isLoading) return <Spin style={{ display: 'block', margin: '48px 0' }} />;
  if (status.isError) {
    return <Alert type="error" showIcon message="无法获取升级状态" description={describeError(status.error)}
      action={<Button icon={<ReloadOutlined />} onClick={() => void status.refetch()}>重试</Button>} />;
  }

  const busy = phaseActive(phase);
  return <>
    <div className="page-toolbar">
      <Typography.Text type="secondary">从 GitHub Releases 拉取面板自身的新版本，下载校验通过后自动替换并重启</Typography.Text>
      <Button icon={<ReloadOutlined />} loading={check.isPending} disabled={busy || !value?.configured} onClick={() => check.mutate()}>检查更新</Button>
    </div>

    {!value?.configured && (
      <Alert type="warning" showIcon message="升级源未配置"
        description="在服务器 config.yaml 中添加 updates 段（repo 填 GitHub owner/repo，token 填具备该仓库读取权限的 PAT）并重启面板后即可在此升级。" />
    )}

    <section className="workspace-section">
      <Typography.Title level={4}>版本信息</Typography.Title>
      <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }} items={[
        { key: 'current', label: '当前版本', children: <Typography.Text strong copyable={value?.currentVersion !== 'dev'}>{value?.currentVersion || '—'}</Typography.Text> },
        { key: 'repo', label: '升级源', children: value?.repo ? <Typography.Text copyable>{value.repo}</Typography.Text> : '—' },
        {
          key: 'latest', label: '最新版本',
          children: latest
            ? <Space>{latest.version}{latest.upgradable ? <Tag color="processing">可升级</Tag> : <Tag color="success">已最新</Tag>}</Space>
            : <Typography.Text type="secondary">尚未检查</Typography.Text>
        },
        { key: 'published', label: '发布时间', children: latest?.publishedAt ? <span style={{ fontFamily: "'JetBrains Mono', ui-monospace, monospace" }}>{formatDateTime(latest.publishedAt * 1000)}</span> : '—' }
      ]} />
      {latest?.notes && (
        <Typography.Paragraph type="secondary" style={{ whiteSpace: 'pre-wrap', marginTop: 12 }} ellipsis={{ rows: 6, expandable: true, symbol: '展开完整说明' }}>
          {latest.notes}
        </Typography.Paragraph>
      )}
    </section>

    {busy && (
      <section className="workspace-section">
        <Typography.Title level={4}>升级进度</Typography.Title>
        <Steps size="small" current={activeStepIndex[phase]} items={[
          { title: '下载新版本', icon: <CloudUploadOutlined /> },
          { title: 'sha256 校验', icon: <SafetyCertificateOutlined /> },
          { title: '备份并替换', icon: <CloudUploadOutlined /> },
          { title: '重启面板', icon: <ReloadOutlined /> }
        ]} />
        {phase === 'restarting' && (
          <Alert style={{ marginTop: 12 }} type="info" showIcon message="面板正在重启"
            description="新二进制已就位，进程优雅停机后将自动重生为最新版本，页面会在恢复后自动刷新；若长时间未恢复请登录服务器查看日志。" />
        )}
      </section>
    )}

    {phase === 'failed' && (
      <section className="workspace-section">
        <Alert type="error" showIcon message="上次升级失败" description={value?.error || '未知原因'}
          action={<Button icon={<ReloadOutlined />} onClick={() => check.mutate()} disabled={check.isPending}>重新检查</Button>} />
      </section>
    )}

    <section className="workspace-section">
      <Space>
        <Popconfirm title={`升级到 ${latest?.version ?? '最新版本'}`}
          description="将下载并替换面板二进制，过程中面板会短暂重启，确定继续？"
          okText="开始升级" cancelText="取消" okButtonProps={{ danger: true }}
          disabled={!value?.configured || busy || !latest?.upgradable}
          onConfirm={() => start.mutate()}>
          <Button type="primary" danger loading={start.isPending}
            disabled={!value?.configured || busy || !latest?.upgradable}>
            升级到 {latest?.version ?? '最新版本'}
          </Button>
        </Popconfirm>
        <Typography.Text type="secondary">升级前自动备份当前二进制为 .bak，失败可登录服务器手动换回。</Typography.Text>
      </Space>
    </section>
  </>;
}

function BackupSection() {
  const { notification } = AntApp.useApp();
  const queryClient = useQueryClient();
  const backups = useQuery({ queryKey: ['system', 'backups'], queryFn: systemApi.backupList, retry: false });

  const create = useMutation({
    mutationFn: systemApi.backupCreate,
    onSuccess: (value) => {
      void queryClient.invalidateQueries({ queryKey: ['system', 'backups'] });
      notification.success({ message: '备份完成', description: `${value.name}（${formatBytes(value.sizeBytes)}）已保存到服务器 backups 目录。` });
    },
    onError: (error) => notification.error({ message: '创建备份失败', description: describeError(error) })
  });

  const remove = useMutation({
    mutationFn: systemApi.backupDelete,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['system', 'backups'] });
      notification.success({ message: '备份已删除' });
    },
    onError: (error) => notification.error({ message: '删除备份失败', description: describeError(error) })
  });

  // 下载不是 react-query mutation（返回 blob 而非信封），用行级 state 标记 loading。
  const [downloading, setDownloading] = useState<string>();
  const handleDownload = async (name: string) => {
    setDownloading(name);
    try {
      await backupDownload(name);
    } catch (error) {
      notification.error({ message: '下载备份失败', description: describeError(error) });
    } finally {
      setDownloading(undefined);
    }
  };

  const columns: ColumnsType<BackupFile> = [
    { title: '文件名', dataIndex: 'name', ellipsis: true, render: (value: string) => <Typography.Text code style={{ fontSize: 12 }}>{value}</Typography.Text> },
    { title: '大小', dataIndex: 'sizeBytes', width: 100, render: (value: number) => formatBytes(value) },
    {
      title: '创建时间', dataIndex: 'createdAt', width: 190,
      render: (value: number) => (
        <span style={{ fontFamily: "'JetBrains Mono', ui-monospace, monospace", fontSize: 13, whiteSpace: 'nowrap' }}>
          {formatDateTime(value * 1000)}
        </span>
      )
    },
    {
      title: '操作', width: 110, render: (_, row) => (
        <Space size={2}>
          <Tooltip title="下载到本地">
            <Button type="text" size="small" aria-label={`下载备份 ${row.name}`} icon={<DownloadOutlined />} loading={downloading === row.name} onClick={() => void handleDownload(row.name)} />
          </Tooltip>
          <Popconfirm title="确认删除该备份？" okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={() => remove.mutate(row.name)}>
            <Tooltip title="删除"><Button type="text" danger size="small" aria-label={`删除备份 ${row.name}`} icon={<DeleteOutlined />} /></Tooltip>
          </Popconfirm>
        </Space>
      )
    }
  ];

  return <section className="workspace-section">
    <Typography.Title level={4}>数据备份</Typography.Title>
    <div className="page-toolbar" style={{ marginBottom: 12 }}>
      <Typography.Text type="secondary">基于 VACUUM INTO 的在线快照（WAL 安全，无需停机）；备份含凭据主密钥与订阅 token，请妥善保管</Typography.Text>
      <Button type="primary" icon={<DatabaseOutlined />} loading={create.isPending} onClick={() => create.mutate()}>立即备份</Button>
    </div>
    {backups.isError && (
      <Alert style={{ marginBottom: 12 }} type="error" showIcon message="无法获取备份列表" description={describeError(backups.error)}
        action={<Button size="small" icon={<ReloadOutlined />} onClick={() => void backups.refetch()}>重试</Button>} />
    )}
    <Table rowKey="name" size="small" pagination={false} loading={backups.isLoading}
      dataSource={backups.data?.items || []} columns={columns} locale={{ emptyText: '暂无备份' }} scroll={{ x: 540 }} />
    <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12, marginBottom: 0 }}>
      每日 04:00 自动备份一次（除手动外，面板持续运行时每日固定留一份）；自动保留最近 5 份，超出裁剪最旧；恢复时停机将备份文件替换 data 目录下的 singbox-web.db（并删除同名 -wal/-shm 文件）后重启面板。
    </Typography.Paragraph>
  </section>;
}
