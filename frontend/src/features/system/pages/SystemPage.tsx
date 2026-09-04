import { CloudUploadOutlined, ReloadOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { App as AntApp, Alert, Button, Descriptions, Popconfirm, Space, Spin, Steps, Tag, Typography } from 'antd';
import { useEffect, useRef } from 'react';
import { Page } from '../../../components/Page';
import { ApiError } from '../../../services/http';
import { systemApi, type UpgradeStatus } from '../../../services/system';

// 升级流水线的对外阶段 → 展示步骤；checking 瞬时完成不进 Steps。
const activeStepIndex: Partial<Record<UpgradeStatus['phase'], number>> = {
  downloading: 0,
  verifying: 1,
  applying: 2,
  restarting: 3
};

const phaseActive = (phase: UpgradeStatus['phase']) => phase in activeStepIndex;

export function SystemPage() {
  const { notification } = AntApp.useApp();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ['system', 'upgrade'],
    queryFn: systemApi.upgradeStatus,
    retry: false,
    // 进行中每 2 秒轮询进度；idle/failed 停表（SSE upgrade.* 事件兜底失效）。
    refetchInterval: (query) => (phaseActive(query.state.data?.phase ?? 'idle') ? 2000 : false)
  });

  const describeError = (error: unknown) => (error instanceof ApiError ? `${error.message}（${error.requestId ?? '无请求标识'}）` : String(error));

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

  if (status.isLoading) return <Page title="系统升级"><Spin size="large" style={{ display: 'block', margin: '80px auto' }} /></Page>;
  if (status.isError) {
    return <Page title="系统升级">
      <Alert type="error" showIcon message="无法获取升级状态" description={describeError(status.error)}
        action={<Button icon={<ReloadOutlined />} onClick={() => void status.refetch()}>重试</Button>} />
    </Page>;
  }

  const busy = phaseActive(phase);
  return <Page title="系统升级">
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
      <Descriptions bordered size="small" column={2} items={[
        { key: 'current', label: '当前版本', children: <Typography.Text strong copyable={value?.currentVersion !== 'dev'}>{value?.currentVersion || '—'}</Typography.Text> },
        { key: 'repo', label: '升级源', children: value?.repo ? <Typography.Text copyable>{value.repo}</Typography.Text> : '—' },
        {
          key: 'latest', label: '最新版本',
          children: latest
            ? <Space>{latest.version}{latest.upgradable ? <Tag color="processing">可升级</Tag> : <Tag color="success">已最新</Tag>}</Space>
            : <Typography.Text type="secondary">尚未检查</Typography.Text>
        },
        { key: 'published', label: '发布时间', children: latest?.publishedAt ? new Date(latest.publishedAt * 1000).toLocaleString() : '—' }
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
  </Page>;
}
