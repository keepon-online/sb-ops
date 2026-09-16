import {
  CloudServerOutlined,
  DisconnectOutlined,
  LogoutOutlined,
  ReloadOutlined,
  SyncOutlined
} from '@ant-design/icons';
import { App as AntApp, Button, Card, Space, Spin, Tag, Typography } from 'antd';
import { type ReactNode, useCallback, useEffect, useState } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { ThemeSwitcher } from './ThemeSwitcher';
import { useIdentity } from '../hooks/useIdentity';
import { clearSessionAndRedirect, getToken } from '../services/http';

export function AuthGuard({ children }: { children: ReactNode }) {
  const location = useLocation();
  const token = getToken();
  const { data, isLoading, isError, refetch } = useIdentity();

  const handleRefetch = useCallback(() => {
    void refetch();
  }, [refetch]);

  if (!token) {
    return <Navigate to="/login" replace state={{ returnTo: location.pathname }} />;
  }

  if (isLoading) {
    return (
      <div className="login-shell">
        <Spin size="large" />
      </div>
    );
  }

  if (isError || !data) {
    return <ConnectionBarrier onRefetch={handleRefetch} />;
  }

  return <>{children}</>;
}

function ConnectionBarrier({ onRefetch }: { onRefetch: () => void }) {
  const [isOnline, setIsOnline] = useState(() => (typeof navigator !== 'undefined' ? navigator.onLine : true));
  const [retrying, setRetrying] = useState(false);
  const [countdown, setCountdown] = useState(5);
  const { message } = AntApp.useApp();

  const handleRetry = useCallback(async () => {
    if (typeof navigator !== 'undefined' && !navigator.onLine) {
      setIsOnline(false);
      message.warning('检测到本地网络处于离线状态，请先检查 Wi-Fi 或蜂窝网络');
      return;
    }
    setIsOnline(true);
    setRetrying(true);
    try {
      await onRefetch();
      setCountdown(5);
    } finally {
      setRetrying(false);
    }
  }, [message, onRefetch]);

  // 网络状态监听：一旦恢复联网，自动重新拉取验证
  useEffect(() => {
    const handleOnline = () => {
      setIsOnline(true);
      message.info('网络已连接，正在重新接入控制台…');
      void handleRetry();
    };
    const handleOffline = () => {
      setIsOnline(false);
    };
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    return () => {
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);
    };
  }, [handleRetry, message]);

  // 本地在线时，每隔 5 秒自动尝试探测一次重连（应对 VPS 重启/服务瞬时下线自愈），带倒计时感知
  useEffect(() => {
    if (!isOnline) return;
    const timer = window.setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          onRefetch();
          return 5;
        }
        return prev - 1;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, [isOnline, onRefetch]);

  return (
    <div className="login-shell">
      <div className="login-theme-bar">
        <ThemeSwitcher />
      </div>

      <Card className="glass-card login-panel" variant="borderless" style={{ textAlign: 'center' }}>
        <div className="login-header">
          <div
            className="login-icon-badge"
            style={{
              color: isOnline ? 'var(--color-warning)' : 'var(--color-error)',
              background: isOnline ? 'color-mix(in srgb, var(--color-warning) 12%, transparent)' : 'color-mix(in srgb, var(--color-error) 12%, transparent)',
              borderColor: isOnline ? 'color-mix(in srgb, var(--color-warning) 25%, transparent)' : 'color-mix(in srgb, var(--color-error) 25%, transparent)',
              boxShadow: isOnline ? '0 4px 16px color-mix(in srgb, var(--color-warning) 18%, transparent)' : '0 4px 16px color-mix(in srgb, var(--color-error) 18%, transparent)'
            }}
          >
            {isOnline ? <CloudServerOutlined /> : <DisconnectOutlined />}
          </div>
          <div className="login-logo" style={{ fontSize: 22, marginBottom: 8 }}>
            {isOnline ? '无法连接到控制面板服务器' : '本地网络连接已断开'}
          </div>
          <Typography.Text type="secondary" className="login-subtitle" style={{ lineHeight: 1.6, padding: '0 8px' }}>
            {isOnline
              ? '控制服务暂时未能响应，可能正在重启、更新或网络发生波动。已启动的 sing-box 节点仍由系统内核在后台平稳代理。'
              : '检测到当前设备处于离线状态，请检查本地 Wi-Fi 或蜂窝网络。连接恢复后将自动重新接入控制台。'}
          </Typography.Text>
        </div>

        <div style={{ marginBottom: 24 }}>
          <Tag
            color={isOnline ? 'warning' : 'error'}
            icon={isOnline ? <SyncOutlined spin /> : <DisconnectOutlined />}
            style={{ padding: '4px 14px', borderRadius: 12, fontSize: 12 }}
          >
            {isOnline ? `正在等待服务恢复，将在 ${countdown} 秒后自动重试…` : '设备处于离线状态，网络恢复后将自动重试'}
          </Tag>
        </div>

        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Button
            type="primary"
            size="large"
            icon={<ReloadOutlined />}
            loading={retrying}
            onClick={handleRetry}
            block
            className="login-submit-btn"
          >
            立即重试
          </Button>
          <Button
            type="default"
            size="large"
            icon={<LogoutOutlined />}
            onClick={() => clearSessionAndRedirect()}
            block
            style={{ height: 42, borderRadius: 8 }}
          >
            退出并返回登录
          </Button>
        </Space>
      </Card>

      <div className="login-footer">
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Sing-box Ops · 控制面板连接自愈诊断
        </Typography.Text>
      </div>
    </div>
  );
}
