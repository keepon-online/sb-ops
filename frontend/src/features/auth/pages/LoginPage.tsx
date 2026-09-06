import { CloudServerOutlined, LockOutlined, LoginOutlined, MoonOutlined, SunOutlined, UserOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Form, Input, Popover, Space, Spin, Typography } from 'antd';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { useAppTheme, themeColors, type ThemeColor } from '../../../context/ThemeContext';
import { consumeReturnTo, setToken } from '../../../services/http';
import { identityApi } from '../../../services/identity';
import { queryClient } from '../../../services/queryClient';

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const { message } = AntApp.useApp();
  const { themeMode, themeColor, setThemeColor, toggleThemeMode } = useAppTheme();
  const setup = useQuery({ queryKey: ['setup', 'status'], queryFn: identityApi.setupStatus, retry: false });
  const mutation = useMutation({
    mutationFn: (values: { username: string; password: string }) => identityApi.login(values.username, values.password),
    onSuccess: (data) => {
      setToken(data.token);
      queryClient.clear();
      message.success('登录成功');
      const state = location.state as { returnTo?: string } | null;
      navigate(state?.returnTo || consumeReturnTo() || '/overview', { replace: true });
    },
    onError: (err: Error) => message.error(err.message)
  });

  if (setup.isLoading) return <div className="centered"><Spin /></div>;
  if (setup.data && !setup.data.initialized) return <Navigate to="/setup" replace />;

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

      <Card className="glass-card login-panel" variant="borderless">
        <div className="login-header">
          <div className="login-icon-badge">
            <CloudServerOutlined />
          </div>
          <div className="login-logo">Sing-box Ops</div>
          <Typography.Text type="secondary" className="login-subtitle">
            轻量级 sing-box 节点与网络运维控制台
          </Typography.Text>
        </div>

        <Form layout="vertical" onFinish={(values) => mutation.mutate(values)} className="login-form">
          <Form.Item
            name="username"
            label="用户名"
            initialValue="admin"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input
              size="large"
              prefix={<UserOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="username"
              placeholder="请输入用户名"
            />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              size="large"
              prefix={<LockOutlined style={{ color: 'var(--text-secondary)' }} />}
              autoComplete="current-password"
              placeholder="请输入密码"
            />
          </Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            size="large"
            loading={mutation.isPending}
            icon={<LoginOutlined />}
            block
            className="login-submit-btn"
          >
            登录
          </Button>
        </Form>
      </Card>

      <div className="login-footer">
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Sing-box Ops · 单 VPS 自托管控制面板
        </Typography.Text>
      </div>
    </div>
  );
}
