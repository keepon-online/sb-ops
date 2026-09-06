import {
  CloudServerOutlined,
  DisconnectOutlined,
  LogoutOutlined,
  MoonOutlined,
  ReloadOutlined,
  SunOutlined,
  SyncOutlined
} from '@ant-design/icons';
import { Button, Card, Popover, Space, Spin, Tag, Typography } from 'antd';
import { type ReactNode, useEffect, useState } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { type ThemeColor, themeColors, useAppTheme } from '../context/ThemeContext';
import { useIdentity } from '../hooks/useIdentity';
import { clearSessionAndRedirect, getToken } from '../services/http';

export function AuthGuard({ children }: { children: ReactNode }) {
  const location = useLocation();
  const token = getToken();
  const { data, isLoading, isError, refetch } = useIdentity();

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
    return <ConnectionBarrier onRefetch={() => void refetch()} />;
  }

  return <>{children}</>;
}

function ConnectionBarrier({ onRefetch }: { onRefetch: () => void }) {
  const [isOnline, setIsOnline] = useState(() => (typeof navigator !== 'undefined' ? navigator.onLine : true));
  const [retrying, setRetrying] = useState(false);
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();

  const handleRetry = async () => {
    setRetrying(true);
    try {
      await onRefetch();
    } finally {
      setRetrying(false);
    }
  };

  // 网络状态监听：一旦恢复联网，自动重新拉取验证
  useEffect(() => {
    const handleOnline = () => {
      setIsOnline(true);
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
  }, []);

  // 本地在线时，每隔 5 秒自动尝试探测一次重连（应对 VPS 重启/服务瞬时下线自愈）
  useEffect(() => {
    if (!isOnline) return;
    const timer = window.setInterval(() => {
      onRefetch();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [isOnline, onRefetch]);

  return (
    <div className="login-shell">
      <div className="login-theme-bar">
        <Popover
          placement="bottomRight"
          trigger="hover"
          arrow={{ pointAtCenter: true }}
          content={
            <div style={{ padding: '4px 2px', width: 168 }}>
              <div style={{ marginBottom: 10 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 8 }}>
                  主题色彩
                </Typography.Text>
                <Space size={8}>
                  {(['blue', 'purple', 'green', 'orange'] as ThemeColor[]).map((color) => (
                    <button
                      key={color}
                      className="theme-swatch"
                      style={{
                        backgroundColor: themeColors[color][themeMode],
                        borderColor: themeColor === color ? 'var(--text-color)' : 'transparent',
                        width: 22,
                        height: 22
                      }}
                      aria-label={`切换为${color}主题`}
                      title={color}
                      onClick={() => setThemeColor(color)}
                    />
                  ))}
                </Space>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 8, borderTop: '1px solid var(--border-color)' }}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>模式外观</Typography.Text>
                <Button
                  size="small"
                  icon={themeMode === 'light' ? <MoonOutlined /> : <SunOutlined />}
                  onClick={toggleThemeMode}
                >
                  {themeMode === 'light' ? '深色' : '浅色'}
                </Button>
              </div>
            </div>
          }
        >
          <Button
            type="text"
            shape="circle"
            className="login-theme-btn"
            icon={themeMode === 'light' ? <MoonOutlined style={{ fontSize: 16 }} /> : <SunOutlined style={{ fontSize: 16 }} />}
            onClick={toggleThemeMode}
            aria-label="外观与主题"
          />
        </Popover>
      </div>

      <Card className="glass-card login-panel" variant="borderless" style={{ textAlign: 'center' }}>
        <div className="login-header">
          <div
            className="login-icon-badge"
            style={{
              color: isOnline ? 'var(--color-warning)' : 'var(--color-error)',
              background: isOnline ? 'rgba(245, 158, 11, 0.12)' : 'rgba(239, 68, 68, 0.12)',
              borderColor: isOnline ? 'rgba(245, 158, 11, 0.25)' : 'rgba(239, 68, 68, 0.25)',
              boxShadow: isOnline ? '0 4px 16px rgba(245, 158, 11, 0.18)' : '0 4px 16px rgba(239, 68, 68, 0.18)'
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
            {isOnline ? '正在等待服务恢复，将在后台自动探测重连…' : '设备处于离线状态，网络恢复后将自动重试'}
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
